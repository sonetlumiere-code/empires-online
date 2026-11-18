package technology_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/economy"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/player"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/production"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/technology"
)

var civs = []string{"ROMAN", "BYZANTINE", "PERSIAN", "NORSE"}

func wood(n int64) economy.Cost  { return economy.Cost{Resource: economy.ResourceWood, Amount: n} }
func stone(n int64) economy.Cost { return economy.Cost{Resource: economy.ResourceStone, Amount: n} }

// catalogoDeLaSpec es el de SPEC-TECHNOLOGIES §4.1, el que siembra 000027.
func catalogoDeLaSpec() []technology.Technology {
	comun := func(code, name string, era city.Era, eff string, v int32, w, s, foreign, ms int64, req ...string) technology.Technology {
		return technology.Technology{Code: code, Name: name, Era: era, Costs: economy.Costs{wood(w), stone(s)},
			ForeignGoods: foreign, DurationMs: ms, Requires: req, Effects: map[string]int32{eff: v}}
	}
	exclusiva := func(code, name, civ, eff string, v int32) technology.Technology {
		return technology.Technology{Code: code, Name: name, Era: city.EraIron, Civilization: civ,
			Costs: economy.Costs{wood(200), stone(200)}, OwnGood: 40, DurationMs: 180_000, Effects: map[string]int32{eff: v}}
	}
	return []technology.Technology{
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
	}
}

func catalogo(t *testing.T) technology.Catalog {
	t.Helper()
	cat, err := technology.NewCatalog(catalogoDeLaSpec(), civs)
	require.NoError(t, err)
	return cat
}

// SPEC-TECHNOLOGIES §4.4 e INV-TECH-002: la composición es la fórmula de
// cantidad en cada paso, en orden de código, con los ejemplos de la spec.
func TestLaComposicionDeLosEfectos(t *testing.T) {
	assert.Equal(t, int32(1100), technology.Compose(1000, 1100))
	assert.Equal(t, int32(1155), technology.Compose(1100, 1050), "1000 → 1100 → 1155")
	assert.Equal(t, int32(1210), technology.Compose(1100, 1100))
	assert.Equal(t, int32(1), technology.Compose(1, 1), "nunca baja de 1")
	assert.Equal(t, int32(1050), technology.Compose(0, 1050), "un rasgo ausente es 1000")
	assert.Equal(t, int32(950), technology.Compose(950, 0), "un efecto nulo no cambia nada")

	cat := catalogo(t)
	romano := player.Civilization{Code: "ROMAN", Good: economy.ResourceWine, Traits: map[string]int32{player.TraitProductionSpeed: 1100}}
	nordico := player.Civilization{Code: "NORSE", Good: economy.ResourceFurs, Traits: map[string]int32{player.TraitMoveSpeed: 1100}}

	rasgos := cat.Traits(romano, map[string]bool{"ROMAN_ROADS": true, "WHEEL": true})
	assert.Equal(t, int32(1155), rasgos[player.TraitMoveSpeed], "primero ROMAN_ROADS, luego WHEEL")
	assert.Equal(t, int64(519), player.ScaleDuration(600, rasgos[player.TraitMoveSpeed]), "600 ms por tile pasan a 519")
	assert.Equal(t, int32(1100), rasgos[player.TraitProductionSpeed], "el rasgo sin tecnologías que lo lleven")
	assert.Equal(t, int32(1000), rasgos[player.TraitAttackDamage], "una clave que nadie lleva")

	assert.Equal(t, int32(1155), cat.Traits(nordico, map[string]bool{"WHEEL": true})[player.TraitMoveSpeed])
	assert.Equal(t, int64(545), player.ScaleDuration(600, cat.Traits(nordico, nil)[player.TraitMoveSpeed]), "sin la rueda, 545")

	// Los techos de la spec: 1210 en velocidad y 1440 en cantidad.
	todas := map[string]bool{}
	for _, c := range cat.Codes() {
		todas[c] = true
	}
	neutra := player.Civilization{Code: "BYZANTINE"}
	assert.Equal(t, int32(1210), cat.Traits(neutra, todas)[player.TraitProductionSpeed])
	assert.Equal(t, int32(1440), cat.Traits(neutra, todas)[player.TraitHouseHP])
	assert.Equal(t, int32(1000), cat.Traits(neutra, map[string]bool{"NO_EXISTE": true})[player.TraitMoveSpeed],
		"una tecnología fuera del catálogo no cuenta")
}

// ADR-046 §1 e INV-TECH-002: un catálogo inválido no se acepta.
func TestLaValidacionDelCatalogo(t *testing.T) {
	assert.Equal(t, 12, catalogo(t).Len())
	casos := []struct {
		nombre  string
		cambiar func([]technology.Technology) []technology.Technology
	}{
		{"código repetido", func(ts []technology.Technology) []technology.Technology { return append(ts, ts[0]) }},
		{"sin código", func(ts []technology.Technology) []technology.Technology { ts[0].Code = ""; return ts }},
		{"era desconocida", func(ts []technology.Technology) []technology.Technology { ts[0].Era = "MEDIEVAL"; return ts }},
		{"plazo nulo", func(ts []technology.Technology) []technology.Technology { ts[0].DurationMs = 0; return ts }},
		{"clave desconocida", func(ts []technology.Technology) []technology.Technology {
			ts[0].Effects = map[string]int32{"economy.trade_yield": 1100}
			return ts
		}},
		{"efecto no positivo", func(ts []technology.Technology) []technology.Technology {
			ts[0].Effects = map[string]int32{player.TraitGatherSpeed: 0}
			return ts
		}},
		{"sin efectos", func(ts []technology.Technology) []technology.Technology { ts[0].Effects = nil; return ts }},
		{"un bien como coste fijo", func(ts []technology.Technology) []technology.Technology {
			ts[0].Costs = economy.Costs{{Resource: economy.ResourceWine, Amount: 5}}
			return ts
		}},
		{"requisito que no existe", func(ts []technology.Technology) []technology.Technology {
			ts[6].Requires = []string{"POLVORA"}
			return ts
		}},
		{"requisito de una era posterior", func(ts []technology.Technology) []technology.Technology {
			ts[0].Requires = []string{"BRONZE_WEAPONS"}
			return ts
		}},
		{"requisito exclusivo de otra civilización", func(ts []technology.Technology) []technology.Technology {
			ts[6].Requires = []string{"ROMAN_ROADS"}
			return ts
		}},
		{"ciclo", func(ts []technology.Technology) []technology.Technology {
			ts[2].Requires = []string{"MASONRY"}
			ts[3].Requires = []string{"BRONZE_WEAPONS"}
			return ts
		}},
		{"exclusiva de una civilización desconocida", func(ts []technology.Technology) []technology.Technology {
			ts[8].Civilization = "AZTEC"
			return ts
		}},
		{"exclusiva sin bien propio", func(ts []technology.Technology) []technology.Technology { ts[8].OwnGood = 0; return ts }},
		{"exclusiva con bienes ajenos", func(ts []technology.Technology) []technology.Technology { ts[8].ForeignGoods = 10; return ts }},
		{"común con bien propio", func(ts []technology.Technology) []technology.Technology { ts[0].OwnGood = 10; return ts }},
	}
	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			_, err := technology.NewCatalog(c.cambiar(catalogoDeLaSpec()), civs)
			assert.ErrorIs(t, err, technology.ErrInvalidCatalog)
		})
	}
}

// almacen es un almacén de prueba.
func almacen(cantidades map[economy.Resource]int64) func(economy.Resource) *economy.Stock {
	return func(r economy.Resource) *economy.Stock {
		n, ok := cantidades[r]
		if !ok {
			return nil
		}
		return &economy.Stock{Resource: r, Amount: n}
	}
}

// RN-TECH-002 a RN-TECH-004 e INV-TECH-001: cada regla de la orden, en el orden
// de la spec, y el coste resuelto.
func TestLaOrdenDeInvestigar(t *testing.T) {
	cat := catalogo(t)
	dueno := uuid.New()
	romano := player.Civilization{Code: "ROMAN", Good: economy.ResourceWine}
	orden := []economy.Resource{economy.ResourceWine, economy.ResourceSilk, economy.ResourceSpices, economy.ResourceFurs}
	lleno := almacen(map[economy.Resource]int64{
		economy.ResourceWood: 5000, economy.ResourceStone: 5000, economy.ResourceWine: 100,
		economy.ResourceSilk: 20, economy.ResourceSpices: 50,
	})
	ciudad := func(era city.Era) *city.City { return &city.City{ID: 1, OwnerPlayerID: dueno, Era: era} }
	pedir := func(c *city.City, quien uuid.UUID, code string, tiene map[string]bool, busy bool, st func(economy.Resource) *economy.Stock) (technology.Order, error) {
		return technology.CanResearch(c, quien, code, cat, romano, tiene, busy, orden, st)
	}

	_, err := pedir(nil, dueno, "STONE_TOOLS", nil, false, lleno)
	assert.ErrorIs(t, err, production.ErrCityNotFound)
	_, err = pedir(ciudad(city.EraStone), uuid.New(), "STONE_TOOLS", nil, false, lleno)
	assert.ErrorIs(t, err, technology.ErrCityNotFound, "una ciudad ajena")
	_, err = pedir(ciudad(city.EraCastle), dueno, "POLVORA", nil, false, lleno)
	assert.ErrorIs(t, err, technology.ErrUnavailable)
	_, err = pedir(ciudad(city.EraCastle), dueno, "RAIDING", nil, false, lleno)
	assert.ErrorIs(t, err, technology.ErrUnavailable, "la exclusiva de otra civilización")
	_, err = pedir(ciudad(city.EraStone), dueno, "STONE_TOOLS", map[string]bool{"STONE_TOOLS": true}, false, lleno)
	assert.ErrorIs(t, err, technology.ErrOwned)
	assert.Contains(t, err.Error(), "Herramientas de piedra")
	_, err = pedir(ciudad(city.EraStone), dueno, "BRONZE_WEAPONS", nil, false, lleno)
	assert.ErrorIs(t, err, technology.ErrEraRequired)
	assert.Contains(t, err.Error(), "Armas de bronce pide la Edad de Bronce")
	_, err = pedir(ciudad(city.EraCastle), dueno, "IRON_WEAPONS", nil, false, lleno)
	assert.ErrorIs(t, err, technology.ErrRequirement)
	assert.Contains(t, err.Error(), "Armas de hierro pide Armas de bronce")
	_, err = pedir(ciudad(city.EraStone), dueno, "STONE_TOOLS", nil, true, lleno)
	assert.ErrorIs(t, err, technology.ErrCityBusy, "ya investiga o avanza")
	_, err = pedir(ciudad(city.EraStone), dueno, "STONE_TOOLS", nil, false,
		almacen(map[economy.Resource]int64{economy.ResourceWood: 149, economy.ResourceStone: 50}))
	assert.ErrorIs(t, err, economy.ErrInsufficientResources)

	// Una común de Piedra: madera y piedra.
	o, err := pedir(ciudad(city.EraBronze), dueno, "STONE_TOOLS", nil, false, lleno)
	require.NoError(t, err)
	assert.Equal(t, economy.Costs{wood(150), stone(50)}, o.Costs)
	assert.Equal(t, int64(60_000), o.Technology.DurationMs)

	// Una de Hierro: los bienes ajenos en el orden del catálogo, nunca el propio.
	o, err = pedir(ciudad(city.EraIron), dueno, "WHEEL", nil, false, lleno)
	require.NoError(t, err)
	assert.Equal(t, economy.Costs{wood(300), stone(200),
		{Resource: economy.ResourceSilk, Amount: 20}, {Resource: economy.ResourceSpices, Amount: 10}}, o.Costs)
	o, err = pedir(ciudad(city.EraCastle), dueno, "ARCHITECTURE", map[string]bool{"GUILDS": true}, false, lleno)
	require.NoError(t, err)
	assert.Equal(t, economy.Costs{wood(400), stone(400),
		{Resource: economy.ResourceSilk, Amount: 20}, {Resource: economy.ResourceSpices, Amount: 40}}, o.Costs)
	_, err = pedir(ciudad(city.EraCastle), dueno, "ARCHITECTURE", map[string]bool{"GUILDS": true}, false,
		almacen(map[economy.Resource]int64{economy.ResourceWood: 500, economy.ResourceStone: 500,
			economy.ResourceWine: 500, economy.ResourceSilk: 59}))
	assert.ErrorIs(t, err, economy.ErrInsufficientResources, "el vino propio no cuenta como ajeno")

	// La exclusiva: su bien propio.
	o, err = pedir(ciudad(city.EraIron), dueno, "ROMAN_ROADS", nil, false, lleno)
	require.NoError(t, err)
	assert.Equal(t, economy.Costs{wood(200), stone(200), {Resource: economy.ResourceWine, Amount: 40}}, o.Costs)
	_, err = pedir(ciudad(city.EraIron), dueno, "ROMAN_ROADS", nil, false,
		almacen(map[economy.Resource]int64{economy.ResourceWood: 500, economy.ResourceStone: 500, economy.ResourceWine: 39}))
	assert.ErrorIs(t, err, economy.ErrInsufficientResources)
}
