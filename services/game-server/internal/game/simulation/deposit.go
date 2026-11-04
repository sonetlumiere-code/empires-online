package simulation

import (
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/deposit"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/movement"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/task"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// El recurso de los tiles en la simulación: la madera del bosque
// (SPEC-ECON-FOREST, ADR-025) y la piedra de la colina (SPEC-ECON-STONE,
// ADR-030).
//
// El terreno no cambia: un tile agotado sigue siendo FOREST o HILL. Cada capa
// guarda, sólo para los tiles tocados, cuánto queda y cuándo vuelve a crecer, y
// el terreno del tile decide la capa. La carga la descuenta en el mismo paso en
// que la pone en la unidad, y su escritura viaja como delta en la transacción
// de la tarea.

// depositSweepInterval es cada cuánto se limpian los tiles que volvieron a
// crecer. La lógica no depende de él: un tile vencido cuenta como lleno desde
// su instante exacto (RN-ECON-016); el barrido sólo lo saca de la capa y lo
// anuncia, como la caducidad de los tratados.
const depositSweepInterval = time.Second

// DepositRow es una fila de la capa de un terreno (forest_wood, hill_stone).
type DepositRow struct {
	Tile  world.Tile    `json:"tile"`
	State deposit.State `json:"state"`
}

// ForestRow es una fila de forest_wood.
type ForestRow = DepositRow

// DepositLoad es la carga de un tile, tal como la escribe la transacción de la
// tarea (ADR-025 §2, ADR-030 §1).
type DepositLoad struct {
	// Layer es la capa del tile: decide la tabla y el evento de agotamiento.
	Layer deposit.Layer
	Tile  world.Tile
	// Amount es lo que pierde el tile, que es lo que gana la unidad.
	Amount int32
	// AtMs es el instante de la carga, y RegrowsAtMs cuándo vuelve a crecer.
	AtMs        int64
	RegrowsAtMs int64
	// FromTrips y FromUntilMs son la fase WORKING que la carga consume: la
	// transacción sólo aplica el delta si la tarea sigue en ella, y así un
	// reintento de una transacción ya confirmada no descuenta dos veces.
	UnitID      int64
	FromTrips   int32
	FromUntilMs int64
	// Depleted es si el tile quedó vacío: la transacción escribe además
	// ForestDepleted o HillDepleted.
	Depleted bool
	PlayerID uuid.UUID
	CityID   *int64
	Tick     uint64
}

// ForestLoad es la carga de un tile de bosque.
type ForestLoad = DepositLoad

// layerState es la capa de un terreno en la RAM.
type layerState struct {
	layer deposit.Layer
	// msgType es el delta que anuncia un cambio (forest.update, hill.update).
	msgType   string
	tiles     map[world.Tile]deposit.State
	lastSweep int64
}

type depositState struct {
	forest layerState
	hill   layerState
	// pending es la carga que la próxima transición de tarea de este paso
	// debe escribir (submitTask la recoge). Sólo vive dentro de un paso del
	// tick, en la goroutine del loop.
	pending *DepositLoad
}

func newDepositState() depositState {
	return depositState{
		forest: layerState{layer: deposit.Forest, msgType: protocol.TypeForestUpdate, tiles: make(map[world.Tile]deposit.State)},
		hill:   layerState{layer: deposit.Hill, msgType: protocol.TypeHillUpdate, tiles: make(map[world.Tile]deposit.State)},
	}
}

// layers son las capas en orden estable.
func (d *depositState) layers() []*layerState { return []*layerState{&d.forest, &d.hill} }

// layerFor es la capa de un terreno; nil si el terreno no da recurso.
func (d *depositState) layerFor(t world.TerrainType) *layerState {
	for _, l := range d.layers() {
		if l.layer.Terrain == t {
			return l
		}
	}
	return nil
}

// ─────────────────────────────────────────────────────────────
// Lectura y carga de la capa
// ─────────────────────────────────────────────────────────────

// setLayer instala una capa leída de la base. Una fila vencida es un tile
// lleno y no entra; una fila fuera de rango o sobre un tile de otro terreno se
// descarta y se señala (INV-ECON-007). Devuelve cuántas entraron.
func (s *State) setLayer(l *layerState, rows []DepositRow, nowMs int64, violation func(t world.Tile, detail string)) int {
	n := 0
	for _, r := range rows {
		if err := l.layer.Validate(r.State); err != nil {
			violation(r.Tile, err.Error())
			continue
		}
		if !s.world.InBounds(r.Tile.X, r.Tile.Y) || s.world.TerrainAt(r.Tile.X, r.Tile.Y) != l.layer.Terrain {
			violation(r.Tile, "la fila no está sobre un tile "+l.layer.Terrain.String())
			continue
		}
		if r.State.Regrown(nowMs) {
			continue
		}
		l.tiles[r.Tile] = r.State
		n++
	}
	return n
}

// SetForests instala la capa de madera leída de la base.
func (s *State) SetForests(rows []DepositRow, nowMs int64, violation func(t world.Tile, detail string)) int {
	return s.setLayer(&s.deposits.forest, rows, nowMs, violation)
}

// SetHills instala la capa de piedra leída de la base.
func (s *State) SetHills(rows []DepositRow, nowMs int64, violation func(t world.Tile, detail string)) int {
	return s.setLayer(&s.deposits.hill, rows, nowMs, violation)
}

// remainingIn es lo que le queda a un tile de una capa en `nowMs`: 0 si el
// tile no es de su terreno, la capacidad si nadie lo ha tocado o si ya volvió
// a crecer.
func (s *State) remainingIn(l *layerState, t world.Tile, nowMs int64) int32 {
	if !s.world.InBounds(t.X, t.Y) || s.world.TerrainAt(t.X, t.Y) != l.layer.Terrain {
		return 0
	}
	e, ok := l.tiles[t]
	if !ok {
		return l.layer.Capacity
	}
	return l.layer.RemainingAt(e, nowMs)
}

// ResourceAt es lo que le queda a un tile en `nowMs`, en la capa de su terreno:
// 0 si su terreno no da recurso.
func (s *State) ResourceAt(t world.Tile, nowMs int64) int32 {
	if !s.world.InBounds(t.X, t.Y) {
		return 0
	}
	l := s.deposits.layerFor(s.world.TerrainAt(t.X, t.Y))
	if l == nil {
		return 0
	}
	return s.remainingIn(l, t, nowMs)
}

// ForestAt es la madera de un tile en `nowMs`: 0 si no es bosque.
func (s *State) ForestAt(t world.Tile, nowMs int64) int32 {
	return s.remainingIn(&s.deposits.forest, t, nowMs)
}

// HillAt es la piedra de un tile en `nowMs`: 0 si no es colina.
func (s *State) HillAt(t world.Tile, nowMs int64) int32 {
	return s.remainingIn(&s.deposits.hill, t, nowMs)
}

// rowsOf es una capa en `nowMs`, en orden de (y, x): los tiles que no están
// llenos (tests y golden).
func rowsOf(l *layerState, nowMs int64) []DepositRow {
	var tiles []world.Tile
	for t, st := range l.tiles {
		if !st.Regrown(nowMs) {
			tiles = append(tiles, t)
		}
	}
	sortTiles(tiles)
	out := make([]DepositRow, 0, len(tiles))
	for _, t := range tiles {
		out = append(out, DepositRow{Tile: t, State: l.tiles[t]})
	}
	return out
}

// Forests es la capa de madera en `nowMs`.
func (s *State) Forests(nowMs int64) []DepositRow { return rowsOf(&s.deposits.forest, nowMs) }

// Hills es la capa de piedra en `nowMs`.
func (s *State) Hills(nowMs int64) []DepositRow { return rowsOf(&s.deposits.hill, nowMs) }

// SetForest fija la madera de un tile (tests).
func (s *State) SetForest(t world.Tile, st deposit.State) { s.deposits.forest.tiles[t] = st }

// SetHill fija la piedra de un tile (tests).
func (s *State) SetHill(t world.Tile, st deposit.State) { s.deposits.hill.tiles[t] = st }

// ForestCount es cuántos tiles tiene la capa de madera (tests y métricas).
func (s *State) ForestCount() int { return len(s.deposits.forest.tiles) }

// HillCount es cuántos tiles tiene la capa de piedra.
func (s *State) HillCount() int { return len(s.deposits.hill.tiles) }

// depositView proyecta un tile al contrato de red. Bosque y colina comparten
// forma (forest.update, hill.update).
func depositView(t world.Tile, st deposit.State) protocol.ForestView {
	at := st.RegrowsAtMs
	return protocol.ForestView{X: t.X, Y: t.Y, Remaining: st.Remaining, RegrowsAtMs: &at}
}

// layerInChunk son los tiles no llenos de una capa en un chunk, en orden de
// (y, x).
func (s *State) layerInChunk(l *layerState, cx, cy int32, nowMs int64) []protocol.ForestView {
	var tiles []world.Tile
	for t, st := range l.tiles {
		if st.Regrown(nowMs) {
			continue
		}
		if x, y := s.world.ChunkOf(t.X, t.Y); x == cx && y == cy {
			tiles = append(tiles, t)
		}
	}
	sortTiles(tiles)
	out := make([]protocol.ForestView, 0, len(tiles))
	for _, t := range tiles {
		out = append(out, depositView(t, l.tiles[t]))
	}
	return out
}

func sortTiles(ts []world.Tile) {
	sort.Slice(ts, func(i, j int) bool {
		if ts[i].Y != ts[j].Y {
			return ts[i].Y < ts[j].Y
		}
		return ts[i].X < ts[j].X
	})
}

// ─────────────────────────────────────────────────────────────
// La carga, la búsqueda del tile siguiente y la regeneración
// ─────────────────────────────────────────────────────────────

// taskLayer es la capa de la tarea de una unidad (ADR-030 §2).
func (s *State) taskLayer(t task.Task) *layerState {
	l, ok := t.Kind.Layer()
	if !ok {
		return nil
	}
	return s.deposits.layerFor(l.Terrain)
}

// taskRemaining es lo que le queda al tile de trabajo de una tarea, en su capa.
func (s *State) taskRemaining(t task.Task, tile world.Tile, nowMs int64) int32 {
	l := s.taskLayer(t)
	if l == nil {
		return 0
	}
	return s.remainingIn(l, tile, nowMs)
}

// takeDeposit carga a una unidad que termina de trabajar en `t.Target` hasta
// `carry` (RN-ECON-013, RN-ECON-021): descuenta el tile en la capa de su tarea,
// anuncia el cambio y deja la carga apuntada para la transacción de la
// transición siguiente. Devuelve lo que se lleva. Varias unidades del mismo
// tile en el mismo tick cargan en orden de unidad, que es el orden de
// advanceTasks.
func (s *Simulation) takeDeposit(u *unit.Unit, t task.Task, carry int32, atMs int64) int32 {
	l := s.state.taskLayer(t)
	if l == nil {
		return 0
	}
	tile := t.Target
	taken, after := l.layer.Take(s.state.remainingIn(l, tile, atMs), carry, atMs)
	if taken == 0 {
		return 0
	}
	l.tiles[tile] = after
	load := &DepositLoad{
		Layer: l.layer, Tile: tile, Amount: taken, AtMs: atMs, RegrowsAtMs: after.RegrowsAtMs,
		UnitID: u.ID, FromTrips: t.TripsDone, FromUntilMs: t.PhaseUntilMs,
		Depleted: after.Remaining == 0, PlayerID: u.PlayerID, CityID: u.CityID, Tick: s.state.Tick(),
	}
	s.state.deposits.pending = load
	s.broadcastDeposit(l, tile, after)
	return taken
}

func (s *Simulation) broadcastDeposit(l *layerState, t world.Tile, st deposit.State) {
	cx, cy := s.state.World().ChunkOf(t.X, t.Y)
	s.deps.Broadcaster.BroadcastChunk(cx, cy, l.msgType, depositView(t, st))
}

// findDeposit busca, en la capa de la tarea, el tile con recurso más cercano a
// `from`, a distancia Chebyshev ≤ su SearchRadius, al que la unidad llegue a
// pie desde donde está (RN-ECON-015, RN-ECON-022). Nunca cambia de capa: un
// aldeano de piedra no se pasa a un bosque. Desempata por distancia, después
// `y`, después `x`, y prueba el pathfinding sólo con los SearchAttempts
// primeros. Devuelve el tile y la ruta desde la unidad.
func (s *Simulation) findDeposit(u *unit.Unit, t task.Task, from world.Tile, def unit.Definition, nowMs int64) (world.Tile, movement.TimedPath, bool) {
	l := s.state.taskLayer(t)
	if l == nil {
		return world.Tile{}, nil, false
	}
	r := l.layer.SearchRadius
	var cands []world.Tile
	for dy := -r; dy <= r; dy++ {
		for dx := -r; dx <= r; dx++ {
			c := world.Tile{X: from.X + dx, Y: from.Y + dy}
			if c == from || s.state.remainingIn(l, c, nowMs) == 0 {
				continue
			}
			cands = append(cands, c)
		}
	}
	sort.Slice(cands, func(i, j int) bool {
		di, dj := chebyshevTiles(from, cands[i]), chebyshevTiles(from, cands[j])
		if di != dj {
			return di < dj
		}
		if cands[i].Y != cands[j].Y {
			return cands[i].Y < cands[j].Y
		}
		return cands[i].X < cands[j].X
	})
	origin := u.Tile()
	for i, c := range cands {
		if i >= l.layer.SearchAttempts {
			break
		}
		if c == origin {
			return c, nil, true
		}
		path, err := s.legPath(origin, c, def)
		if err == nil {
			return c, path, true
		}
	}
	return world.Tile{}, nil, false
}

// regrowDeposits es la fase 5 de las capas: una vez por segundo, cada una saca
// los tiles que ya volvieron a crecer, en orden de (y, x), y lo anuncia.
func (s *Simulation) regrowDeposits(nowMs int64) {
	for _, l := range s.state.deposits.layers() {
		if len(l.tiles) == 0 || nowMs-l.lastSweep < depositSweepInterval.Milliseconds() {
			continue
		}
		l.lastSweep = nowMs
		var due []world.Tile
		for t, st := range l.tiles {
			if st.Regrown(nowMs) {
				due = append(due, t)
			}
		}
		sortTiles(due)
		for _, t := range due {
			delete(l.tiles, t)
			cx, cy := s.state.World().ChunkOf(t.X, t.Y)
			s.deps.Broadcaster.BroadcastChunk(cx, cy, l.msgType,
				protocol.ForestView{X: t.X, Y: t.Y, Remaining: l.layer.Capacity})
		}
	}
}
