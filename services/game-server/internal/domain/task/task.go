// Package task modela lo que una unidad hace a lo largo de varios tramos.
//
// Hay una tarea por recurso de terreno, GATHER_WOOD y GATHER_STONE (ADR-030
// §2), y las dos encadenan los mismos tramos de movimiento y esperas:
//
//	TO_RESOURCE (movimiento) → WORKING (workMs) → TO_DEPOSIT (movimiento) → depósito → TO_RESOURCE …
//
// Los tramos son movimientos normales (docs/specs/movement.md): la tarea sólo
// decide qué hacer cuando uno termina o cuando vence su espera. Este paquete es
// la máquina de estados, pura: cada transición devuelve la tarea siguiente y no
// escribe nada. No es un patrón general para cualquier acción de varios tramos
// (ADR-023, «Fuera de esta decisión»).
//
// Specs: ../../../../../docs/specs/economy-wood.md §4.2 y economy-stone.md §4.2.
package task

import (
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/deposit"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
)

// Kind identifica el tipo de tarea.
type Kind string

const (
	// KindGatherWood es la recolección de madera en un bosque.
	KindGatherWood Kind = "GATHER_WOOD"
	// KindGatherStone es la recolección de piedra en una colina.
	KindGatherStone Kind = "GATHER_STONE"
)

// Valid indica si el tipo pertenece al conjunto del esquema
// (unit_tasks_kind_valid).
func (k Kind) Valid() bool { return k == KindGatherWood || k == KindGatherStone }

// Layer es la capa de recurso que gasta la tarea: la del terreno de su tile de
// trabajo (ADR-030 §2).
func (k Kind) Layer() (deposit.Layer, bool) {
	switch k {
	case KindGatherWood:
		return deposit.Forest, true
	case KindGatherStone:
		return deposit.Hill, true
	default:
		return deposit.Layer{}, false
	}
}

// KindFor es la tarea que gasta una capa.
func KindFor(l deposit.Layer) Kind {
	if l.Terrain == deposit.Hill.Terrain {
		return KindGatherStone
	}
	return KindGatherWood
}

// Phase es la fase de una tarea.
type Phase string

const (
	PhaseToResource Phase = "TO_RESOURCE"
	PhaseWorking    Phase = "WORKING"
	PhaseToDeposit  Phase = "TO_DEPOSIT"
)

// Valid indica si la fase pertenece al conjunto del esquema
// (unit_tasks_phase_valid).
func (p Phase) Valid() bool {
	return p == PhaseToResource || p == PhaseWorking || p == PhaseToDeposit
}

// IsLeg indica si la fase es un tramo de movimiento.
func (p Phase) IsLeg() bool { return p == PhaseToResource || p == PhaseToDeposit }

// EndReason es por qué terminó una tarea (el evento «tarea terminada», §6).
type EndReason string

const (
	// EndCancelled: una orden de movimiento, unit.cancel_move o un tramo que
	// se detuvo por otra causa (RN-ECON-003).
	EndCancelled EndReason = "CANCELLED"
	// EndOwnerAbsent: la ciudad no está ONLINE o su dueño no está presente al
	// terminar un viaje (RN-ECON-005, INV-ECON-006).
	EndOwnerAbsent EndReason = "OWNER_ABSENT"
	// EndReplaced: otro unit.gather la sustituyó (RN-ECON-006).
	EndReplaced EndReason = "REPLACED"
	// EndNoPath: el tramo siguiente no tiene ruta.
	EndNoPath EndReason = "NO_PATH"
	// EndForestDepleted: el bosque se agotó y no hay otro tile con madera
	// cerca (RN-ECON-015).
	EndForestDepleted EndReason = "FOREST_DEPLETED"
	// EndHillDepleted: la colina se agotó y no hay otro tile con piedra cerca
	// (RN-ECON-022).
	EndHillDepleted EndReason = "HILL_DEPLETED"
)

// EndDepleted es el motivo con el que termina una tarea cuyo recurso se agotó.
func EndDepleted(l deposit.Layer) EndReason {
	if l.Terrain == deposit.Hill.Terrain {
		return EndHillDepleted
	}
	return EndForestDepleted
}

// Errores de dominio.
var (
	ErrNotOwned      = errors.New("la unidad no pertenece al jugador")
	ErrNotGatherer   = errors.New("la unidad no recolecta")
	ErrOutOfBounds   = errors.New("el destino está fuera del mundo")
	ErrNotResource   = errors.New("el destino no es un recurso que se pueda recolectar")
	ErrBadTransition = errors.New("transición de tarea no permitida")
	ErrInvalidTask   = errors.New("tarea inválida")
)

// Task es la tarea de una unidad. Es un valor: cada transición devuelve otra.
type Task struct {
	UnitID int64
	Kind   Kind
	// Target es el tile de trabajo: el propio tile FOREST o HILL (RN-ECON-002,
	// RN-ECON-019).
	Target world.Tile
	Phase  Phase
	// PhaseUntilMs es cuándo vence la fase WORKING; 0 en las demás
	// (unit_tasks_working_has_deadline).
	PhaseUntilMs int64
	// Carrying es lo que lleva encima: se pierde si la tarea se cancela
	// (RN-ECON-003, decisión Q5).
	Carrying  int32
	TripsDone int32
}

// NewGather empieza una recolección de madera: el primer tramo va al recurso.
func NewGather(unitID int64, target world.Tile) Task {
	return NewGatherOf(unitID, KindGatherWood, target)
}

// NewGatherOf empieza una recolección del tipo dado.
func NewGatherOf(unitID int64, kind Kind, target world.Tile) Task {
	return Task{UnitID: unitID, Kind: kind, Target: target, Phase: PhaseToResource}
}

// StartWork es la llegada al recurso: TO_RESOURCE → WORKING durante `workMs`
// contados desde el instante EXACTO de llegada, no desde el tick que la
// observa (RN-ECON-003, INV-ECON-005).
func (t Task) StartWork(arrivedAtMs, workMs int64) (Task, error) {
	if t.Phase != PhaseToResource {
		return t, fmt.Errorf("%w: llegar al recurso desde %s", ErrBadTransition, t.Phase)
	}
	if workMs <= 0 {
		return t, fmt.Errorf("%w: workMs %d", ErrInvalidTask, workMs)
	}
	t.Phase, t.PhaseUntilMs = PhaseWorking, arrivedAtMs+workMs
	return t, nil
}

// Due indica si la espera de WORKING venció en `nowMs`.
func (t Task) Due(nowMs int64) bool { return t.Phase == PhaseWorking && t.PhaseUntilMs <= nowMs }

// FinishWork es el fin de la espera: WORKING → TO_DEPOSIT cargando `carry`.
func (t Task) FinishWork(carry int32) (Task, error) {
	if t.Phase != PhaseWorking {
		return t, fmt.Errorf("%w: terminar de trabajar desde %s", ErrBadTransition, t.Phase)
	}
	if carry <= 0 {
		return t, fmt.Errorf("%w: carga %d", ErrInvalidTask, carry)
	}
	t.Phase, t.PhaseUntilMs, t.Carrying = PhaseToDeposit, 0, carry
	return t, nil
}

// Retarget cambia el tile de trabajo porque el suyo se agotó (RN-ECON-015,
// RN-ECON-022): desde TO_RESOURCE o WORKING, la tarea vuelve a TO_RESOURCE
// hacia `target`, sin carga nueva y sin perder la que lleve. El tipo no cambia:
// quien llama elige un tile de la misma capa.
func (t Task) Retarget(target world.Tile) (Task, error) {
	if t.Phase != PhaseToResource && t.Phase != PhaseWorking {
		return t, fmt.Errorf("%w: cambiar de bosque desde %s", ErrBadTransition, t.Phase)
	}
	t.Target, t.Phase, t.PhaseUntilMs = target, PhaseToResource, 0
	return t, nil
}

// ReturnToDeposit manda la tarea al depósito con lo que lleve, aunque sea nada,
// porque su bosque se agotó y no hay otro cerca (RN-ECON-015): desde
// TO_RESOURCE o WORKING pasa a TO_DEPOSIT. Al depositar, quien llama termina
// la tarea.
func (t Task) ReturnToDeposit() (Task, error) {
	if t.Phase != PhaseToResource && t.Phase != PhaseWorking {
		return t, fmt.Errorf("%w: volver al depósito desde %s", ErrBadTransition, t.Phase)
	}
	t.Phase, t.PhaseUntilMs = PhaseToDeposit, 0
	return t, nil
}

// Deposit es la llegada al depósito: entrega lo cargado y la tarea vuelve a
// TO_RESOURCE con un viaje más (RN-ECON-004). Devuelve lo entregado. Si el
// viaje siguiente empieza o no lo decide quien llama (RN-ECON-005).
func (t Task) Deposit() (Task, int32, error) {
	if t.Phase != PhaseToDeposit {
		return t, 0, fmt.Errorf("%w: depositar desde %s", ErrBadTransition, t.Phase)
	}
	delivered := t.Carrying
	t.Phase, t.Carrying, t.TripsDone = PhaseToResource, 0, t.TripsDone+1
	return t, delivered, nil
}

// Validate comprueba una fila leída de la base.
func (t Task) Validate() error {
	if !t.Kind.Valid() {
		return fmt.Errorf("%w: tipo %q", ErrInvalidTask, t.Kind)
	}
	if !t.Phase.Valid() {
		return fmt.Errorf("%w: fase %q", ErrInvalidTask, t.Phase)
	}
	if (t.Phase == PhaseWorking) != (t.PhaseUntilMs > 0) {
		return fmt.Errorf("%w: fase %s con vencimiento %d", ErrInvalidTask, t.Phase, t.PhaseUntilMs)
	}
	if t.Carrying < 0 || t.TripsDone < 0 {
		return fmt.Errorf("%w: carga %d, viajes %d", ErrInvalidTask, t.Carrying, t.TripsDone)
	}
	return nil
}

// CanGather aplica la parte de RN-ECON-001 que depende de la unidad, en el
// orden de unit.move: propiedad, tipo y estado. El error de estado es el de
// unit.EnsureCanMove (UNIT_DEAD, UNIT_GARRISONED…).
func CanGather(u *unit.Unit, requester uuid.UUID) error {
	if u.PlayerID != requester {
		return ErrNotOwned
	}
	def, err := unit.Lookup(u.Type)
	if err != nil {
		return err
	}
	if !def.CanGather() {
		return fmt.Errorf("%w: %s", ErrNotGatherer, u.Type)
	}
	if !u.IsAlive() {
		return unit.ErrDead
	}
	return u.EnsureCanMove()
}

// Grid es lo que hace falta del mundo para validar un destino.
type Grid interface {
	InBounds(x, y int32) bool
	TerrainAt(x, y int32) world.TerrainType
}

// ValidTarget aplica la parte de RN-ECON-001 y RN-ECON-019 que depende del
// destino: dentro del mundo y sobre un terreno que dé recurso (FOREST o HILL).
// Devuelve la capa de ese terreno, que fija el tipo de la tarea. Que al tile le
// quede recurso (RN-ECON-014, RN-ECON-021) lo comprueba la simulación, que
// tiene la capa.
func ValidTarget(g Grid, target world.Tile) (deposit.Layer, error) {
	if !g.InBounds(target.X, target.Y) {
		return deposit.Layer{}, ErrOutOfBounds
	}
	l, ok := deposit.ForTerrain(g.TerrainAt(target.X, target.Y))
	if !ok {
		return deposit.Layer{}, fmt.Errorf("%w: %s es %s", ErrNotResource, target, g.TerrainAt(target.X, target.Y))
	}
	return l, nil
}
