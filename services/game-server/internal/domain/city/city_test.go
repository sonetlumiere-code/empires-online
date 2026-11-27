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

// SPEC-COMBAT-RAID Q1 (ADR-024 §3): la protección vence en
// last_offline_at + cooldown + tope; antes frena un ataque y después no.
// Sin tope, protege mientras dure el estado.
func TestLaProteccionVenceEnSuTope(t *testing.T) {
	desconexion := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	fin := city.ProtectionEndsAt(&desconexion, 5*time.Minute, 48*time.Hour)
	require.NotNil(t, fin)
	require.Equal(t, desconexion.Add(5*time.Minute+48*time.Hour), *fin)

	c := &city.City{PresenceState: city.PresenceProtected, ProtectionUntil: fin}
	require.True(t, c.ProtectedAt(fin.Add(-time.Millisecond)))
	require.False(t, c.ProtectedAt(*fin), "el instante del tope ya no protege")
	require.True(t, c.IsProtected(), "el estado no cambia al vencer: sólo deja de proteger")

	require.Nil(t, city.ProtectionEndsAt(&desconexion, 5*time.Minute, 0), "tope cero: sin fin")
	require.Nil(t, city.ProtectionEndsAt(nil, 5*time.Minute, time.Hour))
	sinTope := &city.City{PresenceState: city.PresenceProtected}
	require.True(t, sinTope.ProtectedAt(desconexion.Add(1000*time.Hour)))
	require.False(t, (&city.City{PresenceState: city.PresenceOfflinePending}).ProtectedAt(desconexion),
		"OFFLINE_PENDING no protege: es la ventana de riesgo")
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

// SPEC-CITY-NAMES RN-CITY-043 y RN-CITY-044, INV-CITY-025: la fundada lleva el
// primer nombre libre de la serie de su dueño, también cuando cayó la capital,
// cuando cayó la del medio o cuando la capital es una tomada (§4.2).
func TestLaFundadaLlevaElPrimerNombreLibreDeSuSerie(t *testing.T) {
	casos := []struct {
		nombre string
		enPie  []string
		quiere string
	}{
		{"la segunda", []string{"Xpolis"}, "Xpolis II"},
		{"cayó la capital", []string{"Xpolis II"}, "Xpolis"},
		{"cayó la del medio", []string{"Xpolis", "Xpolis III"}, "Xpolis II"},
		{"la capital es tomada", []string{"Ypolis"}, "Xpolis"},
		{"la cuarta", []string{"Xpolis", "Xpolis II", "Xpolis III"}, "Xpolis IV"},
		{"con una tomada en medio", []string{"Xpolis", "Ypolis", "Xpolis II"}, "Xpolis III"},
	}
	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			assert.Equal(t, c.quiere, city.FoundedName("X", c.enPie))
		})
	}
	assert.Equal(t, "Xpolis", city.FirstFreeName("Xpolis", []string{"Xpolis II", "Xpolis III", "Xpolis IV", "Ypolis"}),
		"con la serie llena sólo queda la base: fundar ya lo ha impedido el tope")
}

// RN-CITY-043: la base de un nombre generado es el nombre sin sus numerales,
// también los encadenados de antes de E40; un nombre sin numeral es su base.
func TestLaBaseDeUnNombreQuitaSusNumerales(t *testing.T) {
	assert.Equal(t, "Xpolis", city.SeriesBase("Xpolis"))
	assert.Equal(t, "Xpolis", city.SeriesBase("Xpolis II"))
	assert.Equal(t, "Xpolis", city.SeriesBase("Xpolis III"))
	assert.Equal(t, "Xpolis", city.SeriesBase("Xpolis IV"))
	assert.Equal(t, "bot_constructor_2_e35polis", city.SeriesBase("bot_constructor_2_e35polis II II"))
	assert.Equal(t, "XIII", city.SeriesBase("XIII"), "sin espacio no es un numeral")
	assert.Equal(t, " II", city.SeriesBase(" II"), "un numeral solo no se queda en nada")
	assert.Equal(t, "Xpolis", city.BaseName("X"))
}

// RN-CITY-045, INV-CITY-025: la tomada conserva su nombre salvo que el vencedor
// ya tenga en pie otra que lo lleve; entonces, el primero libre de su serie.
func TestLaTomadaConservaSuNombreSalvoQueSeRepita(t *testing.T) {
	assert.Equal(t, "smoke_caidapolis", city.TakenName("smoke_caidapolis", []string{"smoke_runnerpolis"}))
	assert.Equal(t, "smoke_caidapolis II",
		city.TakenName("smoke_caidapolis", []string{"smoke_runnerpolis", "smoke_caidapolis"}))
	assert.Equal(t, "smoke_caidapolis III",
		city.TakenName("smoke_caidapolis", []string{"smoke_runnerpolis", "smoke_caidapolis", "smoke_caidapolis II"}))
	assert.Equal(t, "Ypolis", city.TakenName("Ypolis II", []string{"Xpolis", "Ypolis II"}),
		"la serie de «Ypolis II» empieza en su base")
	assert.Equal(t, "Ypolis III", city.TakenName("Ypolis III", []string{"Xpolis", "Ypolis"}),
		"sin repetirse conserva el numeral que traía")
}

// SPEC-MORE-CITIES RN-CITY-040: el tope de cada era, el techo de cuatro y el menor hueco libre.
func TestElTopeDeCiudadesPorEra(t *testing.T) {
	assert.Equal(t, 4, city.MaxStandingPerOwner)
	assert.Equal(t, 1, city.MaxStandingFor(city.EraStone))
	assert.Equal(t, 2, city.MaxStandingFor(city.EraBronze))
	assert.Equal(t, 3, city.MaxStandingFor(city.EraIron))
	assert.Equal(t, 4, city.MaxStandingFor(city.EraCastle))
	assert.Equal(t, int16(1), city.FreeSlot(nil))
	assert.Equal(t, int16(2), city.FreeSlot([]int16{1, 3}))
	assert.Equal(t, int16(3), city.FreeSlot([]int16{2, 1}))
	assert.Equal(t, int16(0), city.FreeSlot([]int16{1, 2, 3, 4}))
}
