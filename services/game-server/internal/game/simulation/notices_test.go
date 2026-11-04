package simulation_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/eventlog"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// Tests de los avisos en vivo (SPEC-LIVE-NOTICES §9 «simulation», ADR-033). El
// aviso sale por SendToPlayer («player» en el grabador): llega al jugador esté
// mirando donde esté, sin depender de los chunks a los que está suscrito.

// aviso es un event_log.notice grabado, con su destinatario.
type aviso struct {
	to uuid.UUID
	p  protocol.EventLogNoticePayload
}

func avisosDe(h *harness) []aviso {
	h.t.Helper()
	var out []aviso
	for _, m := range h.rec.byType(protocol.TypeEventLogNotice) {
		require.Equal(h.t, "player", m.Kind, "el aviso va al jugador, no al chunk")
		assert.Empty(h.t, m.RequestID, "el aviso no responde a ningún comando")
		out = append(out, aviso{to: m.PlayerID, p: m.Payload.(protocol.EventLogNoticePayload)})
	}
	return out
}

func despawnsMuertos(h *harness) int {
	n := 0
	for _, m := range h.rec.byType(protocol.TypeEntityDespawn) {
		if m.Payload.(protocol.EntityDespawnPayload).Reason == protocol.DespawnDead {
			n++
		}
	}
	return n
}

// RN-NOTICE-001 y RN-NOTICE-002: una unidad muere lejos de su dueño. En el mismo
// tick que su entity.despawn, el dueño recibe UNIT_LOST con la unidad, el tile y
// la ciudad del asesino, y el asesino ENEMY_KILLED; nadie más recibe nada.
func TestUnaMuerteAvisaALosDosLadosEnElTickDelDespawn(t *testing.T) {
	h := newHarness(t)
	enemigo, ciudad := conEnemigo(h)
	ponerUnidad(h, guerreroID, h.playerID, h.cityID, unit.TypeWarrior, world.Tile{X: 15, Y: 20}, 60)
	ponerUnidad(h, enemigoID, enemigo, ciudad.ID, unit.TypeVillager, world.Tile{X: 20, Y: 20}, 40)

	h.atacar(guerreroID, enemigoID)
	require.True(t, h.tickHasta(300, func() bool {
		if despawnsMuertos(h) > 0 {
			return true
		}
		require.Empty(t, avisosDe(h), "ningún aviso antes de la muerte")
		return false
	}))
	muerte := h.clk.NowMs()
	avisos := avisosDe(h)
	require.Len(t, avisos, 2, "en el mismo tick que el despawn")

	porDestino := map[uuid.UUID]protocol.EventLogNoticePayload{}
	for _, a := range avisos {
		porDestino[a.to] = a.p
	}
	require.Len(t, porDestino, 2, "uno para cada lado, y para nadie más")

	perdida := porDestino[enemigo]
	assert.Equal(t, string(eventlog.KindUnitLost), perdida.Kind)
	assert.Equal(t, h.playerID.String(), perdida.OtherPlayerID)
	require.NotNil(t, perdida.OtherCityID)
	require.NotNil(t, perdida.OtherCityName)
	assert.Equal(t, h.cityID, *perdida.OtherCityID)
	assert.Equal(t, "Testópolis", *perdida.OtherCityName)
	assert.Equal(t, string(unit.TypeVillager), perdida.UnitType)
	assert.Equal(t, muerte, perdida.AtMs)
	assert.Nil(t, perdida.Amount)
	require.NotNil(t, perdida.X)
	require.NotNil(t, perdida.Y)
	assert.Equal(t, h.combat.deaths[0].At, world.Tile{X: *perdida.X, Y: *perdida.Y}, "el tile de la muerte")

	baja := porDestino[h.playerID]
	assert.Equal(t, string(eventlog.KindEnemyKilled), baja.Kind)
	assert.Equal(t, enemigo.String(), baja.OtherPlayerID)
	require.NotNil(t, baja.OtherCityID)
	assert.Equal(t, ciudad.ID, *baja.OtherCityID)
	assert.Equal(t, string(unit.TypeVillager), baja.UnitType, "la unidad que murió")
	assert.Equal(t, muerte, baja.AtMs)

	h.advance(10 * time.Second)
	assert.Len(t, avisosDe(h), 2, "la muerte no vuelve a avisar")
}

// SPEC-LIVE-NOTICES §9: un guerrero que saquea da un aviso por golpe a su dueño,
// con la madera de ese golpe, y ninguno al saqueado, que ya tiene pillagedAtMs
// (Q1). El golden de cuatro golpes de 5.
func TestUnSaqueoAvisaAlSaqueadorPorGolpeYNadaAlSaqueado(t *testing.T) {
	h := newHarness(t)
	enemigo, ciudad, _ := conSaqueo(h, 200, world.Tile{X: 20, Y: 30})

	h.saquear(guerreroID, ciudad.ID)
	golpes := 0
	require.True(t, h.tickHasta(300, func() bool {
		if n := len(h.combat.pillages); n != golpes {
			golpes = n
			require.Len(t, avisosDe(h), n, "un aviso por golpe, en su tick")
		}
		_, sigue := h.state.Pillaging(guerreroID)
		return !sigue
	}))
	h.tick()

	avisos := avisosDe(h)
	require.Len(t, avisos, 4)
	for i, a := range avisos {
		assert.Equal(t, h.playerID, a.to, "golpe %d: sólo al saqueador", i)
		assert.Equal(t, string(eventlog.KindPillageDone), a.p.Kind)
		assert.Equal(t, enemigo.String(), a.p.OtherPlayerID)
		require.NotNil(t, a.p.OtherCityID)
		assert.Equal(t, ciudad.ID, *a.p.OtherCityID)
		assert.Equal(t, "Vecina", *a.p.OtherCityName)
		require.NotNil(t, a.p.Amount)
		assert.Equal(t, int64(5), *a.p.Amount, "la madera de ese golpe")
		assert.Empty(t, a.p.UnitType)
		require.NotNil(t, a.p.X)
		assert.Equal(t, h.combat.pillages[i].At, world.Tile{X: *a.p.X, Y: *a.p.Y}, "el tile del guerrero")
		assert.Equal(t, h.combat.pillages[i].AtMs, a.p.AtMs)
	}
}

// SPEC-LIVE-NOTICES §9: una casa derribada da HOUSE_DESTROYED al dueño del
// atacante, con el tile de la casa, en el tick de building.destroyed; el dueño
// de la casa no recibe aviso, porque ya tiene besiegedAtMs (Q1).
func TestUnaCasaDerribadaAvisaAlAtacante(t *testing.T) {
	h := newHarness(t)
	enemigo, ciudad, _ := conAsedio(h, world.Tile{X: 20, Y: 38})

	h.derribar(guerreroID, casaID)
	require.True(t, h.tickHasta(400, func() bool {
		if len(h.rec.byType(protocol.TypeBuildingDestroyed)) > 0 {
			return true
		}
		require.Empty(t, avisosDe(h), "ningún aviso mientras la casa sigue en pie")
		return false
	}))

	avisos := avisosDe(h)
	require.Len(t, avisos, 1, "en el tick en que cae")
	a := avisos[0]
	assert.Equal(t, h.playerID, a.to)
	assert.Equal(t, string(eventlog.KindHouseDestroyed), a.p.Kind)
	assert.Equal(t, enemigo.String(), a.p.OtherPlayerID)
	require.NotNil(t, a.p.OtherCityID)
	assert.Equal(t, ciudad.ID, *a.p.OtherCityID)
	require.NotNil(t, a.p.X)
	assert.Equal(t, tileCasa, world.Tile{X: *a.p.X, Y: *a.p.Y}, "el tile de la casa: «Ir» funciona siempre")
	assert.Equal(t, h.clk.NowMs(), a.p.AtMs)
	assert.Nil(t, a.p.Amount)
	assert.Empty(t, a.p.UnitType)
}

// ADR-033 §2: el aviso sale al decidir el suceso, no al escribirlo. Una muerte y
// una casa cuya escritura falla se reencolan en el volcado y se escriben después,
// pero no vuelven a avisar.
func TestUnaEscrituraReencoladaNoVuelveAAvisar(t *testing.T) {
	t.Run("una muerte", func(t *testing.T) {
		h := newHarness(t)
		enemigo, ciudad := conEnemigo(h)
		ponerUnidad(h, guerreroID, h.playerID, h.cityID, unit.TypeWarrior, world.Tile{X: 15, Y: 20}, 60)
		ponerUnidad(h, enemigoID, enemigo, ciudad.ID, unit.TypeVillager, world.Tile{X: 20, Y: 20}, 40)
		h.combat.deathErr = errors.New("la base no responde")
		h.persister.permanent = true

		h.atacar(guerreroID, enemigoID)
		require.True(t, h.tickHasta(300, func() bool { return despawnsMuertos(h) > 0 }))
		require.Len(t, avisosDe(h), 2)
		h.advance(6 * time.Second) // un volcado reencola la muerte, que vuelve a fallar
		assert.Empty(t, h.combat.deaths)

		h.combat.deathErr = nil
		h.advance(6 * time.Second) // y el siguiente la escribe
		require.Len(t, h.combat.deaths, 1)
		assert.Len(t, avisosDe(h), 2, "la muerte se escribió tarde, pero se avisó una vez")
	})
	t.Run("una casa", func(t *testing.T) {
		h := newHarness(t)
		_, ciudad, _ := conAsedio(h, world.Tile{X: 20, Y: 38})
		h.maderaDe(ciudad.ID, 500)
		h.economy.destroyErr = errors.New("la base no responde")
		h.persister.permanent = true

		h.derribar(guerreroID, casaID)
		require.True(t, h.tickHasta(400, func() bool { return len(h.rec.byType(protocol.TypeBuildingDestroyed)) > 0 }))
		require.Len(t, avisosDe(h), 1)
		h.advance(6 * time.Second)
		assert.Empty(t, h.economy.destroys)

		h.economy.destroyErr = nil
		h.advance(6 * time.Second)
		require.Len(t, h.economy.destroys, 1)
		assert.Len(t, avisosDe(h), 1, "la casa se escribió tarde, pero se avisó una vez")
	})
}

// RN-NOTICE-001: un golpe de saqueo cuya escritura se rechaza devuelve la madera,
// pero su aviso ya salió y no se desavisa (ADR-033 §6).
func TestUnGolpeQueNoSeEscribeYaSeAviso(t *testing.T) {
	h := newHarness(t)
	_, ciudad, _ := conSaqueo(h, 200, world.Tile{X: 20, Y: 38})
	h.combat.pillageErr = errors.New("la base no responde")
	h.persister.permanent = true

	h.saquear(guerreroID, ciudad.ID)
	h.tick()
	h.tick() // el fallo vuelve por el buzón
	assert.Equal(t, int64(200), h.state.Stock(ciudad.ID).Amount, "la madera vuelve")
	require.Len(t, avisosDe(h), 1, "el aviso ya salió")
}
