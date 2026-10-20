package simulation

import (
	"fmt"
	"log/slog"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/movement"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// RN-CITY-022: levantar una zona urbana despeja su huella.
//
// La búsqueda de emplazamiento mira el terreno y la ocupación, no las unidades:
// una unidad ajena puede estar dentro del 3×3, o de camino a través de él, en el
// instante de la fundación. Sin esta regla quedaría emparedada sobre un tile
// intransitable desde el que el A* no puede salir (INV-UNIT-001), o su
// movimiento seguiría atravesando la muralla (INV-MOVE-004).
//
// Dos pasos, en este orden: primero se detienen los movimientos cuya ruta
// pendiente ya no es transitable, después se aparta lo que haya quedado dentro.
// Una unidad que iba a cruzar la muralla y se detiene dentro pasa por los dos.

// footprint es un rectángulo inclusivo de tiles.
type footprint struct {
	minX, minY, maxX, maxY int32
}

func urbanFootprint(c *city.City) footprint {
	minX, minY, maxX, maxY := c.UrbanBounds()
	return footprint{minX: minX, minY: minY, maxX: maxX, maxY: maxY}
}

func (f footprint) contains(t world.Tile) bool {
	return t.X >= f.minX && t.X <= f.maxX && t.Y >= f.minY && t.Y <= f.maxY
}

// evictionSearchRings acota la búsqueda de un tile libre alrededor de la huella.
// La fundación exige un entorno despejado de radio 3 (RN-CITY-003), así que el
// primer anillo siempre basta para una ciudad recién fundada; el margen cubre
// estados cargados de disco que nadie validó al escribirlos.
const evictionSearchRings int32 = 8

// evictionTile elige adónde se aparta una unidad que la huella deja dentro: el
// tile transitable más cercano a `from` (distancia Chebyshev) fuera de la
// huella, buscando anillo a anillo hacia fuera; a igual distancia, el menor en
// orden (y, x). Es determinista: dos ejecuciones apartan la unidad al mismo
// sitio. Devuelve false si no hay ninguno en evictionSearchRings anillos.
func evictionTile(w *world.World, f footprint, from world.Tile) (world.Tile, bool) {
	for ring := int32(1); ring <= evictionSearchRings; ring++ {
		best, found := world.Tile{}, false
		var bestDist int32
		for y := f.minY - ring; y <= f.maxY+ring; y++ {
			for x := f.minX - ring; x <= f.maxX+ring; x++ {
				// Sólo el anillo: lo de dentro ya se miró en anillos anteriores.
				if x > f.minX-ring && x < f.maxX+ring && y > f.minY-ring && y < f.maxY+ring {
					continue
				}
				if !w.IsWalkable(x, y) {
					continue
				}
				t := world.Tile{X: x, Y: y}
				// El recorrido ya va en orden (y, x): con < estricto, el primero
				// a la distancia mínima es el que desempata.
				if d := chebyshev(t, from); !found || d < bestDist {
					best, bestDist, found = t, d, true
				}
			}
		}
		if found {
			return best, true
		}
	}
	return world.Tile{}, false
}

func chebyshev(a, b world.Tile) int32 {
	return max(abs32(a.X-b.X), abs32(a.Y-b.Y))
}

func abs32(v int32) int32 {
	if v < 0 {
		return -v
	}
	return v
}

// occupiesWorld indica si la unidad está en el mapa. Una unidad GARRISONED está
// dentro de una ciudad y una DEAD fuera del juego: ninguna de las dos tiene un
// tile que despejar (INV-UNIT-003).
func occupiesWorld(u *unit.Unit) bool {
	return u.Status != unit.StatusGarrisoned && u.Status != unit.StatusDead
}

// blockedRoutes devuelve, en orden de unidad, las unidades cuyo movimiento
// tiene por delante algún tile que ya no es transitable, o un paso diagonal que
// ahora atajaría una esquina.
func (s *State) blockedRoutes(nowMs int64) []*unit.Unit {
	var out []*unit.Unit
	s.EachMovement(func(m *movement.Movement) bool {
		u, ok := s.units[m.UnitID]
		if !ok || !occupiesWorld(u) {
			return true
		}
		from := m.Path.IndexAt(nowMs - m.StartTimeMs)
		if m.Path.ValidateFrom(from, s.world) != nil {
			out = append(out, u)
		}
		return true
	})
	return out
}

// unitsInside devuelve, en orden de id, las unidades presentes en el mapa cuyo
// tile cae dentro de la huella. Recorre sólo los chunks que la huella toca.
func (s *State) unitsInside(f footprint) []*unit.Unit {
	minCX, minCY := s.world.ChunkOf(max(f.minX, 0), max(f.minY, 0))
	maxCX, maxCY := s.world.ChunkOf(min(f.maxX, s.world.Width()-1), min(f.maxY, s.world.Height()-1))
	var out []*unit.Unit
	for cy := minCY; cy <= maxCY; cy++ {
		for cx := minCX; cx <= maxCX; cx++ {
			for _, u := range s.UnitsInChunk(cx, cy) {
				if occupiesWorld(u) && f.contains(u.Tile()) {
					out = append(out, u)
				}
			}
		}
	}
	return out
}

// clearFootprint aplica RN-CITY-022 en el tick en que la huella pasa a la capa
// de ocupación. Debe llamarse DESPUÉS de marcarla: la validez de las rutas se
// comprueba contra el mundo ya amurallado.
func (s *Simulation) clearFootprint(f footprint, cityID int64, nowMs int64) {
	for _, u := range s.state.blockedRoutes(nowMs) {
		s.cancelActiveMovement(u, movement.ReasonPathBlocked, nowMs)
	}

	w := s.state.World()
	for _, u := range s.state.unitsInside(f) {
		to, ok := evictionTile(w, f, u.Tile())
		if !ok {
			s.invariantViolation("INV-UNIT-001", "CRITICO", "FAIL_FAST", u.ID,
				fmt.Sprintf("la muralla de la ciudad %d deja la unidad en %s y no hay tile libre alrededor",
					cityID, u.Tile()))
			continue
		}
		s.state.MoveUnitTo(u, to)
		// El estado se conserva: IDLE sigue IDLE, y una HIDDEN la reevalúa la
		// fase 5 en su tile nuevo, que es quien decide sobre el ocultamiento.
		s.state.MarkDirty(u.ID)

		x, y := to.X, to.Y
		update := protocol.EntityUpdatePayload{ID: u.ID, X: &x, Y: &y}
		if u.Status == unit.StatusHidden {
			// INV-SAFE-004: una unidad oculta no viaja a terceros.
			s.deps.Broadcaster.SendToPlayer(u.PlayerID, protocol.TypeEntityUpdate, "", update)
		} else {
			cx, cy := w.ChunkOf(to.X, to.Y)
			s.deps.Broadcaster.BroadcastChunk(cx, cy, protocol.TypeEntityUpdate, update)
		}
		s.deps.Log.Info("unidad apartada de una muralla nueva",
			"unit_id", u.ID, "city_id", cityID, "x", to.X, "y", to.Y)
	}
}

// ClearFootprints aplica RN-CITY-022 al arrancar, con las murallas de todas las
// ciudades ya reconstruidas en la capa de ocupación.
//
// Hace falta porque el alta escribe la ciudad en su transacción, pero la
// posición de una unidad apartada llega a disco con el siguiente volcado: una
// caída entre los dos deja la unidad dentro de la muralla. Los movimientos que
// detiene se devuelven para cerrarlos en la base de datos junto a los de
// Hydrate, con la posición final de su unidad; las unidades apartadas quedan
// sucias para el primer volcado.
func ClearFootprints(state *State, cities []*city.City, nowMs int64, log *slog.Logger) []FinishedMovement {
	stopped := make(map[int64]int64) // unitID → movementID
	for _, u := range state.blockedRoutes(nowMs) {
		m, _ := state.Movement(u.ID)
		state.MoveUnitTo(u, m.PositionAt(nowMs))
		state.ClearMovement(u.ID)
		u.Status = unit.StatusIdle
		state.MarkDirty(u.ID)
		stopped[u.ID] = m.ID
	}

	w := state.World()
	for _, c := range cities {
		f := urbanFootprint(c)
		for _, u := range state.unitsInside(f) {
			to, ok := evictionTile(w, f, u.Tile())
			if !ok {
				log.Error("invariant_violation",
					"inv_id", "INV-UNIT-001", "severity", "CRITICO", "policy", "FAIL_FAST",
					"entity", fmt.Sprintf("unit:%d", u.ID),
					"detail", fmt.Sprintf("al arrancar, la muralla de la ciudad %d deja la unidad en %s sin tile libre alrededor",
						c.ID, u.Tile()))
				continue
			}
			state.MoveUnitTo(u, to)
			state.MarkDirty(u.ID)
		}
	}

	var finished []FinishedMovement
	state.EachUnit(func(u *unit.Unit) bool {
		if id, ok := stopped[u.ID]; ok {
			finished = append(finished, FinishedMovement{
				MovementID: id, Status: movement.StatusCancelled, UnitID: u.ID, At: u.Tile(),
			})
		}
		return true
	})
	return finished
}
