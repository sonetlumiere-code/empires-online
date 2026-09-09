package simulation

import (
	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/territory"
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
	// TerritoryControl es el control que la fundación acaba de cambiar de manos,
	// o nil si el tile central no pertenecía a ningún territorio o el territorio
	// ya tenía dueño (RN-TERR-008: no existe la pérdida de control).
	//
	// Viaja con la incorporación del jugador y no como comando aparte porque es
	// parte del MISMO hecho: se escribió en la misma transacción y no hay ningún
	// instante en que uno sea cierto y el otro no.
	TerritoryControl *territory.Control
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

	if cmd.TerritoryControl != nil {
		s.applyTerritoryControl(*cmd.TerritoryControl)
	}

	s.deps.Log.Info("jugador incorporado al mundo",
		"city_id", cityIDOf(cmd.City), "units", len(cmd.Units))
}

// applyTerritoryControl alinea la copia en RAM y difunde el cambio.
//
// INV-TERR-009: ningún cambio de control ocurre sin emitir territory.update a la
// huella de chunks del territorio. Por eso las dos cosas viven en la misma
// función y no en dos sitios que alguien pueda desincronizar.
func (s *Simulation) applyTerritoryControl(c territory.Control) {
	s.state.ApplyTerritoryControl(c)

	t, ok := s.state.territories.ByID(c.TerritoryID)
	if !ok {
		// El control referencia un territorio que el índice no conoce: es
		// INV-TERR-003 roto. No se difunde nada porque no hay huella que usar.
		s.deps.Log.Error("control de un territorio inexistente",
			"territory_id", c.TerritoryID)
		return
	}

	// Una sola emisión por sesión, aunque la huella abarque varios chunks
	// (RN-TERR-012).
	s.deps.Broadcaster.BroadcastChunks(
		s.state.TerritoryFootprint(c.TerritoryID),
		protocol.TypeTerritoryUpdate,
		protocol.TerritoryUpdatePayload{Territory: s.state.TerritoryView(t)},
	)

	s.deps.Log.Info("control de territorio cambiado",
		"territory_id", c.TerritoryID, "name", t.Name,
		"owner_type", string(c.OwnerType), "version", c.Version)
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
