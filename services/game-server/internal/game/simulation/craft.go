package simulation

import (
	"context"
	"errors"
	"sort"

	"github.com/google/uuid"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/building"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/craft"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/economy"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// La elaboración del taller en la simulación (SPEC-CIVILIZATION-GOODS §4.3,
// ADR-036 §4).
//
// El encargo es una orden en dos fases, como el entrenamiento: el tick valida y
// reserva el coste, la transacción lo debita e inserta la fila, y sólo con la
// confirmación la elaboración existe para los demás. La entrega es optimista,
// como la finalización de una casa: al vencer, la RAM acredita el bien y lo
// difunde, y una transacción borra la fila y escribe el delta; si se descarta,
// se reintenta en el siguiente volcado con una versión nueva.

// Nombres de los trabajos de la elaboración (catálogo JobNames).
const (
	JobCityCraft     = "city.craft"
	JobCraftComplete = "craft.complete"
)

// CraftStore escribe la elaboración. Lo implementa persistence.EconomyStore.
type CraftStore interface {
	// Craft debita el coste e inserta la elaboración en una transacción
	// (RN-GOODS-004). Un rechazo de dominio es craft.ErrBusy,
	// craft.ErrNoWorkshop, economy.ErrInsufficientResources o ErrStaleStock.
	Craft(ctx context.Context, o CraftOrder) error
	// CompleteCraft borra la fila y acredita el bien en una transacción; sin
	// fila, no acredita nada (INV-ECON-009).
	CompleteCraft(ctx context.Context, d CraftDelivery) error
}

// CraftOrder es un encargo de elaboración con su débito.
type CraftOrder struct {
	RequestID string
	PlayerID  uuid.UUID
	Craft     craft.Craft
	Stocks    []StockDelta
	Tick      uint64
}

// CraftDelivery es la entrega durable de una elaboración.
type CraftDelivery struct {
	CityID    int64
	RequestID string
	PlayerID  uuid.UUID
	// Stock es el bien acreditado, con los depósitos sin volcar de ese
	// recurso que viajan con él, y su versión.
	Stock StockDelta
	Tick  uint64
}

// CraftGoods pide al taller de una ciudad una elaboración.
type CraftGoods struct {
	PlayerID  uuid.UUID
	SessionID uuid.UUID
	RequestID string
	CityID    int64
}

func (CraftGoods) commandType() string { return protocol.TypeCityCraft }

// CraftResolved lo devuelve la cola con el resultado de un encargo.
type CraftResolved struct {
	PlayerID  uuid.UUID
	RequestID string
	CityID    int64
	// Code vacío: confirmada. Con valor: rechazada o fallida.
	Code string
}

func (CraftResolved) commandType() string { return "city.craft_resolved" }

// CraftDeliverPersistFailed es la compensación de una entrega que no se pudo
// escribir: se reintenta en el siguiente volcado.
type CraftDeliverPersistFailed struct {
	Delivery CraftDelivery
}

func (CraftDeliverPersistFailed) commandType() string { return "craft.deliver_failed" }

// craftState es una elaboración con su situación en el ciclo de dos fases.
type craftState struct {
	craft.Craft
	costs economy.Costs
	// carried son los depósitos sin volcar de cada recurso del coste que
	// viajaron con el encargo.
	carried map[economy.Resource]int64
	pending bool // encargada, esperando su transacción
}

// setCraft instala una elaboración confirmada leída de la base.
func (s *State) setCraft(c craft.Craft) {
	s.economyOf(c.CityID).craft = &craftState{Craft: c, costs: craft.Costs()}
}

// Craft devuelve la elaboración de una ciudad y si está confirmada.
func (s *State) Craft(cityID int64) (craft.Craft, bool, bool) {
	eco, ok := s.economies[cityID]
	if !ok || eco.craft == nil {
		return craft.Craft{}, false, false
	}
	return eco.craft.Craft, !eco.craft.pending, true
}

// craftView es la elaboración CONFIRMADA de una ciudad, o nil.
func (s *State) craftView(cityID int64) *protocol.CraftView {
	c, confirmed, ok := s.Craft(cityID)
	if !ok || !confirmed {
		return nil
	}
	return &protocol.CraftView{Good: string(c.Good), Amount: c.Amount, CompletesAtMs: c.CompletesAtMs}
}

// hasCompleteWorkshop dice si la ciudad tiene un taller completo confirmado.
func (s *State) hasCompleteWorkshop(cityID int64) bool {
	for _, b := range s.BuildingsOf(cityID) {
		if b.Type == building.TypeWorkshop && b.IsComplete() {
			return true
		}
	}
	return false
}

// ─────────────────────────────────────────────────────────────
// city.craft
// ─────────────────────────────────────────────────────────────

// handleCraftGoods valida en el tick y reserva el coste; la transacción de la
// cola lo debita y escribe la elaboración (RN-GOODS-004).
func (s *Simulation) handleCraftGoods(cmd CraftGoods) {
	c, _ := s.state.City(cmd.CityID)
	busy, workshop := false, false
	stock := func(economy.Resource) *economy.Stock { return nil }
	if c != nil {
		eco := s.state.economyOf(c.ID)
		busy, workshop, stock = eco.craft != nil, s.state.hasCompleteWorkshop(c.ID), eco.stockOf
	}
	costs, err := craft.CanCraft(c, cmd.PlayerID, workshop, busy, stock)
	if err != nil {
		s.economyError(cmd.PlayerID, cmd.RequestID, economyErrorCode(err), err.Error())
		return
	}
	if s.deps.Economy == nil {
		s.economyError(cmd.PlayerID, cmd.RequestID, protocol.CodeInternalError, "la economía no está disponible")
		return
	}
	// RN-GOODS-002: el bien es el de la civilización del dueño, del catálogo.
	good := s.state.civilizationOf(c.OwnerPlayerID).Good
	if !good.IsGood() {
		s.economyError(cmd.PlayerID, cmd.RequestID, protocol.CodeInternalError, "la civilización de la ciudad no tiene bien")
		return
	}

	nowMs := s.deps.Clock.NowMs()
	eco := s.state.economyOf(c.ID)
	carried, deltas, ok := s.reserve(c, eco, costs)
	if !ok {
		s.economyError(cmd.PlayerID, cmd.RequestID, protocol.CodeInsufficientResources, "recursos insuficientes")
		return
	}
	cr := craft.Craft{
		CityID: c.ID, Good: good, Amount: craft.Amount,
		CompletesAtMs: nowMs + s.state.productionMs(cmd.PlayerID, craft.DurationMs), RequestID: cmd.RequestID,
	}
	eco.craft = &craftState{Craft: cr, costs: costs, carried: carried, pending: true}

	order := CraftOrder{RequestID: cmd.RequestID, PlayerID: cmd.PlayerID, Craft: cr, Stocks: deltas, Tick: s.state.Tick()}
	resolved := CraftResolved{PlayerID: cmd.PlayerID, RequestID: cmd.RequestID, CityID: c.ID}
	store := s.deps.Economy
	s.deps.Persister.Submit(Job{
		Name: JobCityCraft,
		Key:  CityKey(c.ID),
		Run: func(ctx context.Context) error {
			err := store.Craft(ctx, order)
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
			s.deps.Log.Error("no se pudo escribir el encargo de la elaboración; la reserva vuelve a la ciudad",
				"err", err, "city_id", resolved.CityID)
			r := resolved
			r.Code = protocol.CodeInternalError
			s.reinject(r)
		},
	})
}

// reserve saca de la RAM cada recurso de un coste, con los depósitos sin volcar
// de cada uno, y devuelve sus deltas (ADR-023 §1, ADR-030 §3). Si alguno no
// alcanza, deshace lo reservado y devuelve false.
func (s *Simulation) reserve(c *city.City, eco *cityEconomy, costs economy.Costs) (map[economy.Resource]int64, []StockDelta, bool) {
	carried := make(map[economy.Resource]int64, len(costs))
	deltas := make([]StockDelta, 0, len(costs))
	for i, cost := range costs {
		rs := eco.of(cost.Resource)
		if err := rs.stock.Debit(cost); err != nil {
			s.refund(eco, costs[:i], carried)
			return nil, nil, false
		}
		carried[cost.Resource] = rs.unflushed
		rs.unflushed = 0
		deltas = append(deltas, StockDelta{
			CityID: c.ID, Resource: cost.Resource, Delta: carried[cost.Resource] - cost.Amount, Version: rs.stock.Version,
		})
	}
	s.state.markStockDirty(c.ID)
	return carried, deltas, true
}

// refund devuelve a la ciudad lo reservado de un coste y los depósitos que
// viajaban con él (INV-ECON-002).
func (s *Simulation) refund(eco *cityEconomy, costs economy.Costs, carried map[economy.Resource]int64) {
	for _, cost := range costs {
		rs := eco.of(cost.Resource)
		_ = rs.stock.Credit(cost.Amount)
		rs.unflushed += carried[cost.Resource]
	}
}

// handleCraftResolved confirma la elaboración o devuelve la reserva.
func (s *Simulation) handleCraftResolved(cmd CraftResolved) {
	c, ok := s.state.City(cmd.CityID)
	if !ok {
		return
	}
	eco := s.state.economyOf(c.ID)
	cr := eco.craft
	if cr == nil || !cr.pending || cr.RequestID != cmd.RequestID {
		return
	}
	if cmd.Code != "" {
		s.refund(eco, cr.costs, cr.carried)
		s.state.markStockDirty(c.ID)
		eco.craft = nil
		s.economyError(cmd.PlayerID, cmd.RequestID, cmd.Code, "no se pudo encargar la elaboración")
		s.broadcastStocks(c)
		return
	}
	// RN-GOODS-007: un asedio derribó el taller mientras la transacción iba en
	// vuelo; la escritura del derribo, que va detrás en la cola de la ciudad,
	// borra la fila. La elaboración se pierde sin devolver nada.
	if !s.state.hasCompleteWorkshop(c.ID) {
		eco.craft = nil
		s.broadcastStocks(c)
		return
	}
	cr.pending = false
	stocks := s.state.stocksView(c.ID)
	view := s.state.craftView(c.ID)
	s.broadcastCity(c, protocol.CityUpdatePayload{Stocks: &stocks, Craft: &view})
}

// ─────────────────────────────────────────────────────────────
// Fase 5: elaboraciones que vencen
// ─────────────────────────────────────────────────────────────

// completeCrafts entrega, en orden de ciudad, las elaboraciones confirmadas que
// vencieron. Es optimista: la RAM acredita y difunde, y la escritura lleva su
// compensación.
func (s *Simulation) completeCrafts(nowMs int64) {
	s.state.EachCity(func(c *city.City) bool {
		eco, ok := s.state.economies[c.ID]
		if !ok || eco.craft == nil || eco.craft.pending || !eco.craft.Due(nowMs) {
			return true
		}
		cr := eco.craft.Craft
		eco.craft = nil
		rs := eco.of(cr.Good)
		_ = rs.stock.Credit(int64(cr.Amount))
		carried := rs.unflushed
		rs.unflushed = 0
		s.state.markStockDirty(c.ID)

		stocks := s.state.stocksView(c.ID)
		var ninguna *protocol.CraftView
		s.broadcastCity(c, protocol.CityUpdatePayload{Stocks: &stocks, Craft: &ninguna})
		s.submitCraftDelivery(CraftDelivery{
			CityID: c.ID, RequestID: cr.RequestID, PlayerID: c.OwnerPlayerID, Tick: s.state.Tick(),
			Stock: StockDelta{CityID: c.ID, Resource: cr.Good, Delta: int64(cr.Amount) + carried, Version: rs.stock.Version},
		})
		return true
	})
}

func (s *Simulation) submitCraftDelivery(d CraftDelivery) {
	if s.deps.Economy == nil {
		return
	}
	store := s.deps.Economy
	s.deps.Persister.Submit(Job{
		Name: JobCraftComplete,
		Key:  CityKey(d.CityID),
		Run:  func(ctx context.Context) error { return store.CompleteCraft(ctx, d) },
		OnPermanentFailure: func(err error) {
			s.deps.Log.Error("no se pudo escribir la entrega de la elaboración; se reintentará en el siguiente volcado",
				"err", err, "city_id", d.CityID)
			s.reinject(CraftDeliverPersistFailed{Delivery: d})
		},
	})
}

func (s *Simulation) handleCraftDeliverPersistFailed(cmd CraftDeliverPersistFailed) {
	s.state.pendingCraftDeliveries[cmd.Delivery.RequestID] = cmd.Delivery
}

// flushCraftDeliveries reencola las entregas descartadas, con una versión
// nueva del stock: escrituras posteriores de la ciudad pueden haber llevado la
// base más allá de la versión original. Es idempotente: sin fila, la entrega
// no acredita nada.
func (s *Simulation) flushCraftDeliveries() {
	if len(s.state.pendingCraftDeliveries) == 0 {
		return
	}
	ids := make([]string, 0, len(s.state.pendingCraftDeliveries))
	for id := range s.state.pendingCraftDeliveries {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	pending := s.state.pendingCraftDeliveries
	s.state.pendingCraftDeliveries = make(map[string]CraftDelivery)
	for _, id := range ids {
		d := pending[id]
		rs := s.state.economyOf(d.CityID).of(d.Stock.Resource)
		rs.stock.Version++
		d.Stock.Version = rs.stock.Version
		d.Tick = s.state.Tick()
		s.submitCraftDelivery(d)
	}
}

// loseCraft pierde la elaboración confirmada de una ciudad cuyo taller cae
// (RN-GOODS-007): no se devuelve nada. Una en vuelo se resuelve en
// handleCraftResolved.
func (s *Simulation) loseCraft(c *city.City) {
	eco, ok := s.state.economies[c.ID]
	if !ok || eco.craft == nil || eco.craft.pending {
		return
	}
	eco.craft = nil
	var ninguna *protocol.CraftView
	s.broadcastCity(c, protocol.CityUpdatePayload{Craft: &ninguna})
}
