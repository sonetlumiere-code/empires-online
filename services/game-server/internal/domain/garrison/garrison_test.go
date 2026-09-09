package garrison

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/diplomacy"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
)

// Escenario mínimo: una ciudad en (10,10) y una unidad a su lado.
func ciudadDe(dueno uuid.UUID) *city.City {
	return &city.City{ID: 1, OwnerPlayerID: dueno, Name: "Anfitriona", CenterX: 10, CenterY: 10}
}

func unidadDe(dueno uuid.UUID, x, y int32) *unit.Unit {
	return &unit.Unit{
		ID: 1, PlayerID: dueno, Type: unit.TypeVillager,
		X: x, Y: y, HP: 25, MaxHP: 25, Status: unit.StatusIdle,
	}
}

func tratadoActivoEntre(a, b uuid.UUID, flag bool) diplomacy.Treaty {
	pa, pb := diplomacy.CanonicalPair(a, b)
	ahora := time.Now().UTC()
	return diplomacy.Treaty{
		ID: 1, PlayerA: pa, PlayerB: pb,
		Type: diplomacy.TypeAlliance, Status: diplomacy.StatusActive,
		AllowsGarrison: flag, ProposedAt: ahora, AcceptedAt: &ahora,
	}
}

// ─────────────────────────────────────────────────────────────
// Ciudad propia
// ─────────────────────────────────────────────────────────────

func TestEnCiudadPropiaSeEntraSinTratado(t *testing.T) {
	// RN-GARR-004: la ciudad propia no exige nada más.
	dueno := uuid.New()
	err := CanEnter(unidadDe(dueno, 12, 10), ciudadDe(dueno), dueno, nil)
	assert.NoError(t, err)
}

func TestLaProteccionOfflineDelAnfitrionNoBloqueaLaEntrada(t *testing.T) {
	// RN-GARR-009: la protección es contra agresión, no contra logística.
	dueno := uuid.New()
	c := ciudadDe(dueno)
	c.PresenceState = city.PresenceProtected

	assert.NoError(t, CanEnter(unidadDe(dueno, 12, 10), c, dueno, nil))
}

// ─────────────────────────────────────────────────────────────
// Ciudad ajena y tratado
// ─────────────────────────────────────────────────────────────

func TestEnCiudadAjenaSinTratadoSeRechaza(t *testing.T) {
	visitante, anfitrion := uuid.New(), uuid.New()
	err := CanEnter(unidadDe(visitante, 12, 10), ciudadDe(anfitrion), visitante, nil)
	assert.ErrorIs(t, err, ErrTreatyRequired)
}

func TestEnCiudadAjenaConTratadoActivoYFlagSeEntra(t *testing.T) {
	visitante, anfitrion := uuid.New(), uuid.New()
	tratados := []diplomacy.Treaty{tratadoActivoEntre(visitante, anfitrion, true)}

	assert.NoError(t, CanEnter(unidadDe(visitante, 12, 10), ciudadDe(anfitrion), visitante, tratados))
}

func TestUnTratadoActivoSinElFlagNoAutoriza(t *testing.T) {
	visitante, anfitrion := uuid.New(), uuid.New()
	tratados := []diplomacy.Treaty{tratadoActivoEntre(visitante, anfitrion, false)}

	err := CanEnter(unidadDe(visitante, 12, 10), ciudadDe(anfitrion), visitante, tratados)
	assert.ErrorIs(t, err, ErrTreatyRequired)
}

func TestUnTratadoPropuestoConElFlagNoAutoriza(t *testing.T) {
	// Criterio de aceptación 3 de M7, literal.
	visitante, anfitrion := uuid.New(), uuid.New()
	tr := tratadoActivoEntre(visitante, anfitrion, true)
	tr.Status = diplomacy.StatusProposed

	err := CanEnter(unidadDe(visitante, 12, 10), ciudadDe(anfitrion), visitante, []diplomacy.Treaty{tr})
	assert.ErrorIs(t, err, ErrTreatyRequired)
}

func TestUnTratadoConOtroJugadorNoAutoriza(t *testing.T) {
	visitante, anfitrion, tercero := uuid.New(), uuid.New(), uuid.New()
	tratados := []diplomacy.Treaty{tratadoActivoEntre(visitante, tercero, true)}

	err := CanEnter(unidadDe(visitante, 12, 10), ciudadDe(anfitrion), visitante, tratados)
	assert.ErrorIs(t, err, ErrTreatyRequired)
}

// ─────────────────────────────────────────────────────────────
// Estado de la unidad y propiedad
// ─────────────────────────────────────────────────────────────

func TestSoloElDuenoPuedeGuarnecerSuUnidad(t *testing.T) {
	dueno, intruso := uuid.New(), uuid.New()
	err := CanEnter(unidadDe(dueno, 12, 10), ciudadDe(dueno), intruso, nil)
	assert.ErrorIs(t, err, ErrNotOwned)
}

func TestElOrdenDeComprobacionNoFiltraInformacionSobreTratados(t *testing.T) {
	// Quien ni siquiera es dueño de la unidad debe recibir ErrNotOwned, no
	// ErrTreatyRequired: el segundo le diría si existe o no un tratado entre dos
	// terceros, que no es asunto suyo.
	dueno, anfitrion, curioso := uuid.New(), uuid.New(), uuid.New()

	err := CanEnter(unidadDe(dueno, 12, 10), ciudadDe(anfitrion), curioso, nil)
	assert.ErrorIs(t, err, ErrNotOwned)
}

func TestSoloSeEntraDesdeIdle(t *testing.T) {
	// RN-GARR-002: no existe "entrar en marcha".
	dueno := uuid.New()
	casos := map[unit.Status]error{
		unit.StatusMoving:     ErrNotIdle,
		unit.StatusGarrisoned: ErrAlreadyGarrisoned,
		unit.StatusDead:       ErrDead,
	}
	for estado, esperado := range casos {
		t.Run(string(estado), func(t *testing.T) {
			u := unidadDe(dueno, 12, 10)
			u.Status = estado
			assert.ErrorIs(t, CanEnter(u, ciudadDe(dueno), dueno, nil), esperado)
		})
	}
}

func TestUnaUnidadSinVidaMarcadaIdleTampocoEntra(t *testing.T) {
	// Estado y HP podrían discrepar tras una recuperación parcial. Fiarse sólo
	// del `status` dejaría actuar a una unidad con 0 HP.
	dueno := uuid.New()
	u := unidadDe(dueno, 12, 10)
	u.HP = 0

	assert.ErrorIs(t, CanEnter(u, ciudadDe(dueno), dueno, nil), ErrDead)
}

// ─────────────────────────────────────────────────────────────
// Adyacencia
// ─────────────────────────────────────────────────────────────

func TestLaAdyacenciaCubreLasOchoDirecciones(t *testing.T) {
	// La zona urbana de la ciudad en (10,10) es (9,9)-(11,11); el anillo
	// adyacente es (8,8)-(12,12) sin el interior.
	dueno := uuid.New()
	c := ciudadDe(dueno)

	for _, tile := range [][2]int32{
		{8, 8}, {10, 8}, {12, 8},
		{8, 10}, {12, 10},
		{8, 12}, {10, 12}, {12, 12},
	} {
		t.Run("adyacente", func(t *testing.T) {
			assert.NoError(t, CanEnter(unidadDe(dueno, tile[0], tile[1]), c, dueno, nil),
				"(%d,%d) toca la zona urbana", tile[0], tile[1])
		})
	}
}

func TestUnaUnidadDemasiadoLejosNoEsAdyacente(t *testing.T) {
	dueno := uuid.New()
	c := ciudadDe(dueno)

	for _, tile := range [][2]int32{{7, 10}, {13, 10}, {10, 7}, {10, 13}, {7, 7}} {
		err := CanEnter(unidadDe(dueno, tile[0], tile[1]), c, dueno, nil)
		assert.ErrorIs(t, err, ErrNotAdjacent, "(%d,%d) está fuera del anillo", tile[0], tile[1])
	}
}

func TestUnTileDentroDeLaZonaUrbanaNoCuentaComoAdyacente(t *testing.T) {
	// Además de que la zona está bloqueada y ninguna unidad puede estar ahí,
	// aceptarlo escondería un estado imposible en vez de señalarlo.
	dueno := uuid.New()
	assert.False(t, ciudadDe(dueno).IsAdjacent(10, 10))
	assert.True(t, ciudadDe(dueno).Occupies(10, 10))
}

func TestUnaCiudadInexistenteSeRechaza(t *testing.T) {
	dueno := uuid.New()
	assert.ErrorIs(t, CanEnter(unidadDe(dueno, 12, 10), nil, dueno, nil), ErrCityNotFound)
}

// ─────────────────────────────────────────────────────────────
// Salida
// ─────────────────────────────────────────────────────────────

func TestSoloElDuenoSacaSuUnidad(t *testing.T) {
	dueno, anfitrion := uuid.New(), uuid.New()
	u := unidadDe(dueno, 12, 10)
	u.Status = unit.StatusGarrisoned

	assert.NoError(t, CanLeave(u, dueno))
	assert.ErrorIs(t, CanLeave(u, anfitrion), ErrNotOwned,
		"el anfitrión no puede echar a mano lo que aloja")
}

func TestNoSeSaleDeUnaGuarnicionEnLaQueNoSeEsta(t *testing.T) {
	dueno := uuid.New()
	assert.ErrorIs(t, CanLeave(unidadDe(dueno, 12, 10), dueno), ErrNotIdle)
}

// ─────────────────────────────────────────────────────────────
// Tile de reentrada
// ─────────────────────────────────────────────────────────────

func mundoLlano(t *testing.T) *world.World {
	t.Helper()
	terreno := make([]byte, 32*32)
	for i := range terreno {
		terreno[i] = byte(world.Grassland)
	}
	w, err := world.New(32, 32, 32, 1, terreno)
	require.NoError(t, err)
	return w
}

func TestElTileDeReentradaEsElMenorEnOrdenYX(t *testing.T) {
	// RN-GARR-011: el primer tile del anillo en orden ascendente por (y, x). Con
	// todo libre, la esquina superior izquierda del anillo: (8,8).
	w := mundoLlano(t)
	c := ciudadDe(uuid.New())

	tile, err := ReentryTile(w, c, nil)
	require.NoError(t, err)
	assert.Equal(t, world.Tile{X: 8, Y: 8}, tile)
}

func TestElTileDeReentradaSaltaLosOcupados(t *testing.T) {
	w := mundoLlano(t)
	c := ciudadDe(uuid.New())

	// Se ocupa toda la fila superior del anillo: debe bajar a la siguiente.
	ocupado := func(x, y int32) bool { return y == 8 }

	tile, err := ReentryTile(w, c, ocupado)
	require.NoError(t, err)
	assert.Equal(t, world.Tile{X: 8, Y: 9}, tile)
}

func TestElTileDeReentradaEsDeterminista(t *testing.T) {
	w := mundoLlano(t)
	c := ciudadDe(uuid.New())

	primero, err := ReentryTile(w, c, nil)
	require.NoError(t, err)
	for i := 0; i < 20; i++ {
		otro, err := ReentryTile(w, c, nil)
		require.NoError(t, err)
		require.Equal(t, primero, otro, "la salida debe ser reproducible en tests")
	}
}

func TestUnaCiudadCompletamenteRodeadaDejaLaSalidaPendiente(t *testing.T) {
	// RN-GARR-014: no es un error del jugador ni se inventa un código nuevo. Se
	// devuelve ErrNoReentryTile para que el llamante reintente en el tick
	// siguiente; colocar la unidad en un tile arbitrario rompería INV-UNIT-001.
	w := mundoLlano(t)
	c := ciudadDe(uuid.New())

	todoOcupado := func(x, y int32) bool { return true }

	_, err := ReentryTile(w, c, todoOcupado)
	assert.ErrorIs(t, err, ErrNoReentryTile)
}

func TestElAnilloNoSeSaleDelMundo(t *testing.T) {
	// Una ciudad pegada al borde tiene menos anillo, y pedir tiles fuera del mundo
	// no debe entrar en pánico ni devolver coordenadas imposibles.
	w := mundoLlano(t)
	c := &city.City{ID: 1, OwnerPlayerID: uuid.New(), CenterX: 1, CenterY: 1}

	tile, err := ReentryTile(w, c, nil)
	require.NoError(t, err)
	assert.True(t, w.InBounds(tile.X, tile.Y))
	assert.GreaterOrEqual(t, tile.X, int32(0))
	assert.GreaterOrEqual(t, tile.Y, int32(0))
}
