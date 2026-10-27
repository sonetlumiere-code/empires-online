package simulation

import (
	"fmt"
	"log/slog"
	"sort"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/movement"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/task"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
)

// RecoveryResult resume lo que ocurrió al rehidratar el mundo.
type RecoveryResult struct {
	Units   int
	Cities  int
	Resumed int
	Arrived int
	Failed  int
	// Reconciled son las unidades que llegaron MOVING sin un movimiento ACTIVE
	// que lo respalde y se dejaron IDLE (INV-UNIT-005).
	Reconciled int
	// FinishedMovements son los movimientos que hay que cerrar en la base de datos:
	// terminaron mientras el proceso estaba caído, o su ruta ya no es válida.
	FinishedMovements []FinishedMovement
}

// FinishedMovement es un movimiento que debe cerrarse tras la recuperación.
type FinishedMovement struct {
	MovementID int64
	Status     movement.Status
	// UnitID y At son la unidad y el tile donde queda, para escribirlos en la
	// misma transacción que el cierre (ADR-013 §2.3). UnitID 0: no hay unidad.
	UnitID int64
	At     world.Tile
}

// MovementFinish es el cierre durable de un movimiento: la fila de
// unit_movements y la posición final de su unidad, IDLE, en una transacción.
type MovementFinish struct {
	MovementID int64
	Status     movement.Status
	UnitID     int64
	At         world.Tile
}

// Hydrate reconstruye el mundo en memoria tras un arranque o un reinicio.
//
// Éste es el momento en el que se cobra la decisión de persistir el movimiento
// como polilínea temporizada. Cada movimiento se resuelve en O(1):
//
//   - Si su hora de llegada ya pasó mientras el servidor estaba caído, la unidad
//     se coloca DIRECTAMENTE en el destino y el movimiento se cierra. El mundo
//     siguió existiendo aunque el proceso no.
//   - Si sigue en curso, la posición actual se evalúa sobre la polilínea y el
//     movimiento continúa exactamente donde le tocaba, sin saltos ni recálculos.
//   - Si la ruta ya no es válida (el mapa cambió, la fila está corrupta), el
//     movimiento se marca FAILED y la unidad se queda en su última posición
//     consolidada. Nunca se teletransporta a nadie por un dato dudoso.
//
// Ver ../../../../docs/testing/integration-tests.md (test de recuperación).
func Hydrate(
	state *State,
	units []*unit.Unit,
	cities []*city.City,
	movements []*movement.Movement,
	nowMs int64,
	log *slog.Logger,
) RecoveryResult {
	res := RecoveryResult{}

	for _, c := range cities {
		state.AddCity(c)
		res.Cities++
	}
	for _, u := range units {
		state.AddUnit(u)
		res.Units++
	}

	w := state.World()

	for _, m := range movements {
		u, ok := state.Unit(m.UnitID)
		if !ok {
			// Movimiento huérfano: su unidad ya no existe.
			res.FinishedMovements = append(res.FinishedMovements,
				FinishedMovement{MovementID: m.ID, Status: movement.StatusFailed})
			res.Failed++
			continue
		}
		state.SetMovementIDFloor(m.ID)

		if u.Status == unit.StatusGarrisoned {
			// Entrar en guarnición cancela el movimiento en la misma transacción
			// (GarrisonRepo.Enter): un ACTIVE aquí es una fila corrupta. Manda la
			// guarnición, que es la tabla del hecho (INV-UNIT-007); reanudar el
			// movimiento sacaría la unidad de la ciudad sin pasar por la salida.
			res.FinishedMovements = append(res.FinishedMovements,
				FinishedMovement{MovementID: m.ID, Status: movement.StatusFailed})
			res.Failed++
			log.Error("invariant_violation",
				"inv_id", "INV-UNIT-005", "severity", "CRITICO", "policy", "REPAIR",
				"entity", fmt.Sprintf("unit:%d", u.ID),
				"detail", fmt.Sprintf("movimiento %d ACTIVE de una unidad GARRISONED al rehidratar: se cierra FAILED y la unidad sigue guarnecida", m.ID))
			continue
		}

		if err := movement.Validate(m.Path, w); err != nil {
			log.Warn("polilínea inválida al rehidratar; el movimiento se descarta",
				"movement_id", m.ID, "unit_id", m.UnitID, "err", err)
			u.Status = unit.StatusIdle
			state.MarkDirty(u.ID)
			res.FinishedMovements = append(res.FinishedMovements,
				FinishedMovement{MovementID: m.ID, Status: movement.StatusFailed, UnitID: u.ID, At: u.Tile()})
			res.Failed++
			continue
		}

		// INV-MOVE-005: arrival_time_ms y el destino están desnormalizados en la
		// fila; la fuente es la polilínea. Una fila que no cuadra con ella haría
		// llegar la unidad antes de tiempo, o nunca, o anunciar al cliente un
		// destino que la ruta no alcanza. Se corrige en memoria desde la
		// polilínea, que ya se validó arriba.
		if want := m.StartTimeMs + m.Path.DurationMs(); m.ArrivalTimeMs != want || m.Target != m.Destination() {
			log.Error("invariant_violation",
				"inv_id", "INV-MOVE-005", "severity", "ALTO", "policy", "REPAIR",
				"entity", fmt.Sprintf("movement:%d", m.ID),
				"detail", fmt.Sprintf("la fila dice llegada %d y destino %s; la polilínea, %d y %s: manda la polilínea",
					m.ArrivalTimeMs, m.Target, want, m.Destination()))
			m.ArrivalTimeMs, m.Target = want, m.Destination()
		}

		if m.HasArrived(nowMs) {
			if e, conTarea := state.tasks[u.ID]; conTarea && e.Phase.IsLeg() {
				// INV-ECON-005: el tramo de una tarea que llegó durante la caída
				// no se cierra aquí. Se deja activo, con la unidad ya en el
				// destino, para que la fase 3 del primer tick procese la llegada
				// con su instante exacto y la tarea ancle a él su fase siguiente.
				state.MoveUnitTo(u, m.Destination())
				u.Status = unit.StatusMoving
				state.SetMovement(m)
				res.Resumed++
				continue
			}
			dest := m.Destination()
			state.MoveUnitTo(u, dest)
			u.Status = unit.StatusIdle
			state.MarkDirty(u.ID)
			res.FinishedMovements = append(res.FinishedMovements,
				FinishedMovement{MovementID: m.ID, Status: movement.StatusCompleted, UnitID: u.ID, At: dest})
			res.Arrived++
			continue
		}

		pos := m.PositionAt(nowMs)
		state.MoveUnitTo(u, pos)
		u.Status = unit.StatusMoving
		state.SetMovement(m)
		res.Resumed++
	}

	// Una unidad puede llegar MOVING sin movimiento que la respalde: el volcado
	// periódico la escribió MOVING a mitad de trayecto, el movimiento se cerró y
	// el proceso cayó antes del siguiente volcado. Sin reconciliar, quedaría
	// MOVING en RAM hasta su próxima orden. Se queda donde está —la última
	// posición consolidada— e IDLE, y se vuelca en el primer volcado.
	state.EachUnit(func(u *unit.Unit) bool {
		if u.Status != unit.StatusMoving {
			return true
		}
		if _, ok := state.Movement(u.ID); ok {
			return true
		}
		u.Status = unit.StatusIdle
		state.MarkDirty(u.ID)
		res.Reconciled++
		log.Error("invariant_violation",
			"inv_id", "INV-UNIT-005", "severity", "CRITICO", "policy", "REPAIR",
			"entity", fmt.Sprintf("unit:%d", u.ID),
			"detail", "MOVING sin movimiento ACTIVE al rehidratar: queda IDLE en su última posición consolidada")
		return true
	})

	return res
}

// ReconcileTasks termina, al arrancar, las tareas que no pueden reanudarse, y
// devuelve lo que hay que escribir para que la base lo sepa. Va después de
// Hydrate y de ClearFootprints, que pueden haber detenido tramos.
//
//   - Una tarea en un tramo sin movimiento activo: el tramo se cerró o se
//     detuvo (una muralla nueva, una polilínea inválida).
//   - Una tarea en WORKING cuya unidad no está WORKING o no está sobre su
//     bosque: alguien la movió (INV-ECON-003).
//
// La unidad queda IDLE donde está y la tarea se borra (INV-ECON-005: lo que no
// puede terminar a su hora no se inventa). Las de unidades que no están vivas
// ya las borró la validación de carga.
func ReconcileTasks(state *State, log *slog.Logger) []TaskWrite {
	ids := make([]int64, 0, len(state.tasks))
	for id := range state.tasks {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	var writes []TaskWrite
	for _, id := range ids {
		e := state.tasks[id]
		u, ok := state.Unit(id)
		if !ok {
			delete(state.tasks, id)
			continue
		}
		_, enMarcha := state.Movement(id)
		var motivo string
		switch {
		case e.Phase.IsLeg() && !enMarcha:
			motivo = "tramo sin movimiento activo"
		case e.Phase == task.PhaseWorking && (u.Status != unit.StatusWorking || u.Tile() != e.Target):
			motivo = fmt.Sprintf("WORKING sobre una unidad %s en %s, no en %s", u.Status, u.Tile(), e.Target)
		default:
			continue
		}
		delete(state.tasks, id)
		if enMarcha {
			// Un movimiento que no es de la tarea: la unidad sigue con él y sólo
			// se borra la fila (INV-PERSIST-004: no se escribe su posición).
			writes = append(writes, TaskWrite{UnitID: u.ID, KeepUnit: true})
		} else {
			u.Status = unit.StatusIdle
			state.MarkDirty(u.ID)
			writes = append(writes, TaskWrite{UnitID: u.ID, Status: u.Status, At: u.Tile()})
		}
		log.Warn("tarea que no puede reanudarse: termina al arrancar",
			"unit_id", u.ID, "phase", string(e.Phase), "detail", motivo)
	}
	return writes
}
