// Package task modela lo que una unidad hace a lo largo de varios tramos.
//
// En el primer slice de economía existe una sola tarea, GATHER_WOOD, que
// encadena tramos de movimiento y esperas:
//
//	TO_RESOURCE (movimiento) → WORKING (workMs) → TO_DEPOSIT (movimiento) → depósito → TO_RESOURCE …
//
// Los tramos son movimientos normales (docs/specs/movement.md): la tarea sólo
// decide qué hacer cuando uno termina o cuando vence su espera. Este paquete es
// la máquina de estados, pura: cada transición devuelve la tarea siguiente y no
// escribe nada. No es un patrón general para cualquier acción de varios tramos
// (ADR-023, «Fuera de esta decisión»).
//
// Spec: ../../../../../docs/specs/economy-wood.md §4.2.
package task

import (
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
)

// Kind identifica el tipo de tarea.
type Kind string

// KindGatherWood es la recolección de madera.
const KindGatherWood Kind = "GATHER_WOOD"

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
)

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
	// Target es el tile de trabajo: el propio tile FOREST (RN-ECON-002).
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

// NewGather empieza una recolección: el primer tramo va al recurso.
func NewGather(unitID int64, target world.Tile) Task {
	return Task{UnitID: unitID, Kind: KindGatherWood, Target: target, Phase: PhaseToResource}
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
	if t.Kind != KindGatherWood {
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

// ValidTarget aplica la parte de RN-ECON-001 que depende del destino: dentro
// del mundo y sobre terreno FOREST. El bosque no se agota en este slice.
func ValidTarget(g Grid, target world.Tile) error {
	if !g.InBounds(target.X, target.Y) {
		return ErrOutOfBounds
	}
	if g.TerrainAt(target.X, target.Y) != world.Forest {
		return fmt.Errorf("%w: %s es %s", ErrNotResource, target, g.TerrainAt(target.X, target.Y))
	}
	return nil
}
