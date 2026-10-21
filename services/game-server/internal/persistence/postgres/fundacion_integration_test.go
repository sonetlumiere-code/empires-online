//go:build integration

package postgres_test

// Tests de la fundación interna (internal/httpapi, ADR-018) contra PostgreSQL
// real. Viven en esta suite, y no en internal/httpapi, porque reutilizan su
// arnés protegido (newTestStore): base terminada en _test y cerrojo entre
// procesos. Duplicar esas guardas en otro paquete es la forma de que un día una
// de las dos copias se quede atrás.

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/auth"
	"github.com/empires-online/empires-online/services/game-server/internal/clock"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/player"
	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/httpapi"
	"github.com/empires-online/empires-online/services/game-server/internal/persistence/memory"
	"github.com/empires-online/empires-online/services/game-server/internal/persistence/postgres"
)

const secretoDePrueba = "secreto-de-la-suite-de-integracion-0123456789"

type fundacion struct {
	api      *httpapi.FoundingAPI
	commands chan simulation.Command
	logs     *bytes.Buffer
	// interno acuña tokens de fundación; juego, tickets de juego.
	interno *auth.Issuer
	juego   *auth.Issuer
}

func montarFundacion(t *testing.T, store *postgres.Store, w *world.World, era city.Era, commands chan simulation.Command) fundacion {
	t.Helper()
	players, cities, _, _, bootstrapper := newRepos(store)
	clk := clock.NewFakeClock(testEpoch)
	hot := memory.New(clk, 30*time.Second, 10*time.Second, 120*time.Second, 300*time.Second)
	t.Cleanup(func() { _ = hot.Close() })
	logs := &bytes.Buffer{}
	api := httpapi.NewFoundingAPI(players, cities, bootstrapper,
		httpapi.NewFoundingAuthenticator(secretoDePrueba, clk, hot.Tickets()),
		w, commands,
		httpapi.Options{DefaultEra: era, CivilizationID: 1, FactionID: 3, Clock: clk},
		slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	return fundacion{
		api: api, commands: commands, logs: logs,
		interno: auth.NewIssuerFor(secretoDePrueba, time.Minute, clk, auth.InternalAudience),
		juego:   auth.NewIssuer(secretoDePrueba, time.Minute, clk),
	}
}

// fundar llama al endpoint con un token del emisor dado, para el sub dado.
func (f fundacion) fundar(t *testing.T, emisor *auth.Issuer, sub uuid.UUID, body any) *httptest.ResponseRecorder {
	t.Helper()
	token, err := emisor.Issue(sub)
	require.NoError(t, err)
	return f.conToken(token, body)
}

func (f fundacion) conToken(token string, body any) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, httpapi.FoundPath, bytes.NewReader(raw))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	f.api.Found()(rec, req)
	return rec
}

func cuerpo(id uuid.UUID, username string) map[string]string {
	return map[string]string{"playerId": id.String(), "username": username}
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
// conexión. El jugador lleva el id que eligió apps/web (ADR-018).
func TestLaFundacionIntroduceAlJugadorDespuesDelCommit(t *testing.T) {
	store, ctx := newTestStore(t)
	players, cities, units, _, _ := newRepos(store)
	f := montarFundacion(t, store, testWorld(t), city.EraStone, make(chan simulation.Command))
	id := uuid.New()

	hecho := make(chan *httptest.ResponseRecorder, 1)
	go func() { hecho <- f.fundar(t, f.interno, id, cuerpo(id, "puntual")) }()

	var intro simulation.IntroducePlayer
	select {
	case cmd := <-f.commands:
		var ok bool
		intro, ok = cmd.(simulation.IntroducePlayer)
		require.True(t, ok, "se esperaba IntroducePlayer, llegó %T", cmd)
	case rec := <-hecho:
		t.Fatalf("la fundación terminó sin introducir al jugador: %d %s", rec.Code, rec.Body.String())
	}

	// Desde otra conexión, en el instante de recibir el comando.
	p, err := players.GetByUsername(ctx, "puntual")
	require.NoError(t, err, "el jugador tiene que estar confirmado antes de introducirse")
	assert.Equal(t, id, p.ID, "el jugador lleva el id de su cuenta")
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

// INV-PLAYER-009: repetir la fundación del mismo jugador —lo que hace apps/web
// con una cuenta PENDING— devuelve el mismo jugador y la misma ciudad, y no lo
// introduce dos veces en el mundo.
func TestLaFundacionEsIdempotentePorPlayerId(t *testing.T) {
	store, ctx := newTestStore(t)
	f := montarFundacion(t, store, testWorld(t), city.EraStone, make(chan simulation.Command, 4))
	id := uuid.New()

	primera := f.fundar(t, f.interno, id, cuerpo(id, "insistente"))
	require.Equal(t, http.StatusCreated, primera.Code, primera.Body.String())
	<-f.commands

	segunda := f.fundar(t, f.interno, id, cuerpo(id, "insistente"))
	require.Equal(t, http.StatusOK, segunda.Code, segunda.Body.String())
	assert.JSONEq(t, primera.Body.String(), segunda.Body.String(), "misma ciudad")
	assert.Empty(t, f.commands, "no se vuelve a introducir")

	var n int
	require.NoError(t, store.Pool().QueryRow(ctx, `SELECT count(*) FROM cities`).Scan(&n))
	assert.Equal(t, 1, n)
}

// INV-PLAYER-009: el jugador que ya existe se reconoce ANTES de buscar sitio. Si
// no, repetir la fundación de una cuenta PENDING con el mundo ya lleno
// respondería NO_SITE_AVAILABLE, y apps/web borraría una cuenta cuyo jugador sí
// existe.
func TestLaRepeticionNoBuscaSitioAunqueElMundoEsteLleno(t *testing.T) {
	store, ctx := newTestStore(t)
	_, _, _, _, bootstrapper := newRepos(store)
	id := uuid.New()
	req := bootstrapRequest("previo", world.Tile{X: 30, Y: 30})
	req.PlayerID = id
	_, err := bootstrapper.Create(ctx, req)
	require.NoError(t, err)

	agua := make([]byte, 64*64)
	for i := range agua {
		agua[i] = byte(world.Water)
	}
	oceano, err := world.New(64, 64, 32, 1, agua)
	require.NoError(t, err)
	f := montarFundacion(t, store, oceano, city.EraStone, make(chan simulation.Command, 4))

	rec := f.fundar(t, f.interno, id, cuerpo(id, "previo"))
	assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Empty(t, f.commands)
}

// INV-PLAYER-009, la carrera: dos fundaciones del MISMO jugador a la vez —un
// alta que se reintenta mientras el login de la cuenta PENDING la repite— pasan
// las dos la comprobación previa y compiten en la transacción. La que pierde
// tropieza con la unicidad y tiene que responder el jugador que ganó, no un
// USERNAME_TAKEN que haría a apps/web borrar la cuenta. Para que la carrera sea
// determinista, el test retiene el cerrojo de fundación hasta que las dos
// esperan en él.
func TestDosFundacionesSimultaneasDelMismoJugadorAcabanEnUno(t *testing.T) {
	store, ctx := newTestStore(t)
	f := montarFundacion(t, store, testWorld(t), city.EraStone, make(chan simulation.Command, 4))
	id := uuid.New()

	// El de postgres.foundingLockKey, «EO_FUND» en ASCII.
	const cerrojoDeFundacion int64 = 0x454f5f46554e44
	conn, err := store.Pool().Acquire(ctx)
	require.NoError(t, err)
	defer conn.Release()
	_, err = conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, cerrojoDeFundacion)
	require.NoError(t, err)

	resultados := make(chan *httptest.ResponseRecorder, 2)
	for range 2 {
		go func() { resultados <- f.fundar(t, f.interno, id, cuerpo(id, "gemela")) }()
	}
	require.Eventually(t, func() bool {
		var esperando int
		_ = store.Pool().QueryRow(ctx,
			`SELECT count(*) FROM pg_locks WHERE locktype = 'advisory' AND NOT granted`).Scan(&esperando)
		return esperando == 2
	}, 10*time.Second, 20*time.Millisecond, "las dos fundaciones tienen que esperar en el cerrojo")
	_, err = conn.Exec(ctx, `SELECT pg_advisory_unlock($1)`, cerrojoDeFundacion)
	require.NoError(t, err)

	codigos := []int{(<-resultados).Code, (<-resultados).Code}
	assert.ElementsMatch(t, []int{http.StatusCreated, http.StatusOK}, codigos)
	var n int
	require.NoError(t, store.Pool().QueryRow(ctx, `SELECT count(*) FROM cities`).Scan(&n))
	assert.Equal(t, 1, n)
	assert.Len(t, f.commands, 1, "sólo la ganadora introduce al jugador")
}

// INV-PLAYER-008, la otra mitad: una fundación rechazada no introduce nada. El
// nombre ocupado por OTRO id —una cuenta anterior a ADR-018— no se reasigna.
func TestUnNombreDeOtroJugadorNoSeFundaNiSeReasigna(t *testing.T) {
	store, _ := newTestStore(t)
	f := montarFundacion(t, store, testWorld(t), city.EraStone, make(chan simulation.Command, 4))
	primero := uuid.New()

	rec := f.fundar(t, f.interno, primero, cuerpo(primero, "repetido"))
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	<-f.commands

	otro := uuid.New()
	rec = f.fundar(t, f.interno, otro, cuerpo(otro, "repetido"))
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Equal(t, "USERNAME_TAKEN", codigoHTTP(t, rec))
	assert.Empty(t, f.commands, "la fundación rechazada no llega al mundo")

	// Y el id ya fundado con otro nombre tampoco se reutiliza.
	rec = f.fundar(t, f.interno, primero, cuerpo(primero, "impostor"))
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Equal(t, "PLAYER_CONFLICT", codigoHTTP(t, rec))
}

// INV-PLAYER-003 y RN-PLAYER-014: sin sitio válido, la fundación responde 409
// NO_SITE_AVAILABLE, no escribe nada y no coloca al jugador «donde sea».
func TestSinSitioLaFundacionResponde409YNoEscribe(t *testing.T) {
	store, ctx := newTestStore(t)
	players, _, _, _, _ := newRepos(store)

	agua := make([]byte, 64*64)
	for i := range agua {
		agua[i] = byte(world.Water)
	}
	oceano, err := world.New(64, 64, 32, 1, agua)
	require.NoError(t, err)
	f := montarFundacion(t, store, oceano, city.EraStone, make(chan simulation.Command, 4))

	id := uuid.New()
	rec := f.fundar(t, f.interno, id, cuerpo(id, "naufrago"))
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Equal(t, "NO_SITE_AVAILABLE", codigoHTTP(t, rec))
	_, err = players.GetByUsername(ctx, "naufrago")
	assert.Error(t, err, "no queda jugador")
	assert.Empty(t, f.commands)
}

// INV-SEC-008: un ticket de juego, firmado con el mismo secreto, no funda nada;
// tampoco una petición sin token.
func TestUnTicketDeJuegoNoFundaJugadores(t *testing.T) {
	store, ctx := newTestStore(t)
	players, _, _, _, _ := newRepos(store)
	f := montarFundacion(t, store, testWorld(t), city.EraStone, make(chan simulation.Command, 4))
	id := uuid.New()

	rec := f.fundar(t, f.juego, id, cuerpo(id, "colado"))
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Equal(t, "UNAUTHORIZED", codigoHTTP(t, rec))

	rec = f.conToken("", cuerpo(id, "colado"))
	assert.Equal(t, http.StatusUnauthorized, rec.Code)

	_, err := players.GetByUsername(ctx, "colado")
	assert.Error(t, err, "no queda jugador")
	assert.Empty(t, f.commands)
}

// INV-SEC-008 e INV-SEC-003: un token de fundación es de un solo uso, y sólo
// autoriza a fundar el id de su sub.
func TestUnTokenDeFundacionSirveUnaVezYSoloParaSuSub(t *testing.T) {
	store, ctx := newTestStore(t)
	players, _, _, _, _ := newRepos(store)
	f := montarFundacion(t, store, testWorld(t), city.EraStone, make(chan simulation.Command, 4))

	autorizado := uuid.New()
	otro := uuid.New()
	token, err := f.interno.Issue(autorizado)
	require.NoError(t, err)

	rec := f.conToken(token, cuerpo(otro, "suplantado"))
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Equal(t, "FORBIDDEN", codigoHTTP(t, rec))
	_, err = players.GetByUsername(ctx, "suplantado")
	assert.Error(t, err, "no queda jugador")

	// El token ya se consumió al verificarse: tampoco sirve para su propio sub.
	rec = f.conToken(token, cuerpo(autorizado, "legitimo"))
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Empty(t, f.commands)
}

// INV-PLAYER-007, mitad de los logs: se recorren las rutas de la fundación que
// más escriben —éxito, token rechazado y una fundación que falla dentro de la
// transacción y registra el error— y ninguna línea contiene un token ni el
// secreto.
func TestLosLogsDeLaFundacionNoContienenSecretos(t *testing.T) {
	store, _ := newTestStore(t)
	f := montarFundacion(t, store, testWorld(t), city.EraStone, make(chan simulation.Command, 8))

	id := uuid.New()
	bueno, err := f.interno.Issue(id)
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, f.conToken(bueno, cuerpo(id, "discreta")).Code)

	deJuego, err := f.juego.Issue(id)
	require.NoError(t, err)
	require.Equal(t, http.StatusUnauthorized, f.conToken(deJuego, cuerpo(id, "discreta")).Code)

	// Una fundación que falla dentro de la transacción y SÍ registra el error.
	rota := montarFundacion(t, store, testWorld(t), "SPACE_AGE", f.commands)
	otroID := uuid.New()
	malo, err := rota.interno.Issue(otroID)
	require.NoError(t, err)
	require.Equal(t, http.StatusInternalServerError, rota.conToken(malo, cuerpo(otroID, "sin_era")).Code)

	todo := f.logs.String() + rota.logs.String()
	require.NotEmpty(t, todo, "la fundación fallida tiene que haber escrito algo")
	for nombre, prohibido := range map[string]string{
		"secreto": secretoDePrueba, "token de fundación": bueno,
		"ticket de juego": deJuego, "token fallido": malo,
	} {
		assert.False(t, strings.Contains(todo, prohibido), "los logs contienen %s", nombre)
	}
}
