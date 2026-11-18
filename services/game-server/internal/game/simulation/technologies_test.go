package simulation_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/building"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/economy"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/player"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/task"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/technology"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// Tests del árbol tecnológico (SPEC-TECHNOLOGIES §9, ADR-046). La ciudad 1 del
// arnés está en (10,10), en la Edad de Piedra; cada test la pone en la era que
// necesita.

// catalogoDeTecnologias es el de SPEC-TECHNOLOGIES §4.1, el que siembra 000027.
func catalogoDeTecnologias(t *testing.T) technology.Catalog {
	t.Helper()
	madera := func(n int64) economy.Cost { return economy.Cost{Resource: economy.ResourceWood, Amount: n} }
	piedra := func(n int64) economy.Cost { return economy.Cost{Resource: economy.ResourceStone, Amount: n} }
	comun := func(code, name string, era city.Era, eff string, v int32, w, s, foreign, ms int64, req ...string) technology.Technology {
		return technology.Technology{Code: code, Name: name, Era: era, Costs: economy.Costs{madera(w), piedra(s)},
			ForeignGoods: foreign, DurationMs: ms, Requires: req, Effects: map[string]int32{eff: v}}
	}
	exclusiva := func(code, name, civ, eff string, v int32) technology.Technology {
		return technology.Technology{Code: code, Name: name, Era: city.EraIron, Civilization: civ,
			Costs: economy.Costs{madera(200), piedra(200)}, OwnGood: 40, DurationMs: 180_000, Effects: map[string]int32{eff: v}}
	}
	cat, err := technology.NewCatalog([]technology.Technology{
		comun("STONE_TOOLS", "Herramientas de piedra", city.EraStone, player.TraitGatherSpeed, 1100, 150, 50, 0, 60_000),
		comun("BASKETRY", "Cestería", city.EraStone, player.TraitGatherCarry, 1200, 120, 30, 0, 60_000),
		comun("BRONZE_WEAPONS", "Armas de bronce", city.EraBronze, player.TraitAttackDamage, 1100, 250, 150, 0, 120_000),
		comun("MASONRY", "Albañilería", city.EraBronze, player.TraitHouseHP, 1200, 200, 200, 0, 120_000),
		comun("WHEEL", "La rueda", city.EraIron, player.TraitMoveSpeed, 1050, 300, 200, 30, 180_000),
		comun("GUILDS", "Gremios", city.EraIron, player.TraitProductionSpeed, 1100, 300, 300, 30, 180_000),
		comun("IRON_WEAPONS", "Armas de hierro", city.EraCastle, player.TraitAttackDamage, 1100, 400, 300, 60, 300_000, "BRONZE_WEAPONS"),
		comun("ARCHITECTURE", "Arquitectura", city.EraCastle, player.TraitProductionSpeed, 1100, 400, 400, 60, 300_000, "GUILDS"),
		exclusiva("ROMAN_ROADS", "Calzadas", "ROMAN", player.TraitMoveSpeed, 1100),
		exclusiva("THEODOSIAN_WALLS", "Muros de Teodosio", "BYZANTINE", player.TraitHouseHP, 1200),
		exclusiva("CARAVANSERAI", "Caravasares", "PERSIAN", player.TraitGatherCarry, 1200),
		exclusiva("RAIDING", "Incursiones", "NORSE", player.TraitLootCarry, 1200),
	}, []string{"ROMAN", "BYZANTINE", "PERSIAN", "NORSE"})
	require.NoError(t, err)
	return cat
}

// conTecnologias instala el catálogo y da a cada jugador las tecnologías de
// `tenidas`, como la carga.
func conTecnologias(h *harness, tenidas map[uuid.UUID][]string) {
	h.t.Helper()
	h.state.SetTechnologies(catalogoDeTecnologias(h.t), tenidas)
}

func (h *harness) investigar(code string) string {
	h.t.Helper()
	req := uuid.NewString()
	h.send(simulation.ResearchTechnology{PlayerID: h.playerID, RequestID: req, CityID: h.cityID, Technology: code})
	return req
}

// paraElDueno son los city.update que recibió el dueño de la ciudad por su
// canal propio (SendToPlayer).
func (h *harness) paraElDueno() []protocol.CityUpdatePayload {
	var out []protocol.CityUpdatePayload
	for _, m := range h.rec.byType(protocol.TypeCityUpdate) {
		if m.Kind == "player" && m.PlayerID == h.playerID {
			out = append(out, m.Payload.(protocol.CityUpdatePayload))
		}
	}
	return out
}

// Golden «investigación» (RN-TECH-002 a RN-TECH-006, ADR-046 §3): una ciudad
// romana en Hierro investiga Gremios. Al ordenar se reserva el coste —300 de
// madera, 300 de piedra y 30 de seda, el bien ajeno del orden del catálogo—, la
// transacción lo debita y, al volver, el dueño (sólo él) recibe la
// investigación. El plazo es el del catálogo con production.speed romano: 180 000
// / 1,1 = 163 636 ms; vence en el tick de 163 700, el jugador tiene Gremios y su
// production.speed compuesto es 1210. La casa encargada antes del fin termina a
// su hora romana (13 636); la de después, con 1210, a los 12 397. Mientras
// investiga, la ciudad entrena, pero no avanza de era.
func TestGoldenInvestigacion(t *testing.T) {
	h := newHarness(t)
	conCivilizacion(h, "ROMAN")
	conTecnologias(h, nil)
	h.darEras()
	h.ciudad().Era = city.EraIron
	h.almacenDe(h.cityID, map[economy.Resource]int64{
		economy.ResourceWood: 1000, economy.ResourceStone: 1000, economy.ResourceSilk: 50,
	})

	h.investigar("GUILDS")
	require.Empty(t, h.codigos())
	assert.Equal(t, int64(700), h.stockDe(economy.ResourceWood), "la reserva sale al ordenar")
	assert.Equal(t, int64(700), h.stockDe(economy.ResourceStone))
	assert.Equal(t, int64(20), h.stockDe(economy.ResourceSilk))
	r, confirmada, ok := h.state.Research(h.cityID)
	require.True(t, ok)
	assert.False(t, confirmada, "hasta que vuelve la transacción no existe para nadie")
	assert.Equal(t, epoch+163_636, r.CompletesAtMs)
	require.Len(t, h.economy.research, 1)
	o := h.economy.research[0]
	assert.Equal(t, "GUILDS", o.Research.Technology)
	assert.Len(t, o.Stocks, 3, "un delta por recurso del coste")
	assert.Empty(t, h.paraElDueno())

	h.tick()
	_, confirmada, _ = h.state.Research(h.cityID)
	require.True(t, confirmada)
	updates := h.paraElDueno()
	require.Len(t, updates, 1)
	require.NotNil(t, updates[0].Research)
	require.NotNil(t, *updates[0].Research)
	assert.Equal(t, protocol.ResearchView{Technology: "GUILDS", CompletesAtMs: epoch + 163_636}, **updates[0].Research)
	for _, m := range h.rec.byType(protocol.TypeCityUpdate) {
		if m.Kind != "player" {
			assert.Nil(t, m.Payload.(protocol.CityUpdatePayload).Research, "el área no ve la investigación")
		}
	}

	// Mientras investiga: entrena, pero no avanza (RN-TECH-003).
	h.entrenar()
	assert.Empty(t, h.codigos(), "investigar no impide entrenar")
	h.avanzar()
	assert.Equal(t, []string{protocol.CodeCityBusy}, h.codigos())
	h.rec.reset()

	h.hasta(epoch + 160_000)
	h.construir(world.Tile{X: 9, Y: 9})
	require.Empty(t, h.codigos())
	antes := h.economy.builds[len(h.economy.builds)-1]
	assert.Equal(t, epoch+160_000+13_636, antes.Building.CompletesAtMs, "con el rasgo romano, sin Gremios")

	h.hasta(epoch + 163_600)
	assert.Empty(t, h.state.Technologies(h.playerID), "a 163 600 todavía no")
	h.tick()
	assert.Equal(t, epoch+163_700, h.clk.NowMs())
	assert.Equal(t, []string{"GUILDS"}, h.state.Technologies(h.playerID))
	assert.Equal(t, int32(1210), h.state.Trait(h.playerID, player.TraitProductionSpeed), "1100 × 1100")
	_, _, sigue := h.state.Research(h.cityID)
	assert.False(t, sigue)
	updates = h.paraElDueno()
	require.NotEmpty(t, updates)
	fin := updates[len(updates)-1]
	require.NotNil(t, fin.Research)
	assert.Nil(t, *fin.Research, "la investigación termina")
	require.NotNil(t, fin.Technologies)
	assert.Equal(t, []string{"GUILDS"}, *fin.Technologies)
	require.Len(t, h.economy.researched, 1)
	assert.Equal(t, simulation.ResearchCompletion{CityID: h.cityID, PlayerID: h.playerID, Technology: "GUILDS",
		AtMs: epoch + 163_636, Tick: h.economy.researched[0].Tick}, h.economy.researched[0])

	h.construir(world.Tile{X: 11, Y: 9})
	require.Empty(t, h.codigos())
	despues := h.economy.builds[len(h.economy.builds)-1]
	assert.Equal(t, h.clk.NowMs()+12_397, despues.Building.CompletesAtMs, "la siguiente obra ya lleva Gremios")
	h.hasta(epoch + 173_700)
	b, _ := h.state.BuildingAt(world.Tile{X: 9, Y: 9})
	assert.Equal(t, building.StatusComplete, b.Status, "la que estaba en obra terminó a su hora")
}

// SPEC-TECHNOLOGIES §5: cada rechazo de city.research con su código y su
// motivo, sin reserva ni encargo.
func TestLosRechazosDeInvestigar(t *testing.T) {
	casos := []struct {
		nombre  string
		era     city.Era
		tenidas []string
		code    string
		quien   func(h *harness) uuid.UUID
		codigo  string
		motivo  string
	}{
		{nombre: "una ciudad ajena", era: city.EraStone, code: "STONE_TOOLS",
			quien: func(*harness) uuid.UUID { return uuid.New() }, codigo: protocol.CodeCityNotFound},
		{nombre: "una tecnología que no existe", era: city.EraCastle, code: "POLVORA",
			codigo: protocol.CodeInvalidTarget, motivo: "esa tecnología no está a tu alcance"},
		{nombre: "la exclusiva de otra civilización", era: city.EraCastle, code: "RAIDING",
			codigo: protocol.CodeInvalidTarget, motivo: "esa tecnología no está a tu alcance"},
		{nombre: "una que ya tiene", era: city.EraStone, tenidas: []string{"STONE_TOOLS"}, code: "STONE_TOOLS",
			codigo: protocol.CodeInvalidTarget, motivo: "ya tienes esa tecnología: Herramientas de piedra"},
		{nombre: "una de una era posterior", era: city.EraStone, code: "BRONZE_WEAPONS",
			codigo: protocol.CodeInvalidTarget, motivo: "Armas de bronce pide la Edad de Bronce"},
		{nombre: "sin su requisito", era: city.EraCastle, code: "IRON_WEAPONS",
			codigo: protocol.CodeInvalidTarget, motivo: "Armas de hierro pide Armas de bronce"},
		{nombre: "sin almacén", era: city.EraIron, code: "ROMAN_ROADS", codigo: protocol.CodeInsufficientResources},
	}
	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			h := newHarness(t)
			conCivilizacion(h, "ROMAN")
			conTecnologias(h, map[uuid.UUID][]string{h.playerID: c.tenidas})
			h.ciudad().Era = c.era
			h.almacenDe(h.cityID, map[economy.Resource]int64{economy.ResourceWood: 5000, economy.ResourceStone: 5000})
			quien := h.playerID
			if c.quien != nil {
				quien = c.quien(h)
			}
			h.send(simulation.ResearchTechnology{PlayerID: quien, RequestID: uuid.NewString(), CityID: h.cityID, Technology: c.code})
			assert.Equal(t, []string{c.codigo}, h.codigos())
			if c.motivo != "" {
				assert.Contains(t, h.ultimoMotivo(), c.motivo)
			}
			_, _, investiga := h.state.Research(h.cityID)
			assert.False(t, investiga)
			assert.Empty(t, h.economy.research)
			assert.Equal(t, int64(5000), h.stockDe(economy.ResourceWood), "nada reservado")
		})
	}
}

// RN-TECH-003, RN-ECON-026 e INV-TECH-001: una investigación y un avance de era no
// conviven, en los dos órdenes; otra investigación tampoco.
func TestLaInvestigacionYElAvanceNoConviven(t *testing.T) {
	t.Run("investigar y luego avanzar u otra investigación", func(t *testing.T) {
		h := newHarness(t)
		conTecnologias(h, nil)
		h.darEras()
		h.darRecursos(5000, 5000)
		h.investigar("STONE_TOOLS")
		require.Empty(t, h.codigos())
		h.avanzar()
		assert.Equal(t, []string{protocol.CodeCityBusy}, h.codigos())
		h.rec.reset()
		h.investigar("BASKETRY")
		assert.Equal(t, []string{protocol.CodeCityBusy}, h.codigos())
	})
	t.Run("avanzar y luego investigar", func(t *testing.T) {
		h := newHarness(t)
		conTecnologias(h, nil)
		h.darEras()
		h.darRecursos(5000, 5000)
		h.avanzar()
		require.Empty(t, h.codigos())
		h.investigar("STONE_TOOLS")
		assert.Equal(t, []string{protocol.CodeCityBusy}, h.codigos())
	})
}

// INV-ECON-002 e INV-TECH-001: si la base rechaza el encargo (un trigger, por
// ejemplo), la reserva vuelve entera y no queda investigación.
func TestLaBaseRechazaLaInvestigacion(t *testing.T) {
	h := newHarness(t)
	conTecnologias(h, nil)
	h.darRecursos(500, 500)
	h.economy.researchErr = technology.ErrUnavailable
	h.investigar("STONE_TOOLS")
	require.Empty(t, h.codigos())
	assert.Equal(t, int64(350), h.madera())
	h.tick()
	assert.Equal(t, []string{protocol.CodeInvalidTarget}, h.codigos())
	assert.Equal(t, int64(500), h.madera(), "la madera vuelve")
	assert.Equal(t, int64(500), h.piedra(), "y la piedra")
	_, _, investiga := h.state.Research(h.cityID)
	assert.False(t, investiga)
	assert.Empty(t, h.paraElDueno())
}

// SPEC-TECHNOLOGIES §4.3 e INV-TECH-002: unit.move_speed (La rueda) fija la
// polilínea al dar la orden: cinco pasos de hierba a 571 ms.
func TestLaRuedaAcortaLaPolilinea(t *testing.T) {
	h := newHarness(t)
	conTecnologias(h, map[uuid.UUID][]string{h.playerID: {"WHEEL"}})
	h.send(simulation.MoveUnit{PlayerID: h.playerID, RequestID: uuid.NewString(), UnitID: 1, Target: world.Tile{X: 17, Y: 10}})
	require.Empty(t, h.codigos())
	started := h.rec.byType(protocol.TypeUnitMovementStarted)
	require.Len(t, started, 1)
	assert.Equal(t, epoch+2_855, started[0].Payload.(protocol.UnitMovementStartedPayload).Movement.ArrivalTimeMs)
}

// production.speed (Gremios): una casa sin rasgo termina a los 13 636 ms; con el
// rasgo romano y Gremios, compuesto a 1210, a los 12 397.
func TestGremiosAcortaLaObra(t *testing.T) {
	h := newHarness(t)
	conTecnologias(h, map[uuid.UUID][]string{h.playerID: {"GUILDS"}})
	h.darMadera(30)
	h.construir(world.Tile{X: 9, Y: 9})
	require.Len(t, h.economy.builds, 1)
	assert.Equal(t, epoch+13_636, h.economy.builds[0].Building.CompletesAtMs)

	r := newHarness(t)
	conCivilizacion(r, "ROMAN")
	conTecnologias(r, map[uuid.UUID][]string{r.playerID: {"GUILDS"}})
	r.darMadera(30)
	r.construir(world.Tile{X: 9, Y: 9})
	require.Len(t, r.economy.builds, 1)
	assert.Equal(t, epoch+12_397, r.economy.builds[0].Building.CompletesAtMs, "1100 × 1100 = 1210")
}

// economy.gather_speed (Herramientas de piedra) fija el trabajo en el tile al
// llegar: 9 091 ms en vez de 10 000; la llegada no cambia.
func TestLasHerramientasDePiedraAcortanElTrabajo(t *testing.T) {
	h := newHarnessMadera(t)
	conTecnologias(h, map[uuid.UUID][]string{h.playerID: {"STONE_TOOLS"}})
	h.conectar()
	h.recolectar(1, bosque)
	started := h.rec.byType(protocol.TypeUnitMovementStarted)
	require.Len(t, started, 1)
	llegada := started[0].Payload.(protocol.UnitMovementStartedPayload).Movement.ArrivalTimeMs
	assert.Equal(t, epoch+6_360, llegada)
	h.hasta(llegada + 100)
	tk, ok := h.state.Task(1)
	require.True(t, ok)
	require.Equal(t, task.PhaseWorking, tk.Phase)
	assert.Equal(t, llegada+9_091, tk.PhaseUntilMs)
}

// economy.gather_carry (Cestería): el aldeano carga 12 en vez de 10 al terminar
// el trabajo en el tile, y deposita los 12 (INV-ECON-008: la carga sale del
// bosque).
func TestLaCesteriaCargaDoce(t *testing.T) {
	h := newHarnessMadera(t)
	conTecnologias(h, map[uuid.UUID][]string{h.playerID: {"BASKETRY"}})
	h.conectar()
	h.recolectar(1, bosque)
	h.hasta(epoch + 16_400)
	cargas := h.cargas()
	require.NotEmpty(t, cargas)
	assert.EqualValues(t, 12, cargas[0].Amount)
	h.hasta(epoch + 22_500)
	assert.Equal(t, int64(12), h.madera())
}

// building.house_hp (Albañilería) de la civilización del dueño de la casa: 150 ×
// 1,2 = 180.
func TestLaAlbanileriaDaCasasDe180(t *testing.T) {
	h := newHarness(t)
	enemigo, _, _ := conAsedio(h, world.Tile{X: 20, Y: 30})
	conTecnologias(h, map[uuid.UUID][]string{enemigo: {"MASONRY"}})
	hp, maxHP, ok := h.state.BuildingHP(casaID)
	require.True(t, ok)
	assert.Equal(t, int32(180), hp)
	assert.Equal(t, int32(180), maxHP)
}

// combat.loot_carry (Incursiones): un guerrero carga 24; cuatro golpes de 5 y
// uno de 4.
func TestLasIncursionesCarganVeinticuatro(t *testing.T) {
	h := newHarness(t)
	_, ciudad, g := conSaqueo(h, 200, world.Tile{X: 20, Y: 30})
	conTecnologias(h, map[uuid.UUID][]string{h.playerID: {"RAIDING"}})
	h.darMadera(0)
	h.saquear(guerreroID, ciudad.ID)
	require.Empty(t, h.codigos())
	var stocks []int64
	stock := h.state.Stock(ciudad.ID).Amount
	require.True(t, h.tickHasta(400, func() bool {
		if s := h.state.Stock(ciudad.ID).Amount; s != stock {
			stock = s
			stocks = append(stocks, s)
		}
		_, sigue := h.state.Pillaging(guerreroID)
		return !sigue
	}))
	h.tick() // la carga del último golpe llega con su transacción
	assert.Equal(t, []int64{195, 190, 185, 180, 176}, stocks)
	assert.Equal(t, int32(24), g.Loot)
}

// combat.attack_damage (Armas de bronce): el golpe de 8 pasa a 9 (8 × 1,1 =
// 8,8), contra una unidad y contra una casa.
func TestLasArmasDeBronceGolpeanNueve(t *testing.T) {
	t.Run("contra una unidad", func(t *testing.T) {
		h := newHarness(t)
		enemigo, _ := conEnemigo(h)
		conTecnologias(h, map[uuid.UUID][]string{h.playerID: {"BRONZE_WEAPONS"}})
		ponerUnidad(h, guerreroID, h.playerID, h.cityID, unit.TypeWarrior, world.Tile{X: 19, Y: 20}, 60)
		v := ponerUnidad(h, enemigoID, enemigo, 2, unit.TypeVillager, world.Tile{X: 20, Y: 20}, 40)
		h.atacar(guerreroID, v.ID)
		require.Empty(t, h.codigos())
		require.True(t, h.tickHasta(30, func() bool { return v.HP < 40 }))
		assert.Equal(t, int32(31), v.HP)
	})
	t.Run("contra una casa", func(t *testing.T) {
		h := newHarness(t)
		_, _, _ = conAsedio(h, world.Tile{X: 20, Y: 30})
		conTecnologias(h, map[uuid.UUID][]string{h.playerID: {"BRONZE_WEAPONS"}})
		h.derribar(guerreroID, casaID)
		require.Empty(t, h.codigos())
		require.True(t, h.tickHasta(600, func() bool { hp, _ := vidaDe(h, casaID); return hp < 150 }))
		hp, _ := vidaDe(h, casaID)
		assert.Equal(t, int32(141), hp)
	})
}

// RN-TECH-008: el snapshot lleva la investigación y las tecnologías sólo en la
// ciudad de quien lo recibe.
func TestElSnapshotLlevaLasTecnologiasSoloAlDueno(t *testing.T) {
	h := newHarness(t)
	conTecnologias(h, map[uuid.UUID][]string{h.playerID: {"STONE_TOOLS"}})
	h.darRecursos(500, 500)
	h.investigar("BASKETRY")
	h.tick()
	chunks := []world.ChunkCoord{{CX: 0, CY: 0}, {CX: 1, CY: 0}, {CX: 0, CY: 1}, {CX: 1, CY: 1}}

	propia := h.sim.BuildSnapshot(h.playerID, chunks, h.clk.NowMs(), false)
	require.NotEmpty(t, propia.Cities)
	v := propia.Cities[0]
	require.NotNil(t, v.Research)
	require.NotNil(t, *v.Research)
	assert.Equal(t, "BASKETRY", (*v.Research).Technology)
	require.NotNil(t, v.Technologies)
	assert.Equal(t, []string{"STONE_TOOLS"}, *v.Technologies)

	ajena := h.sim.BuildSnapshot(uuid.New(), chunks, h.clk.NowMs(), false)
	require.NotEmpty(t, ajena.Cities)
	assert.Nil(t, ajena.Cities[0].Research)
	assert.Nil(t, ajena.Cities[0].Technologies)
}

// Recuperación (SPEC-TECHNOLOGIES §9, INV-ECON-005): una investigación que venció
// con el servidor parado termina en el primer tick, en su instante; las
// tecnologías leídas vuelven con su rasgo compuesto.
func TestLasInvestigacionesTrasUnReinicio(t *testing.T) {
	h := newHarness(t)
	conCivilizacion(h, "ROMAN")
	conTecnologias(h, map[uuid.UUID][]string{h.playerID: {"GUILDS"}})
	h.ciudad().Era = city.EraIron
	h.state.SetEconomy(simulation.EconomyLoad{Research: []technology.Research{
		{CityID: h.cityID, Technology: "WHEEL", CompletesAtMs: h.clk.NowMs() - 5_000, RequestID: "r-1"},
	}})
	assert.Equal(t, int32(1210), h.state.Trait(h.playerID, player.TraitProductionSpeed))
	r, confirmada, ok := h.state.Research(h.cityID)
	require.True(t, ok)
	assert.True(t, confirmada, "la que vuelve de la base está confirmada")
	assert.Equal(t, "WHEEL", r.Technology)

	h.tick()
	assert.Equal(t, []string{"GUILDS", "WHEEL"}, h.state.Technologies(h.playerID))
	assert.Equal(t, int32(1050), h.state.Trait(h.playerID, player.TraitMoveSpeed))
	require.Len(t, h.economy.researched, 1)
	assert.Equal(t, epoch-5_000, h.economy.researched[0].AtMs, "en su instante, no en el del tick")
}

// ADR-022: un fin que la cola no escribe vuelve en el volcado siguiente.
func TestUnFinDeInvestigacionDescartadoSeReencola(t *testing.T) {
	h := newHarness(t)
	conTecnologias(h, nil)
	h.darRecursos(500, 500)
	h.persister.permanent = true
	h.investigar("STONE_TOOLS")
	h.tick()
	h.economy.researchEndErr = assert.AnError
	h.hasta(epoch + 60_100)
	assert.Equal(t, []string{"STONE_TOOLS"}, h.state.Technologies(h.playerID), "la RAM no espera a la base")
	assert.Empty(t, h.economy.researched)

	h.economy.researchEndErr = nil
	h.advance(6 * time.Second) // un volcado
	require.Len(t, h.economy.researched, 1)
	assert.Equal(t, "STONE_TOOLS", h.economy.researched[0].Technology)
}

// RN-TECH-003 y RN-TECH-004: la exclusiva romana (Calzadas) cobra 40 de vino, el
// bien propio, además de la madera y la piedra; y mientras se investiga, el taller
// elabora.
func TestLaExclusivaCobraElBienPropioYNoImpideElaborar(t *testing.T) {
	h := newHarness(t)
	conCivilizacion(h, "ROMAN")
	conTecnologias(h, nil)
	h.ciudad().Era = city.EraIron
	h.almacenDe(h.cityID, map[economy.Resource]int64{
		economy.ResourceWood: 500, economy.ResourceStone: 500, economy.ResourceWine: 40,
	})
	h.conTaller(h.cityID, 70, tileTaller)

	h.investigar("ROMAN_ROADS")
	require.Empty(t, h.codigos())
	assert.Equal(t, int64(300), h.stockDe(economy.ResourceWood))
	assert.Equal(t, int64(300), h.stockDe(economy.ResourceStone))
	assert.Equal(t, int64(0), h.stockDe(economy.ResourceWine), "el bien propio, sólo en la exclusiva")
	require.Len(t, h.economy.research, 1)
	assert.Len(t, h.economy.research[0].Stocks, 3)
	h.tick()

	h.elaborar()
	assert.Empty(t, h.codigos(), "investigar no impide elaborar")
}
