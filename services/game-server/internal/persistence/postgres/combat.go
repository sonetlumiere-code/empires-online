package postgres

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
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
	VictimType     unit.Type
	AtMs           int64
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
		"unitType": string(k.VictimType), "atMs": k.AtMs,
	}
	victim := k.VictimPlayer
	if err := appendEvent(ctx, db, worldEvent{
		EventType: "UnitKilled", Tick: k.Tick, PlayerID: &victim, CounterpartID: counterpart(k.KillerPlayer), Payload: payload,
	}); err != nil {
		return false, err
	}
	return true, nil
}

// Attacked es el primer golpe de un enfrentamiento (SPEC-COMBAT-RESPONSE Q8).
type Attacked struct {
	AttackerID     int64
	AttackerPlayer uuid.UUID
	TargetID       int64
	TargetPlayer   uuid.UUID
	At             world.Tile
	Tick           uint64
	Auto           bool
	AtMs           int64
}

// Attacked registra el evento UnitAttacked, a nombre del jugador atacado como
// UnitKilled. Es sólo auditoría: no toca ninguna otra fila.
func (r *UnitRepo) Attacked(ctx context.Context, db DB, a Attacked) error {
	target := a.TargetPlayer
	return appendEvent(ctx, db, worldEvent{
		EventType: "UnitAttacked", Tick: a.Tick, PlayerID: &target, CounterpartID: counterpart(a.AttackerPlayer),
		Payload: map[string]any{
			"unitId": a.TargetID, "attackerId": a.AttackerID, "attackerPlayerId": a.AttackerPlayer.String(),
			"x": a.At.X, "y": a.At.Y, "auto": a.Auto, "atMs": a.AtMs,
		},
	})
}

// Pillaged es un golpe de saqueo (SPEC-COMBAT-PILLAGE, ADR-026): el débito del
// stock de la ciudad saqueada, como delta versionado que lleva también los
// depósitos sin volcar, y el evento que lo audita.
type Pillaged struct {
	AttackerID     int64
	AttackerPlayer uuid.UUID
	CityID         int64
	VictimPlayer   uuid.UUID
	Amount         int32
	Stock          StockDelta
	At             world.Tile
	Tick           uint64
	AtMs           int64
}

// Pillaged escribe un golpe de saqueo dentro de la transacción `db` y dice si
// lo escribió. Es idempotente por la versión del stock (ADR-026 §3): si la base
// ya tiene esa versión o una posterior, el golpe ya estaba escrito —la cola
// ejecuta en orden todas las escrituras del stock de una ciudad— y no hace
// nada, tampoco el evento. Un débito que dejaría el stock negativo es
// economy.ErrInsufficientResources (INV-ECON-001).
func (r *UnitRepo) Pillaged(ctx context.Context, db DB, p Pillaged) (bool, error) {
	aplicado, err := applyStock(ctx, db, p.Stock)
	if err != nil || !aplicado {
		return false, err
	}
	victim := p.VictimPlayer
	return true, appendEvent(ctx, db, worldEvent{
		EventType: "CityPillaged", Tick: p.Tick, PlayerID: &victim, CounterpartID: counterpart(p.AttackerPlayer),
		Payload: map[string]any{
			"cityId": p.CityID, "attackerId": p.AttackerID, "attackerPlayerId": p.AttackerPlayer.String(),
			"amount": p.Amount, "x": p.At.X, "y": p.At.Y, "atMs": p.AtMs,
		},
	})
}
