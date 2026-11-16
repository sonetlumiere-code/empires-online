package postgres

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/clan"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/territory"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// clanLockSpace es el primer entero del candado de transacción de un clan
// (pg_advisory_xact_lock(int4, int4)): lo toman la entrada, la salida y cada
// envío del canal, así que los id del canal se confirman en orden y nadie
// escribe tras su salida (ADR-040 §5). «CLAN» en ASCII.
const clanLockSpace int32 = 0x434C414E

// ClanRepo escribe y lee los clanes (SPEC-CLANS, ADR-040): la pertenencia, que
// escribe la cola de la simulación, y el canal y la vista pública, que atiende
// la conexión.
type ClanRepo struct {
	store *Store
	cfg   ClansConfig
}

// ClansConfig parametriza el canal (los mismos valores que los mensajes
// directos, SPEC-CLANS Q9).
type ClansConfig struct {
	// MaxAge es hasta cuándo atrás se muestra el canal (EO_MESSAGE_MAX_DAYS).
	MaxAge time.Duration
	// PageSize es cuántos mensajes lleva una página (EO_MESSAGE_PAGE_SIZE).
	PageSize int
}

// NewClanRepo construye el repositorio.
func NewClanRepo(s *Store, cfg ClansConfig) *ClanRepo {
	if cfg.PageSize < 1 {
		cfg.PageSize = 50
	}
	if cfg.MaxAge <= 0 {
		cfg.MaxAge = 30 * 24 * time.Hour
	}
	return &ClanRepo{store: s, cfg: cfg}
}

// ClanRejected es un rechazo de dominio de la base: el clan, la invitación o el
// jugador cambiaron por otra vía. Reason es el motivo que ve el jugador. No se
// reintenta.
type ClanRejected struct{ Reason string }

func (e ClanRejected) Error() string { return e.Reason }

// lockClan toma el candado de transacción de un clan.
func lockClan(ctx context.Context, tx pgx.Tx, clanID int64) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1, $2::int4)`, clanLockSpace, int32(clanID)); err != nil {
		return fmt.Errorf("tomar el candado del clan %d: %w", clanID, normalize(err))
	}
	return nil
}

// ─────────────────────────────────────────────────────────────
// Carga
// ─────────────────────────────────────────────────────────────

// ListLive devuelve los clanes vivos, por id.
func (r *ClanRepo) ListLive(ctx context.Context) ([]clan.Clan, error) {
	rows, err := r.store.pool.Query(ctx,
		`SELECT id, name, tag, leader_player_id FROM clans WHERE dissolved_at IS NULL ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("leer los clanes: %w", normalize(err))
	}
	defer rows.Close()
	var out []clan.Clan
	for rows.Next() {
		var c clan.Clan
		if err := rows.Scan(&c.ID, &c.Name, &c.Tag, &c.LeaderID); err != nil {
			return nil, fmt.Errorf("leer un clan: %w", normalize(err))
		}
		out = append(out, c)
	}
	return out, normalize(rows.Err())
}

// ListStays devuelve las estancias abiertas de los clanes vivos y las cerradas
// después de since, de las que sale la paz residual (ADR-040 §3).
func (r *ClanRepo) ListStays(ctx context.Context, since time.Time) ([]clan.Stay, error) {
	rows, err := r.store.pool.Query(ctx,
		`SELECT m.clan_id, m.player_id, m.joined_at, m.left_at, m.rank
		   FROM clan_members m JOIN clans c ON c.id = m.clan_id
		  WHERE (m.left_at IS NULL AND c.dissolved_at IS NULL) OR m.left_at > $1
		  ORDER BY m.id`, since)
	if err != nil {
		return nil, fmt.Errorf("leer las estancias de los clanes: %w", normalize(err))
	}
	defer rows.Close()
	var out []clan.Stay
	for rows.Next() {
		var (
			st     clan.Stay
			joined time.Time
			left   *time.Time
			rank   string
		)
		if err := rows.Scan(&st.ClanID, &st.PlayerID, &joined, &left, &rank); err != nil {
			return nil, fmt.Errorf("leer una estancia: %w", normalize(err))
		}
		st.JoinedAtMs, st.Officer = joined.UnixMilli(), rank == clan.RoleOfficer
		if left != nil {
			st.LeftAtMs = left.UnixMilli()
		}
		out = append(out, st)
	}
	return out, normalize(rows.Err())
}

// ListInvites devuelve las invitaciones vivas en now de los clanes vivos.
func (r *ClanRepo) ListInvites(ctx context.Context, now time.Time) ([]clan.Invite, error) {
	rows, err := r.store.pool.Query(ctx,
		`SELECT i.clan_id, i.player_id, i.invited_by, i.expires_at
		   FROM clan_invites i JOIN clans c ON c.id = i.clan_id
		  WHERE c.dissolved_at IS NULL AND i.expires_at > $1
		  ORDER BY i.clan_id, i.player_id`, now)
	if err != nil {
		return nil, fmt.Errorf("leer las invitaciones: %w", normalize(err))
	}
	defer rows.Close()
	var out []clan.Invite
	for rows.Next() {
		var (
			inv clan.Invite
			exp time.Time
		)
		if err := rows.Scan(&inv.ClanID, &inv.PlayerID, &inv.InvitedBy, &exp); err != nil {
			return nil, fmt.Errorf("leer una invitación: %w", normalize(err))
		}
		inv.ExpiresAtMs = exp.UnixMilli()
		out = append(out, inv)
	}
	return out, normalize(rows.Err())
}

// ClanViolation es un clan que la carga no puede incorporar.
type ClanViolation struct {
	InvID  string
	ClanID int64
	Detail string
}

// Violations comprueba al arrancar lo que la base no impide sola: que todo clan
// vivo tenga un líder que sea miembro abierto (INV-CLAN-001) y que ninguno
// junte a la Orden y al Caos (INV-CLAN-002, por si los triggers se
// desactivaron a mano).
func (r *ClanRepo) Violations(ctx context.Context) ([]ClanViolation, error) {
	rows, err := r.store.pool.Query(ctx,
		`SELECT c.id, 'INV-CLAN-001', 'el líder no es un miembro abierto'
		   FROM clans c
		  WHERE c.dissolved_at IS NULL
		    AND NOT EXISTS (SELECT 1 FROM clan_members m
		                     WHERE m.clan_id = c.id AND m.player_id = c.leader_player_id AND m.left_at IS NULL)
		 UNION ALL
		 SELECT c.id, 'INV-CLAN-002', 'junta a la Orden y al Caos'
		   FROM clans c
		  WHERE c.dissolved_at IS NULL AND clan_mixes_factions(c.id)
		 ORDER BY 1, 2`)
	if err != nil {
		return nil, fmt.Errorf("comprobar los clanes: %w", normalize(err))
	}
	defer rows.Close()
	var out []ClanViolation
	for rows.Next() {
		var v ClanViolation
		if err := rows.Scan(&v.ClanID, &v.InvID, &v.Detail); err != nil {
			return nil, fmt.Errorf("leer una violación de clan: %w", normalize(err))
		}
		out = append(out, v)
	}
	return out, normalize(rows.Err())
}

// ─────────────────────────────────────────────────────────────
// Pertenencia (la escribe la cola de la simulación)
// ─────────────────────────────────────────────────────────────

// ClanCreate es una fundación ya validada por el tick.
type ClanCreate struct {
	PlayerID uuid.UUID
	Name     string
	Tag      string
	At       time.Time
	Tick     uint64
	AtMs     int64
}

// Create funda el clan, abre la estancia de su líder, borra sus invitaciones
// recibidas y escribe ClanFounded, en una transacción. Un reintento encuentra el
// clan ya fundado por ese jugador en ese instante y devuelve su id. Un nombre o
// una sigla ocupados, o un fundador que ya está en un clan, son ClanRejected.
func (r *ClanRepo) Create(ctx context.Context, o ClanCreate) (int64, error) {
	var id int64
	err := r.store.InTx(ctx, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx,
			`SELECT c.id FROM clans c JOIN clan_members m ON m.clan_id = c.id
			  WHERE c.leader_player_id = $1 AND c.tag = $2 AND m.player_id = $1 AND m.joined_at = $3`,
			o.PlayerID, o.Tag, o.At).Scan(&id)
		if err == nil {
			return nil // ya fundado: un reintento
		}
		if !errors.Is(normalize(err), ErrNotFound) {
			return fmt.Errorf("buscar el clan de %s: %w", o.PlayerID, normalize(err))
		}
		var taken string
		err = tx.QueryRow(ctx,
			`SELECT CASE WHEN lower(name) = lower($1) THEN 'name' ELSE 'tag' END
			   FROM clans WHERE dissolved_at IS NULL AND (lower(name) = lower($1) OR tag = $2) LIMIT 1`,
			o.Name, o.Tag).Scan(&taken)
		switch {
		case err == nil && taken == "name":
			return ClanRejected{Reason: "ese nombre ya existe"}
		case err == nil:
			return ClanRejected{Reason: "esa sigla ya existe"}
		case !errors.Is(normalize(err), ErrNotFound):
			return fmt.Errorf("comprobar el nombre del clan: %w", normalize(err))
		}
		if err := tx.QueryRow(ctx,
			`INSERT INTO clans (name, tag, leader_player_id, founded_at) VALUES ($1, $2, $3, $4) RETURNING id`,
			o.Name, o.Tag, o.PlayerID, o.At).Scan(&id); err != nil {
			if IsUniqueViolation(err) {
				return ClanRejected{Reason: "ese nombre o esa sigla ya existen"}
			}
			return fmt.Errorf("fundar el clan: %w", normalize(err))
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO clan_members (clan_id, player_id, joined_at, first_message_id) VALUES ($1, $2, $3, 0)`,
			id, o.PlayerID, o.At); err != nil {
			if IsUniqueViolation(err) {
				return ClanRejected{Reason: "ya estás en un clan"}
			}
			return fmt.Errorf("abrir la estancia del fundador: %w", normalize(err))
		}
		if _, err := tx.Exec(ctx, `DELETE FROM clan_invites WHERE player_id = $1`, o.PlayerID); err != nil {
			return fmt.Errorf("borrar las invitaciones del fundador: %w", normalize(err))
		}
		return appendEvent(ctx, tx, worldEvent{
			EventType: "ClanFounded", Tick: o.Tick, PlayerID: &o.PlayerID,
			Payload: map[string]any{"clanId": id, "name": o.Name, "tag": o.Tag, "atMs": o.AtMs},
		})
	})
	return id, err
}

// ClanInvite es una invitación, o su borrado, ya validada por el tick.
type ClanInvite struct {
	ClanID    int64
	PlayerID  uuid.UUID
	InvitedBy uuid.UUID
	Remove    bool
	At        time.Time
	ExpiresAt time.Time
}

// Invite escribe una invitación, sólo si el clan sigue vivo, quien invita es
// su líder o un oficial abierto, con el candado del clan (INV-CLAN-005), y el
// invitado no está en ningún clan; o la borra (tolerante). Un reintento no la
// duplica.
func (r *ClanRepo) Invite(ctx context.Context, o ClanInvite) error {
	if o.Remove {
		_, err := r.store.pool.Exec(ctx, `DELETE FROM clan_invites WHERE clan_id = $1 AND player_id = $2`, o.ClanID, o.PlayerID)
		if err != nil {
			return fmt.Errorf("borrar la invitación: %w", normalize(err))
		}
		return nil
	}
	return r.store.InTx(ctx, func(tx pgx.Tx) error {
		if err := lockClan(ctx, tx, o.ClanID); err != nil {
			return err
		}
		role, err := clanRoleOf(ctx, tx, o.ClanID, o.InvitedBy)
		if err != nil {
			return err
		}
		if !clan.CanInvite(role) {
			return ClanRejected{Reason: "sólo el líder o un oficial invita"}
		}
		var ok bool
		if err := tx.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM clan_members WHERE player_id = $1 AND left_at IS NULL)`,
			o.PlayerID).Scan(&ok); err != nil {
			return fmt.Errorf("comprobar al invitado: %w", normalize(err))
		}
		if ok {
			return ClanRejected{Reason: "ese jugador ya está en un clan"}
		}
		_, err = tx.Exec(ctx,
			`INSERT INTO clan_invites (clan_id, player_id, invited_by, created_at, expires_at)
			 VALUES ($1, $2, $3, $4, $5)
			 ON CONFLICT (clan_id, player_id) DO UPDATE
			   SET invited_by = EXCLUDED.invited_by, created_at = EXCLUDED.created_at, expires_at = EXCLUDED.expires_at
			 WHERE clan_invites.expires_at <= EXCLUDED.created_at OR clan_invites.created_at = EXCLUDED.created_at`,
			o.ClanID, o.PlayerID, o.InvitedBy, o.At, o.ExpiresAt)
		if err != nil {
			return fmt.Errorf("escribir la invitación: %w", normalize(err))
		}
		return nil
	})
}

// ClanJoin es la entrada de un invitado ya validada por el tick.
type ClanJoin struct {
	ClanID     int64
	PlayerID   uuid.UUID
	MaxMembers int
	// BreakNotice es el preaviso que abre una alianza que la entrada mezcla con
	// la Orden y el Caos (ADR-042 §4); cero: no abre ninguno.
	BreakNotice time.Duration
	At          time.Time
	Tick        uint64
	AtMs        int64
}

// Join abre la estancia del invitado con su first_message_id, borra sus
// invitaciones y escribe ClanJoined, en una transacción con el candado del clan
// y su fila tomada. Vuelve a comprobar que el clan vive, que la invitación sigue
// viva en el instante de la orden, el tope y la regla de bandos (el trigger,
// INV-CLAN-002). Un reintento encuentra la estancia ya abierta.
func (r *ClanRepo) Join(ctx context.Context, o ClanJoin) error {
	_, err := r.JoinBreaking(ctx, o)
	return err
}

// JoinBreaking es Join, y además abre en la misma transacción el preaviso de
// cada alianza del clan que la entrada mezcla con la Orden y el Caos, y las
// devuelve (ADR-042 §4). Un reintento no abre ninguno: la estancia ya estaba.
func (r *ClanRepo) JoinBreaking(ctx context.Context, o ClanJoin) ([]AllianceBreak, error) {
	var breaks []AllianceBreak
	err := r.store.InTx(ctx, func(tx pgx.Tx) error {
		breaks = nil
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
		var (
			openClan int64
			joined   time.Time
		)
		err = tx.QueryRow(ctx,
			`SELECT clan_id, joined_at FROM clan_members WHERE player_id = $1 AND left_at IS NULL`, o.PlayerID).Scan(&openClan, &joined)
		switch {
		case err == nil && openClan == o.ClanID && joined.Equal(o.At):
			return nil // ya escrita: un reintento
		case err == nil:
			return ClanRejected{Reason: "ya estás en un clan"}
		case !errors.Is(normalize(err), ErrNotFound):
			return fmt.Errorf("leer la estancia de %s: %w", o.PlayerID, normalize(err))
		}
		var invited bool
		if err := tx.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM clan_invites WHERE clan_id = $1 AND player_id = $2 AND expires_at > $3)`,
			o.ClanID, o.PlayerID, o.At).Scan(&invited); err != nil {
			return fmt.Errorf("comprobar la invitación: %w", normalize(err))
		}
		if !invited {
			return ClanRejected{Reason: "la invitación no existe o caducó"}
		}
		var members int
		if err := tx.QueryRow(ctx,
			`SELECT count(*) FROM clan_members WHERE clan_id = $1 AND left_at IS NULL`, o.ClanID).Scan(&members); err != nil {
			return fmt.Errorf("contar los miembros: %w", normalize(err))
		}
		if o.MaxMembers > 0 && members >= o.MaxMembers {
			return ClanRejected{Reason: "el clan está lleno"}
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO clan_members (clan_id, player_id, joined_at, first_message_id)
			 VALUES ($1, $2, $3, (SELECT coalesce(max(id), 0) FROM clan_messages WHERE clan_id = $1))`,
			o.ClanID, o.PlayerID, o.At); err != nil {
			switch {
			case IsCheckViolation(err):
				return ClanRejected{Reason: "un clan no junta a la Orden y al Caos"}
			case IsUniqueViolation(err):
				return ClanRejected{Reason: "ya estás en un clan"}
			}
			return fmt.Errorf("abrir la estancia: %w", normalize(err))
		}
		if _, err := tx.Exec(ctx, `DELETE FROM clan_invites WHERE player_id = $1`, o.PlayerID); err != nil {
			return fmt.Errorf("borrar las invitaciones: %w", normalize(err))
		}
		if err := appendEvent(ctx, tx, worldEvent{
			EventType: "ClanJoined", Tick: o.Tick, PlayerID: &o.PlayerID,
			Payload: map[string]any{"clanId": o.ClanID, "atMs": o.AtMs},
		}); err != nil {
			return err
		}
		if o.BreakNotice > 0 {
			var err error
			if breaks, err = breakMixedAlliances(ctx, tx, o); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return breaks, nil
}

// ClanLeave es una salida o una expulsión ya aplicada en la RAM.
type ClanLeave struct {
	ClanID    int64
	PlayerID  uuid.UUID
	Reason    string
	By        uuid.UUID
	At        time.Time
	NewLeader uuid.UUID
	Dissolve  bool
	Tick      uint64
	AtMs      int64
}

// Leave cierra la estancia y, en la misma transacción, cambia el líder o
// disuelve el clan (con sus invitaciones) y escribe ClanLeft o
// ClanMemberExpelled, con el candado del clan (ADR-040 §2). Es tolerante: si la
// estancia ya está cerrada (un reintento), termina sin escribir. Devuelve si
// escribió. Una salida que se reintenta no puede cerrar una estancia posterior:
// mientras la base tiene la anterior abierta, la entrada siguiente se rechaza.
// Una disolución libera antes los territorios del clan y los devuelve
// (ADR-041 §4, INV-TERR-012).
func (r *ClanRepo) Leave(ctx context.Context, l ClanLeave) (bool, []territory.Control, error) {
	applied := false
	var released []territory.Control
	err := r.store.InTx(ctx, func(tx pgx.Tx) error {
		released = nil
		if err := lockClan(ctx, tx, l.ClanID); err != nil {
			return err
		}
		if l.Reason == clan.ReasonExpelled {
			// INV-CLAN-005: quien expulsa tiene, aquí y ahora, el rango que lo
			// permite. Una estancia ya cerrada es un reintento: no se mira.
			target, err := clanRoleOf(ctx, tx, l.ClanID, l.PlayerID)
			if err != nil {
				return err
			}
			if target != "" {
				by, err := clanRoleOf(ctx, tx, l.ClanID, l.By)
				if err != nil {
					return err
				}
				if !clan.CanExpel(by, target) {
					return ClanRejected{Reason: "quien expulsa no tiene el rango para hacerlo"}
				}
			}
		}
		tag, err := tx.Exec(ctx,
			`UPDATE clan_members SET left_at = $3, left_reason = $4
			  WHERE clan_id = $1 AND player_id = $2 AND left_at IS NULL`,
			l.ClanID, l.PlayerID, l.At, l.Reason)
		if err != nil {
			return fmt.Errorf("cerrar la estancia: %w", normalize(err))
		}
		if tag.RowsAffected() == 0 {
			return nil // ya cerrada: un reintento
		}
		applied = true
		if l.NewLeader != uuid.Nil {
			// RN-CLANGOV-004: el que hereda deja de ser oficial antes de liderar.
			if _, err := tx.Exec(ctx,
				`UPDATE clan_members SET rank = 'MEMBER' WHERE clan_id = $1 AND player_id = $2 AND left_at IS NULL`,
				l.ClanID, l.NewLeader); err != nil {
				return fmt.Errorf("quitar el rango al líder nuevo: %w", normalize(err))
			}
			if _, err := tx.Exec(ctx, `UPDATE clans SET leader_player_id = $2 WHERE id = $1`, l.ClanID, l.NewLeader); err != nil {
				return fmt.Errorf("cambiar el líder: %w", normalize(err))
			}
		}
		if l.Dissolve {
			var err error
			if released, err = releaseClanTerritories(ctx, tx, l); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE clans SET dissolved_at = $2 WHERE id = $1 AND dissolved_at IS NULL`, l.ClanID, l.At); err != nil {
				return fmt.Errorf("disolver el clan: %w", normalize(err))
			}
			if _, err := tx.Exec(ctx, `DELETE FROM clan_invites WHERE clan_id = $1`, l.ClanID); err != nil {
				return fmt.Errorf("borrar las invitaciones del clan: %w", normalize(err))
			}
			// RN-CLANALLY-008: sus ofertas y alianzas expiran con él.
			if err := expireClanAlliances(ctx, tx, l); err != nil {
				return err
			}
		}
		ev := worldEvent{
			EventType: "ClanLeft", Tick: l.Tick, PlayerID: &l.PlayerID,
			Payload: map[string]any{"clanId": l.ClanID, "dissolved": l.Dissolve, "atMs": l.AtMs},
		}
		if l.NewLeader != uuid.Nil {
			ev.Payload["newLeaderId"] = l.NewLeader.String()
		}
		if l.Reason == clan.ReasonExpelled {
			ev.EventType = "ClanMemberExpelled"
			ev.CounterpartID = counterpart(l.By)
		}
		return appendEvent(ctx, tx, ev)
	})
	if err != nil {
		released = nil
	}
	return applied, released, err
}

// releaseClanTerritories deja libres los territorios de un clan que se disuelve
// y registra un TerritoryControlChanged por cada uno, sin counterpart: no lo hizo
// nadie y no va al registro (SPEC-CLAN-TERRITORIES §6).
func releaseClanTerritories(ctx context.Context, tx pgx.Tx, l ClanLeave) ([]territory.Control, error) {
	rows, err := tx.Query(ctx,
		`UPDATE territory_control
		    SET owner_type = 'NONE', owner_id = NULL, captured_at = NULL, version = version + 1
		  WHERE owner_type = 'CLAN' AND owner_id = $1
		RETURNING territory_id, owner_type, owner_id, control_points, contested, captured_at, version`,
		territory.ClanOwnerID(l.ClanID))
	if err != nil {
		return nil, fmt.Errorf("liberar los territorios del clan %d: %w", l.ClanID, normalize(err))
	}
	var out []territory.Control
	for rows.Next() {
		c, err := scanControl(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, c)
	}
	rows.Close()
	if err := normalize(rows.Err()); err != nil {
		return nil, fmt.Errorf("liberar los territorios del clan %d: %w", l.ClanID, err)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TerritoryID < out[j].TerritoryID })
	for _, c := range out {
		if err := appendEvent(ctx, tx, worldEvent{
			EventType: territory.EventControlChanged, Tick: l.Tick, PlayerID: &l.PlayerID,
			Payload: map[string]any{
				"territoryId": c.TerritoryID, "ownerType": string(territory.OwnerNone), "version": c.Version,
				"previousOwnerId": territory.ClanOwnerID(l.ClanID), "released": true, "atMs": l.AtMs,
			},
		}); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// clanTerritoryViews son los territorios de un clan, por id
// (SPEC-CLAN-TERRITORIES Q11).
func clanTerritoryViews(ctx context.Context, db DB, clanID int64) ([]protocol.ClanTerritoryView, error) {
	rows, err := db.Query(ctx,
		`SELECT t.id, t.name, (t.min_x + t.max_x) / 2, (t.min_y + t.max_y) / 2
		   FROM territory_control tc JOIN territories t ON t.id = tc.territory_id
		  WHERE tc.owner_type = 'CLAN' AND tc.owner_id = $1 ORDER BY t.id`, territory.ClanOwnerID(clanID))
	if err != nil {
		return nil, fmt.Errorf("leer los territorios del clan %d: %w", clanID, normalize(err))
	}
	defer rows.Close()
	out := []protocol.ClanTerritoryView{}
	for rows.Next() {
		var v protocol.ClanTerritoryView
		if err := rows.Scan(&v.ID, &v.Name, &v.X, &v.Y); err != nil {
			return nil, fmt.Errorf("leer un territorio del clan: %w", normalize(err))
		}
		out = append(out, v)
	}
	return out, normalize(rows.Err())
}

// ─────────────────────────────────────────────────────────────
// Canal y vista pública (los atiende la conexión)
// ─────────────────────────────────────────────────────────────

// openStay devuelve el clan de la estancia abierta de un jugador y su
// first_message_id, o clan.ErrNotMember.
func openStay(ctx context.Context, db DB, playerID uuid.UUID) (int64, int64, error) {
	var clanID, first int64
	err := db.QueryRow(ctx,
		`SELECT clan_id, first_message_id FROM clan_members WHERE player_id = $1 AND left_at IS NULL`,
		playerID).Scan(&clanID, &first)
	if errors.Is(normalize(err), ErrNotFound) {
		return 0, 0, clan.ErrNotMember
	}
	if err != nil {
		return 0, 0, fmt.Errorf("leer la estancia de %s: %w", playerID, normalize(err))
	}
	return clanID, first, nil
}

// ChatSend guarda un mensaje del canal del clan de from, con el candado del
// clan tomado y la pertenencia comprobada bajo él (INV-SEC-012), y devuelve los
// miembros abiertos a los que entregarlo. Un requestId repetido devuelve el
// mensaje que ya existía (INV-SEC-007).
func (r *ClanRepo) ChatSend(ctx context.Context, from uuid.UUID, body string, requestID uuid.UUID) (clan.Sent, error) {
	sent := clan.Sent{From: from, Body: body}
	err := r.store.InTx(ctx, func(tx pgx.Tx) error {
		clanID, _, err := openStay(ctx, tx, from)
		if err != nil {
			return err
		}
		if err := lockClan(ctx, tx, clanID); err != nil {
			return err
		}
		// Bajo el candado: la salida que lo esperaba ya confirmó, o no ha empezado.
		again, _, err := openStay(ctx, tx, from)
		if err != nil {
			return err
		}
		if again != clanID {
			return clan.ErrNotMember
		}
		sent.ClanID = clanID
		var at time.Time
		err = tx.QueryRow(ctx,
			`INSERT INTO clan_messages (clan_id, sender_player_id, body, request_id)
			 VALUES ($1, $2, $3, $4)
			 ON CONFLICT (sender_player_id, request_id) DO NOTHING
			 RETURNING id, sent_at`,
			clanID, from, body, requestID).Scan(&sent.ID, &at)
		if errors.Is(normalize(err), ErrNotFound) {
			found, err := r.existing(ctx, tx, from, requestID, &sent)
			if err == nil && !found {
				err = fmt.Errorf("el mensaje del requestId %s desapareció", requestID)
			}
			sent.Duplicate = true
			return err
		}
		if err != nil {
			return fmt.Errorf("guardar el mensaje del canal: %w", normalize(err))
		}
		sent.SentAtMs = at.UnixMilli()
		sent.Members, err = openMembers(ctx, tx, clanID)
		return err
	})
	if err != nil {
		return clan.Sent{}, err
	}
	if err := r.fillSender(ctx, &sent); err != nil {
		return clan.Sent{}, err
	}
	return sent, nil
}

// ChatExisting devuelve el mensaje que from ya guardó en algún canal con ese
// requestId, si lo hay: el reintento de un envío confirmado no choca con el
// límite de ritmo.
func (r *ClanRepo) ChatExisting(ctx context.Context, from, requestID uuid.UUID) (clan.Sent, bool, error) {
	sent := clan.Sent{From: from}
	found, err := r.existing(ctx, r.store.pool, from, requestID, &sent)
	if err != nil || !found {
		return clan.Sent{}, found, err
	}
	sent.Duplicate = true
	if err := r.fillSender(ctx, &sent); err != nil {
		return clan.Sent{}, false, err
	}
	return sent, true, nil
}

func (r *ClanRepo) existing(ctx context.Context, db DB, from, requestID uuid.UUID, sent *clan.Sent) (bool, error) {
	var at time.Time
	err := db.QueryRow(ctx,
		`SELECT id, clan_id, body, sent_at FROM clan_messages WHERE sender_player_id = $1 AND request_id = $2`,
		from, requestID).Scan(&sent.ID, &sent.ClanID, &sent.Body, &at)
	if errors.Is(normalize(err), ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("buscar el mensaje del requestId: %w", normalize(err))
	}
	sent.SentAtMs = at.UnixMilli()
	return true, nil
}

// fillSender pone la ciudad del remitente.
func (r *ClanRepo) fillSender(ctx context.Context, sent *clan.Sent) error {
	cities, err := citiesOfPlayers(ctx, r.store.pool, []uuid.UUID{sent.From})
	if err != nil {
		return err
	}
	if c := cities[sent.From]; c != nil {
		sent.FromCityID, sent.FromCityName = c.ID, c.Name
	}
	return nil
}

// openMembers son los miembros abiertos de un clan.
func openMembers(ctx context.Context, db DB, clanID int64) ([]uuid.UUID, error) {
	rows, err := db.Query(ctx,
		`SELECT player_id FROM clan_members WHERE clan_id = $1 AND left_at IS NULL ORDER BY joined_at, player_id`, clanID)
	if err != nil {
		return nil, fmt.Errorf("leer los miembros del clan: %w", normalize(err))
	}
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("leer un miembro: %w", normalize(err))
		}
		out = append(out, id)
	}
	return out, normalize(rows.Err())
}

// ChatPage sirve una página del canal del clan de me: la más reciente, o la de
// los mensajes con id menor que beforeID, sólo los posteriores a su entrada y de
// los últimos MaxAge (RN-CLAN-009, INV-SEC-012).
func (r *ClanRepo) ChatPage(ctx context.Context, me uuid.UUID, beforeID int64) (protocol.ClanChatPagePayload, error) {
	page := protocol.ClanChatPagePayload{Messages: []protocol.ClanMessageView{}}
	clanID, first, err := openStay(ctx, r.store.pool, me)
	if err != nil {
		return page, err
	}
	page.ClanID = clanID
	if beforeID <= 0 {
		beforeID = 1<<63 - 1
	}
	since := time.Now().Add(-r.cfg.MaxAge)
	rows, err := r.store.pool.Query(ctx,
		`SELECT m.id, m.sender_player_id, m.body, m.sent_at, c.id, c.name
		   FROM clan_messages m
		   LEFT JOIN LATERAL (SELECT id, name FROM cities WHERE owner_player_id = m.sender_player_id ORDER BY id LIMIT 1) c ON true
		  WHERE m.clan_id = $1 AND m.id > $2 AND m.id < $3 AND m.sent_at > $4
		  ORDER BY m.id DESC LIMIT $5`,
		clanID, first, beforeID, since, r.cfg.PageSize+1)
	if err != nil {
		return page, fmt.Errorf("leer el canal: %w", normalize(err))
	}
	defer rows.Close()
	var desc []protocol.ClanMessageView
	for rows.Next() {
		var (
			v        protocol.ClanMessageView
			from     uuid.UUID
			at       time.Time
			cityID   *int64
			cityName *string
		)
		if err := rows.Scan(&v.ID, &from, &v.Body, &at, &cityID, &cityName); err != nil {
			return page, fmt.Errorf("leer un mensaje del canal: %w", normalize(err))
		}
		v.ClanID, v.FromPlayerID, v.SentAtMs = clanID, from.String(), at.UnixMilli()
		v.FromCityID, v.FromCityName = cityID, cityName
		desc = append(desc, v)
	}
	if err := normalize(rows.Err()); err != nil {
		return page, err
	}
	if len(desc) > r.cfg.PageSize {
		desc = desc[:r.cfg.PageSize]
		next := desc[len(desc)-1].ID
		page.NextBeforeID = &next
	}
	for i := len(desc) - 1; i >= 0; i-- {
		page.Messages = append(page.Messages, desc[i])
	}
	err = r.store.pool.QueryRow(ctx,
		`SELECT coalesce((SELECT read_up_to_id FROM clan_message_reads WHERE player_id = $1 AND clan_id = $2), 0)`,
		me, clanID).Scan(&page.ReadUpToID)
	if err != nil {
		return page, fmt.Errorf("leer lo leído del canal: %w", normalize(err))
	}
	return page, nil
}

// ChatMarkRead da por leído el canal del clan de me hasta upTo(clanID), sólo
// hacia delante: upTo es el tope de lo que la sesión recibió de ese canal
// (RN-CLAN-009). Sin estancia abierta, clan.ErrNotMember; con tope 0, nada.
func (r *ClanRepo) ChatMarkRead(ctx context.Context, me uuid.UUID, upTo func(clanID int64) int64) error {
	clanID, _, err := openStay(ctx, r.store.pool, me)
	if err != nil {
		return err
	}
	n := upTo(clanID)
	if n < 1 {
		return nil
	}
	_, err = r.store.pool.Exec(ctx,
		`INSERT INTO clan_message_reads (player_id, clan_id, read_up_to_id) VALUES ($1, $2, $3)
		 ON CONFLICT (player_id, clan_id) DO UPDATE
		   SET read_up_to_id = GREATEST(clan_message_reads.read_up_to_id, EXCLUDED.read_up_to_id)`,
		me, clanID, n)
	if err != nil {
		return fmt.Errorf("guardar lo leído del canal: %w", normalize(err))
	}
	return nil
}

// Lookup es la vista pública de un clan vivo (RN-CLAN-008): su nombre, su sigla
// y sus miembros abiertos con su ciudad, su bando y su rango.
func (r *ClanRepo) Lookup(ctx context.Context, clanID int64) (protocol.ClanInfoPayload, error) {
	info := protocol.ClanInfoPayload{ClanID: clanID, Members: []protocol.ClanMemberView{},
		Territories: []protocol.ClanTerritoryView{}, Alliances: []protocol.ClanPublicAllianceView{}}
	var leader uuid.UUID
	err := r.store.pool.QueryRow(ctx,
		`SELECT name, tag, leader_player_id FROM clans WHERE id = $1 AND dissolved_at IS NULL`, clanID).
		Scan(&info.Name, &info.Tag, &leader)
	if errors.Is(normalize(err), ErrNotFound) {
		return info, clan.ErrUnknownClan
	}
	if err != nil {
		return info, fmt.Errorf("leer el clan %d: %w", clanID, normalize(err))
	}
	rows, err := r.store.pool.Query(ctx,
		`SELECT m.player_id, m.joined_at, m.rank, f.code, c.id, c.name
		   FROM clan_members m
		   JOIN players p ON p.id = m.player_id
		   JOIN factions f ON f.id = p.faction_id
		   LEFT JOIN LATERAL (SELECT id, name FROM cities WHERE owner_player_id = m.player_id ORDER BY id LIMIT 1) c ON true
		  WHERE m.clan_id = $1 AND m.left_at IS NULL
		  ORDER BY m.joined_at, m.player_id`, clanID)
	if err != nil {
		return info, fmt.Errorf("leer los miembros del clan: %w", normalize(err))
	}
	defer rows.Close()
	for rows.Next() {
		var (
			v      protocol.ClanMemberView
			id     uuid.UUID
			joined time.Time
		)
		if err := rows.Scan(&id, &joined, &v.Role, &v.Faction, &v.CityID, &v.CityName); err != nil {
			return info, fmt.Errorf("leer un miembro: %w", normalize(err))
		}
		v.PlayerID, v.JoinedAtMs = id.String(), joined.UnixMilli()
		if id == leader {
			v.Role = clan.RoleLeader
		}
		info.Members = append(info.Members, v)
	}
	if err := normalize(rows.Err()); err != nil {
		return info, err
	}
	rows.Close()
	ts, err := clanTerritoryViews(ctx, r.store.pool, clanID)
	if err != nil {
		return info, err
	}
	info.Territories = ts
	// RN-CLANALLY-007: sus alianzas activas son públicas.
	as, err := publicAlliances(ctx, r.store.pool, clanID)
	if err != nil {
		return info, err
	}
	info.Alliances = as
	return info, nil
}
