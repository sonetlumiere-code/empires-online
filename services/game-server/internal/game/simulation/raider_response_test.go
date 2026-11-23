package simulation_test

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
)

// Tests del saqueador y el asediador que responden (SPEC-BOTS-TRADE §4.6,
// RN-COMBAT-039 y RN-COMBAT-040). Mundo de newHarness: la ciudad enemiga de
// conEnemigo en (20,40), con su muralla de (19,39) a (21,41). Los saqueadores
// del jugador del harness (100, 101, 102) están ya en el anillo norte, en
// (19,38), (20,38) y (21,38); los guardias del vecino (201, 202), parados justo
// detrás de ellos, en (19,37) y (20,37), a 3 del centro de su ciudad.

// muerteEn es una muerte de un encuentro: quién, a cuántos ms de la orden y
// quién la causó.
type muerteEn struct {
	victima, asesino int64
	ms               int64
}

// encuentroDeSaqueo monta `saqueadores` contra `guardias`, ordena el saqueo a
// todos en el mismo tick y devuelve el harness y el instante de la orden.
func encuentroDeSaqueo(t *testing.T, saqueadores, guardias int) (*harness, int64) {
	t.Helper()
	h := newHarness(t)
	h.conectar()
	enemigo, ciudad := conEnemigo(h)
	h.maderaDe(ciudad.ID, 5_000)
	for i := 0; i < saqueadores; i++ {
		ponerUnidad(h, guerreroID+int64(i), h.playerID, h.cityID, unit.TypeWarrior,
			world.Tile{X: 19 + int32(i), Y: 38}, 60)
	}
	for i := 0; i < guardias; i++ {
		ponerUnidad(h, guardiaID+int64(i), enemigo, ciudad.ID, unit.TypeWarrior,
			world.Tile{X: 19 + int32(i), Y: 37}, 60)
	}
	for i := 0; i < saqueadores; i++ {
		h.commands <- simulation.PillageCity{PlayerID: h.playerID, RequestID: uuid.NewString(),
			UnitID: guerreroID + int64(i), CityID: ciudad.ID}
	}
	t0 := h.clk.NowMs()
	h.loop.Step(t0)
	return h, t0
}

func muertesDesde(h *harness, t0 int64) []muerteEn {
	var out []muerteEn
	for _, d := range h.combat.deaths {
		out = append(out, muerteEn{victima: d.VictimID, asesino: d.KillerID, ms: d.AtMs - t0})
	}
	return out
}

// Golden «3 contra 2» (spec §4.6): el primer golpe de saqueo, el de 100,
// despierta a los dos guardias contra 100. Su primer golpe despierta a 100 y,
// por el radio de guardia, a 101 y 102, que dejan de saquear y responden contra
// el guardia que golpeó primero (201), con su puesto en el tile en que
// saqueaban. Tres contra uno, 201 cae a los 3 200 ms; 202 remata a 100 a los
// 6 100 ms y cae a los 9 200 ms. Los dos supervivientes vuelven a su puesto
// IDLE y no reanudan el saqueo (RN-COMBAT-040).
func TestGoldenTresSaqueadoresGananADosGuardias(t *testing.T) {
	h, t0 := encuentroDeSaqueo(t, 3, 2)

	h.tick() // el primer golpe de saqueo, en el contacto, despierta a los guardias
	objetivo, ok := h.state.Engaged(guardiaID)
	require.True(t, ok)
	assert.Equal(t, guerreroID, objetivo)

	h.tick() // los guardias golpean a 100; responden 100 y sus compañeros
	for i, puesto := range []world.Tile{{X: 19, Y: 38}, {X: 20, Y: 38}, {X: 21, Y: 38}} {
		id := guerreroID + int64(i)
		_, saquea := h.state.Pillaging(id)
		assert.False(t, saquea, "%d deja de saquear al responder", id)
		desde, auto := h.state.Responding(id)
		require.True(t, auto, "%d responde (RN-COMBAT-039)", id)
		assert.Equal(t, puesto, desde, "el puesto de %d es el tile en que saqueaba", id)
		objetivo, _ := h.state.Engaged(id)
		assert.Equal(t, guardiaID, objetivo, "%d responde al guardia que golpeó primero", id)
	}

	h.advance(20 * time.Second)
	assert.Equal(t, []muerteEn{
		{victima: guardiaID, asesino: guerreroID + 1, ms: 3_200},
		{victima: guerreroID, asesino: guardia2ID, ms: 6_100},
		{victima: guardia2ID, asesino: guerreroID + 1, ms: 9_200},
	}, muertesDesde(h, t0))
	assert.Len(t, h.combat.pillages, 3, "un golpe de saqueo de cada uno, el del contacto")

	for _, c := range []struct {
		id   int64
		hp   int32
		tile world.Tile
	}{{guerreroID + 1, 44, world.Tile{X: 20, Y: 38}}, {guerreroID + 2, 60, world.Tile{X: 21, Y: 38}}} {
		u, vive := h.state.Unit(c.id)
		require.True(t, vive)
		assert.Equal(t, c.hp, u.HP)
		assert.Equal(t, c.tile, u.Tile(), "vuelve a su puesto")
		assert.Equal(t, unit.StatusIdle, u.Status)
		_, saquea := h.state.Pillaging(c.id)
		assert.False(t, saquea, "no reanuda el saqueo solo")
	}
}

// Golden «2 contra 2»: los guardias golpean primero y ganan, con una baja; el
// que queda, a 20 de vida.
func TestGoldenDosSaqueadoresPierdenContraDosGuardias(t *testing.T) {
	h, t0 := encuentroDeSaqueo(t, 2, 2)
	h.advance(20 * time.Second)
	assert.Equal(t, []muerteEn{
		{victima: guerreroID, asesino: guardia2ID, ms: 4_600},
		{victima: guardiaID, asesino: guerreroID + 1, ms: 6_200},
		{victima: guerreroID + 1, asesino: guardia2ID, ms: 13_800},
	}, muertesDesde(h, t0))
	g, vive := h.state.Unit(guardia2ID)
	require.True(t, vive)
	assert.Equal(t, int32(20), g.HP)
}

// Golden «1 contra 1»: el guardia, que golpea primero, gana por un golpe.
func TestGoldenUnSaqueadorPierdeContraUnGuardiaPorUnGolpe(t *testing.T) {
	h, t0 := encuentroDeSaqueo(t, 1, 1)
	h.advance(20 * time.Second)
	assert.Equal(t, []muerteEn{{victima: guerreroID, asesino: guardiaID, ms: 10_600}}, muertesDesde(h, t0))
	g, vive := h.state.Unit(guardiaID)
	require.True(t, vive)
	assert.Equal(t, int32(4), g.HP, "le quedaba un golpe")
}

// RN-COMBAT-039 «desde la orden»: un saqueador de camino a la muralla también
// responde. Su puesto es el tile en que estaba; al matar a quien lo atacó
// vuelve a él y queda IDLE, sin retomar el camino del saqueo (RN-COMBAT-040).
func TestElSaqueadorDeCaminoRespondeYNoRetomaElSaqueo(t *testing.T) {
	h := newHarness(t)
	enemigo, ciudad, g := conSaqueo(h, 500, world.Tile{X: 20, Y: 30})
	h.saquear(guerreroID, ciudad.ID)
	_, saquea := h.state.Pillaging(guerreroID)
	require.True(t, saquea)
	h.advance(1200 * time.Millisecond) // dos tiles de camino
	donde := g.Tile()
	require.NotEqual(t, world.Tile{X: 20, Y: 30}, donde)

	ponerUnidad(h, enemigoID, enemigo, ciudad.ID, unit.TypeWarrior, world.Tile{X: donde.X + 1, Y: donde.Y}, 8)
	h.commands <- simulation.AttackUnit{PlayerID: enemigo, RequestID: uuid.NewString(), UnitID: enemigoID, TargetID: guerreroID}
	h.tick() // el contacto, el primer golpe y la respuesta, al final de la fase 4
	_, saquea = h.state.Pillaging(guerreroID)
	assert.False(t, saquea)
	desde, auto := h.state.Responding(guerreroID)
	require.True(t, auto)

	require.True(t, h.tickHasta(100, func() bool { _, vive := h.state.Unit(enemigoID); return !vive }))
	h.advance(5 * time.Second)
	assert.Equal(t, desde, g.Tile(), "vuelve al tile en que respondió")
	assert.Equal(t, unit.StatusIdle, g.Status)
	_, saquea = h.state.Pillaging(guerreroID)
	assert.False(t, saquea, "no retoma el saqueo")
}

// RN-COMBAT-039 con el asedio: el asediador responde al guardia, deja la casa
// y, al ganar, vuelve a su puesto IDLE sin seguir derribando.
func TestElAsediadorRespondeYNoReanudaElAsedio(t *testing.T) {
	h := newHarness(t)
	enemigo, ciudad, g := conAsedio(h, world.Tile{X: 20, Y: 38})
	ponerUnidad(h, enemigoID, enemigo, ciudad.ID, unit.TypeWarrior, world.Tile{X: 20, Y: 37}, 16)

	h.derribar(guerreroID, casaID)
	h.tick() // el primer golpe de asedio despierta al guardia
	objetivo, ok := h.state.Engaged(enemigoID)
	require.True(t, ok)
	assert.Equal(t, guerreroID, objetivo)
	h.tick() // el guardia golpea; el asediador responde
	_, derriba := h.state.Demolishing(guerreroID)
	assert.False(t, derriba)
	desde, auto := h.state.Responding(guerreroID)
	require.True(t, auto)
	assert.Equal(t, world.Tile{X: 20, Y: 38}, desde)

	require.True(t, h.tickHasta(100, func() bool { _, vive := h.state.Unit(enemigoID); return !vive }))
	vida, _ := vidaDe(h, casaID)
	h.advance(5 * time.Second)
	despues, _ := vidaDe(h, casaID)
	assert.Equal(t, vida, despues, "la casa no recibe más golpes")
	assert.Equal(t, unit.StatusIdle, g.Status)
	assert.Equal(t, world.Tile{X: 20, Y: 38}, g.Tile())
}

// RN-COMBAT-039 y RN-COMBAT-012: el ariete asedia pero no ataca, así que no
// responde y sigue derribando.
func TestElArieteNoResponde(t *testing.T) {
	h := newHarness(t)
	enemigo, ciudad, _ := conAsedio(h, world.Tile{X: 5, Y: 5})
	conMuralla(h, ciudad, true, 0)
	ponerUnidad(h, enemigoID, enemigo, ciudad.ID, unit.TypeWarrior, world.Tile{X: 20, Y: 37}, 60)
	ponerAriete(h, arieteID, world.Tile{X: 20, Y: 38})

	h.send(h.derribarCon(arieteID, murallaID))
	require.Empty(t, h.codigos())
	h.advance(3 * time.Second)
	_, auto := h.state.Responding(arieteID)
	assert.False(t, auto)
	objetivo, derriba := h.state.Demolishing(arieteID)
	require.True(t, derriba, "sigue derribando")
	assert.Equal(t, murallaID, objetivo)
}

// RN-COMBAT-039: una orden de movimiento sigue sin sustituirse. El saqueador
// que vuelve a casa cargado camina con una orden del jugador y no responde.
func TestElSaqueadorQueVuelveCargadoNoResponde(t *testing.T) {
	h := newHarness(t)
	enemigo, ciudad, _ := conSaqueo(h, 500, world.Tile{X: 20, Y: 38})
	h.saquear(guerreroID, ciudad.ID)
	h.advance(2 * time.Second)
	h.send(simulation.MoveUnit{PlayerID: h.playerID, RequestID: uuid.NewString(), UnitID: guerreroID,
		Target: world.Tile{X: 20, Y: 20}})
	h.tick()
	g, _ := h.state.Unit(guerreroID)
	ponerUnidad(h, enemigoID, enemigo, ciudad.ID, unit.TypeWarrior, g.Tile(), 60)
	h.commands <- simulation.AttackUnit{PlayerID: enemigo, RequestID: uuid.NewString(), UnitID: enemigoID, TargetID: guerreroID}
	h.tick()
	h.tick()
	_, auto := h.state.Responding(guerreroID)
	assert.False(t, auto, "la orden de moverse manda")
	_, ataca := h.state.Engaged(guerreroID)
	assert.False(t, ataca)
}

// §7, nivel «recovery»: la respuesta de un saqueador vive sólo en la RAM. El
// volcado lo escribe IDLE, y al cargarlo de nuevo no saquea ni responde.
func TestElVolcadoDeUnSaqueadorQueRespondeLoDejaIdleSinSaqueo(t *testing.T) {
	h, _ := encuentroDeSaqueo(t, 1, 1)
	h.tick()
	h.tick()
	_, auto := h.state.Responding(guerreroID)
	require.True(t, auto)
	g, _ := h.state.Unit(guerreroID)
	require.Equal(t, unit.StatusAttacking, g.Status)

	h.sim.FlushDirty()
	volcada, ok := h.repos.flushed[guerreroID]
	require.True(t, ok)
	assert.Equal(t, unit.StatusIdle, volcada.Status)

	state := simulation.NewState(h.state.World())
	copia := volcada
	simulation.Hydrate(state, []*unit.Unit{&copia}, nil, nil, h.clk.NowMs(),
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	_, saquea := state.Pillaging(guerreroID)
	assert.False(t, saquea)
	_, auto = state.Responding(guerreroID)
	assert.False(t, auto)
}
