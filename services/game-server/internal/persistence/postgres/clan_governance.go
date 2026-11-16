package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/clan"
)

// El gobierno del clan (SPEC-CLAN-GOVERNANCE, migración 000022). Cada orden
// toma el candado del clan y vuelve a comprobar el rango de quien la dio
// (INV-CLAN-005): la RAM ya la aplicó, y si la base no la admite la deshace.

// clanRoleOf es el rango de un jugador en un clan vivo: LEADER, OFFICER o
// MEMBER, o "" si no tiene una estancia abierta en él.
func clanRoleOf(ctx context.Context, tx pgx.Tx, clanID int64, playerID uuid.UUID) (string, error) {
	var (
		rank   string
		leader uuid.UUID
	)
	err := tx.QueryRow(ctx,
		`SELECT m.rank, c.leader_player_id
		   FROM clan_members m JOIN clans c ON c.id = m.clan_id
		  WHERE m.clan_id = $1 AND m.player_id = $2 AND m.left_at IS NULL AND c.dissolved_at IS NULL`,
		clanID, playerID).Scan(&rank, &leader)
	if errors.Is(normalize(err), ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("leer el rango de %s en el clan %d: %w", playerID, clanID, normalize(err))
	}
	if leader == playerID {
		return clan.RoleLeader, nil
	}
	return rank, nil
}

// ClanRank es un nombramiento (o su retirada) ya aplicado en la RAM.
type ClanRank struct {
	ClanID   int64
	PlayerID uuid.UUID
	By       uuid.UUID
	Officer  bool
	At       time.Time
	Tick     uint64
	AtMs     int64
}

// SetRank nombra oficial a un miembro o le quita el rango, si quien lo ordena
// es el líder y el miembro sigue abierto y no es el líder (RN-CLANGOV-003,
// INV-CLAN-005), y escribe ClanRankChanged, en una transacción con el candado
// del clan. Un reintento encuentra el rango ya escrito y no escribe nada.
func (r *ClanRepo) SetRank(ctx context.Context, o ClanRank) error {
	want := clan.RoleMember
	if o.Officer {
		want = clan.RoleOfficer
	}
	return r.store.InTx(ctx, func(tx pgx.Tx) error {
		if err := lockClan(ctx, tx, o.ClanID); err != nil {
			return err
		}
		by, err := clanRoleOf(ctx, tx, o.ClanID, o.By)
		if err != nil {
			return err
		}
		if !clan.CanGovern(by) {
			return ClanRejected{Reason: "sólo el líder nombra oficiales"}
		}
		target, err := clanRoleOf(ctx, tx, o.ClanID, o.PlayerID)
		if err != nil {
			return err
		}
		switch target {
		case "":
			return ClanRejected{Reason: "ese jugador ya no es de tu clan"}
		case clan.RoleLeader:
			return ClanRejected{Reason: "el líder no tiene otro rango"}
		case want:
			return nil // ya escrito: un reintento
		}
		if _, err := tx.Exec(ctx,
			`UPDATE clan_members SET rank = $3 WHERE clan_id = $1 AND player_id = $2 AND left_at IS NULL`,
			o.ClanID, o.PlayerID, want); err != nil {
			return fmt.Errorf("cambiar el rango: %w", normalize(err))
		}
		return appendEvent(ctx, tx, worldEvent{
			EventType: "ClanRankChanged", Tick: o.Tick, PlayerID: &o.PlayerID,
			Payload: map[string]any{"clanId": o.ClanID, "rank": want, "byPlayerId": o.By.String(), "atMs": o.AtMs},
		})
	})
}

// ClanLeaderChange es un cambio de líder ya aplicado en la RAM: un traspaso
// (By = From) o la sucesión de un líder ausente (By = uuid.Nil).
type ClanLeaderChange struct {
	ClanID int64
	Tag    string
	From   uuid.UUID
	To     uuid.UUID
	Reason string
	By     uuid.UUID
	At     time.Time
	Tick   uint64
	AtMs   int64
}

// ChangeLeader pasa el liderazgo de From a To en una transacción con el
// candado del clan, si From sigue siendo el líder y To un miembro abierto
// (INV-CLAN-001, INV-CLAN-005): To deja de ser oficial, pasa a líder y From
// queda como oficial (Q4, Q7). Escribe ClanLeaderChanged con el nuevo líder
// como player_id y el anterior como counterpart, que el registro cuenta a los
// dos (Q8). Un reintento encuentra a To ya de líder y no escribe nada.
func (r *ClanRepo) ChangeLeader(ctx context.Context, o ClanLeaderChange) error {
	return r.store.InTx(ctx, func(tx pgx.Tx) error {
		if err := lockClan(ctx, tx, o.ClanID); err != nil {
			return err
		}
		var leader uuid.UUID
		err := tx.QueryRow(ctx,
			`SELECT leader_player_id FROM clans WHERE id = $1 AND dissolved_at IS NULL FOR UPDATE`, o.ClanID).Scan(&leader)
		if errors.Is(normalize(err), ErrNotFound) {
			return ClanRejected{Reason: "el clan ya no existe"}
		}
		if err != nil {
			return fmt.Errorf("leer el clan %d: %w", o.ClanID, normalize(err))
		}
		if leader == o.To {
			return nil // ya escrito: un reintento
		}
		if leader != o.From {
			return ClanRejected{Reason: "quien cede el liderazgo ya no es el líder"}
		}
		to, err := clanRoleOf(ctx, tx, o.ClanID, o.To)
		if err != nil {
			return err
		}
		if to == "" {
			return ClanRejected{Reason: "ese jugador ya no es de tu clan"}
		}
		if _, err := tx.Exec(ctx,
			`UPDATE clan_members SET rank = 'MEMBER' WHERE clan_id = $1 AND player_id = $2 AND left_at IS NULL`,
			o.ClanID, o.To); err != nil {
			return fmt.Errorf("quitar el rango al líder nuevo: %w", normalize(err))
		}
		if _, err := tx.Exec(ctx, `UPDATE clans SET leader_player_id = $2 WHERE id = $1`, o.ClanID, o.To); err != nil {
			return fmt.Errorf("cambiar el líder: %w", normalize(err))
		}
		if _, err := tx.Exec(ctx,
			`UPDATE clan_members SET rank = 'OFFICER' WHERE clan_id = $1 AND player_id = $2 AND left_at IS NULL`,
			o.ClanID, o.From); err != nil {
			return fmt.Errorf("dejar de oficial al líder anterior: %w", normalize(err))
		}
		return appendEvent(ctx, tx, worldEvent{
			EventType: "ClanLeaderChanged", Tick: o.Tick, PlayerID: &o.To, CounterpartID: counterpart(o.From),
			Payload: map[string]any{
				"clanId": o.ClanID, "reason": o.Reason, "ownerClanTag": o.Tag, "forClanTag": o.Tag, "atMs": o.AtMs,
			},
		})
	})
}
