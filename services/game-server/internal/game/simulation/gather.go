package simulation

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/movement"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/task"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/pathfinding"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// La tarea de recolección GATHER_WOOD en la simulación (SPEC-ECON-WOOD §4.2).
//
//	TO_RESOURCE (tramo) → WORKING (workMs) → TO_DEPOSIT (tramo) → depósito → TO_RESOURCE …
//
// Los tramos son movimientos normales: la fase 3 los avanza como cualquier
// otro, y al llegar le pregunta a la tarea qué sigue. La fase 5 despierta a las
// unidades cuyo trabajo venció. Toda la línea temporal se ancla a instantes
// absolutos —la llegada exacta de un tramo, el vencimiento exacto del trabajo—
// y no al tick que los observa: por eso un reinicio no la desplaza
// (INV-ECON-005).

// GatherWood pone a una unidad a recolectar madera en un tile de bosque.
type GatherWood struct {
	PlayerID  uuid.UUID
	SessionID uuid.UUID
	RequestID string
	UnitID    int64
	Target    world.Tile
}

func (GatherWood) commandType() string { return protocol.TypeUnitGather }

// TaskPersistFailed es la compensación de una transición de tarea que no se
// escribió (ADR-023 §6). Rev es la revisión de la tarea que escribía: si la RAM
// ya escribió otra después, aquélla manda y no hay nada que compensar.
type TaskPersistFailed struct {
	UnitID int64
	Rev    uint64
}

func (TaskPersistFailed) commandType() string { return "task.persist_failed" }

// taskEntry es una tarea con su revisión en la RAM.
type taskEntry struct {
	task.Task
	rev uint64
}

// setTask registra o sustituye la tarea de una unidad y sube su revisión.
func (s *State) setTask(t task.Task) uint64 {
	s.taskRev++
	s.tasks[t.UnitID] = &taskEntry{Task: t, rev: s.taskRev}
	return s.taskRev
}

// Task devuelve la tarea de una unidad (lectura y tests).
func (s *State) Task(unitID int64) (task.Task, bool) {
	e, ok := s.tasks[unitID]
	if !ok {
		return task.Task{}, false
	}
	return e.Task, true
}

// TaskCount es el número de tareas vivas.
func (s *State) TaskCount() int { return len(s.tasks) }

// ownerPresent dice si la ciudad admite un viaje nuevo (RN-ECON-005,
// INV-ECON-006): está ONLINE y su dueño tiene una sesión viva o está dentro
// del margen de reconexión. La segunda condición cubre el arranque, en el que
// una ciudad que estaba ONLINE se queda ONLINE sin nadie conectado
// (docs/specs/presence.md §7.6).
func (s *Simulation) ownerPresent(c *city.City) bool {
	if c.PresenceState != city.PresenceOnline {
		return false
	}
	if len(s.sessions[c.OwnerPlayerID]) > 0 {
		return true
	}
	_, enMargen := s.disconnectedAt[c.OwnerPlayerID]
	return enMargen
}

// cityOfUnit es la ciudad de origen de una unidad (units.city_id): la de su
// depósito (RN-ECON-004).
func (s *Simulation) cityOfUnit(u *unit.Unit) (*city.City, bool) {
	if u.CityID == nil {
		return nil, false
	}
	return s.state.City(*u.CityID)
}

// depositTile es el primer tile transitable del anillo de nacimiento de la
// ciudad, con el criterio de los aldeanos iniciales (RN-ECON-004).
func (s *Simulation) depositTile(c *city.City) (world.Tile, bool) {
	return c.FirstSpawnTile(s.state.World().IsWalkable)
}

// ─────────────────────────────────────────────────────────────
// unit.gather
// ─────────────────────────────────────────────────────────────

func (s *Simulation) handleGatherWood(cmd GatherWood) {
	nowMs := s.deps.Clock.NowMs()
	u, ok := s.state.Unit(cmd.UnitID)
	if !ok {
		s.economyError(cmd.PlayerID, cmd.RequestID, protocol.CodeUnitNotFound, "la unidad no existe")
		return
	}
	// RN-ECON-001, en el orden de unit.move: la propiedad antes que nada.
	if err := task.CanGather(u, cmd.PlayerID); err != nil {
		s.economyError(cmd.PlayerID, cmd.RequestID, gatherErrorCode(err), err.Error())
		return
	}
	if s.state.IsEntering(u.ID) {
		s.economyError(cmd.PlayerID, cmd.RequestID, protocol.CodeUnitNotMovable, "la unidad está entrando en una ciudad")
		return
	}
	w := s.state.World()
	if err := task.ValidTarget(w, cmd.Target); err != nil {
		s.economyError(cmd.PlayerID, cmd.RequestID, gatherErrorCode(err), err.Error())
		return
	}
	if s.deps.Economy == nil {
		s.economyError(cmd.PlayerID, cmd.RequestID, protocol.CodeInternalError, "la economía no está disponible")
		return
	}
	def, err := unit.Lookup(u.Type)
	if err != nil {
		s.economyError(cmd.PlayerID, cmd.RequestID, protocol.CodeInternalError, "tipo de unidad no catalogado")
		return
	}

	// RN-ECON-002: el tile de trabajo es el propio bosque, y la ruta se rechaza
	// igual que la de un unit.move.
	origin := s.authoritativePosition(u, nowMs)
	var path movement.TimedPath
	if origin != cmd.Target {
		path, err = s.legPath(origin, cmd.Target, def)
		if err != nil {
			s.economyError(cmd.PlayerID, cmd.RequestID, pathErrorCode(err), err.Error())
			return
		}
	}

	// RN-ECON-006: la orden sustituye la tarea que hubiera, y su carga se pierde.
	// El tramo en curso se cancela como lo cancela un unit.move.
	s.dropTask(u)
	s.cancelActiveMovement(u, movement.ReasonReplaced, nowMs)

	wasHidden := u.Status == unit.StatusHidden
	t := task.NewGather(u.ID, cmd.Target)
	if path == nil {
		// Ya está sobre el bosque: empieza a trabajar ahora.
		s.arriveAtResource(u, t, nowMs, nil, wasHidden)
		return
	}
	s.startLeg(u, t, path, cmd.Target, origin, nowMs, nil, wasHidden)
}

// gatherErrorCode traduce los rechazos de unit.gather al catálogo.
func gatherErrorCode(err error) string {
	switch {
	case errors.Is(err, task.ErrNotOwned):
		return protocol.CodeUnitNotOwned
	case errors.Is(err, unit.ErrDead):
		return protocol.CodeUnitDead
	case errors.Is(err, unit.ErrGarrisoned):
		return protocol.CodeUnitGarrisoned
	case errors.Is(err, task.ErrOutOfBounds):
		return protocol.CodeTargetOutOfBounds
	case errors.Is(err, task.ErrNotResource):
		return protocol.CodeInvalidTarget
	default:
		// Un tipo que no recolecta, o un estado que no admite órdenes.
		return protocol.CodeUnitNotMovable
	}
}

// legPath calcula la polilínea de un tramo con el mismo A* y los mismos topes
// que un unit.move.
func (s *Simulation) legPath(from, to world.Tile, def unit.Definition) (movement.TimedPath, error) {
	w := s.state.World()
	start := time.Now() //lint:reloj-real mide la duración del A* para la métrica; no afecta al resultado
	tiles, err := s.deps.Pathfinder.FindPath(context.Background(), w, from, to, pathfinding.Options{
		MaxNodes:    s.deps.PathMaxNodes,
		MaxDistance: s.deps.PathMaxDistance,
	})
	s.LastPathfindingCalls++
	s.LastPathfindingTime += time.Since(start) //lint:reloj-real ídem
	if err != nil {
		return nil, err
	}
	return movement.BuildTimedPath(tiles, w, def.BaseMsPerTile)
}

// ─────────────────────────────────────────────────────────────
// Transiciones
// ─────────────────────────────────────────────────────────────

// startLeg abre un tramo de la tarea que empieza en el instante `startMs` y lo
// escribe junto al cierre del anterior (`finish`, opcional). `wasHidden` es si
// la unidad estaba oculta: recolectar revela, como moverse (RN-SAFE-013).
func (s *Simulation) startLeg(u *unit.Unit, t task.Task, path movement.TimedPath, target, origin world.Tile,
	startMs int64, finish *MovementFinish, wasHidden bool) {
	nowMs := s.deps.Clock.NowMs()
	m := movement.New(u.ID, path, target, startMs)
	m.ID = s.state.NewMovementID()
	s.state.SetMovement(m)
	u.Status = unit.StatusMoving
	s.state.MarkDirty(u.ID)
	rev := s.state.setTask(t)

	start := *m
	tk := t
	s.submitTask(TaskWrite{UnitID: u.ID, Finish: finish, Start: &start, Status: unit.StatusMoving, At: origin, Task: &tk}, rev)

	cx, cy := s.state.World().ChunkOf(origin.X, origin.Y)
	if wasHidden {
		s.deps.Broadcaster.BroadcastChunkExcept(cx, cy, u.PlayerID, protocol.TypeEntitySpawn,
			protocol.EntitySpawnPayload{Unit: s.UnitView(u, nowMs)})
	}
	s.deps.Broadcaster.BroadcastChunk(cx, cy, protocol.TypeUnitMovementStarted,
		protocol.UnitMovementStartedPayload{UnitID: u.ID, Movement: toActiveMovement(m)})
}

// onLegArrived es la fase 3 para el tramo de una tarea que acaba de llegar: se
// anuncia como cualquier llegada y la tarea decide qué sigue, con el cierre
// del tramo en la misma transacción que la transición.
func (s *Simulation) onLegArrived(u *unit.Unit, t task.Task, movementID int64, at world.Tile, arrivedAtMs int64) {
	cx, cy := s.state.World().ChunkOf(at.X, at.Y)
	s.deps.Broadcaster.BroadcastChunk(cx, cy, protocol.TypeUnitMovementCompleted,
		protocol.UnitMovementCompletedPayload{
			UnitID: u.ID, MovementID: movementID, FinalPosition: protocol.Tile{X: at.X, Y: at.Y},
		})
	finish := &MovementFinish{MovementID: movementID, Status: movement.StatusCompleted, UnitID: u.ID, At: at}

	switch {
	case t.Phase == task.PhaseToResource && at == t.Target:
		s.arriveAtResource(u, t, arrivedAtMs, finish, false)
	case t.Phase == task.PhaseToDeposit:
		s.arriveAtDeposit(u, t, arrivedAtMs, finish)
	default:
		// Un tramo al recurso que no acabó en el recurso: no hay ruta que lo
		// produzca. La tarea termina donde está la unidad.
		s.endTask(u, task.EndCancelled, finish)
	}
}

// arriveAtResource es la llegada al bosque en el instante `arrivedAtMs`:
// TO_RESOURCE → WORKING durante workMs (RN-ECON-003). Si el trabajo ya venció
// —tras un reinicio—, la fase 5 de este mismo tick lo despierta.
func (s *Simulation) arriveAtResource(u *unit.Unit, t task.Task, arrivedAtMs int64, finish *MovementFinish, wasHidden bool) {
	def, err := unit.Lookup(u.Type)
	if err != nil {
		s.endTask(u, task.EndCancelled, finish)
		return
	}
	next, err := t.StartWork(arrivedAtMs, def.GatherWorkMs)
	if err != nil {
		s.endTask(u, task.EndCancelled, finish)
		return
	}
	nowMs := s.deps.Clock.NowMs()
	u.Status = unit.StatusWorking
	s.state.MarkDirty(u.ID)
	rev := s.state.setTask(next)
	tk := next
	s.submitTask(TaskWrite{UnitID: u.ID, Finish: finish, Status: unit.StatusWorking, At: u.Tile(), Task: &tk}, rev)

	cx, cy := s.state.World().ChunkOf(u.X, u.Y)
	if wasHidden {
		s.deps.Broadcaster.BroadcastChunkExcept(cx, cy, u.PlayerID, protocol.TypeEntitySpawn,
			protocol.EntitySpawnPayload{Unit: s.UnitView(u, nowMs)})
	}
	status := string(u.Status)
	s.deps.Broadcaster.BroadcastChunk(cx, cy, protocol.TypeEntityUpdate,
		protocol.EntityUpdatePayload{ID: u.ID, Status: &status})
}

// leaveResource es el fin del trabajo en `t.PhaseUntilMs`: carga y sale hacia
// el depósito (WORKING → TO_DEPOSIT).
func (s *Simulation) leaveResource(u *unit.Unit, t task.Task) {
	def, err := unit.Lookup(u.Type)
	if err != nil {
		s.endTask(u, task.EndCancelled, nil)
		return
	}
	c, ok := s.cityOfUnit(u)
	if !ok {
		s.endTask(u, task.EndNoPath, nil)
		return
	}
	depot, ok := s.depositTile(c)
	if !ok {
		s.endTask(u, task.EndNoPath, nil)
		return
	}
	leftAtMs := t.PhaseUntilMs
	next, err := t.FinishWork(def.GatherCarry)
	if err != nil {
		s.endTask(u, task.EndCancelled, nil)
		return
	}
	origin := u.Tile()
	if origin == depot {
		// El bosque es el propio tile de depósito: el viaje es instantáneo.
		s.arriveAtDeposit(u, next, leftAtMs, nil)
		return
	}
	path, err := s.legPath(origin, depot, def)
	if err != nil {
		s.endTask(u, task.EndNoPath, nil)
		return
	}
	s.startLeg(u, next, path, depot, origin, leftAtMs, nil, false)

	carga := next.Carrying
	cx, cy := s.state.World().ChunkOf(origin.X, origin.Y)
	s.deps.Broadcaster.BroadcastChunk(cx, cy, protocol.TypeEntityUpdate,
		protocol.EntityUpdatePayload{ID: u.ID, Carrying: &carga})
}

// arriveAtDeposit es la llegada al depósito en `arrivedAtMs`: entrega la carga
// (RN-ECON-004) y, si el dueño está presente, empieza el viaje siguiente
// anclado a ese mismo instante; si no, la tarea termina y la unidad queda IDLE
// en el depósito (RN-ECON-005, INV-ECON-006).
func (s *Simulation) arriveAtDeposit(u *unit.Unit, t task.Task, arrivedAtMs int64, finish *MovementFinish) {
	next, delivered, err := t.Deposit()
	if err != nil {
		s.endTask(u, task.EndCancelled, finish)
		return
	}
	c, ok := s.cityOfUnit(u)
	if !ok {
		s.endTask(u, task.EndCancelled, finish)
		return
	}
	s.deposit(c, delivered)
	cero := int32(0)
	cx, cy := s.state.World().ChunkOf(u.X, u.Y)
	s.deps.Broadcaster.BroadcastChunk(cx, cy, protocol.TypeEntityUpdate,
		protocol.EntityUpdatePayload{ID: u.ID, Carrying: &cero})

	// INV-ECON-006: ningún viaje nuevo con el dueño ausente.
	if !s.ownerPresent(c) {
		s.endTask(u, task.EndOwnerAbsent, finish)
		return
	}
	def, err := unit.Lookup(u.Type)
	if err != nil {
		s.endTask(u, task.EndCancelled, finish)
		return
	}
	origin := u.Tile()
	if origin == next.Target {
		s.arriveAtResource(u, next, arrivedAtMs, finish, false)
		return
	}
	path, err := s.legPath(origin, next.Target, def)
	if err != nil {
		s.endTask(u, task.EndNoPath, finish)
		return
	}
	s.startLeg(u, next, path, next.Target, origin, arrivedAtMs, finish, false)
}

// endTask termina la tarea de una unidad que está parada (sin tramo activo):
// queda IDLE donde está y la base lo sabe en una transacción, con el cierre
// del tramo que acaba de llegar si lo hay.
func (s *Simulation) endTask(u *unit.Unit, reason task.EndReason, finish *MovementFinish) {
	delete(s.state.tasks, u.ID)
	prev := u.Status
	u.Status = unit.StatusIdle
	s.state.MarkDirty(u.ID)
	s.submitTask(TaskWrite{UnitID: u.ID, Finish: finish, Status: unit.StatusIdle, At: u.Tile()}, 0)

	if prev != unit.StatusIdle && prev != unit.StatusMoving {
		// Una unidad que trabajaba deja de hacerlo a la vista de todos. La que
		// llegaba ya se vio llegar con unit.movement.completed.
		status := string(u.Status)
		cx, cy := s.state.World().ChunkOf(u.X, u.Y)
		s.deps.Broadcaster.BroadcastChunk(cx, cy, protocol.TypeEntityUpdate,
			protocol.EntityUpdatePayload{ID: u.ID, Status: &status})
	}
	s.deps.Log.Debug("tarea terminada", "unit_id", u.ID, "reason", string(reason))
}

// dropTask olvida en la RAM la tarea de una unidad, con su carga, porque otra
// orden la sustituye o la cancela (RN-ECON-003, RN-ECON-006). No escribe nada:
// la escritura de esa orden borra la fila en su transacción. Devuelve si había.
func (s *Simulation) dropTask(u *unit.Unit) bool {
	e, ok := s.state.tasks[u.ID]
	if !ok {
		return false
	}
	delete(s.state.tasks, u.ID)
	if e.Carrying > 0 {
		cero := int32(0)
		cx, cy := s.state.World().ChunkOf(u.X, u.Y)
		s.deps.Broadcaster.BroadcastChunk(cx, cy, protocol.TypeEntityUpdate,
			protocol.EntityUpdatePayload{ID: u.ID, Carrying: &cero})
	}
	return true
}

// stopWorking detiene a una unidad WORKING por una orden del jugador: pierde la
// tarea y la carga y queda IDLE en el bosque; la base lo sabe con un cierre sin
// movimiento, que borra la tarea en su transacción.
func (s *Simulation) stopWorking(u *unit.Unit) {
	if u.Status != unit.StatusWorking {
		return
	}
	s.dropTask(u)
	u.Status = unit.StatusIdle
	s.state.MarkDirty(u.ID)
	s.submitFinish(MovementFinish{UnitID: u.ID, At: u.Tile()}, u.ID)
	status := string(u.Status)
	cx, cy := s.state.World().ChunkOf(u.X, u.Y)
	s.deps.Broadcaster.BroadcastChunk(cx, cy, protocol.TypeEntityUpdate,
		protocol.EntityUpdatePayload{ID: u.ID, Status: &status})
}

// ─────────────────────────────────────────────────────────────
// Fase 5
// ─────────────────────────────────────────────────────────────

// advanceTasks despierta, en orden de unidad, a las que terminaron de trabajar.
func (s *Simulation) advanceTasks(nowMs int64) {
	if len(s.state.tasks) == 0 {
		return
	}
	ids := make([]int64, 0, len(s.state.tasks))
	for id, e := range s.state.tasks {
		if e.Due(nowMs) {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		e, ok := s.state.tasks[id]
		if !ok || !e.Due(nowMs) {
			continue
		}
		u, ok := s.state.Unit(id)
		if !ok {
			delete(s.state.tasks, id)
			continue
		}
		if u.Status != unit.StatusWorking {
			// INV-ECON-003: una tarea en WORKING exige la unidad WORKING. No
			// hay ruta que llegue aquí; se termina la tarea sin carga.
			s.invariantViolation("INV-ECON-003", "ALTO", "REPAIR", u.ID,
				"tarea en WORKING sobre una unidad "+string(u.Status)+": se termina")
			delete(s.state.tasks, id)
			continue
		}
		s.leaveResource(u, e.Task)
	}
}

// ─────────────────────────────────────────────────────────────
// Persistencia
// ─────────────────────────────────────────────────────────────

// submitTask encola una transición de tarea, ordenada con las demás escrituras
// de su unidad. `rev` es la revisión de la tarea que escribe (0 si la borra).
func (s *Simulation) submitTask(w TaskWrite, rev uint64) {
	if s.deps.Economy == nil {
		return
	}
	store := s.deps.Economy
	s.deps.Persister.Submit(Job{
		Name: JobTaskWrite,
		Key:  UnitKey(w.UnitID),
		Run:  func(ctx context.Context) error { return store.PersistTask(ctx, w) },
		OnPermanentFailure: func(err error) {
			s.deps.Log.Error("no se pudo escribir la transición de la tarea; la tarea se termina",
				"err", err, "unit_id", w.UnitID)
			s.reinject(TaskPersistFailed{UnitID: w.UnitID, Rev: rev})
		},
	})
}

// handleTaskPersistFailed termina la tarea cuya transición no llegó a la base,
// si sigue siendo la misma revisión: la cancelación escribe un estado nuevo y
// completo. Si la tarea ya terminó, o la RAM escribió otra transición después,
// no hay nada que compensar.
func (s *Simulation) handleTaskPersistFailed(cmd TaskPersistFailed) {
	e, ok := s.state.tasks[cmd.UnitID]
	u, uok := s.state.Unit(cmd.UnitID)
	if !uok {
		return
	}
	if cmd.Rev == 0 {
		// Se perdió el fin de una tarea: la unidad sigue parada; el volcado la
		// escribe IDLE y el cierre sin movimiento borra la fila.
		if !ok {
			s.submitFinish(MovementFinish{UnitID: u.ID, At: u.Tile()}, u.ID)
		}
		return
	}
	if !ok || e.rev != cmd.Rev {
		return
	}
	nowMs := s.deps.Clock.NowMs()
	if u.Status == unit.StatusWorking {
		s.stopWorking(u)
		return
	}
	s.cancelActiveMovement(u, movement.ReasonServer, nowMs)
}
