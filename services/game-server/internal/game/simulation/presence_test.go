package simulation_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// conVecinos añade n jugadores más, cada uno con su ciudad, y devuelve sus ids
// en el orden de sus ciudades (2, 3, …).
func conVecinos(h *harness, n int) []uuid.UUID {
	var jugadores []uuid.UUID
	for i := 0; i < n; i++ {
		p := uuid.New()
		h.state.AddCity(&city.City{
			ID: int64(i + 2), OwnerPlayerID: p, Name: "Vecina",
			CenterX: int32(20 + 8*i), CenterY: 40, Era: city.EraStone,
			PopulationLimit: 20, PresenceState: city.PresenceOnline,
		})
		jugadores = append(jugadores, p)
	}
	return jugadores
}

// Canon §8: si varios jugadores agotan el margen de reconexión en el mismo
// tick, sus city.update salen en orden ascendente de ciudad, siempre. El
// estado de las sesiones vive en un map, cuyo orden de iteración cambia entre
// ejecuciones: el test repite el escenario para que un recorrido sin ordenar
// no pueda pasar por suerte.
func TestLasCiudadesQueDegradanEnElMismoTickSalenEnOrden(t *testing.T) {
	for ronda := 0; ronda < 25; ronda++ {
		h := newHarness(t)
		jugadores := append([]uuid.UUID{h.playerID}, conVecinos(h, 5)...)
		for _, p := range jugadores {
			h.send(simulation.PlayerConnected{PlayerID: p, SessionID: uuid.New()})
		}
		for _, p := range jugadores {
			h.commands <- simulation.PlayerDisconnected{PlayerID: p, SessionID: uuid.New()}
		}
		h.loop.Step(h.clk.NowMs()) // todas las desconexiones en el mismo tick
		h.rec.reset()

		h.advance(31 * time.Second)

		var orden []int64
		for _, m := range h.rec.byType(protocol.TypeCityUpdate) {
			orden = append(orden, m.Payload.(protocol.CityUpdatePayload).ID)
		}
		require.Equal(t, []int64{1, 2, 3, 4, 5, 6}, orden, "ronda %d", ronda)
	}
}

// La degradación sella last_offline_at con el instante del tick que la
// decide, el mismo con el que el paso siguiente evalúa el cooldown, y no con
// una segunda lectura del reloj.
func TestLaDegradacionSellaElInstanteDelTick(t *testing.T) {
	h := newHarness(t)
	c, _ := h.state.City(h.cityID)
	h.send(simulation.PlayerConnected{PlayerID: h.playerID, SessionID: uuid.New()})
	h.send(simulation.PlayerDisconnected{PlayerID: h.playerID, SessionID: uuid.New()})

	tick := h.clk.Now().Add(45 * time.Second) // el reloj no se mueve: sólo el tick
	h.sim.ProcessTimers(tick)

	require.Equal(t, city.PresenceOfflinePending, c.PresenceState)
	require.NotNil(t, c.LastOfflineAt)
	assert.True(t, c.LastOfflineAt.Equal(tick), "se esperaba %s, llegó %s", tick, c.LastOfflineAt)
}

// INV-CITY-011, INV-CITY-012 e INV-CITY-015 comprobados tras CADA tick de un
// ciclo largo de conexiones y desconexiones, con cortes dentro y fuera del
// margen y protecciones concedidas y levantadas.
func TestLasMarcasDePresenciaAcompañanAlEstadoYNoRetroceden(t *testing.T) {
	h := newHarness(t)
	c, _ := h.state.City(h.cityID)

	var online, offline time.Time
	verificar := func() {
		t.Helper()
		if c.PresenceState != city.PresenceOnline {
			require.NotNil(t, c.LastOfflineAt, "INV-CITY-011: %s sin marca de desconexión", c.PresenceState)
		} else {
			require.Nil(t, c.ProtectionUntil, "INV-CITY-012: ONLINE con protection_until")
		}
		if c.LastOnlineAt != nil {
			require.False(t, c.LastOnlineAt.Before(online), "INV-CITY-015: last_online_at retrocedió")
			online = *c.LastOnlineAt
		}
		if c.LastOfflineAt != nil {
			require.False(t, c.LastOfflineAt.Before(offline), "INV-CITY-015: last_offline_at retrocedió")
			offline = *c.LastOfflineAt
		}
	}
	avanzar := func(d time.Duration) {
		for i := int64(0); i < d.Milliseconds()/100; i++ {
			h.advance(100 * time.Millisecond)
			verificar()
		}
	}

	// Pausas: dentro del margen, hasta OFFLINE_PENDING, hasta PROTECTED.
	for _, pausa := range []time.Duration{10 * time.Second, 40 * time.Second, 340 * time.Second, 5 * time.Second, 400 * time.Second} {
		h.send(simulation.PlayerConnected{PlayerID: h.playerID, SessionID: uuid.New()})
		verificar()
		avanzar(2 * time.Second)
		h.send(simulation.PlayerDisconnected{PlayerID: h.playerID, SessionID: uuid.New()})
		verificar()
		avanzar(pausa)
	}
	require.Equal(t, city.PresenceProtected, c.PresenceState, "el ciclo termina protegido")
	require.NotNil(t, c.LastOnlineAt)
}
