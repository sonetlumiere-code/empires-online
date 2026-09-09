package simulation

import (
	"encoding/base64"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// BuildSnapshot construye el estado completo del ÁREA DE INTERÉS de una sesión.
//
// Nunca el mundo entero: sólo los chunks suscritos. Ése es el mecanismo que
// permite que el coste por jugador no crezca con el tamaño del mundo ni con el
// número total de jugadores. Ver ../../../../docs/architecture/networking.md
//
// `includeTerrain` permite omitir el terreno cuando el cliente ya lo tiene
// cacheado: el terreno es inmutable, así que sólo hace falta enviarlo una vez.
func (s *Simulation) BuildSnapshot(chunks []world.ChunkCoord, nowMs int64, includeTerrain bool) protocol.WorldSnapshotPayload {
	w := s.state.World()

	payload := protocol.WorldSnapshotPayload{
		ServerTimeMs: nowMs,
		Tick:         s.state.Tick(),
		Chunks:       make([]protocol.ChunkRef, 0, len(chunks)),
		Terrain:      make([]protocol.ChunkTerrain, 0, len(chunks)),
		Units:        make([]protocol.UnitView, 0, 32),
		Cities:       make([]protocol.CityView, 0, 4),
		// RN-TERR-011: los territorios cuya huella intersecta el área de interés
		// viajan en el snapshot inicial, para que el cliente pinte el overlay sin
		// esperar a que algo cambie.
		Territories: s.state.TerritoryViews(chunks),
	}

	for _, ch := range chunks {
		payload.Chunks = append(payload.Chunks, protocol.ChunkRef{CX: ch.CX, CY: ch.CY})

		if includeTerrain {
			if terrain, err := w.ChunkTerrain(ch.CX, ch.CY); err == nil {
				payload.Terrain = append(payload.Terrain, protocol.ChunkTerrain{
					CX:      ch.CX,
					CY:      ch.CY,
					Size:    w.ChunkSize(),
					Terrain: base64.StdEncoding.EncodeToString(terrain),
				})
			}
		}

		for _, u := range s.state.UnitsInChunk(ch.CX, ch.CY) {
			payload.Units = append(payload.Units, s.UnitView(u, nowMs))
		}
		for _, c := range s.state.CitiesInChunk(ch.CX, ch.CY) {
			payload.Cities = append(payload.Cities, CityView(c))
		}
	}

	return payload
}

// UnitView proyecta una unidad al contrato de red, resolviendo su posición
// autoritativa y adjuntando su movimiento activo si lo tiene.
func (s *Simulation) UnitView(u *unit.Unit, nowMs int64) protocol.UnitView {
	view := protocol.UnitView{
		ID:       u.ID,
		PlayerID: u.PlayerID.String(),
		CityID:   u.CityID,
		UnitType: string(u.Type),
		X:        u.X,
		Y:        u.Y,
		HP:       u.HP,
		MaxHP:    u.MaxHP,
		Status:   string(u.Status),
	}
	if m, ok := s.state.Movement(u.ID); ok {
		pos := m.PositionAt(nowMs)
		view.X, view.Y = pos.X, pos.Y
		am := toActiveMovement(m)
		view.Movement = &am
	}
	return view
}

// CityView proyecta una ciudad al contrato de red.
func CityView(c *city.City) protocol.CityView {
	view := protocol.CityView{
		ID:              c.ID,
		OwnerPlayerID:   c.OwnerPlayerID.String(),
		Name:            c.Name,
		CenterX:         c.CenterX,
		CenterY:         c.CenterY,
		Era:             string(c.Era),
		Population:      c.Population,
		PopulationLimit: c.PopulationLimit,
		PresenceState:   string(c.PresenceState),
	}
	if c.ProtectionUntil != nil {
		ms := c.ProtectionUntil.UnixMilli()
		view.ProtectionUntilMs = &ms
	}
	return view
}
