package simulation_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/building"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// SPEC-CITY-TAKEN RN-CITY-036, RN-CITY-037, INV-CITY-024: el vencedor con una
// sola ciudad pide la toma con los aldeanos de la tomada; la RAM quita la caída,
// mete la tomada del vencedor en el mismo centro con su casa completa y sus tres
// aldeanos, destruye la obra, y city.fallen dice qué ciudad la tomó.
func TestUnaCiudadQueCaeConHuecoLaTomaElVencedor(t *testing.T) {
	h, store, enemigo, ciudad := escenarioDeCaida(t, nil)
	store.take = true
	store.takeBuildings = []int64{501}
	h.state.SetEconomy(simulation.EconomyLoad{Buildings: []building.Building{
		{ID: 501, CityID: ciudad.ID, Type: building.TypeHouse, X: ciudad.CenterX + 3, Y: ciudad.CenterY,
			Status: building.StatusComplete, CompletesAtMs: h.clk.NowMs()},
		{ID: 502, CityID: ciudad.ID, Type: building.TypeHouse, X: ciudad.CenterX - 3, Y: ciudad.CenterY,
			Status: building.StatusUnderConstruction, CompletesAtMs: h.clk.NowMs() + 999_999},
	}})

	h.asaltar(sitiadorID, ciudad.ID)
	h.tick()
	a, _, ok := h.state.ActiveAssault(ciudad.ID)
	require.True(t, ok)
	h.hasta(a.EndsAtMs)
	require.Equal(t, 1, store.fallCount())
	assert.Len(t, store.falls[0].TakeSpawns, 3, "con hueco, la caída pide los tres aldeanos de la tomada")
	h.tick()

	_, sigue := h.state.City(ciudad.ID)
	assert.False(t, sigue, "la caída sale del mundo")
	tomada, ok := h.state.City(120)
	require.True(t, ok, "la tomada entra en el mundo")
	assert.Equal(t, h.playerID, tomada.OwnerPlayerID)
	assert.Equal(t, ciudad.Center(), tomada.Center())
	assert.Equal(t, ciudad.Name, tomada.Name)
	capital, _ := h.state.CityOf(h.playerID)
	assert.Equal(t, capital.Civilization, tomada.Civilization, "lo del jugador, de la capital del vencedor")
	assert.Len(t, h.state.CitiesOf(h.playerID), 2)
	assert.Equal(t, h.cityID, capital.ID, "la capital del vencedor no cambia")
	casa, ok := h.state.Building(501)
	require.True(t, ok, "la casa completa sigue en pie")
	assert.Equal(t, int64(120), casa.CityID, "y es de la tomada")
	_, obra := h.state.Building(502)
	assert.False(t, obra, "la obra se pierde")
	for i := int64(960); i < 963; i++ {
		u, ok := h.state.Unit(i)
		require.True(t, ok, "los tres aldeanos de la tomada")
		assert.Equal(t, h.playerID, u.PlayerID)
	}
	_, ok = h.state.CityOf(enemigo)
	assert.True(t, ok, "el vencido refunda como siempre (RN-CITY-039)")

	caidas := h.rec.byType(protocol.TypeCityFallen)
	require.NotEmpty(t, caidas)
	p := caidas[0].Payload.(protocol.CityFallenPayload)
	require.NotNil(t, p.TakenCityID)
	assert.Equal(t, int64(120), *p.TakenCityID)
	heredado := false
	for _, m := range h.rec.byType(protocol.TypeBuildingSpawn) {
		if b := m.Payload.(protocol.BuildingSpawnPayload).Building; b.ID == 501 && b.CityID == 120 {
			heredado = true
		}
	}
	assert.True(t, heredado, "la casa heredada se anuncia con su ciudad nueva")
}

// RN-CITY-036: con dos ciudades, el vencedor no pide la toma y la caída arrasa.
func TestSinHuecoLaCaidaArrasa(t *testing.T) {
	h, store, _, ciudad := escenarioDeCaida(t, nil)
	store.take = true
	h.state.AddCity(&city.City{
		ID: 9, OwnerPlayerID: h.playerID, Name: "Mia II", CenterX: 80, CenterY: 80, Era: city.EraBronze,
		Population: 3, PopulationLimit: 20, PresenceState: city.PresenceOnline,
	})
	h.asaltar(sitiadorID, ciudad.ID)
	h.tick()
	a, _, ok := h.state.ActiveAssault(ciudad.ID)
	require.True(t, ok)
	h.hasta(a.EndsAtMs)
	require.Equal(t, 1, store.fallCount())
	assert.Empty(t, store.falls[0].TakeSpawns)
	h.tick()
	_, tomada := h.state.City(120)
	assert.False(t, tomada)
	p := h.rec.byType(protocol.TypeCityFallen)[0].Payload.(protocol.CityFallenPayload)
	assert.Nil(t, p.TakenCityID)
}
