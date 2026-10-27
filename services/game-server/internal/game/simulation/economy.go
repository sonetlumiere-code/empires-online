package simulation

import (
	"context"
	"errors"
	"sort"

	"github.com/google/uuid"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/building"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/economy"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/movement"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/production"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/task"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// Economía en la simulación: el stock de madera de cada ciudad, los comandos
// city.build y city.train, la finalización de casas y entrenamientos, y el
// volcado de los depósitos. La tarea de recolección está en gather.go.
//
// Spec: ../../../../docs/specs/economy-wood.md · ADR-023.

// ErrStaleStock: la escritura de un encargo encontró en la base un stock con
// una versión igual o posterior a la suya. Con la cola ordenada por ciudad no
// debería ocurrir; si ocurre, el encargo se rechaza en lugar de insertar el
// efecto sin su débito (INV-ECON-002).
var ErrStaleStock = errors.New("el stock de la base va por delante del encargo")

// birthRetryMs es cuánto espera un nacimiento cuya transacción falló antes de
// volver a intentarse: el ritmo del volcado, no el del tick.
const birthRetryMs int64 = 5_000

// ─────────────────────────────────────────────────────────────
// Escrituras durables
// ─────────────────────────────────────────────────────────────

// EconomyStore escribe la economía de forma transaccional. Lo implementa
// postgres.EconomyRepo. Cada método es idempotente: la cola puede reintentar.
type EconomyStore interface {
	// PersistTask escribe una transición de tarea con su tramo de movimiento y
	// el estado de la unidad, en una transacción (ADR-023 §6).
	PersistTask(ctx context.Context, w TaskWrite) error
	// FlushStocks suma los depósitos pendientes (ADR-023 §1).
	FlushStocks(ctx context.Context, deltas []StockDelta) error
	// Build debita y encarga el edificio en una transacción (RN-ECON-008).
	// Devuelve su id. Un rechazo de dominio es building.ErrInvalidSite,
	// economy.ErrInsufficientResources o ErrStaleStock.
	Build(ctx context.Context, o BuildOrder) (int64, error)
	// Train debita, reserva la población y encarga el entrenamiento en una
	// transacción (RN-ECON-010). Un rechazo de dominio es production.ErrCityBusy,
	// city.ErrPopulationLimit, economy.ErrInsufficientResources o ErrStaleStock.
	Train(ctx context.Context, o TrainOrder) error
	// CompleteBuilding marca el edificio COMPLETE y suma su bono al límite de
	// población, sólo si seguía en obras (RN-ECON-009).
	CompleteBuilding(ctx context.Context, c BuildingCompletion) error
	// BirthUnit inserta la unidad entrenada y la apunta en su producción.
	// Devuelve el id que le dio la base (ADR-023 §3).
	BirthUnit(ctx context.Context, b Birth) (int64, error)
}

// StockDelta es una escritura del stock: sumar Delta (negativo en un encargo)
// si la versión de la base es anterior a Version (ADR-023 §1).
type StockDelta struct {
	CityID   int64
	Resource economy.Resource
	Delta    int64
	Version  int64
}

// TaskWrite es una transición de tarea: el tramo que se cierra, el que se
// abre, la unidad tal como queda y la tarea (nil: se borra).
type TaskWrite struct {
	UnitID int64
	// Finish cierra el tramo que termina; sólo su fila de unit_movements.
	Finish *MovementFinish
	// Start es el tramo que empieza.
	Start *movement.Movement
	// Status y At son el estado y el tile de la unidad tras la transición.
	Status unit.Status
	At     world.Tile
	// KeepUnit: no se escribe la unidad, sólo la tarea. Lo usa el arranque
	// para borrar la tarea de una unidad que sigue con otro movimiento.
	KeepUnit bool
	Task     *task.Task
}

// BuildOrder es un encargo de edificio con su débito.
type BuildOrder struct {
	RequestID string
	PlayerID  uuid.UUID
	Building  building.Building
	Stock     StockDelta
	Tick      uint64
}

// TrainOrder es un encargo de entrenamiento con su débito y su reserva.
type TrainOrder struct {
	RequestID      string
	PlayerID       uuid.UUID
	Production     production.Production
	PopulationCost int32
	Stock          StockDelta
	Tick           uint64
}

// BuildingCompletion es la finalización durable de un edificio.
type BuildingCompletion struct {
	BuildingID int64
	CityID     int64
	PlayerID   uuid.UUID
	Bonus      int32
	Tick       uint64
}

// Birth es el nacimiento durable de la unidad de una producción.
type Birth struct {
	CityID   int64
	PlayerID uuid.UUID
	Type     unit.Type
	At       world.Tile
	HP       int32
	Tick     uint64
}

// ─────────────────────────────────────────────────────────────
// Comandos
// ─────────────────────────────────────────────────────────────

// BuildBuilding pide encargar un edificio.
type BuildBuilding struct {
	PlayerID  uuid.UUID
	SessionID uuid.UUID
	RequestID string
	CityID    int64
	Type      building.Type
	Tile      world.Tile
}

func (BuildBuilding) commandType() string { return protocol.TypeCityBuild }

// TrainUnit pide entrenar una unidad.
type TrainUnit struct {
	PlayerID  uuid.UUID
	SessionID uuid.UUID
	RequestID string
	CityID    int64
	Type      unit.Type
}

func (TrainUnit) commandType() string { return protocol.TypeCityTrain }

// BuildResolved lo devuelve la cola con el resultado de la transacción de un
// encargo de edificio. Code vacío: el edificio está escrito con su débito.
type BuildResolved struct {
	PlayerID   uuid.UUID
	RequestID  string
	CityID     int64
	Tile       world.Tile
	BuildingID int64
	Code       string
}

func (BuildResolved) commandType() string { return "city.build_resolved" }

// TrainResolved lo devuelve la cola con el resultado de un encargo de
// entrenamiento.
type TrainResolved struct {
	PlayerID  uuid.UUID
	RequestID string
	CityID    int64
	Code      string
}

func (TrainResolved) commandType() string { return "city.train_resolved" }

// BirthResolved lo devuelve la cola con el resultado de un nacimiento.
type BirthResolved struct {
	CityID int64
	UnitID int64
	At     world.Tile
	Code   string
}

func (BirthResolved) commandType() string { return "production.birth_resolved" }

// StocksFlushFailed es la compensación de un volcado de depósitos que no se
// escribió: el delta vuelve a la ciudad y sale en el volcado siguiente.
type StocksFlushFailed struct {
	CityID int64
	Delta  int64
}

func (StocksFlushFailed) commandType() string { return "stocks.flush_failed" }

// BuildingCompletePersistFailed es la compensación de una finalización que no
// se escribió: el volcado siguiente la reencola (ADR-022).
type BuildingCompletePersistFailed struct {
	BuildingID int64
}

func (BuildingCompletePersistFailed) commandType() string { return "building.complete_failed" }

// ─────────────────────────────────────────────────────────────
// Estado
// ─────────────────────────────────────────────────────────────

// cityEconomy es la economía de una ciudad en la RAM.
type cityEconomy struct {
	wood economy.Stock
	// unflushed son los depósitos todavía no escritos: viajan con el volcado
	// o con el siguiente encargo de la ciudad (ADR-023 §1).
	unflushed int64
	// production es el entrenamiento de la ciudad; nil si no tiene.
	production *productionState
}

// productionState es una producción con su situación en el ciclo de dos fases.
type productionState struct {
	production.Production
	cost      economy.Cost
	popCost   int32
	carried   int64
	pending   bool // encargada, esperando su transacción
	birthing  bool // su nacimiento espera la transacción
	retryAtMs int64
}

// pendingBuild es un edificio encargado cuya transacción no ha vuelto.
type pendingBuild struct {
	playerID  uuid.UUID
	requestID string
	building  building.Building
	cost      economy.Cost
	carried   int64
}

// EconomyLoad es la economía leída de la base al arrancar.
type EconomyLoad struct {
	Stocks      []StockRow
	Buildings   []building.Building
	Productions []production.Production
	Tasks       []task.Task
}

// StockRow es una fila de city_stocks.
type StockRow struct {
	CityID int64
	Stock  economy.Stock
}

// SetEconomy instala la economía leída al arrancar. Va antes de Hydrate: la
// rehidratación de movimientos pregunta qué unidades tienen una tarea.
func (s *State) SetEconomy(load EconomyLoad) {
	for _, row := range load.Stocks {
		if row.Stock.Resource != economy.ResourceWood {
			continue
		}
		s.economyOf(row.CityID).wood = row.Stock
	}
	for _, b := range load.Buildings {
		s.addBuilding(b)
	}
	for _, p := range load.Productions {
		if !p.InProgress() {
			// La última producción terminada de la ciudad: su fila sólo espera
			// al siguiente encargo, que la sustituye (ADR-023 §3).
			continue
		}
		cost := economy.Cost{}
		popCost := int32(1)
		if def, err := unit.Lookup(p.UnitType); err == nil {
			cost, popCost = def.TrainCost, def.PopulationCost
		}
		s.economyOf(p.CityID).production = &productionState{Production: p, cost: cost, popCost: popCost}
	}
	for _, t := range load.Tasks {
		s.setTask(t)
	}
}

// economyOf devuelve la economía de una ciudad, creándola a cero si aún no
// existe (una ciudad recién fundada, o sin fila de stock).
func (s *State) economyOf(cityID int64) *cityEconomy {
	eco, ok := s.economies[cityID]
	if !ok {
		eco = &cityEconomy{wood: economy.NewStock(economy.ResourceWood)}
		s.economies[cityID] = eco
	}
	return eco
}

// Stock devuelve el stock de madera de una ciudad (lectura y tests).
func (s *State) Stock(cityID int64) economy.Stock {
	if eco, ok := s.economies[cityID]; ok {
		return eco.wood
	}
	return economy.NewStock(economy.ResourceWood)
}

// UnflushedDeposits es la madera depositada que aún no se ha escrito.
func (s *State) UnflushedDeposits(cityID int64) int64 {
	if eco, ok := s.economies[cityID]; ok {
		return eco.unflushed
	}
	return 0
}

// Production devuelve el entrenamiento en curso de una ciudad y si está
// confirmado por la base.
func (s *State) Production(cityID int64) (production.Production, bool, bool) {
	eco, ok := s.economies[cityID]
	if !ok || eco.production == nil {
		return production.Production{}, false, false
	}
	return eco.production.Production, !eco.production.pending, true
}

func (s *State) addBuilding(b building.Building) {
	cp := b
	s.buildings[b.ID] = &cp
	s.buildingAt[b.Tile()] = b.ID
	s.buildingIDs = insertSorted(s.buildingIDs, b.ID)
}

// Building recupera un edificio confirmado.
func (s *State) Building(id int64) (building.Building, bool) {
	b, ok := s.buildings[id]
	if !ok {
		return building.Building{}, false
	}
	return *b, true
}

// BuildingAt devuelve el edificio confirmado de un tile.
func (s *State) BuildingAt(t world.Tile) (building.Building, bool) {
	id, ok := s.buildingAt[t]
	if !ok {
		return building.Building{}, false
	}
	return s.Building(id)
}

// BuildingsOf devuelve los edificios confirmados de una ciudad, por id.
func (s *State) BuildingsOf(cityID int64) []building.Building {
	var out []building.Building
	for _, id := range s.buildingIDs {
		if b := s.buildings[id]; b.CityID == cityID {
			out = append(out, *b)
		}
	}
	return out
}

// buildingOccupied dice si un tile tiene edificio, confirmado o encargado
// (RN-ECON-007).
func (s *State) buildingOccupied(t world.Tile) bool {
	if _, ok := s.buildingAt[t]; ok {
		return true
	}
	_, ok := s.pendingBuilds[t]
	return ok
}

// buildingsInChunk devuelve, por id, los edificios confirmados de un chunk.
func (s *State) buildingsInChunk(cx, cy int32) []building.Building {
	var out []building.Building
	for _, id := range s.buildingIDs {
		b := s.buildings[id]
		if bcx, bcy := s.world.ChunkOf(b.X, b.Y); bcx == cx && bcy == cy {
			out = append(out, *b)
		}
	}
	return out
}

// ─────────────────────────────────────────────────────────────
// Vistas
// ─────────────────────────────────────────────────────────────

// BuildingView proyecta un edificio al contrato de red.
func BuildingView(b building.Building) protocol.BuildingView {
	return protocol.BuildingView{
		ID: b.ID, CityID: b.CityID, Type: string(b.Type), X: b.X, Y: b.Y,
		Status: string(b.Status), CompletesAtMs: b.CompletesAtMs,
	}
}

// stocksView es el stock de una ciudad tal como viaja.
func (s *State) stocksView(cityID int64) protocol.Stocks {
	return protocol.Stocks{Wood: s.Stock(cityID).Amount}
}

// productionView es el entrenamiento CONFIRMADO de una ciudad, o nil: lo que
// espera su transacción todavía no existe para nadie.
func (s *State) productionView(cityID int64) *protocol.ProductionView {
	p, confirmed, ok := s.Production(cityID)
	if !ok || !confirmed {
		return nil
	}
	return &protocol.ProductionView{UnitType: string(p.UnitType), CompletesAtMs: p.CompletesAtMs}
}

// cityViewOf es CityView con la economía de la ciudad.
func (s *State) cityViewOf(c *city.City) protocol.CityView {
	v := CityView(c)
	v.Stocks = s.stocksView(c.ID)
	v.Production = s.productionView(c.ID)
	return v
}

// broadcastCity difunde un city.update de la ciudad a su chunk.
func (s *Simulation) broadcastCity(c *city.City, payload protocol.CityUpdatePayload) {
	payload.ID = c.ID
	cx, cy := s.state.World().ChunkOf(c.CenterX, c.CenterY)
	s.deps.Broadcaster.BroadcastChunk(cx, cy, protocol.TypeCityUpdate, payload)
}

// broadcastStocks difunde el stock actual de una ciudad.
func (s *Simulation) broadcastStocks(c *city.City) {
	stocks := s.state.stocksView(c.ID)
	s.broadcastCity(c, protocol.CityUpdatePayload{Stocks: &stocks})
}

// ─────────────────────────────────────────────────────────────
// Depósitos y volcado
// ─────────────────────────────────────────────────────────────

// deposit suma madera a una ciudad: RAM primero, base por volcado (§7).
func (s *Simulation) deposit(c *city.City, amount int32) {
	if amount <= 0 {
		return
	}
	eco := s.state.economyOf(c.ID)
	_ = eco.wood.Credit(int64(amount))
	eco.unflushed += int64(amount)
	s.state.stockDirty[c.ID] = struct{}{}
	s.broadcastStocks(c)
}

// flushStocks encola, ciudad a ciudad y en orden, los depósitos que no se han
// escrito. Si uno no se escribe, su delta vuelve a la ciudad (ADR-022).
func (s *Simulation) flushStocks() {
	if len(s.state.stockDirty) == 0 {
		return
	}
	ids := make([]int64, 0, len(s.state.stockDirty))
	for id := range s.state.stockDirty {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	s.state.stockDirty = make(map[int64]struct{})
	if s.deps.Economy == nil {
		return
	}

	store := s.deps.Economy
	for _, id := range ids {
		eco, ok := s.state.economies[id]
		if !ok || eco.unflushed == 0 {
			continue
		}
		d := StockDelta{CityID: id, Resource: economy.ResourceWood, Delta: eco.unflushed, Version: eco.wood.Version}
		eco.unflushed = 0
		s.deps.Persister.Submit(Job{
			Name: JobStocksFlush,
			Key:  CityKey(id),
			Run:  func(ctx context.Context) error { return store.FlushStocks(ctx, []StockDelta{d}) },
			OnPermanentFailure: func(err error) {
				s.deps.Log.Error("no se pudieron volcar los depósitos; vuelven a la ciudad",
					"err", err, "city_id", d.CityID, "delta", d.Delta)
				s.reinject(StocksFlushFailed{CityID: d.CityID, Delta: d.Delta})
			},
		})
	}
}

// handleStocksFlushFailed devuelve a la ciudad el delta que no se escribió. La
// versión sube: el volcado siguiente tiene que superar a cualquier encargo que
// se haya escrito entre medias, o se perdería (ADR-023, Negativas).
func (s *Simulation) handleStocksFlushFailed(cmd StocksFlushFailed) {
	if _, ok := s.state.City(cmd.CityID); !ok {
		return
	}
	eco := s.state.economyOf(cmd.CityID)
	eco.unflushed += cmd.Delta
	eco.wood.Version++
	s.state.stockDirty[cmd.CityID] = struct{}{}
}

// ─────────────────────────────────────────────────────────────
// city.build
// ─────────────────────────────────────────────────────────────

// handleBuildBuilding valida en el tick y reserva; la transacción de la cola
// debita y escribe el edificio (ADR-023 §2).
func (s *Simulation) handleBuildBuilding(cmd BuildBuilding) {
	c, _ := s.state.City(cmd.CityID)
	var stock *economy.Stock
	if c != nil {
		stock = &s.state.economyOf(c.ID).wood
	}
	// RN-ECON-007, en el orden de la spec. La propiedad primero: una ciudad
	// ajena responde CITY_NOT_FOUND, igual que una inexistente.
	def, err := building.CanBuild(c, cmd.PlayerID, cmd.Type, cmd.Tile, s.state.buildingOccupied, stock)
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
	// La reserva: la madera sale ya de la RAM para que un segundo encargo del
	// mismo tick no la cuente dos veces. Los depósitos sin volcar viajan con el
	// encargo, en su transacción (ADR-023 §1).
	if err := eco.wood.Debit(def.Cost); err != nil {
		s.economyError(cmd.PlayerID, cmd.RequestID, protocol.CodeInsufficientResources, err.Error())
		return
	}
	carried := eco.unflushed
	eco.unflushed = 0
	delete(s.state.stockDirty, c.ID)

	b := building.Building{
		CityID: c.ID, Type: def.Type, X: cmd.Tile.X, Y: cmd.Tile.Y,
		Status: building.StatusUnderConstruction, CompletesAtMs: nowMs + def.BuildMs,
	}
	s.state.pendingBuilds[cmd.Tile] = pendingBuild{
		playerID: cmd.PlayerID, requestID: cmd.RequestID, building: b, cost: def.Cost, carried: carried,
	}

	order := BuildOrder{
		RequestID: cmd.RequestID, PlayerID: cmd.PlayerID, Building: b, Tick: s.state.Tick(),
		Stock: StockDelta{CityID: c.ID, Resource: def.Cost.Resource, Delta: carried - def.Cost.Amount, Version: eco.wood.Version},
	}
	resolved := BuildResolved{PlayerID: cmd.PlayerID, RequestID: cmd.RequestID, CityID: c.ID, Tile: cmd.Tile}
	store := s.deps.Economy
	s.deps.Persister.Submit(Job{
		Name: JobCityBuild,
		Key:  CityKey(c.ID),
		Run: func(ctx context.Context) error {
			id, err := store.Build(ctx, order)
			if err == nil {
				r := resolved
				r.BuildingID = id
				s.reinject(r)
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
			s.deps.Log.Error("no se pudo escribir el encargo del edificio; la madera vuelve a la ciudad",
				"err", err, "city_id", resolved.CityID, "x", resolved.Tile.X, "y", resolved.Tile.Y)
			r := resolved
			r.Code = protocol.CodeInternalError
			s.reinject(r)
		},
	})
}

// handleBuildResolved confirma el edificio o devuelve la reserva (RN-ECON-008).
func (s *Simulation) handleBuildResolved(cmd BuildResolved) {
	pb, ok := s.state.pendingBuilds[cmd.Tile]
	if !ok || pb.requestID != cmd.RequestID {
		return
	}
	delete(s.state.pendingBuilds, cmd.Tile)
	c, ok := s.state.City(cmd.CityID)
	if !ok {
		return
	}
	eco := s.state.economyOf(c.ID)

	if cmd.Code != "" {
		// La transacción no escribió nada: la madera reservada y los depósitos
		// que viajaban con ella vuelven a la ciudad.
		_ = eco.wood.Credit(pb.cost.Amount)
		eco.unflushed += pb.carried
		if eco.unflushed > 0 {
			s.state.stockDirty[c.ID] = struct{}{}
		}
		s.economyError(cmd.PlayerID, cmd.RequestID, cmd.Code, "no se pudo encargar el edificio")
		s.broadcastStocks(c)
		return
	}

	b := pb.building
	b.ID = cmd.BuildingID
	s.state.addBuilding(b)
	cx, cy := s.state.World().ChunkOf(b.X, b.Y)
	s.deps.Broadcaster.BroadcastChunk(cx, cy, protocol.TypeBuildingSpawn,
		protocol.BuildingSpawnPayload{Building: BuildingView(b)})
	s.broadcastStocks(c)
}

// ─────────────────────────────────────────────────────────────
// city.train
// ─────────────────────────────────────────────────────────────

// handleTrainUnit valida en el tick y reserva madera y población; la
// transacción de la cola debita, reserva y escribe la producción.
func (s *Simulation) handleTrainUnit(cmd TrainUnit) {
	c, _ := s.state.City(cmd.CityID)
	var stock *economy.Stock
	busy := false
	if c != nil {
		eco := s.state.economyOf(c.ID)
		stock, busy = &eco.wood, eco.production != nil
	}
	// RN-ECON-010 en el orden de la spec: ciudad, producción en curso,
	// población y madera.
	def, err := production.CanTrain(c, cmd.PlayerID, cmd.Type, busy, stock)
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
	if err := eco.wood.Debit(def.TrainCost); err != nil {
		s.economyError(cmd.PlayerID, cmd.RequestID, protocol.CodeInsufficientResources, err.Error())
		return
	}
	carried := eco.unflushed
	eco.unflushed = 0
	delete(s.state.stockDirty, c.ID)
	// RN-ECON-010: la plaza se reserva al ordenar.
	c.Population += def.PopulationCost

	p := production.Production{
		CityID: c.ID, UnitType: def.Type, CompletesAtMs: nowMs + def.TrainMs, RequestID: cmd.RequestID,
	}
	eco.production = &productionState{
		Production: p, cost: def.TrainCost, popCost: def.PopulationCost, carried: carried, pending: true,
	}

	order := TrainOrder{
		RequestID: cmd.RequestID, PlayerID: cmd.PlayerID, Production: p,
		PopulationCost: def.PopulationCost, Tick: s.state.Tick(),
		Stock: StockDelta{CityID: c.ID, Resource: def.TrainCost.Resource, Delta: carried - def.TrainCost.Amount, Version: eco.wood.Version},
	}
	resolved := TrainResolved{PlayerID: cmd.PlayerID, RequestID: cmd.RequestID, CityID: c.ID}
	store := s.deps.Economy
	s.deps.Persister.Submit(Job{
		Name: JobCityTrain,
		Key:  CityKey(c.ID),
		Run: func(ctx context.Context) error {
			err := store.Train(ctx, order)
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
			s.deps.Log.Error("no se pudo escribir el encargo del entrenamiento; la reserva vuelve a la ciudad",
				"err", err, "city_id", resolved.CityID)
			r := resolved
			r.Code = protocol.CodeInternalError
			s.reinject(r)
		},
	})
}

// handleTrainResolved confirma la producción o deshace la reserva.
func (s *Simulation) handleTrainResolved(cmd TrainResolved) {
	c, ok := s.state.City(cmd.CityID)
	if !ok {
		return
	}
	eco := s.state.economyOf(c.ID)
	p := eco.production
	if p == nil || !p.pending || p.RequestID != cmd.RequestID {
		return
	}

	if cmd.Code != "" {
		_ = eco.wood.Credit(p.cost.Amount)
		eco.unflushed += p.carried
		if eco.unflushed > 0 {
			s.state.stockDirty[c.ID] = struct{}{}
		}
		c.Population -= p.popCost
		eco.production = nil
		s.economyError(cmd.PlayerID, cmd.RequestID, cmd.Code, "no se pudo encargar el entrenamiento")
		stocks, population := s.state.stocksView(c.ID), c.Population
		s.broadcastCity(c, protocol.CityUpdatePayload{Stocks: &stocks, Population: &population})
		return
	}

	p.pending = false
	stocks, population := s.state.stocksView(c.ID), c.Population
	view := s.state.productionView(c.ID)
	s.broadcastCity(c, protocol.CityUpdatePayload{Stocks: &stocks, Population: &population, Production: &view})
}

// ─────────────────────────────────────────────────────────────
// Fase 5: edificios y producciones que vencen
// ─────────────────────────────────────────────────────────────

// completeBuildings completa, en orden de id, las casas cuya obra venció
// (RN-ECON-009). Es optimista: la RAM y los mensajes van primero, y la
// escritura lleva su compensación (ADR-023 §5).
func (s *Simulation) completeBuildings(nowMs int64) {
	for _, id := range s.state.buildingIDs {
		b := s.state.buildings[id]
		if !b.Due(nowMs) {
			continue
		}
		c, ok := s.state.City(b.CityID)
		if !ok {
			continue
		}
		def, err := building.Lookup(b.Type)
		if err != nil {
			continue
		}
		if err := b.Complete(); err != nil {
			continue
		}
		// INV-ECON-004: el límite es la era más el bono de las casas completas;
		// la casa que se completa suma el suyo una sola vez.
		c.PopulationLimit += def.PopulationBonus

		cx, cy := s.state.World().ChunkOf(b.X, b.Y)
		s.deps.Broadcaster.BroadcastChunk(cx, cy, protocol.TypeBuildingUpdate,
			protocol.BuildingUpdatePayload{ID: b.ID, Status: string(b.Status)})
		limit := c.PopulationLimit
		s.broadcastCity(c, protocol.CityUpdatePayload{PopulationLimit: &limit})

		s.submitBuildingComplete(BuildingCompletion{
			BuildingID: b.ID, CityID: c.ID, PlayerID: c.OwnerPlayerID, Bonus: def.PopulationBonus, Tick: s.state.Tick(),
		})
	}
}

func (s *Simulation) submitBuildingComplete(bc BuildingCompletion) {
	if s.deps.Economy == nil {
		return
	}
	store := s.deps.Economy
	s.deps.Persister.Submit(Job{
		Name: JobBuildingComplete,
		Key:  CityKey(bc.CityID),
		Run:  func(ctx context.Context) error { return store.CompleteBuilding(ctx, bc) },
		OnPermanentFailure: func(err error) {
			s.deps.Log.Error("no se pudo escribir la finalización del edificio; se reintentará en el siguiente volcado",
				"err", err, "building_id", bc.BuildingID)
			s.reinject(BuildingCompletePersistFailed{BuildingID: bc.BuildingID})
		},
	})
}

func (s *Simulation) handleBuildingCompletePersistFailed(cmd BuildingCompletePersistFailed) {
	if _, ok := s.state.buildings[cmd.BuildingID]; ok {
		s.state.pendingCompletions[cmd.BuildingID] = struct{}{}
	}
}

// flushCompletions reencola las finalizaciones descartadas. Es idempotente:
// la escritura sólo suma el bono si la fila seguía en obras.
func (s *Simulation) flushCompletions() {
	if len(s.state.pendingCompletions) == 0 {
		return
	}
	ids := make([]int64, 0, len(s.state.pendingCompletions))
	for id := range s.state.pendingCompletions {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	s.state.pendingCompletions = make(map[int64]struct{})
	for _, id := range ids {
		b, ok := s.state.buildings[id]
		if !ok {
			continue
		}
		c, ok := s.state.City(b.CityID)
		def, err := building.Lookup(b.Type)
		if !ok || err != nil {
			continue
		}
		s.submitBuildingComplete(BuildingCompletion{
			BuildingID: b.ID, CityID: c.ID, PlayerID: c.OwnerPlayerID, Bonus: def.PopulationBonus, Tick: s.state.Tick(),
		})
	}
}

// completeProductions decide, en orden de ciudad, el nacimiento de los
// entrenamientos vencidos (RN-ECON-011). El aldeano entra en la RAM cuando la
// base le da su id (ADR-023 §3); sin tile libre, espera al tick siguiente.
func (s *Simulation) completeProductions(nowMs int64) {
	if s.deps.Economy == nil {
		return
	}
	w := s.state.World()
	s.state.EachCity(func(c *city.City) bool {
		eco, ok := s.state.economies[c.ID]
		if !ok || eco.production == nil {
			return true
		}
		p := eco.production
		if p.pending || p.birthing || !p.Due(nowMs) || nowMs < p.retryAtMs {
			return true
		}
		def, err := unit.Lookup(p.UnitType)
		if err != nil {
			return true
		}
		tile, ok := c.FirstSpawnTile(w.IsWalkable)
		if !ok {
			// RN-ECON-011: como la salida de guarnición sin tile (RN-GARR-014).
			return true
		}
		p.birthing = true
		birth := Birth{CityID: c.ID, PlayerID: c.OwnerPlayerID, Type: p.UnitType, At: tile, HP: def.MaxHP, Tick: s.state.Tick()}
		store := s.deps.Economy
		s.deps.Persister.Submit(Job{
			Name: JobProductionComplete,
			Key:  CityKey(c.ID),
			Run: func(ctx context.Context) error {
				id, err := store.BirthUnit(ctx, birth)
				if err != nil {
					return err
				}
				s.reinject(BirthResolved{CityID: birth.CityID, UnitID: id, At: birth.At})
				return nil
			},
			OnPermanentFailure: func(err error) {
				s.deps.Log.Error("no se pudo escribir el nacimiento; se reintentará",
					"err", err, "city_id", birth.CityID)
				s.reinject(BirthResolved{CityID: birth.CityID, At: birth.At, Code: protocol.CodeInternalError})
			},
		})
		return true
	})
}

// handleBirthResolved incorpora la unidad que la base acaba de confirmar.
func (s *Simulation) handleBirthResolved(cmd BirthResolved) {
	c, ok := s.state.City(cmd.CityID)
	if !ok {
		return
	}
	eco := s.state.economyOf(c.ID)
	p := eco.production
	if p == nil || !p.birthing {
		return
	}
	p.birthing = false
	if cmd.Code != "" {
		p.retryAtMs = s.deps.Clock.NowMs() + birthRetryMs
		return
	}
	if _, exists := s.state.Unit(cmd.UnitID); exists {
		// Un reintento cuya primera respuesta ya llegó: la unidad ya está.
		eco.production = nil
		return
	}
	def, err := unit.Lookup(p.UnitType)
	if err != nil {
		return
	}
	cityID := c.ID
	u := &unit.Unit{
		ID: cmd.UnitID, PlayerID: c.OwnerPlayerID, CityID: &cityID, Type: p.UnitType,
		X: cmd.At.X, Y: cmd.At.Y, HP: def.MaxHP, MaxHP: def.MaxHP, Status: unit.StatusIdle,
	}
	// La población ya contaba esta plaza desde el encargo (INV-CITY-017).
	s.state.AddUnit(u)
	eco.production = nil

	nowMs := s.deps.Clock.NowMs()
	cx, cy := s.state.World().ChunkOf(u.X, u.Y)
	s.deps.Broadcaster.BroadcastChunk(cx, cy, protocol.TypeEntitySpawn,
		protocol.EntitySpawnPayload{Unit: s.UnitView(u, nowMs)})
	var ninguna *protocol.ProductionView
	s.broadcastCity(c, protocol.CityUpdatePayload{Production: &ninguna})
}

// ─────────────────────────────────────────────────────────────
// Utilidades
// ─────────────────────────────────────────────────────────────

func (s *Simulation) economyError(playerID uuid.UUID, requestID, code, message string) {
	s.deps.Broadcaster.SendToPlayer(playerID, protocol.TypeSystemError, requestID,
		protocol.SystemErrorPayload{Code: code, Message: message})
}

// economyErrorCode traduce un error de dominio de la economía al catálogo
// (SPEC-ECON-WOOD §5). Lo que no es de dominio es un fallo interno.
func economyErrorCode(err error) string {
	switch {
	case errors.Is(err, building.ErrCityNotFound), errors.Is(err, production.ErrCityNotFound):
		return protocol.CodeCityNotFound
	case errors.Is(err, economy.ErrInsufficientResources):
		return protocol.CodeInsufficientResources
	case errors.Is(err, production.ErrCityBusy):
		return protocol.CodeCityBusy
	case errors.Is(err, city.ErrPopulationLimit):
		return protocol.CodePopulationLimitReached
	case errors.Is(err, building.ErrInvalidSite), errors.Is(err, building.ErrUnknownType),
		errors.Is(err, unit.ErrUnknownType), errors.Is(err, production.ErrNotTrainable):
		return protocol.CodeInvalidTarget
	default:
		return protocol.CodeInternalError
	}
}
