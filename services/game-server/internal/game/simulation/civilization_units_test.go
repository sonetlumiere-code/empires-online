package simulation_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/economy"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/production"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// Tests de simulación de SPEC-CIVILIZATION-UNITS (§9) y ADR-043. Mundo de
// newHarness; el catálogo de civilizaciones es el de catalogoDeCivilizaciones,
// con la unidad de cada una (000024).

// paraEntrenarExclusiva deja la ciudad del jugador en la Edad de Bronce, con su
// civilización y lo justo para pagar una unidad exclusiva: 80 de madera, 40 de
// piedra y 10 de su bien (y 10 de pieles, que no deben servir a un romano).
func paraEntrenarExclusiva(h *harness, civ string, bien economy.Resource) {
	h.t.Helper()
	conCivilizacion(h, civ)
	h.ciudad().Era = city.EraBronze
	h.state.SetEconomy(simulation.EconomyLoad{Stocks: []simulation.StockRow{
		{CityID: h.cityID, Stock: economy.Stock{Resource: economy.ResourceWood, Amount: 80, Version: 1}},
		{CityID: h.cityID, Stock: economy.Stock{Resource: economy.ResourceStone, Amount: 40, Version: 1}},
		{CityID: h.cityID, Stock: economy.Stock{Resource: bien, Amount: 10, Version: 1}},
	}})
}

func (h *harness) entrenarTipo(tipo unit.Type) {
	h.t.Helper()
	h.send(simulation.TrainUnit{PlayerID: h.playerID, RequestID: uuid.NewString(), CityID: h.cityID, Type: tipo})
}

func (h *harness) stockDe(r economy.Resource) int64 { return h.state.StockOf(h.cityID, r).Amount }

// RN-CIVUNIT-002 y RN-CIVUNIT-003, golden «legionario»: un romano en Bronce lo
// encarga pagando 80 de madera, 40 de piedra y 10 de vino en un solo encargo
// con tres deltas; se entrena en 31 818 ms (35 000 con production.speed 1100) y
// nace con su tipo y sus 110 de vida.
func TestUnRomanoEntrenaUnLegionario(t *testing.T) {
	h := newHarness(t)
	paraEntrenarExclusiva(h, "ROMAN", economy.ResourceWine)

	h.entrenarTipo(unit.TypeLegionary)
	require.Empty(t, h.codigos())
	assert.Zero(t, h.stockDe(economy.ResourceWood))
	assert.Zero(t, h.stockDe(economy.ResourceStone))
	assert.Zero(t, h.stockDe(economy.ResourceWine))
	assert.Equal(t, int32(4), h.ciudad().Population, "la plaza se reserva al ordenar")
	require.Len(t, h.economy.trains, 1)
	o := h.economy.trains[0]
	assert.Equal(t, unit.TypeLegionary, o.Production.UnitType)
	assert.Equal(t, epoch+31_818, o.Production.CompletesAtMs)
	deltas := map[economy.Resource]int64{}
	for _, d := range o.Stocks {
		deltas[d.Resource] = d.Delta
	}
	assert.Equal(t, map[economy.Resource]int64{
		economy.ResourceWood: -80, economy.ResourceStone: -40, economy.ResourceWine: -10,
	}, deltas, "un delta por recurso, en la misma transacción (INV-ECON-002)")

	h.hasta(epoch + 31_818)
	h.tick()
	spawns := h.rec.byType(protocol.TypeEntitySpawn)
	require.Len(t, spawns, 1)
	nacido := spawns[0].Payload.(protocol.EntitySpawnPayload).Unit
	assert.Equal(t, string(unit.TypeLegionary), nacido.UnitType)
	assert.Equal(t, int32(110), nacido.HP)
	assert.Equal(t, int32(110), nacido.MaxHP)
}

// production.speed sobre la unidad exclusiva: un berserker nórdico tarda 38 889.
func TestUnNordicoEntrenaUnBerserkerMasDespacio(t *testing.T) {
	h := newHarness(t)
	paraEntrenarExclusiva(h, "NORSE", economy.ResourceFurs)
	h.entrenarTipo(unit.TypeBerserker)
	require.Empty(t, h.codigos())
	require.Len(t, h.economy.trains, 1)
	assert.Equal(t, epoch+38_889, h.economy.trains[0].Production.CompletesAtMs)
}

// §5 e INV-UNIT-012: la unidad de otra civilización y la propia antes de
// Bronce son INVALID_TARGET; sin el bien propio, INSUFFICIENT_RESOURCES. Ningún
// rechazo toca el almacén ni la población.
func TestRechazosDeLaUnidadExclusiva(t *testing.T) {
	casos := []struct {
		nombre string
		tipo   unit.Type
		prepar func(h *harness)
		codigo string
	}{
		{"la de otra civilización", unit.TypeBerserker, func(*harness) {}, protocol.CodeInvalidTarget},
		{"la propia en la Edad de Piedra", unit.TypeLegionary, func(h *harness) { h.ciudad().Era = city.EraStone },
			protocol.CodeInvalidTarget},
		{"sin vino, con pieles", unit.TypeLegionary, func(h *harness) {
			h.state.SetEconomy(simulation.EconomyLoad{Stocks: []simulation.StockRow{
				{CityID: h.cityID, Stock: economy.Stock{Resource: economy.ResourceWine, Amount: 9, Version: 1}},
				{CityID: h.cityID, Stock: economy.Stock{Resource: economy.ResourceFurs, Amount: 99, Version: 1}},
			}})
		}, protocol.CodeInsufficientResources},
	}
	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			h := newHarness(t)
			paraEntrenarExclusiva(h, "ROMAN", economy.ResourceWine)
			c.prepar(h)
			antes := h.stockDe(economy.ResourceWood)
			h.entrenarTipo(c.tipo)
			assert.Equal(t, []string{c.codigo}, h.codigos())
			assert.Equal(t, antes, h.stockDe(economy.ResourceWood))
			assert.Equal(t, int32(3), h.ciudad().Population)
			assert.Empty(t, h.economy.trains)
		})
	}
}

// Un encargo de unidad exclusiva que la base rechaza (el trigger de 000024, por
// ejemplo) devuelve los tres recursos y la plaza.
func TestUnaUnidadExclusivaRechazadaDevuelveLosTresRecursos(t *testing.T) {
	h := newHarness(t)
	paraEntrenarExclusiva(h, "ROMAN", economy.ResourceWine)
	h.economy.trainErr = production.ErrNotTrainable
	h.entrenarTipo(unit.TypeLegionary)
	h.tick()
	assert.Equal(t, []string{protocol.CodeInvalidTarget}, h.codigos())
	assert.Equal(t, int64(80), h.stockDe(economy.ResourceWood))
	assert.Equal(t, int64(40), h.stockDe(economy.ResourceStone))
	assert.Equal(t, int64(10), h.stockDe(economy.ResourceWine))
	assert.Equal(t, int32(3), h.ciudad().Population)
	_, _, entrenando := h.state.Production(h.cityID)
	assert.False(t, entrenando)
}

// danosRecibidos ticks hasta que `objetivo` muere o pasan `max` ticks, y
// devuelve la vida que le queda tras cada golpe.
func danosRecibidos(h *harness, u *unit.Unit, max int) []int32 {
	h.t.Helper()
	var vidas []int32
	hp := u.HP
	for i := 0; i < max; i++ {
		h.tick()
		if u.HP != hp {
			hp = u.HP
			vidas = append(vidas, hp)
		}
		if _, vive := h.state.Unit(u.ID); !vive {
			break
		}
	}
	return vidas
}

// Golden «berserker» (§4.2): mata a un guerrero en cinco golpes de 14, que
// responde y le quita 8 por golpe hasta morir.
func TestElBerserkerMataAUnGuerreroEnCincoGolpes(t *testing.T) {
	h := newHarness(t)
	enemigo, _ := conEnemigo(h)
	b := ponerUnidad(h, guerreroID, h.playerID, h.cityID, unit.TypeBerserker, world.Tile{X: 19, Y: 20}, 60)
	g := ponerUnidad(h, enemigoID, enemigo, 2, unit.TypeWarrior, world.Tile{X: 20, Y: 20}, 60)

	h.atacar(guerreroID, enemigoID)
	require.Empty(t, h.codigos())
	require.Equal(t, int32(46), g.HP, "en contacto, el primer golpe cae en el tick de la orden")
	vidas := danosRecibidos(h, g, 200)
	assert.Equal(t, []int32{32, 18, 4, 0}, vidas, "cinco golpes de 14 en total")
	assert.True(t, b.IsAlive(), "el berserker gana el duelo")
	assert.Equal(t, int32(60-4*8), b.HP, "el guerrero responde desde el primer golpe y golpea cuatro veces")
}

// Golden «legionario» (§4.2): un guerrero que ataca a un legionario le quita 8
// por golpe; el legionario responde y lo mata en ocho golpes, con vida de sobra
// (un guerrero necesitaría 14).
func TestElLegionarioAguantaYGanaElDuelo(t *testing.T) {
	h := newHarness(t)
	enemigo, _ := conEnemigo(h)
	g := ponerUnidad(h, guerreroID, h.playerID, h.cityID, unit.TypeWarrior, world.Tile{X: 19, Y: 20}, 60)
	l := ponerUnidad(h, enemigoID, enemigo, 2, unit.TypeLegionary, world.Tile{X: 20, Y: 20}, 110)

	h.atacar(guerreroID, enemigoID)
	require.Empty(t, h.codigos())
	vidas := danosRecibidos(h, g, 200)
	require.Len(t, vidas, 8, "el legionario mata al guerrero en ocho golpes")
	assert.Equal(t, int32(0), vidas[7])
	assert.True(t, l.IsAlive())
	assert.Equal(t, int32(110-8*8), l.HP, "recibió ocho golpes de 8 y le quedan 46")
	_, auto := h.state.Responding(enemigoID)
	assert.False(t, auto, "terminado el enfrentamiento, deja de responder")
}

// Golden «arquero» (§4.2): con alcance 3 golpea sin moverse a un aldeano a
// tres tiles, y se acerca sólo hasta tres tiles de uno a seis.
func TestElArqueroGolpeaDesdeTresTiles(t *testing.T) {
	h := newHarness(t)
	enemigo, _ := conEnemigo(h)
	ponerUnidad(h, guerreroID, h.playerID, h.cityID, unit.TypeHorseArcher, world.Tile{X: 17, Y: 20}, 60)
	v := ponerUnidad(h, enemigoID, enemigo, 2, unit.TypeVillager, world.Tile{X: 20, Y: 20}, 40)

	h.atacar(guerreroID, enemigoID)
	require.Empty(t, h.codigos())
	_, persigue := h.state.Movement(guerreroID)
	assert.False(t, persigue, "a tres tiles ya está en alcance")
	h.tick()
	assert.Equal(t, int32(32), v.HP, "primer golpe en el contacto, a distancia")

	lejos := newHarness(t)
	enemigo, _ = conEnemigo(lejos)
	ponerUnidad(lejos, guerreroID, lejos.playerID, lejos.cityID, unit.TypeHorseArcher, world.Tile{X: 14, Y: 20}, 60)
	ponerUnidad(lejos, enemigoID, enemigo, 2, unit.TypeVillager, world.Tile{X: 20, Y: 20}, 40)
	a, _ := lejos.state.Unit(guerreroID)
	vl, _ := lejos.state.Unit(enemigoID)
	lejos.atacar(guerreroID, enemigoID)
	require.Empty(t, lejos.codigos())
	_, va := lejos.state.Movement(guerreroID)
	require.True(t, va, "a seis tiles, persigue")
	require.True(t, lejos.tickHasta(100, func() bool { return vl.HP < 40 }))
	_, sigue := lejos.state.Movement(guerreroID)
	assert.False(t, sigue, "al entrar en alcance deja de moverse")
	assert.Equal(t, world.Tile{X: 17, Y: 20}, a.Tile(), "se para a tres tiles del objetivo")
}

// RN-COMBAT-012 con RN-CIVUNIT-005: una unidad exclusiva parada responde sola al
// golpe sobre un aldeano cercano de su dueño; el arquero, desde donde está si el
// incursor queda en su alcance.
func TestUnaUnidadExclusivaRespondeSola(t *testing.T) {
	for _, tipo := range []unit.Type{unit.TypeLegionary, unit.TypeCataphract, unit.TypeHorseArcher, unit.TypeBerserker} {
		t.Run(string(tipo), func(t *testing.T) {
			h := newHarness(t)
			enemigo, _ := conEnemigo(h)
			def, err := unit.Lookup(tipo)
			require.NoError(t, err)
			g := ponerUnidad(h, guerreroID, h.playerID, h.cityID, unit.TypeWarrior, world.Tile{X: 19, Y: 20}, 60)
			ponerUnidad(h, enemigoID, enemigo, 2, unit.TypeVillager, world.Tile{X: 20, Y: 20}, 40)
			puesto := world.Tile{X: 22, Y: 20}
			ponerUnidad(h, guardiaID, enemigo, 2, tipo, puesto, def.MaxHP)

			h.atacar(guerreroID, enemigoID)
			h.tick()
			objetivo, ok := h.state.Engaged(guardiaID)
			require.True(t, ok, "el golpe despierta a la unidad exclusiva")
			assert.Equal(t, guerreroID, objetivo)
			_, auto := h.state.Responding(guardiaID)
			assert.True(t, auto)
			if tipo == unit.TypeHorseArcher {
				_, mueve := h.state.Movement(guardiaID)
				assert.False(t, mueve, "el incursor está a tres tiles: el arquero no se mueve")
				h.tick()
				assert.Less(t, g.HP, int32(60), "y le golpea desde su puesto")
			}
		})
	}
}

// unit.move_speed sobre el catafracto: cinco pasos de hierba a 450 ms, 2 250; a
// 474 si es bizantino (2 370), más deprisa que el guerrero (3 000).
func TestElCatafractoEsRapido(t *testing.T) {
	for _, c := range []struct {
		civ     string
		llegada int64
	}{{"", 2_250}, {"BYZANTINE", 2_370}} {
		t.Run(c.civ, func(t *testing.T) {
			h := newHarness(t)
			if c.civ != "" {
				conCivilizacion(h, c.civ)
			}
			ponerUnidad(h, guerreroID, h.playerID, h.cityID, unit.TypeCataphract, world.Tile{X: 12, Y: 20}, 80)
			h.send(simulation.MoveUnit{PlayerID: h.playerID, RequestID: uuid.NewString(), UnitID: guerreroID,
				Target: world.Tile{X: 17, Y: 20}})
			require.Empty(t, h.codigos())
			started := h.rec.byType(protocol.TypeUnitMovementStarted)
			require.Len(t, started, 1)
			assert.Equal(t, epoch+c.llegada, started[0].Payload.(protocol.UnitMovementStartedPayload).Movement.ArrivalTimeMs)
		})
	}
}

// RN-FAC-008 sin bono propio (Q8): un legionario en su tierra recibe 6 por golpe
// de guerrero, como cualquier unidad; con 110 de vida aguanta 19.
func TestElLegionarioEnTierraAfinRecibeComoTodos(t *testing.T) {
	h, _ := conFacciones(t)
	victima := conVecinos(h, 1)[0]
	conTierra(t, h, victima)
	ponerUnidad(h, guerreroID, h.playerID, h.cityID, unit.TypeWarrior, world.Tile{X: 24, Y: 25}, 60)
	l := ponerUnidad(h, enemigoID, victima, 2, unit.TypeLegionary, world.Tile{X: 25, Y: 25}, 110)
	assert.Equal(t, int32(6), primerGolpe(t, h, l))
	assert.Equal(t, int32(104), l.HP)
}

// Golden «berserker asedia» (§4.2): derriba una casa de 150 en once golpes de 14.
func TestElBerserkerDerribaUnaCasaEnOnceGolpes(t *testing.T) {
	h := newHarness(t)
	h.conectar()
	enemigo, c := conEnemigo(h)
	_ = enemigo
	conCasa(h, c, casaID, tileCasa, true)
	ponerUnidad(h, guerreroID, h.playerID, h.cityID, unit.TypeBerserker, world.Tile{X: 20, Y: 30}, 60)

	h.derribar(guerreroID, casaID)
	require.Empty(t, h.codigos())
	golpes := 0
	vida := int32(150)
	require.True(t, h.tickHasta(600, func() bool {
		hp, enPie := vidaDe(h, casaID)
		if !enPie {
			hp = 0
		}
		if hp != vida {
			vida = hp
			golpes++
		}
		return !enPie
	}))
	assert.Equal(t, 11, golpes)
}

// El arquero asedia desde su alcance: se para a tres tiles de la casa, no
// junto a ella, y la golpea desde allí.
func TestElArqueroAsediaDesdeTresTiles(t *testing.T) {
	h := newHarness(t)
	h.conectar()
	_, c := conEnemigo(h)
	conCasa(h, c, casaID, tileCasa, true)
	ponerUnidad(h, guerreroID, h.playerID, h.cityID, unit.TypeHorseArcher, world.Tile{X: 20, Y: 30}, 60)

	h.derribar(guerreroID, casaID)
	require.Empty(t, h.codigos())
	m, va := h.state.Movement(guerreroID)
	require.True(t, va)
	d := max(abs32(m.Target.X-tileCasa.X), abs32(m.Target.Y-tileCasa.Y))
	assert.Equal(t, int32(3), d, "el tile de asedio está a tres tiles de la casa")
	require.True(t, h.tickHasta(200, func() bool { hp, _ := vidaDe(h, casaID); return hp < 150 }))
}

// El saqueo pide estar junto a la muralla (RN-COMBAT-019): el arquero se acerca
// hasta ella aunque su alcance sea 3, y carga 20 como el guerrero.
func TestElArqueroSaqueaJuntoALaMuralla(t *testing.T) {
	h := newHarness(t)
	_, ciudad, _ := conSaqueo(h, 200, world.Tile{X: 20, Y: 10})
	ponerUnidad(h, guerreroID+1, h.playerID, h.cityID, unit.TypeHorseArcher, world.Tile{X: 20, Y: 30}, 60)
	h.saquear(guerreroID+1, ciudad.ID)
	require.Empty(t, h.codigos())
	m, va := h.state.Movement(guerreroID + 1)
	require.True(t, va)
	assert.True(t, ciudad.IsAdjacent(m.Target.X, m.Target.Y), "va al anillo de la muralla: %v", m.Target)
}

// RN-CIVUNIT-005 y ADR-043 §6: todo tipo que ataca puede atacar, saquear,
// derribar y plantar el estandarte; ninguno que no ataca puede. Recorre el
// catálogo entero para que la próxima unidad herede lo mismo.
func TestTodaUnidadQueAtacaPuedeHacerLoQueHaceElGuerrero(t *testing.T) {
	for _, tipo := range unit.Types() {
		def, err := unit.Lookup(tipo)
		require.NoError(t, err)
		if def.Autonomous {
			continue // la caravana no admite órdenes de nadie
		}
		t.Run(string(tipo), func(t *testing.T) {
			ordenes := map[string]func(h *harness, c *city.City){
				"atacar":   func(h *harness, _ *city.City) { h.atacar(guerreroID, enemigoID) },
				"saquear":  func(h *harness, c *city.City) { h.saquear(guerreroID, c.ID) },
				"derribar": func(h *harness, _ *city.City) { h.derribar(guerreroID, casaID) },
			}
			for nombre, orden := range ordenes {
				h := newHarness(t)
				h.conectar()
				enemigo, c := conEnemigo(h)
				h.maderaDe(c.ID, 200)
				conCasa(h, c, casaID, tileCasa, true)
				ponerUnidad(h, enemigoID, enemigo, 2, unit.TypeVillager, world.Tile{X: 25, Y: 20}, 40)
				ponerUnidad(h, guerreroID, h.playerID, h.cityID, tipo, world.Tile{X: 20, Y: 20}, def.MaxHP)
				orden(h, c)
				if def.CanAttack() {
					assert.Empty(t, h.codigos(), "%s %s", tipo, nombre)
				} else {
					assert.NotEmpty(t, h.codigos(), "%s no debe poder %s", tipo, nombre)
				}
			}
		})
	}
}

// RN-CIVUNIT-005 en la conquista (RN-CONQ-001): un legionario planta el
// estandarte en un territorio libre como el guerrero.
func TestUnLegionarioPlantaElEstandarte(t *testing.T) {
	h := newHarness(t)
	conCuatroTerritorios(h, map[int64]uuid.UUID{1: h.playerID})
	l := ponerUnidad(h, guerreroID, h.playerID, h.cityID, unit.TypeLegionary, world.Tile{X: 40, Y: 10}, 110)

	h.reclamar(guerreroID)
	require.Empty(t, h.codigos())
	h.tick()
	_, confirmada, ok := h.state.ActiveClaim(2)
	require.True(t, ok)
	assert.True(t, confirmada)
	assert.Equal(t, unit.StatusClaiming, l.Status)
}

// RN-CIVUNIT-005 en las caravanas (RN-CARAVAN-006): un berserker de un tercero
// mata la caravana y la captura para su ciudad, con la carga entera.
func TestUnBerserkerCapturaUnaCaravana(t *testing.T) {
	h := newHarness(t)
	tratoEnCamino(h)
	tercero := conTercero(h)
	m := h.movimientoDe(caravanaDeMadera)
	junto := m.Path[6].Tile()
	ponerUnidad(h, asaltanteID, tercero, 3, unit.TypeBerserker, world.Tile{X: junto.X + 1, Y: junto.Y}, 60)

	h.send(simulation.AttackUnit{PlayerID: tercero, RequestID: uuid.NewString(), UnitID: asaltanteID,
		TargetID: caravanaDeMadera})
	require.Empty(t, h.codigos())
	require.True(t, h.tickHasta(1000, func() bool {
		c, ok := h.state.Caravan(caravanaDeMadera)
		return ok && c.ToCity == 3
	}), "el berserker alcanza la caravana y la captura")
	u, ok := h.state.Unit(caravanaDeMadera)
	require.True(t, ok)
	assert.Equal(t, tercero, u.PlayerID)
	c, _ := h.state.Caravan(caravanaDeMadera)
	assert.Equal(t, economy.Costs{madera300()}, c.Cargo, "INV-TRADE-005: la carga no cambia")
}

func abs32(v int32) int32 {
	if v < 0 {
		return -v
	}
	return v
}
