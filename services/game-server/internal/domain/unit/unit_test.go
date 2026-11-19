package unit_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/economy"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
)

// INV-UNIT-002 e INV-UNIT-003: el estado decide, en un único punto, si una
// unidad acepta una orden de movimiento. Tabla exhaustiva sobre los seis
// estados del esquema y uno que no existe.
func TestEnsureCanMoveDecidePorEstado(t *testing.T) {
	cases := []struct {
		status unit.Status
		want   error
	}{
		{unit.StatusIdle, nil},
		{unit.StatusMoving, nil},  // la orden nueva reemplaza a la anterior
		{unit.StatusHidden, nil},  // moverse revela, no está prohibido
		{unit.StatusWorking, nil}, // la orden cancela la tarea y pierde la carga (RN-ECON-003)
		{unit.StatusDead, unit.ErrDead},
		{unit.StatusGarrisoned, unit.ErrGarrisoned},
		{unit.Status("WALKING"), unit.ErrNotMovable},
	}
	for _, c := range cases {
		t.Run(string(c.status), func(t *testing.T) {
			u := &unit.Unit{Status: c.status, HP: 40, MaxHP: 40}
			err := u.EnsureCanMove()
			if c.want == nil {
				assert.NoError(t, err)
				return
			}
			assert.ErrorIs(t, err, c.want)
		})
	}
}

// INV-UNIT-004: una unidad sin vida no cuenta como viva aunque su estado se
// haya quedado atrás, y una DEAD no lo está aunque conserve vida.
func TestIsAliveExigeEstadoYVida(t *testing.T) {
	assert.True(t, (&unit.Unit{Status: unit.StatusIdle, HP: 1}).IsAlive())
	assert.False(t, (&unit.Unit{Status: unit.StatusIdle, HP: 0}).IsAlive())
	assert.False(t, (&unit.Unit{Status: unit.StatusDead, HP: 40}).IsAlive())
}

// INV-UNIT-006: la propiedad se compara por valor; ni el uuid nulo ni otro
// jugador pasan por el dueño.
func TestEnsureOwnedByComparaElDueno(t *testing.T) {
	dueno := uuid.New()
	u := &unit.Unit{PlayerID: dueno}
	assert.NoError(t, u.EnsureOwnedBy(dueno))
	assert.ErrorIs(t, u.EnsureOwnedBy(uuid.New()), unit.ErrNotOwned)
	assert.ErrorIs(t, u.EnsureOwnedBy(uuid.Nil), unit.ErrNotOwned)
}

// INV-UNIT-009: el catálogo sólo conoce sus tipos. Un tipo inventado no
// resuelve a ninguna definición por defecto.
func TestLookupSoloConoceElCatalogo(t *testing.T) {
	def, err := unit.Lookup(unit.TypeVillager)
	require.NoError(t, err)
	assert.Equal(t, unit.TypeVillager, def.Type)
	assert.Equal(t, int32(40), def.MaxHP)
	assert.Equal(t, int64(600), def.BaseMsPerTile)
	assert.Equal(t, int32(1), def.PopulationCost)

	for _, raro := range []unit.Type{"villager", "", "TOWN_CENTER", "KNIGHT"} {
		_, err := unit.Lookup(raro)
		assert.ErrorIs(t, err, unit.ErrUnknownType, "%q", raro)
	}
}

func TestLosEstadosDelDominioSonLosDelEsquema(t *testing.T) {
	// El CHECK units_status_valid admite exactamente estos seis (000006 añade
	// WORKING).
	for _, s := range []unit.Status{unit.StatusIdle, unit.StatusMoving, unit.StatusGarrisoned, unit.StatusHidden, unit.StatusWorking, unit.StatusDead} {
		assert.True(t, s.Valid(), "%s", s)
	}
	for _, s := range []unit.Status{"idle", "", "WALKING"} {
		assert.False(t, s.Valid(), "%q", s)
	}
}

// SPEC-ECON-WOOD §4.5: los valores de equilibrio del aldeano viven en su
// definición. Si cambian, cambian aquí y en ningún otro sitio.
func TestElAldeanoRecolectaYSeEntrenaConLosValoresDelCatalogo(t *testing.T) {
	def, err := unit.Lookup(unit.TypeVillager)
	require.NoError(t, err)
	assert.True(t, def.CanGather(), "RN-ECON-001: el aldeano recolecta")
	assert.True(t, def.CanTrain(), "RN-ECON-010: el aldeano se entrena")
	assert.Equal(t, int64(10_000), def.GatherWorkMs)
	assert.Equal(t, int32(10), def.GatherCarry)
	assert.Equal(t, economy.Costs{{Resource: economy.ResourceWood, Amount: 50}}, def.TrainCosts)
	assert.Equal(t, int64(20_000), def.TrainMs)
	assert.False(t, unit.Definition{}.CanGather())
	assert.False(t, unit.Definition{}.CanTrain())
}

// SPEC-CIVILIZATION-UNITS §4.2 (Q3, Q5): los cuatro perfiles, su coste, su plazo
// y su edad viven en el catálogo, y las cuatro atacan.
func TestLasUnidadesDeCivilizacionTienenSuPerfil(t *testing.T) {
	type perfil struct {
		hp, damage, rango, loot int32
		paso                    int64
	}
	for tipo, p := range map[unit.Type]perfil{
		unit.TypeLegionary:   {hp: 110, damage: 8, rango: 1, loot: 20, paso: 600},
		unit.TypeCataphract:  {hp: 80, damage: 10, rango: 1, loot: 20, paso: 450},
		unit.TypeHorseArcher: {hp: 60, damage: 8, rango: 3, loot: 20, paso: 540},
		unit.TypeBerserker:   {hp: 60, damage: 14, rango: 1, loot: 30, paso: 600},
	} {
		def, err := unit.Lookup(tipo)
		require.NoError(t, err)
		assert.True(t, def.Exclusive, "%s", tipo)
		assert.True(t, def.CanAttack(), "%s ataca", tipo)
		assert.True(t, def.CanTrain(), "%s se entrena", tipo)
		assert.False(t, def.CanGather(), "%s no recolecta", tipo)
		assert.Equal(t, p.hp, def.MaxHP, "%s", tipo)
		assert.Equal(t, p.damage, def.AttackDamage, "%s", tipo)
		assert.Equal(t, int64(1_500), def.AttackIntervalMs, "%s", tipo)
		assert.Equal(t, p.rango, def.AttackRange, "%s", tipo)
		assert.Equal(t, p.loot, def.LootCarry, "%s", tipo)
		assert.Equal(t, p.paso, def.BaseMsPerTile, "%s", tipo)
		assert.Equal(t, int32(1), def.PopulationCost, "%s", tipo)
		assert.Equal(t, int64(35_000), def.TrainMs, "%s", tipo)
		assert.Equal(t, int64(10), def.OwnGoodCost, "%s", tipo)
		assert.Equal(t, city.EraBronze, def.RequiresEra, "%s", tipo)
		assert.Equal(t, economy.Costs{
			{Resource: economy.ResourceWood, Amount: 80},
			{Resource: economy.ResourceStone, Amount: 40},
			{Resource: economy.ResourceSilk, Amount: 10},
		}, def.CostsWith(economy.ResourceSilk), "%s paga el bien que le den", tipo)
	}
	for _, comun := range []unit.Type{unit.TypeVillager, unit.TypeWarrior, unit.TypeCaravan} {
		def, err := unit.Lookup(comun)
		require.NoError(t, err)
		assert.False(t, def.Exclusive, "%s no es exclusiva", comun)
		assert.Empty(t, def.RequiresEra, "%s no pide era", comun)
		assert.Zero(t, def.OwnGoodCost, "%s no paga bien", comun)
		assert.Equal(t, append(economy.Costs{}, def.TrainCosts...), def.CostsWith(economy.ResourceWine),
			"%s: sin bien, el coste no cambia", comun)
	}
	assert.Len(t, unit.Types(), 8, "los siete de E22 y el ariete de E27")
}

// CostsWith no toca el catálogo: dos llamadas con bienes distintos no se pisan.
func TestCostsWithDevuelveUnaListaNueva(t *testing.T) {
	def, err := unit.Lookup(unit.TypeLegionary)
	require.NoError(t, err)
	a := def.CostsWith(economy.ResourceWine)
	b := def.CostsWith(economy.ResourceFurs)
	a[0].Amount = 1
	assert.Equal(t, economy.ResourceFurs, b[2].Resource)
	again, _ := unit.Lookup(unit.TypeLegionary)
	assert.Equal(t, int64(80), again.TrainCosts[0].Amount)
}

// RN-SIEGE-003 e INV-COMBAT-009: el ariete, con los valores de
// SPEC-SIEGE-ENGINES §4.2. Derriba con su golpe de asedio, sin golpe de combate
// ni botín; no es exclusivo de ninguna civilización.
func TestElArieteSoloGolpeaEdificios(t *testing.T) {
	def, err := unit.Lookup(unit.TypeRam)
	require.NoError(t, err)
	assert.Equal(t, int32(250), def.MaxHP)
	assert.Equal(t, int64(1_800), def.BaseMsPerTile)
	assert.Equal(t, int32(2), def.PopulationCost)
	assert.Equal(t, economy.Costs{
		{Resource: economy.ResourceWood, Amount: 150},
		{Resource: economy.ResourceStone, Amount: 75},
	}, def.TrainCosts)
	assert.Equal(t, int64(40_000), def.TrainMs)
	assert.Equal(t, city.EraIron, def.RequiresEra)
	assert.False(t, def.Exclusive)
	assert.True(t, def.CanTrain())
	assert.False(t, def.CanAttack(), "sin golpe de combate")
	assert.False(t, def.CanGather())
	assert.Zero(t, def.LootCarry, "sin botín")
	assert.True(t, def.CanDemolish())
	assert.True(t, def.IsSiegeEngine())
	dano, ritmo := def.SiegeHit()
	assert.Equal(t, [2]int64{40, 3_000}, [2]int64{int64(dano), ritmo})
	assert.Contains(t, unit.Types(), unit.TypeRam)
}

// ADR-048 §3: quien ataca derriba con su golpe de combate; nadie más es una
// máquina de asedio.
func TestElGolpeDeAsedioDeQuienAtaca(t *testing.T) {
	for _, tp := range unit.Types() {
		def, err := unit.Lookup(tp)
		require.NoError(t, err)
		if tp == unit.TypeRam {
			continue
		}
		assert.False(t, def.IsSiegeEngine(), "%s", tp)
		assert.Equal(t, def.CanAttack(), def.CanDemolish(), "%s derriba si ataca", tp)
		dano, ritmo := def.SiegeHit()
		assert.Equal(t, def.AttackDamage, dano, "%s", tp)
		assert.Equal(t, def.AttackIntervalMs, ritmo, "%s", tp)
	}
}
