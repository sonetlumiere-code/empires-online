package simulation

import (
	"context"
	"errors"
	"sort"

	"github.com/google/uuid"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/garrison"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/movement"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// Guarnición en la simulación: los comandos unit.garrison y unit.ungarrison.
//
// La entrada se decide en dos fases (RN-GARR-020): el tick comprueba lo que
// está en la RAM y la transacción de la cola comprueba el tratado, que sólo
// vive en PostgreSQL; su resultado vuelve al loop como comando. La salida no
// depende de nada externo: la decide y la aplica el tick, y se compensa si no
// se puede escribir.
//
// Spec: ../../../../docs/specs/garrison.md · ADR-017.

// GarrisonStore ejecuta la entrada y la salida de forma transaccional. Lo
// implementa postgres.GarrisonRepo.
type GarrisonStore interface {
	// Enter vuelve a decidir con el tratado ACTUAL y, si autoriza, escribe la
	// entrada. Devuelve el error de dominio de garrison si la rechaza.
	Enter(ctx context.Context, u *unit.Unit, c *city.City, requester uuid.UUID, tick uint64) (int64, error)
	// Leave escribe la salida al tile dado.
	Leave(ctx context.Context, u *unit.Unit, x, y, chunkX, chunkY int32, tick uint64) error
}

// GarrisonUnit pide guarnecer una unidad en una ciudad.
type GarrisonUnit struct {
	PlayerID  uuid.UUID
	SessionID uuid.UUID
	RequestID string
	UnitID    int64
	CityID    int64
}

func (GarrisonUnit) commandType() string { return protocol.TypeUnitGarrison }

// UngarrisonUnit pide sacar una unidad de su guarnición.
type UngarrisonUnit struct {
	PlayerID  uuid.UUID
	SessionID uuid.UUID
	RequestID string
	UnitID    int64
}

func (UngarrisonUnit) commandType() string { return protocol.TypeUnitUngarrison }

// GarrisonEntryResolved lo devuelve la cola de persistencia con el resultado de
// la transacción de entrada. Code vacío: la entrada está escrita.
type GarrisonEntryResolved struct {
	PlayerID  uuid.UUID
	RequestID string
	UnitID    int64
	CityID    int64
	Code      string
}

func (GarrisonEntryResolved) commandType() string { return "garrison.entry_resolved" }

// GarrisonExitFailed es la compensación de una salida que no se pudo escribir
// (ADR-017 §2.3): la base sigue teniendo la unidad dentro.
type GarrisonExitFailed struct {
	UnitID int64
	CityID int64
	// From es el tile que tenía la unidad antes de salir: el que la base conoce.
	From world.Tile
}

func (GarrisonExitFailed) commandType() string { return "garrison.exit_failed" }

// exitRequest es una salida a la espera de tile (RN-GARR-014). Guarda el
// requestId para contestar cuando la unidad salga.
type exitRequest struct {
	PlayerID  uuid.UUID
	RequestID string
	CityID    int64
}

// ─────────────────────────────────────────────────────────────
// Estado
// ─────────────────────────────────────────────────────────────

// SetGarrisons instala el índice unidad → ciudad anfitriona leído de
// `garrisons` al arrancar.
func (s *State) SetGarrisons(rows map[int64]int64) {
	s.garrisonedIn = make(map[int64]int64, len(rows))
	for unitID, cityID := range rows {
		s.garrisonedIn[unitID] = cityID
	}
}

// GarrisonCity devuelve la ciudad que aloja a una unidad guarnecida.
func (s *State) GarrisonCity(unitID int64) (int64, bool) {
	id, ok := s.garrisonedIn[unitID]
	return id, ok
}

// IsEntering indica si la entrada de una unidad espera su transacción.
func (s *State) IsEntering(unitID int64) bool {
	_, ok := s.entering[unitID]
	return ok
}

// keepDirty devuelve una unidad al conjunto sucio sin apuntarla al
// ocultamiento: el volcado la aplaza, no cambió nada.
func (s *State) keepDirty(id int64) { s.unitDirty[id] = struct{}{} }

// ─────────────────────────────────────────────────────────────
// Entrada
// ─────────────────────────────────────────────────────────────

func (s *Simulation) handleGarrisonUnit(cmd GarrisonUnit) {
	u, ok := s.state.Unit(cmd.UnitID)
	if !ok {
		s.garrisonError(cmd.PlayerID, cmd.RequestID, protocol.CodeUnitNotFound, "la unidad no existe")
		return
	}
	// La propiedad antes que nada: un jugador no puede deducir el estado de una
	// unidad ajena por el error que recibe (RN-GARR-001).
	if err := u.EnsureOwnedBy(cmd.PlayerID); err != nil {
		s.garrisonError(cmd.PlayerID, cmd.RequestID, protocol.CodeUnitNotOwned, "la unidad no te pertenece")
		return
	}
	if s.state.IsEntering(u.ID) {
		s.garrisonError(cmd.PlayerID, cmd.RequestID, protocol.CodeUnitNotMovable, "la unidad ya está entrando en una ciudad")
		return
	}

	var c *city.City
	if found, ok := s.state.City(cmd.CityID); ok {
		c = found
	}
	// Sin tratados: en ciudad propia autoriza y en ciudad ajena devuelve
	// ErrTreatyRequired, que aquí significa «falta la comprobación que sólo
	// puede hacer la base». Cualquier otro rechazo se responde ya.
	if err := garrison.CanEnter(u, c, cmd.PlayerID, nil); err != nil && !errors.Is(err, garrison.ErrTreatyRequired) {
		s.garrisonError(cmd.PlayerID, cmd.RequestID, garrisonErrorCode(err), err.Error())
		return
	}
	if s.deps.Garrisons == nil {
		s.garrisonError(cmd.PlayerID, cmd.RequestID, protocol.CodeInternalError, "la guarnición no está disponible")
		return
	}

	s.state.entering[u.ID] = struct{}{}

	// El worker recibe COPIAS: los objetos de la RAM son del loop.
	copiaUnidad, copiaCiudad := *u, *c
	tick, store := s.state.Tick(), s.deps.Garrisons
	resolved := GarrisonEntryResolved{PlayerID: cmd.PlayerID, RequestID: cmd.RequestID, UnitID: u.ID, CityID: c.ID}
	s.deps.Persister.Submit(Job{
		Name: JobGarrisonEnter,
		Key:  UnitKey(u.ID),
		Run: func(ctx context.Context) error {
			unidad := copiaUnidad // cada intento parte del estado del tick
			_, err := store.Enter(ctx, &unidad, &copiaCiudad, resolved.PlayerID, tick)
			if err == nil {
				s.reinject(resolved)
				return nil
			}
			if code := garrisonErrorCode(err); code != protocol.CodeInternalError {
				// Un rechazo de dominio no se reintenta: es la respuesta.
				r := resolved
				r.Code = code
				s.reinject(r)
				return nil
			}
			return err
		},
		OnPermanentFailure: func(err error) {
			s.deps.Log.Error("no se pudo escribir la entrada en guarnición",
				"err", err, "unit_id", resolved.UnitID, "city_id", resolved.CityID)
			r := resolved
			r.Code = protocol.CodeInternalError
			s.reinject(r)
		},
	})
}

// handleGarrisonEntryResolved aplica el resultado de la transacción de entrada.
func (s *Simulation) handleGarrisonEntryResolved(cmd GarrisonEntryResolved) {
	delete(s.state.entering, cmd.UnitID)

	if cmd.Code != "" {
		s.garrisonError(cmd.PlayerID, cmd.RequestID, cmd.Code, "no se pudo entrar en la ciudad")
		return
	}
	u, ok := s.state.Unit(cmd.UnitID)
	if !ok {
		return
	}

	// La base ya tiene la unidad dentro: la RAM la sigue. No se marca sucia; el
	// estado que volcaría es el que la transacción acaba de escribir.
	u.Status = unit.StatusGarrisoned
	s.state.garrisonedIn[u.ID] = cmd.CityID

	status := string(u.Status)
	anfitriona := &cmd.CityID
	s.deps.Broadcaster.SendToPlayer(u.PlayerID, protocol.TypeEntityUpdate, cmd.RequestID,
		protocol.EntityUpdatePayload{ID: u.ID, Status: &status, GarrisonCityID: &anfitriona})

	// Los demás la pierden de vista, el anfitrión incluido (INV-GARR-006).
	cx, cy := s.state.World().ChunkOf(u.X, u.Y)
	s.deps.Broadcaster.BroadcastChunkExcept(cx, cy, u.PlayerID, protocol.TypeEntityDespawn,
		protocol.EntityDespawnPayload{ID: u.ID, Reason: protocol.DespawnGarrisoned})
}

// ─────────────────────────────────────────────────────────────
// Salida
// ─────────────────────────────────────────────────────────────

func (s *Simulation) handleUngarrisonUnit(cmd UngarrisonUnit) {
	u, ok := s.state.Unit(cmd.UnitID)
	if !ok {
		s.garrisonError(cmd.PlayerID, cmd.RequestID, protocol.CodeUnitNotFound, "la unidad no existe")
		return
	}
	if err := u.EnsureOwnedBy(cmd.PlayerID); err != nil {
		s.garrisonError(cmd.PlayerID, cmd.RequestID, protocol.CodeUnitNotOwned, "la unidad no te pertenece")
		return
	}
	if s.state.IsEntering(u.ID) {
		s.garrisonError(cmd.PlayerID, cmd.RequestID, protocol.CodeUnitNotMovable, "la unidad está entrando en una ciudad")
		return
	}
	if _, saliendo := s.state.pendingExits[u.ID]; saliendo {
		s.garrisonError(cmd.PlayerID, cmd.RequestID, protocol.CodeUnitNotMovable, "la unidad ya está saliendo")
		return
	}
	if err := garrison.CanLeave(u, cmd.PlayerID); err != nil {
		s.garrisonError(cmd.PlayerID, cmd.RequestID, protocol.CodeUnitNotMovable, "la unidad no está guarnecida")
		return
	}
	hostID, ok := s.state.GarrisonCity(u.ID)
	c, cityOK := s.state.City(hostID)
	if !ok || !cityOK {
		s.invariantViolation("INV-GARR-002", "CRITICO", "REJECT", u.ID,
			"unidad GARRISONED sin ciudad anfitriona en la RAM: no se puede sacar")
		s.garrisonError(cmd.PlayerID, cmd.RequestID, protocol.CodeInternalError, "no se encuentra la guarnición")
		return
	}

	req := exitRequest{PlayerID: cmd.PlayerID, RequestID: cmd.RequestID, CityID: c.ID}
	if err := s.exit(u, c, req); errors.Is(err, garrison.ErrNoReentryTile) {
		// RN-GARR-014: no es un error del jugador. Se reintenta en la fase 5 y
		// la respuesta llega cuando salga.
		s.state.pendingExits[u.ID] = req
		s.deps.Log.Warn("sin tile de reentrada: la salida queda pendiente",
			"unit_id", u.ID, "city_id", c.ID)
	}
}

// exit saca la unidad al primer tile libre del anillo y lo difunde.
func (s *Simulation) exit(u *unit.Unit, c *city.City, req exitRequest) error {
	w := s.state.World()
	// Las unidades no ocupan tile en el MVP: sólo cuentan terreno y murallas.
	tile, err := garrison.ReentryTile(w, c, nil)
	if err != nil {
		return err
	}
	from := u.Tile()
	nowMs := s.deps.Clock.NowMs()

	prev := u.Status
	u.Status = unit.StatusIdle
	delete(s.state.garrisonedIn, u.ID)
	// Si el tile de reentrada cae en otro chunk, la transición sólo alcanza al
	// dueño: los terceros no la veían guarnecida y la reciben por el
	// entity.spawn de abajo, una sola vez.
	s.moveUnit(u, tile, prev, nowMs)
	s.state.MarkDirty(u.ID)

	status, x, y := string(u.Status), tile.X, tile.Y
	var ninguna *int64
	s.deps.Broadcaster.SendToPlayer(u.PlayerID, protocol.TypeEntityUpdate, req.RequestID,
		protocol.EntityUpdatePayload{ID: u.ID, X: &x, Y: &y, Status: &status, GarrisonCityID: &ninguna})
	cx, cy := w.ChunkOf(tile.X, tile.Y)
	s.deps.Broadcaster.BroadcastChunkExcept(cx, cy, u.PlayerID, protocol.TypeEntitySpawn,
		protocol.EntitySpawnPayload{Unit: s.UnitView(u, nowMs)})

	if s.deps.Garrisons == nil {
		return nil
	}
	copia, tick, store, cityID := *u, s.state.Tick(), s.deps.Garrisons, c.ID
	s.deps.Persister.Submit(Job{
		Name: JobGarrisonLeave,
		Key:  UnitKey(u.ID),
		Run: func(ctx context.Context) error {
			unidad := copia
			return store.Leave(ctx, &unidad, x, y, cx, cy, tick)
		},
		OnPermanentFailure: func(err error) {
			s.deps.Log.Error("no se pudo escribir la salida de la guarnición; la unidad vuelve a la ciudad",
				"err", err, "unit_id", copia.ID, "city_id", cityID)
			s.reinject(GarrisonExitFailed{UnitID: copia.ID, CityID: cityID, From: from})
		},
	})
	return nil
}

// handleGarrisonExitFailed devuelve a la ciudad una unidad cuya salida la base
// nunca conoció. Si ya echó a andar, se detiene: la base la tiene dentro.
func (s *Simulation) handleGarrisonExitFailed(cmd GarrisonExitFailed) {
	u, ok := s.state.Unit(cmd.UnitID)
	if !ok || u.Status == unit.StatusGarrisoned {
		return
	}
	nowMs := s.deps.Clock.NowMs()
	s.cancelActiveMovement(u, movement.ReasonServer, nowMs)

	// Los terceros la conocen en el chunk donde está ahora.
	cx, cy := s.state.World().ChunkOf(u.X, u.Y)
	s.deps.Broadcaster.BroadcastChunkExcept(cx, cy, u.PlayerID, protocol.TypeEntityDespawn,
		protocol.EntityDespawnPayload{ID: u.ID, Reason: protocol.DespawnGarrisoned})

	// Primero el estado, después el tile: si el regreso cruza de chunk, el
	// dueño recibe la unidad ya GARRISONED en su ciudad, y los terceros nada.
	prev := u.Status
	u.Status = unit.StatusGarrisoned
	s.state.garrisonedIn[u.ID] = cmd.CityID
	s.moveUnit(u, cmd.From, prev, nowMs)
	s.state.MarkDirty(u.ID)

	status, x, y := string(u.Status), cmd.From.X, cmd.From.Y
	anfitriona := &cmd.CityID
	s.deps.Broadcaster.SendToPlayer(u.PlayerID, protocol.TypeEntityUpdate, "",
		protocol.EntityUpdatePayload{ID: u.ID, X: &x, Y: &y, Status: &status, GarrisonCityID: &anfitriona})
}

// retryPendingExits reintenta, en orden de unidad, las salidas sin tile.
func (s *Simulation) retryPendingExits() {
	if len(s.state.pendingExits) == 0 {
		return
	}
	ids := make([]int64, 0, len(s.state.pendingExits))
	for id := range s.state.pendingExits {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	for _, id := range ids {
		req := s.state.pendingExits[id]
		u, ok := s.state.Unit(id)
		c, cityOK := s.state.City(req.CityID)
		if !ok || !cityOK || u.Status != unit.StatusGarrisoned {
			delete(s.state.pendingExits, id)
			continue
		}
		if err := s.exit(u, c, req); err == nil {
			delete(s.state.pendingExits, id)
		}
	}
}

// ─────────────────────────────────────────────────────────────
// Utilidades
// ─────────────────────────────────────────────────────────────

func (s *Simulation) garrisonError(playerID uuid.UUID, requestID, code, message string) {
	s.deps.Broadcaster.SendToPlayer(playerID, protocol.TypeSystemError, requestID,
		protocol.SystemErrorPayload{Code: code, Message: message})
}

// garrisonErrorCode traduce un error de dominio de garrison al catálogo. Lo que
// no es de dominio es un fallo interno.
func garrisonErrorCode(err error) string {
	switch {
	case errors.Is(err, garrison.ErrNotOwned):
		return protocol.CodeUnitNotOwned
	case errors.Is(err, garrison.ErrDead):
		return protocol.CodeUnitDead
	case errors.Is(err, garrison.ErrAlreadyGarrisoned):
		return protocol.CodeUnitGarrisoned
	case errors.Is(err, garrison.ErrNotIdle):
		return protocol.CodeUnitNotMovable
	case errors.Is(err, garrison.ErrCityNotFound):
		return protocol.CodeCityNotFound
	case errors.Is(err, garrison.ErrNotAdjacent):
		return protocol.CodeInvalidTarget
	case errors.Is(err, garrison.ErrTreatyRequired):
		return protocol.CodeTreatyRequired
	default:
		return protocol.CodeInternalError
	}
}
