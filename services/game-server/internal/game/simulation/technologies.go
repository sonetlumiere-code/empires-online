package simulation

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/economy"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/player"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/technology"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// El árbol tecnológico en la simulación (SPEC-TECHNOLOGIES §4, ADR-046 §2 y §3).
//
// La investigación es un encargo en dos fases, como el avance de era: el tick
// valida y reserva el coste, y la transacción de la cola lo debita junto con la
// fila de city_research. El fin es optimista: la RAM da la tecnología al
// jugador, recompone sus rasgos y avisa al dueño, y la escritura lleva su
// reencolado.
//
// Los efectos no viven en ningún sistema: State.trait compone el rasgo de la
// civilización con las tecnologías del jugador (INV-TECH-002), y los puntos de
// consumo siguen preguntando por una clave.

// ResearchStore escribe la investigación. Lo implementa postgres.EconomyRepo,
// como el resto de EconomyStore.
type ResearchStore interface {
	// Research debita todo el coste y registra la investigación en una
	// transacción (RN-TECH-004). Un rechazo de dominio es
	// technology.ErrCityBusy, technology.ErrUnavailable,
	// economy.ErrInsufficientResources o ErrStaleStock.
	Research(ctx context.Context, o ResearchOrder) error
	// CompleteResearch da la tecnología al jugador y borra la investigación,
	// idempotente (RN-TECH-005).
	CompleteResearch(ctx context.Context, c ResearchCompletion) error
}

// ResearchOrder es un encargo de investigación con su débito.
type ResearchOrder struct {
	RequestID string
	PlayerID  uuid.UUID
	Research  technology.Research
	Stocks    []StockDelta
	Tick      uint64
}

// ResearchCompletion es el fin durable de una investigación.
type ResearchCompletion struct {
	CityID     int64
	PlayerID   uuid.UUID
	Technology string
	AtMs       int64
	Tick       uint64
}

// ResearchTechnology pide investigar una tecnología en una ciudad.
type ResearchTechnology struct {
	PlayerID   uuid.UUID
	SessionID  uuid.UUID
	RequestID  string
	CityID     int64
	Technology string
}

func (ResearchTechnology) commandType() string { return protocol.TypeCityResearch }

// ResearchResolved lo devuelve la cola con el resultado de la transacción de un
// encargo de investigación. Code vacío: está escrita con su débito.
type ResearchResolved struct {
	PlayerID  uuid.UUID
	RequestID string
	CityID    int64
	Code      string
}

func (ResearchResolved) commandType() string { return "city.research_resolved" }

// ResearchCompletePersistFailed es la compensación de un fin de investigación
// que no se escribió: el volcado siguiente lo reencola.
type ResearchCompletePersistFailed struct {
	Completion ResearchCompletion
}

func (ResearchCompletePersistFailed) commandType() string { return "city.research_complete_failed" }

// researchState es una investigación con su situación en el ciclo de dos fases.
type researchState struct {
	technology.Research
	costs   economy.Costs
	carried map[economy.Resource]int64
	pending bool // encargada, esperando su transacción
}

// techState es lo que la simulación sabe del árbol tecnológico.
type techState struct {
	catalog technology.Catalog
	// owned son las tecnologías de cada jugador.
	owned map[uuid.UUID]map[string]bool
	// traits es el rasgo compuesto de cada jugador que tiene alguna tecnología
	// (ADR-046 §2). Se rellena al consultarlo y se borra al cambiar sus
	// tecnologías; un jugador sin tecnologías usa el de su civilización.
	traits map[uuid.UUID]map[string]int32
	// pendingCompletions son los fines descartados por la cola, por ciudad.
	pendingCompletions map[int64]ResearchCompletion
}

func newTechState() techState {
	return techState{
		owned:              make(map[uuid.UUID]map[string]bool),
		traits:             make(map[uuid.UUID]map[string]int32),
		pendingCompletions: make(map[int64]ResearchCompletion),
	}
}

// ─────────────────────────────────────────────────────────────
// Estado
// ─────────────────────────────────────────────────────────────

// SetTechnologies instala el catálogo validado y las tecnologías de cada
// jugador leídas al arrancar. Una tecnología fuera del catálogo se ignora.
func (s *State) SetTechnologies(cat technology.Catalog, owned map[uuid.UUID][]string) {
	s.tech.catalog = cat
	s.tech.owned = make(map[uuid.UUID]map[string]bool, len(owned))
	s.tech.traits = make(map[uuid.UUID]map[string]int32)
	for p, codes := range owned {
		for _, code := range codes {
			if _, ok := cat.Lookup(code); !ok {
				continue
			}
			if s.tech.owned[p] == nil {
				s.tech.owned[p] = make(map[string]bool)
			}
			s.tech.owned[p][code] = true
		}
	}
}

// TechnologyCatalog es el catálogo instalado.
func (s *State) TechnologyCatalog() technology.Catalog { return s.tech.catalog }

// Technologies son las tecnologías de un jugador, en orden de código.
func (s *State) Technologies(playerID uuid.UUID) []string {
	out := make([]string, 0, len(s.tech.owned[playerID]))
	for code := range s.tech.owned[playerID] {
		out = append(out, code)
	}
	sort.Strings(out)
	return out
}

// trait es el rasgo efectivo de un jugador: el de su civilización compuesto con
// el de cada tecnología que tiene (SPEC-TECHNOLOGIES §4.4, INV-TECH-002). Es la
// ÚNICA vía para leer un rasgo.
func (s *State) trait(playerID uuid.UUID, key string) int32 {
	if len(s.tech.owned[playerID]) == 0 {
		return s.civilizationOf(playerID).Trait(key)
	}
	t, ok := s.tech.traits[playerID]
	if !ok {
		t = s.tech.catalog.Traits(s.civilizationOf(playerID), s.tech.owned[playerID])
		s.tech.traits[playerID] = t
	}
	if v, ok := t[key]; ok {
		return v
	}
	return player.TraitNeutral
}

// Trait es trait para los tests.
func (s *State) Trait(playerID uuid.UUID, key string) int32 { return s.trait(playerID, key) }

// grantTechnology da una tecnología a un jugador y olvida su rasgo compuesto.
func (s *State) grantTechnology(playerID uuid.UUID, code string) {
	if s.tech.owned[playerID] == nil {
		s.tech.owned[playerID] = make(map[string]bool)
	}
	s.tech.owned[playerID][code] = true
	delete(s.tech.traits, playerID)
}

// setResearch instala una investigación confirmada leída de la base.
func (s *State) setResearch(r technology.Research) {
	s.economyOf(r.CityID).research = &researchState{Research: r}
}

// Research devuelve la investigación de una ciudad y si está confirmada.
func (s *State) Research(cityID int64) (technology.Research, bool, bool) {
	eco, ok := s.economies[cityID]
	if !ok || eco.research == nil {
		return technology.Research{}, false, false
	}
	return eco.research.Research, !eco.research.pending, true
}

// researchView es la investigación CONFIRMADA de una ciudad, o nil.
func (s *State) researchView(cityID int64) *protocol.ResearchView {
	r, confirmed, ok := s.Research(cityID)
	if !ok || !confirmed {
		return nil
	}
	return &protocol.ResearchView{Technology: r.Technology, CompletesAtMs: r.CompletesAtMs}
}

// withOwnerFields añade a la vista de una ciudad lo que sólo ve su dueño: la
// investigación en curso y sus tecnologías (RN-TECH-008).
func (s *State) withOwnerFields(v protocol.CityView, c *city.City) protocol.CityView {
	research := s.researchView(c.ID)
	techs := s.Technologies(c.OwnerPlayerID)
	v.Research, v.Technologies = &research, &techs
	return v
}

// sendToOwner manda a su dueño un city.update con lo que sólo ve él.
func (s *Simulation) sendToOwner(c *city.City, payload protocol.CityUpdatePayload) {
	payload.ID = c.ID
	s.deps.Broadcaster.SendToPlayer(c.OwnerPlayerID, protocol.TypeCityUpdate, "", payload)
}

// researchMs es lo que tarda una investigación: el plazo del catálogo con
// production.speed, o el de EO_TECHNOLOGY_RESEARCH_SECONDS si está fijado.
func (s *State) researchMs(playerID uuid.UUID, baseMs int64, override time.Duration) int64 {
	if override > 0 {
		return override.Milliseconds()
	}
	return s.productionMs(playerID, baseMs)
}

// ─────────────────────────────────────────────────────────────
// city.research
// ─────────────────────────────────────────────────────────────

// handleResearchTechnology valida en el tick y reserva todo el coste; la
// transacción de la cola lo debita y escribe la investigación (RN-TECH-002 a
// RN-TECH-004).
func (s *Simulation) handleResearchTechnology(cmd ResearchTechnology) {
	c, _ := s.state.City(cmd.CityID)
	if c != nil && c.OwnerPlayerID == cmd.PlayerID && s.state.CityFalling(c.ID) {
		s.economyError(cmd.PlayerID, cmd.RequestID, protocol.CodeInvalidTarget, msgCityFalling)
		return
	}
	busy := false
	stock := func(economy.Resource) *economy.Stock { return nil }
	civ := player.Civilization{}
	if c != nil {
		eco := s.state.economyOf(c.ID)
		busy = eco.research != nil || eco.advance != nil
		stock = eco.stockOf
		civ = s.state.civilizationOf(c.OwnerPlayerID)
	}
	o, err := technology.CanResearch(c, cmd.PlayerID, cmd.Technology, s.state.tech.catalog, civ,
		s.state.tech.owned[cmd.PlayerID], busy, s.state.goodsOrder, stock)
	if err != nil {
		s.economyError(cmd.PlayerID, cmd.RequestID, economyErrorCode(err), err.Error())
		return
	}
	if s.deps.Economy == nil {
		s.economyError(cmd.PlayerID, cmd.RequestID, protocol.CodeInternalError, "la economía no está disponible")
		return
	}

	nowMs := s.deps.Clock.NowMs()
	eco := s.state.economyOf(c.ID)
	// La reserva, como la del avance de era (ADR-030 §3).
	carried := make(map[economy.Resource]int64, len(o.Costs))
	deltas := make([]StockDelta, 0, len(o.Costs))
	for i, cost := range o.Costs {
		rs := eco.of(cost.Resource)
		if err := rs.stock.Debit(cost); err != nil {
			for _, prev := range o.Costs[:i] {
				back := eco.of(prev.Resource)
				_ = back.stock.Credit(prev.Amount)
				back.unflushed += carried[prev.Resource]
			}
			s.state.markStockDirty(c.ID)
			s.economyError(cmd.PlayerID, cmd.RequestID, protocol.CodeInsufficientResources, err.Error())
			return
		}
		carried[cost.Resource] = rs.unflushed
		rs.unflushed = 0
		deltas = append(deltas, StockDelta{
			CityID: c.ID, Resource: cost.Resource, Delta: carried[cost.Resource] - cost.Amount, Version: rs.stock.Version,
		})
	}
	s.state.markStockDirty(c.ID)

	r := technology.Research{
		CityID: c.ID, Technology: o.Technology.Code, RequestID: cmd.RequestID,
		CompletesAtMs: nowMs + s.state.researchMs(cmd.PlayerID, o.Technology.DurationMs, s.deps.ResearchOverride),
	}
	eco.research = &researchState{Research: r, costs: o.Costs, carried: carried, pending: true}

	order := ResearchOrder{RequestID: cmd.RequestID, PlayerID: cmd.PlayerID, Research: r, Stocks: deltas, Tick: s.state.Tick()}
	resolved := ResearchResolved{PlayerID: cmd.PlayerID, RequestID: cmd.RequestID, CityID: c.ID}
	store := s.deps.Economy
	s.deps.Persister.Submit(Job{
		Name: JobCityResearch,
		Key:  CityKey(c.ID),
		Run: func(ctx context.Context) error {
			err := store.Research(ctx, order)
			if err == nil {
				s.reinject(resolved)
				return nil
			}
			if code := economyErrorCode(err); code != protocol.CodeInternalError || errors.Is(err, ErrStaleStock) {
				r := resolved
				r.Code = code
				s.reinject(r)
				return nil
			}
			return err
		},
		OnPermanentFailure: func(err error) {
			s.deps.Log.Error("no se pudo escribir el encargo de la investigación; la reserva vuelve a la ciudad",
				"err", err, "city_id", resolved.CityID)
			r := resolved
			r.Code = protocol.CodeInternalError
			s.reinject(r)
		},
	})
}

// handleResearchResolved confirma la investigación o devuelve toda la reserva.
func (s *Simulation) handleResearchResolved(cmd ResearchResolved) {
	c, ok := s.state.City(cmd.CityID)
	if !ok {
		return
	}
	eco := s.state.economyOf(c.ID)
	r := eco.research
	if r == nil || !r.pending || r.RequestID != cmd.RequestID {
		return
	}
	if cmd.Code != "" {
		// La transacción no escribió nada: lo reservado vuelve (INV-ECON-002).
		for _, cost := range r.costs {
			rs := eco.of(cost.Resource)
			_ = rs.stock.Credit(cost.Amount)
			rs.unflushed += r.carried[cost.Resource]
		}
		s.state.markStockDirty(c.ID)
		eco.research = nil
		s.economyError(cmd.PlayerID, cmd.RequestID, cmd.Code, "no se pudo encargar la investigación")
		s.broadcastStocks(c)
		return
	}
	r.pending = false
	// El stock lo ve todo el área, como siempre; la investigación, sólo el dueño.
	s.broadcastStocks(c)
	view := s.state.researchView(c.ID)
	s.sendToOwner(c, protocol.CityUpdatePayload{Research: &view})
}

// ─────────────────────────────────────────────────────────────
// Fase 5: investigaciones que vencen
// ─────────────────────────────────────────────────────────────

// completeResearch termina, en orden de ciudad, las investigaciones
// confirmadas que vencieron (RN-TECH-005). Es optimista: la RAM y el mensaje
// van primero, y la escritura lleva su reencolado.
func (s *Simulation) completeResearch(nowMs int64) {
	s.state.EachCity(func(c *city.City) bool {
		eco, ok := s.state.economies[c.ID]
		if !ok || eco.research == nil {
			return true
		}
		r := eco.research
		if r.pending || !r.Due(nowMs) {
			return true
		}
		eco.research = nil
		s.state.grantTechnology(c.OwnerPlayerID, r.Technology)
		var ninguna *protocol.ResearchView
		techs := s.state.Technologies(c.OwnerPlayerID)
		s.sendToOwner(c, protocol.CityUpdatePayload{Research: &ninguna, Technologies: &techs})
		s.submitResearchComplete(ResearchCompletion{
			CityID: c.ID, PlayerID: c.OwnerPlayerID, Technology: r.Technology, AtMs: r.CompletesAtMs, Tick: s.state.Tick(),
		})
		return true
	})
}

func (s *Simulation) submitResearchComplete(rc ResearchCompletion) {
	if s.deps.Economy == nil {
		return
	}
	store := s.deps.Economy
	s.deps.Persister.Submit(Job{
		Name: JobResearchComplete,
		Key:  CityKey(rc.CityID),
		Run:  func(ctx context.Context) error { return store.CompleteResearch(ctx, rc) },
		OnPermanentFailure: func(err error) {
			s.deps.Log.Error("no se pudo escribir el fin de la investigación; se reintentará en el siguiente volcado",
				"err", err, "city_id", rc.CityID)
			s.reinject(ResearchCompletePersistFailed{Completion: rc})
		},
	})
}

func (s *Simulation) handleResearchCompletePersistFailed(cmd ResearchCompletePersistFailed) {
	s.state.tech.pendingCompletions[cmd.Completion.CityID] = cmd.Completion
}

// flushResearchCompletions reencola los fines descartados, en orden de ciudad.
// Es idempotente: la escritura no da dos veces la misma tecnología.
func (s *Simulation) flushResearchCompletions() {
	if len(s.state.tech.pendingCompletions) == 0 {
		return
	}
	ids := make([]int64, 0, len(s.state.tech.pendingCompletions))
	for id := range s.state.tech.pendingCompletions {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	pend := s.state.tech.pendingCompletions
	s.state.tech.pendingCompletions = make(map[int64]ResearchCompletion)
	for _, id := range ids {
		rc := pend[id]
		rc.Tick = s.state.Tick()
		s.submitResearchComplete(rc)
	}
}
