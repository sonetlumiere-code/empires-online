package simulation

import (
	"github.com/empires-online/empires-online/services/game-server/internal/domain/territory"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// SetTerritories instala la geometría y el control vigente en el estado.
//
// Se llama una sola vez, durante la hidratación: la geometría es inmutable en
// runtime (spec §10) y sólo cambia el control.
func (s *State) SetTerritories(set *territory.Set, controls []territory.Control) {
	s.territories = set
	s.control = make(map[int64]territory.Control, len(controls))
	for _, c := range controls {
		s.control[c.TerritoryID] = c
	}
}

// TerritoryAt resuelve el territorio que contiene el tile, si hay alguno.
func (s *State) TerritoryAt(x, y int32) (territory.Territory, bool) {
	return s.territories.TerritoryAt(x, y)
}

// TerritoryControl devuelve el control vigente de un territorio.
func (s *State) TerritoryControl(id int64) (territory.Control, bool) {
	c, ok := s.control[id]
	return c, ok
}

// ApplyTerritoryControl instala un control nuevo en RAM.
//
// La escritura durable ya ocurrió: esto sólo alinea la copia en memoria con lo
// que la base de datos ya confirmó, nunca al revés.
func (s *State) ApplyTerritoryControl(c territory.Control) {
	if s.control == nil {
		s.control = make(map[int64]territory.Control, 1)
	}
	s.control[c.TerritoryID] = c
}

// TerritoryFootprint devuelve los chunks que solapa un territorio.
func (s *State) TerritoryFootprint(id int64) []world.ChunkCoord {
	return s.territories.FootprintOf(id)
}

// TerritoryViews devuelve la vista de red de los territorios que intersectan
// esos chunks, en orden ascendente de id.
//
// Es lo que viaja en `world.snapshot.payload.territories[]` (RN-TERR-011).
func (s *State) TerritoryViews(chunks []world.ChunkCoord) []protocol.TerritoryView {
	found := s.territories.InChunks(chunks)
	out := make([]protocol.TerritoryView, 0, len(found))
	for _, t := range found {
		out = append(out, s.TerritoryView(t))
	}
	return out
}

// TerritoryView proyecta un territorio y su control al contrato de red.
//
// La geometría viaja en CADA mensaje (RN-TERR-014). Es redundante, pequeña y
// constante, y evita que el cliente mantenga un catálogo aparte con su propio
// problema de invalidación.
func (s *State) TerritoryView(t territory.Territory) protocol.TerritoryView {
	v := protocol.TerritoryView{
		ID:   t.ID,
		Name: t.Name,
		MinX: t.MinX, MinY: t.MinY, MaxX: t.MaxX, MaxY: t.MaxY,
		OwnerType: string(territory.OwnerNone),
	}
	if c, ok := s.control[t.ID]; ok {
		v.OwnerType = string(c.OwnerType)
		v.OwnerID = c.OwnerID
		v.Contested = c.Contested
	}
	return v
}
