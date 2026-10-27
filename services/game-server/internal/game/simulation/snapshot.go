package simulation

import (
	"encoding/base64"

	"github.com/google/uuid"

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
// `viewer` es el jugador que recibirá el snapshot. Un snapshot es un delta
// completo, y por eso aplica el mismo filtro de visibilidad que los deltas: una
// unidad oculta ajena que reapareciera al reconectar sería la misma fuga por
// otra puerta (INV-SAFE-004).
//
// `includeTerrain` permite omitir el terreno cuando el cliente ya lo tiene
// cacheado: el terreno es inmutable, así que sólo hace falta enviarlo una vez.
func (s *Simulation) BuildSnapshot(viewer uuid.UUID, chunks []world.ChunkCoord, nowMs int64, includeTerrain bool) protocol.WorldSnapshotPayload {
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
		Buildings:   make([]protocol.BuildingView, 0, 8),
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
			if !VisibleTo(u, viewer) {
				continue
			}
			payload.Units = append(payload.Units, s.UnitView(u, nowMs))
		}
		for _, c := range s.state.CitiesInChunk(ch.CX, ch.CY) {
			payload.Cities = append(payload.Cities, s.state.cityViewOf(c))
		}
		for _, b := range s.state.buildingsInChunk(ch.CX, ch.CY) {
			payload.Buildings = append(payload.Buildings, BuildingView(b))
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
	if cityID, ok := s.state.GarrisonCity(u.ID); ok {
		view.GarrisonCityID = &cityID
	}
	if e, ok := s.state.tasks[u.ID]; ok {
		view.Carrying = e.Carrying
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

// VisibleTo decide si una unidad viaja hacia un jugador concreto.
//
// Una unidad HIDDEN o GARRISONED sólo la ve su propietario (INV-SAFE-004,
// INV-GARR-006). Son las únicas reglas de visibilidad por observador del MVP;
// el resto la decide el chunk.
func VisibleTo(u *unit.Unit, viewer uuid.UUID) bool {
	return u.PlayerID == viewer || visibleToThirdParties(u.Status)
}

// visibleToThirdParties dice si un estado deja ver la unidad a quien no es su
// propietario. HIDDEN y GARRISONED son los dos únicos que no (INV-SAFE-004,
// INV-GARR-006). Es la misma regla que VisibleTo, expresada sobre el estado
// para poder preguntar por el de ANTES de una operación, no sólo por el actual.
func visibleToThirdParties(st unit.Status) bool {
	return st != unit.StatusHidden && st != unit.StatusGarrisoned
}
