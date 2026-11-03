package postgres

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
)

// Kill es lo que escribe la transacción de una muerte (RN-COMBAT-009,
// ADR-024 §2).
type Kill struct {
	VictimID       int64
	VictimPlayer   uuid.UUID
	KillerID       int64
	KillerPlayer   uuid.UUID
	CityID         *int64
	PopulationCost int32
	At             world.Tile
	Tick           uint64
	Loot           int32
	Lost           int32
}

// Kill escribe una muerte dentro de la transacción `db`: la víctima DEAD, sin
// vida, sin botín y en su último tile; su movimiento cerrado y su tarea
// borrada; un punto de población menos en su ciudad (por delta, que conmuta
// con las demás escrituras de la población); y el evento UnitKilled.
//
// Es idempotente: si la víctima ya estaba DEAD no hace nada y devuelve false.
// La carga del asesino no se escribe aquí: viaja en el volcado de su unidad
// (INV-COMBAT-003).
func (r *UnitRepo) Kill(ctx context.Context, db DB, k Kill) (bool, error) {
	tag, err := db.Exec(ctx,
		`UPDATE units
		 SET status = 'DEAD', hp = 0, loot = 0, x = $2, y = $3, chunk_x = $4, chunk_y = $5
		 WHERE id = $1 AND status <> 'DEAD'`,
		k.VictimID, k.At.X, k.At.Y, k.At.X/r.chunkSize, k.At.Y/r.chunkSize)
	if err != nil {
		return false, fmt.Errorf("matar la unidad %d: %w", k.VictimID, normalize(err))
	}
	if tag.RowsAffected() == 0 {
		return false, nil
	}
	if _, err := db.Exec(ctx,
		`UPDATE unit_movements SET status = 'CANCELLED', finished_at = now()
		 WHERE unit_id = $1 AND status = 'ACTIVE'`, k.VictimID); err != nil {
		return false, fmt.Errorf("cerrar el movimiento de la unidad muerta %d: %w", k.VictimID, normalize(err))
	}
	if err := DeleteTask(ctx, db, k.VictimID); err != nil {
		return false, err
	}
	if k.CityID != nil && k.PopulationCost > 0 {
		if _, err := db.Exec(ctx,
			`UPDATE cities SET population = GREATEST(population - $2, 0), version = version + 1 WHERE id = $1`,
			*k.CityID, k.PopulationCost); err != nil {
			return false, fmt.Errorf("descontar la población de la ciudad %d: %w", *k.CityID, normalize(err))
		}
	}
	payload := map[string]any{
		"unitId": k.VictimID, "killerId": k.KillerID, "killerPlayerId": k.KillerPlayer.String(),
		"x": k.At.X, "y": k.At.Y, "loot": k.Loot, "lost": k.Lost,
	}
	victim := k.VictimPlayer
	if err := appendEvent(ctx, db, worldEvent{EventType: "UnitKilled", Tick: k.Tick, PlayerID: &victim, Payload: payload}); err != nil {
		return false, err
	}
	return true, nil
}
