package simulation_test

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/movement"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// Tests de simulación de la respuesta automática (SPEC-COMBAT-RESPONSE §9).
//
// El jugador del harness es el incursor: su guerrero 100 ataca al aldeano 200
// del vecino. Los guardias del vecino son los 201 y 202.

const (
	guardiaID  = int64(201)
	guardia2ID = int64(202)
)

// tickHasta avanza tick a tick hasta que se cumple la condición o se agota el
// presupuesto.
func (h *harness) tickHasta(max int, cond func() bool) bool {
	h.t.Helper()
	for i := 0; i < max; i++ {
		if cond() {
			return true
		}
		h.tick()
	}
	return cond()
}

// Golden «escolta» (RN-COMBAT-014, RN-COMBAT-017): el primer golpe al aldeano
// despierta al guardia que está a 2 tiles. El guardia persigue al incursor, lo
// mata y vuelve a su puesto, donde queda IDLE. El incursor, que tenía su
// propio enfrentamiento, no responde. Cada enfrentamiento escribe un
// UnitAttacked, y sólo el del guardia lleva auto.
func TestElGuardiaRespondeMataAlIncursorYVuelveASuPuesto(t *testing.T) {
	h := newHarness(t)
	enemigo, _ := conEnemigo(h)
	ponerUnidad(h, guerreroID, h.playerID, h.cityID, unit.TypeWarrior, world.Tile{X: 19, Y: 20}, 16)
	v := ponerUnidad(h, enemigoID, enemigo, 2, unit.TypeVillager, world.Tile{X: 20, Y: 20}, 40)
	puesto := world.Tile{X: 22, Y: 20}
	g := ponerUnidad(h, guardiaID, enemigo, 2, unit.TypeWarrior, puesto, 60)

	h.atacar(guerreroID, enemigoID)
	h.tick()
	require.Equal(t, int32(32), v.HP, "primer golpe en el contacto")
	objetivo, ok := h.state.Engaged(guardiaID)
	require.True(t, ok, "el golpe despierta al guardia en el mismo tick")
	assert.Equal(t, guerreroID, objetivo)
	desde, auto := h.state.Responding(guardiaID)
	require.True(t, auto)
	assert.Equal(t, puesto, desde)

	muerto := h.tickHasta(200, func() bool { _, vive := h.state.Unit(guerreroID); return !vive })
	require.True(t, muerto, "dos golpes de 8 matan a un incursor de 16")
	assert.Greater(t, v.HP, int32(0), "el aldeano sobrevive")
	_, sigue := h.state.Engaged(guardiaID)
	assert.False(t, sigue)

	h.advance(5 * time.Second)
	assert.Equal(t, puesto, g.Tile(), "vuelve a su puesto")
	assert.Equal(t, unit.StatusIdle, g.Status)
	_, volviendo := h.state.Returning(guardiaID)
	assert.False(t, volviendo, "la vuelta terminó")

	require.Len(t, h.combat.attacks, 2)
	assert.Equal(t, simulation.Attack{AttackerID: guerreroID, AttackerPlayer: h.playerID, TargetID: enemigoID,
		TargetPlayer: enemigo, At: world.Tile{X: 20, Y: 20}, Tick: h.combat.attacks[0].Tick,
		AtMs: h.combat.attacks[0].AtMs}, h.combat.attacks[0])
	assert.Positive(t, h.combat.attacks[0].AtMs, "lleva el instante de la simulación (ADR-028 §3)")
	assert.Equal(t, guardiaID, h.combat.attacks[1].AttackerID)
	assert.Equal(t, guerreroID, h.combat.attacks[1].TargetID)
	assert.True(t, h.combat.attacks[1].Auto)
	require.Len(t, h.combat.deaths, 1)
	assert.Equal(t, guardiaID, h.combat.deaths[0].KillerID)
}

// RN-COMBAT-014: la víctima guerrera se defiende sola, y un enemigo que no
// golpea no despierta a nadie.
func TestElGuerreroGolpeadoSeDefiende(t *testing.T) {
	h := newHarness(t)
	enemigo, _ := conEnemigo(h)
	ponerUnidad(h, guerreroID, h.playerID, h.cityID, unit.TypeWarrior, world.Tile{X: 19, Y: 20}, 60)
	ponerUnidad(h, enemigoID, enemigo, 2, unit.TypeWarrior, world.Tile{X: 20, Y: 20}, 60)

	h.advance(3 * time.Second)
	_, engaged := h.state.Engaged(enemigoID)
	require.False(t, engaged, "tener al enemigo al lado no dispara nada")

	h.atacar(guerreroID, enemigoID)
	h.tick()
	objetivo, ok := h.state.Engaged(enemigoID)
	require.True(t, ok)
	assert.Equal(t, guerreroID, objetivo)
	desde, auto := h.state.Responding(enemigoID)
	require.True(t, auto)
	assert.Equal(t, world.Tile{X: 20, Y: 20}, desde)
}

// RN-COMBAT-014 en el borde del radio: a 4 tiles de la víctima responde; a 5,
// no.
func TestElRadioDeGuardia(t *testing.T) {
	for _, c := range []struct {
		x        int32
		responde bool
	}{{24, true}, {25, false}} {
		h := newHarness(t)
		enemigo, _ := conEnemigo(h)
		ponerUnidad(h, guerreroID, h.playerID, h.cityID, unit.TypeWarrior, world.Tile{X: 19, Y: 20}, 60)
		ponerUnidad(h, enemigoID, enemigo, 2, unit.TypeVillager, world.Tile{X: 20, Y: 20}, 40)
		ponerUnidad(h, guardiaID, enemigo, 2, unit.TypeWarrior, world.Tile{X: c.x, Y: 20}, 60)

		h.atacar(guerreroID, enemigoID)
		h.tick()
		_, ok := h.state.Engaged(guardiaID)
		assert.Equal(t, c.responde, ok, "guardia en x=%d", c.x)
	}
}

// RN-COMBAT-013: sólo responde quien está libre. Un guardia que se mueve por
// orden de su dueño o que ya pelea sigue a lo suyo.
func TestUnGuerreroOcupadoNoResponde(t *testing.T) {
	h := newHarness(t)
	enemigo, _ := conEnemigo(h)
	ponerUnidad(h, guerreroID, h.playerID, h.cityID, unit.TypeWarrior, world.Tile{X: 19, Y: 20}, 60)
	ponerUnidad(h, enemigoID, enemigo, 2, unit.TypeVillager, world.Tile{X: 20, Y: 20}, 40)
	ponerUnidad(h, guardiaID, enemigo, 2, unit.TypeWarrior, world.Tile{X: 22, Y: 20}, 60)
	ponerUnidad(h, guardia2ID, enemigo, 2, unit.TypeWarrior, world.Tile{X: 21, Y: 22}, 60)
	ponerUnidad(h, 300, h.playerID, h.cityID, unit.TypeVillager, world.Tile{X: 21, Y: 23}, 40)

	h.commands <- simulation.MoveUnit{PlayerID: enemigo, RequestID: uuid.NewString(), UnitID: guardiaID,
		Target: world.Tile{X: 22, Y: 40}}
	h.commands <- simulation.AttackUnit{PlayerID: enemigo, RequestID: uuid.NewString(), UnitID: guardia2ID, TargetID: 300}
	h.tick()
	h.atacar(guerreroID, enemigoID)
	h.tick()

	_, ok := h.state.Engaged(guardiaID)
	assert.False(t, ok, "la orden de moverse manda")
	objetivo, _ := h.state.Engaged(guardia2ID)
	assert.Equal(t, int64(300), objetivo, "quien ya pelea no cambia de objetivo")
	_, auto := h.state.Responding(guardia2ID)
	assert.False(t, auto)
}

// RN-COMBAT-015: dos guardias responden al mismo golpe; cada uno abre su
// propia respuesta, y el orden de proceso es el de sus ids.
func TestDosGuardiasRespondenAlMismoGolpe(t *testing.T) {
	h := newHarness(t)
	enemigo, _ := conEnemigo(h)
	ponerUnidad(h, guerreroID, h.playerID, h.cityID, unit.TypeWarrior, world.Tile{X: 19, Y: 20}, 60)
	ponerUnidad(h, enemigoID, enemigo, 2, unit.TypeVillager, world.Tile{X: 20, Y: 20}, 40)
	ponerUnidad(h, guardia2ID, enemigo, 2, unit.TypeWarrior, world.Tile{X: 20, Y: 21}, 60)
	ponerUnidad(h, guardiaID, enemigo, 2, unit.TypeWarrior, world.Tile{X: 20, Y: 19}, 60)

	h.atacar(guerreroID, enemigoID)
	h.tick()
	for _, id := range []int64{guardiaID, guardia2ID} {
		objetivo, ok := h.state.Engaged(id)
		require.True(t, ok, "guardia %d", id)
		assert.Equal(t, guerreroID, objetivo)
	}
	// Ya adyacentes: los dos golpean en el tick siguiente, a la vez.
	g, _ := h.state.Unit(guerreroID)
	h.tick()
	assert.Equal(t, int32(60-16), g.HP)
}

// RN-COMBAT-017 y RN-COMBAT-013: el incursor que huye más allá de la correa se
// salva; el guardia abandona y vuelve. Si por el camino otro golpe lo
// despierta, responde con su puesto original.
func TestLaCorreaYLaVueltaConservanElPuesto(t *testing.T) {
	h := newHarness(t)
	enemigo, _ := conEnemigo(h)
	ponerUnidad(h, guerreroID, h.playerID, h.cityID, unit.TypeWarrior, world.Tile{X: 19, Y: 20}, 60)
	v := ponerUnidad(h, enemigoID, enemigo, 2, unit.TypeVillager, world.Tile{X: 20, Y: 20}, 40)
	puesto := world.Tile{X: 23, Y: 20}
	g := ponerUnidad(h, guardiaID, enemigo, 2, unit.TypeWarrior, puesto, 60)

	h.atacar(guerreroID, enemigoID)
	h.tick()
	require.Equal(t, int32(32), v.HP)
	_, ok := h.state.Engaged(guardiaID)
	require.True(t, ok)

	// El incursor huye hacia el oeste; el guardia, a la misma velocidad, no lo
	// alcanza.
	h.send(simulation.MoveUnit{PlayerID: h.playerID, RequestID: uuid.NewString(), UnitID: guerreroID,
		Target: world.Tile{X: 2, Y: 20}})
	soltado := h.tickHasta(200, func() bool { _, e := h.state.Engaged(guardiaID); return !e })
	require.True(t, soltado, "la correa corta la persecución")
	inc, _ := h.state.Unit(guerreroID)
	assert.Equal(t, int32(60), inc.HP, "no llegó a golpearlo")
	vuelta, volviendo := h.state.Returning(guardiaID)
	require.True(t, volviendo)
	assert.Equal(t, puesto, vuelta)

	// Un segundo incursor golpea al aldeano mientras el guardia vuelve.
	ponerUnidad(h, 101, h.playerID, h.cityID, unit.TypeWarrior, world.Tile{X: 20, Y: 21}, 60)
	h.atacar(101, enemigoID)
	h.tick()
	objetivo, ok := h.state.Engaged(guardiaID)
	require.True(t, ok, "volviendo, está disponible")
	assert.Equal(t, int64(101), objetivo)
	desde, _ := h.state.Responding(guardiaID)
	assert.Equal(t, puesto, desde, "conserva el puesto original")
	assert.NotEqual(t, puesto, g.Tile())
}

// Una orden del jugador sustituye la respuesta y descarta el puesto: al
// terminar, el guardia no vuelve.
func TestUnaOrdenDelJugadorSustituyeLaRespuesta(t *testing.T) {
	h := newHarness(t)
	enemigo, _ := conEnemigo(h)
	ponerUnidad(h, guerreroID, h.playerID, h.cityID, unit.TypeWarrior, world.Tile{X: 19, Y: 20}, 60)
	ponerUnidad(h, enemigoID, enemigo, 2, unit.TypeVillager, world.Tile{X: 20, Y: 20}, 40)
	g := ponerUnidad(h, guardiaID, enemigo, 2, unit.TypeWarrior, world.Tile{X: 22, Y: 20}, 60)

	h.atacar(guerreroID, enemigoID)
	h.tick()
	_, ok := h.state.Responding(guardiaID)
	require.True(t, ok)

	destino := world.Tile{X: 22, Y: 26}
	h.commands <- simulation.MoveUnit{PlayerID: enemigo, RequestID: uuid.NewString(), UnitID: guardiaID, Target: destino}
	h.tick()
	_, ok = h.state.Engaged(guardiaID)
	assert.False(t, ok)
	h.advance(10 * time.Second)
	assert.Equal(t, destino, g.Tile(), "llega a donde le mandaron y se queda")
	_, volviendo := h.state.Returning(guardiaID)
	assert.False(t, volviendo)
}

// RN-COMBAT-018 e INV-COMBAT-002: cuando el incursor pasa a protegido, su
// enfrentamiento termina y no vuelve a golpear; tampoco se le responde.
func TestUnIncursorProtegidoDejaDeGolpear(t *testing.T) {
	h := newHarness(t)
	enemigo, _ := conEnemigo(h)
	ponerUnidad(h, guerreroID, h.playerID, h.cityID, unit.TypeWarrior, world.Tile{X: 19, Y: 20}, 60)
	v := ponerUnidad(h, enemigoID, enemigo, 2, unit.TypeVillager, world.Tile{X: 20, Y: 20}, 40)

	h.atacar(guerreroID, enemigoID)
	h.tick()
	require.Equal(t, int32(32), v.HP)

	mia, ok := h.state.City(h.cityID)
	require.True(t, ok)
	mia.PresenceState = city.PresenceProtected
	g := ponerUnidad(h, guardiaID, enemigo, 2, unit.TypeWarrior, world.Tile{X: 21, Y: 20}, 60)
	h.advance(5 * time.Second)
	assert.Equal(t, int32(32), v.HP, "ningún golpe más")
	_, engaged := h.state.Engaged(guerreroID)
	assert.False(t, engaged)
	assert.Equal(t, unit.StatusIdle, g.Status)
}

// Un jugador protegido tampoco puede ordenar un ataque (RN-COMBAT-018).
func TestUnJugadorProtegidoNoPuedeAtacar(t *testing.T) {
	h := newHarness(t)
	enemigo, _ := conEnemigo(h)
	ponerUnidad(h, guerreroID, h.playerID, h.cityID, unit.TypeWarrior, world.Tile{X: 19, Y: 20}, 60)
	ponerUnidad(h, enemigoID, enemigo, 2, unit.TypeVillager, world.Tile{X: 20, Y: 20}, 40)
	mia, _ := h.state.City(h.cityID)
	mia.PresenceState = city.PresenceProtected

	h.atacar(guerreroID, enemigoID)
	assert.Equal(t, []string{"CITY_PROTECTED"}, h.codigos())
}

// §7, recuperación: la respuesta y el puesto viven sólo en la RAM. Un guerrero
// que volvía a su puesto cuando cayó el servidor reaparece con ese movimiento
// como uno normal: sin enfrentamiento ni vuelta anotada, y llega a su destino.
func TestTrasUnReinicioLaVueltaAlPuestoEsUnMovimientoNormal(t *testing.T) {
	terrain := make([]byte, 64*64)
	for i := range terrain {
		terrain[i] = byte(world.Grassland)
	}
	w, err := world.New(64, 64, 32, 1, terrain)
	require.NoError(t, err)
	g := &unit.Unit{ID: guardiaID, PlayerID: uuid.New(), Type: unit.TypeWarrior,
		X: 20, Y: 20, HP: 44, MaxHP: 60, Status: unit.StatusMoving}
	path, err := movement.BuildTimedPath([]world.Tile{{X: 20, Y: 20}, {X: 21, Y: 20}, {X: 22, Y: 20}}, w, 600)
	require.NoError(t, err)
	m := movement.New(g.ID, path, world.Tile{X: 22, Y: 20}, epoch)
	m.ID = 5

	state := simulation.NewState(w)
	res := simulation.Hydrate(state, []*unit.Unit{g}, nil, []*movement.Movement{m},
		epoch+100, slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.Equal(t, 1, res.Resumed)
	_, engaged := state.Engaged(guardiaID)
	assert.False(t, engaged)
	_, auto := state.Responding(guardiaID)
	assert.False(t, auto)
	_, volviendo := state.Returning(guardiaID)
	assert.False(t, volviendo)

	sim := simulation.New(state, simulation.Deps{})
	sim.AdvanceMovements(epoch + 1_200)
	assert.Equal(t, world.Tile{X: 22, Y: 20}, g.Tile())
	assert.Equal(t, unit.StatusIdle, g.Status)
}

// unit.cancel es una orden del jugador: detiene la respuesta y el guardia se
// queda donde está, sin volver a su puesto.
func TestCancelarLaRespuestaNoVuelveAlPuesto(t *testing.T) {
	h := newHarness(t)
	enemigo, _ := conEnemigo(h)
	ponerUnidad(h, guerreroID, h.playerID, h.cityID, unit.TypeWarrior, world.Tile{X: 19, Y: 20}, 60)
	ponerUnidad(h, enemigoID, enemigo, 2, unit.TypeVillager, world.Tile{X: 20, Y: 20}, 40)
	puesto := world.Tile{X: 23, Y: 20}
	g := ponerUnidad(h, guardiaID, enemigo, 2, unit.TypeWarrior, puesto, 60)

	h.atacar(guerreroID, enemigoID)
	h.advance(700 * time.Millisecond)
	_, ok := h.state.Responding(guardiaID)
	require.True(t, ok)
	require.NotEqual(t, puesto, g.Tile(), "ya ha salido")

	// El incursor también se para: si siguiera golpeando, su próximo golpe
	// volvería a despertar al guardia, que ya está libre.
	h.commands <- simulation.CancelMovement{PlayerID: h.playerID, RequestID: uuid.NewString(), UnitID: guerreroID}
	h.commands <- simulation.CancelMovement{PlayerID: enemigo, RequestID: uuid.NewString(), UnitID: guardiaID}
	arranques := func() int {
		n := 0
		for _, m := range h.rec.byType(protocol.TypeUnitMovementStarted) {
			if m.Payload.(protocol.UnitMovementStartedPayload).UnitID == guardiaID {
				n++
			}
		}
		return n
	}
	antes := arranques()
	h.tick()
	assert.Equal(t, antes, arranques(), "ni siquiera un tramo de vuelta que se cancele al instante")
	donde := g.Tile()
	_, engaged := h.state.Engaged(guardiaID)
	assert.False(t, engaged)
	_, volviendo := h.state.Returning(guardiaID)
	assert.False(t, volviendo)
	h.advance(3 * time.Second)
	assert.Equal(t, donde, g.Tile(), "se queda donde le pararon")
	assert.Equal(t, unit.StatusIdle, g.Status)
}
