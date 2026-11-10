//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/building"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/production"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/persistence/postgres"
)

// Integración del asedio (SPEC-COMBAT-SIEGE §9 «integration», ADR-029): la
// destrucción de una casa en una transacción idempotente, el trigger de
// INV-CITY-002 y el recuento exento de la carga, contra PostgreSQL real.

type asedio struct {
	escenarioEconomico
	atacante *postgres.BootstrapResult
}

func nuevoAsedio(t *testing.T, ctx context.Context, store *postgres.Store) asedio {
	t.Helper()
	e := nuevaEconomia(t, ctx, store)
	_, _, _, _, bootstrapper := newRepos(store)
	atacante, err := bootstrapper.Create(ctx, bootstrapRequest("sitiadora", world.Tile{X: 60, Y: 20}))
	require.NoError(t, err)
	return asedio{escenarioEconomico: e, atacante: atacante}
}

// casaEn encarga una casa en (19, 19) y la completa si se pide. Devuelve su id.
func (a asedio) casaEn(t *testing.T, ctx context.Context, completa bool) int64 {
	t.Helper()
	a.depositar(t, ctx, 30, 1)
	id, err := a.economia.Build(ctx, "req-casa", a.jugador.Player.ID, casa(a.ciudad(), 19, 19), []postgres.StockDelta{debito(a.ciudad(), -30, 2)}, 1)
	require.NoError(t, err)
	if completa {
		require.NoError(t, a.economia.CompleteBuilding(ctx, id, a.ciudad(), a.jugador.Player.ID, 5, 2))
	}
	return id
}

func (a asedio) derribo(id int64) postgres.BuildingDestruction {
	return postgres.BuildingDestruction{
		BuildingID: id, CityID: a.ciudad(), VictimPlayer: a.jugador.Player.ID,
		AttackerPlayer: a.atacante.Player.ID, AttackerID: a.atacante.Units[0].ID,
		Type: building.TypeHouse, At: world.Tile{X: 19, Y: 19}, Bonus: 5, Tick: 9, AtMs: 1_757_376_030_000,
	}
}

func limiteYPoblacion(t *testing.T, ctx context.Context, store *postgres.Store, cityID int64) (int32, int32) {
	t.Helper()
	var limite, poblacion int32
	require.NoError(t, store.Pool().QueryRow(ctx,
		`SELECT population_limit, population FROM cities WHERE id = $1`, cityID).Scan(&limite, &poblacion))
	return limite, poblacion
}

func estadoDeEdificio(t *testing.T, ctx context.Context, store *postgres.Store, id int64) string {
	t.Helper()
	var s string
	require.NoError(t, store.Pool().QueryRow(ctx, `SELECT status FROM buildings WHERE id = $1`, id).Scan(&s))
	return s
}

// ADR-029 §4 e INV-ECON-004: derribar una casa completa la marca DESTROYED,
// resta su bono una sola vez y escribe BuildingDestroyed con el atacante como
// el otro jugador (ADR-028); un reintento no cambia nada.
func TestDerribarUnaCasaCompletaRestaSuBonoUnaVez(t *testing.T) {
	store, ctx := newTestStore(t)
	a := nuevoAsedio(t, ctx, store)
	id := a.casaEn(t, ctx, true)
	limite, _ := limiteYPoblacion(t, ctx, store, a.ciudad())
	require.EqualValues(t, 25, limite)

	require.NoError(t, a.economia.DestroyBuilding(ctx, a.derribo(id)))
	require.NoError(t, a.economia.DestroyBuilding(ctx, a.derribo(id)), "un reintento")

	assert.Equal(t, "DESTROYED", estadoDeEdificio(t, ctx, store, id))
	limite, _ = limiteYPoblacion(t, ctx, store, a.ciudad())
	assert.EqualValues(t, 20, limite, "el bono sale una vez")
	assert.Equal(t, 1, eventosDe(t, ctx, store, postgres.EventBuildingDestroyed))

	var victima, otro uuid.UUID
	var completa bool
	var atMs int64
	require.NoError(t, store.Pool().QueryRow(ctx,
		`SELECT player_id, counterpart_player_id, (payload->>'wasComplete')::boolean, (payload->>'atMs')::bigint
		   FROM world_events WHERE event_type = $1`, postgres.EventBuildingDestroyed).
		Scan(&victima, &otro, &completa, &atMs))
	assert.Equal(t, a.jugador.Player.ID, victima)
	assert.Equal(t, a.atacante.Player.ID, otro)
	assert.True(t, completa)
	assert.EqualValues(t, 1_757_376_030_000, atMs)

	edificios, err := a.economia.ListBuildings(ctx)
	require.NoError(t, err)
	assert.Empty(t, edificios, "una casa derribada no se carga")
}

// ADR-029 §4: el bono se resta según el estado de la base. Una casa derribada
// en obras no toca el límite, y su compleción, si llega después (un reintento
// de la cola), ya no la encuentra en obras y no suma.
func TestDerribarUnaCasaEnObraNoTocaElLimiteNiDejaQueSeComplete(t *testing.T) {
	store, ctx := newTestStore(t)
	a := nuevoAsedio(t, ctx, store)
	id := a.casaEn(t, ctx, false)

	require.NoError(t, a.economia.DestroyBuilding(ctx, a.derribo(id)))
	require.NoError(t, a.economia.CompleteBuilding(ctx, id, a.ciudad(), a.jugador.Player.ID, 5, 3))

	assert.Equal(t, "DESTROYED", estadoDeEdificio(t, ctx, store, id))
	limite, _ := limiteYPoblacion(t, ctx, store, a.ciudad())
	assert.EqualValues(t, 20, limite)
	assert.Equal(t, 0, eventosDe(t, ctx, store, postgres.EventBuildingCompleted))
}

// ADR-029 §6: el único por tile vale sólo para las casas en pie; en el tile de
// una derribada se vuelve a construir.
func TestSeReconstruyeEnElTileDeUnaCasaDerribada(t *testing.T) {
	store, ctx := newTestStore(t)
	a := nuevoAsedio(t, ctx, store)
	vieja := a.casaEn(t, ctx, true)
	require.NoError(t, a.economia.DestroyBuilding(ctx, a.derribo(vieja)))

	a.depositar(t, ctx, 30, 3)
	nueva, err := a.economia.Build(ctx, "req-otra", a.jugador.Player.ID, casa(a.ciudad(), 19, 19), []postgres.StockDelta{debito(a.ciudad(), -30, 4)}, 4)
	require.NoError(t, err)
	assert.NotEqual(t, vieja, nueva)

	a.depositar(t, ctx, 30, 5)
	_, err = a.economia.Build(ctx, "req-tercera", a.jugador.Player.ID, casa(a.ciudad(), 19, 19), []postgres.StockDelta{debito(a.ciudad(), -30, 6)}, 6)
	assert.True(t, errors.Is(err, building.ErrInvalidSite), "con una en pie, el tile sigue ocupado: %v", err)

	edificios, err := a.economia.ListBuildings(ctx)
	require.NoError(t, err)
	require.Len(t, edificios, 1)
	assert.Equal(t, nueva, edificios[0].ID)
}

// §9 «integration»: con un fallo inyectado al final de la transacción —el
// evento apunta a un jugador que no existe—, no queda nada a medias: ni la casa
// derribada sin evento ni el límite rebajado con la casa en pie.
func TestUnaDerribaQueFallaNoDejaNadaAMedias(t *testing.T) {
	store, ctx := newTestStore(t)
	a := nuevoAsedio(t, ctx, store)
	id := a.casaEn(t, ctx, true)

	d := a.derribo(id)
	d.VictimPlayer = uuid.New() // FK de world_events: falla
	require.Error(t, a.economia.DestroyBuilding(ctx, d))

	assert.Equal(t, "COMPLETE", estadoDeEdificio(t, ctx, store, id))
	limite, _ := limiteYPoblacion(t, ctx, store, a.ciudad())
	assert.EqualValues(t, 25, limite)
	assert.Equal(t, 0, eventosDe(t, ctx, store, postgres.EventBuildingDestroyed))
}

// Una casa que no existe no se derriba: es ErrNotFound y no escribe nada.
func TestDerribarUnaCasaQueNoExisteEsNotFound(t *testing.T) {
	store, ctx := newTestStore(t)
	a := nuevoAsedio(t, ctx, store)
	err := a.economia.DestroyBuilding(ctx, a.derribo(999_999))
	assert.True(t, errors.Is(err, postgres.ErrNotFound), "%v", err)
	assert.Equal(t, 0, eventosDe(t, ctx, store, postgres.EventBuildingDestroyed))
}

// INV-CITY-002 (ADR-029 §2): el trigger deja bajar el límite por debajo de la
// población —una ciudad que pierde casas—, deja bajar la población, y rechaza
// subirla mientras siga por encima, con el nombre de restricción de antes.
// Entrenar en esa ciudad es ErrPopulationLimit sin debitar.
func TestElTriggerDejaBajarElLimiteYNoSubirLaPoblacionPorEncima(t *testing.T) {
	store, ctx := newTestStore(t)
	e := nuevaEconomia(t, ctx, store)
	db := store.Pool()

	_, err := db.Exec(ctx, `UPDATE cities SET population_limit = 2 WHERE id = $1`, e.ciudad())
	require.NoError(t, err, "bajar el límite por debajo de la población (3) pasa")

	_, err = db.Exec(ctx, `UPDATE cities SET population = population + 1 WHERE id = $1`, e.ciudad())
	assert.Equal(t, "cities_population_within_limit", constraintDe(t, err), "subir por encima no")

	_, err = db.Exec(ctx, `UPDATE cities SET population = population - 1 WHERE id = $1`, e.ciudad())
	require.NoError(t, err, "bajar la población pasa")
	_, err = db.Exec(ctx, `UPDATE cities SET population = population + 1 WHERE id = $1`, e.ciudad())
	assert.Equal(t, "cities_population_within_limit", constraintDe(t, err), "de 2 a 3 con límite 2, tampoco")

	_, err = db.Exec(ctx, `UPDATE cities SET name = 'otra' WHERE id = $1`, e.ciudad())
	require.NoError(t, err, "una ciudad por encima de su límite se puede escribir sin tocar la población")

	e.depositar(t, ctx, 100, 1)
	p := production.Production{CityID: e.ciudad(), UnitType: unit.TypeVillager, CompletesAtMs: 1}
	err = e.economia.Train(ctx, "req-1", e.jugador.Player.ID, p, 1, debito(e.ciudad(), -50, 2), 1)
	require.True(t, errors.Is(err, city.ErrPopulationLimit), "%v", err)
	amount, _ := stockEnBase(t, ctx, store, e.ciudad())
	assert.Equal(t, int64(100), amount)
}

// ADR-029 §3: sólo el recuento de la carga, que lo declara, puede subir la
// población por encima del límite; y sólo dentro de su transacción.
func TestSoloElRecuentoDeclaradoSubeLaPoblacionPorEncima(t *testing.T) {
	store, ctx := newTestStore(t)
	e := nuevaEconomia(t, ctx, store)
	db := store.Pool()
	_, err := db.Exec(ctx, `UPDATE cities SET population_limit = 2 WHERE id = $1`, e.ciudad())
	require.NoError(t, err)

	require.NoError(t, store.InTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SET LOCAL eo.population_recount = 'on'`); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE cities SET population = 5 WHERE id = $1`, e.ciudad())
		return err
	}))
	_, poblacion := limiteYPoblacion(t, ctx, store, e.ciudad())
	assert.EqualValues(t, 5, poblacion)

	_, err = db.Exec(ctx, `UPDATE cities SET population = 6 WHERE id = $1`, e.ciudad())
	assert.Equal(t, "cities_population_within_limit", constraintDe(t, err), "fuera de esa transacción, no")
}

// ADR-029 §3 con la carga real: una ciudad con más unidades vivas que el
// límite de su era y la población mal escrita se recuenta por encima del
// límite, sin subir el límite ni perder unidades; el arranque siguiente no
// encuentra nada.
func TestLaCargaRecuentaPorEncimaDelLimiteSinSubirlo(t *testing.T) {
	store, ctx := newTestStore(t)
	_, _, _, _, bootstrapper := newRepos(store)
	alta, err := bootstrapper.Create(ctx, bootstrapRequest("superpoblada", world.Tile{X: 20, Y: 20}))
	require.NoError(t, err)
	ciudad := alta.City.ID
	db := store.Pool()
	_, err = db.Exec(ctx,
		`INSERT INTO units (player_id, city_id, unit_type, x, y, hp, max_hp, status, chunk_x, chunk_y)
		 SELECT $1, $2, 'VILLAGER', 22 + g % 10, 25 + g / 10, 40, 40, 'IDLE', 0, 0 FROM generate_series(0, 19) g`,
		alta.Player.ID, ciudad)
	require.NoError(t, err)

	res, err := cargar(t, ctx, store, 81)
	require.NoError(t, err)
	var hallazgos []string
	for _, f := range res.Findings {
		hallazgos = append(hallazgos, f.InvID)
	}
	assert.Equal(t, []string{"INV-CITY-017"}, hallazgos, "ni INV-CITY-002 ni INV-CITY-003")

	limite, poblacion := limiteYPoblacion(t, ctx, store, ciudad)
	assert.EqualValues(t, 20, limite, "el population_cap de STONE_AGE, no el recuento")
	assert.EqualValues(t, 23, poblacion, "las 23 unidades vivas")

	res, err = cargar(t, ctx, store, 82)
	require.NoError(t, err)
	assert.Empty(t, res.Findings)
}
