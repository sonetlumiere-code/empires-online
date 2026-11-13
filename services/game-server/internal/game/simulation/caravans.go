package simulation

import (
	"context"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/caravan"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/economy"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/eventlog"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/movement"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/player"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/trade"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/pathfinding"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// Las caravanas en la simulación (SPEC-CARAVANS, ADR-037).
//
// Un trato aceptado no acredita nada: su transacción debita lo que da cada
// parte y crea dos caravanas, una por sentido, que nacen en la RAM al volver la
// confirmación. Cada caravana es una unidad CARAVAN que camina por su polilínea
// como cualquier otra (fase 3). Al llegar al depósito de su destino, la fase 5
// la entrega de forma optimista: la RAM acredita y la quita del mapa, y la
// transacción lo confirma. El golpe que la deja en 0 de vida no la mata: la
// captura (fase 4), y sigue hacia la ciudad del captor.

// Nombres de los trabajos de las caravanas (catálogo JobNames).
const (
	JobCaravanDeliver = "caravan.deliver"
	JobCaravanCapture = "caravan.capture"
)

// caravanRerouteMs es lo que espera una caravana parada fuera de su destino
// —tras un reinicio con una polilínea inválida, por ejemplo— antes de volver a
// buscar ruta.
const caravanRerouteMs = 60_000

// CaravanSpawn es una de las dos caravanas que crea un trato: qué mitad del
// trato lleva, dónde nace y el movimiento que el loop ya le asignó, con su id
// y sin unidad todavía (ADR-037 §3).
type CaravanSpawn struct {
	Half     caravan.Half
	Origin   world.Tile
	MaxHP    int32
	Movement movement.Movement
}

// CaravanCreated son los ids que la base dio a una caravana y a su unidad.
type CaravanCreated struct {
	CaravanID int64
	UnitID    int64
}

// CaravanStore escribe las llegadas y las capturas. Lo implementa
// persistence.CaravanStore. Es idempotente: la cola puede reintentar.
type CaravanStore interface {
	// DeliverCaravan cierra la caravana, borra su unidad y acredita su carga en
	// una transacción; si ya no está en camino, no acredita nada
	// (INV-TRADE-006). Un delta ya superado es ErrStaleStock.
	DeliverCaravan(ctx context.Context, d CaravanDelivery) error
	// CaptureCaravan cambia el dueño de la caravana y le da su movimiento
	// nuevo, o la pierde si Movement es nil (RN-CARAVAN-006).
	CaptureCaravan(ctx context.Context, c CaravanCapture) error
}

// CaravanDelivery es la entrega durable de una caravana.
type CaravanDelivery struct {
	CaravanID int64
	UnitID    int64
	CityID    int64
	// Recipient es el dueño de la ciudad de destino; Counterpart, el dueño de la
	// caravana si es otro.
	Recipient   uuid.UUID
	Counterpart uuid.UUID
	// Stocks son los deltas versionados del almacén del destino: la carga más
	// los depósitos sin volcar que viajan con ella.
	Stocks []StockDelta
	Cargo  economy.Costs
	At     world.Tile
	Tick   uint64
	AtMs   int64
}

// CaravanCapture es la captura durable de una caravana, o su pérdida si
// Movement es nil.
type CaravanCapture struct {
	CaravanID         int64
	UnitID            int64
	PreviousOwner     uuid.UUID
	Captor            uuid.UUID
	CaptorCity        int64
	OriginalRecipient uuid.UUID
	HP                int32
	At                world.Tile
	Movement          *movement.Movement
	Cargo             economy.Costs
	Tick              uint64
	AtMs              int64
}

// CaravanDeliverPersistFailed es la compensación de una entrega descartada: se
// reintenta en el siguiente volcado con una versión nueva del stock.
type CaravanDeliverPersistFailed struct{ Delivery CaravanDelivery }

func (CaravanDeliverPersistFailed) commandType() string { return "caravan.deliver_failed" }

// CaravanCapturePersistFailed es la compensación de una captura descartada: se
// reintenta en el siguiente volcado. La transacción es idempotente.
type CaravanCapturePersistFailed struct{ Capture CaravanCapture }

func (CaravanCapturePersistFailed) commandType() string { return "caravan.capture_failed" }

// caravanEntry es una caravana en camino con lo que la RAM sabe de ella.
type caravanEntry struct {
	caravan.Caravan
	// rerouteAtMs: una caravana parada fuera de su destino no busca otra ruta
	// antes de este instante.
	rerouteAtMs int64
}

// caravanState es lo que la simulación sabe de las caravanas.
type caravanState struct {
	// byUnit son las caravanas en camino, por id de su unidad.
	byUnit map[int64]*caravanEntry
	// pendingDeliveries y pendingCaptures son las escrituras descartadas, por
	// caravana (ADR-022).
	pendingDeliveries map[int64]CaravanDelivery
	pendingCaptures   map[int64][]CaravanCapture
}

func newCaravanState() caravanState {
	return caravanState{
		byUnit:            make(map[int64]*caravanEntry),
		pendingDeliveries: make(map[int64]CaravanDelivery),
		pendingCaptures:   make(map[int64][]CaravanCapture),
	}
}

// SetCaravans instala las caravanas en camino leídas al arrancar (ADR-037 §8).
// Va después de Hydrate: sus unidades ya están en la RAM. Una caravana sin
// unidad viva no se incorpora; la validación de carga la habrá señalado.
func (s *State) SetCaravans(cs []caravan.Caravan) {
	s.caravans = newCaravanState()
	for _, c := range cs {
		if c.Status != caravan.StatusInTransit {
			continue
		}
		if u, ok := s.Unit(c.UnitID); !ok || u.Type != unit.TypeCaravan {
			continue
		}
		copia := c
		copia.Cargo = append(economy.Costs(nil), c.Cargo...)
		s.caravans.byUnit[c.UnitID] = &caravanEntry{Caravan: copia}
	}
}

// Caravan devuelve la caravana en camino que lleva una unidad (tests).
func (s *State) Caravan(unitID int64) (caravan.Caravan, bool) {
	e, ok := s.caravans.byUnit[unitID]
	if !ok {
		return caravan.Caravan{}, false
	}
	return e.Caravan, true
}

// CaravanCount es cuántas caravanas hay en camino (tests).
func (s *State) CaravanCount() int { return len(s.caravans.byUnit) }

// caravanIDs son las unidades de las caravanas en camino, en orden.
func (s *State) caravanIDs() []int64 {
	ids := make([]int64, 0, len(s.caravans.byUnit))
	for id := range s.caravans.byUnit {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// ownerOfCity es el dueño de una ciudad, o uuid.Nil.
func (s *State) ownerOfCity(id int64) uuid.UUID {
	if c, ok := s.City(id); ok {
		return c.OwnerPlayerID
	}
	return uuid.Nil
}

// ─────────────────────────────────────────────────────────────
// La ruta de un trato (RN-CARAVAN-002)
// ─────────────────────────────────────────────────────────────

// tradeRoute busca la ruta entre los depósitos de las dos ciudades de un trato,
// del de quien propone al de quien recibe la oferta. La de vuelta es la misma
// al revés (ADR-037 §3).
func (s *Simulation) tradeRoute(from, to *city.City) ([]world.Tile, error) {
	a, ok := s.depositTile(from)
	if !ok {
		return nil, pathfinding.ErrPathNotFound
	}
	b, ok := s.depositTile(to)
	if !ok {
		return nil, pathfinding.ErrPathNotFound
	}
	return s.findPath(a, b)
}

// findPath es el A* de siempre, con sus topes y su métrica.
func (s *Simulation) findPath(from, to world.Tile) ([]world.Tile, error) {
	start := time.Now() //lint:reloj-real mide la duración del A* para la métrica; no afecta al resultado
	tiles, err := s.deps.Pathfinder.FindPath(context.Background(), s.state.World(), from, to, pathfinding.Options{
		MaxNodes:    s.deps.PathMaxNodes,
		MaxDistance: s.deps.PathMaxDistance,
	})
	s.LastPathfindingCalls++
	s.LastPathfindingTime += time.Since(start) //lint:reloj-real ídem
	return tiles, err
}

// caravanPath es la polilínea de una caravana de un jugador por unos tiles, a
// su velocidad y con el rasgo de su civilización (Q4, RN-CIV-001).
func (s *Simulation) caravanPath(owner uuid.UUID, tiles []world.Tile) (movement.TimedPath, error) {
	def, err := unit.Lookup(unit.TypeCaravan)
	if err != nil {
		return nil, err
	}
	ms := player.ScaleDuration(def.BaseMsPerTile, s.state.trait(owner, player.TraitMoveSpeed))
	return movement.BuildTimedPath(tiles, s.state.World(), ms)
}

// caravanSpawns prepara las dos caravanas de un trato aceptado: la de quien
// propuso por la ruta y la de quien acepta por la misma al revés, con sus
// movimientos ya numerados por el loop (RN-CARAVAN-001).
func (s *Simulation) caravanSpawns(o trade.Offer, route []world.Tile, nowMs int64) ([]CaravanSpawn, error) {
	def, err := unit.Lookup(unit.TypeCaravan)
	if err != nil {
		return nil, err
	}
	halves := caravan.Halves(o)
	routes := [2][]world.Tile{route, caravan.Reverse(route)}
	out := make([]CaravanSpawn, 0, 2)
	for i, h := range halves {
		path, err := s.caravanPath(h.Sender, routes[i])
		if err != nil {
			return nil, err
		}
		m := movement.New(0, path, path.Destination(), nowMs)
		m.ID = s.state.NewMovementID()
		out = append(out, CaravanSpawn{Half: h, Origin: path.Origin(), MaxHP: def.MaxHP, Movement: *m})
	}
	return out, nil
}

// spawnCaravans pone en el mapa las caravanas de un trato que la base acaba de
// confirmar, con los ids que les dio. Su movimiento empezó en el tick de la
// aceptación: aparecen ya avanzadas lo que diga su polilínea.
func (s *Simulation) spawnCaravans(spawns []CaravanSpawn, created []CaravanCreated, offerID int64) {
	nowMs := s.deps.Clock.NowMs()
	for i, sp := range spawns {
		if i >= len(created) || created[i].UnitID == 0 {
			continue
		}
		if _, ya := s.state.Unit(created[i].UnitID); ya {
			continue // un reintento cuya primera respuesta ya llegó
		}
		from := sp.Half.FromCity
		u := &unit.Unit{
			ID: created[i].UnitID, PlayerID: sp.Half.Sender, CityID: &from, Type: unit.TypeCaravan,
			X: sp.Origin.X, Y: sp.Origin.Y, HP: sp.MaxHP, MaxHP: sp.MaxHP, Status: unit.StatusMoving,
		}
		s.state.AddUnit(u)
		m := sp.Movement
		m.UnitID = u.ID
		s.state.SetMovement(&m)
		pos := m.PositionAt(nowMs)
		s.state.MoveUnitTo(u, pos)

		e := &caravanEntry{Caravan: caravan.Caravan{
			ID: created[i].CaravanID, UnitID: u.ID, OfferID: offerID, Sender: sp.Half.Sender,
			FromCity: sp.Half.FromCity, ToCity: sp.Half.ToCity, OriginalToCity: sp.Half.ToCity,
			Cargo: append(economy.Costs(nil), sp.Half.Cargo...), Status: caravan.StatusInTransit,
			DepartedAt: time.UnixMilli(m.StartTimeMs).UTC(),
		}}
		s.state.caravans.byUnit[u.ID] = e

		cx, cy := s.state.World().ChunkOf(pos.X, pos.Y)
		s.deps.Broadcaster.BroadcastChunk(cx, cy, protocol.TypeEntitySpawn,
			protocol.EntitySpawnPayload{Unit: s.UnitView(u, nowMs)})
		s.broadcastCaravan(e, u, uuid.Nil)
	}
}

// ─────────────────────────────────────────────────────────────
// Fase 5: llegadas (RN-CARAVAN-004)
// ─────────────────────────────────────────────────────────────

// deliverCaravans entrega, en orden de unidad, las caravanas paradas en el
// depósito de su destino (RN-CARAVAN-007). Va en la fase 5, después del
// combate: una caravana que llega y recibe el golpe mortal en el mismo tick ya
// se capturó y va hacia otra parte. Una caravana parada fuera de su destino
// —su movimiento no se pudo recuperar— busca otra ruta.
func (s *Simulation) deliverCaravans(nowMs int64) {
	for _, id := range s.state.caravanIDs() {
		e := s.state.caravans.byUnit[id]
		u, ok := s.state.Unit(id)
		if !ok {
			delete(s.state.caravans.byUnit, id)
			continue
		}
		if _, moving := s.state.Movement(id); moving {
			continue
		}
		c, ok := s.state.City(e.ToCity)
		if !ok {
			continue
		}
		depot, ok := s.depositTile(c)
		if !ok {
			continue
		}
		if u.Tile() == depot {
			s.deliverCaravan(e, u, c, nowMs)
			continue
		}
		if nowMs >= e.rerouteAtMs {
			e.rerouteAtMs = nowMs + caravanRerouteMs
			s.routeCaravan(u, depot, nowMs)
		}
	}
}

// routeCaravan pone a una caravana parada en camino hacia su destino.
func (s *Simulation) routeCaravan(u *unit.Unit, depot world.Tile, nowMs int64) {
	tiles, err := s.findPath(u.Tile(), depot)
	if err != nil {
		s.deps.Log.Warn("una caravana parada no encuentra ruta a su destino; se reintentará",
			"unit_id", u.ID, "err", err)
		return
	}
	path, err := s.caravanPath(u.PlayerID, tiles)
	if err != nil {
		return
	}
	s.beginMovement(u, u.Tile(), depot, path, nowMs, nil)
}

// deliverCaravan acredita la carga en el destino, quita la caravana del mapa y
// encola su entrega durable.
func (s *Simulation) deliverCaravan(e *caravanEntry, u *unit.Unit, c *city.City, nowMs int64) {
	eco := s.state.economyOf(c.ID)
	deltas := make([]StockDelta, 0, len(e.Cargo))
	for _, cost := range e.Cargo {
		rs := eco.of(cost.Resource)
		_ = rs.stock.Credit(cost.Amount)
		carried := rs.unflushed
		rs.unflushed = 0
		deltas = append(deltas, StockDelta{
			CityID: c.ID, Resource: cost.Resource, Delta: cost.Amount + carried, Version: rs.stock.Version,
		})
	}
	s.state.markStockDirty(c.ID)
	s.broadcastStocks(c)

	at := u.Tile()
	recipient := c.OwnerPlayerID
	var other uuid.UUID
	if u.PlayerID != recipient {
		other = u.PlayerID
	}
	d := CaravanDelivery{
		CaravanID: e.ID, UnitID: u.ID, CityID: c.ID, Recipient: recipient, Counterpart: other,
		Stocks: deltas, Cargo: append(economy.Costs(nil), e.Cargo...), At: at, Tick: s.state.Tick(), AtMs: nowMs,
	}

	e.Status = caravan.StatusDelivered
	s.broadcastCaravan(e, u, uuid.Nil)
	s.removeCaravanUnit(u, nowMs)
	delete(s.state.caravans.byUnit, u.ID)
	s.submitCaravanDelivery(d)
}

// removeCaravanUnit quita del mapa la unidad de una caravana que llega o se
// pierde. Quien la atacaba se queda sin objetivo.
func (s *Simulation) removeCaravanUnit(u *unit.Unit, nowMs int64) {
	at := s.authoritativePosition(u, nowMs)
	s.state.ClearMovement(u.ID)
	cx, cy := s.state.World().ChunkOf(at.X, at.Y)
	s.deps.Broadcaster.BroadcastChunk(cx, cy, protocol.TypeEntityDespawn,
		protocol.EntityDespawnPayload{ID: u.ID, Reason: protocol.DespawnRemoved})
	s.state.RemoveUnit(u.ID)
	for _, id := range s.attackersOf(u.ID) {
		if a, ok := s.state.Unit(id); ok {
			s.endEngagement(a, true)
		}
	}
}

func (s *Simulation) submitCaravanDelivery(d CaravanDelivery) {
	if s.deps.Caravans == nil {
		return
	}
	store := s.deps.Caravans
	s.deps.Persister.Submit(Job{
		Name: JobCaravanDeliver,
		Key:  UnitKey(d.UnitID),
		Key2: CityKey(d.CityID),
		Run:  func(ctx context.Context) error { return store.DeliverCaravan(ctx, d) },
		OnPermanentFailure: func(err error) {
			s.deps.Log.Error("no se pudo escribir la llegada de una caravana; se reintentará en el siguiente volcado",
				"err", err, "caravan_id", d.CaravanID)
			s.reinject(CaravanDeliverPersistFailed{Delivery: d})
		},
	})
}

func (s *Simulation) handleCaravanDeliverPersistFailed(cmd CaravanDeliverPersistFailed) {
	s.state.caravans.pendingDeliveries[cmd.Delivery.CaravanID] = cmd.Delivery
}

// flushCaravanWrites reencola las entregas y las capturas descartadas. Las
// entregas, con una versión nueva de cada stock: escrituras posteriores de la
// ciudad pueden haber llevado la base más allá de la original. Las dos son
// idempotentes.
func (s *Simulation) flushCaravanWrites() {
	if len(s.state.caravans.pendingCaptures) > 0 {
		ids := make([]int64, 0, len(s.state.caravans.pendingCaptures))
		for id := range s.state.caravans.pendingCaptures {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		pending := s.state.caravans.pendingCaptures
		s.state.caravans.pendingCaptures = make(map[int64][]CaravanCapture)
		for _, id := range ids {
			for _, c := range pending[id] {
				s.submitCaravanCapture(c)
			}
		}
	}
	if len(s.state.caravans.pendingDeliveries) == 0 {
		return
	}
	ids := make([]int64, 0, len(s.state.caravans.pendingDeliveries))
	for id := range s.state.caravans.pendingDeliveries {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	pending := s.state.caravans.pendingDeliveries
	s.state.caravans.pendingDeliveries = make(map[int64]CaravanDelivery)
	for _, id := range ids {
		d := pending[id]
		eco := s.state.economyOf(d.CityID)
		for i := range d.Stocks {
			rs := eco.of(d.Stocks[i].Resource)
			rs.stock.Version++
			d.Stocks[i].Version = rs.stock.Version
		}
		d.Tick = s.state.Tick()
		s.submitCaravanDelivery(d)
	}
}

// ─────────────────────────────────────────────────────────────
// Fase 4: la captura (RN-CARAVAN-006)
// ─────────────────────────────────────────────────────────────

// captureCaravan aplica el golpe que deja una caravana en 0: pasa al dueño del
// guerrero que lo dio, con la vida entera y la carga intacta, y sigue hacia la
// ciudad del captor; sin ruta, se pierde con su carga. Nunca queda DEAD.
func (s *Simulation) captureCaravan(v, killer *unit.Unit, nowMs int64) {
	e, ok := s.state.caravans.byUnit[v.ID]
	if !ok || killer == nil {
		// Una unidad CARAVAN sin caravana no debería existir: se trata como
		// cualquier otra muerte.
		s.kill(v, killer, nowMs)
		return
	}
	at := s.authoritativePosition(v, nowMs)
	previous, captor := v.PlayerID, killer.PlayerID
	recipient := s.state.ownerOfCity(e.OriginalToCity)

	// Su movimiento termina aquí; la transacción de la captura cierra la fila
	// al escribir el nuevo (MovementRepo.Start).
	s.state.ClearMovement(v.ID)
	s.state.MoveUnitTo(v, at)
	// Los guerreros del captor dejan de atacarla: ya es suya.
	for _, id := range s.attackersOf(v.ID) {
		if a, ok := s.state.Unit(id); ok && a.PlayerID == captor {
			s.endEngagement(a, true)
		}
	}

	capture := CaravanCapture{
		CaravanID: e.ID, UnitID: v.ID, PreviousOwner: previous, Captor: captor,
		OriginalRecipient: recipient, At: at, Cargo: append(economy.Costs(nil), e.Cargo...),
		Tick: s.state.Tick(), AtMs: nowMs,
	}
	notice := eventlog.Event{
		Type: eventlog.EventCaravanCaptured, PlayerID: previous, Counterpart: captor, AtMs: nowMs,
		X: at.X, Y: at.Y, HasTile: true, Cargo: cargoLines(e.Cargo),
	}

	var path movement.TimedPath
	var dest world.Tile
	c, hasCity := s.state.CityOf(captor)
	routed := false
	if hasCity {
		if depot, ok := s.depositTile(c); ok {
			if tiles, err := s.findPath(at, depot); err == nil {
				if p, err := s.caravanPath(captor, tiles); err == nil {
					path, dest, routed = p, depot, true
				}
			}
		}
	}

	if !routed {
		// Se pierde: la carga desaparece con ella (INV-TRADE-001, precisión).
		notice.Type = eventlog.EventCaravanLost
		e.Status = caravan.StatusLost
		if hasCity {
			capture.CaptorCity = c.ID
		}
		s.broadcastCaravan(e, v, previous, captor)
		s.removeCaravanUnit(v, nowMs)
		delete(s.state.caravans.byUnit, v.ID)
	} else {
		cityID := c.ID
		v.PlayerID, v.CityID = captor, &cityID
		v.HP = v.MaxHP
		v.Status = unit.StatusMoving
		m := movement.New(v.ID, path, dest, nowMs)
		m.ID = s.state.NewMovementID()
		s.state.SetMovement(m)
		e.ToCity = cityID
		e.rerouteAtMs = 0
		capture.CaptorCity, capture.HP, capture.Movement = cityID, v.HP, m

		// El dueño cambió: los demás la reciben de nuevo, entera.
		cx, cy := s.state.World().ChunkOf(at.X, at.Y)
		s.deps.Broadcaster.BroadcastChunk(cx, cy, protocol.TypeEntityDespawn,
			protocol.EntityDespawnPayload{ID: v.ID, Reason: protocol.DespawnRemoved})
		s.deps.Broadcaster.BroadcastChunk(cx, cy, protocol.TypeEntitySpawn,
			protocol.EntitySpawnPayload{Unit: s.UnitView(v, nowMs)})
		s.broadcastCaravan(e, v, previous)
	}

	s.deps.Log.Info("caravana capturada", "unit_id", v.ID, "caravan_id", e.ID,
		"captor", captor, "lost", !routed)
	s.sendNotices(notice)
	if recipient != uuid.Nil && recipient != previous && recipient != captor {
		intercepted := notice
		intercepted.Type, intercepted.PlayerID = eventlog.EventCaravanIntercepted, recipient
		s.sendNotices(intercepted)
	}
	s.submitCaravanCapture(capture)
}

func cargoLines(cs economy.Costs) []eventlog.Line {
	out := make([]eventlog.Line, len(cs))
	for i, c := range cs {
		out[i] = eventlog.Line{Resource: string(c.Resource), Amount: c.Amount}
	}
	return out
}

func (s *Simulation) submitCaravanCapture(c CaravanCapture) {
	if s.deps.Caravans == nil {
		return
	}
	store := s.deps.Caravans
	s.deps.Persister.Submit(Job{
		Name: JobCaravanCapture,
		Key:  UnitKey(c.UnitID),
		Run:  func(ctx context.Context) error { return store.CaptureCaravan(ctx, c) },
		OnPermanentFailure: func(err error) {
			s.deps.Log.Error("no se pudo escribir la captura de una caravana; se reintentará en el siguiente volcado",
				"err", err, "caravan_id", c.CaravanID)
			s.reinject(CaravanCapturePersistFailed{Capture: c})
		},
	})
}

func (s *Simulation) handleCaravanCapturePersistFailed(cmd CaravanCapturePersistFailed) {
	id := cmd.Capture.CaravanID
	s.state.caravans.pendingCaptures[id] = append(s.state.caravans.pendingCaptures[id], cmd.Capture)
}

// ─────────────────────────────────────────────────────────────
// Vistas (RN-CARAVAN-009)
// ─────────────────────────────────────────────────────────────

// caravanView es una caravana vista por uno de sus implicados, con su carga.
func (s *Simulation) caravanView(e *caravanEntry, u *unit.Unit) protocol.CaravanView {
	owner := e.Sender
	if u != nil {
		owner = u.PlayerID
	}
	v := protocol.CaravanView{
		ID: e.ID, UnitID: e.UnitID, OwnerPlayerID: owner.String(), SenderPlayerID: e.Sender.String(),
		FromCityID: e.FromCity, ToCityID: e.ToCity, OriginalToCityID: e.OriginalToCity,
		Cargo: resourceAmounts(e.Cargo), Status: string(e.Status),
	}
	if c, ok := s.state.City(e.FromCity); ok && c.Name != "" {
		name := c.Name
		v.FromCityName = &name
	}
	if c, ok := s.state.City(e.ToCity); ok && c.Name != "" {
		name := c.Name
		v.ToCityName = &name
	}
	if e.Status == caravan.StatusInTransit {
		if m, ok := s.state.Movement(e.UnitID); ok {
			at := m.ArrivalTimeMs
			v.ArrivesAtMs = &at
		}
	}
	return v
}

// caravanParties son los jugadores que ven una caravana con su carga: su dueño,
// quien la mandó, el destinatario original y el dueño de su destino de ahora,
// más los que se pasen (el que la acaba de perder), sin repetir y en un orden
// estable.
func (s *Simulation) caravanParties(e *caravanEntry, u *unit.Unit, extra ...uuid.UUID) []uuid.UUID {
	candidates := []uuid.UUID{e.Sender, s.state.ownerOfCity(e.OriginalToCity), s.state.ownerOfCity(e.ToCity)}
	if u != nil {
		candidates = append([]uuid.UUID{u.PlayerID}, candidates...)
	}
	candidates = append(candidates, extra...)
	seen := make(map[uuid.UUID]bool, len(candidates))
	out := make([]uuid.UUID, 0, len(candidates))
	for _, p := range candidates {
		if p == uuid.Nil || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

// broadcastCaravan lleva una caravana a sus implicados y a nadie más.
func (s *Simulation) broadcastCaravan(e *caravanEntry, u *unit.Unit, also uuid.UUID, more ...uuid.UUID) {
	extra := append([]uuid.UUID{also}, more...)
	view := s.caravanView(e, u)
	for _, p := range s.caravanParties(e, u, extra...) {
		s.deps.Broadcaster.SendToPlayer(p, protocol.TypeCaravanUpdate, "",
			protocol.CaravanUpdatePayload{Caravan: view})
	}
}

// caravansOf son las caravanas en camino que un jugador ve con su carga, por
// unidad, para su snapshot.
func (s *Simulation) caravansOf(viewer uuid.UUID) []protocol.CaravanView {
	var out []protocol.CaravanView
	for _, id := range s.state.caravanIDs() {
		e := s.state.caravans.byUnit[id]
		u, _ := s.state.Unit(id)
		for _, p := range s.caravanParties(e, u) {
			if p == viewer {
				out = append(out, s.caravanView(e, u))
				break
			}
		}
	}
	return out
}
