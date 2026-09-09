package movement

import (
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
)

// Status es el ciclo de vida de un movimiento.
type Status string

const (
	// StatusActive: en curso. Una unidad tiene como máximo uno (INV-MOVE-001,
	// garantizado físicamente por un índice único parcial en PostgreSQL).
	StatusActive Status = "ACTIVE"
	// StatusCompleted: la unidad llegó a su destino.
	StatusCompleted Status = "COMPLETED"
	// StatusCancelled: interrumpido por una orden nueva o por el jugador.
	StatusCancelled Status = "CANCELLED"
	// StatusFailed: la ruta dejó de ser válida (por ejemplo, quedó bloqueada).
	StatusFailed Status = "FAILED"
)

// Valid indica si el estado pertenece al conjunto conocido.
func (s Status) Valid() bool {
	switch s {
	case StatusActive, StatusCompleted, StatusCancelled, StatusFailed:
		return true
	}
	return false
}

// CancelReason explica por qué se interrumpió un movimiento. Se transmite al
// cliente para que pueda dar una respuesta útil al jugador.
type CancelReason string

const (
	ReasonReplaced          CancelReason = "REPLACED"
	ReasonCancelledByPlayer CancelReason = "CANCELLED_BY_PLAYER"
	ReasonPathBlocked       CancelReason = "PATH_BLOCKED"
	ReasonUnitDead          CancelReason = "UNIT_DEAD"
	ReasonServer            CancelReason = "SERVER"
)

// Movement es un movimiento persistente de una unidad.
//
// Sobrevive a la desconexión del jugador y al reinicio del servidor: es un hecho
// del mundo, no un estado de sesión.
type Movement struct {
	ID            int64
	UnitID        int64
	Path          TimedPath
	Target        world.Tile
	StartTimeMs   int64
	ArrivalTimeMs int64
	Status        Status
}

// New construye un movimiento a partir de una polilínea ya validada.
func New(unitID int64, path TimedPath, target world.Tile, startTimeMs int64) *Movement {
	return &Movement{
		UnitID:        unitID,
		Path:          path,
		Target:        target,
		StartTimeMs:   startTimeMs,
		ArrivalTimeMs: startTimeMs + path.DurationMs(),
		Status:        StatusActive,
	}
}

// PositionAt devuelve la posición autoritativa de la unidad en un instante absoluto.
func (m *Movement) PositionAt(nowMs int64) world.Tile {
	return m.Path.PositionAt(nowMs - m.StartTimeMs)
}

// HasArrived indica si el movimiento ya alcanzó su destino en el instante dado.
//
// Es deliberadamente independiente de Status: al recuperarse de un crash, un
// movimiento sigue marcado ACTIVE en la base de datos aunque su hora de llegada
// haya pasado hace rato. Esta función es la que permite detectarlo y finalizarlo.
func (m *Movement) HasArrived(nowMs int64) bool {
	return nowMs >= m.ArrivalTimeMs
}

// RemainingMs devuelve los milisegundos que faltan para llegar; 0 si ya llegó.
func (m *Movement) RemainingMs(nowMs int64) int64 {
	if r := m.ArrivalTimeMs - nowMs; r > 0 {
		return r
	}
	return 0
}

// Destination es el tile final de la polilínea. Coincide con Target salvo que el
// movimiento se haya construido con una ruta parcial.
func (m *Movement) Destination() world.Tile { return m.Path.Destination() }
