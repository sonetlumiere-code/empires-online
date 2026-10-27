package city_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
)

// INV-CITY-004: el autómata de presencia sólo admite las transiciones definidas.
func TestAutomataDePresencia(t *testing.T) {
	permitidas := []struct{ from, to city.PresenceState }{
		{city.PresenceOnline, city.PresenceOfflinePending},
		{city.PresenceOfflinePending, city.PresenceProtected},
		{city.PresenceOfflinePending, city.PresenceOnline},
		{city.PresenceProtected, city.PresenceOnline},
	}
	for _, c := range permitidas {
		require.True(t, city.CanTransition(c.from, c.to), "%s -> %s debería permitirse", c.from, c.to)
	}

	prohibidas := []struct {
		from, to city.PresenceState
		why      string
	}{
		{city.PresenceOnline, city.PresenceProtected,
			"no se puede pasar a protegido sin cumplir antes el cooldown"},
		{city.PresenceProtected, city.PresenceOfflinePending,
			"la protección no retrocede a pendiente"},
		{city.PresenceOnline, "SOMETHING_ELSE", "estado desconocido"},
		{"NOPE", city.PresenceOnline, "estado de origen desconocido"},
	}
	for _, c := range prohibidas {
		require.False(t, city.CanTransition(c.from, c.to), "%s -> %s: %s", c.from, c.to, c.why)
	}
}

func TestTransicionAlMismoEstadoEsIdempotente(t *testing.T) {
	// Reaplicar el estado actual no es un error: la reconexión puede llegar dos
	// veces y no debe registrarse como una violación.
	for _, s := range []city.PresenceState{city.PresenceOnline, city.PresenceOfflinePending, city.PresenceProtected} {
		require.True(t, city.CanTransition(s, s))
	}
}

func TestNextStateOnDisconnect(t *testing.T) {
	require.Equal(t, city.PresenceOfflinePending, city.NextStateOnDisconnect(city.PresenceOnline))
	// Desconectarse otra vez estando ya offline no cambia nada.
	require.Equal(t, city.PresenceOfflinePending, city.NextStateOnDisconnect(city.PresenceOfflinePending))
	require.Equal(t, city.PresenceProtected, city.NextStateOnDisconnect(city.PresenceProtected))
}

func TestShouldEngageProtection(t *testing.T) {
	base := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	cooldown := 300 * time.Second
	offline := base

	t.Run("no aplica si la ciudad está en línea", func(t *testing.T) {
		require.False(t, city.ShouldEngageProtection(
			city.PresenceOnline, &offline, cooldown, base.Add(time.Hour)))
	})

	t.Run("no aplica sin marca de desconexión", func(t *testing.T) {
		require.False(t, city.ShouldEngageProtection(
			city.PresenceOfflinePending, nil, cooldown, base.Add(time.Hour)))
	})

	t.Run("aún no vence", func(t *testing.T) {
		require.False(t, city.ShouldEngageProtection(
			city.PresenceOfflinePending, &offline, cooldown, base.Add(299*time.Second)))
	})

	t.Run("vence exactamente en el límite", func(t *testing.T) {
		require.True(t, city.ShouldEngageProtection(
			city.PresenceOfflinePending, &offline, cooldown, base.Add(300*time.Second)))
	})

	t.Run("vencido hace mucho", func(t *testing.T) {
		require.True(t, city.ShouldEngageProtection(
			city.PresenceOfflinePending, &offline, cooldown, base.Add(24*time.Hour)))
	})

	t.Run("cooldown cero protege de inmediato", func(t *testing.T) {
		require.True(t, city.ShouldEngageProtection(
			city.PresenceOfflinePending, &offline, 0, base))
	})
}

// INV-CITY-002: la población nunca supera el límite.
func TestLimiteDePoblacion(t *testing.T) {
	c := &city.City{Population: 3, PopulationLimit: 20}

	require.True(t, c.HasPopulationRoom(1))
	require.True(t, c.HasPopulationRoom(17))
	require.False(t, c.HasPopulationRoom(18))

	lleno := &city.City{Population: 20, PopulationLimit: 20}
	require.False(t, lleno.HasPopulationRoom(1))
	require.True(t, lleno.HasPopulationRoom(0))
}

func TestEstadosDePresenciaValidos(t *testing.T) {
	require.True(t, city.PresenceOnline.Valid())
	require.True(t, city.PresenceOfflinePending.Valid())
	require.True(t, city.PresenceProtected.Valid())
	require.False(t, city.PresenceState("INVISIBLE").Valid())
}

func TestIsProtected(t *testing.T) {
	require.True(t, (&city.City{PresenceState: city.PresenceProtected}).IsProtected())
	require.False(t, (&city.City{PresenceState: city.PresenceOfflinePending}).IsProtected())
	require.False(t, (&city.City{PresenceState: city.PresenceOnline}).IsProtected())
}

// RN-ECON-004 y RN-ECON-011: el anillo de nacimiento es el de los aldeanos
// iniciales, a distancia 2 del centro, en un orden fijo; el primer tile libre
// es el de depósito y el de nacimiento.
func TestElAnilloDeNacimientoTieneOrdenFijo(t *testing.T) {
	c := &city.City{CenterX: 10, CenterY: 10}
	anillo := city.SpawnTilesAround(c.Center())
	require.Len(t, anillo, 8)
	assert.Equal(t, world.Tile{X: 12, Y: 10}, anillo[0])
	assert.Equal(t, world.Tile{X: 8, Y: 10}, anillo[1])
	for _, t2 := range anillo {
		assert.True(t, c.IsAdjacent(t2.X, t2.Y), "%s es adyacente a la zona urbana", t2)
	}

	primero, ok := c.FirstSpawnTile(func(x, y int32) bool { return true })
	require.True(t, ok)
	assert.Equal(t, world.Tile{X: 12, Y: 10}, primero)

	segundo, ok := c.FirstSpawnTile(func(x, y int32) bool { return !(x == 12 && y == 10) })
	require.True(t, ok)
	assert.Equal(t, world.Tile{X: 8, Y: 10}, segundo)

	_, ok = c.FirstSpawnTile(func(x, y int32) bool { return false })
	assert.False(t, ok, "sin tile libre no hay depósito ni nacimiento")
}
