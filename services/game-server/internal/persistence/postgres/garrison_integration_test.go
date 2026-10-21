//go:build integration

package postgres_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/garrison"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/persistence/postgres"
)

// Guarnición sobre PostgreSQL real con los comandos de red ya abiertos
// (ADR-017): la cola puede repetir una transacción, y repetirla no puede
// escribir dos veces el mismo hecho.

func eventosDe(t *testing.T, ctx context.Context, store *postgres.Store, tipo string) int {
	t.Helper()
	var n int
	require.NoError(t, store.Pool().QueryRow(ctx,
		`SELECT count(*) FROM world_events WHERE event_type = $1`, tipo).Scan(&n))
	return n
}

// origenYDueno lee de la base las dos columnas que la guarnición no puede tocar.
func origenYDueno(t *testing.T, ctx context.Context, store *postgres.Store, unitID int64) (int64, uuid.UUID) {
	t.Helper()
	var cityID int64
	var owner uuid.UUID
	require.NoError(t, store.Pool().QueryRow(ctx,
		`SELECT city_id, player_id FROM units WHERE id = $1`, unitID).Scan(&cityID, &owner))
	return cityID, owner
}

// ADR-017 §2.2: un reintento de una entrada ya escrita —la cola no sabe si el
// COMMIT llegó— es un éxito sin un segundo evento. La misma unidad en otra
// ciudad es UNIT_GARRISONED.
func TestUnaEntradaRepetidaEsIdempotenteYNoDuplicaElEvento(t *testing.T) {
	store, ctx := newTestStore(t)
	e := nuevoEscenario(t, ctx, store)
	u := e.visitante.Units[0]
	pegadaA(t, ctx, store, u, e.visitante.City)
	enElTick := *u // lo que recibe cada intento: la unidad tal como estaba, IDLE

	for intento := 0; intento < 2; intento++ {
		copia := enElTick
		_, err := e.garrisons.Enter(ctx, &copia, e.visitante.City, e.visitante.Player.ID, 3)
		require.NoError(t, err, "intento %d", intento)
		assert.Equal(t, unit.StatusGarrisoned, copia.Status)
	}
	assert.Equal(t, 1, eventosDe(t, ctx, store, "UnitGarrisoned"))
	var conTratado bool
	require.NoError(t, store.Pool().QueryRow(ctx,
		`SELECT payload ? 'treatyId' FROM world_events WHERE event_type = 'UnitGarrisoned'`).Scan(&conTratado))
	assert.False(t, conTratado, "en ciudad propia no hay tratado que registrar")

	copia := enElTick
	_, err := e.garrisons.Enter(ctx, &copia, e.anfitrion.City, e.visitante.Player.ID, 4)
	require.ErrorIs(t, err, garrison.ErrAlreadyGarrisoned)
	cityID, dentro, err := e.garrisons.CityOf(ctx, u.ID)
	require.NoError(t, err)
	require.True(t, dentro)
	assert.Equal(t, e.visitante.City.ID, cityID, "la guarnición abierta no cambia de ciudad")
}

// ADR-017 §2.3: repetir una salida ya escrita no escribe un segundo evento.
func TestUnaSalidaRepetidaEsIdempotenteYNoDuplicaElEvento(t *testing.T) {
	store, ctx := newTestStore(t)
	e := nuevoEscenario(t, ctx, store)
	u := e.visitante.Units[0]
	pegadaA(t, ctx, store, u, e.visitante.City)
	_, err := e.garrisons.Enter(ctx, u, e.visitante.City, e.visitante.Player.ID, 1)
	require.NoError(t, err)

	salida, err := garrison.ReentryTile(testWorld(t), e.visitante.City, nil)
	require.NoError(t, err)
	for intento := 0; intento < 2; intento++ {
		copia := *u
		require.NoError(t, e.garrisons.Leave(ctx, &copia, salida.X, salida.Y, salida.X/32, salida.Y/32, 2),
			"intento %d", intento)
	}
	assert.Equal(t, 1, eventosDe(t, ctx, store, "UnitUngarrisoned"))
}

// INV-GARR-009 e INV-GARR-007: entrar en una ciudad ajena y salir no cambian
// ni la ciudad de origen ni el dueño de la unidad.
func TestGuarnecerYSalirNoCambianLaCiudadDeOrigenNiElDueno(t *testing.T) {
	store, ctx := newTestStore(t)
	e := nuevoEscenario(t, ctx, store)
	tratado := tratadoActivo(t, ctx, store, e.treaties, e.visitante.Player.ID, e.anfitrion.Player.ID, true)
	u := e.visitante.Units[0]
	pegadaA(t, ctx, store, u, e.anfitrion.City)
	origen, dueno := origenYDueno(t, ctx, store, u.ID)
	require.Equal(t, e.visitante.City.ID, origen)

	_, err := e.garrisons.Enter(ctx, u, e.anfitrion.City, e.visitante.Player.ID, 1)
	require.NoError(t, err)

	// INV-GARR-004: el evento dice qué tratado autorizó la entrada.
	var autorizante *int64
	require.NoError(t, store.Pool().QueryRow(ctx,
		`SELECT (payload->>'treatyId')::bigint FROM world_events WHERE event_type = 'UnitGarrisoned'`).Scan(&autorizante))
	require.NotNil(t, autorizante)
	assert.Equal(t, tratado.ID, *autorizante)
	enLaGuarnicion, duenoDentro := origenYDueno(t, ctx, store, u.ID)
	assert.Equal(t, origen, enLaGuarnicion, "units.city_id sigue siendo la de origen, no la anfitriona")
	assert.Equal(t, dueno, duenoDentro)
	anfitriona, _, err := e.garrisons.CityOf(ctx, u.ID)
	require.NoError(t, err)
	assert.Equal(t, e.anfitrion.City.ID, anfitriona, "la anfitriona está en garrisons")

	salida, err := garrison.ReentryTile(testWorld(t), e.anfitrion.City, nil)
	require.NoError(t, err)
	require.NoError(t, e.garrisons.Leave(ctx, u, salida.X, salida.Y, salida.X/32, salida.Y/32, 2))
	fuera, duenoFuera := origenYDueno(t, ctx, store, u.ID)
	assert.Equal(t, origen, fuera)
	assert.Equal(t, dueno, duenoFuera)

	// Y la población de las dos ciudades es la de siempre.
	var poblacionOrigen, poblacionAnfitriona int32
	require.NoError(t, store.Pool().QueryRow(ctx, `SELECT population FROM cities WHERE id = $1`,
		e.visitante.City.ID).Scan(&poblacionOrigen))
	require.NoError(t, store.Pool().QueryRow(ctx, `SELECT population FROM cities WHERE id = $1`,
		e.anfitrion.City.ID).Scan(&poblacionAnfitriona))
	assert.EqualValues(t, 3, poblacionOrigen)
	assert.EqualValues(t, 3, poblacionAnfitriona)
}

// INV-GARR-001: la clave primaria hace imposible una segunda guarnición abierta
// de la misma unidad, y borrar la ciudad anfitriona borra su guarnición.
func TestUnaUnidadNoPuedeTenerDosGuarnicionesAbiertas(t *testing.T) {
	store, ctx := newTestStore(t)
	e := nuevoEscenario(t, ctx, store)
	u := e.visitante.Units[0]
	db := store.Pool()

	_, err := db.Exec(ctx, `INSERT INTO garrisons (unit_id, city_id) VALUES ($1, $2)`, u.ID, e.anfitrion.City.ID)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `INSERT INTO garrisons (unit_id, city_id) VALUES ($1, $2)`, u.ID, e.visitante.City.ID)
	assert.Equal(t, "garrisons_pkey", constraintDe(t, err))

	_, err = db.Exec(ctx, `DELETE FROM cities WHERE id = $1`, e.anfitrion.City.ID)
	require.NoError(t, err)
	var filas int
	require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM garrisons`).Scan(&filas))
	assert.Zero(t, filas, "ON DELETE CASCADE")
}
