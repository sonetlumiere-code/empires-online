package simulation_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/advance"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/building"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/diplomacy"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/economy"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/eventlog"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// Tests de la Edad de los Castillos (SPEC-CASTLE-AGE §9, ADR-044). Mundo de
// newHarness: la ciudad propia en (10,10); la enemiga de conEnemigo en (20,40),
// con su zona urbana de (19,39) a (21,41). El castillo enemigo va en (21,41) y
// la casa de conAsedio en (20,39), que se golpea desde (19,38): a 3 del castillo.

const castilloID = int64(60)

var tileCastillo = world.Tile{X: 21, Y: 41}

// conCastillo da a una ciudad, ya en la Edad de los Castillos, un castillo
// completo.
func conCastillo(h *harness, c *city.City, id int64, at world.Tile) {
	h.t.Helper()
	c.Era = city.EraCastle
	h.state.SetEconomy(simulation.EconomyLoad{Buildings: []building.Building{{
		ID: id, CityID: c.ID, Type: building.TypeCastle, X: at.X, Y: at.Y,
		Status: building.StatusComplete, CompletesAtMs: h.clk.NowMs(),
	}}})
}

// objetivosDelCastillo son los targetUnitId que difundió un castillo, en orden:
// 0 cuando dejó de apuntar.
func (h *harness) objetivosDelCastillo(id int64) []int64 {
	var out []int64
	for _, m := range h.rec.byType(protocol.TypeBuildingUpdate) {
		p := m.Payload.(protocol.BuildingUpdatePayload)
		if p.ID != id || p.TargetUnitID == nil {
			continue
		}
		if *p.TargetUnitID == nil {
			out = append(out, 0)
		} else {
			out = append(out, **p.TargetUnitID)
		}
	}
	return out
}

// Golden «castillo» (RN-CASTLE-004, RN-CASTLE-006): el guerrero que asedia una
// casa recibe el primer disparo en el tick en que empieza a golpear, uno cada
// 2 000 ms exactos, y muere con el sexto, a los 10 s, tras siete golpes de
// asedio: la casa queda en 94. La muerte la firma el castillo, sin botín, y la
// avisan los dos lados con los tipos del castillo. El objetivo se difunde al
// empezar y se borra cuando no queda nadie a quien disparar.
func TestElCastilloGoldenMataAlGuerreroQueAsedia(t *testing.T) {
	h := newHarness(t)
	enemigo, ciudad, g := conAsedio(h, world.Tile{X: 20, Y: 30})
	conCastillo(h, ciudad, castilloID, tileCastillo)
	hp, maxHP, ok := h.state.BuildingHP(castilloID)
	require.True(t, ok)
	assert.Equal(t, int32(1_000), maxHP)
	assert.Equal(t, int32(1_000), hp)

	h.derribar(guerreroID, casaID)
	require.Empty(t, h.codigos())

	var disparos []int64
	var vidas []int32
	var primerAsedio int64
	vida := g.HP
	require.True(t, h.tickHasta(600, func() bool {
		if casa, _ := vidaDe(h, casaID); casa < 150 && primerAsedio == 0 {
			primerAsedio = h.clk.NowMs()
		}
		if g.HP != vida {
			vida = g.HP
			disparos = append(disparos, h.clk.NowMs())
			vidas = append(vidas, g.HP)
		}
		return !g.IsAlive()
	}))

	require.Len(t, disparos, 6, "60 de vida a 10 por disparo")
	assert.Equal(t, primerAsedio, disparos[0], "el primer disparo cae en el tick del primer golpe de asedio")
	for i := 1; i < len(disparos); i++ {
		assert.Equal(t, int64(2_000), disparos[i]-disparos[i-1], "disparo %d", i)
	}
	assert.Equal(t, []int32{50, 40, 30, 20, 10, 0}, vidas)
	casa, enPie := vidaDe(h, casaID)
	require.True(t, enPie)
	assert.Equal(t, int32(94), casa, "siete golpes de asedio de 8")

	require.Len(t, h.combat.deaths, 1)
	d := h.combat.deaths[0]
	assert.Equal(t, guerreroID, d.VictimID)
	assert.Zero(t, d.KillerID)
	assert.Equal(t, enemigo, d.KillerPlayer)
	assert.Equal(t, castilloID, d.KillerBuildingID)
	assert.Equal(t, building.TypeCastle, d.KillerBuildingType)
	assert.Zero(t, d.Loot)
	assert.Empty(t, h.combat.attacks, "el castillo no escribe UnitAttacked")

	propios := h.avisosPara(h.playerID)
	require.NotEmpty(t, propios)
	assert.Equal(t, string(eventlog.KindUnitLostToCastle), propios[len(propios)-1].Kind)
	assert.Equal(t, "WARRIOR", propios[len(propios)-1].UnitType)
	ajenos := h.avisosPara(enemigo)
	require.NotEmpty(t, ajenos)
	assert.Equal(t, string(eventlog.KindCastleKill), ajenos[len(ajenos)-1].Kind)

	h.tick()
	assert.Equal(t, []int64{guerreroID, 0}, h.objetivosDelCastillo(castilloID))
	_, apunta := h.state.CastleTarget(castilloID)
	assert.False(t, apunta)
}

// RN-CASTLE-004: el castillo golpea al atacante más cercano y, a igual
// distancia, al de menor id, aunque haya llegado después.
func TestElCastilloEligeAlMasCercanoYDesempataPorID(t *testing.T) {
	t.Run("el más cercano", func(t *testing.T) {
		h := newHarness(t)
		h.conectar()
		enemigo, ciudad := conEnemigo(h)
		conCastillo(h, ciudad, castilloID, tileCastillo)
		lejos := ponerUnidad(h, guerreroID, h.playerID, h.cityID, unit.TypeWarrior, world.Tile{X: 26, Y: 38}, 60)
		cerca := ponerUnidad(h, guerreroID+1, h.playerID, h.cityID, unit.TypeWarrior, world.Tile{X: 24, Y: 41}, 60)
		ponerUnidad(h, enemigoID, enemigo, ciudad.ID, unit.TypeVillager, world.Tile{X: 25, Y: 38}, 40)
		ponerUnidad(h, enemigoID+1, enemigo, ciudad.ID, unit.TypeVillager, world.Tile{X: 23, Y: 41}, 40)

		// Cada orden es un paso del arnés: el lejano, solo, recibe el primer
		// disparo; cuando llega el cercano, el castillo cambia de objetivo y el
		// siguiente disparo, 2 s después, es para él.
		h.atacar(lejos.ID, enemigoID)
		require.Equal(t, int32(50), lejos.HP)
		h.atacar(cerca.ID, enemigoID+1)
		require.Empty(t, h.codigos())
		objetivo, ok := h.state.CastleTarget(castilloID)
		require.True(t, ok)
		assert.Equal(t, cerca.ID, objetivo, "a 3 antes que a 5")
		h.hasta(h.clk.NowMs() + 2_000)
		assert.Equal(t, int32(50), cerca.HP)
		assert.Equal(t, int32(50), lejos.HP)
		assert.Equal(t, []int64{lejos.ID, cerca.ID}, h.objetivosDelCastillo(castilloID))
	})

	t.Run("a igual distancia, el de menor id", func(t *testing.T) {
		h := newHarness(t)
		h.conectar()
		enemigo, ciudad := conEnemigo(h)
		conCastillo(h, ciudad, castilloID, tileCastillo)
		segundo := ponerUnidad(h, guerreroID+2, h.playerID, h.cityID, unit.TypeWarrior, world.Tile{X: 24, Y: 41}, 60)
		primero := ponerUnidad(h, guerreroID+1, h.playerID, h.cityID, unit.TypeWarrior, world.Tile{X: 24, Y: 40}, 60)
		ponerUnidad(h, enemigoID, enemigo, ciudad.ID, unit.TypeVillager, world.Tile{X: 25, Y: 41}, 40)
		ponerUnidad(h, enemigoID+1, enemigo, ciudad.ID, unit.TypeVillager, world.Tile{X: 25, Y: 40}, 40)

		h.atacar(segundo.ID, enemigoID)
		h.atacar(primero.ID, enemigoID+1)
		require.Empty(t, h.codigos())
		objetivo, ok := h.state.CastleTarget(castilloID)
		require.True(t, ok)
		assert.Equal(t, primero.ID, objetivo)
	})
}

// INV-COMBAT-007 y RN-CASTLE-005: el castillo no golpea a quien pasa sin atacar,
// a quien ataca a otro jugador, a quien está fuera de su alcance, ni a quien
// queda en paz con su dueño a mitad del ataque; y un castillo en obra no
// dispara.
func TestElCastilloSoloGolpeaAQuienAtacaASuDueno(t *testing.T) {
	quieto := func(t *testing.T, h *harness, ids ...int64) {
		t.Helper()
		vidas := map[int64]int32{}
		for _, id := range ids {
			u, ok := h.state.Unit(id)
			require.True(t, ok)
			vidas[id] = u.HP
		}
		for i := 0; i < 40; i++ {
			h.tick()
		}
		for _, id := range ids {
			u, ok := h.state.Unit(id)
			require.True(t, ok)
			assert.Equal(t, vidas[id], u.HP, "la unidad %d no recibe nada", id)
		}
		_, apunta := h.state.CastleTarget(castilloID)
		assert.False(t, apunta)
	}

	t.Run("pasa junto a la muralla sin atacar", func(t *testing.T) {
		h := newHarness(t)
		h.conectar()
		_, ciudad := conEnemigo(h)
		conCastillo(h, ciudad, castilloID, tileCastillo)
		ponerUnidad(h, guerreroID, h.playerID, h.cityID, unit.TypeWarrior, world.Tile{X: 22, Y: 42}, 60)
		quieto(t, h, guerreroID)
	})

	t.Run("ataca a otro jugador", func(t *testing.T) {
		h := newHarness(t)
		h.conectar()
		vecinos := conVecinos(h, 2)
		ciudad, _ := h.state.City(2)
		conCastillo(h, ciudad, castilloID, tileCastillo)
		ponerUnidad(h, guerreroID, h.playerID, h.cityID, unit.TypeWarrior, world.Tile{X: 23, Y: 41}, 60)
		ponerUnidad(h, enemigoID, vecinos[1], 3, unit.TypeVillager, world.Tile{X: 24, Y: 41}, 40)
		h.atacar(guerreroID, enemigoID)
		require.Empty(t, h.codigos())
		quieto(t, h, guerreroID)
	})

	t.Run("fuera de su alcance", func(t *testing.T) {
		h := newHarness(t)
		h.conectar()
		enemigo, ciudad := conEnemigo(h)
		conCastillo(h, ciudad, castilloID, tileCastillo)
		ponerUnidad(h, guerreroID, h.playerID, h.cityID, unit.TypeWarrior, world.Tile{X: 27, Y: 41}, 60)
		ponerUnidad(h, enemigoID, enemigo, ciudad.ID, unit.TypeVillager, world.Tile{X: 28, Y: 41}, 40)
		h.atacar(guerreroID, enemigoID)
		require.Empty(t, h.codigos())
		quieto(t, h, guerreroID)
	})

	t.Run("un castillo en obra", func(t *testing.T) {
		h := newHarness(t)
		h.conectar()
		enemigo, ciudad := conEnemigo(h)
		ciudad.Era = city.EraCastle
		h.state.SetEconomy(simulation.EconomyLoad{Buildings: []building.Building{{
			ID: castilloID, CityID: ciudad.ID, Type: building.TypeCastle, X: tileCastillo.X, Y: tileCastillo.Y,
			Status: building.StatusUnderConstruction, CompletesAtMs: epoch + 3_600_000,
		}}})
		ponerUnidad(h, guerreroID, h.playerID, h.cityID, unit.TypeWarrior, world.Tile{X: 23, Y: 41}, 60)
		ponerUnidad(h, enemigoID, enemigo, ciudad.ID, unit.TypeVillager, world.Tile{X: 24, Y: 41}, 40)
		h.atacar(guerreroID, enemigoID)
		require.Empty(t, h.codigos())
		quieto(t, h, guerreroID)
	})

	t.Run("queda en paz a mitad del ataque", func(t *testing.T) {
		h := newHarness(t)
		h.conectar()
		enemigo, ciudad := conEnemigo(h)
		conCastillo(h, ciudad, castilloID, tileCastillo)
		g := ponerUnidad(h, guerreroID, h.playerID, h.cityID, unit.TypeWarrior, world.Tile{X: 23, Y: 41}, 60)
		ponerUnidad(h, enemigoID, enemigo, ciudad.ID, unit.TypeVillager, world.Tile{X: 24, Y: 41}, 40)
		h.atacar(guerreroID, enemigoID)
		require.Empty(t, h.codigos())
		require.Equal(t, int32(50), g.HP, "mientras ataca, recibe")

		h.state.SetPeaceTreaties([]diplomacy.Treaty{{ID: 1, PlayerA: h.playerID, PlayerB: enemigo,
			Type: diplomacy.TypeNonAggression, Status: diplomacy.StatusActive}})
		quieto(t, h, guerreroID)
	})
}

// RN-CASTLE-006 y RN-FAC-008: el disparo del castillo resta 8 a una unidad en
// tierra afín a su dueño, como cualquier golpe.
func TestElDisparoDelCastilloEnTierraAfinRestaOcho(t *testing.T) {
	h, _ := conFacciones(t)
	h.conectar()
	enemigo, ciudad := conEnemigo(h)
	conTierra(t, h, h.playerID)
	conCastillo(h, ciudad, castilloID, tileCastillo)
	g := ponerUnidad(h, guerreroID, h.playerID, h.cityID, unit.TypeWarrior, world.Tile{X: 22, Y: 37}, 60)
	ponerUnidad(h, enemigoID, enemigo, ciudad.ID, unit.TypeVillager, world.Tile{X: 22, Y: 36}, 40)
	h.atacar(guerreroID, enemigoID)
	require.Empty(t, h.codigos())
	assert.Equal(t, int32(52), g.HP, "10 × 800 / 1000 en la tierra del atacante")
}

// RN-CASTLE-006: el disparo no despierta a las unidades del jugador golpeado,
// que sólo responden a golpes de unidades.
func TestElDisparoDelCastilloNoDespiertaLaRespuesta(t *testing.T) {
	h := newHarness(t)
	h.conectar()
	enemigo, ciudad := conEnemigo(h)
	conCastillo(h, ciudad, castilloID, tileCastillo)
	g := ponerUnidad(h, guerreroID, h.playerID, h.cityID, unit.TypeWarrior, world.Tile{X: 23, Y: 41}, 60)
	ponerUnidad(h, guardiaID, h.playerID, h.cityID, unit.TypeWarrior, world.Tile{X: 24, Y: 43}, 60)
	ponerUnidad(h, enemigoID, enemigo, ciudad.ID, unit.TypeVillager, world.Tile{X: 24, Y: 41}, 40)
	h.atacar(guerreroID, enemigoID)
	require.Empty(t, h.codigos())
	require.Less(t, g.HP, int32(60))
	for i := 0; i < 30; i++ {
		h.tick()
	}
	_, responde := h.state.Engaged(guardiaID)
	assert.False(t, responde, "el guardia no responde a un castillo")
	guardia, _ := h.state.Unit(guardiaID)
	assert.Equal(t, unit.StatusIdle, guardia.Status)
}

// RN-CASTLE-007: seis guerreros que asedian el castillo lo derriban a los 39 s,
// tras perder tres (el castillo mata uno cada 12 s a partir del primero, que cae
// a los 10). Derribado, deja de disparar y de apuntar en ese tick.
func TestSeisGuerrerosDerribanElCastillo(t *testing.T) {
	h := newHarness(t)
	h.conectar()
	_, ciudad := conEnemigo(h)
	conCastillo(h, ciudad, castilloID, tileCastillo)
	tiles := []world.Tile{{X: 22, Y: 41}, {X: 22, Y: 40}, {X: 22, Y: 42}, {X: 21, Y: 42}, {X: 20, Y: 42}, {X: 22, Y: 41}}
	var guerreros []*unit.Unit
	for i, at := range tiles {
		guerreros = append(guerreros, ponerUnidad(h, guerreroID+int64(i), h.playerID, h.cityID, unit.TypeWarrior, at, 60))
	}
	inicio := h.clk.NowMs()
	for _, g := range guerreros {
		h.derribar(g.ID, castilloID)
	}
	require.Empty(t, h.codigos())

	require.True(t, h.tickHasta(600, func() bool {
		_, enPie := h.state.Building(castilloID)
		return !enPie
	}))
	assert.Equal(t, inicio+39_000, h.clk.NowMs(), "126 golpes de 8")
	require.Len(t, h.combat.deaths, 3)
	for i, d := range h.combat.deaths {
		assert.Equal(t, guerreroID+int64(i), d.VictimID, "cae primero el de menor id")
		assert.Equal(t, castilloID, d.KillerBuildingID)
	}
	assert.Equal(t, inicio+10_000, h.combat.deaths[0].AtMs)
	assert.Equal(t, inicio+22_000, h.combat.deaths[1].AtMs)
	assert.Equal(t, inicio+34_000, h.combat.deaths[2].AtMs)
	_, apunta := h.state.CastleTarget(castilloID)
	assert.False(t, apunta, "un castillo derribado no apunta")

	cuarto := guerreros[3]
	vida := cuarto.HP
	for i := 0; i < 40; i++ {
		h.tick()
	}
	assert.Equal(t, vida, cuarto.HP, "ni un disparo más")
}

// RN-CASTLE-002, RN-CASTLE-003 e INV-ECON-012: el castillo pide la Edad de los
// Castillos, cuesta 500 de madera y 800 de piedra, tarda 180 s con el rasgo
// (163 636 ms un romano, 200 000 un nórdico), es único y no da población.
func TestElCastilloPideCastillosCuestaDosRecursosYEsUnico(t *testing.T) {
	encargar := func(h *harness, at world.Tile) {
		h.send(simulation.BuildBuilding{PlayerID: h.playerID, RequestID: uuid.NewString(), CityID: h.cityID,
			Type: building.TypeCastle, Tile: at})
	}
	en := func(t *testing.T, civilizacion string, era city.Era) *harness {
		h := newHarness(t)
		h.darEras()
		conCivilizacion(h, civilizacion)
		h.ciudad().Era = era
		h.ciudad().PopulationLimit = 150
		h.almacenDe(h.cityID, map[economy.Resource]int64{economy.ResourceWood: 1_000, economy.ResourceStone: 1_000})
		return h
	}

	hierro := en(t, "ROMAN", city.EraIron)
	encargar(hierro, tileTaller)
	assert.Equal(t, []string{protocol.CodeInvalidTarget}, hierro.codigos(), "en Hierro no hay castillo")

	h := en(t, "ROMAN", city.EraCastle)
	h.almacenDe(h.cityID, map[economy.Resource]int64{economy.ResourceWood: 500, economy.ResourceStone: 799})
	encargar(h, tileTaller)
	assert.Equal(t, []string{protocol.CodeInsufficientResources}, h.codigos())

	h.almacenDe(h.cityID, map[economy.Resource]int64{economy.ResourceWood: 600, economy.ResourceStone: 900})
	encargar(h, tileTaller)
	require.Len(t, h.economy.builds, 1)
	orden := h.economy.builds[0]
	assert.Equal(t, building.TypeCastle, orden.Building.Type)
	assert.Equal(t, epoch+163_636, orden.Building.CompletesAtMs)
	require.Len(t, orden.Stocks, 2)
	assert.Equal(t, int64(-500), orden.Stocks[0].Delta)
	assert.Equal(t, int64(-800), orden.Stocks[1].Delta)

	h.rec.reset()
	encargar(h, world.Tile{X: 9, Y: 9})
	assert.Equal(t, []string{protocol.CodeInvalidTarget}, h.codigos(), "INV-ECON-012: uno por ciudad")
	assert.Len(t, h.economy.builds, 1)

	h.hasta(epoch + 163_700)
	b, ok := h.state.BuildingAt(tileTaller)
	require.True(t, ok)
	assert.Equal(t, building.StatusComplete, b.Status)
	assert.Equal(t, int32(150), h.ciudad().PopulationLimit, "el castillo no da población")

	nordico := en(t, "NORSE", city.EraCastle)
	encargar(nordico, tileTaller)
	require.Len(t, nordico.economy.builds, 1)
	assert.Equal(t, epoch+200_000, nordico.economy.builds[0].Building.CompletesAtMs)
}

// RN-CASTLE-001, INV-ECON-011 y Q1-Q2: un nórdico en Hierro avanza a Castillos
// pagando 1 600 de madera, 800 de piedra y 120 bienes ajenos en el orden del
// catálogo (30 de vino, 40 de seda y 50 de especias), nunca pieles; no entrena
// mientras dura y a los 360 s está en Castillos con límite 150. Con 119 ajenos y
// pieles de sobra se rechaza; desde Castillos no hay más.
func TestElAvanceACastillosCobraCientoVeinteBienesAjenos(t *testing.T) {
	hierro := func(t *testing.T) *harness {
		h := newHarness(t)
		h.darEras()
		conCivilizacion(h, "NORSE")
		h.ciudad().Era = city.EraIron
		h.ciudad().PopulationLimit = 100
		return h
	}
	h := hierro(t)
	h.almacenDe(h.cityID, map[economy.Resource]int64{
		economy.ResourceWood: 1_700, economy.ResourceStone: 800, economy.ResourceFurs: 500,
		economy.ResourceWine: 30, economy.ResourceSilk: 40, economy.ResourceSpices: 90,
	})
	h.avanzar()
	require.Empty(t, h.codigos())
	require.Len(t, h.economy.advances, 1)
	assert.Equal(t, city.EraCastle, h.economy.advances[0].Advance.To)
	pagado := map[economy.Resource]int64{}
	for _, d := range h.economy.advances[0].Stocks {
		pagado[d.Resource] = -d.Delta
	}
	assert.Equal(t, map[economy.Resource]int64{
		economy.ResourceWood: 1_600, economy.ResourceStone: 800,
		economy.ResourceWine: 30, economy.ResourceSilk: 40, economy.ResourceSpices: 50,
	}, pagado)
	assert.Equal(t, int64(500), h.stockDe(economy.ResourceFurs), "las pieles propias no se tocan")

	h.darRecursos(100, 0)
	h.entrenar()
	assert.Equal(t, []string{protocol.CodeCityBusy}, h.codigos(), "no entrena mientras avanza")

	h.hasta(epoch + 359_900)
	assert.Equal(t, city.EraIron, h.ciudad().Era)
	h.hasta(epoch + 360_000)
	assert.Equal(t, city.EraCastle, h.ciudad().Era)
	assert.Equal(t, int32(150), h.ciudad().PopulationLimit)

	sin := hierro(t)
	sin.almacenDe(sin.cityID, map[economy.Resource]int64{
		economy.ResourceWood: 1_600, economy.ResourceStone: 800, economy.ResourceFurs: 900, economy.ResourceWine: 119,
	})
	sin.avanzar()
	assert.Equal(t, []string{protocol.CodeInsufficientResources}, sin.codigos(), "las pieles propias no cuentan")
	assert.Empty(t, sin.economy.advances)
}

// INV-ECON-005 y ADR-044 §2: tras un reinicio, un avance a Castillos a medias
// termina en su instante, y un castillo completo se carga entero, listo y sin
// objetivo: dispara en cuanto alguien ataca.
func TestReinicioConLaEdadDeLosCastillos(t *testing.T) {
	t.Run("un avance a Castillos a medias", func(t *testing.T) {
		h := newHarness(t)
		h.darEras()
		h.ciudad().Era = city.EraIron
		h.ciudad().PopulationLimit = 100
		h.state.SetEconomy(simulation.EconomyLoad{Advances: []advance.Advance{{
			CityID: h.cityID, From: city.EraIron, To: city.EraCastle, CompletesAtMs: epoch + 5_000, RequestID: "r",
		}}})
		h.hasta(epoch + 4_900)
		assert.Equal(t, city.EraIron, h.ciudad().Era)
		h.hasta(epoch + 5_000)
		assert.Equal(t, city.EraCastle, h.ciudad().Era)
		assert.Equal(t, int32(150), h.ciudad().PopulationLimit)
	})

	t.Run("un castillo completo", func(t *testing.T) {
		h := newHarness(t)
		h.conectar()
		enemigo, ciudad := conEnemigo(h)
		conCastillo(h, ciudad, castilloID, tileCastillo)
		hp, _, ok := h.state.BuildingHP(castilloID)
		require.True(t, ok)
		assert.Equal(t, int32(1_000), hp)
		_, apunta := h.state.CastleTarget(castilloID)
		assert.False(t, apunta)

		g := ponerUnidad(h, guerreroID, h.playerID, h.cityID, unit.TypeWarrior, world.Tile{X: 23, Y: 41}, 60)
		ponerUnidad(h, enemigoID, enemigo, ciudad.ID, unit.TypeVillager, world.Tile{X: 24, Y: 41}, 40)
		h.atacar(guerreroID, enemigoID)
		require.Empty(t, h.codigos())
		assert.Equal(t, int32(50), g.HP, "el primer disparo, en el primer tick del ataque")
	})
}

// SPEC-CASTLE-AGE §9: un saqueador junto a la muralla está al alcance del
// castillo (a 3 de él), que le apunta y le quita 10 en su primer disparo.
func TestElCastilloDisparaAlSaqueador(t *testing.T) {
	h := newHarness(t)
	_, ciudad, g := conSaqueo(h, 200, world.Tile{X: 20, Y: 30})
	conCastillo(h, ciudad, castilloID, tileCastillo)

	h.saquear(guerreroID, ciudad.ID)
	require.Empty(t, h.codigos())
	require.True(t, h.tickHasta(300, func() bool { return g.HP < 60 }))
	assert.Equal(t, int32(50), g.HP)
	objetivo, ok := h.state.CastleTarget(castilloID)
	require.True(t, ok)
	assert.Equal(t, guerreroID, objetivo)
	cid, saquea := h.state.Pillaging(guerreroID)
	require.True(t, saquea, "el disparo no termina el saqueo")
	assert.Equal(t, ciudad.ID, cid)
}

// SPEC-CASTLE-AGE §4.3 y §9: el arquero a caballo, que asedia desde tres tiles de
// la casa, sigue al alcance del castillo (ningún tile de su anillo está a más de
// cinco de él) y recibe su disparo.
func TestElCastilloAlcanzaAlArqueroQueAsediaDesdeTres(t *testing.T) {
	h := newHarness(t)
	h.conectar()
	_, c := conEnemigo(h)
	conCasa(h, c, casaID, tileCasa, true)
	conCastillo(h, c, castilloID, tileCastillo)
	a := ponerUnidad(h, guerreroID, h.playerID, h.cityID, unit.TypeHorseArcher, world.Tile{X: 20, Y: 30}, 60)

	h.derribar(guerreroID, casaID)
	require.Empty(t, h.codigos())
	require.True(t, h.tickHasta(300, func() bool { return a.HP < 60 }))
	assert.Equal(t, int32(50), a.HP)
	pos := h.unitTile(guerreroID)
	assert.Equal(t, int32(3), max(abs32(pos.X-tileCasa.X), abs32(pos.Y-tileCasa.Y)), "asedia desde tres tiles")
	assert.LessOrEqual(t, max(abs32(pos.X-tileCastillo.X), abs32(pos.Y-tileCastillo.Y)), int32(5))
}

// INV-ECON-005 (SPEC-CASTLE-AGE §9, recovery): una obra de castillo a medias
// termina tras un reinicio en su instante absoluto, sin dar población; y una que
// venció durante la caída, en el primer tick.
func TestReinicioConUnCastilloEnObra(t *testing.T) {
	obra := building.Building{ID: castilloID, CityID: 1, Type: building.TypeCastle, X: 9, Y: 9,
		Status: building.StatusUnderConstruction, CompletesAtMs: epoch + 180_000}
	load := simulation.EconomyLoad{Buildings: []building.Building{obra}}

	h, _ := arrancar(t, persistido{economia: load}, epoch+10_000)
	limite := h.ciudad().PopulationLimit
	h.hasta(epoch + 179_900)
	b, _ := h.state.Building(castilloID)
	assert.Equal(t, building.StatusUnderConstruction, b.Status)
	h.hasta(epoch + 180_000)
	b, _ = h.state.Building(castilloID)
	assert.Equal(t, building.StatusComplete, b.Status)
	assert.Equal(t, limite, h.ciudad().PopulationLimit, "el castillo no da población")
	_, apunta := h.state.CastleTarget(castilloID)
	assert.False(t, apunta, "completo, listo y sin objetivo")

	h2, _ := arrancar(t, persistido{economia: load}, epoch+200_000)
	h2.tick()
	b, _ = h2.state.Building(castilloID)
	assert.Equal(t, building.StatusComplete, b.Status)
	require.Len(t, h2.economy.completions, 1)
}
