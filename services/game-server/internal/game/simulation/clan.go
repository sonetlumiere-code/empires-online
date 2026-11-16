package simulation

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/clan"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/eventlog"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/player"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/territory"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// Los clanes en la simulación (SPEC-CLANS, ADR-040).
//
// La pertenencia vive aquí porque decide la paz (ADR-040 §1). Fundar, invitar y
// entrar van en dos fases, como faction.join: el tick valida y reserva, la cola
// escribe y el buzón confirma o deshace; nada cuenta hasta la confirmación.
// Salir y expulsar son optimistas: la RAM cierra la estancia en el acto y un
// único trabajo lo escribe, con su reencolado (ADR-040 §2). La paz residual se
// deriva de las estancias cerradas recientes (ADR-040 §3). El canal no pasa por
// aquí: lo atiende la conexión (ADR-040 §5).

// ErrClanConflict: la base no admite la orden porque el clan, la invitación o
// el jugador cambiaron por otra vía. Es un rechazo de dominio: no se reintenta.
// El texto que sigue a «: » es el motivo que ve el jugador.
var ErrClanConflict = errors.New("el clan cambió")

// ClanRejection es un rechazo de la base con el motivo que ve el jugador.
type ClanRejection struct{ Reason string }

func (r ClanRejection) Error() string { return r.Reason }

// Is hace que errors.Is(err, ErrClanConflict) reconozca un rechazo.
func (r ClanRejection) Is(target error) bool { return target == ErrClanConflict }

// ClanStore escribe los clanes. Lo implementa persistence.ClanStore. Cada
// escritura es idempotente: la cola puede reintentar.
type ClanStore interface {
	// Create funda el clan con su líder y devuelve su id. Un nombre o una sigla
	// ocupados son un ClanRejection.
	Create(ctx context.Context, o ClanCreateOrder) (int64, error)
	// Invite escribe o borra una invitación.
	Invite(ctx context.Context, o ClanInviteOrder) error
	// Join abre la estancia del invitado, si el clan, la invitación, el tope y
	// los bandos lo permiten todavía.
	Join(ctx context.Context, o ClanJoinOrder) error
	// Leave cierra una estancia con el líder nuevo o la disolución, de forma
	// tolerante (ADR-040 §2). Una disolución libera en la misma transacción
	// los territorios del clan y los devuelve (ADR-041 §4).
	Leave(ctx context.Context, l ClanLeave) ([]territory.Control, error)
}

// ClanCreateOrder es una fundación ya validada por el tick.
type ClanCreateOrder struct {
	PlayerID  uuid.UUID
	RequestID string
	Name      string
	Tag       string
	At        time.Time
	Tick      uint64
	AtMs      int64
}

// ClanInviteOrder es una invitación (o su retirada, o su rechazo) ya validada.
type ClanInviteOrder struct {
	ClanID    int64
	PlayerID  uuid.UUID
	InvitedBy uuid.UUID
	// Remove: borrar la invitación en lugar de escribirla. Declined dice si la
	// borra el invitado al rechazarla y no el líder al retirarla.
	Remove    bool
	Declined  bool
	At        time.Time
	ExpiresAt time.Time
	Tick      uint64
	AtMs      int64
}

// ClanJoinOrder es la entrada de un invitado ya validada por el tick.
type ClanJoinOrder struct {
	ClanID     int64
	PlayerID   uuid.UUID
	MaxMembers int
	At         time.Time
	Tick       uint64
	AtMs       int64
}

// ClanLeave es una salida o una expulsión ya aplicada en la RAM.
type ClanLeave struct {
	ClanID   int64
	PlayerID uuid.UUID
	// Reason es clan.ReasonLeft o clan.ReasonExpelled; By, el líder que
	// expulsó (uuid.Nil en una salida).
	Reason string
	By     uuid.UUID
	At     time.Time
	// NewLeader es el líder que hereda, o uuid.Nil si no cambia; Dissolve, si
	// el clan se queda sin nadie.
	NewLeader uuid.UUID
	Dissolve  bool
	Tick      uint64
	AtMs      int64
}

// Comandos de los clanes (protocolo v1).

// CreateClan funda un clan (clan.create).
type CreateClan struct {
	PlayerID  uuid.UUID
	SessionID uuid.UUID
	RequestID string
	Name      string
	Tag       string
}

func (CreateClan) commandType() string { return protocol.TypeClanCreate }

// InviteToClan invita a un jugador al clan del líder, o retira la invitación
// (clan.invite).
type InviteToClan struct {
	PlayerID  uuid.UUID
	SessionID uuid.UUID
	RequestID string
	TargetID  uuid.UUID
	Invited   bool
}

func (InviteToClan) commandType() string { return protocol.TypeClanInvite }

// RespondClan acepta o rechaza una invitación (clan.respond).
type RespondClan struct {
	PlayerID  uuid.UUID
	SessionID uuid.UUID
	RequestID string
	ClanID    int64
	Accept    bool
}

func (RespondClan) commandType() string { return protocol.TypeClanRespond }

// LeaveClan sale del clan propio (clan.leave).
type LeaveClan struct {
	PlayerID  uuid.UUID
	SessionID uuid.UUID
	RequestID string
}

func (LeaveClan) commandType() string { return protocol.TypeClanLeave }

// ExpelFromClan expulsa a un miembro del clan del líder (clan.expel).
type ExpelFromClan struct {
	PlayerID  uuid.UUID
	SessionID uuid.UUID
	RequestID string
	TargetID  uuid.UUID
}

func (ExpelFromClan) commandType() string { return protocol.TypeClanExpel }

// ClanCreateResolved lo devuelve la cola con el resultado de una fundación.
// Code vacío: el clan existe con el id ClanID.
type ClanCreateResolved struct {
	Order   ClanCreateOrder
	ClanID  int64
	Code    string
	Message string
}

func (ClanCreateResolved) commandType() string { return "clan.create_resolved" }

// ClanInviteResolved lo devuelve la cola con el resultado de una invitación.
type ClanInviteResolved struct {
	Order     ClanInviteOrder
	RequestID string
	Code      string
	Message   string
}

func (ClanInviteResolved) commandType() string { return "clan.invite_resolved" }

// ClanJoinResolved lo devuelve la cola con el resultado de una entrada.
type ClanJoinResolved struct {
	Order     ClanJoinOrder
	RequestID string
	Code      string
	Message   string
}

func (ClanJoinResolved) commandType() string { return "clan.join_resolved" }

// ClanLeavePersistFailed es la compensación de una salida que no se escribió:
// el volcado siguiente la reencola (ADR-022).
type ClanLeavePersistFailed struct {
	Leave ClanLeave
}

func (ClanLeavePersistFailed) commandType() string { return "clan.leave_failed" }

// Nombres de los trabajos de los clanes (catálogo JobNames).
const (
	JobClanCreate = "clan.create"
	JobClanInvite = "clan.invite"
	JobClanJoin   = "clan.join"
	JobClanLeave  = "clan.leave"
)

// ClanKey es la clave de la cola de las escrituras de un clan: las entradas,
// las salidas y las invitaciones de un clan se ordenan entre sí.
func ClanKey(id int64) string { return "clan:" + strconv.FormatInt(id, 10) }

// Mensajes de los rechazos por el clan.
const (
	msgClanPeace = "sois del mismo clan"
	msgClanMixes = "un clan no junta a la Orden y al Caos"
)

// ─────────────────────────────────────────────────────────────
// Estado
// ─────────────────────────────────────────────────────────────

// clanRecord es un clan vivo en la RAM.
type clanRecord struct {
	ID       int64
	Name     string
	Tag      string
	LeaderID uuid.UUID
	// Members es el instante de entrada de cada miembro confirmado.
	Members map[uuid.UUID]int64
	// dissolving: la última salida de un clan con territorios espera su
	// transacción (ADR-041 §4); el clan rechaza entradas mientras tanto.
	dissolving bool
}

type inviteKey struct {
	ClanID   int64
	PlayerID uuid.UUID
}

type clanInvite struct {
	InvitedBy   uuid.UUID
	ExpiresAtMs int64
}

// clanState son los clanes vivos, la pertenencia, las invitaciones, las
// salidas recientes y las reservas de las órdenes en vuelo.
type clanState struct {
	clans    map[int64]*clanRecord
	memberOf map[uuid.UUID]int64
	invites  map[inviteKey]clanInvite
	// departed son las estancias cerradas hace menos de grace, de las que sale
	// la paz residual (ADR-040 §3).
	departed []clan.Stay
	grace    time.Duration

	// Reservas: fundaciones, entradas e invitaciones cuya transacción no ha
	// vuelto. Cuentan para el tope y para la regla de bandos.
	creating map[uuid.UUID]bool
	joining  map[uuid.UUID]int64
	inviting map[inviteKey]bool

	// pendingLeaves son las salidas cuya escritura se descartó (ADR-022).
	pendingLeaves map[string]ClanLeave
}

func newClanState() clanState {
	return clanState{
		clans:         make(map[int64]*clanRecord),
		memberOf:      make(map[uuid.UUID]int64),
		invites:       make(map[inviteKey]clanInvite),
		grace:         clan.DefaultPeaceGrace,
		creating:      make(map[uuid.UUID]bool),
		joining:       make(map[uuid.UUID]int64),
		inviting:      make(map[inviteKey]bool),
		pendingLeaves: make(map[string]ClanLeave),
	}
}

// SetClans instala los clanes vivos, las estancias abiertas y las cerradas
// recientes, y las invitaciones vivas, al arrancar. Va después de cargar las
// ciudades: marca el clan en la ciudad de cada miembro. grace es la paz
// residual (cero: el valor por defecto).
func (s *State) SetClans(clans []clan.Clan, stays []clan.Stay, invites []clan.Invite, grace time.Duration) {
	s.clans = newClanState()
	if grace > 0 {
		s.clans.grace = grace
	}
	for _, c := range clans {
		s.clans.clans[c.ID] = &clanRecord{
			ID: c.ID, Name: c.Name, Tag: c.Tag, LeaderID: c.LeaderID, Members: make(map[uuid.UUID]int64),
		}
	}
	for _, st := range stays {
		if !st.Open() {
			s.clans.departed = append(s.clans.departed, st)
			continue
		}
		rec, ok := s.clans.clans[st.ClanID]
		if !ok {
			continue
		}
		rec.Members[st.PlayerID] = st.JoinedAtMs
		s.clans.memberOf[st.PlayerID] = st.ClanID
		if c, ok := s.CityOf(st.PlayerID); ok {
			c.Clan = &city.ClanTag{ID: rec.ID, Tag: rec.Tag}
		}
	}
	for _, inv := range invites {
		if _, ok := s.clans.clans[inv.ClanID]; ok {
			s.clans.invites[inviteKey{inv.ClanID, inv.PlayerID}] = clanInvite{InvitedBy: inv.InvitedBy, ExpiresAtMs: inv.ExpiresAtMs}
		}
	}
}

// ClanOf es el clan confirmado de un jugador, si lo tiene.
func (s *State) ClanOf(playerID uuid.UUID) (int64, bool) {
	id, ok := s.clans.memberOf[playerID]
	return id, ok
}

// ClanMembers son los miembros confirmados de un clan vivo, por antigüedad
// (tests y vistas).
func (s *State) ClanMembers(clanID int64) []uuid.UUID {
	rec, ok := s.clans.clans[clanID]
	if !ok {
		return nil
	}
	ms := rec.members()
	out := make([]uuid.UUID, 0, len(ms))
	for _, m := range ms {
		out = append(out, m.PlayerID)
	}
	return out
}

// ClanLeader es el líder de un clan vivo.
func (s *State) ClanLeader(clanID int64) (uuid.UUID, bool) {
	rec, ok := s.clans.clans[clanID]
	if !ok {
		return uuid.Nil, false
	}
	return rec.LeaderID, true
}

// ClanPeace dice si dos jugadores están en paz por su clan (RN-CLAN-004,
// RN-CLAN-005): los dos son miembros del mismo clan, o queda paz residual entre
// ellos por una salida reciente.
func (s *State) ClanPeace(x, y uuid.UUID, now time.Time) bool {
	if x == y {
		return false
	}
	cx, okx := s.clans.memberOf[x]
	cy, oky := s.clans.memberOf[y]
	if okx && oky && cx == cy {
		return true
	}
	if len(s.clans.departed) == 0 {
		return false
	}
	stays := make([]clan.Stay, 0, 4)
	for _, p := range []uuid.UUID{x, y} {
		if id, ok := s.clans.memberOf[p]; ok {
			stays = append(stays, clan.Stay{ClanID: id, PlayerID: p, JoinedAtMs: s.clans.clans[id].Members[p]})
		}
	}
	for _, d := range s.clans.departed {
		if d.PlayerID == x || d.PlayerID == y {
			stays = append(stays, d)
		}
	}
	return clan.ResidualPeace(x, y, stays, now.UnixMilli(), s.clans.grace)
}

func (r *clanRecord) members() []clan.Member {
	ms := make([]clan.Member, 0, len(r.Members))
	for id, at := range r.Members {
		ms = append(ms, clan.Member{PlayerID: id, JoinedAtMs: at})
	}
	clan.SortMembers(ms)
	return ms
}

// inClanOrPending dice si un jugador está en un clan o tiene una fundación o
// una entrada en vuelo, y el motivo del rechazo.
func (s *State) inClanOrPending(playerID uuid.UUID) (bool, string) {
	if _, ok := s.clans.memberOf[playerID]; ok {
		return true, "ya estás en un clan"
	}
	if s.clans.creating[playerID] {
		return true, "ya estás fundando un clan"
	}
	if _, ok := s.clans.joining[playerID]; ok {
		return true, "ya estás entrando en un clan"
	}
	return false, ""
}

// liveInvite es la invitación viva de un jugador a un clan.
func (s *State) liveInvite(k inviteKey, nowMs int64) (clanInvite, bool) {
	inv, ok := s.clans.invites[k]
	if !ok || inv.ExpiresAtMs <= nowMs {
		return clanInvite{}, false
	}
	return inv, true
}

// stanceOf es la postura de un jugador para la regla de bandos: su facción
// efectiva y su entrada pendiente, confirmada o en vuelo.
func (s *State) stanceOf(playerID uuid.UUID) clan.Stance {
	st := clan.Stance{Faction: s.FactionOf(playerID)}
	if c, ok := s.CityOf(playerID); ok && c.FactionJoin != nil {
		st.Joining = c.FactionJoin.Faction
	}
	if j, ok := s.factionJoining[playerID]; ok {
		st.Joining = j.Faction
	}
	return st
}

// clanStances son las posturas de los miembros de un clan y de quienes están
// entrando en él, salvo except.
func (s *State) clanStances(clanID int64, except uuid.UUID) []clan.Stance {
	var out []clan.Stance
	if rec, ok := s.clans.clans[clanID]; ok {
		for id := range rec.Members {
			if id != except {
				out = append(out, s.stanceOf(id))
			}
		}
	}
	for id, c := range s.clans.joining {
		if c == clanID && id != except {
			out = append(out, s.stanceOf(id))
		}
	}
	return out
}

// clanOccupancy es lo que ocupa un clan frente al tope: sus miembros, las
// entradas en vuelo y, si countInvites, las invitaciones vivas y en vuelo.
func (s *State) clanOccupancy(clanID int64, nowMs int64, countInvites bool) int {
	n := 0
	if rec, ok := s.clans.clans[clanID]; ok {
		n += len(rec.Members)
	}
	for _, c := range s.clans.joining {
		if c == clanID {
			n++
		}
	}
	if countInvites {
		for k := range s.clans.invites {
			if _, viva := s.liveInvite(k, nowMs); viva && k.ClanID == clanID {
				n++
			}
		}
		for k := range s.clans.inviting {
			if k.ClanID == clanID {
				n++
			}
		}
	}
	return n
}

// ─────────────────────────────────────────────────────────────
// Configuración
// ─────────────────────────────────────────────────────────────

func (s *Simulation) clanMaxMembers() int {
	if s.deps.ClanMaxMembers > 0 {
		return s.deps.ClanMaxMembers
	}
	return clan.DefaultMaxMembers
}

func (s *Simulation) clanInviteTTL() time.Duration {
	if s.deps.ClanInviteTTL > 0 {
		return s.deps.ClanInviteTTL
	}
	return clan.DefaultInviteTTL
}

// ─────────────────────────────────────────────────────────────
// Vistas
// ─────────────────────────────────────────────────────────────

// cityClanView es el clan de una ciudad para la red, o nil.
func cityClanView(c *city.City) *protocol.CityClanView {
	if c.Clan == nil {
		return nil
	}
	return &protocol.CityClanView{ID: c.Clan.ID, Tag: c.Clan.Tag}
}

// cityRef es la ciudad de un jugador para las vistas, o nil.
func (s *State) cityRef(playerID uuid.UUID) (*int64, *string) {
	c, ok := s.CityOf(playerID)
	if !ok {
		return nil, nil
	}
	id, name := c.ID, c.Name
	return &id, &name
}

// clanView es el clan visto por uno de sus miembros; las invitaciones enviadas,
// sólo para el líder.
func (s *State) clanView(clanID int64, viewer uuid.UUID, nowMs int64) *protocol.ClanView {
	rec, ok := s.clans.clans[clanID]
	if !ok {
		return nil
	}
	v := &protocol.ClanView{
		ID: rec.ID, Name: rec.Name, Tag: rec.Tag, LeaderID: rec.LeaderID.String(),
		Members: []protocol.ClanMemberView{}, SentInvitations: []protocol.ClanSentInvitationView{},
		Territories: s.clanTerritoryViews(rec.ID),
	}
	for _, m := range rec.members() {
		role := clan.RoleMember
		if m.PlayerID == rec.LeaderID {
			role = clan.RoleLeader
		}
		cid, cname := s.cityRef(m.PlayerID)
		v.Members = append(v.Members, protocol.ClanMemberView{
			PlayerID: m.PlayerID.String(), CityID: cid, CityName: cname,
			Faction: s.FactionOf(m.PlayerID), Role: role, JoinedAtMs: m.JoinedAtMs,
		})
	}
	if viewer == rec.LeaderID {
		keys := make([]inviteKey, 0)
		for k := range s.clans.invites {
			if _, viva := s.liveInvite(k, nowMs); viva && k.ClanID == clanID {
				keys = append(keys, k)
			}
		}
		sort.Slice(keys, func(i, j int) bool { return keys[i].PlayerID.String() < keys[j].PlayerID.String() })
		for _, k := range keys {
			cid, cname := s.cityRef(k.PlayerID)
			v.SentInvitations = append(v.SentInvitations, protocol.ClanSentInvitationView{
				PlayerID: k.PlayerID.String(), CityID: cid, CityName: cname, ExpiresAtMs: s.clans.invites[k].ExpiresAtMs,
			})
		}
	}
	return v
}

// ClanUpdateFor es el clan de un jugador y sus invitaciones recibidas vivas,
// por id de clan.
func (s *State) ClanUpdateFor(playerID uuid.UUID, nowMs int64) protocol.ClanUpdatePayload {
	p := protocol.ClanUpdatePayload{Invitations: []protocol.ClanInvitationView{}}
	if id, ok := s.clans.memberOf[playerID]; ok {
		p.Clan = s.clanView(id, playerID, nowMs)
	}
	keys := make([]inviteKey, 0)
	for k := range s.clans.invites {
		if _, viva := s.liveInvite(k, nowMs); viva && k.PlayerID == playerID {
			keys = append(keys, k)
		}
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].ClanID < keys[j].ClanID })
	for _, k := range keys {
		rec, ok := s.clans.clans[k.ClanID]
		if !ok {
			continue
		}
		p.Invitations = append(p.Invitations, protocol.ClanInvitationView{
			ClanID: rec.ID, Name: rec.Name, Tag: rec.Tag, ExpiresAtMs: s.clans.invites[k].ExpiresAtMs,
		})
	}
	return p
}

// sendClanUpdate manda clan.update a todas las sesiones de un jugador; la que
// dio la orden lo recibe con su requestId.
func (s *Simulation) sendClanUpdate(playerID uuid.UUID, requestID string) {
	s.deps.Broadcaster.SendToPlayer(playerID, protocol.TypeClanUpdate, requestID,
		s.state.ClanUpdateFor(playerID, s.deps.Clock.NowMs()))
}

// sendClanUpdates manda clan.update a cada jugador una vez, en orden de id;
// requestID va sólo a actor.
func (s *Simulation) sendClanUpdates(players []uuid.UUID, actor uuid.UUID, requestID string) {
	seen := make(map[uuid.UUID]bool, len(players))
	uniq := make([]uuid.UUID, 0, len(players))
	for _, p := range players {
		if p != uuid.Nil && !seen[p] {
			seen[p] = true
			uniq = append(uniq, p)
		}
	}
	sort.Slice(uniq, func(i, j int) bool { return uniq[i].String() < uniq[j].String() })
	for _, p := range uniq {
		req := ""
		if p == actor {
			req = requestID
		}
		s.sendClanUpdate(p, req)
	}
}

// setCityClan cambia el clan de la ciudad de un jugador y la difunde a su chunk
// y a su dueño. Sin requestId: la respuesta a la orden es el clan.update.
func (s *Simulation) setCityClan(playerID uuid.UUID, tag *city.ClanTag) {
	c, ok := s.state.CityOf(playerID)
	if !ok {
		return
	}
	c.Clan = tag
	view := cityClanView(c)
	s.broadcastFaction(c, protocol.CityUpdatePayload{Clan: &view}, "")
}

// ─────────────────────────────────────────────────────────────
// clan.create (dos fases)
// ─────────────────────────────────────────────────────────────

func (s *Simulation) handleCreateClan(cmd CreateClan) {
	if err := clan.ValidateName(cmd.Name); err != nil {
		s.economyError(cmd.PlayerID, cmd.RequestID, protocol.CodeInvalidMessage, err.Error())
		return
	}
	if err := clan.ValidateTag(cmd.Tag); err != nil {
		s.economyError(cmd.PlayerID, cmd.RequestID, protocol.CodeInvalidMessage, err.Error())
		return
	}
	if s.deps.Clans == nil {
		s.economyError(cmd.PlayerID, cmd.RequestID, protocol.CodeInternalError, "los clanes no están disponibles")
		return
	}
	if _, ok := s.state.CityOf(cmd.PlayerID); !ok {
		s.economyError(cmd.PlayerID, cmd.RequestID, protocol.CodeInvalidTarget, "no tienes ciudad")
		return
	}
	if busy, why := s.state.inClanOrPending(cmd.PlayerID); busy {
		s.economyError(cmd.PlayerID, cmd.RequestID, protocol.CodeInvalidTarget, why)
		return
	}
	for _, rec := range s.state.clans.clans {
		if clan.NameKey(rec.Name) == clan.NameKey(cmd.Name) {
			s.economyError(cmd.PlayerID, cmd.RequestID, protocol.CodeInvalidTarget, "ese nombre ya existe")
			return
		}
		if rec.Tag == cmd.Tag {
			s.economyError(cmd.PlayerID, cmd.RequestID, protocol.CodeInvalidTarget, "esa sigla ya existe")
			return
		}
	}

	nowMs := s.deps.Clock.NowMs()
	s.state.clans.creating[cmd.PlayerID] = true
	order := ClanCreateOrder{
		PlayerID: cmd.PlayerID, RequestID: cmd.RequestID, Name: cmd.Name, Tag: cmd.Tag,
		At: time.UnixMilli(nowMs).UTC(), Tick: s.state.Tick(), AtMs: nowMs,
	}
	store := s.deps.Clans
	s.deps.Persister.Submit(Job{
		Name: JobClanCreate,
		Key:  PlayerKey(cmd.PlayerID),
		Run: func(ctx context.Context) error {
			id, err := store.Create(ctx, order)
			if err == nil {
				s.reinject(ClanCreateResolved{Order: order, ClanID: id})
				return nil
			}
			if errors.Is(err, ErrClanConflict) {
				s.reinject(ClanCreateResolved{Order: order, Code: protocol.CodeInvalidTarget, Message: err.Error()})
				return nil
			}
			return err
		},
		OnPermanentFailure: func(err error) {
			s.deps.Log.Error("no se pudo fundar un clan", "err", err)
			s.reinject(ClanCreateResolved{Order: order, Code: protocol.CodeInternalError, Message: "no se pudo fundar el clan"})
		},
	})
}

func (s *Simulation) handleClanCreateResolved(cmd ClanCreateResolved) {
	o := cmd.Order
	if !s.state.clans.creating[o.PlayerID] {
		return
	}
	delete(s.state.clans.creating, o.PlayerID)
	if cmd.Code != "" {
		s.economyError(o.PlayerID, o.RequestID, cmd.Code, cmd.Message)
		return
	}
	rec := &clanRecord{
		ID: cmd.ClanID, Name: o.Name, Tag: o.Tag, LeaderID: o.PlayerID,
		Members: map[uuid.UUID]int64{o.PlayerID: o.AtMs},
	}
	s.state.clans.clans[rec.ID] = rec
	s.state.clans.memberOf[o.PlayerID] = rec.ID
	notify := s.dropInvitesOf(o.PlayerID)
	s.setCityClan(o.PlayerID, &city.ClanTag{ID: rec.ID, Tag: rec.Tag})
	s.sendClanUpdates(append(notify, o.PlayerID), o.PlayerID, o.RequestID)
	s.deps.Log.Info("clan fundado", "clan_id", rec.ID, "tag", rec.Tag, "player_id", o.PlayerID.String())
}

// dropInvitesOf borra de la RAM las invitaciones recibidas por un jugador que
// acaba de entrar en un clan (la base las borra en la misma transacción), y
// devuelve los líderes que tienen que ver su lista sin ellas.
func (s *Simulation) dropInvitesOf(playerID uuid.UUID) []uuid.UUID {
	var leaders []uuid.UUID
	for k := range s.state.clans.invites {
		if k.PlayerID != playerID {
			continue
		}
		delete(s.state.clans.invites, k)
		if rec, ok := s.state.clans.clans[k.ClanID]; ok {
			leaders = append(leaders, rec.LeaderID)
		}
	}
	return leaders
}

// ─────────────────────────────────────────────────────────────
// clan.invite (dos fases; la retirada, optimista)
// ─────────────────────────────────────────────────────────────

// leaderClan es el clan del que un jugador es líder.
func (s *State) leaderClan(playerID uuid.UUID) (*clanRecord, bool) {
	id, ok := s.clans.memberOf[playerID]
	if !ok {
		return nil, false
	}
	rec := s.clans.clans[id]
	if rec == nil || rec.LeaderID != playerID {
		return nil, false
	}
	return rec, true
}

func (s *Simulation) handleInviteToClan(cmd InviteToClan) {
	if s.deps.Clans == nil {
		s.economyError(cmd.PlayerID, cmd.RequestID, protocol.CodeInternalError, "los clanes no están disponibles")
		return
	}
	rec, ok := s.state.leaderClan(cmd.PlayerID)
	if !ok {
		s.economyError(cmd.PlayerID, cmd.RequestID, protocol.CodeInvalidTarget, "sólo el líder de un clan invita")
		return
	}
	if rec.dissolving {
		s.economyError(cmd.PlayerID, cmd.RequestID, protocol.CodeInvalidTarget, "el clan se está disolviendo")
		return
	}
	if cmd.TargetID == cmd.PlayerID {
		s.economyError(cmd.PlayerID, cmd.RequestID, protocol.CodeInvalidTarget, "no puedes invitarte a ti mismo")
		return
	}
	nowMs := s.deps.Clock.NowMs()
	k := inviteKey{ClanID: rec.ID, PlayerID: cmd.TargetID}

	if !cmd.Invited {
		inv, viva := s.state.liveInvite(k, nowMs)
		if !viva {
			s.economyError(cmd.PlayerID, cmd.RequestID, protocol.CodeInvalidTarget, "no hay ninguna invitación que retirar")
			return
		}
		delete(s.state.clans.invites, k)
		s.submitInviteRemoval(ClanInviteOrder{
			ClanID: rec.ID, PlayerID: cmd.TargetID, InvitedBy: inv.InvitedBy, Remove: true,
			At: time.UnixMilli(nowMs).UTC(), Tick: s.state.Tick(), AtMs: nowMs,
		})
		s.sendClanUpdates([]uuid.UUID{cmd.PlayerID, cmd.TargetID}, cmd.PlayerID, cmd.RequestID)
		return
	}

	if _, ok := s.state.CityOf(cmd.TargetID); !ok {
		s.economyError(cmd.PlayerID, cmd.RequestID, protocol.CodeInvalidTarget, "ese jugador no existe")
		return
	}
	if busy, _ := s.state.inClanOrPending(cmd.TargetID); busy {
		s.economyError(cmd.PlayerID, cmd.RequestID, protocol.CodeInvalidTarget, "ese jugador ya está en un clan")
		return
	}
	if _, viva := s.state.liveInvite(k, nowMs); viva || s.state.clans.inviting[k] {
		s.economyError(cmd.PlayerID, cmd.RequestID, protocol.CodeInvalidTarget, "ese jugador ya está invitado")
		return
	}
	if s.state.clanOccupancy(rec.ID, nowMs, true) >= s.clanMaxMembers() {
		s.economyError(cmd.PlayerID, cmd.RequestID, protocol.CodeInvalidTarget, "el clan está lleno")
		return
	}
	// RN-CLAN-006 e INV-CLAN-002.
	if clan.Mixes(append(s.state.clanStances(rec.ID, uuid.Nil), s.state.stanceOf(cmd.TargetID))...) {
		s.economyError(cmd.PlayerID, cmd.RequestID, protocol.CodeInvalidTarget, msgClanMixes)
		return
	}

	s.state.clans.inviting[k] = true
	at := time.UnixMilli(nowMs).UTC()
	order := ClanInviteOrder{
		ClanID: rec.ID, PlayerID: cmd.TargetID, InvitedBy: cmd.PlayerID,
		At: at, ExpiresAt: at.Add(s.clanInviteTTL()), Tick: s.state.Tick(), AtMs: nowMs,
	}
	store := s.deps.Clans
	requestID := cmd.RequestID
	s.deps.Persister.Submit(Job{
		Name: JobClanInvite,
		Key:  ClanKey(rec.ID),
		Run: func(ctx context.Context) error {
			err := store.Invite(ctx, order)
			if err == nil {
				s.reinject(ClanInviteResolved{Order: order, RequestID: requestID})
				return nil
			}
			if errors.Is(err, ErrClanConflict) {
				s.reinject(ClanInviteResolved{Order: order, RequestID: requestID, Code: protocol.CodeInvalidTarget, Message: err.Error()})
				return nil
			}
			return err
		},
		OnPermanentFailure: func(err error) {
			s.deps.Log.Error("no se pudo escribir una invitación a un clan", "err", err)
			s.reinject(ClanInviteResolved{Order: order, RequestID: requestID, Code: protocol.CodeInternalError, Message: "no se pudo invitar"})
		},
	})
}

func (s *Simulation) handleClanInviteResolved(cmd ClanInviteResolved) {
	o := cmd.Order
	k := inviteKey{ClanID: o.ClanID, PlayerID: o.PlayerID}
	if !s.state.clans.inviting[k] {
		return
	}
	delete(s.state.clans.inviting, k)
	if cmd.Code != "" {
		s.economyError(o.InvitedBy, cmd.RequestID, cmd.Code, cmd.Message)
		return
	}
	if _, vivo := s.state.clans.clans[o.ClanID]; !vivo {
		// El clan se disolvió mientras la invitación se escribía: la disolución
		// ya la borró de la base (misma clave de la cola).
		s.economyError(o.InvitedBy, cmd.RequestID, protocol.CodeInvalidTarget, "el clan ya no existe")
		return
	}
	s.state.clans.invites[k] = clanInvite{InvitedBy: o.InvitedBy, ExpiresAtMs: o.ExpiresAt.UnixMilli()}
	leader, _ := s.state.ClanLeader(o.ClanID)
	s.sendClanUpdates([]uuid.UUID{o.InvitedBy, leader, o.PlayerID}, o.InvitedBy, cmd.RequestID)
}

// submitInviteRemoval borra una invitación de la base. Es optimista: la RAM ya
// la borró. Si la escritura se pierde del todo, la invitación vuelve con un
// reinicio hasta que caduca; no da paz ni acceso a nada (la entrada se vuelve a
// validar en la base).
func (s *Simulation) submitInviteRemoval(o ClanInviteOrder) {
	if s.deps.Clans == nil {
		return
	}
	store := s.deps.Clans
	s.deps.Persister.Submit(Job{
		Name: JobClanInvite,
		Key:  ClanKey(o.ClanID),
		Run:  func(ctx context.Context) error { return store.Invite(ctx, o) },
		OnPermanentFailure: func(err error) {
			s.deps.Log.Error("no se pudo borrar una invitación a un clan; caducará sola",
				"err", err, "clan_id", o.ClanID, "player_id", o.PlayerID.String())
		},
	})
}

// ─────────────────────────────────────────────────────────────
// clan.respond (rechazar, optimista; aceptar, dos fases)
// ─────────────────────────────────────────────────────────────

func (s *Simulation) handleRespondClan(cmd RespondClan) {
	if s.deps.Clans == nil {
		s.economyError(cmd.PlayerID, cmd.RequestID, protocol.CodeInternalError, "los clanes no están disponibles")
		return
	}
	nowMs := s.deps.Clock.NowMs()
	k := inviteKey{ClanID: cmd.ClanID, PlayerID: cmd.PlayerID}
	inv, viva := s.state.liveInvite(k, nowMs)
	rec, vivo := s.state.clans.clans[cmd.ClanID]
	if !viva || !vivo {
		s.economyError(cmd.PlayerID, cmd.RequestID, protocol.CodeInvalidTarget, "la invitación no existe o caducó")
		return
	}

	if !cmd.Accept {
		delete(s.state.clans.invites, k)
		s.submitInviteRemoval(ClanInviteOrder{
			ClanID: cmd.ClanID, PlayerID: cmd.PlayerID, InvitedBy: inv.InvitedBy, Remove: true, Declined: true,
			At: time.UnixMilli(nowMs).UTC(), Tick: s.state.Tick(), AtMs: nowMs,
		})
		s.sendClanUpdates([]uuid.UUID{cmd.PlayerID, rec.LeaderID}, cmd.PlayerID, cmd.RequestID)
		return
	}

	if busy, why := s.state.inClanOrPending(cmd.PlayerID); busy {
		s.economyError(cmd.PlayerID, cmd.RequestID, protocol.CodeInvalidTarget, why)
		return
	}
	if rec.dissolving {
		s.economyError(cmd.PlayerID, cmd.RequestID, protocol.CodeInvalidTarget, "el clan se está disolviendo")
		return
	}
	if s.state.clanOccupancy(rec.ID, nowMs, false) >= s.clanMaxMembers() {
		s.economyError(cmd.PlayerID, cmd.RequestID, protocol.CodeInvalidTarget, "el clan está lleno")
		return
	}
	// RN-CLAN-006 e INV-CLAN-002.
	if clan.Mixes(append(s.state.clanStances(rec.ID, cmd.PlayerID), s.state.stanceOf(cmd.PlayerID))...) {
		s.economyError(cmd.PlayerID, cmd.RequestID, protocol.CodeInvalidTarget, msgClanMixes)
		return
	}

	s.state.clans.joining[cmd.PlayerID] = rec.ID
	order := ClanJoinOrder{
		ClanID: rec.ID, PlayerID: cmd.PlayerID, MaxMembers: s.clanMaxMembers(),
		At: time.UnixMilli(nowMs).UTC(), Tick: s.state.Tick(), AtMs: nowMs,
	}
	store := s.deps.Clans
	requestID := cmd.RequestID
	s.deps.Persister.Submit(Job{
		Name: JobClanJoin,
		Key:  ClanKey(rec.ID),
		Run: func(ctx context.Context) error {
			err := store.Join(ctx, order)
			if err == nil {
				s.reinject(ClanJoinResolved{Order: order, RequestID: requestID})
				return nil
			}
			if errors.Is(err, ErrClanConflict) {
				s.reinject(ClanJoinResolved{Order: order, RequestID: requestID, Code: protocol.CodeInvalidTarget, Message: err.Error()})
				return nil
			}
			return err
		},
		OnPermanentFailure: func(err error) {
			s.deps.Log.Error("no se pudo escribir la entrada en un clan", "err", err)
			s.reinject(ClanJoinResolved{Order: order, RequestID: requestID, Code: protocol.CodeInternalError, Message: "no se pudo entrar en el clan"})
		},
	})
}

func (s *Simulation) handleClanJoinResolved(cmd ClanJoinResolved) {
	o := cmd.Order
	if c, ok := s.state.clans.joining[o.PlayerID]; !ok || c != o.ClanID {
		return
	}
	delete(s.state.clans.joining, o.PlayerID)
	if cmd.Code != "" {
		s.economyError(o.PlayerID, cmd.RequestID, cmd.Code, cmd.Message)
		return
	}
	rec, ok := s.state.clans.clans[o.ClanID]
	if !ok {
		// No hay ruta: la disolución espera a las entradas en vuelo (leaveClan).
		s.deps.Log.Error("invariant_violation", "inv_id", "INV-CLAN-001", "severity", "ALTO", "policy", "REJECT",
			"entity", "player:"+o.PlayerID.String(), "tick", s.state.Tick(),
			"detail", "entrada confirmada en un clan que la RAM ya no tiene")
		return
	}
	rec.Members[o.PlayerID] = o.AtMs
	s.state.clans.memberOf[o.PlayerID] = rec.ID
	notify := s.dropInvitesOf(o.PlayerID)
	s.setCityClan(o.PlayerID, &city.ClanTag{ID: rec.ID, Tag: rec.Tag})
	s.sendClanUpdates(append(append(notify, s.state.ClanMembers(rec.ID)...), o.PlayerID), o.PlayerID, cmd.RequestID)
	s.deps.Log.Info("jugador unido a un clan", "clan_id", rec.ID, "player_id", o.PlayerID.String())
}

// ─────────────────────────────────────────────────────────────
// clan.leave y clan.expel (optimistas)
// ─────────────────────────────────────────────────────────────

func (s *Simulation) handleLeaveClan(cmd LeaveClan) {
	id, ok := s.state.clans.memberOf[cmd.PlayerID]
	if !ok {
		s.economyError(cmd.PlayerID, cmd.RequestID, protocol.CodeInvalidTarget, "no estás en ningún clan")
		return
	}
	s.leaveClan(id, cmd.PlayerID, clan.ReasonLeft, uuid.Nil, cmd.PlayerID, cmd.RequestID)
}

func (s *Simulation) handleExpelFromClan(cmd ExpelFromClan) {
	rec, ok := s.state.leaderClan(cmd.PlayerID)
	if !ok {
		s.economyError(cmd.PlayerID, cmd.RequestID, protocol.CodeInvalidTarget, "sólo el líder de un clan expulsa")
		return
	}
	if cmd.TargetID == cmd.PlayerID {
		s.economyError(cmd.PlayerID, cmd.RequestID, protocol.CodeInvalidTarget, "para irte, sal del clan")
		return
	}
	if _, miembro := rec.Members[cmd.TargetID]; !miembro {
		s.economyError(cmd.PlayerID, cmd.RequestID, protocol.CodeInvalidTarget, "ese jugador no es de tu clan")
		return
	}
	s.leaveClan(rec.ID, cmd.TargetID, clan.ReasonExpelled, cmd.PlayerID, cmd.PlayerID, cmd.RequestID)
}

// leaveClan cierra la estancia de playerID en el acto (RN-CLAN-005): la RAM,
// las vistas y un único trabajo que lo escribe. La paz con los antiguos
// compañeros dura EO_CLAN_PEACE_GRACE_SECONDS más (ADR-040 §3).
func (s *Simulation) leaveClan(clanID int64, playerID uuid.UUID, reason string, by, actor uuid.UUID, requestID string) {
	rec := s.state.clans.clans[clanID]
	if rec.dissolving {
		s.economyError(actor, requestID, protocol.CodeInvalidTarget, "el clan se está disolviendo")
		return
	}
	dissolve := len(rec.Members) <= 1
	if dissolve {
		// Una entrada en vuelo encontraría el clan disuelto en la base: el
		// último no sale hasta que se resuelva (milisegundos).
		for _, c := range s.state.clans.joining {
			if c == clanID {
				s.economyError(actor, requestID, protocol.CodeInvalidTarget,
					"alguien está entrando en el clan; inténtalo en un momento")
				return
			}
		}
		// ADR-041 §4: con territorios, la disolución va en dos fases.
		if len(s.state.clanTerritories(clanID)) > 0 {
			s.startDissolution(rec, playerID, reason, by, actor, requestID)
			return
		}
	}
	s.submitClanLeave(s.applyLeave(rec, playerID, reason, by, actor, requestID, s.deps.Clock.NowMs()))
}

// applyLeave cierra en la RAM la estancia de playerID en el instante nowMs: el
// líder nuevo o la disolución, la ciudad, los clan.update y el aviso de una
// expulsión. Devuelve la salida que hay que escribir.
func (s *Simulation) applyLeave(rec *clanRecord, playerID uuid.UUID, reason string, by, actor uuid.UUID, requestID string, nowMs int64) ClanLeave {
	clanID := rec.ID
	remaining := make([]clan.Member, 0, len(rec.Members))
	for _, m := range rec.members() {
		if m.PlayerID != playerID {
			remaining = append(remaining, m)
		}
	}
	dissolve := len(remaining) == 0

	joinedAt := rec.Members[playerID]
	delete(rec.Members, playerID)
	delete(s.state.clans.memberOf, playerID)
	s.state.clans.departed = append(s.state.clans.departed, clan.Stay{
		ClanID: clanID, PlayerID: playerID, JoinedAtMs: joinedAt, LeftAtMs: nowMs,
	})

	newLeader := uuid.Nil
	if !dissolve && rec.LeaderID == playerID {
		newLeader, _ = clan.NextLeader(remaining)
		rec.LeaderID = newLeader
	}
	var invitees []uuid.UUID
	if dissolve {
		delete(s.state.clans.clans, clanID)
		for k := range s.state.clans.invites {
			if k.ClanID == clanID {
				invitees = append(invitees, k.PlayerID)
				delete(s.state.clans.invites, k)
			}
		}
	}

	leave := ClanLeave{
		ClanID: clanID, PlayerID: playerID, Reason: reason, By: by,
		At:        time.UnixMilli(nowMs).UTC(),
		NewLeader: newLeader, Dissolve: dissolve, Tick: s.state.Tick(), AtMs: nowMs,
	}
	s.setCityClan(playerID, nil)
	players := append([]uuid.UUID{playerID}, invitees...)
	for _, m := range remaining {
		players = append(players, m.PlayerID)
	}
	s.sendClanUpdates(players, actor, requestID)
	if reason == clan.ReasonExpelled {
		// SPEC-CLANS Q10: el expulsado lo sabe en vivo, esté donde esté.
		s.sendNotices(eventlog.Event{
			Type: eventlog.EventClanMemberExpelled, PlayerID: playerID, Counterpart: by, AtMs: nowMs,
		})
	}
	s.deps.Log.Info("salida de un clan", "clan_id", clanID, "player_id", playerID.String(),
		"reason", reason, "dissolved", dissolve)
	return leave
}

func (s *Simulation) submitClanLeave(l ClanLeave) {
	if s.deps.Clans == nil {
		return
	}
	store := s.deps.Clans
	s.deps.Persister.Submit(Job{
		Name: JobClanLeave,
		Key:  ClanKey(l.ClanID),
		Run: func(ctx context.Context) error {
			released, err := store.Leave(ctx, l)
			if err == nil && len(released) > 0 {
				// ADR-041 §4: una captura confirmó en la misma cola justo antes.
				s.reinject(ClanTerritoriesReleased{Controls: released})
			}
			return err
		},
		OnPermanentFailure: func(err error) {
			s.deps.Log.Error("no se pudo escribir la salida de un clan; se reintentará en el siguiente volcado",
				"err", err, "clan_id", l.ClanID, "player_id", l.PlayerID.String())
			s.reinject(ClanLeavePersistFailed{Leave: l})
		},
	})
}

func leaveKey(l ClanLeave) string {
	return strconv.FormatInt(l.ClanID, 10) + ":" + l.PlayerID.String() + ":" + strconv.FormatInt(l.AtMs, 10)
}

func (s *Simulation) handleClanLeavePersistFailed(cmd ClanLeavePersistFailed) {
	s.state.clans.pendingLeaves[leaveKey(cmd.Leave)] = cmd.Leave
}

// flushClanLeaves reencola las salidas descartadas, en orden de instante. Es
// idempotente: la escritura es tolerante.
func (s *Simulation) flushClanLeaves() {
	if len(s.state.clans.pendingLeaves) == 0 {
		return
	}
	pend := make([]ClanLeave, 0, len(s.state.clans.pendingLeaves))
	for _, l := range s.state.clans.pendingLeaves {
		pend = append(pend, l)
	}
	s.state.clans.pendingLeaves = make(map[string]ClanLeave)
	sort.Slice(pend, func(i, j int) bool {
		if pend[i].AtMs != pend[j].AtMs {
			return pend[i].AtMs < pend[j].AtMs
		}
		return leaveKey(pend[i]) < leaveKey(pend[j])
	})
	for _, l := range pend {
		l.Tick = s.state.Tick()
		s.submitClanLeave(l)
	}
}

// ─────────────────────────────────────────────────────────────
// Fase 5
// ─────────────────────────────────────────────────────────────

// sweepClans olvida las salidas cuya paz residual venció y las invitaciones
// caducadas. No escribe nada: la paz residual se deriva de la base y una
// invitación caducada no se puede aceptar (la entrada lo vuelve a comprobar).
func (s *Simulation) sweepClans(nowMs int64) {
	if len(s.state.clans.departed) > 0 {
		graceMs := s.state.clans.grace.Milliseconds()
		kept := s.state.clans.departed[:0]
		for _, d := range s.state.clans.departed {
			if nowMs-d.LeftAtMs < graceMs {
				kept = append(kept, d)
			}
		}
		s.state.clans.departed = kept
	}
	for k, inv := range s.state.clans.invites {
		if inv.ExpiresAtMs <= nowMs {
			delete(s.state.clans.invites, k)
		}
	}
}

// ─────────────────────────────────────────────────────────────
// La regla de bandos en faction.join
// ─────────────────────────────────────────────────────────────

// clanBlocksFaction dice si la entrada de un jugador en un bando juntaría a la
// Orden y al Caos en su clan (RN-CLAN-006, INV-CLAN-002), y el motivo.
func (s *State) clanBlocksFaction(playerID uuid.UUID, faction string) (bool, string) {
	id, ok := s.clans.memberOf[playerID]
	if !ok {
		id, ok = s.clans.joining[playerID]
	}
	if !ok {
		return false, ""
	}
	stances := s.clanStances(id, playerID)
	if !clan.Mixes(append(stances, clan.Stance{Faction: s.FactionOf(playerID), Joining: faction})...) {
		return false, ""
	}
	if faction == player.FactionChaos {
		return true, "tu clan tiene miembros de la Orden"
	}
	return true, "tu clan tiene miembros del Caos"
}
