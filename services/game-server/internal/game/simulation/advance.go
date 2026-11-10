package simulation

import (
	"context"
	"errors"
	"sort"
	"strconv"

	"github.com/google/uuid"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/advance"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/economy"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// El avance de era en la simulación (SPEC-ECON-STONE §4.4, ADR-030 §4 y §5).
//
// El encargo es en dos fases, como el entrenamiento: el tick valida y reserva
// todo el coste —madera y piedra—, y la transacción de la cola lo debita entero
// junto con la fila del avance. El fin es optimista, como la compleción de una
// casa: la RAM cambia la era y sube el límite en cap(to) − cap(from), y la
// escritura lleva su reencolado.

// AdvanceStore escribe el avance. Lo implementa postgres.EconomyRepo, como el
// resto de EconomyStore.
type AdvanceStore interface {
	// Advance debita todo el coste y registra el avance en una transacción
	// (RN-ECON-025). Un rechazo de dominio es advance.ErrCityBusy,
	// advance.ErrUnavailable, economy.ErrInsufficientResources o
	// ErrStaleStock.
	Advance(ctx context.Context, o AdvanceOrder) error
	// CompleteAdvance cambia la era y sube el límite, sólo si la ciudad seguía
	// en la era de partida (RN-ECON-027, INV-CITY-018).
	CompleteAdvance(ctx context.Context, c AdvanceCompletion) error
}

// AdvanceOrder es un encargo de avance con su débito, un delta por recurso.
type AdvanceOrder struct {
	RequestID string
	PlayerID  uuid.UUID
	Advance   advance.Advance
	Stocks    []StockDelta
	Tick      uint64
}

// AdvanceCompletion es el fin durable de un avance.
type AdvanceCompletion struct {
	CityID   int64
	PlayerID uuid.UUID
	From     city.Era
	To       city.Era
	Tick     uint64
}

// AdvanceEra pide avanzar una ciudad a la era siguiente.
type AdvanceEra struct {
	PlayerID  uuid.UUID
	SessionID uuid.UUID
	RequestID string
	CityID    int64
}

func (AdvanceEra) commandType() string { return protocol.TypeCityAdvance }

// AdvanceResolved lo devuelve la cola con el resultado de la transacción de un
// encargo de avance. Code vacío: el avance está escrito con su débito.
type AdvanceResolved struct {
	PlayerID  uuid.UUID
	RequestID string
	CityID    int64
	Code      string
}

func (AdvanceResolved) commandType() string { return "city.advance_resolved" }

// AdvanceCompletePersistFailed es la compensación de un fin de avance que no
// se escribió: el volcado siguiente lo reencola (ADR-030 §5).
type AdvanceCompletePersistFailed struct {
	Completion AdvanceCompletion
}

func (AdvanceCompletePersistFailed) commandType() string { return "city.advance_complete_failed" }

// advanceState es un avance con su situación en el ciclo de dos fases.
type advanceState struct {
	advance.Advance
	costs economy.Costs
	// carried son los depósitos sin volcar de cada recurso del coste que
	// viajaron con el encargo.
	carried map[economy.Resource]int64
	pending bool // encargado, esperando su transacción
}

// ─────────────────────────────────────────────────────────────
// Estado
// ─────────────────────────────────────────────────────────────

// SetEras instala el catálogo de eras: el population_cap de cada una, leído de
// la tabla eras (RN-CITY-015). Sin él, ningún avance termina.
func (s *State) SetEras(defs []city.EraDefinition) {
	s.eraCaps = make(map[city.Era]int32, len(defs))
	for _, d := range defs {
		s.eraCaps[d.Code] = d.PopulationCap
	}
}

// eraCap es el population_cap de una era del catálogo.
func (s *State) eraCap(e city.Era) (int32, bool) {
	c, ok := s.eraCaps[e]
	return c, ok
}

// setAdvance instala un avance confirmado leído de la base.
func (s *State) setAdvance(a advance.Advance) {
	step, _ := advance.Next(a.From)
	s.economyOf(a.CityID).advance = &advanceState{Advance: a, costs: step.Costs}
}

// Advance devuelve el avance de una ciudad y si está confirmado por la base.
func (s *State) Advance(cityID int64) (advance.Advance, bool, bool) {
	eco, ok := s.economies[cityID]
	if !ok || eco.advance == nil {
		return advance.Advance{}, false, false
	}
	return eco.advance.Advance, !eco.advance.pending, true
}

// advanceView es el avance CONFIRMADO de una ciudad, o nil: el que espera su
// transacción todavía no existe para nadie.
func (s *State) advanceView(cityID int64) *protocol.AdvanceView {
	a, confirmed, ok := s.Advance(cityID)
	if !ok || !confirmed {
		return nil
	}
	return &protocol.AdvanceView{Era: string(a.To), CompletesAtMs: a.CompletesAtMs}
}

// ─────────────────────────────────────────────────────────────
// city.advance
// ─────────────────────────────────────────────────────────────

// handleAdvanceEra valida en el tick y reserva todo el coste; la transacción de
// la cola lo debita y escribe el avance (RN-ECON-024, RN-ECON-025).
func (s *Simulation) handleAdvanceEra(cmd AdvanceEra) {
	c, _ := s.state.City(cmd.CityID)
	busy := false
	stock := func(economy.Resource) *economy.Stock { return nil }
	if c != nil {
		eco := s.state.economyOf(c.ID)
		busy = eco.production != nil || eco.advance != nil
		stock = eco.stockOf
	}
	// SPEC-CIVILIZATION-GOODS RN-GOODS-006: los bienes ajenos se resuelven con
	// el bien de la civilización del dueño y el orden del catálogo.
	var own economy.Resource
	if c != nil {
		own = s.state.civilizationOf(c.OwnerPlayerID).Good
	}
	step, err := advance.CanAdvance(c, cmd.PlayerID, busy, own, s.state.goodsOrder, stock)
	if err != nil {
		s.economyError(cmd.PlayerID, cmd.RequestID, economyErrorCode(err), err.Error())
		return
	}
	if s.deps.Economy == nil {
		s.economyError(cmd.PlayerID, cmd.RequestID, protocol.CodeInternalError, "la economía no está disponible")
		return
	}
	// Sin los population_cap de las dos eras no se puede subir el límite al
	// terminar: mejor no cobrar (RN-CITY-015).
	_, okFrom := s.state.eraCap(step.From)
	_, okTo := s.state.eraCap(step.To)
	if !okFrom || !okTo {
		s.economyError(cmd.PlayerID, cmd.RequestID, protocol.CodeInternalError, "el catálogo de eras no está cargado")
		return
	}

	nowMs := s.deps.Clock.NowMs()
	eco := s.state.economyOf(c.ID)
	// La reserva: todos los recursos salen ya de la RAM —CanAdvance comprobó
	// que alcanzan todos—, y los depósitos sin volcar de cada uno viajan con
	// el encargo, en su transacción (ADR-030 §3).
	carried := make(map[economy.Resource]int64, len(step.Costs))
	deltas := make([]StockDelta, 0, len(step.Costs))
	for i, cost := range step.Costs {
		rs := eco.of(cost.Resource)
		if err := rs.stock.Debit(cost); err != nil {
			// No hay ruta que llegue aquí; se deshace lo ya reservado.
			for _, prev := range step.Costs[:i] {
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

	a := advance.Advance{
		CityID: c.ID, From: step.From, To: step.To, CompletesAtMs: nowMs + step.DurationMs, RequestID: cmd.RequestID,
	}
	eco.advance = &advanceState{Advance: a, costs: step.Costs, carried: carried, pending: true}

	order := AdvanceOrder{RequestID: cmd.RequestID, PlayerID: cmd.PlayerID, Advance: a, Stocks: deltas, Tick: s.state.Tick()}
	resolved := AdvanceResolved{PlayerID: cmd.PlayerID, RequestID: cmd.RequestID, CityID: c.ID}
	store := s.deps.Economy
	s.deps.Persister.Submit(Job{
		Name: JobCityAdvance,
		Key:  CityKey(c.ID),
		Run: func(ctx context.Context) error {
			err := store.Advance(ctx, order)
			if err == nil {
				s.reinject(resolved)
				return nil
			}
			if code := economyErrorCode(err); code != protocol.CodeInternalError || errors.Is(err, ErrStaleStock) {
				// Un rechazo de dominio no se reintenta: es la respuesta.
				r := resolved
				r.Code = code
				s.reinject(r)
				return nil
			}
			return err
		},
		OnPermanentFailure: func(err error) {
			s.deps.Log.Error("no se pudo escribir el encargo del avance; la reserva vuelve a la ciudad",
				"err", err, "city_id", resolved.CityID)
			r := resolved
			r.Code = protocol.CodeInternalError
			s.reinject(r)
		},
	})
}

// handleAdvanceResolved confirma el avance o devuelve toda la reserva.
func (s *Simulation) handleAdvanceResolved(cmd AdvanceResolved) {
	c, ok := s.state.City(cmd.CityID)
	if !ok {
		return
	}
	eco := s.state.economyOf(c.ID)
	a := eco.advance
	if a == nil || !a.pending || a.RequestID != cmd.RequestID {
		return
	}

	if cmd.Code != "" {
		// La transacción no escribió nada: cada recurso reservado y los
		// depósitos que viajaban con él vuelven a la ciudad (INV-ECON-002).
		for _, cost := range a.costs {
			rs := eco.of(cost.Resource)
			_ = rs.stock.Credit(cost.Amount)
			rs.unflushed += a.carried[cost.Resource]
		}
		s.state.markStockDirty(c.ID)
		eco.advance = nil
		s.economyError(cmd.PlayerID, cmd.RequestID, cmd.Code, "no se pudo encargar el avance de era")
		s.broadcastStocks(c)
		return
	}

	a.pending = false
	stocks := s.state.stocksView(c.ID)
	view := s.state.advanceView(c.ID)
	s.broadcastCity(c, protocol.CityUpdatePayload{Stocks: &stocks, Advance: &view})
}

// ─────────────────────────────────────────────────────────────
// Fase 5: avances que vencen
// ─────────────────────────────────────────────────────────────

// completeAdvances termina, en orden de ciudad, los avances confirmados que
// vencieron (RN-ECON-027). Es optimista: la RAM y los mensajes van primero, y
// la escritura lleva su reencolado (ADR-030 §5).
func (s *Simulation) completeAdvances(nowMs int64) {
	s.state.EachCity(func(c *city.City) bool {
		eco, ok := s.state.economies[c.ID]
		if !ok || eco.advance == nil {
			return true
		}
		a := eco.advance
		if a.pending || !a.Due(nowMs) {
			return true
		}
		if c.Era != a.From {
			// INV-CITY-018: un avance que no sale de la era de la ciudad no se
			// aplica. No hay ruta que lo produzca; la carga lo descarta antes.
			s.deps.Log.Error("invariant_violation",
				"inv_id", "INV-CITY-018", "severity", "ALTO", "policy", "REJECT",
				"entity", "city:"+strconv.FormatInt(c.ID, 10), "tick", s.state.Tick(),
				"detail", "avance de "+string(a.From)+" en una ciudad "+string(c.Era)+": se descarta")
			eco.advance = nil
			return true
		}
		fromCap, okFrom := s.state.eraCap(a.From)
		toCap, okTo := s.state.eraCap(a.To)
		if !okFrom || !okTo {
			// Sin el catálogo no hay límite que subir: el avance espera.
			return true
		}
		eco.advance = nil
		c.Era = a.To
		// RN-ECON-027: el límite sube en la diferencia de los population_cap;
		// las casas siguen sumando lo suyo (INV-ECON-004).
		c.PopulationLimit += toCap - fromCap

		era, limit := string(c.Era), c.PopulationLimit
		var ninguno *protocol.AdvanceView
		s.broadcastCity(c, protocol.CityUpdatePayload{Era: &era, PopulationLimit: &limit, Advance: &ninguno})
		s.submitAdvanceComplete(AdvanceCompletion{
			CityID: c.ID, PlayerID: c.OwnerPlayerID, From: a.From, To: a.To, Tick: s.state.Tick(),
		})
		return true
	})
}

func (s *Simulation) submitAdvanceComplete(ac AdvanceCompletion) {
	if s.deps.Economy == nil {
		return
	}
	store := s.deps.Economy
	s.deps.Persister.Submit(Job{
		Name: JobAdvanceComplete,
		Key:  CityKey(ac.CityID),
		Run:  func(ctx context.Context) error { return store.CompleteAdvance(ctx, ac) },
		OnPermanentFailure: func(err error) {
			s.deps.Log.Error("no se pudo escribir el fin del avance; se reintentará en el siguiente volcado",
				"err", err, "city_id", ac.CityID)
			s.reinject(AdvanceCompletePersistFailed{Completion: ac})
		},
	})
}

func (s *Simulation) handleAdvanceCompletePersistFailed(cmd AdvanceCompletePersistFailed) {
	s.state.pendingAdvanceCompletions[cmd.Completion.CityID] = cmd.Completion
}

// flushAdvanceCompletions reencola los fines de avance descartados. Es
// idempotente: la escritura sólo cambia la era si la ciudad seguía en la de
// partida.
func (s *Simulation) flushAdvanceCompletions() {
	if len(s.state.pendingAdvanceCompletions) == 0 {
		return
	}
	ids := make([]int64, 0, len(s.state.pendingAdvanceCompletions))
	for id := range s.state.pendingAdvanceCompletions {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	pend := s.state.pendingAdvanceCompletions
	s.state.pendingAdvanceCompletions = make(map[int64]AdvanceCompletion)
	for _, id := range ids {
		ac := pend[id]
		ac.Tick = s.state.Tick()
		s.submitAdvanceComplete(ac)
	}
}
