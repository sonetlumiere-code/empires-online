package websocket_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/clan"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/message"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/territory"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// Los clanes por WebSocket (SPEC-CLANS §9 «contract / e2e»): la pertenencia por
// la simulación, con un almacén en memoria, y el canal y la vista pública por
// la conexión, con un canal falso. La base, el candado del clan y los triggers
// están en clans_integration_test.go.

// memoryClans hace de base de la pertenencia: acepta toda escritura.
type memoryClans struct {
	mu             sync.Mutex
	nextID         int64
	nextAllianceID int64
}

func (m *memoryClans) Create(context.Context, simulation.ClanCreateOrder) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextID++
	return m.nextID, nil
}
func (*memoryClans) Invite(context.Context, simulation.ClanInviteOrder) error { return nil }
func (*memoryClans) Join(context.Context, simulation.ClanJoinOrder) ([]simulation.AllianceBreak, error) {
	return nil, nil
}
func (*memoryClans) Leave(context.Context, simulation.ClanLeave) ([]territory.Control, error) {
	return nil, nil
}
func (*memoryClans) SetRank(context.Context, simulation.ClanRankOrder) error        { return nil }
func (*memoryClans) ChangeLeader(context.Context, simulation.ClanLeaderOrder) error { return nil }
func (m *memoryClans) ApplyAlliance(_ context.Context, w simulation.AllianceWrite) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if w.Insert {
		m.nextAllianceID++
		return m.nextAllianceID, nil
	}
	return w.Alliance.ID, nil
}

// canalFalso hace de canal: su propia pertenencia (la fija el test, nunca lee la
// RAM de la simulación desde la conexión) y sus mensajes.
type canalFalso struct {
	mu       sync.Mutex
	clanDe   map[uuid.UUID]int64
	ciudades map[uuid.UUID]string
	mensajes []clan.Sent
	leidos   map[uuid.UUID]int64
}

func (c *canalFalso) miembro(p uuid.UUID, clanID int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if clanID == 0 {
		delete(c.clanDe, p)
		return
	}
	c.clanDe[p] = clanID
}

func (c *canalFalso) miembros(clanID int64) []uuid.UUID {
	var out []uuid.UUID
	for p, id := range c.clanDe {
		if id == clanID {
			out = append(out, p)
		}
	}
	return out
}

func (c *canalFalso) ChatSend(_ context.Context, from uuid.UUID, body string, req uuid.UUID) (clan.Sent, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	id, ok := c.clanDe[from]
	if !ok {
		return clan.Sent{}, clan.ErrNotMember
	}
	s := clan.Sent{ID: int64(len(c.mensajes) + 1), ClanID: id, From: from, FromCityID: 1, FromCityName: c.ciudades[from],
		Body: body, SentAtMs: 1, Members: c.miembros(id)}
	c.mensajes = append(c.mensajes, s)
	_ = req
	return s, nil
}

func (c *canalFalso) ChatExisting(context.Context, uuid.UUID, uuid.UUID) (clan.Sent, bool, error) {
	return clan.Sent{}, false, nil
}

func (c *canalFalso) ChatPage(_ context.Context, me uuid.UUID, _ int64) (protocol.ClanChatPagePayload, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	id, ok := c.clanDe[me]
	if !ok {
		return protocol.ClanChatPagePayload{}, clan.ErrNotMember
	}
	page := protocol.ClanChatPagePayload{ClanID: id, Messages: []protocol.ClanMessageView{}, ReadUpToID: c.leidos[me]}
	for _, s := range c.mensajes {
		if s.ClanID == id {
			cid, name := s.FromCityID, s.FromCityName
			page.Messages = append(page.Messages, protocol.ClanMessageView{ID: s.ID, ClanID: id, FromPlayerID: s.From.String(),
				FromCityID: &cid, FromCityName: &name, Body: s.Body, SentAtMs: s.SentAtMs})
		}
	}
	return page, nil
}

func (c *canalFalso) ChatMarkRead(_ context.Context, me uuid.UUID, upTo func(int64) int64) error {
	c.mu.Lock()
	id, ok := c.clanDe[me]
	c.mu.Unlock()
	if !ok {
		return clan.ErrNotMember
	}
	n := upTo(id)
	c.mu.Lock()
	defer c.mu.Unlock()
	if n > c.leidos[me] {
		c.leidos[me] = n
	}
	return nil
}

func (c *canalFalso) Lookup(_ context.Context, clanID int64) (protocol.ClanInfoPayload, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ms := c.miembros(clanID)
	if len(ms) == 0 {
		return protocol.ClanInfoPayload{}, clan.ErrUnknownClan
	}
	info := protocol.ClanInfoPayload{
		ClanID: clanID, Name: "Los Lobos", Tag: "LOB", Members: []protocol.ClanMemberView{},
		Territories: []protocol.ClanTerritoryView{}, Alliances: []protocol.ClanPublicAllianceView{},
	}
	for i, p := range ms {
		role := clan.RoleMember
		if i == 0 {
			role = clan.RoleLeader
		}
		info.Members = append(info.Members, protocol.ClanMemberView{PlayerID: p.String(), Faction: "NEUTRAL", Role: role, JoinedAtMs: 1})
	}
	return info, nil
}

func clanUpdate(t *testing.T, raw json.RawMessage) protocol.ClanUpdatePayload {
	t.Helper()
	return decode[protocol.ClanUpdatePayload](t, raw)
}

// SPEC-CLANS §9 «e2e»: el clan de punta a punta. Al conectar llega el clan
// (null); fundar, invitar y aceptar vuelven como clan.update a los implicados;
// el canal llega a los miembros y no a un tercero; la vista pública la pide
// cualquiera; entre compañeros el ataque se rechaza con su motivo; y la
// expulsión se avisa al expulsado.
func TestUnClanPorLaRed(t *testing.T) {
	env := newE2E(t)
	bea, ce := uuid.New(), uuid.New()
	ciudadBea, ciudadCe := int64(2), int64(3)
	env.state.AddCity(&city.City{ID: ciudadBea, OwnerPlayerID: bea, Name: "Bizancio", CenterX: 70, CenterY: 70,
		Era: city.EraStone, PopulationLimit: 20, PresenceState: city.PresenceOnline, Civilization: "ROMAN"})
	env.state.AddCity(&city.City{ID: ciudadCe, OwnerPlayerID: ce, Name: "Persépolis", CenterX: 100, CenterY: 100,
		Era: city.EraStone, PopulationLimit: 20, PresenceState: city.PresenceOnline, Civilization: "ROMAN"})
	env.state.AddUnit(&unit.Unit{ID: 900, PlayerID: bea, CityID: &ciudadBea, Type: unit.TypeWarrior,
		X: 43, Y: 40, HP: 60, MaxHP: 60, Status: unit.StatusIdle})
	canal := &canalFalso{clanDe: map[uuid.UUID]int64{}, leidos: map[uuid.UUID]int64{},
		ciudades: map[uuid.UUID]string{env.playerID: "Testópolis", bea: "Bizancio", ce: "Persépolis"}}
	env.ws.SetClans(canal)
	env.ws.SetMessages(nuevoBuzon(map[uuid.UUID]string{}), message.NewLimiter(10, 10))

	yo, snap := conectar(t, env, env.playerID)
	for _, c := range snap.Cities {
		assert.Nil(t, c.Clan, "nadie tiene clan")
	}
	inicial := yo.await(protocol.TypeClanUpdate, 3*time.Second)
	assert.True(t, claveNula(t, inicial.Payload, "clan"), "al conectar, el clan llega como null")
	assert.Empty(t, clanUpdate(t, inicial.Payload).Invitations)
	b, _ := conectar(t, env, bea)
	b.await(protocol.TypeClanUpdate, 3*time.Second)
	c, _ := conectar(t, env, ce)
	c.await(protocol.TypeClanUpdate, 3*time.Second)

	// Fundar.
	req := yo.send(protocol.TypeClanCreate, map[string]any{"name": "Los Lobos", "tag": "LOB"})
	env.pump()
	fundado, _ := yo.awaitWhere(protocol.TypeClanUpdate, func(raw json.RawMessage) bool {
		return clanUpdate(t, raw).Clan != nil
	}, 3*time.Second)
	assert.Equal(t, req, fundado.RequestID)
	vista := clanUpdate(t, fundado.Payload).Clan
	assert.Equal(t, "LOB", vista.Tag)
	assert.Equal(t, env.playerID.String(), vista.LeaderID)
	clanID := vista.ID

	// Un nombre mal formado no llega a la simulación con otro código.
	r := yo.send(protocol.TypeClanCreate, map[string]any{"name": "Los Lobos", "tag": "lob"})
	env.pump()
	assert.Equal(t, protocol.CodeInvalidMessage, errorDe(t, yo, r).Code)

	// Invitar y aceptar.
	yo.send(protocol.TypeClanInvite, map[string]any{"playerId": bea.String(), "invited": true})
	env.pump()
	inv, _ := b.awaitWhere(protocol.TypeClanUpdate, func(raw json.RawMessage) bool {
		return len(clanUpdate(t, raw).Invitations) == 1
	}, 3*time.Second)
	assert.Equal(t, clanID, clanUpdate(t, inv.Payload).Invitations[0].ClanID)
	req = b.send(protocol.TypeClanRespond, map[string]any{"clanId": clanID, "accept": true})
	env.pump()
	dentro, _ := b.awaitWhere(protocol.TypeClanUpdate, func(raw json.RawMessage) bool {
		return clanUpdate(t, raw).Clan != nil
	}, 3*time.Second)
	assert.Equal(t, req, dentro.RequestID)
	assert.Len(t, clanUpdate(t, dentro.Payload).Clan.Members, 2)
	yo.awaitWhere(protocol.TypeClanUpdate, func(raw json.RawMessage) bool {
		p := clanUpdate(t, raw)
		return p.Clan != nil && len(p.Clan.Members) == 2
	}, 3*time.Second)
	canal.miembro(env.playerID, clanID)
	canal.miembro(bea, clanID)

	// El canal: llega a los dos miembros, a quien lo envió con su requestId.
	req = yo.send(protocol.TypeClanChatSend, map[string]any{"body": "  al norte  "})
	propio := yo.await(protocol.TypeClanChatNew, 3*time.Second)
	assert.Equal(t, req, propio.RequestID)
	m := decode[protocol.ClanChatNewPayload](t, propio.Payload).Message
	assert.Equal(t, "al norte", m.Body, "recortado como un mensaje directo")
	deBea := b.await(protocol.TypeClanChatNew, 3*time.Second)
	assert.Empty(t, deBea.RequestID)
	r = c.send(protocol.TypeClanChatSend, map[string]any{"body": "hola"})
	assert.Equal(t, protocol.CodeInvalidTarget, errorDe(t, c, r).Code, "sin clan no se escribe")

	b.send(protocol.TypeClanChatFetch, map[string]any{})
	pagina := decode[protocol.ClanChatPagePayload](t, b.await(protocol.TypeClanChatPage, 3*time.Second).Payload)
	require.Len(t, pagina.Messages, 1)
	b.send(protocol.TypeClanChatRead, map[string]any{"upToId": 99})
	require.Eventually(t, func() bool {
		canal.mu.Lock()
		defer canal.mu.Unlock()
		return canal.leidos[bea] == pagina.Messages[0].ID
	}, 3*time.Second, 10*time.Millisecond, "lo leído no pasa de lo recibido")

	// La vista pública, para cualquiera.
	c.send(protocol.TypeClanLookup, map[string]any{"clanId": clanID})
	info := decode[protocol.ClanInfoPayload](t, c.await(protocol.TypeClanInfo, 3*time.Second).Payload)
	assert.Len(t, info.Members, 2)

	// Entre compañeros no hay ataque.
	r = b.send(protocol.TypeUnitAttack, map[string]any{"unitId": 900, "targetUnitId": env.unitIDs[0]})
	env.pump()
	errp := errorDe(t, b, r)
	assert.Equal(t, protocol.CodeInvalidTarget, errp.Code)
	assert.Equal(t, "sois del mismo clan", errp.Message)

	// La expulsión: aviso y clan null para el expulsado.
	yo.send(protocol.TypeClanExpel, map[string]any{"playerId": bea.String()})
	env.pump()
	// Primero su clan (null) y después el aviso, en el orden en que salen.
	b.awaitWhere(protocol.TypeClanUpdate, func(raw json.RawMessage) bool { return claveNula(t, raw, "clan") }, 3*time.Second)
	aviso := decode[protocol.EventLogNoticePayload](t, b.await(protocol.TypeEventLogNotice, 3*time.Second).Payload)
	assert.Equal(t, "CLAN_EXPELLED", aviso.Kind)
	assert.Equal(t, env.playerID.String(), aviso.OtherPlayerID)
}

// SPEC-CLAN-GOVERNANCE §9, e2e: el líder nombra oficial a un miembro, que lo ve
// en su vista; el oficial invita a un tercero, al que le llega la invitación, y
// el líder la ve entre las enviadas; el líder cede el liderazgo y el nuevo líder
// recibe su clan y el aviso con la sigla. Un miembro raso no puede ceder.
func TestElGobiernoDelClanPorLaRed(t *testing.T) {
	env := newE2E(t)
	bea, ce := uuid.New(), uuid.New()
	for i, p := range []uuid.UUID{bea, ce} {
		env.state.AddCity(&city.City{ID: int64(2 + i), OwnerPlayerID: p, Name: []string{"Bizancio", "Persépolis"}[i],
			CenterX: int32(70 + 30*i), CenterY: 70, Era: city.EraStone, PopulationLimit: 20,
			PresenceState: city.PresenceOnline, Civilization: "ROMAN"})
	}
	env.ws.SetClans(&canalFalso{clanDe: map[uuid.UUID]int64{}, leidos: map[uuid.UUID]int64{}, ciudades: map[uuid.UUID]string{}})

	yo, _ := conectar(t, env, env.playerID)
	yo.await(protocol.TypeClanUpdate, 3*time.Second)
	b, _ := conectar(t, env, bea)
	b.await(protocol.TypeClanUpdate, 3*time.Second)
	c, _ := conectar(t, env, ce)
	c.await(protocol.TypeClanUpdate, 3*time.Second)

	yo.send(protocol.TypeClanCreate, map[string]any{"name": "Los Lobos", "tag": "LOB"})
	env.pump()
	fundado, _ := yo.awaitWhere(protocol.TypeClanUpdate, func(raw json.RawMessage) bool { return clanUpdate(t, raw).Clan != nil }, 3*time.Second)
	clanID := clanUpdate(t, fundado.Payload).Clan.ID
	yo.send(protocol.TypeClanInvite, map[string]any{"playerId": bea.String(), "invited": true})
	env.pump()
	b.awaitWhere(protocol.TypeClanUpdate, func(raw json.RawMessage) bool { return len(clanUpdate(t, raw).Invitations) == 1 }, 3*time.Second)
	b.send(protocol.TypeClanRespond, map[string]any{"clanId": clanID, "accept": true})
	env.pump()
	b.awaitWhere(protocol.TypeClanUpdate, func(raw json.RawMessage) bool { return clanUpdate(t, raw).Clan != nil }, 3*time.Second)

	rolDe := func(p protocol.ClanUpdatePayload, who uuid.UUID) string {
		for _, m := range p.Clan.Members {
			if m.PlayerID == who.String() {
				return m.Role
			}
		}
		return ""
	}

	// Un raso no cede.
	r := b.send(protocol.TypeClanTransfer, map[string]any{"playerId": env.playerID.String()})
	env.pump()
	errp := errorDe(t, b, r)
	assert.Equal(t, protocol.CodeInvalidTarget, errp.Code)
	assert.Equal(t, "sólo el líder nombra oficiales y cede el liderazgo", errp.Message)

	// Nombrar oficial.
	req := yo.send(protocol.TypeClanPromote, map[string]any{"playerId": bea.String(), "officer": true})
	env.pump()
	propio, _ := yo.awaitWhere(protocol.TypeClanUpdate, func(raw json.RawMessage) bool {
		p := clanUpdate(t, raw)
		return p.Clan != nil && rolDe(p, bea) == clan.RoleOfficer
	}, 3*time.Second)
	assert.Equal(t, req, propio.RequestID)
	b.awaitWhere(protocol.TypeClanUpdate, func(raw json.RawMessage) bool {
		p := clanUpdate(t, raw)
		return p.Clan != nil && rolDe(p, bea) == clan.RoleOfficer
	}, 3*time.Second)

	// El oficial invita: le llega al invitado y el líder la ve.
	b.send(protocol.TypeClanInvite, map[string]any{"playerId": ce.String(), "invited": true})
	env.pump()
	c.awaitWhere(protocol.TypeClanUpdate, func(raw json.RawMessage) bool { return len(clanUpdate(t, raw).Invitations) == 1 }, 3*time.Second)
	yo.awaitWhere(protocol.TypeClanUpdate, func(raw json.RawMessage) bool {
		p := clanUpdate(t, raw)
		return p.Clan != nil && len(p.Clan.SentInvitations) == 1
	}, 3*time.Second)

	// Ceder el liderazgo.
	yo.send(protocol.TypeClanTransfer, map[string]any{"playerId": bea.String()})
	env.pump()
	nuevo, _ := b.awaitWhere(protocol.TypeClanUpdate, func(raw json.RawMessage) bool {
		p := clanUpdate(t, raw)
		return p.Clan != nil && p.Clan.LeaderID == bea.String()
	}, 3*time.Second)
	vista := clanUpdate(t, nuevo.Payload)
	assert.Equal(t, clan.RoleLeader, rolDe(vista, bea))
	assert.Equal(t, clan.RoleOfficer, rolDe(vista, env.playerID), "el anterior queda como oficial")
	aviso := decode[protocol.EventLogNoticePayload](t, b.await(protocol.TypeEventLogNotice, 3*time.Second).Payload)
	assert.Equal(t, "CLAN_LEADERSHIP_RECEIVED", aviso.Kind)
	assert.Equal(t, "LOB", aviso.ClanTag)
	assert.Equal(t, env.playerID.String(), aviso.OtherPlayerID)
}

// alianzasEnElJSON son las alianzas de clan.alliances tal como van por la red:
// falla si la lista no es un array.
func alianzasEnElJSON(t *testing.T, raw json.RawMessage) []map[string]any {
	t.Helper()
	var m struct {
		Clan map[string]json.RawMessage `json:"clan"`
	}
	require.NoError(t, json.Unmarshal(raw, &m))
	require.NotNil(t, m.Clan)
	crudo, ok := m.Clan["alliances"]
	require.True(t, ok, "sin alliances")
	require.NotEqual(t, "null", string(crudo))
	var out []map[string]any
	require.NoError(t, json.Unmarshal(crudo, &out))
	return out
}

// SPEC-CLAN-ALLIANCES §9 «e2e»: el líder de [LOB] propone una alianza al de
// [OSO], que la ve en su clan con el aviso; el oficial de [LOB] ve la oferta y
// el raso no; el líder de [OSO] acepta, al que propuso le llega el aviso, todos
// ven la alianza activa, y el ataque de un miembro de [OSO] contra uno de [LOB]
// se rechaza con su motivo.
func TestUnaAlianzaDeClanesPorLaRed(t *testing.T) {
	env := newE2E(t)
	bea, ce, dani := uuid.New(), uuid.New(), uuid.New()
	for i, p := range []uuid.UUID{bea, ce, dani} {
		env.state.AddCity(&city.City{ID: int64(2 + i), OwnerPlayerID: p, Name: []string{"Bizancio", "Persépolis", "Tebas"}[i],
			CenterX: int32(70 + 30*i), CenterY: 70, Era: city.EraStone, PopulationLimit: 20,
			PresenceState: city.PresenceOnline, Civilization: "ROMAN"})
	}
	ciudadBea := int64(2)
	env.state.AddUnit(&unit.Unit{ID: 900, PlayerID: bea, CityID: &ciudadBea, Type: unit.TypeWarrior,
		X: 43, Y: 40, HP: 60, MaxHP: 60, Status: unit.StatusIdle})
	env.ws.SetClans(&canalFalso{clanDe: map[uuid.UUID]int64{}, leidos: map[uuid.UUID]int64{}, ciudades: map[uuid.UUID]string{}})

	yo, _ := conectar(t, env, env.playerID)
	yo.await(protocol.TypeClanUpdate, 3*time.Second)
	b, _ := conectar(t, env, bea)
	b.await(protocol.TypeClanUpdate, 3*time.Second)
	c, _ := conectar(t, env, ce)
	c.await(protocol.TypeClanUpdate, 3*time.Second)
	d, _ := conectar(t, env, dani)
	d.await(protocol.TypeClanUpdate, 3*time.Second)

	fundar := func(quien *conn, name, tag string) int64 {
		quien.send(protocol.TypeClanCreate, map[string]any{"name": name, "tag": tag})
		env.pump()
		f, _ := quien.awaitWhere(protocol.TypeClanUpdate, func(raw json.RawMessage) bool { return clanUpdate(t, raw).Clan != nil }, 3*time.Second)
		return clanUpdate(t, f.Payload).Clan.ID
	}
	lob := fundar(yo, "Los Lobos", "LOB")
	oso := fundar(b, "Los Osos", "OSO")
	for _, p := range []struct {
		id uuid.UUID
		c  *conn
	}{{ce, c}, {dani, d}} {
		yo.send(protocol.TypeClanInvite, map[string]any{"playerId": p.id.String(), "invited": true})
		env.pump()
		p.c.awaitWhere(protocol.TypeClanUpdate, func(raw json.RawMessage) bool { return len(clanUpdate(t, raw).Invitations) == 1 }, 3*time.Second)
		p.c.send(protocol.TypeClanRespond, map[string]any{"clanId": lob, "accept": true})
		env.pump()
		p.c.awaitWhere(protocol.TypeClanUpdate, func(raw json.RawMessage) bool { return clanUpdate(t, raw).Clan != nil }, 3*time.Second)
	}
	yo.send(protocol.TypeClanPromote, map[string]any{"playerId": dani.String(), "officer": true})
	env.pump()
	daniOficial := func(raw json.RawMessage) bool {
		for _, m := range clanUpdate(t, raw).Clan.Members {
			if m.PlayerID == dani.String() {
				return m.Role == clan.RoleOfficer
			}
		}
		return false
	}
	d.awaitWhere(protocol.TypeClanUpdate, daniOficial, 3*time.Second)
	c.awaitWhere(protocol.TypeClanUpdate, daniOficial, 3*time.Second) // lo anterior ya no cuenta

	// La oferta.
	conAlianzas := func(n int, status string) func(json.RawMessage) bool {
		return func(raw json.RawMessage) bool {
			p := clanUpdate(t, raw)
			return p.Clan != nil && len(p.Clan.Alliances) == n && (n == 0 || p.Clan.Alliances[0].Status == status)
		}
	}
	req := yo.send(protocol.TypeClanAlliancePropose, map[string]any{"clanId": oso})
	env.pump()
	propia, _ := yo.awaitWhere(protocol.TypeClanUpdate, conAlianzas(1, "PROPOSED"), 3*time.Second)
	assert.Equal(t, req, propia.RequestID)
	assert.True(t, clanUpdate(t, propia.Payload).Clan.Alliances[0].ProposedByUs)
	recibida, _ := b.awaitWhere(protocol.TypeClanUpdate, conAlianzas(1, "PROPOSED"), 3*time.Second)
	oferta := clanUpdate(t, recibida.Payload).Clan.Alliances[0]
	assert.Equal(t, lob, oferta.ClanID)
	assert.False(t, oferta.ProposedByUs)
	aviso := decode[protocol.EventLogNoticePayload](t, b.await(protocol.TypeEventLogNotice, 3*time.Second).Payload)
	assert.Equal(t, "CLAN_ALLIANCE_OFFERED", aviso.Kind)
	assert.Equal(t, "LOB", aviso.ClanTag)
	assert.Equal(t, env.playerID.String(), aviso.OtherPlayerID)
	d.awaitWhere(protocol.TypeClanUpdate, conAlianzas(1, "PROPOSED"), 3*time.Second)
	delRaso, _ := c.awaitWhere(protocol.TypeClanUpdate, func(json.RawMessage) bool { return true }, 3*time.Second)
	assert.Empty(t, alianzasEnElJSON(t, delRaso.Payload), "un raso no ve las ofertas")

	// La aceptación.
	b.send(protocol.TypeClanAllianceRespond, map[string]any{"allianceId": oferta.ID, "accept": true})
	env.pump()
	yo.awaitWhere(protocol.TypeClanUpdate, conAlianzas(1, "ACTIVE"), 3*time.Second)
	aviso = decode[protocol.EventLogNoticePayload](t, yo.await(protocol.TypeEventLogNotice, 3*time.Second).Payload)
	assert.Equal(t, "CLAN_ALLIANCE_ACCEPTED", aviso.Kind)
	assert.Equal(t, "OSO", aviso.ClanTag)
	activa, _ := c.awaitWhere(protocol.TypeClanUpdate, conAlianzas(1, "ACTIVE"), 3*time.Second)
	assert.Equal(t, "OSO", alianzasEnElJSON(t, activa.Payload)[0]["tag"])
	assert.Nil(t, alianzasEnElJSON(t, activa.Payload)[0]["breaksAtMs"], "sin preaviso, null")

	// La paz entre los dos clanes.
	r := b.send(protocol.TypeUnitAttack, map[string]any{"unitId": 900, "targetUnitId": env.unitIDs[0]})
	env.pump()
	errp := errorDe(t, b, r)
	assert.Equal(t, protocol.CodeInvalidTarget, errp.Code)
	assert.Equal(t, "vuestros clanes están aliados", errp.Message)
}

// enviadasEnElJSON son los invitados de clan.sentInvitations tal como van por
// la red: falla si la lista no es un array (null, o sin la clave).
func enviadasEnElJSON(t *testing.T, raw json.RawMessage) []string {
	t.Helper()
	var m struct {
		Clan map[string]json.RawMessage `json:"clan"`
	}
	require.NoError(t, json.Unmarshal(raw, &m))
	require.NotNil(t, m.Clan)
	var lista []map[string]any
	crudo, ok := m.Clan["sentInvitations"]
	require.True(t, ok, "sin sentInvitations")
	require.NotEqual(t, "null", string(crudo))
	require.NoError(t, json.Unmarshal(crudo, &lista))
	out := []string{}
	for _, inv := range lista {
		out = append(out, inv["playerId"].(string))
	}
	return out
}

// SPEC-CLAN-GOVERNANCE §4.2 y §9 «e2e», desde el punto de vista de cada rango:
// un oficial invita a dos jugadores; cuando uno entra, el clan entero recibe su
// vista, y el oficial ve la invitación que sigue viva mientras un raso (y quien
// acaba de entrar) recibe sentInvitations como lista vacía.
func TestLasInvitacionesEnviadasPorRangoPorLaRed(t *testing.T) {
	env := newE2E(t)
	bea, ce, dani, eva := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	for i, p := range []uuid.UUID{bea, ce, dani, eva} {
		env.state.AddCity(&city.City{ID: int64(2 + i), OwnerPlayerID: p, Name: []string{"Bizancio", "Persépolis", "Tebas", "Éfeso"}[i],
			CenterX: int32(70 + 30*i), CenterY: 70, Era: city.EraStone, PopulationLimit: 20,
			PresenceState: city.PresenceOnline, Civilization: "ROMAN"})
	}
	env.ws.SetClans(&canalFalso{clanDe: map[uuid.UUID]int64{}, leidos: map[uuid.UUID]int64{}, ciudades: map[uuid.UUID]string{}})

	yo, _ := conectar(t, env, env.playerID)
	yo.await(protocol.TypeClanUpdate, 3*time.Second)
	b, _ := conectar(t, env, bea)
	b.await(protocol.TypeClanUpdate, 3*time.Second)
	c, _ := conectar(t, env, ce)
	c.await(protocol.TypeClanUpdate, 3*time.Second)
	e, _ := conectar(t, env, eva)
	e.await(protocol.TypeClanUpdate, 3*time.Second)

	yo.send(protocol.TypeClanCreate, map[string]any{"name": "Los Lobos", "tag": "LOB"})
	env.pump()
	fundado, _ := yo.awaitWhere(protocol.TypeClanUpdate, func(raw json.RawMessage) bool { return clanUpdate(t, raw).Clan != nil }, 3*time.Second)
	clanID := clanUpdate(t, fundado.Payload).Clan.ID
	invitar := func(quien *conn, p uuid.UUID) {
		quien.send(protocol.TypeClanInvite, map[string]any{"playerId": p.String(), "invited": true})
		env.pump()
	}
	entrar := func(quien *conn) json.RawMessage {
		quien.awaitWhere(protocol.TypeClanUpdate, func(raw json.RawMessage) bool { return len(clanUpdate(t, raw).Invitations) == 1 }, 3*time.Second)
		quien.send(protocol.TypeClanRespond, map[string]any{"clanId": clanID, "accept": true})
		env.pump()
		dentro, _ := quien.awaitWhere(protocol.TypeClanUpdate, func(raw json.RawMessage) bool { return clanUpdate(t, raw).Clan != nil }, 3*time.Second)
		return dentro.Payload
	}
	invitar(yo, bea)
	entrar(b)
	invitar(yo, ce)
	entrar(c)

	yo.send(protocol.TypeClanPromote, map[string]any{"playerId": bea.String(), "officer": true})
	env.pump()
	b.awaitWhere(protocol.TypeClanUpdate, func(raw json.RawMessage) bool {
		p := clanUpdate(t, raw)
		if p.Clan == nil {
			return false
		}
		for _, m := range p.Clan.Members {
			if m.PlayerID == bea.String() {
				return m.Role == clan.RoleOfficer
			}
		}
		return false
	}, 3*time.Second)

	// El oficial invita a dani y a eva; eva entra y el clan entero recibe su vista.
	invitar(b, dani)
	invitar(b, eva)
	vistaDeEva := entrar(e)
	assert.Empty(t, enviadasEnElJSON(t, vistaDeEva), "ni quien acaba de entrar")
	deCuatro := func(raw json.RawMessage) bool {
		p := clanUpdate(t, raw)
		return p.Clan != nil && len(p.Clan.Members) == 4
	}
	vistaDelOficial, _ := b.awaitWhere(protocol.TypeClanUpdate, deCuatro, 3*time.Second)
	assert.Equal(t, []string{dani.String()}, enviadasEnElJSON(t, vistaDelOficial.Payload), "el oficial ve la que sigue viva")
	vistaDelLider, _ := yo.awaitWhere(protocol.TypeClanUpdate, deCuatro, 3*time.Second)
	assert.Equal(t, []string{dani.String()}, enviadasEnElJSON(t, vistaDelLider.Payload))
	vistaDelRaso, _ := c.awaitWhere(protocol.TypeClanUpdate, deCuatro, 3*time.Second)
	assert.Empty(t, enviadasEnElJSON(t, vistaDelRaso.Payload), "un raso no ve las enviadas")
}
