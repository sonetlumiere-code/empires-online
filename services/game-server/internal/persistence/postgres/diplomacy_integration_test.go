//go:build integration

package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/diplomacy"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/garrison"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/movement"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/persistence/postgres"
)

// escenarioDiplomatico crea dos jugadores con su ciudad, lo bastante separados
// para que sus zonas urbanas no se toquen.
type escenarioDiplomatico struct {
	visitante *postgres.BootstrapResult
	anfitrion *postgres.BootstrapResult
	treaties  *postgres.TreatyRepo
	garrisons *postgres.GarrisonRepo
	movements *postgres.MovementRepo
	units     *postgres.UnitRepo
}

func nuevoEscenario(t *testing.T, ctx context.Context, store *postgres.Store) escenarioDiplomatico {
	t.Helper()
	_, _, units, movements, bootstrapper := newRepos(store)

	visitante, err := bootstrapper.Create(ctx, bootstrapRequest("visitante", world.Tile{X: 10, Y: 10}))
	require.NoError(t, err)
	anfitrion, err := bootstrapper.Create(ctx, bootstrapRequest("anfitrion", world.Tile{X: 40, Y: 40}))
	require.NoError(t, err)

	treaties := postgres.NewTreatyRepo(store)
	return escenarioDiplomatico{
		visitante: visitante,
		anfitrion: anfitrion,
		treaties:  treaties,
		garrisons: postgres.NewGarrisonRepo(store, units, movements, treaties),
		movements: movements,
		units:     units,
	}
}

// pegadaA coloca una unidad justo al lado de la zona urbana de una ciudad.
func pegadaA(t *testing.T, ctx context.Context, store *postgres.Store, u *unit.Unit, c *city.City) {
	t.Helper()
	minX, minY, _, _ := c.UrbanBounds()
	x, y := minX-1, minY-1
	_, err := store.Pool().Exec(ctx, `UPDATE units SET x = $2, y = $3 WHERE id = $1`, u.ID, x, y)
	require.NoError(t, err)
	u.X, u.Y = x, y
}

func tratadoActivo(t *testing.T, ctx context.Context, store *postgres.Store, repo *postgres.TreatyRepo,
	a, b uuid.UUID, flag bool) diplomacy.Treaty {
	t.Helper()
	ahora := time.Now().UTC()
	pa, pb := diplomacy.CanonicalPair(a, b)
	tr, err := repo.Create(ctx, store.Pool(), diplomacy.Treaty{
		PlayerA: pa, PlayerB: pb,
		Type: diplomacy.TypeAlliance, Status: diplomacy.StatusActive,
		AllowsGarrison: flag, AcceptedAt: &ahora,
	})
	require.NoError(t, err)
	return tr
}

// ─────────────────────────────────────────────────────────────
// Tratados
// ─────────────────────────────────────────────────────────────

func TestElParDelTratadoSeGuardaSiempreEnOrdenCanonico(t *testing.T) {
	store, ctx := newTestStore(t)
	e := nuevoEscenario(t, ctx, store)

	a, b := e.visitante.Player.ID, e.anfitrion.Player.ID
	menor, mayor := diplomacy.CanonicalPair(a, b)

	// Se crea con el par INVERTIDO a propósito: el repositorio debe normalizarlo
	// antes de escribir, no dejar que el CHECK lo rechace.
	ahora := time.Now().UTC()
	tr, err := e.treaties.Create(ctx, store.Pool(), diplomacy.Treaty{
		PlayerA: mayor, PlayerB: menor,
		Type: diplomacy.TypeTrade, Status: diplomacy.StatusActive,
		AllowsGarrison: true, AcceptedAt: &ahora,
	})
	require.NoError(t, err)

	assert.Equal(t, menor, tr.PlayerA)
	assert.Equal(t, mayor, tr.PlayerB)
	assert.NoError(t, tr.Validate())
}

func TestSoloPuedeHaberUnTratadoActivoPorParYTipo(t *testing.T) {
	// El índice único parcial `treaties_one_active_per_pair_and_type`. Es una
	// garantía de la BASE, no del dominio: comprobarla aquí es la única forma de
	// saber que existe de verdad.
	store, ctx := newTestStore(t)
	e := nuevoEscenario(t, ctx, store)
	a, b := e.visitante.Player.ID, e.anfitrion.Player.ID

	// El helper crea una ALLIANCE.
	tratadoActivo(t, ctx, store, e.treaties, a, b, true)

	ahora := time.Now().UTC()
	pa, pb := diplomacy.CanonicalPair(a, b)

	_, err := e.treaties.Create(ctx, store.Pool(), diplomacy.Treaty{
		PlayerA: pa, PlayerB: pb,
		Type: diplomacy.TypeTrade, Status: diplomacy.StatusActive, AcceptedAt: &ahora,
	})
	require.NoError(t, err, "otro TIPO sí puede estar activo a la vez entre el mismo par")

	_, err = e.treaties.Create(ctx, store.Pool(), diplomacy.Treaty{
		PlayerA: pa, PlayerB: pb,
		Type: diplomacy.TypeAlliance, Status: diplomacy.StatusActive, AcceptedAt: &ahora,
	})
	require.Error(t, err, "una SEGUNDA alianza activa entre el mismo par, no")
	// Se comprueba contra ErrConflict y no con IsUniqueViolation porque el
	// repositorio normaliza: `normalize` traduce la violación de unicidad al
	// error estable del paquete y no conserva el error del driver en la cadena.
	// Afirmar sobre el error del driver aquí pasaría por casualidad sólo en los
	// caminos que se saltan la normalización.
	assert.ErrorIs(t, err, postgres.ErrConflict,
		"y debe ser precisamente el índice único parcial, no otro error")
}

func TestLaTransicionRechazaUnTratadoQueYaCambio(t *testing.T) {
	// Concurrencia: entre la lectura y la escritura, otro camino rompió el
	// tratado. Sin la condición `status = $2` del WHERE, la caducidad pisaría la
	// ruptura y se perdería el motivo real por el que terminó.
	store, ctx := newTestStore(t)
	e := nuevoEscenario(t, ctx, store)
	tr := tratadoActivo(t, ctx, store, e.treaties, e.visitante.Player.ID, e.anfitrion.Player.ID, true)

	_, err := e.treaties.Transition(ctx, store.Pool(), tr, diplomacy.StatusBroken, time.Now())
	require.NoError(t, err)

	// `tr` sigue diciendo ACTIVE: es una lectura vieja.
	_, err = e.treaties.Transition(ctx, store.Pool(), tr, diplomacy.StatusExpired, time.Now())
	assert.ErrorIs(t, err, postgres.ErrVersionConflict)

	final, err := e.treaties.ListAll(ctx)
	require.NoError(t, err)
	require.Len(t, final, 1)
	assert.Equal(t, diplomacy.StatusBroken, final[0].Status, "gana quien escribió primero")
}

func TestLaCaducidadSeDetectaYTransicionaAExpired(t *testing.T) {
	// Criterio de aceptación 6 de M7. Se usa `ListExpirable` con un instante
	// explícito, que es lo que hará la fase 5 del tick con su reloj inyectado.
	store, ctx := newTestStore(t)
	e := nuevoEscenario(t, ctx, store)

	vencido := time.Now().UTC().Add(-time.Minute)
	ahora := time.Now().UTC()
	pa, pb := diplomacy.CanonicalPair(e.visitante.Player.ID, e.anfitrion.Player.ID)
	tr, err := e.treaties.Create(ctx, store.Pool(), diplomacy.Treaty{
		PlayerA: pa, PlayerB: pb,
		Type: diplomacy.TypeAlliance, Status: diplomacy.StatusActive,
		AllowsGarrison: true, AcceptedAt: &ahora, ExpiresAt: &vencido,
	})
	require.NoError(t, err)

	pendientes, err := e.treaties.ListExpirable(ctx, time.Now().UTC())
	require.NoError(t, err)
	require.Len(t, pendientes, 1)
	assert.Equal(t, tr.ID, pendientes[0].ID)

	caducado, err := e.treaties.Transition(ctx, store.Pool(), pendientes[0], diplomacy.StatusExpired, time.Now())
	require.NoError(t, err)
	assert.Equal(t, diplomacy.StatusExpired, caducado.Status)
	require.NotNil(t, caducado.ExpiresAt)
	assert.WithinDuration(t, vencido, *caducado.ExpiresAt, time.Second,
		"expires_at conserva cuándo DEBÍA caducar, no cuándo se detectó")

	// Y ya no vuelve a salir en la consulta.
	pendientes, err = e.treaties.ListExpirable(ctx, time.Now().UTC())
	require.NoError(t, err)
	assert.Empty(t, pendientes)
}

func TestUnTratadoSinVencimientoNuncaSaleEnLaConsultaDeCaducidad(t *testing.T) {
	store, ctx := newTestStore(t)
	e := nuevoEscenario(t, ctx, store)
	tratadoActivo(t, ctx, store, e.treaties, e.visitante.Player.ID, e.anfitrion.Player.ID, true)

	pendientes, err := e.treaties.ListExpirable(ctx, time.Now().UTC().Add(100*365*24*time.Hour))
	require.NoError(t, err)
	assert.Empty(t, pendientes)
}

// ─────────────────────────────────────────────────────────────
// Guarnición
// ─────────────────────────────────────────────────────────────

func TestGuarnecerEnCiudadAjenaSinTratadoSeRechaza(t *testing.T) {
	// Criterio de aceptación 1 de M7.
	store, ctx := newTestStore(t)
	e := nuevoEscenario(t, ctx, store)

	u := e.visitante.Units[0]
	pegadaA(t, ctx, store, u, e.anfitrion.City)

	_, err := e.garrisons.Enter(ctx, u, e.anfitrion.City, e.visitante.Player.ID, 1)
	assert.ErrorIs(t, err, garrison.ErrTreatyRequired)

	// Y nada se escribió: la transacción revirtió entera.
	_, guarnecida, err := e.garrisons.CityOf(ctx, u.ID)
	require.NoError(t, err)
	assert.False(t, guarnecida)
}

func TestGuarnecerConTratadoActivoDejaLaUnidadGarrisonedYPersistida(t *testing.T) {
	// Criterio de aceptación 2 de M7.
	store, ctx := newTestStore(t)
	e := nuevoEscenario(t, ctx, store)
	tratadoActivo(t, ctx, store, e.treaties, e.visitante.Player.ID, e.anfitrion.Player.ID, true)

	u := e.visitante.Units[0]
	pegadaA(t, ctx, store, u, e.anfitrion.City)

	_, err := e.garrisons.Enter(ctx, u, e.anfitrion.City, e.visitante.Player.ID, 7)
	require.NoError(t, err)

	cityID, guarnecida, err := e.garrisons.CityOf(ctx, u.ID)
	require.NoError(t, err)
	require.True(t, guarnecida)
	assert.Equal(t, e.anfitrion.City.ID, cityID)

	// El estado persistido, releído de la base y no del objeto en memoria.
	vivas, err := e.units.ListByPlayer(ctx, e.visitante.Player.ID)
	require.NoError(t, err)
	var encontrada *unit.Unit
	for _, v := range vivas {
		if v.ID == u.ID {
			encontrada = v
		}
	}
	require.NotNil(t, encontrada)
	assert.Equal(t, unit.StatusGarrisoned, encontrada.Status)
}

func TestUnTratadoSinElFlagNoHabilitaGuarnicion(t *testing.T) {
	store, ctx := newTestStore(t)
	e := nuevoEscenario(t, ctx, store)
	tratadoActivo(t, ctx, store, e.treaties, e.visitante.Player.ID, e.anfitrion.Player.ID, false)

	u := e.visitante.Units[0]
	pegadaA(t, ctx, store, u, e.anfitrion.City)

	_, err := e.garrisons.Enter(ctx, u, e.anfitrion.City, e.visitante.Player.ID, 1)
	assert.ErrorIs(t, err, garrison.ErrTreatyRequired)
}

func TestUnTratadoRotoDejaDeHabilitarGuarnicionesNuevas(t *testing.T) {
	// RN-GARR-018: la autorización se recalcula SIEMPRE contra el estado actual,
	// nunca contra un tratado que un día estuvo activo.
	store, ctx := newTestStore(t)
	e := nuevoEscenario(t, ctx, store)
	tr := tratadoActivo(t, ctx, store, e.treaties, e.visitante.Player.ID, e.anfitrion.Player.ID, true)

	_, err := e.treaties.Transition(ctx, store.Pool(), tr, diplomacy.StatusBroken, time.Now())
	require.NoError(t, err)

	u := e.visitante.Units[0]
	pegadaA(t, ctx, store, u, e.anfitrion.City)

	_, err = e.garrisons.Enter(ctx, u, e.anfitrion.City, e.visitante.Player.ID, 1)
	assert.ErrorIs(t, err, garrison.ErrTreatyRequired)
}

func TestEnCiudadPropiaSeGuarneceSinNingunTratado(t *testing.T) {
	store, ctx := newTestStore(t)
	e := nuevoEscenario(t, ctx, store)

	u := e.visitante.Units[0]
	pegadaA(t, ctx, store, u, e.visitante.City)

	_, err := e.garrisons.Enter(ctx, u, e.visitante.City, e.visitante.Player.ID, 1)
	require.NoError(t, err)

	_, guarnecida, err := e.garrisons.CityOf(ctx, u.ID)
	require.NoError(t, err)
	assert.True(t, guarnecida)
}

func TestGuarnecerUnaUnidadEnMovimientoCancelaSuMovimiento(t *testing.T) {
	// Criterio de aceptación 5 de M7: el movimiento queda CANCELLED y no queda
	// ninguno ACTIVE. Las dos cosas en la MISMA transacción — una unidad
	// GARRISONED con un movimiento vivo la seguiría moviendo el bucle.
	store, ctx := newTestStore(t)
	e := nuevoEscenario(t, ctx, store)
	w := testWorld(t)

	u := e.visitante.Units[0]
	pegadaA(t, ctx, store, u, e.visitante.City)

	path, err := movement.BuildTimedPath(
		[]world.Tile{{X: u.X, Y: u.Y}, {X: u.X, Y: u.Y + 1}}, w, 600)
	require.NoError(t, err)
	m := movement.New(u.ID, path, world.Tile{X: u.X, Y: u.Y + 1}, testEpoch)
	_, _, err = e.movements.Start(ctx, store.Pool(), m)
	require.NoError(t, err)

	// La unidad está en marcha; guarnecer la detiene.
	u.Status = unit.StatusIdle // el repositorio no exige MOVING para cancelar
	cancelado, err := e.garrisons.Enter(ctx, u, e.visitante.City, e.visitante.Player.ID, 3)
	require.NoError(t, err)
	assert.NotZero(t, cancelado, "debe informar de qué movimiento canceló")

	activos, err := e.movements.ListActive(ctx)
	require.NoError(t, err)
	assert.Empty(t, activos, "ningún movimiento puede quedar ACTIVE")

	var estado string
	require.NoError(t, store.Pool().QueryRow(ctx,
		`SELECT status FROM unit_movements WHERE id = $1`, cancelado).Scan(&estado))
	assert.Equal(t, "CANCELLED", estado)
}

func TestSalirDeLaGuarnicionDevuelveLaUnidadAlMapaYBorraLaFila(t *testing.T) {
	// RN-GARR-012 y RN-GARR-013.
	store, ctx := newTestStore(t)
	e := nuevoEscenario(t, ctx, store)

	u := e.visitante.Units[0]
	pegadaA(t, ctx, store, u, e.visitante.City)
	_, err := e.garrisons.Enter(ctx, u, e.visitante.City, e.visitante.Player.ID, 1)
	require.NoError(t, err)

	salida, err := garrison.ReentryTile(testWorld(t), e.visitante.City, nil)
	require.NoError(t, err)

	require.NoError(t, e.garrisons.Leave(ctx, u, salida.X, salida.Y, salida.X/32, salida.Y/32, 2))

	_, guarnecida, err := e.garrisons.CityOf(ctx, u.ID)
	require.NoError(t, err)
	assert.False(t, guarnecida, "la fila se borra: la tabla sólo modela guarniciones abiertas")

	vivas, err := e.units.ListByPlayer(ctx, e.visitante.Player.ID)
	require.NoError(t, err)
	for _, v := range vivas {
		if v.ID == u.ID {
			assert.Equal(t, unit.StatusIdle, v.Status)
			assert.Equal(t, salida.X, v.X)
			assert.Equal(t, salida.Y, v.Y)
		}
	}
}

func TestTratadosYGuarnicionesSobrevivenAUnReinicio(t *testing.T) {
	// El *recovery* de M7: se tira todo lo que hay en memoria y se relee.
	store, ctx := newTestStore(t)
	e := nuevoEscenario(t, ctx, store)
	tratadoActivo(t, ctx, store, e.treaties, e.visitante.Player.ID, e.anfitrion.Player.ID, true)

	u := e.visitante.Units[0]
	pegadaA(t, ctx, store, u, e.anfitrion.City)
	_, err := e.garrisons.Enter(ctx, u, e.anfitrion.City, e.visitante.Player.ID, 1)
	require.NoError(t, err)

	// Repositorios nuevos sobre el mismo store: nada en memoria se reutiliza.
	treaties := postgres.NewTreatyRepo(store)
	_, _, units, movements, _ := newRepos(store)
	garrisons := postgres.NewGarrisonRepo(store, units, movements, treaties)

	activos, err := treaties.ListActive(ctx)
	require.NoError(t, err)
	require.Len(t, activos, 1)
	assert.True(t, activos[0].AuthorizesGarrison())

	cityID, guarnecida, err := garrisons.CityOf(ctx, u.ID)
	require.NoError(t, err)
	require.True(t, guarnecida)
	assert.Equal(t, e.anfitrion.City.ID, cityID)

	// Y no resucita ningún movimiento.
	pendientes, err := movements.ListActive(ctx)
	require.NoError(t, err)
	assert.Empty(t, pendientes)
}

func TestLaGuarnicionDeUnaCiudadSeListaEnOrdenEstable(t *testing.T) {
	store, ctx := newTestStore(t)
	e := nuevoEscenario(t, ctx, store)

	for _, u := range e.visitante.Units {
		pegadaA(t, ctx, store, u, e.visitante.City)
		_, err := e.garrisons.Enter(ctx, u, e.visitante.City, e.visitante.Player.ID, 1)
		require.NoError(t, err)
	}

	ids, err := e.garrisons.ListByCity(ctx, e.visitante.City.ID)
	require.NoError(t, err)
	require.Len(t, ids, len(e.visitante.Units))
	for i := 1; i < len(ids); i++ {
		assert.Less(t, ids[i-1], ids[i], "orden ascendente por unit_id")
	}
}
