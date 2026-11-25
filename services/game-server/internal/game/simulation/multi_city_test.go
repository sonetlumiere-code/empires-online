package simulation_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/cityfall"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/economy"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/founding"
	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// SPEC-MULTI-CITY: la fundación de una segunda ciudad, la capital y la caída de
// una de dos.

const fundadorID = int64(400)

// sitioDeFundacion es el tile del aldeano fundador: a 30 tiles de la capital
// (10,10), con un bosque pintado al lado (RN-CITY-023).
var sitioDeFundacion = world.Tile{X: 40, Y: 40}

// escenarioDeFundacion monta el harness con un bosque junto al sitio, la ciudad
// del jugador en Bronce con 800 de madera y 400 de piedra, el aldeano fundador
// en el sitio y el almacén de la caída y la fundación.
func escenarioDeFundacion(t *testing.T, ajustar func(*simulation.Deps)) (*harness, *fakeCityFall) {
	t.Helper()
	store := &fakeCityFall{}
	bosque := func(terrain []byte) {
		for y := int32(38); y <= 42; y++ {
			for x := int32(44); x <= 47; x++ {
				terrain[int(y)*64+int(x)] = byte(world.Forest)
			}
		}
	}
	var mundo *world.World
	h := newHarnessConDeps(t, bosque, func(d *simulation.Deps) {
		d.CityFall = store
		// La cola comprueba el sitio con founding.CheckSite (sus tests, en
		// internal/game/founding), aquí sin capa de madera.
		d.FoundSite = func(_ context.Context, center world.Tile, existing []world.Tile) (cityfall.Site, error) {
			s, err := founding.CheckSite(mundo, center, existing, cityfall.FoundVillagers, nil)
			if err != nil {
				return cityfall.Site{}, err
			}
			return cityfall.Site{Center: s.Center, Spawns: s.Spawns}, nil
		}
		if ajustar != nil {
			ajustar(d)
		}
	})
	mundo = h.state.World()
	store.centers = func() []world.Tile { return []world.Tile{{X: 10, Y: 10}} }
	h.ciudad().Era = city.EraBronze
	h.ciudad().Civilization, h.ciudad().Faction = "NORSE", "CHAOS"
	h.state.SetEconomy(simulation.EconomyLoad{Stocks: []simulation.StockRow{
		{CityID: h.cityID, Stock: economy.Stock{Resource: economy.ResourceWood, Amount: 800, Version: 1}},
		{CityID: h.cityID, Stock: economy.Stock{Resource: economy.ResourceStone, Amount: 400, Version: 1}},
	}})
	ponerUnidad(h, fundadorID, h.playerID, h.cityID, unit.TypeVillager, sitioDeFundacion, 40)
	h.conectar()
	return h, store
}

func (h *harness) fundarCiudad(unitID int64) {
	h.t.Helper()
	h.send(simulation.FoundCity{PlayerID: h.playerID, RequestID: uuid.NewString(), UnitID: unitID})
}

// RN-CITY-030 a RN-CITY-033: el aldeano funda en su tile, paga su ciudad, la
// nueva nace con el nombre de la capital y « II», en su era, con el fundador y
// dos aldeanos más; la capital sigue siendo la primera.
func TestUnAldeanoFundaLaSegundaCiudad(t *testing.T) {
	var vistaEn []world.Tile
	h, store := escenarioDeFundacion(t, func(d *simulation.Deps) {
		d.ResnapshotAt = func(_ uuid.UUID, center world.Tile) { vistaEn = append(vistaEn, center) }
	})

	h.fundarCiudad(fundadorID)
	require.Len(t, store.founds, 1, "la fundación se encola")
	f := store.founds[0].Founding
	assert.Equal(t, h.playerID, f.OwnerID)
	assert.Equal(t, h.cityID, f.FromCityID)
	assert.Equal(t, fundadorID, f.UnitID)
	assert.Equal(t, "Testópolis II", f.Name)
	assert.Equal(t, city.EraBronze, f.Era, "nace en la era de la ciudad que la funda")
	assert.Zero(t, h.stockDe(economy.ResourceWood), "lo pagado queda reservado")
	assert.Zero(t, h.stockDe(economy.ResourceStone))

	h.tick() // vuelve la fundación por el buzón
	ciudades := h.state.CitiesOf(h.playerID)
	require.Len(t, ciudades, 2, "INV-CITY-022: dos ciudades en pie")
	capital, _ := h.state.CityOf(h.playerID)
	assert.Equal(t, h.cityID, capital.ID, "RN-CITY-033: la capital es la de id menor")
	nueva := ciudades[1]
	assert.Equal(t, int64(77), nueva.ID)
	assert.Equal(t, sitioDeFundacion, nueva.Center())
	assert.Equal(t, "NORSE", nueva.Civilization, "lo del jugador se copia de la capital")
	assert.Equal(t, "CHAOS", nueva.Faction)
	assert.False(t, h.state.IsCapital(nueva))
	fundador, _ := h.state.Unit(fundadorID)
	require.NotNil(t, fundador.CityID)
	assert.Equal(t, nueva.ID, *fundador.CityID, "el fundador pasa a la ciudad nueva")
	assert.NotEqual(t, sitioDeFundacion, fundador.Tile(), "y sale de su zona urbana (RN-CITY-022)")
	for _, id := range []int64{950, 951} {
		u, ok := h.state.Unit(id)
		require.True(t, ok, "los dos aldeanos nuevos")
		assert.Equal(t, nueva.ID, *u.CityID)
	}
	assert.Equal(t, int32(2), h.ciudad().Population, "la que paga pierde al fundador")
	assert.Zero(t, h.stockDe(economy.ResourceWood), "el cobro se queda")
	assert.Equal(t, []world.Tile{sitioDeFundacion}, vistaEn, "el snapshot del fundador, centrado en la ciudad nueva (§9.1)")

	// RN-CITY-031: una tercera no.
	h.rec.reset()
	ponerUnidad(h, fundadorID+1, h.playerID, h.cityID, unit.TypeVillager, world.Tile{X: 40, Y: 10}, 40)
	h.fundarCiudad(fundadorID + 1)
	code, _ := errorDe(t, h)
	assert.Equal(t, protocol.CodeInvalidTarget, code)
	errs := h.rec.byType(protocol.TypeSystemError)
	assert.Contains(t, errs[0].Payload.(protocol.SystemErrorPayload).Message, "dos ciudades", "el motivo es el tope, no el sitio")
	assert.Len(t, store.founds, 1)
}

// RN-CITY-030, RN-CITY-031 y spec §5: cada rechazo de la orden, sin reservar ni
// encolar nada.
func TestLosRechazosDeFundar(t *testing.T) {
	casos := []struct {
		nombre   string
		preparar func(h *harness) int64
		code     string
	}{
		{"no es suya", func(h *harness) int64 {
			otro, c := conEnemigo(h)
			ponerUnidad(h, 500, otro, c.ID, unit.TypeVillager, sitioDeFundacion, 40)
			return 500
		}, protocol.CodeUnitNotOwned},
		{"no es un aldeano", func(h *harness) int64 {
			ponerUnidad(h, 500, h.playerID, h.cityID, unit.TypeWarrior, sitioDeFundacion, 60)
			return 500
		}, protocol.CodeInvalidTarget},
		{"no está parado", func(h *harness) int64 {
			u, _ := h.state.Unit(fundadorID)
			u.Status = unit.StatusWorking
			return fundadorID
		}, protocol.CodeUnitNotMovable},
		{"la ciudad en Piedra", func(h *harness) int64 {
			h.ciudad().Era = city.EraStone
			return fundadorID
		}, protocol.CodeInvalidTarget},
		{"sin recursos", func(h *harness) int64 {
			h.state.SetEconomy(simulation.EconomyLoad{Stocks: []simulation.StockRow{
				{CityID: h.cityID, Stock: economy.Stock{Resource: economy.ResourceWood, Amount: 799, Version: 2}},
				{CityID: h.cityID, Stock: economy.Stock{Resource: economy.ResourceStone, Amount: 400, Version: 2}},
			}})
			return fundadorID
		}, protocol.CodeInsufficientResources},
		{"demasiado cerca de la capital", func(h *harness) int64 {
			u, _ := h.state.Unit(fundadorID)
			u.X, u.Y = 25, 10
			return fundadorID
		}, protocol.CodeInvalidTarget},
		{"sin bosque cerca", func(h *harness) int64 {
			u, _ := h.state.Unit(fundadorID)
			u.X, u.Y = 40, 55
			return fundadorID
		}, protocol.CodeInvalidTarget},
	}
	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			h, store := escenarioDeFundacion(t, nil)
			id := c.preparar(h)
			antes := h.stockDe(economy.ResourceWood)
			h.fundarCiudad(id)
			code, _ := errorDe(t, h)
			assert.Equal(t, c.code, code)
			assert.Empty(t, store.founds, "nada se encola")
			assert.Equal(t, antes, h.stockDe(economy.ResourceWood), "nada se reserva")
		})
	}
}

// ADR-051 §4: si la transacción la rechaza (el tope o el sitio cambiaron en
// vuelo), lo reservado vuelve; mientras va en vuelo, otra orden no sale.
func TestUnaFundacionRechazadaDevuelveLoReservado(t *testing.T) {
	h, store := escenarioDeFundacion(t, nil)
	store.foundErr = cityfall.ErrFoundRejected

	h.fundarCiudad(fundadorID)
	require.Len(t, store.founds, 1)
	assert.Zero(t, h.stockDe(economy.ResourceWood))
	h.tick()
	code, _ := errorDe(t, h)
	assert.Equal(t, protocol.CodeInvalidTarget, code)
	assert.Equal(t, int64(800), h.stockDe(economy.ResourceWood), "INV-ECON-002: vuelve lo reservado")
	assert.Equal(t, int64(400), h.stockDe(economy.ResourceStone))
	assert.Len(t, h.state.CitiesOf(h.playerID), 1)
}

// RN-CITY-034: la presencia es del jugador; las dos ciudades cambian a la vez.
func TestLaPresenciaCambiaEnLasDosCiudades(t *testing.T) {
	h, _ := escenarioDeFundacion(t, nil)
	h.fundarCiudad(fundadorID)
	h.tick()
	ciudades := h.state.CitiesOf(h.playerID)
	require.Len(t, ciudades, 2)

	h.send(simulation.PlayerDisconnected{PlayerID: h.playerID, SessionID: sesion})
	h.advance(31 * time.Second)
	for _, c := range ciudades {
		assert.Equal(t, city.PresenceOfflinePending, c.PresenceState, "ciudad %d", c.ID)
	}
	h.advance(300 * time.Second)
	for _, c := range ciudades {
		assert.Equal(t, city.PresenceProtected, c.PresenceState, "ciudad %d", c.ID)
	}
	h.conectar()
	for _, c := range ciudades {
		assert.Equal(t, city.PresenceOnline, c.PresenceState, "ciudad %d", c.ID)
	}
}

// RN-CITY-035, INV-CITY-023: con otra ciudad en pie, la caída no refunda; lo
// suyo pasa a la que queda, que es la capital.
func TestUnaCiudadQueCaeConOtraEnPieNoRefunda(t *testing.T) {
	h, store, enemigo, ciudad := escenarioDeCaida(t, nil)
	otra := &city.City{
		ID: 8, OwnerPlayerID: enemigo, Name: "Vecina II", CenterX: 50, CenterY: 10, Era: city.EraBronze,
		Population: 3, PopulationLimit: 20, PresenceState: city.PresenceOnline,
	}
	h.state.AddCity(otra)
	store.keep = otra.ID
	capital, _ := h.state.CityOf(enemigo)
	require.Equal(t, ciudad.ID, capital.ID, "la que se asalta es la capital (id menor)")

	h.asaltar(sitiadorID, ciudad.ID)
	h.tick()
	a, _, ok := h.state.ActiveAssault(ciudad.ID)
	require.True(t, ok)
	h.hasta(a.EndsAtMs)
	require.Equal(t, 1, store.fallCount())
	h.tick() // vuelve la caída

	_, sigue := h.state.City(ciudad.ID)
	assert.False(t, sigue, "la ciudad caída sale del mundo")
	queda, ok := h.state.CityOf(enemigo)
	require.True(t, ok, "INV-CITY-020: le queda una")
	assert.Equal(t, otra.ID, queda.ID, "la que queda es ahora la capital")
	assert.Len(t, h.state.CitiesOf(enemigo), 1, "no hay refundada")
	_, refundada := h.state.City(99)
	assert.False(t, refundada)
	aldeano, _ := h.state.Unit(enemigoID)
	require.NotNil(t, aldeano.CityID)
	assert.Equal(t, otra.ID, *aldeano.CityID, "las unidades de fuera pasan a la que queda")
	assert.Equal(t, int32(4), queda.Population)
	assert.Equal(t, int32(25), queda.PopulationLimit, "con el bono de territorio, si pasa a ser la capital")
	for i := int64(900); i < 903; i++ {
		_, ok := h.state.Unit(i)
		assert.False(t, ok, "sin aldeanos de refundación")
	}
}
