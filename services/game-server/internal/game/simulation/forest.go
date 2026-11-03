package simulation

import (
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/forest"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/movement"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/task"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// La madera del bosque en la simulación (SPEC-ECON-FOREST, ADR-025).
//
// El terreno no cambia: un tile agotado sigue siendo FOREST. La capa guarda,
// sólo para los tiles tocados, cuánta madera queda y cuándo vuelve a crecer.
// La carga la descuenta en el mismo paso en que la pone en la unidad, y su
// escritura viaja como delta en la transacción de la tarea.

// forestSweepInterval es cada cuánto se limpian los tiles que volvieron a
// crecer. La lógica no depende de él: un tile vencido cuenta como lleno desde
// su instante exacto (RN-ECON-016); el barrido sólo lo saca de la capa y lo
// anuncia, como la caducidad de los tratados.
const forestSweepInterval = time.Second

// ForestRow es una fila de forest_wood.
type ForestRow struct {
	Tile  world.Tile   `json:"tile"`
	State forest.State `json:"state"`
}

// ForestLoad es la carga de un tile, tal como la escribe la transacción de la
// tarea (ADR-025 §2).
type ForestLoad struct {
	Tile world.Tile
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
	// ForestDepleted.
	Depleted bool
	PlayerID uuid.UUID
	CityID   *int64
	Tick     uint64
}

type forestState struct {
	tiles     map[world.Tile]forest.State
	lastSweep int64
	// pending es la carga que la próxima transición de tarea de este paso
	// debe escribir (submitTask la recoge). Sólo vive dentro de un paso del
	// tick, en la goroutine del loop.
	pending *ForestLoad
}

func newForestState() forestState {
	return forestState{tiles: make(map[world.Tile]forest.State)}
}

// SetForests instala la capa leída de la base. Una fila vencida es un tile
// lleno y no entra; una fila fuera de rango o sobre un tile que no es bosque
// se descarta y se señala (INV-ECON-007). Devuelve cuántas entraron.
func (s *State) SetForests(rows []ForestRow, nowMs int64, violation func(t world.Tile, detail string)) int {
	n := 0
	for _, r := range rows {
		if err := r.State.Validate(); err != nil {
			violation(r.Tile, err.Error())
			continue
		}
		if !s.world.InBounds(r.Tile.X, r.Tile.Y) || s.world.TerrainAt(r.Tile.X, r.Tile.Y) != world.Forest {
			violation(r.Tile, "la fila no está sobre un tile FOREST")
			continue
		}
		if r.State.Regrown(nowMs) {
			continue
		}
		s.forest.tiles[r.Tile] = r.State
		n++
	}
	return n
}

// ForestAt es la madera de un tile en `nowMs`: 0 si no es bosque, la capacidad
// si nadie lo ha tocado o si ya volvió a crecer.
func (s *State) ForestAt(t world.Tile, nowMs int64) int32 {
	if !s.world.InBounds(t.X, t.Y) || s.world.TerrainAt(t.X, t.Y) != world.Forest {
		return 0
	}
	e, ok := s.forest.tiles[t]
	if !ok {
		return forest.Capacity
	}
	return e.RemainingAt(nowMs)
}

// Forests es la capa en `nowMs`, en orden de (y, x): los tiles que no están
// llenos (tests y golden).
func (s *State) Forests(nowMs int64) []ForestRow {
	var tiles []world.Tile
	for t, st := range s.forest.tiles {
		if !st.Regrown(nowMs) {
			tiles = append(tiles, t)
		}
	}
	sortTiles(tiles)
	out := make([]ForestRow, 0, len(tiles))
	for _, t := range tiles {
		out = append(out, ForestRow{Tile: t, State: s.forest.tiles[t]})
	}
	return out
}

// SetForest fija la madera de un tile (tests).
func (s *State) SetForest(t world.Tile, st forest.State) { s.forest.tiles[t] = st }

// ForestCount es cuántos tiles tiene la capa (tests y métricas).
func (s *State) ForestCount() int { return len(s.forest.tiles) }

// forestView proyecta un tile al contrato de red.
func forestView(t world.Tile, st forest.State) protocol.ForestView {
	at := st.RegrowsAtMs
	return protocol.ForestView{X: t.X, Y: t.Y, Remaining: st.Remaining, RegrowsAtMs: &at}
}

// forestsInChunk son los tiles no llenos de un chunk, en orden de (y, x).
func (s *State) forestsInChunk(cx, cy int32, nowMs int64) []protocol.ForestView {
	var tiles []world.Tile
	for t, st := range s.forest.tiles {
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
		out = append(out, forestView(t, s.forest.tiles[t]))
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

// takeForest carga a una unidad que termina de trabajar en `t.Target` hasta
// `carry` (RN-ECON-013): descuenta el tile, anuncia el cambio y deja la carga
// apuntada para la transacción de la transición siguiente. Devuelve lo que se
// lleva. Varias unidades del mismo tile en el mismo tick cargan en orden de
// unidad, que es el orden de advanceTasks.
func (s *Simulation) takeForest(u *unit.Unit, t task.Task, carry int32, atMs int64) int32 {
	tile := t.Target
	taken, after := forest.Take(s.state.ForestAt(tile, atMs), carry, atMs)
	if taken == 0 {
		return 0
	}
	s.state.forest.tiles[tile] = after
	load := &ForestLoad{
		Tile: tile, Amount: taken, AtMs: atMs, RegrowsAtMs: after.RegrowsAtMs,
		UnitID: u.ID, FromTrips: t.TripsDone, FromUntilMs: t.PhaseUntilMs,
		Depleted: after.Remaining == 0, PlayerID: u.PlayerID, CityID: u.CityID, Tick: s.state.Tick(),
	}
	s.state.forest.pending = load
	s.broadcastForest(tile, after)
	return taken
}

func (s *Simulation) broadcastForest(t world.Tile, st forest.State) {
	cx, cy := s.state.World().ChunkOf(t.X, t.Y)
	s.deps.Broadcaster.BroadcastChunk(cx, cy, protocol.TypeForestUpdate, forestView(t, st))
}

// findForest busca el tile con madera más cercano a `from`, a distancia
// Chebyshev ≤ SearchRadius, al que la unidad llegue a pie desde donde está
// (RN-ECON-015). Desempata por distancia, después `y`, después `x`, y prueba
// el pathfinding sólo con los SearchAttempts primeros. Devuelve el tile y la
// ruta desde la unidad.
func (s *Simulation) findForest(u *unit.Unit, from world.Tile, def unit.Definition, nowMs int64) (world.Tile, movement.TimedPath, bool) {
	r := forest.SearchRadius
	var cands []world.Tile
	for dy := -r; dy <= r; dy++ {
		for dx := -r; dx <= r; dx++ {
			t := world.Tile{X: from.X + dx, Y: from.Y + dy}
			if t == from || s.state.ForestAt(t, nowMs) == 0 {
				continue
			}
			cands = append(cands, t)
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
		if i >= forest.SearchAttempts {
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

// regrowForests es la fase 5 del bosque: una vez por segundo, saca de la capa
// los tiles que ya volvieron a crecer, en orden de (y, x), y lo anuncia.
func (s *Simulation) regrowForests(nowMs int64) {
	if len(s.state.forest.tiles) == 0 || nowMs-s.state.forest.lastSweep < forestSweepInterval.Milliseconds() {
		return
	}
	s.state.forest.lastSweep = nowMs
	var due []world.Tile
	for t, st := range s.state.forest.tiles {
		if st.Regrown(nowMs) {
			due = append(due, t)
		}
	}
	sortTiles(due)
	for _, t := range due {
		delete(s.state.forest.tiles, t)
		cx, cy := s.state.World().ChunkOf(t.X, t.Y)
		s.deps.Broadcaster.BroadcastChunk(cx, cy, protocol.TypeForestUpdate,
			protocol.ForestView{X: t.X, Y: t.Y, Remaining: forest.Capacity})
	}
}
