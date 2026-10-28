package task

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
)

var (
	dueno  = uuid.MustParse("11111111-1111-4111-8111-111111111111")
	bosque = world.Tile{X: 20, Y: 10}
)

// RN-ECON-003 y RN-ECON-004: el ciclo completo, fase a fase, con el
// vencimiento anclado al instante de llegada y no al tick que la observa.
func TestElCicloDeRecoleccionFaseAFase(t *testing.T) {
	tk := NewGather(1, bosque)
	assert.Equal(t, PhaseToResource, tk.Phase)
	require.NoError(t, tk.Validate())

	tk, err := tk.StartWork(5_030, 10_000)
	require.NoError(t, err)
	assert.Equal(t, PhaseWorking, tk.Phase)
	assert.Equal(t, int64(15_030), tk.PhaseUntilMs)
	assert.False(t, tk.Due(15_029))
	assert.True(t, tk.Due(15_030))
	require.NoError(t, tk.Validate())

	tk, err = tk.FinishWork(10)
	require.NoError(t, err)
	assert.Equal(t, PhaseToDeposit, tk.Phase)
	assert.Equal(t, int64(0), tk.PhaseUntilMs)
	assert.Equal(t, int32(10), tk.Carrying)
	assert.False(t, tk.Due(99_999), "un tramo no vence: lo cierra su llegada")

	tk, entregado, err := tk.Deposit()
	require.NoError(t, err)
	assert.Equal(t, int32(10), entregado)
	assert.Equal(t, PhaseToResource, tk.Phase)
	assert.Equal(t, int32(0), tk.Carrying)
	assert.Equal(t, int32(1), tk.TripsDone)
}

// Ninguna transición se salta una fase: cada una sólo sale de la suya.
func TestLasTransicionesSoloSalenDeSuFase(t *testing.T) {
	recurso := NewGather(1, bosque)
	trabajando, _ := recurso.StartWork(0, 10_000)
	volviendo, _ := trabajando.FinishWork(10)

	_, err := trabajando.StartWork(0, 10_000)
	assert.True(t, errors.Is(err, ErrBadTransition))
	_, err = volviendo.StartWork(0, 10_000)
	assert.True(t, errors.Is(err, ErrBadTransition))

	_, err = recurso.FinishWork(10)
	assert.True(t, errors.Is(err, ErrBadTransition))
	_, err = volviendo.FinishWork(10)
	assert.True(t, errors.Is(err, ErrBadTransition))

	_, _, err = recurso.Deposit()
	assert.True(t, errors.Is(err, ErrBadTransition))
	_, _, err = trabajando.Deposit()
	assert.True(t, errors.Is(err, ErrBadTransition))
}

// RN-ECON-006 y Q5: una tarea sustituida o cancelada no entrega nada; la carga
// se pierde porque la nueva tarea empieza de cero.
func TestSustituirUnaTareaPierdeLaCarga(t *testing.T) {
	tk, _ := NewGather(1, bosque).StartWork(0, 10_000)
	tk, _ = tk.FinishWork(10)
	require.Equal(t, int32(10), tk.Carrying)
	nueva := NewGather(1, world.Tile{X: 30, Y: 30})
	assert.Equal(t, int32(0), nueva.Carrying)
	assert.Equal(t, int32(0), nueva.TripsDone)
}

func TestValoresInvalidosNoAvanzan(t *testing.T) {
	_, err := NewGather(1, bosque).StartWork(0, 0)
	assert.True(t, errors.Is(err, ErrInvalidTask))
	tk, _ := NewGather(1, bosque).StartWork(0, 10_000)
	_, err = tk.FinishWork(0)
	assert.True(t, errors.Is(err, ErrInvalidTask))
}

// La fila leída de la base: fase cerrada, y vencimiento si y sólo si WORKING
// (unit_tasks_working_has_deadline).
func TestValidateEsElCheckDelEsquema(t *testing.T) {
	assert.NoError(t, Task{Kind: KindGatherWood, Phase: PhaseWorking, PhaseUntilMs: 1}.Validate())
	assert.Error(t, Task{Kind: KindGatherWood, Phase: PhaseWorking}.Validate())
	assert.Error(t, Task{Kind: KindGatherWood, Phase: PhaseToDeposit, PhaseUntilMs: 1}.Validate())
	assert.Error(t, Task{Kind: KindGatherWood, Phase: "RESTING"}.Validate())
	assert.Error(t, Task{Kind: "GATHER_GOLD", Phase: PhaseToResource}.Validate())
	assert.Error(t, Task{Kind: KindGatherWood, Phase: PhaseToDeposit, Carrying: -1}.Validate())
	assert.True(t, PhaseToResource.IsLeg())
	assert.True(t, PhaseToDeposit.IsLeg())
	assert.False(t, PhaseWorking.IsLeg())
}

// RN-ECON-001: un VILLAGER propio, vivo y no guarnecido. Los errores de estado
// son los de unit.move.
func TestCanGatherAplicaRNECON001(t *testing.T) {
	aldeano := func(st unit.Status, hp int32) *unit.Unit {
		return &unit.Unit{ID: 1, PlayerID: dueno, Type: unit.TypeVillager, Status: st, HP: hp, MaxHP: 40}
	}
	assert.NoError(t, CanGather(aldeano(unit.StatusIdle, 40), dueno))
	assert.NoError(t, CanGather(aldeano(unit.StatusMoving, 40), dueno))
	assert.NoError(t, CanGather(aldeano(unit.StatusWorking, 40), dueno), "RN-ECON-006: sustituye la tarea")
	assert.NoError(t, CanGather(aldeano(unit.StatusHidden, 40), dueno), "recolectar revela, como moverse")

	assert.True(t, errors.Is(CanGather(aldeano(unit.StatusIdle, 40), uuid.New()), ErrNotOwned))
	assert.True(t, errors.Is(CanGather(aldeano(unit.StatusDead, 0), dueno), unit.ErrDead))
	assert.True(t, errors.Is(CanGather(aldeano(unit.StatusIdle, 0), dueno), unit.ErrDead))
	assert.True(t, errors.Is(CanGather(aldeano(unit.StatusGarrisoned, 40), dueno), unit.ErrGarrisoned))

	raro := aldeano(unit.StatusIdle, 40)
	raro.Type = "KNIGHT"
	assert.True(t, errors.Is(CanGather(raro, dueno), unit.ErrUnknownType))
}

type rejilla struct{ forest map[world.Tile]bool }

func (r rejilla) InBounds(x, y int32) bool { return x >= 0 && y >= 0 && x < 64 && y < 64 }
func (r rejilla) TerrainAt(x, y int32) world.TerrainType {
	if r.forest[world.Tile{X: x, Y: y}] {
		return world.Forest
	}
	return world.Grassland
}

// RN-ECON-001: el destino es un tile FOREST dentro del mundo.
func TestElDestinoTieneQueSerBosque(t *testing.T) {
	g := rejilla{forest: map[world.Tile]bool{bosque: true}}
	assert.NoError(t, ValidTarget(g, bosque))
	assert.True(t, errors.Is(ValidTarget(g, world.Tile{X: 21, Y: 10}), ErrNotResource))
	assert.True(t, errors.Is(ValidTarget(g, world.Tile{X: -1, Y: 0}), ErrOutOfBounds))
	assert.True(t, errors.Is(ValidTarget(g, world.Tile{X: 64, Y: 0}), ErrOutOfBounds))
}
