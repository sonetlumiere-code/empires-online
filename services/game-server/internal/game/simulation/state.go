// Package simulation mantiene el estado autoritativo del mundo en memoria.
//
// Propiedad de la memoria: TODO lo que hay aquí lo posee la goroutine del game
// loop. Ninguna otra goroutine muta este estado — las conexiones WebSocket envían
// comandos por un canal y esperan a que el loop los aplique. Esa regla es lo que
// evita tener que sembrar mutexes por todo el dominio.
package simulation

import (
	"sort"

	"github.com/google/uuid"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/movement"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
)

// chunkKey empaqueta unas coordenadas de chunk en una clave de mapa.
type chunkKey uint64

func makeChunkKey(cx, cy int32) chunkKey {
	return chunkKey(uint64(uint32(cx))<<32 | uint64(uint32(cy)))
}

// State es el mundo vivo.
type State struct {
	world *world.World

	units     map[int64]*unit.Unit
	unitIDs   []int64 // ordenado: garantiza recorridos deterministas
	unitDirty map[int64]struct{}

	// movements se indexa por unitID: por construcción sólo puede haber uno
	// activo por unidad, que es justamente INV-MOVE-001.
	movements map[int64]*movement.Movement

	cities       map[int64]*city.City
	cityByOwner  map[uuid.UUID]int64
	unitsByChunk map[chunkKey]map[int64]struct{}

	tick uint64
}

// NewState crea un mundo vacío sobre el mapa dado.
func NewState(w *world.World) *State {
	return &State{
		world:        w,
		units:        make(map[int64]*unit.Unit),
		unitDirty:    make(map[int64]struct{}),
		movements:    make(map[int64]*movement.Movement),
		cities:       make(map[int64]*city.City),
		cityByOwner:  make(map[uuid.UUID]int64),
		unitsByChunk: make(map[chunkKey]map[int64]struct{}),
	}
}

// World devuelve el mapa.
func (s *State) World() *world.World { return s.world }

// Tick devuelve el tick actual.
func (s *State) Tick() uint64 { return s.tick }

// SetTick fija el tick actual.
func (s *State) SetTick(t uint64) { s.tick = t }

// ─────────────────────────────────────────────────────────────
// Unidades
// ─────────────────────────────────────────────────────────────

// AddUnit incorpora una unidad al mundo.
func (s *State) AddUnit(u *unit.Unit) {
	if _, exists := s.units[u.ID]; !exists {
		s.unitIDs = insertSorted(s.unitIDs, u.ID)
	}
	s.units[u.ID] = u
	s.indexUnit(u)
}

// RemoveUnit saca una unidad del mundo.
func (s *State) RemoveUnit(id int64) {
	u, ok := s.units[id]
	if !ok {
		return
	}
	s.unindexUnit(u)
	delete(s.units, id)
	delete(s.movements, id)
	delete(s.unitDirty, id)
	s.unitIDs = removeSorted(s.unitIDs, id)
}

// Unit recupera una unidad por identificador.
func (s *State) Unit(id int64) (*unit.Unit, bool) {
	u, ok := s.units[id]
	return u, ok
}

// UnitCount es el número de unidades vivas en la simulación.
func (s *State) UnitCount() int { return len(s.units) }

// EachUnit recorre las unidades en orden estable de identificador.
//
// El orden importa: un recorrido no determinista haría que dos ejecuciones con la
// misma entrada produjeran estados distintos, y con ello se pierden los tests de
// simulación reproducibles.
func (s *State) EachUnit(fn func(*unit.Unit) bool) {
	for _, id := range s.unitIDs {
		if u, ok := s.units[id]; ok {
			if !fn(u) {
				return
			}
		}
	}
}

// UnitsInChunk devuelve, en orden estable, las unidades de un chunk.
func (s *State) UnitsInChunk(cx, cy int32) []*unit.Unit {
	set, ok := s.unitsByChunk[makeChunkKey(cx, cy)]
	if !ok {
		return nil
	}
	ids := make([]int64, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	out := make([]*unit.Unit, 0, len(ids))
	for _, id := range ids {
		if u, ok := s.units[id]; ok {
			out = append(out, u)
		}
	}
	return out
}

// MoveUnitTo actualiza la posición de una unidad y reindexa su chunk.
//
// Devuelve true si la unidad cambió de chunk: quien llama necesita saberlo para
// emitir spawn/despawn a las sesiones cuyo área de interés cambió.
func (s *State) MoveUnitTo(u *unit.Unit, t world.Tile) bool {
	oldCX, oldCY := s.world.ChunkOf(u.X, u.Y)
	newCX, newCY := s.world.ChunkOf(t.X, t.Y)
	u.SetTile(t)

	if oldCX == newCX && oldCY == newCY {
		return false
	}
	s.removeFromChunk(oldCX, oldCY, u.ID)
	s.addToChunk(newCX, newCY, u.ID)
	return true
}

func (s *State) indexUnit(u *unit.Unit) {
	cx, cy := s.world.ChunkOf(u.X, u.Y)
	s.addToChunk(cx, cy, u.ID)
}

func (s *State) unindexUnit(u *unit.Unit) {
	cx, cy := s.world.ChunkOf(u.X, u.Y)
	s.removeFromChunk(cx, cy, u.ID)
}

func (s *State) addToChunk(cx, cy int32, id int64) {
	key := makeChunkKey(cx, cy)
	set, ok := s.unitsByChunk[key]
	if !ok {
		set = make(map[int64]struct{})
		s.unitsByChunk[key] = set
	}
	set[id] = struct{}{}
}

func (s *State) removeFromChunk(cx, cy int32, id int64) {
	key := makeChunkKey(cx, cy)
	if set, ok := s.unitsByChunk[key]; ok {
		delete(set, id)
		if len(set) == 0 {
			delete(s.unitsByChunk, key)
		}
	}
}

// ─────────────────────────────────────────────────────────────
// Estado sucio (volcado periódico)
// ─────────────────────────────────────────────────────────────

// MarkDirty señala una unidad cuya posición o estado debe volcarse.
func (s *State) MarkDirty(id int64) { s.unitDirty[id] = struct{}{} }

// DrainDirty devuelve las unidades sucias y limpia el conjunto.
//
// Es la mitad de la estrategia que evita un UPDATE por unidad y por tick: se
// acumulan los cambios y se vuelcan en un único statement por lote.
func (s *State) DrainDirty() []*unit.Unit {
	if len(s.unitDirty) == 0 {
		return nil
	}
	ids := make([]int64, 0, len(s.unitDirty))
	for id := range s.unitDirty {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	out := make([]*unit.Unit, 0, len(ids))
	for _, id := range ids {
		if u, ok := s.units[id]; ok {
			out = append(out, u)
		}
	}
	s.unitDirty = make(map[int64]struct{}, len(ids))
	return out
}

// DirtyCount indica cuántas unidades esperan volcado.
func (s *State) DirtyCount() int { return len(s.unitDirty) }

// ─────────────────────────────────────────────────────────────
// Movimientos
// ─────────────────────────────────────────────────────────────

// SetMovement registra el movimiento activo de una unidad, reemplazando el previo.
func (s *State) SetMovement(m *movement.Movement) { s.movements[m.UnitID] = m }

// Movement devuelve el movimiento activo de una unidad.
func (s *State) Movement(unitID int64) (*movement.Movement, bool) {
	m, ok := s.movements[unitID]
	return m, ok
}

// ClearMovement retira el movimiento activo de una unidad.
func (s *State) ClearMovement(unitID int64) { delete(s.movements, unitID) }

// MovementCount es el número de movimientos en curso.
func (s *State) MovementCount() int { return len(s.movements) }

// EachMovement recorre los movimientos activos ordenados por unitID.
func (s *State) EachMovement(fn func(*movement.Movement) bool) {
	ids := make([]int64, 0, len(s.movements))
	for id := range s.movements {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		if m, ok := s.movements[id]; ok {
			if !fn(m) {
				return
			}
		}
	}
}

// ─────────────────────────────────────────────────────────────
// Ciudades
// ─────────────────────────────────────────────────────────────

// AddCity incorpora una ciudad al mundo.
func (s *State) AddCity(c *city.City) {
	s.cities[c.ID] = c
	s.cityByOwner[c.OwnerPlayerID] = c.ID
}

// City recupera una ciudad por identificador.
func (s *State) City(id int64) (*city.City, bool) {
	c, ok := s.cities[id]
	return c, ok
}

// CityOf recupera la ciudad de un jugador.
func (s *State) CityOf(playerID uuid.UUID) (*city.City, bool) {
	id, ok := s.cityByOwner[playerID]
	if !ok {
		return nil, false
	}
	c, ok := s.cities[id]
	return c, ok
}

// CitiesInChunk devuelve las ciudades cuyo centro cae en un chunk.
func (s *State) CitiesInChunk(cx, cy int32) []*city.City {
	ids := make([]int64, 0)
	for id, c := range s.cities {
		ccx, ccy := s.world.ChunkOf(c.CenterX, c.CenterY)
		if ccx == cx && ccy == cy {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	out := make([]*city.City, 0, len(ids))
	for _, id := range ids {
		out = append(out, s.cities[id])
	}
	return out
}

// EachCity recorre las ciudades en orden estable.
func (s *State) EachCity(fn func(*city.City) bool) {
	ids := make([]int64, 0, len(s.cities))
	for id := range s.cities {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		if !fn(s.cities[id]) {
			return
		}
	}
}

// ─────────────────────────────────────────────────────────────
// Utilidades de slices ordenados
// ─────────────────────────────────────────────────────────────

func insertSorted(s []int64, v int64) []int64 {
	i := sort.Search(len(s), func(i int) bool { return s[i] >= v })
	if i < len(s) && s[i] == v {
		return s
	}
	s = append(s, 0)
	copy(s[i+1:], s[i:])
	s[i] = v
	return s
}

func removeSorted(s []int64, v int64) []int64 {
	i := sort.Search(len(s), func(i int) bool { return s[i] >= v })
	if i < len(s) && s[i] == v {
		return append(s[:i], s[i+1:]...)
	}
	return s
}
