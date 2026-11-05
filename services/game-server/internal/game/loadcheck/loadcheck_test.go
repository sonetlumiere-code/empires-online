package loadcheck_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/building"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/player"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/production"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/task"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/territory"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/loadcheck"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
)

var instante = time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)

// mundo es un mapa de 64 × 64 de pradera con una franja de agua en x = 50.
func mundo(t *testing.T) *world.World {
	t.Helper()
	terrain := make([]byte, 64*64)
	for y := 0; y < 64; y++ {
		terrain[y*64+50] = byte(world.Water)
	}
	w, err := world.New(64, 64, 32, 1, terrain)
	require.NoError(t, err)
	return w
}

// sano es un estado sin ninguna violación: una ciudad con sus tres aldeanos,
// un territorio con dueño y otro libre.
func sano(t *testing.T) (loadcheck.Input, *city.City, []*unit.Unit) {
	t.Helper()
	dueno := uuid.New()
	ciudad := &city.City{ID: 1, OwnerPlayerID: dueno, CenterX: 20, CenterY: 20, Era: city.EraStone,
		Population: 3, PopulationLimit: 20, PresenceState: city.PresenceOnline, Civilization: "NORSE"}
	var aldeanos []*unit.Unit
	for i := int64(1); i <= 3; i++ {
		aldeanos = append(aldeanos, &unit.Unit{ID: i, PlayerID: dueno, CityID: &ciudad.ID, Type: unit.TypeVillager,
			X: 22, Y: int32(18 + i), HP: 40, MaxHP: 40, Status: unit.StatusIdle})
	}
	captura := instante.Add(-time.Hour)
	id := dueno.String()
	return loadcheck.Input{
		World:   mundo(t),
		EraCaps: map[city.Era]int32{city.EraStone: 20},
		Civilizations: []player.Civilization{
			{ID: 4, Code: "NORSE", Traits: map[string]int32{player.TraitMoveSpeed: 1100, player.TraitLootCarry: 1150}},
		},
		Players:   map[uuid.UUID]struct{}{dueno: {}},
		Cities:    []*city.City{ciudad},
		Units:     aldeanos,
		Garrisons: map[int64]int64{},
		Controls: []territory.Control{
			{TerritoryID: 7, OwnerType: territory.OwnerPlayer, OwnerID: &id, CapturedAt: &captura, Version: 2},
			{TerritoryID: 8, OwnerType: territory.OwnerNone},
		},
		Now: instante,
	}, ciudad, aldeanos
}

func invs(r loadcheck.Result) []string {
	out := make([]string, 0, len(r.Findings))
	for _, f := range r.Findings {
		out = append(out, f.InvID+"/"+f.Policy)
	}
	return out
}

func TestUnEstadoSanoNoProduceHallazgos(t *testing.T) {
	in, _, aldeanos := sano(t)
	r := loadcheck.Check(in)
	assert.Empty(t, r.Findings)
	assert.Len(t, r.Units, len(aldeanos))
	assert.False(t, r.Fatal())
	assert.Empty(t, r.CityRepairs)
	assert.Empty(t, r.UnitRepairs)
	assert.Empty(t, r.ControlRepairs)
	assert.Empty(t, r.GarrisonDeletes)
}

// INV-PLAYER-002 (SPEC-CIVILIZATION): una ciudad sin una civilización del
// catálogo aborta el arranque; tener rasgos neutros sería una civilización por
// defecto asignada en silencio.
func TestUnaCiudadSinCivilizacionDelCatalogoAbortaElArranque(t *testing.T) {
	for _, codigo := range []string{"", "AZTEC"} {
		in, ciudad, _ := sano(t)
		ciudad.Civilization = codigo
		r := loadcheck.Check(in)
		assert.Equal(t, []string{"INV-PLAYER-002/FAIL_FAST"}, invs(r), "civilización %q", codigo)
		assert.True(t, r.Fatal())
	}
}

// INV-PLAYER-012: un rasgo que no se puede aplicar aborta el arranque.
func TestUnRasgoInvalidoEnElCatalogoAbortaElArranque(t *testing.T) {
	in, _, _ := sano(t)
	in.Civilizations[0].Traits[player.TraitHouseHP] = 0
	r := loadcheck.Check(in)
	assert.Equal(t, []string{"INV-PLAYER-012/FAIL_FAST"}, invs(r))
	assert.True(t, r.Fatal())
}

// INV-CITY-003, 017, 011 y 012: se reparan en la ciudad en memoria.
func TestLasCiudadesSeReparanDesdeSusFuentes(t *testing.T) {
	in, ciudad, _ := sano(t)
	ciudad.Population = 9
	ciudad.PopulationLimit = 99
	ciudad.PresenceState = city.PresenceOfflinePending

	r := loadcheck.Check(in)

	assert.Equal(t, []string{"INV-CITY-017/REPAIR", "INV-CITY-003/REPAIR", "INV-CITY-011/REPAIR"}, invs(r))
	assert.EqualValues(t, 3, ciudad.Population, "el recuento de unidades vivas")
	assert.EqualValues(t, 20, ciudad.PopulationLimit, "el de su era")
	require.NotNil(t, ciudad.LastOfflineAt)
	assert.Equal(t, instante, *ciudad.LastOfflineAt, "se fecha en la detección")
	assert.Equal(t, []int64{1}, r.CityRepairs, "una sola escritura por ciudad")

	in2, online, _ := sano(t)
	futuro := instante.Add(time.Hour)
	online.ProtectionUntil = &futuro
	r2 := loadcheck.Check(in2)
	assert.Equal(t, []string{"INV-CITY-012/REPAIR"}, invs(r2))
	assert.Nil(t, online.ProtectionUntil)
}

// INV-CITY-002 (ADR-029): si las unidades vivas superan el límite de la era
// no se destruye ninguna, y el límite tampoco sube hasta el recuento: queda en
// el de su era y la ciudad no crea nada hasta volver bajo él.
func TestUnaCiudadPorEncimaDelLimiteDeSuEraNoPierdeUnidades(t *testing.T) {
	in, ciudad, _ := sano(t)
	in.EraCaps[city.EraStone] = 2

	r := loadcheck.Check(in)

	assert.Equal(t, []string{"INV-CITY-003/REPAIR"}, invs(r))
	assert.EqualValues(t, 2, ciudad.PopulationLimit, "el límite es el de la era, no el recuento")
	assert.EqualValues(t, 3, ciudad.Population)
	assert.False(t, ciudad.HasPopulationRoom(1), "ninguna creación cabe hasta volver bajo el límite")
	assert.Len(t, r.Units, 3)
}

// ADR-029 §1: una ciudad que perdió casas y quedó por encima de su límite es
// legal. Con el límite ya en el de su era y sus casas, la carga no encuentra
// nada: ni sube el límite ni toca la población.
func TestUnaCiudadPorEncimaDeSuLimiteNoEsUnHallazgo(t *testing.T) {
	in, ciudad, _ := sano(t)
	in.EraCaps[city.EraStone] = 2
	ciudad.PopulationLimit = 2

	r := loadcheck.Check(in)

	assert.Empty(t, invs(r))
	assert.Empty(t, r.CityRepairs)
	assert.EqualValues(t, 2, ciudad.PopulationLimit)
	assert.EqualValues(t, 3, ciudad.Population)
	assert.Len(t, r.Units, 3)
}

// INV-CITY-007: un centro sobre terreno imposible aborta el arranque.
func TestUnCentroSobreAguaAbortaElArranque(t *testing.T) {
	in, ciudad, _ := sano(t)
	ciudad.CenterX = 50
	r := loadcheck.Check(in)
	assert.Equal(t, []string{"INV-CITY-007/FAIL_FAST"}, invs(r))
	assert.True(t, r.Fatal())
}

// INV-UNIT-009, 004 y 001.
func TestLasUnidadesSeExcluyenSeReparanOAbortan(t *testing.T) {
	in, ciudad, aldeanos := sano(t)
	aldeanos[0].Type = "KNIGHT"
	aldeanos[1].MaxHP, aldeanos[1].HP = 60, 55
	aldeanos[2].X = 50 // agua

	r := loadcheck.Check(in)

	assert.Equal(t, []string{"INV-UNIT-009/EXCLUDE", "INV-UNIT-004/REPAIR", "INV-UNIT-001/FAIL_FAST"}, invs(r))
	require.Len(t, r.Units, 2, "la de tipo desconocido no entra")
	assert.NotContains(t, r.Units, aldeanos[0])
	assert.EqualValues(t, 40, aldeanos[1].MaxHP)
	assert.EqualValues(t, 40, aldeanos[1].HP, "la vida se recorta al máximo del catálogo")
	assert.Equal(t, []int64{2}, r.UnitRepairs)
	assert.EqualValues(t, 3, ciudad.Population, "la excluida sigue existiendo y consumiendo población")
	assert.True(t, r.Fatal())
}

// INV-UNIT-007: garrisons manda sobre el estado, en los dos sentidos, y una
// fila de una unidad que no está viva se borra.
func TestLaGuarnicionSeDerivaDeSuTabla(t *testing.T) {
	in, _, aldeanos := sano(t)
	aldeanos[0].Status = unit.StatusGarrisoned // sin fila
	in.Garrisons[aldeanos[1].ID] = 1           // con fila e IDLE
	aldeanos[1].X = 50                         // su posición histórica no importa
	in.Garrisons[999] = 1                      // de una unidad que no está viva

	r := loadcheck.Check(in)

	assert.Equal(t, []string{"INV-UNIT-007/REPAIR", "INV-UNIT-007/REPAIR", "INV-UNIT-007/REPAIR"}, invs(r))
	assert.Equal(t, unit.StatusIdle, aldeanos[0].Status)
	assert.Equal(t, unit.StatusGarrisoned, aldeanos[1].Status)
	assert.Equal(t, []int64{1, 2}, r.UnitRepairs)
	assert.Equal(t, []int64{999}, r.GarrisonDeletes)
	assert.False(t, r.Fatal(), "una guarnecida no ocupa tile: su posición no aborta")
}

// INV-TERR-005 e INV-TERR-007, en orden de territorio.
func TestLosControlesSeReparan(t *testing.T) {
	in, _, _ := sano(t)
	fantasma := uuid.NewString()
	in.Controls[0].OwnerID = &fantasma
	clanID := "clan-1"
	in.Controls = append(in.Controls, territory.Control{TerritoryID: 3, OwnerType: territory.OwnerClan,
		OwnerID: &clanID, Version: 5})

	r := loadcheck.Check(in)

	assert.Equal(t, []string{"INV-TERR-007/REPAIR", "INV-TERR-005/REPAIR"}, invs(r))
	require.Len(t, r.Controls, 3)
	clan, liberado := r.Controls[0], r.Controls[1]
	assert.Equal(t, int64(3), clan.TerritoryID)
	require.NotNil(t, clan.CapturedAt)
	assert.Equal(t, instante, *clan.CapturedAt)
	assert.EqualValues(t, 6, clan.Version)
	assert.Equal(t, territory.OwnerNone, liberado.OwnerType)
	assert.Nil(t, liberado.OwnerID)
	assert.Nil(t, liberado.CapturedAt)
	assert.NoError(t, liberado.Validate(), "el control liberado es válido")
	assert.EqualValues(t, 3, liberado.Version)
	assert.Equal(t, []int64{3, 7}, r.ControlRepairs)
	assert.Equal(t, territory.OwnerPlayer, in.Controls[0].OwnerType, "la entrada no se toca")
}

func TestLaMismaEntradaDaLosMismosHallazgos(t *testing.T) {
	for i := 0; i < 5; i++ {
		in, ciudad, aldeanos := sano(t)
		ciudad.Population = 0
		aldeanos[2].Type = "X"
		in.Garrisons[42] = 1
		r := loadcheck.Check(in)
		assert.Equal(t, []string{"INV-UNIT-009/EXCLUDE", "INV-UNIT-007/REPAIR", "INV-CITY-017/REPAIR"}, invs(r))
	}
}

// INV-WORLD-001: una coordenada persistida fuera del mundo, de unidad o de
// centro de ciudad, aborta el arranque. La base no la impide.
func TestUnaEntidadFueraDelMundoAbortaElArranque(t *testing.T) {
	in, ciudad, aldeanos := sano(t)
	aldeanos[0].X = -1
	ciudad.CenterY = 64

	r := loadcheck.Check(in)

	assert.Equal(t, []string{"INV-UNIT-001/FAIL_FAST", "INV-CITY-007/FAIL_FAST"}, invs(r))
	assert.True(t, r.Fatal())
}

// INV-ECON-004: con casas completas, el límite es la era más cinco por casa;
// las que están en obras no cuentan. Sin casas, el hallazgo sigue siendo
// INV-CITY-003.
func TestElLimiteDePoblacionCuentaLasCasasCompletas(t *testing.T) {
	in, ciudad, _ := sano(t)
	in.Buildings = []building.Building{
		{ID: 1, CityID: ciudad.ID, Type: building.TypeHouse, X: 19, Y: 19, Status: building.StatusComplete},
		{ID: 2, CityID: ciudad.ID, Type: building.TypeHouse, X: 21, Y: 19, Status: building.StatusComplete},
		{ID: 3, CityID: ciudad.ID, Type: building.TypeHouse, X: 19, Y: 21, Status: building.StatusUnderConstruction},
	}
	r := loadcheck.Check(in)
	assert.Equal(t, []string{"INV-ECON-004/REPAIR"}, invs(r))
	assert.EqualValues(t, 30, ciudad.PopulationLimit)

	ciudad.PopulationLimit = 30
	assert.Empty(t, loadcheck.Check(in).Findings, "el límite con casas ya es el correcto")
}

// INV-CITY-017: la población cuenta las producciones en curso, no las
// terminadas (cuya unidad ya está entre las vivas).
func TestLaPoblacionCuentaLasProduccionesEnCurso(t *testing.T) {
	in, ciudad, _ := sano(t)
	in.Productions = []production.Production{{CityID: ciudad.ID, UnitType: unit.TypeVillager, CompletesAtMs: 1}}
	r := loadcheck.Check(in)
	assert.Equal(t, []string{"INV-CITY-017/REPAIR"}, invs(r))
	assert.EqualValues(t, 4, ciudad.Population, "tres aldeanos y una plaza reservada")

	in2, ciudad2, _ := sano(t)
	in2.Productions = []production.Production{{CityID: ciudad2.ID, UnitType: unit.TypeVillager, UnitID: 3}}
	assert.Empty(t, loadcheck.Check(in2).Findings, "una producción terminada no reserva nada")
}

// INV-ECON-003: se repara siempre hacia el lado sin tarea.
func TestLasTareasIncoherentesSeReparanHaciaElLadoSinTarea(t *testing.T) {
	in, _, aldeanos := sano(t)
	bosque := world.Tile{X: 30, Y: 30}
	trabajando, err := task.NewGather(2, bosque).StartWork(1, 10_000)
	require.NoError(t, err)
	aldeanos[0].Status = unit.StatusWorking // WORKING sin tarea
	in.Tasks = []task.Task{
		trabajando,                 // tarea en WORKING de una unidad IDLE
		task.NewGather(3, bosque),  // un tramo: lo resuelve la rehidratación
		task.NewGather(99, bosque), // de una unidad que no existe
	}

	r := loadcheck.Check(in)
	assert.ElementsMatch(t, []string{"INV-ECON-003/REPAIR", "INV-ECON-003/REPAIR", "INV-ECON-003/REPAIR"}, invs(r))
	assert.Equal(t, []int64{2, 99}, r.TaskDeletes)
	assert.Equal(t, unit.StatusIdle, aldeanos[0].Status)
	assert.Equal(t, []int64{1}, r.UnitRepairs)
	require.Len(t, r.Tasks, 1)
	assert.Equal(t, int64(3), r.Tasks[0].UnitID)
}

// Una unidad WORKING con su tarea en WORKING es coherente.
func TestUnaUnidadQueTrabajaConSuTareaEsCoherente(t *testing.T) {
	in, _, aldeanos := sano(t)
	trabajando, _ := task.NewGather(1, world.Tile{X: 22, Y: 19}).StartWork(1, 10_000)
	aldeanos[0].Status = unit.StatusWorking
	in.Tasks = []task.Task{trabajando}
	r := loadcheck.Check(in)
	assert.Empty(t, r.Findings)
	assert.Len(t, r.Tasks, 1)
}

// INV-ECON-004 (SPEC-TERRITORY-CONQUEST §4.2): el límite suma 5 por territorio
// conquistado, y la capital —el territorio con el centro de la ciudad— no
// cuenta.
func TestElLimiteDePoblacionCuentaLosTerritoriosConquistados(t *testing.T) {
	in, ciudad, _ := sano(t)
	in.Territories = []territory.Territory{
		{ID: 7, MinX: 0, MinY: 0, MaxX: 31, MaxY: 31},  // la capital: contiene (20,20)
		{ID: 8, MinX: 32, MinY: 0, MaxX: 63, MaxY: 31}, // libre
		{ID: 9, MinX: 0, MinY: 32, MaxX: 31, MaxY: 63}, // conquistado
	}
	id := ciudad.OwnerPlayerID.String()
	captura := instante.Add(-time.Minute)
	in.Controls = append(in.Controls, territory.Control{TerritoryID: 9, OwnerType: territory.OwnerPlayer,
		OwnerID: &id, CapturedAt: &captura, Version: 1})

	r := loadcheck.Check(in)
	assert.Equal(t, []string{"INV-ECON-004/REPAIR"}, invs(r))
	assert.EqualValues(t, 25, ciudad.PopulationLimit, "la era más cinco por el conquistado, nada por la capital")

	ciudad.PopulationLimit = 25
	assert.Empty(t, loadcheck.Check(in).Findings, "el límite con el territorio ya es el correcto")
}

// INV-TERR-006 (ADR-034 §4): contested es «hay una reclamación activa»; la carga
// lo alinea en los dos sentidos.
func TestContestedSeAlineaConLasReclamacionesActivas(t *testing.T) {
	in, _, _ := sano(t)
	in.Controls[0].Contested = true // el 7, sin reclamación
	in.ActiveClaims = []territory.Claim{{ID: 1, TerritoryID: 8}}

	r := loadcheck.Check(in)
	assert.Equal(t, []string{"INV-TERR-006/REPAIR", "INV-TERR-006/REPAIR"}, invs(r))
	require.Len(t, r.Controls, 2)
	assert.False(t, r.Controls[0].Contested)
	assert.True(t, r.Controls[1].Contested)
	assert.Equal(t, []int64{7, 8}, r.ControlRepairs)
	assert.EqualValues(t, 3, r.Controls[0].Version, "una reparación sube la versión")
}
