package simulation

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/empires-online/empires-online/services/game-server/internal/clock"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/movement"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/pathfinding"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// Repositories son las escrituras durables que necesita la simulación.
//
// Se declaran como interfaz AQUÍ, en el consumidor, no en el paquete de
// PostgreSQL: el dominio define lo que necesita y la infraestructura se adapta.
type Repositories interface {
	PersistMovementStart(ctx context.Context, m *movement.Movement) error
	PersistMovementFinish(ctx context.Context, movementID int64, status movement.Status) error
	PersistUnitPositions(ctx context.Context, units []*unit.Unit) error
	PersistCityPresence(ctx context.Context, cityID int64, state city.PresenceState, at time.Time) error
}

// Deps son las dependencias inyectadas de la simulación.
type Deps struct {
	Clock       clock.Clock
	Pathfinder  pathfinding.Pathfinder
	Broadcaster Broadcaster
	Persister   Persister
	Repos       Repositories
	Log         *slog.Logger

	// ProtectionCooldown es el tiempo desde la desconexión hasta PROTECTED.
	ProtectionCooldown time.Duration
	// DisconnectGrace es el margen de reconexión antes de degradar a OFFLINE_PENDING.
	DisconnectGrace time.Duration
	// PathMaxNodes y PathMaxDistance acotan cada consulta de pathfinding.
	PathMaxNodes    int
	PathMaxDistance int32
}

// Simulation aplica comandos y hace avanzar el mundo.
//
// Todos sus métodos deben invocarse desde la goroutine del game loop, y sólo
// desde ella. No hay mutexes aquí a propósito: el aislamiento es por diseño, no
// por candados.
type Simulation struct {
	state *State
	deps  Deps

	// sessions cuenta las sesiones vivas por jugador. Un jugador con dos pestañas
	// abiertas sigue estando presente cuando cierra una.
	sessions map[uuid.UUID]int
	// disconnectedAt registra cuándo se quedó sin sesiones un jugador, para
	// aplicar el margen de reconexión antes de degradar su ciudad.
	disconnectedAt map[uuid.UUID]time.Time

	// Contadores de observabilidad del último tick.
	LastPathfindingCalls int
	LastPathfindingTime  time.Duration
}

// New crea la simulación sobre un estado ya hidratado.
func New(state *State, deps Deps) *Simulation {
	if deps.Log == nil {
		deps.Log = slog.Default()
	}
	if deps.Broadcaster == nil {
		deps.Broadcaster = NoopBroadcaster{}
	}
	if deps.Persister == nil {
		deps.Persister = NoopPersister{}
	}
	return &Simulation{
		state:          state,
		deps:           deps,
		sessions:       make(map[uuid.UUID]int),
		disconnectedAt: make(map[uuid.UUID]time.Time),
	}
}

// State expone el mundo (lectura desde el hilo del loop, y tests).
func (s *Simulation) State() *State { return s.state }

// Apply despacha un comando.
func (s *Simulation) Apply(cmd Command) {
	switch c := cmd.(type) {
	case MoveUnit:
		s.handleMoveUnit(c)
	case CancelMovement:
		s.handleCancelMovement(c)
	case PlayerConnected:
		s.handlePlayerConnected(c)
	case PlayerDisconnected:
		s.handlePlayerDisconnected(c)
	case RequestSnapshot:
		s.handleRequestSnapshot(c)
	case IntroducePlayer:
		s.handleIntroducePlayer(c)
	default:
		s.deps.Log.Warn("comando desconocido descartado", "type", cmd.commandType())
	}
}

// ─────────────────────────────────────────────────────────────
// Movimiento
// ─────────────────────────────────────────────────────────────

// handleMoveUnit valida y ejecuta una orden de movimiento.
//
// El orden de las validaciones es deliberado: la propiedad se comprueba antes que
// cualquier otra cosa, para que un jugador no pueda deducir el estado de unidades
// ajenas a partir de qué error recibe.
func (s *Simulation) handleMoveUnit(cmd MoveUnit) {
	nowMs := s.deps.Clock.NowMs()

	u, ok := s.state.Unit(cmd.UnitID)
	if !ok {
		s.rejectMove(cmd, protocol.CodeUnitNotFound, "la unidad no existe")
		return
	}
	if err := u.EnsureOwnedBy(cmd.PlayerID); err != nil {
		s.rejectMove(cmd, protocol.CodeUnitNotOwned, "la unidad no te pertenece")
		return
	}
	if err := u.EnsureCanMove(); err != nil {
		s.rejectMove(cmd, moveErrorCode(err), err.Error())
		return
	}

	w := s.state.World()
	if !w.TileInBounds(cmd.Target) {
		s.rejectMove(cmd, protocol.CodeTargetOutOfBounds, "el destino está fuera del mundo")
		return
	}
	if !w.IsWalkable(cmd.Target.X, cmd.Target.Y) {
		s.rejectMove(cmd, protocol.CodeTargetNotWalkable, "el destino no es transitable")
		return
	}

	// El origen es la posición AUTORITATIVA actual. Si la unidad ya se está
	// moviendo, es el tile que ocupa en este instante según su polilínea — no la
	// posición consolidada en la base de datos, que puede ir por detrás.
	origin := s.authoritativePosition(u, nowMs)
	if origin == cmd.Target {
		// Ya está allí. Se acepta sin crear movimiento: es idempotente y evita
		// polilíneas degeneradas de un solo punto.
		s.cancelActiveMovement(u, movement.ReasonCancelledByPlayer, nowMs)
		s.deps.Broadcaster.SendToPlayer(cmd.PlayerID, protocol.TypeUnitMoveAccepted, cmd.RequestID,
			protocol.UnitMoveAcceptedPayload{UnitID: u.ID, MovementID: 0})
		return
	}

	def, err := unit.Lookup(u.Type)
	if err != nil {
		s.rejectMove(cmd, protocol.CodeInternalError, "tipo de unidad no catalogado")
		return
	}

	start := time.Now()
	tiles, err := s.deps.Pathfinder.FindPath(context.Background(), w, origin, cmd.Target, pathfinding.Options{
		MaxNodes:    s.deps.PathMaxNodes,
		MaxDistance: s.deps.PathMaxDistance,
	})
	s.LastPathfindingCalls++
	s.LastPathfindingTime += time.Since(start)

	if err != nil {
		s.rejectMove(cmd, pathErrorCode(err), err.Error())
		return
	}

	path, err := movement.BuildTimedPath(tiles, w, def.BaseMsPerTile)
	if err != nil {
		s.deps.Log.Error("no se pudo construir la polilínea", "err", err, "unit_id", u.ID)
		s.rejectMove(cmd, protocol.CodeInternalError, "no se pudo construir la ruta")
		return
	}

	// Cancelar el movimiento anterior ANTES de registrar el nuevo, para que en
	// ningún instante existan dos movimientos activos de la misma unidad.
	s.cancelActiveMovement(u, movement.ReasonReplaced, nowMs)

	m := movement.New(u.ID, path, cmd.Target, nowMs)
	s.state.SetMovement(m)
	u.Status = unit.StatusMoving
	s.state.MarkDirty(u.ID)

	// Escritura durable diferida. Si acaba fallando de forma permanente, la
	// compensación detiene la unidad: el mundo no puede quedarse con un movimiento
	// que la base de datos nunca conoció.
	s.deps.Persister.Submit(Job{
		Name: "movement.start",
		Run:  func(ctx context.Context) error { return s.deps.Repos.PersistMovementStart(ctx, m) },
		OnPermanentFailure: func(err error) {
			s.deps.Log.Error("no se pudo persistir el inicio del movimiento; se detiene la unidad",
				"err", err, "unit_id", m.UnitID)
		},
	})

	s.deps.Broadcaster.SendToPlayer(cmd.PlayerID, protocol.TypeUnitMoveAccepted, cmd.RequestID,
		protocol.UnitMoveAcceptedPayload{UnitID: u.ID, MovementID: m.ID})

	cx, cy := w.ChunkOf(origin.X, origin.Y)
	s.deps.Broadcaster.BroadcastChunk(cx, cy, protocol.TypeUnitMovementStarted,
		protocol.UnitMovementStartedPayload{UnitID: u.ID, Movement: toActiveMovement(m)})
}

func (s *Simulation) handleCancelMovement(cmd CancelMovement) {
	nowMs := s.deps.Clock.NowMs()

	u, ok := s.state.Unit(cmd.UnitID)
	if !ok {
		s.deps.Broadcaster.SendToPlayer(cmd.PlayerID, protocol.TypeSystemError, cmd.RequestID,
			protocol.SystemErrorPayload{Code: protocol.CodeUnitNotFound, Message: "la unidad no existe"})
		return
	}
	if err := u.EnsureOwnedBy(cmd.PlayerID); err != nil {
		s.deps.Broadcaster.SendToPlayer(cmd.PlayerID, protocol.TypeSystemError, cmd.RequestID,
			protocol.SystemErrorPayload{Code: protocol.CodeUnitNotOwned, Message: "la unidad no te pertenece"})
		return
	}
	s.cancelActiveMovement(u, movement.ReasonCancelledByPlayer, nowMs)
}

// cancelActiveMovement detiene el movimiento en curso, si lo hay.
//
// La unidad queda SIEMPRE sobre el último tile alcanzado, nunca entre dos: el
// mundo no admite posiciones fraccionarias (INV-MOVE-006).
func (s *Simulation) cancelActiveMovement(u *unit.Unit, reason movement.CancelReason, nowMs int64) {
	m, ok := s.state.Movement(u.ID)
	if !ok {
		return
	}
	stoppedAt := m.PositionAt(nowMs)
	s.state.MoveUnitTo(u, stoppedAt)
	s.state.ClearMovement(u.ID)
	u.Status = unit.StatusIdle
	s.state.MarkDirty(u.ID)

	movementID := m.ID
	s.deps.Persister.Submit(Job{
		Name: "movement.cancel",
		Run: func(ctx context.Context) error {
			return s.deps.Repos.PersistMovementFinish(ctx, movementID, movement.StatusCancelled)
		},
	})

	cx, cy := s.state.World().ChunkOf(stoppedAt.X, stoppedAt.Y)
	s.deps.Broadcaster.BroadcastChunk(cx, cy, protocol.TypeUnitMovementCancelled,
		protocol.UnitMovementCancelledPayload{
			UnitID:     u.ID,
			MovementID: movementID,
			StoppedAt:  protocol.Tile{X: stoppedAt.X, Y: stoppedAt.Y},
			Reason:     string(reason),
		})
}

// AdvanceMovements hace avanzar todos los movimientos hasta el instante dado.
//
// Es la fase 3 del tick. No recalcula rutas ni consulta la base de datos: sólo
// evalúa la función posición(polilínea, tiempo) para cada movimiento activo.
func (s *Simulation) AdvanceMovements(nowMs int64) {
	type arrival struct {
		unitID     int64
		movementID int64
		at         world.Tile
	}
	var arrivals []arrival

	s.state.EachMovement(func(m *movement.Movement) bool {
		u, ok := s.state.Unit(m.UnitID)
		if !ok {
			// La unidad desapareció bajo un movimiento vivo: se limpia.
			arrivals = append(arrivals, arrival{unitID: m.UnitID, movementID: m.ID})
			return true
		}

		pos := m.PositionAt(nowMs)
		if pos != u.Tile() {
			s.state.MoveUnitTo(u, pos)
			s.state.MarkDirty(u.ID)

			cx, cy := s.state.World().ChunkOf(pos.X, pos.Y)
			x, y := pos.X, pos.Y
			s.deps.Broadcaster.BroadcastChunk(cx, cy, protocol.TypeEntityUpdate,
				protocol.EntityUpdatePayload{ID: u.ID, X: &x, Y: &y})
		}

		if m.HasArrived(nowMs) {
			arrivals = append(arrivals, arrival{unitID: m.UnitID, movementID: m.ID, at: pos})
		}
		return true
	})

	for _, a := range arrivals {
		s.state.ClearMovement(a.unitID)

		if u, ok := s.state.Unit(a.unitID); ok {
			u.Status = unit.StatusIdle
			s.state.MarkDirty(u.ID)

			cx, cy := s.state.World().ChunkOf(a.at.X, a.at.Y)
			s.deps.Broadcaster.BroadcastChunk(cx, cy, protocol.TypeUnitMovementCompleted,
				protocol.UnitMovementCompletedPayload{
					UnitID:        a.unitID,
					MovementID:    a.movementID,
					FinalPosition: protocol.Tile{X: a.at.X, Y: a.at.Y},
				})
		}

		movementID := a.movementID
		s.deps.Persister.Submit(Job{
			Name: "movement.complete",
			Run: func(ctx context.Context) error {
				return s.deps.Repos.PersistMovementFinish(ctx, movementID, movement.StatusCompleted)
			},
		})
	}
}

// authoritativePosition devuelve dónde está realmente una unidad ahora mismo.
func (s *Simulation) authoritativePosition(u *unit.Unit, nowMs int64) world.Tile {
	if m, ok := s.state.Movement(u.ID); ok {
		return m.PositionAt(nowMs)
	}
	return u.Tile()
}

// AuthoritativePosition expone la posición autoritativa (snapshots y tests).
func (s *Simulation) AuthoritativePosition(u *unit.Unit, nowMs int64) world.Tile {
	return s.authoritativePosition(u, nowMs)
}

// ─────────────────────────────────────────────────────────────
// Presencia y protección
// ─────────────────────────────────────────────────────────────

func (s *Simulation) handlePlayerConnected(cmd PlayerConnected) {
	s.sessions[cmd.PlayerID]++
	delete(s.disconnectedAt, cmd.PlayerID)
	s.setPresence(cmd.PlayerID, city.PresenceOnline)
}

func (s *Simulation) handlePlayerDisconnected(cmd PlayerDisconnected) {
	if n := s.sessions[cmd.PlayerID]; n > 1 {
		// Le quedan otras sesiones abiertas: sigue presente.
		s.sessions[cmd.PlayerID] = n - 1
		return
	}
	delete(s.sessions, cmd.PlayerID)
	// No se degrada inmediatamente: primero corre el margen de reconexión, para
	// que un corte de red de tres segundos no cambie el estado del mundo.
	s.disconnectedAt[cmd.PlayerID] = s.deps.Clock.Now()
}

// ProcessTimers es la fase 5 del tick: presencia, protección y demás plazos.
//
// Es enteramente aritmética sobre estado en RAM. No consulta la base de datos:
// las ciudades ya están cargadas y sus marcas temporales también.
func (s *Simulation) ProcessTimers(now time.Time) {
	// 1. Margen de reconexión agotado: ONLINE -> OFFLINE_PENDING.
	for playerID, since := range s.disconnectedAt {
		if s.sessions[playerID] > 0 {
			delete(s.disconnectedAt, playerID)
			continue
		}
		if now.Sub(since) < s.deps.DisconnectGrace {
			continue
		}
		s.setPresence(playerID, city.PresenceOfflinePending)
		delete(s.disconnectedAt, playerID)
	}

	// 2. Cooldown de seguridad vencido: OFFLINE_PENDING -> PROTECTED.
	s.state.EachCity(func(c *city.City) bool {
		if city.ShouldEngageProtection(c.PresenceState, c.LastOfflineAt, s.deps.ProtectionCooldown, now) {
			s.applyPresence(c, city.PresenceProtected, now)
		}
		return true
	})
}

func (s *Simulation) setPresence(playerID uuid.UUID, state city.PresenceState) {
	c, ok := s.state.CityOf(playerID)
	if !ok {
		return
	}
	s.applyPresence(c, state, s.deps.Clock.Now())
}

func (s *Simulation) applyPresence(c *city.City, state city.PresenceState, now time.Time) {
	if c.PresenceState == state {
		return
	}
	if !city.CanTransition(c.PresenceState, state) {
		s.deps.Log.Warn("transición de presencia rechazada",
			"city_id", c.ID, "from", c.PresenceState, "to", state)
		return
	}

	c.PresenceState = state
	switch state {
	case city.PresenceOnline:
		t := now
		c.LastOnlineAt = &t
		c.ProtectionUntil = nil
	case city.PresenceOfflinePending:
		t := now
		c.LastOfflineAt = &t
	}

	cityID, at := c.ID, now
	s.deps.Persister.Submit(Job{
		Name: "city.presence",
		Run: func(ctx context.Context) error {
			return s.deps.Repos.PersistCityPresence(ctx, cityID, state, at)
		},
	})

	presence := string(state)
	cx, cy := s.state.World().ChunkOf(c.CenterX, c.CenterY)
	s.deps.Broadcaster.BroadcastChunk(cx, cy, protocol.TypeCityUpdate,
		protocol.CityUpdatePayload{ID: c.ID, PresenceState: &presence})
}

// ─────────────────────────────────────────────────────────────
// Volcado periódico
// ─────────────────────────────────────────────────────────────

// FlushDirty encola el volcado de las unidades marcadas como sucias.
func (s *Simulation) FlushDirty() int {
	dirty := s.state.DrainDirty()
	if len(dirty) == 0 {
		return 0
	}
	// Se copia el estado relevante: los punteros del mundo siguen mutando en el
	// loop mientras el worker escribe, y escribir desde otra goroutine sobre esos
	// objetos sería una carrera de datos.
	snapshot := make([]*unit.Unit, len(dirty))
	for i, u := range dirty {
		cp := *u
		snapshot[i] = &cp
	}
	s.deps.Persister.Submit(Job{
		Name: "units.flush",
		Run:  func(ctx context.Context) error { return s.deps.Repos.PersistUnitPositions(ctx, snapshot) },
	})
	return len(snapshot)
}

// ─────────────────────────────────────────────────────────────
// Utilidades
// ─────────────────────────────────────────────────────────────

func (s *Simulation) rejectMove(cmd MoveUnit, code, message string) {
	s.deps.Broadcaster.SendToPlayer(cmd.PlayerID, protocol.TypeUnitMoveRejected, cmd.RequestID,
		protocol.UnitMoveRejectedPayload{UnitID: cmd.UnitID, Code: code, Message: message})
}

func moveErrorCode(err error) string {
	switch {
	case errors.Is(err, unit.ErrDead):
		return protocol.CodeUnitDead
	case errors.Is(err, unit.ErrGarrisoned):
		return protocol.CodeUnitGarrisoned
	default:
		return protocol.CodeUnitNotMovable
	}
}

func pathErrorCode(err error) string {
	switch {
	case errors.Is(err, pathfinding.ErrTargetOutOfBounds):
		return protocol.CodeTargetOutOfBounds
	case errors.Is(err, pathfinding.ErrTargetNotWalkable):
		return protocol.CodeTargetNotWalkable
	case errors.Is(err, pathfinding.ErrPathTooLong):
		return protocol.CodePathTooLong
	case errors.Is(err, pathfinding.ErrPathNotFound):
		return protocol.CodePathNotFound
	default:
		return protocol.CodeInternalError
	}
}

func toActiveMovement(m *movement.Movement) protocol.ActiveMovement {
	path := make([]protocol.Waypoint, len(m.Path))
	for i, w := range m.Path {
		path[i] = protocol.Waypoint{X: w.X, Y: w.Y, TMs: w.TMs}
	}
	return protocol.ActiveMovement{
		MovementID:    m.ID,
		Path:          path,
		StartTimeMs:   m.StartTimeMs,
		ArrivalTimeMs: m.ArrivalTimeMs,
		Target:        protocol.Tile{X: m.Target.X, Y: m.Target.Y},
	}
}

// ToActiveMovement expone la conversión para los snapshots.
func ToActiveMovement(m *movement.Movement) protocol.ActiveMovement { return toActiveMovement(m) }
