//go:build integration

package postgres_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/territory"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/persistence/postgres"
)

// seedTerritories siembra la rejilla del mundo de pruebas (64 × 64, lado 32 ⇒
// cuatro territorios) y devuelve el repositorio y lo sembrado.
func seedTerritories(t *testing.T, ctx context.Context, store *postgres.Store) (*postgres.TerritoryRepo, []territory.Territory) {
	t.Helper()

	repo := postgres.NewTerritoryRepo(store)
	seeds, err := territory.SeedGrid(64, 64, 32)
	require.NoError(t, err)
	require.NoError(t, repo.SeedInTx(ctx, seeds))

	sembrados, err := repo.LoadAll(ctx)
	require.NoError(t, err)
	require.Len(t, sembrados, 4)
	return repo, sembrados
}

func TestLaSiembraCreaUnaFilaDeControlPorTerritorio(t *testing.T) {
	store, ctx := newTestStore(t)
	repo, sembrados := seedTerritories(t, ctx, store)

	controls, err := repo.LoadControls(ctx)
	require.NoError(t, err)

	// INV-TERR-003: una fila de control por territorio, ni más ni menos. Lo
	// garantiza la PRIMARY KEY, pero la siembra podría no crearla.
	require.Len(t, controls, len(sembrados))

	for _, c := range controls {
		// RN-TERR-006: el estado inicial lo fijan los DEFAULT del DDL.
		assert.Equal(t, territory.OwnerNone, c.OwnerType)
		assert.Nil(t, c.OwnerID)
		assert.Nil(t, c.CapturedAt)
		assert.EqualValues(t, 0, c.ControlPoints)
		assert.False(t, c.Contested)
		assert.EqualValues(t, 0, c.Version)
		assert.NoError(t, c.Validate())
	}
}

func TestElOwnershipSobreviveAUnReinicio(t *testing.T) {
	store, ctx := newTestStore(t)
	repo, sembrados := seedTerritories(t, ctx, store)

	objetivo := sembrados[0]
	jugador := uuid.New()
	capturado := time.Now().UTC().Truncate(time.Millisecond)

	control, err := repo.ClaimForPlayer(ctx, store.Pool(), objetivo.ID, jugador, capturado, 0)
	require.NoError(t, err)
	require.Equal(t, territory.OwnerPlayer, control.OwnerType)

	// "Reiniciar" es exactamente esto: tirar todo lo que hay en memoria y volver
	// a leer de la base, que es lo único que sobrevive a un proceso muerto.
	controls, err := repo.LoadControls(ctx)
	require.NoError(t, err)

	var tras territory.Control
	for _, c := range controls {
		if c.TerritoryID == objetivo.ID {
			tras = c
		}
	}
	require.EqualValues(t, objetivo.ID, tras.TerritoryID)
	assert.Equal(t, territory.OwnerPlayer, tras.OwnerType)
	require.NotNil(t, tras.OwnerID)
	assert.Equal(t, jugador.String(), *tras.OwnerID)
	require.NotNil(t, tras.CapturedAt, "INV-TERR-007: un dueño exige captured_at")
	assert.EqualValues(t, 1, tras.Version, "la reclamación incrementa la versión")
	assert.NoError(t, tras.Validate())

	// Y el índice reconstruido desde esa geometría sigue resolviendo igual.
	geometria, err := repo.LoadAll(ctx)
	require.NoError(t, err)
	set, solapes, err := territory.BuildSet(geometria, 64, 64, 32)
	require.NoError(t, err)
	require.Empty(t, solapes)
	resuelto, ok := set.TerritoryAt(objetivo.MinX, objetivo.MinY)
	require.True(t, ok)
	assert.Equal(t, objetivo.ID, resuelto.ID)
}

func TestUnaReclamacionConVersionDesactualizadaSeRechaza(t *testing.T) {
	// Criterio de aceptación 4 de M6, literal.
	store, ctx := newTestStore(t)
	repo, sembrados := seedTerritories(t, ctx, store)

	objetivo := sembrados[1]
	ahora := time.Now().UTC()

	primero, err := repo.ClaimForPlayer(ctx, store.Pool(), objetivo.ID, uuid.New(), ahora, 0)
	require.NoError(t, err)
	require.EqualValues(t, 1, primero.Version)

	// Segundo intento con la versión que ya no es. Falla por DUEÑO, no por
	// versión, porque el WHERE exige ambas cosas y el dueño se comprueba antes
	// de que la versión importe: el mensaje debe decir la razón real.
	_, err = repo.ClaimForPlayer(ctx, store.Pool(), objetivo.ID, uuid.New(), ahora, 0)
	require.Error(t, err)
	assert.ErrorIs(t, err, postgres.ErrAlreadyOwned)

	// Y el dueño no cambió: RN-TERR-008, no existe la pérdida de control.
	actual, err := repo.GetControl(ctx, store.Pool(), objetivo.ID)
	require.NoError(t, err)
	assert.Equal(t, *primero.OwnerID, *actual.OwnerID)
	assert.EqualValues(t, 1, actual.Version)
}

func TestUnaVersionDesactualizadaSobreUnTerritorioLibreSeRechaza(t *testing.T) {
	// El caso puro de concurrencia optimista, sin que el dueño lo enmascare:
	// el territorio sigue libre pero la versión que traemos es vieja.
	store, ctx := newTestStore(t)
	repo, sembrados := seedTerritories(t, ctx, store)

	objetivo := sembrados[2]

	// Alguien toca la fila sin reclamarla: la versión avanza.
	_, err := store.Pool().Exec(ctx,
		`UPDATE territory_control SET version = version + 1 WHERE territory_id = $1`, objetivo.ID)
	require.NoError(t, err)

	_, err = repo.ClaimForPlayer(ctx, store.Pool(), objetivo.ID, uuid.New(), time.Now().UTC(), 0)
	require.Error(t, err)
	assert.ErrorIs(t, err, postgres.ErrVersionConflict)

	// Nada se escribió a medias.
	actual, err := repo.GetControl(ctx, store.Pool(), objetivo.ID)
	require.NoError(t, err)
	assert.Equal(t, territory.OwnerNone, actual.OwnerType)
	assert.Nil(t, actual.OwnerID)
}

func TestDosReclamacionesConcurrentesSoloUnaGana(t *testing.T) {
	// Lo que pide M6: dos cambios simultáneos sobre el mismo territorio; uno gana
	// y el otro falla de forma explícita. Lo que NO puede pasar es que ganen los
	// dos, o que uno sobrescriba al otro en silencio.
	store, ctx := newTestStore(t)
	repo, sembrados := seedTerritories(t, ctx, store)

	objetivo := sembrados[3]
	ahora := time.Now().UTC()

	const intentos = 8
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		ganados  []uuid.UUID
		fallidos int
	)

	// Todas leen la versión 0 y todas intentan escribir sobre ella. Es
	// exactamente la carrera que la columna `version` existe para resolver.
	for i := 0; i < intentos; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			jugador := uuid.New()
			_, err := repo.ClaimForPlayer(ctx, store.Pool(), objetivo.ID, jugador, ahora, 0)

			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				ganados = append(ganados, jugador)
				return
			}
			fallidos++
		}()
	}
	wg.Wait()

	require.Len(t, ganados, 1, "exactamente una reclamación debe prosperar")
	assert.Equal(t, intentos-1, fallidos, "el resto debe fallar, no quedarse callado")

	final, err := repo.GetControl(ctx, store.Pool(), objetivo.ID)
	require.NoError(t, err)
	require.NotNil(t, final.OwnerID)
	assert.Equal(t, ganados[0].String(), *final.OwnerID, "el dueño es el que ganó, no el último en escribir")
	assert.EqualValues(t, 1, final.Version, "una sola escritura efectiva ⇒ una sola subida de versión")
}

func TestFundarReclamaElTerritorioDelCentroYRegistraElEvento(t *testing.T) {
	store, ctx := newTestStore(t)
	repo, sembrados := seedTerritories(t, ctx, store)
	_, _, _, _, bootstrapper := newRepos(store)

	// El centro cae dentro del primer territorio de la rejilla, (0,0)-(31,31).
	centro := world.Tile{X: 10, Y: 10}
	esperado := sembrados[0]
	require.True(t, esperado.Contains(centro.X, centro.Y), "el test asume que el centro cae en el primer territorio")

	req := bootstrapRequest("fundador", centro)
	req.TerritoryID = esperado.ID
	req.Tick = 4242

	result, err := bootstrapper.Create(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, result.TerritoryControl, "fundar en territorio libre debe cambiar el dueño")
	assert.Equal(t, territory.OwnerPlayer, result.TerritoryControl.OwnerType)

	control, err := repo.GetControl(ctx, store.Pool(), esperado.ID)
	require.NoError(t, err)
	require.NotNil(t, control.OwnerID)
	assert.Equal(t, result.Player.ID.String(), *control.OwnerID)

	// INV-TERR-009 por el lado durable: el evento de dominio existe y va en la
	// misma transacción, con el tick que le pasó el llamante.
	var tick int64
	var ownerType string
	err = store.Pool().QueryRow(ctx,
		`SELECT tick, payload->>'ownerType' FROM world_events
		  WHERE event_type = $1 AND player_id = $2`,
		territory.EventControlChanged, result.Player.ID,
	).Scan(&tick, &ownerType)
	require.NoError(t, err, "debe existir exactamente un TerritoryControlChanged de este jugador")
	assert.EqualValues(t, 4242, tick)
	assert.Equal(t, "PLAYER", ownerType)
}

func TestFundarEnTerritorioAjenoNoLoCambiaDeManosNiFalla(t *testing.T) {
	// RN-TERR-008: no existe la pérdida de control. Fundar dentro del territorio
	// de otro es VÁLIDO y no altera el ownership; tratarlo como error abortaría
	// el alta entera por una regla que dice lo contrario.
	store, ctx := newTestStore(t)
	repo, sembrados := seedTerritories(t, ctx, store)
	_, _, _, _, bootstrapper := newRepos(store)

	objetivo := sembrados[0]
	primerDueno := uuid.New()
	_, err := repo.ClaimForPlayer(ctx, store.Pool(), objetivo.ID, primerDueno, time.Now().UTC(), 0)
	require.NoError(t, err)

	req := bootstrapRequest("recienllegado", world.Tile{X: 10, Y: 10})
	req.TerritoryID = objetivo.ID

	result, err := bootstrapper.Create(ctx, req)
	require.NoError(t, err, "el alta debe completarse aunque el territorio tenga dueño")
	assert.Nil(t, result.TerritoryControl, "no hubo cambio de control que difundir")
	require.NotNil(t, result.City, "y la ciudad sí se funda")

	actual, err := repo.GetControl(ctx, store.Pool(), objetivo.ID)
	require.NoError(t, err)
	require.NotNil(t, actual.OwnerID)
	assert.Equal(t, primerDueno.String(), *actual.OwnerID, "el dueño original conserva el territorio")
	assert.EqualValues(t, 1, actual.Version, "y la versión no se movió")
}

func TestFundarFueraDeTodoTerritorioNoRompeNada(t *testing.T) {
	// TerritoryID = 0 significa "el centro no pertenece a ningún territorio".
	// Es un estado legítimo (RN-TERR-002, tierra de nadie), no un error.
	store, ctx := newTestStore(t)
	seedTerritories(t, ctx, store)
	_, _, _, _, bootstrapper := newRepos(store)

	req := bootstrapRequest("sinterritorio", world.Tile{X: 10, Y: 10})
	req.TerritoryID = 0

	result, err := bootstrapper.Create(ctx, req)
	require.NoError(t, err)
	assert.Nil(t, result.TerritoryControl)

	var n int64
	require.NoError(t, store.Pool().QueryRow(ctx,
		`SELECT count(*) FROM territory_control WHERE owner_type <> 'NONE'`).Scan(&n))
	assert.Zero(t, n, "ningún territorio debe haber cambiado de manos")
}

func TestElIndiceReconstruidoDesdeLaBaseCubreElMundoEntero(t *testing.T) {
	// INV-TERR-008 contra datos REALES: el índice es función pura de lo que hay
	// en la tabla, y la rejilla sembrada no deja tiles huérfanos.
	store, ctx := newTestStore(t)
	repo, _ := seedTerritories(t, ctx, store)

	geometria, err := repo.LoadAll(ctx)
	require.NoError(t, err)

	uno, solapes, err := territory.BuildSet(geometria, 64, 64, 32)
	require.NoError(t, err)
	require.Empty(t, solapes, "INV-TERR-002 sobre los datos cargados")

	for y := int32(0); y < 64; y++ {
		for x := int32(0); x < 64; x++ {
			if _, ok := uno.TerritoryAt(x, y); !ok {
				t.Fatalf("el tile (%d,%d) quedó sin territorio", x, y)
			}
		}
	}

	otro, _, err := territory.BuildSet(geometria, 64, 64, 32)
	require.NoError(t, err)
	assert.Equal(t, uno.All(), otro.All(), "reconstruirlo da lo mismo")
}
