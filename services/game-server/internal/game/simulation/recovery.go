package simulation

import (
	"log/slog"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/movement"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
)

// RecoveryResult resume lo que ocurrió al rehidratar el mundo.
type RecoveryResult struct {
	Units   int
	Cities  int
	Resumed int
	Arrived int
	Failed  int
	// FinishedMovements son los movimientos que hay que cerrar en la base de datos:
	// terminaron mientras el proceso estaba caído, o su ruta ya no es válida.
	FinishedMovements []FinishedMovement
}

// FinishedMovement es un movimiento que debe cerrarse tras la recuperación.
type FinishedMovement struct {
	MovementID int64
	Status     movement.Status
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

		if err := movement.Validate(m.Path, w); err != nil {
			log.Warn("polilínea inválida al rehidratar; el movimiento se descarta",
				"movement_id", m.ID, "unit_id", m.UnitID, "err", err)
			u.Status = unit.StatusIdle
			state.MarkDirty(u.ID)
			res.FinishedMovements = append(res.FinishedMovements,
				FinishedMovement{MovementID: m.ID, Status: movement.StatusFailed})
			res.Failed++
			continue
		}

		if m.HasArrived(nowMs) {
			dest := m.Destination()
			state.MoveUnitTo(u, dest)
			u.Status = unit.StatusIdle
			state.MarkDirty(u.ID)
			res.FinishedMovements = append(res.FinishedMovements,
				FinishedMovement{MovementID: m.ID, Status: movement.StatusCompleted})
			res.Arrived++
			continue
		}

		pos := m.PositionAt(nowMs)
		state.MoveUnitTo(u, pos)
		u.Status = unit.StatusMoving
		state.SetMovement(m)
		res.Resumed++
	}

	return res
}
