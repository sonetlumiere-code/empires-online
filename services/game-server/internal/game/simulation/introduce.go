package simulation

import (
	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// IntroducePlayer incorpora al mundo vivo un jugador recién creado.
//
// El alta se escribe primero en PostgreSQL, dentro de una transacción, y sólo
// después se introduce en la simulación por esta vía. El orden importa: si la
// transacción falla, el mundo en RAM nunca llega a conocer a ese jugador, y no
// queda un fantasma que la base de datos desconoce.
type IntroducePlayer struct {
	City  *city.City
	Units []*unit.Unit
	// BlockedMin/Max es la zona urbana amurallada que pasa a ser intransitable.
	BlockedMinX, BlockedMinY int32
	BlockedMaxX, BlockedMaxY int32
}

func (IntroducePlayer) commandType() string { return "player.introduce" }

func (s *Simulation) handleIntroducePlayer(cmd IntroducePlayer) {
	nowMs := s.deps.Clock.NowMs()
	w := s.state.World()

	if cmd.City != nil {
		s.state.AddCity(cmd.City)
		// La zona amurallada se marca en la capa de OCUPACIÓN, no en el terreno:
		// el terreno de debajo sigue siendo lo que era, y demoler la ciudad algún
		// día devolverá el tile a su estado original sin inventar nada.
		w.SetBlocked(cmd.BlockedMinX, cmd.BlockedMinY, cmd.BlockedMaxX, cmd.BlockedMaxY, true)

		cx, cy := w.ChunkOf(cmd.City.CenterX, cmd.City.CenterY)
		s.deps.Broadcaster.BroadcastChunk(cx, cy, protocol.TypeCityUpdate,
			protocol.CityUpdatePayload{
				ID:              cmd.City.ID,
				Name:            &cmd.City.Name,
				Population:      &cmd.City.Population,
				PopulationLimit: &cmd.City.PopulationLimit,
			})
	}

	for _, u := range cmd.Units {
		s.state.AddUnit(u)
		cx, cy := w.ChunkOf(u.X, u.Y)
		s.deps.Broadcaster.BroadcastChunk(cx, cy, protocol.TypeEntitySpawn,
			protocol.EntitySpawnPayload{Unit: s.UnitView(u, nowMs)})
	}

	s.deps.Log.Info("jugador incorporado al mundo",
		"city_id", cityIDOf(cmd.City), "units", len(cmd.Units))
}

func cityIDOf(c *city.City) int64 {
	if c == nil {
		return 0
	}
	return c.ID
}

// CityCenters devuelve los centros de todas las ciudades. Lo usa la búsqueda de
// emplazamiento para respetar la separación mínima entre asentamientos.
func (s *Simulation) CityCenters() []struct{ X, Y int32 } {
	out := make([]struct{ X, Y int32 }, 0)
	s.state.EachCity(func(c *city.City) bool {
		out = append(out, struct{ X, Y int32 }{c.CenterX, c.CenterY})
		return true
	})
	return out
}
