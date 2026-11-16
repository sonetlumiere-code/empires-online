package simulation

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/clan"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/diplomacy"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/player"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// Las facciones en la simulación (SPEC-FACTIONS, ADR-039).
//
// La facción efectiva de un jugador y su entrada confirmada viven en su ciudad
// (city.City.Faction y FactionJoin), como la civilización. Unirse va en dos
// fases, como el encargo de un avance de era: el tick reserva la entrada
// —invisible para todos—, la cola la escribe y el buzón la confirma. El
// vencimiento, en la fase 5, es optimista: la RAM cambia la facción y rompe los
// pactos y alianzas con el bando contrario, y un único trabajo lo escribe todo
// en una transacción, con su reencolado (ADR-039 §4).

// defaultFactionJoinDelay es el preaviso de una entrada (SPEC-FACTIONS Q3).
const defaultFactionJoinDelay = 15 * time.Minute

// ErrFactionConflict: la base no admite la entrada porque el jugador ya no es
// NEUTRAL o ya tiene otra entrada. Es un rechazo de dominio: no se reintenta.
var ErrFactionConflict = errors.New("la facción del jugador cambió por otra vía")

// FactionStore escribe la entrada en un bando y su vencimiento. Lo implementa
// persistence.FactionStore. Es idempotente: la cola puede reintentar.
type FactionStore interface {
	// RequestJoin apunta la entrada pendiente, sólo si el jugador sigue NEUTRAL
	// sin entrada; un reintento con la misma entrada termina sin error.
	RequestJoin(ctx context.Context, o FactionJoinOrder) error
	// CompleteJoin escribe en una transacción la facción, el borrado de la
	// entrada y cada ruptura, de forma tolerante (ADR-039 §4).
	CompleteJoin(ctx context.Context, c FactionJoinCompletion) error
}

// FactionJoinOrder es una entrada en un bando ya validada por el tick.
type FactionJoinOrder struct {
	PlayerID  uuid.UUID
	RequestID string
	Faction   string
	// At es el instante en que la entrada cuenta.
	At   time.Time
	Tick uint64
	AtMs int64
}

// FactionJoinCompletion es el vencimiento de una entrada: la facción nueva y los
// pactos y alianzas que rompe.
type FactionJoinCompletion struct {
	PlayerID uuid.UUID
	Faction  string
	// At es el instante de la entrada que vence.
	At     time.Time
	Breaks []TreatyWrite
	// AllianceBreaks son las alianzas del clan del jugador a las que la
	// entrada abre el preaviso (ADR-042 §4).
	AllianceBreaks []AllianceWrite
	Tick           uint64
	AtMs           int64
}

// JoinFaction pide unirse a un bando (protocolo v1, faction.join).
type JoinFaction struct {
	PlayerID  uuid.UUID
	SessionID uuid.UUID
	RequestID string
	Faction   string
}

func (JoinFaction) commandType() string { return protocol.TypeFactionJoin }

// FactionJoinResolved lo devuelve la cola con el resultado de la escritura de
// una entrada. Code vacío: la entrada está escrita.
type FactionJoinResolved struct {
	PlayerID  uuid.UUID
	RequestID string
	Join      city.FactionJoin
	Code      string
}

func (FactionJoinResolved) commandType() string { return "faction.join_resolved" }

// FactionJoinCompletePersistFailed es la compensación de un vencimiento que no
// se escribió: el volcado siguiente lo reencola (ADR-022).
type FactionJoinCompletePersistFailed struct {
	Completion FactionJoinCompletion
}

func (FactionJoinCompletePersistFailed) commandType() string { return "faction.joined_failed" }

// Nombres de los trabajos de las facciones (catálogo JobNames).
const (
	JobFactionJoin   = "faction.join"
	JobFactionJoined = "faction.joined"
)

// PlayerKey es la clave de la cola de las escrituras de un jugador.
func PlayerKey(id uuid.UUID) string { return "player:" + id.String() }

// ─────────────────────────────────────────────────────────────
// Consultas
// ─────────────────────────────────────────────────────────────

// FactionOf es la facción efectiva de un jugador: la de su ciudad, o NEUTRAL.
func (s *State) FactionOf(playerID uuid.UUID) string {
	c, ok := s.CityOf(playerID)
	if !ok {
		return player.FactionNeutral
	}
	return player.EffectiveFaction(c.Faction)
}

// OrderPeace dice si dos jugadores están en paz por su bando: los dos son de la
// Orden (RN-FAC-003, ADR-039 §2).
func (s *State) OrderPeace(x, y uuid.UUID) bool {
	return player.OrderPeace(s.FactionOf(x), s.FactionOf(y))
}

// opposedTreaty dice si un tratado de ese tipo está prohibido entre dos
// jugadores por su bando (RN-FAC-005).
func (s *State) opposedTreaty(x, y uuid.UUID, t diplomacy.Type) bool {
	if t != diplomacy.TypeNonAggression && t != diplomacy.TypeAlliance {
		return false
	}
	return player.Opposed(s.FactionOf(x), s.FactionOf(y))
}

// Mensajes de los rechazos por el bando.
const (
	msgOrderPeace   = "los dos sois de la Orden"
	msgOpposedPeace = "la Orden y el Caos no firman la paz"
)

func (s *Simulation) factionJoinDelay() time.Duration {
	if s.deps.FactionJoinDelay > 0 {
		return s.deps.FactionJoinDelay
	}
	return defaultFactionJoinDelay
}

func (s *Simulation) affineDamagePerMille() int32 {
	if s.deps.FactionAffineDamagePerMille > 0 {
		return s.deps.FactionAffineDamagePerMille
	}
	return player.DefaultAffineDamagePerMille
}

// damageTaken es el daño que recibe una unidad de un golpe: menos en un tile de
// territorio afín a su dueño (RN-FAC-007, RN-FAC-008, ADR-039 §5).
func (s *Simulation) damageTaken(target *unit.Unit, damage int32, nowMs int64) int32 {
	pos := s.authoritativePosition(target, nowMs)
	t, ok := s.state.TerritoryAt(pos.X, pos.Y)
	if !ok {
		return damage
	}
	control, ok := s.state.TerritoryControl(t.ID)
	if !ok {
		return damage
	}
	// RN-FAC-007, con la tierra del propio clan desde E19 (RN-CLANTERR-003).
	if !s.state.territoryAffine(control, target.PlayerID) {
		return damage
	}
	return player.AffineDamage(damage, s.affineDamagePerMille())
}

// factionJoinView es la entrada confirmada de una ciudad, o nil.
func factionJoinView(c *city.City) *protocol.FactionJoinView {
	if c.FactionJoin == nil {
		return nil
	}
	return &protocol.FactionJoinView{Faction: c.FactionJoin.Faction, AtMs: c.FactionJoin.AtMs}
}

// broadcastFaction difunde la facción de una ciudad a su chunk y a su dueño,
// esté donde esté su vista (el HUD la necesita); el dueño la recibe una vez,
// con el requestId de su orden si lo hay.
func (s *Simulation) broadcastFaction(c *city.City, payload protocol.CityUpdatePayload, requestID string) {
	payload.ID = c.ID
	cx, cy := s.state.World().ChunkOf(c.CenterX, c.CenterY)
	s.deps.Broadcaster.BroadcastChunkExcept(cx, cy, c.OwnerPlayerID, protocol.TypeCityUpdate, payload)
	s.deps.Broadcaster.SendToPlayer(c.OwnerPlayerID, protocol.TypeCityUpdate, requestID, payload)
}

// ─────────────────────────────────────────────────────────────
// faction.join (dos fases)
// ─────────────────────────────────────────────────────────────

func (s *Simulation) handleJoinFaction(cmd JoinFaction) {
	if cmd.Faction != player.FactionOrder && cmd.Faction != player.FactionChaos {
		s.economyError(cmd.PlayerID, cmd.RequestID, protocol.CodeInvalidMessage, "sólo puedes unirte a la Orden o al Caos")
		return
	}
	if s.deps.Factions == nil {
		s.economyError(cmd.PlayerID, cmd.RequestID, protocol.CodeInternalError, "las facciones no están disponibles")
		return
	}
	c, ok := s.state.CityOf(cmd.PlayerID)
	if !ok {
		s.economyError(cmd.PlayerID, cmd.RequestID, protocol.CodeInvalidTarget, "no tienes ciudad")
		return
	}
	// RN-FAC-001 e INV-PLAYER-013: sólo desde NEUTRAL, una vez.
	if !player.CanJoin(c.Faction, cmd.Faction) {
		s.economyError(cmd.PlayerID, cmd.RequestID, protocol.CodeInvalidTarget, "ya perteneces a un bando")
		return
	}
	if _, reservada := s.state.factionJoining[cmd.PlayerID]; reservada || c.FactionJoin != nil {
		s.economyError(cmd.PlayerID, cmd.RequestID, protocol.CodeInvalidTarget, "ya te estás uniendo a un bando")
		return
	}
	// RN-CLAN-006 e INV-CLAN-002: un clan no junta a la Orden y al Caos.
	if blocked, why := s.state.clanBlocksFaction(cmd.PlayerID, cmd.Faction); blocked {
		s.economyError(cmd.PlayerID, cmd.RequestID, protocol.CodeInvalidTarget, why)
		return
	}

	nowMs := s.deps.Clock.NowMs()
	at := time.UnixMilli(nowMs).UTC().Add(s.factionJoinDelay())
	join := city.FactionJoin{Faction: cmd.Faction, AtMs: at.UnixMilli()}
	s.state.factionJoining[cmd.PlayerID] = join

	order := FactionJoinOrder{
		PlayerID: cmd.PlayerID, RequestID: cmd.RequestID, Faction: cmd.Faction, At: at,
		Tick: s.state.Tick(), AtMs: nowMs,
	}
	resolved := FactionJoinResolved{PlayerID: cmd.PlayerID, RequestID: cmd.RequestID, Join: join}
	store := s.deps.Factions
	s.deps.Persister.Submit(Job{
		Name: JobFactionJoin,
		Key:  PlayerKey(cmd.PlayerID),
		Run: func(ctx context.Context) error {
			err := store.RequestJoin(ctx, order)
			if err == nil {
				s.reinject(resolved)
				return nil
			}
			if errors.Is(err, ErrFactionConflict) {
				r := resolved
				r.Code = protocol.CodeInvalidTarget
				s.reinject(r)
				return nil
			}
			return err
		},
		OnPermanentFailure: func(err error) {
			s.deps.Log.Error("no se pudo escribir la entrada en un bando", "err", err)
			r := resolved
			r.Code = protocol.CodeInternalError
			s.reinject(r)
		},
	})
}

// handleFactionJoinResolved confirma la entrada y la difunde, o la deshace y
// responde el rechazo.
func (s *Simulation) handleFactionJoinResolved(cmd FactionJoinResolved) {
	reservada, ok := s.state.factionJoining[cmd.PlayerID]
	if !ok || reservada != cmd.Join {
		return
	}
	delete(s.state.factionJoining, cmd.PlayerID)
	if cmd.Code != "" {
		s.economyError(cmd.PlayerID, cmd.RequestID, cmd.Code, "no se pudo apuntar la entrada en el bando")
		return
	}
	c, ok := s.state.CityOf(cmd.PlayerID)
	if !ok {
		return
	}
	join := cmd.Join
	c.FactionJoin = &join
	view := factionJoinView(c)
	s.broadcastFaction(c, protocol.CityUpdatePayload{FactionJoin: &view}, cmd.RequestID)
	s.deps.Log.Info("entrada en un bando apuntada", "city_id", c.ID, "faction", join.Faction, "at_ms", join.AtMs)
}

// ─────────────────────────────────────────────────────────────
// Fase 5: entradas que vencen
// ─────────────────────────────────────────────────────────────

// completeFactionJoins aplica, en orden de ciudad, las entradas confirmadas que
// vencieron (RN-FAC-002, RN-FAC-006). Una entrada espera al tick siguiente si
// algún tratado que va a romper tiene una orden en vuelo (ADR-039 §4).
func (s *Simulation) completeFactionJoins(nowMs int64) {
	s.state.EachCity(func(c *city.City) bool {
		if c.FactionJoin == nil || nowMs < c.FactionJoin.AtMs {
			return true
		}
		join := *c.FactionJoin
		if !player.CanJoin(c.Faction, join.Faction) {
			// INV-PLAYER-013: la carga y la orden lo impiden; no hay ruta.
			s.deps.Log.Error("invariant_violation",
				"inv_id", "INV-PLAYER-013", "severity", "ALTO", "policy", "REJECT",
				"entity", "player:"+c.OwnerPlayerID.String(), "tick", s.state.Tick(),
				"detail", "entrada en "+join.Faction+" de un jugador "+player.EffectiveFaction(c.Faction)+": se descarta")
			c.FactionJoin = nil
			return true
		}
		rotos, ok := s.treatiesToBreak(c.OwnerPlayerID, join.Faction)
		if !ok {
			return true // una orden de tratado en vuelo: el tick siguiente
		}
		// RN-CLANALLY-005: las alianzas de su clan con un clan del bando
		// contrario empiezan su preaviso; espera si alguna tiene una orden en
		// vuelo, como los tratados.
		var aliadas []*clan.Alliance
		if clanID, enClan := s.state.clans.memberOf[c.OwnerPlayerID]; enClan {
			if aliadas, ok = s.state.alliancesMixedBy(clanID, clan.Stance{Faction: join.Faction}); !ok {
				return true
			}
		}

		now := s.treatyNow()
		fin := now.Add(s.breakNotice())
		writes := make([]TreatyWrite, 0, len(rotos))
		for _, t := range rotos {
			after := *t
			finCopia := fin
			after.BreakEffectiveAt = &finCopia
			s.storeTreaty(after)
			s.broadcastTreaty(after, uuid.Nil, "")
			writes = append(writes, TreatyWrite{
				Treaty: after, From: diplomacy.StatusActive, Event: "TreatyBreakAnnounced",
				Tick: s.state.Tick(), AtMs: nowMs, By: c.OwnerPlayerID,
			})
		}

		allianceWrites := make([]AllianceWrite, 0, len(aliadas))
		for _, a := range aliadas {
			allianceWrites = append(allianceWrites, s.openAllianceNotice(a, c.OwnerPlayerID, fin.UnixMilli(), nowMs))
		}

		c.Faction = join.Faction
		c.FactionJoin = nil
		faction := c.Faction
		var ninguna *protocol.FactionJoinView
		s.broadcastFaction(c, protocol.CityUpdatePayload{Faction: &faction, FactionJoin: &ninguna}, "")
		s.broadcastOwnedTerritories(c.OwnerPlayerID)
		s.deps.Log.Info("jugador unido a un bando", "city_id", c.ID, "faction", faction, "treaties_broken", len(writes))

		s.submitFactionJoined(FactionJoinCompletion{
			PlayerID: c.OwnerPlayerID, Faction: join.Faction, At: time.UnixMilli(join.AtMs).UTC(),
			Breaks: writes, AllianceBreaks: allianceWrites, Tick: s.state.Tick(), AtMs: nowMs,
		})
		return true
	})
}

// treatiesToBreak son los pactos y alianzas vigentes sin preaviso de un jugador
// con el bando contrario al que entra, en orden de id; ok es false si alguno
// tiene una orden en vuelo.
func (s *Simulation) treatiesToBreak(playerID uuid.UUID, faction string) ([]*diplomacy.Treaty, bool) {
	var out []*diplomacy.Treaty
	for _, t := range s.state.diplo.open {
		if !t.HasParty(playerID) || t.Status != diplomacy.StatusActive || t.BreakEffectiveAt != nil {
			continue
		}
		if t.Type != diplomacy.TypeNonAggression && t.Type != diplomacy.TypeAlliance {
			continue
		}
		if !player.Opposed(faction, s.state.FactionOf(t.Other(playerID))) {
			continue
		}
		if _, ocupado := s.state.diplo.busy[pairKey(t.PlayerA, t.PlayerB)]; ocupado {
			return nil, false
		}
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, true
}

// broadcastOwnedTerritories vuelve a difundir los territorios de un jugador,
// con el bando nuevo de su dueño, en orden de id.
func (s *Simulation) broadcastOwnedTerritories(playerID uuid.UUID) {
	ids := make([]int64, 0)
	for id, c := range s.state.control {
		if owner, ok := c.PlayerOwner(); ok && owner == playerID {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		t, ok := s.state.territories.ByID(id)
		if !ok {
			continue
		}
		s.deps.Broadcaster.BroadcastChunks(s.state.TerritoryFootprint(id), protocol.TypeTerritoryUpdate,
			protocol.TerritoryUpdatePayload{Territory: s.state.TerritoryView(t)})
	}
}

func (s *Simulation) submitFactionJoined(fc FactionJoinCompletion) {
	if s.deps.Factions == nil {
		return
	}
	store := s.deps.Factions
	s.deps.Persister.Submit(Job{
		Name: JobFactionJoined,
		Key:  PlayerKey(fc.PlayerID),
		Run:  func(ctx context.Context) error { return store.CompleteJoin(ctx, fc) },
		OnPermanentFailure: func(err error) {
			s.deps.Log.Error("no se pudo escribir la entrada en un bando; se reintentará en el siguiente volcado",
				"err", err, "player_id", fc.PlayerID.String())
			s.reinject(FactionJoinCompletePersistFailed{Completion: fc})
		},
	})
}

func (s *Simulation) handleFactionJoinCompletePersistFailed(cmd FactionJoinCompletePersistFailed) {
	s.state.pendingFactionCompletions[cmd.Completion.PlayerID] = cmd.Completion
}

// flushFactionCompletions reencola los vencimientos descartados. Es
// idempotente: la escritura es tolerante (ADR-039 §4).
func (s *Simulation) flushFactionCompletions() {
	if len(s.state.pendingFactionCompletions) == 0 {
		return
	}
	ids := make([]uuid.UUID, 0, len(s.state.pendingFactionCompletions))
	for id := range s.state.pendingFactionCompletions {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i].String() < ids[j].String() })
	pend := s.state.pendingFactionCompletions
	s.state.pendingFactionCompletions = make(map[uuid.UUID]FactionJoinCompletion)
	for _, id := range ids {
		fc := pend[id]
		fc.Tick = s.state.Tick()
		s.submitFactionJoined(fc)
	}
}
