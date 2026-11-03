package simulation_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
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
		sesiones := map[uuid.UUID]uuid.UUID{}
		for _, p := range jugadores {
			sesiones[p] = uuid.New()
			h.send(simulation.PlayerConnected{PlayerID: p, SessionID: sesiones[p]})
		}
		for _, p := range jugadores {
			h.commands <- simulation.PlayerDisconnected{PlayerID: p, SessionID: sesiones[p]}
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
	sesion := uuid.New()
	h.send(simulation.PlayerConnected{PlayerID: h.playerID, SessionID: sesion})
	h.send(simulation.PlayerDisconnected{PlayerID: h.playerID, SessionID: sesion})

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
		sesion := uuid.New()
		h.send(simulation.PlayerConnected{PlayerID: h.playerID, SessionID: sesion})
		verificar()
		avanzar(2 * time.Second)
		h.send(simulation.PlayerDisconnected{PlayerID: h.playerID, SessionID: sesion})
		verificar()
		avanzar(pausa)
	}
	require.Equal(t, city.PresenceProtected, c.PresenceState, "el ciclo termina protegido")
	require.NotNil(t, c.LastOnlineAt)
}

// RN-PRESENCE-023 / ADR-022: la baja de una sesión puede llegar antes que su
// alta —viaja por el buzón cuando el canal de comandos está lleno, y el buzón
// se drena antes— y puede llegar dos veces. Las sesiones se llevan por id, no
// por recuento: la baja adelantada anula el alta cuando llega, y la duplicada
// no resta nada ni reinicia el margen de reconexión.
func TestUnaBajaAdelantadaODuplicadaNoDesajustaLasSesiones(t *testing.T) {
	h := newHarness(t)
	c, _ := h.state.City(h.cityID)
	pestaña, fugaz := uuid.New(), uuid.New()

	h.send(simulation.PlayerConnected{PlayerID: h.playerID, SessionID: pestaña})
	h.send(simulation.PlayerDisconnected{PlayerID: h.playerID, SessionID: fugaz}) // su alta aún no llegó
	h.send(simulation.PlayerConnected{PlayerID: h.playerID, SessionID: fugaz})    // llega después: se anulan
	require.Equal(t, 1, h.sim.SessionCount(h.playerID), "la sesión ya cerrada no cuenta")

	h.send(simulation.PlayerDisconnected{PlayerID: h.playerID, SessionID: pestaña}) // la última: arranca el margen
	require.Equal(t, 0, h.sim.SessionCount(h.playerID))
	h.advance(20 * time.Second)
	h.send(simulation.PlayerDisconnected{PlayerID: h.playerID, SessionID: pestaña}) // duplicada
	require.Equal(t, 0, h.sim.SessionCount(h.playerID), "no baja de cero")
	h.advance(11 * time.Second) // 31 s desde la baja real

	require.Equal(t, city.PresenceOfflinePending, c.PresenceState,
		"la baja duplicada no reinició el margen de reconexión")
}

// Al parar, el loop vuelca lo que quedaba sucio. Una unidad que llegó a su
// destino entre dos volcados periódicos no puede perder esa posición por un
// apagado ordenado: tras reiniciar volvería a donde estaba antes.
func TestAlPararElLoopVuelcaLoQueQuedabaSucio(t *testing.T) {
	h := newHarness(t)
	u := h.units[0]
	h.send(simulation.MoveUnit{PlayerID: h.playerID, RequestID: uuid.NewString(),
		UnitID: u.ID, Target: world.Tile{X: u.X + 2, Y: u.Y}})
	h.advance(1500 * time.Millisecond) // llega, y faltan muchos ticks para el volcado
	require.Equal(t, unit.StatusIdle, u.Status)
	antes := h.repos.unitsFlushed

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, h.loop.Run(ctx), context.Canceled)

	assert.Greater(t, h.repos.unitsFlushed, antes, "el volcado final escribe la unidad que llegó")
}

// SPEC-COMBAT-RAID Q1 (ADR-024 §3): al conceder la protección se fija su fin,
// last_offline_at + cooldown + tope, y viaja en el city.update para que el
// cliente sepa hasta cuándo.
func TestLaProteccionConcedidaLlevaSuFin(t *testing.T) {
	h := newHarness(t)
	c, _ := h.state.City(h.cityID)
	sesion := uuid.New()
	h.send(simulation.PlayerConnected{PlayerID: h.playerID, SessionID: sesion})
	h.send(simulation.PlayerDisconnected{PlayerID: h.playerID, SessionID: sesion})
	h.advance(31 * time.Second)
	require.Equal(t, city.PresenceOfflinePending, c.PresenceState)
	h.rec.reset()

	h.advance(300 * time.Second)
	require.Equal(t, city.PresenceProtected, c.PresenceState)
	require.NotNil(t, c.ProtectionUntil)
	esperado := c.LastOfflineAt.Add(300*time.Second + 48*time.Hour)
	assert.True(t, c.ProtectionUntil.Equal(esperado), "fin %s, esperado %s", c.ProtectionUntil, esperado)

	var fin *int64
	for _, m := range h.rec.byType(protocol.TypeCityUpdate) {
		if p := m.Payload.(protocol.CityUpdatePayload); p.PresenceState != nil && *p.PresenceState == string(city.PresenceProtected) {
			fin = p.ProtectionUntilMs
		}
	}
	require.NotNil(t, fin, "el city.update de PROTECTED lleva protectionUntilMs")
	assert.Equal(t, esperado.UnixMilli(), *fin)
	assert.True(t, c.ProtectedAt(h.clk.Now()))
	assert.False(t, c.ProtectedAt(esperado), "al vencer el tope deja de proteger")
}

// ADR-024 §4: tras una caída con el dueño conectado, la ciudad se queda ONLINE
// sin nadie. Al arrancar se le da por ido en ese instante y sigue el autómata
// normal; una ciudad PROTECTED recupera el fin de su protección, que no se
// persiste.
func TestAlArrancarSeReanclaLaCiudadOnlineSinSesion(t *testing.T) {
	h := newHarness(t)
	conVecinos(h, 1)
	c, _ := h.state.City(h.cityID)
	protegida, _ := h.state.City(2)
	desconexion := h.clk.Now().Add(-time.Hour)
	protegida.PresenceState = city.PresenceProtected
	protegida.LastOfflineAt = &desconexion

	arranque := h.clk.Now()
	h.sim.AfterBoot(arranque)

	require.Equal(t, city.PresenceOfflinePending, c.PresenceState, "ONLINE sin sesión pasa a OFFLINE_PENDING")
	require.NotNil(t, c.LastOfflineAt)
	assert.True(t, c.LastOfflineAt.Equal(arranque))
	assert.Equal(t, city.PresenceOfflinePending, h.repos.presence[h.cityID], "y la base lo sabe")

	require.NotNil(t, protegida.ProtectionUntil)
	assert.True(t, protegida.ProtectionUntil.Equal(desconexion.Add(300*time.Second+48*time.Hour)))
	assert.Equal(t, city.PresenceProtected, protegida.PresenceState, "la protegida no cambia de estado")

	// Desde ahí, el autómata de siempre: el cooldown lleva a PROTECTED.
	h.advance(301 * time.Second)
	assert.Equal(t, city.PresenceProtected, c.PresenceState)
}

// Un dueño que vuelve justo tras el arranque recupera su ciudad: el reanclaje
// no es un castigo, sólo deja de suponer que sigue conectado.
func TestElDuenoQueVuelveTrasElArranqueRecuperaSuCiudad(t *testing.T) {
	h := newHarness(t)
	c, _ := h.state.City(h.cityID)
	h.sim.AfterBoot(h.clk.Now())
	require.Equal(t, city.PresenceOfflinePending, c.PresenceState)

	h.send(simulation.PlayerConnected{PlayerID: h.playerID, SessionID: uuid.New()})
	assert.Equal(t, city.PresenceOnline, c.PresenceState)
}
