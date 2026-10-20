//go:build integration

package postgres_test

// Tests del alta HTTP (internal/httpapi) contra PostgreSQL real. Viven en esta
// suite, y no en internal/httpapi, porque reutilizan su arnés protegido
// (newTestStore): base terminada en _test, Redis fuera del índice 0 y cerrojo
// entre procesos. Duplicar esas guardas en otro paquete es la forma de que un
// día una de las dos copias se quede atrás.

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/auth"
	"github.com/empires-online/empires-online/services/game-server/internal/clock"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/player"
	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/httpapi"
	"github.com/empires-online/empires-online/services/game-server/internal/persistence/postgres"
)

const secretoDePrueba = "secreto-de-la-suite-de-integracion-0123456789"

type alta struct {
	api      *httpapi.AuthAPI
	commands chan simulation.Command
	logs     *bytes.Buffer
}

func montarAlta(t *testing.T, store *postgres.Store, w *world.World, era city.Era, commands chan simulation.Command) alta {
	t.Helper()
	players, cities, _, _, bootstrapper := newRepos(store)
	clk := clock.NewFakeClock(testEpoch)
	logs := &bytes.Buffer{}
	api := httpapi.NewAuthAPI(players, cities, bootstrapper,
		auth.NewIssuer(secretoDePrueba, time.Minute, clk), w, commands,
		httpapi.Options{DefaultEra: era, CivilizationID: 1, FactionID: 3, Clock: clk},
		slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	return alta{api: api, commands: commands, logs: logs}
}

func postJSON(h http.HandlerFunc, body any) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(body)
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodPost, "/api/auth/register", bytes.NewReader(raw)))
	return rec
}

func codigoHTTP(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var e struct {
		Code string `json:"code"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &e), rec.Body.String())
	return e.Code
}

// INV-PLAYER-008: IntroducePlayer se emite DESPUÉS del COMMIT y con los ids que
// asignó PostgreSQL. El canal de comandos no tiene buffer, así que el handler
// queda bloqueado en el envío hasta que el test recibe: en ese instante, el
// jugador, su ciudad y sus aldeanos ya tienen que ser visibles desde otra
// conexión.
func TestElAltaHTTPIntroduceAlJugadorDespuesDelCommit(t *testing.T) {
	store, ctx := newTestStore(t)
	players, cities, units, _, _ := newRepos(store)
	a := montarAlta(t, store, testWorld(t), city.EraStone, make(chan simulation.Command))

	hecho := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		hecho <- postJSON(a.api.Register(), map[string]string{"username": "puntual", "password": "contrasena-larga"})
	}()

	var intro simulation.IntroducePlayer
	select {
	case cmd := <-a.commands:
		var ok bool
		intro, ok = cmd.(simulation.IntroducePlayer)
		require.True(t, ok, "se esperaba IntroducePlayer, llegó %T", cmd)
	case rec := <-hecho:
		t.Fatalf("el alta terminó sin introducir al jugador: %d %s", rec.Code, rec.Body.String())
	}

	// Desde otra conexión, en el instante de recibir el comando.
	p, _, err := players.GetByUsername(ctx, "puntual")
	require.NoError(t, err, "el jugador tiene que estar confirmado antes de introducirse")
	persistida, err := cities.GetByOwner(ctx, p.ID)
	require.NoError(t, err)
	require.NotNil(t, intro.City)
	assert.Equal(t, persistida.ID, intro.City.ID, "la RAM recibe el id definitivo de la ciudad")

	vivas, err := units.ListByPlayer(ctx, p.ID)
	require.NoError(t, err)
	require.Len(t, intro.Units, player.InitialVillagers)
	idsEnDisco := map[int64]bool{}
	for _, u := range vivas {
		idsEnDisco[u.ID] = true
	}
	for _, u := range intro.Units {
		assert.True(t, idsEnDisco[u.ID], "la unidad %d de la RAM no existe en disco", u.ID)
	}

	rec := <-hecho
	assert.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
}

// INV-PLAYER-008, la otra mitad: un alta que no confirma no introduce nada.
func TestUnAltaRechazadaNoIntroduceNada(t *testing.T) {
	store, _ := newTestStore(t)
	a := montarAlta(t, store, testWorld(t), city.EraStone, make(chan simulation.Command, 4))

	rec := postJSON(a.api.Register(), map[string]string{"username": "repetido", "password": "contrasena-larga"})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	<-a.commands

	rec = postJSON(a.api.Register(), map[string]string{"username": "repetido", "password": "otra-contrasena"})
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Equal(t, "USERNAME_TAKEN", codigoHTTP(t, rec))
	assert.Empty(t, a.commands, "el alta rechazada no llega al mundo")
}

// INV-PLAYER-003 y RN-PLAYER-014: sin sitio válido, el alta responde 409
// NO_SITE_AVAILABLE, no escribe nada y no coloca al jugador «donde sea».
func TestSinSitioElAltaResponde409YNoEscribe(t *testing.T) {
	store, ctx := newTestStore(t)
	players, _, _, _, _ := newRepos(store)

	agua := make([]byte, 64*64)
	for i := range agua {
		agua[i] = byte(world.Water)
	}
	oceano, err := world.New(64, 64, 32, 1, agua)
	require.NoError(t, err)
	a := montarAlta(t, store, oceano, city.EraStone, make(chan simulation.Command, 4))

	rec := postJSON(a.api.Register(), map[string]string{"username": "naufrago", "password": "contrasena-larga"})
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Equal(t, "NO_SITE_AVAILABLE", codigoHTTP(t, rec))
	_, _, err = players.GetByUsername(ctx, "naufrago")
	assert.Error(t, err, "no queda jugador")
	assert.Empty(t, a.commands)
}

// INV-PLAYER-007, mitad de los logs. Se recorren las rutas del alta y del login
// que más escriben —éxito, contraseña errónea, usuario inexistente y un alta
// que falla dentro de la transacción y registra el error— y ninguna línea
// contiene la contraseña, un hash bcrypt, un ticket emitido ni el secreto JWT.
func TestLosLogsDeAltaYLoginNoContienenSecretos(t *testing.T) {
	store, _ := newTestStore(t)
	a := montarAlta(t, store, testWorld(t), city.EraStone, make(chan simulation.Command, 8))
	const contrasena = "contrasena-que-no-debe-salir"

	rec := postJSON(a.api.Register(), map[string]string{"username": "discreta", "password": contrasena})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var emitido struct {
		Ticket string `json:"ticket"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &emitido))

	login := postJSON(a.api.Login(), map[string]string{"username": "discreta", "password": contrasena})
	require.Equal(t, http.StatusOK, login.Code)
	var deLogin struct {
		Ticket string `json:"ticket"`
	}
	require.NoError(t, json.Unmarshal(login.Body.Bytes(), &deLogin))
	assert.Equal(t, http.StatusUnauthorized, postJSON(a.api.Login(), map[string]string{"username": "discreta", "password": "equivocada-del-todo"}).Code)
	assert.Equal(t, http.StatusUnauthorized, postJSON(a.api.Login(), map[string]string{"username": "nadie_existe", "password": contrasena}).Code)

	// Un alta que falla dentro de la transacción y SÍ registra el error.
	rota := montarAlta(t, store, testWorld(t), "SPACE_AGE", a.commands)
	fallo := postJSON(rota.api.Register(), map[string]string{"username": "sin_era", "password": contrasena})
	require.Equal(t, http.StatusInternalServerError, fallo.Code)

	todo := a.logs.String() + rota.logs.String()
	require.NotEmpty(t, todo, "el alta fallida tiene que haber escrito algo")
	for nombre, prohibido := range map[string]string{
		"contraseña": contrasena, "hash bcrypt": "$2a$", "secreto JWT": secretoDePrueba,
		"ticket del alta": emitido.Ticket, "ticket del login": deLogin.Ticket,
	} {
		assert.False(t, strings.Contains(todo, prohibido), "los logs contienen %s", nombre)
	}
}
