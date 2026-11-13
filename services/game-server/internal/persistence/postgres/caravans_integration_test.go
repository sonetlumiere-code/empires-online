//go:build integration

package postgres_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/economy"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/movement"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/persistence/postgres"
)

// Persistencia de las caravanas (SPEC-CARAVANS §7 y §9 «integration», ADR-037),
// contra PostgreSQL real. Un trato de 300 de madera de visitante por 100 de
// piedra de anfitrion, aceptado: dos caravanas en camino.

type caravanasEnCamino struct {
	escenarioComercial
	repo    *postgres.CaravanRepo
	creadas []postgres.CaravanCreated
}

// tratoEnCamino acepta el trato del escenario comercial y devuelve sus dos
// caravanas: la de visitante (madera hacia anfitrion) y la de anfitrion
// (piedra hacia visitante).
func tratoEnCamino(t *testing.T, ctx context.Context, store *postgres.Store) caravanasEnCamino {
	t.Helper()
	e := nuevoEscenarioComercial(t, ctx, store)
	e.llenar(t, ctx)
	o := e.proponer(t, ctx, e.ofertaNueva(uuid.NewString()))
	creadas, err := e.trade.Accept(ctx, e.aceptacion(o, uuid.NewString()))
	require.NoError(t, err)
	require.Len(t, creadas, 2)
	return caravanasEnCamino{escenarioComercial: e, repo: postgres.NewCaravanRepo(store, 32), creadas: creadas}
}

// entregaDeMadera es la llegada de la caravana de visitante a la ciudad de
// anfitrion, con la versión que tendría la RAM.
func (c caravanasEnCamino) entregaDeMadera(version int64) postgres.CaravanDelivery {
	return postgres.CaravanDelivery{
		CaravanID: c.creadas[0].CaravanID, UnitID: c.creadas[0].UnitID, CityID: c.anfitrion.City.ID,
		Recipient: c.anfitrion.Player.ID, Counterpart: c.visitante.Player.ID,
		Cargo:  economy.Costs{{Resource: economy.ResourceWood, Amount: 300}},
		Stocks: []postgres.StockDelta{{CityID: c.anfitrion.City.ID, Resource: economy.ResourceWood, Delta: 300, Version: version}},
		At:     world.Tile{X: 38, Y: 38}, Tick: 7, AtMs: 1_000,
	}
}

// INV-TRADE-006 y RN-CARAVAN-004: la llegada acredita la carga, cierra la
// caravana, borra su unidad y escribe CaravanArrived, en una transacción; un
// reintento no acredita otra vez.
func Test_INV_TRADE_006_UnaCaravanaSeEntregaUnaSolaVez(t *testing.T) {
	store, ctx := newTestStore(t)
	c := tratoEnCamino(t, ctx, store)

	for vez := 0; vez < 2; vez++ {
		require.NoError(t, c.repo.Deliver(ctx, c.entregaDeMadera(int64(1+vez))), "vez %d", vez)
	}
	madera, _ := stockDe(t, ctx, store, c.anfitrion.City.ID, economy.ResourceWood)
	assert.Equal(t, int64(300), madera, "la segunda vez no acredita nada")
	assert.Equal(t, 1, eventos(t, ctx, store, "CaravanArrived"))
	assert.Equal(t, 1, contar(t, ctx, store,
		`SELECT count(*) FROM caravans WHERE id = $1 AND status = 'DELIVERED' AND unit_id IS NULL AND closed_at IS NOT NULL`,
		c.creadas[0].CaravanID))
	assert.Zero(t, contar(t, ctx, store, `SELECT count(*) FROM units WHERE id = $1`, c.creadas[0].UnitID),
		"la unidad se borra con su movimiento")
	assert.Zero(t, contar(t, ctx, store, `SELECT count(*) FROM unit_movements WHERE unit_id = $1`, c.creadas[0].UnitID))

	var jugador, otro uuid.UUID
	require.NoError(t, store.Pool().QueryRow(ctx,
		`SELECT player_id, counterpart_player_id FROM world_events WHERE event_type = 'CaravanArrived'`).Scan(&jugador, &otro))
	assert.Equal(t, [2]uuid.UUID{c.anfitrion.Player.ID, c.visitante.Player.ID}, [2]uuid.UUID{jugador, otro})

	// Sólo queda en camino la otra.
	enCamino, err := c.repo.ListInTransit(ctx)
	require.NoError(t, err)
	require.Len(t, enCamino, 1)
	assert.Equal(t, c.creadas[1].CaravanID, enCamino[0].ID)
	assert.Equal(t, economy.Costs{{Resource: economy.ResourceStone, Amount: 100}}, enCamino[0].Cargo)
	assert.Equal(t, c.visitante.City.ID, enCamino[0].ToCity)
	assert.Equal(t, c.anfitrion.Player.ID, enCamino[0].Sender)
}

// Una entrega cuyo delta la base ya superó no escribe nada: la simulación la
// reintenta con una versión nueva.
func TestUnaEntregaAdelantadaNoEscribeNada(t *testing.T) {
	store, ctx := newTestStore(t)
	c := tratoEnCamino(t, ctx, store)
	require.NoError(t, c.economia.FlushStocks(ctx, []postgres.StockDelta{
		{CityID: c.anfitrion.City.ID, Resource: economy.ResourceWood, Delta: 5, Version: 4},
	}))
	assert.ErrorIs(t, c.repo.Deliver(ctx, c.entregaDeMadera(4)), postgres.ErrStaleStock)
	assert.Equal(t, 2, contar(t, ctx, store, `SELECT count(*) FROM caravans WHERE status = 'IN_TRANSIT'`))
	assert.Zero(t, eventos(t, ctx, store, "CaravanArrived"))
}

// RN-CARAVAN-006 e INV-TRADE-005: un tercero captura la caravana de visitante,
// que iba a anfitrion. La unidad pasa al captor con la vida entera y un
// movimiento nuevo, la caravana cambia de destino y su carga no cambia; se
// escriben CaravanCaptured para visitante y CaravanIntercepted para anfitrion.
func TestUnaCapturaCambiaElDuenoYNoLaCarga(t *testing.T) {
	store, ctx := newTestStore(t)
	c := tratoEnCamino(t, ctx, store)
	_, _, _, _, bootstrapper := newRepos(store)
	tercero, err := bootstrapper.Create(ctx, bootstrapRequest("tercero", world.Tile{X: 70, Y: 10}))
	require.NoError(t, err)

	at := world.Tile{X: 20, Y: 20}
	path := movement.TimedPath{{X: at.X, Y: at.Y, TMs: 0}, {X: at.X + 1, Y: at.Y, TMs: 1200}}
	m := movement.New(c.creadas[0].UnitID, path, path.Destination(), 5_000)
	m.ID = 2001
	captura := postgres.CaravanCapture{
		CaravanID: c.creadas[0].CaravanID, UnitID: c.creadas[0].UnitID,
		PreviousOwner: c.visitante.Player.ID, Captor: tercero.Player.ID, CaptorCity: tercero.City.ID,
		OriginalRecipient: c.anfitrion.Player.ID, HP: 80, At: at, Movement: m,
		Cargo: economy.Costs{{Resource: economy.ResourceWood, Amount: 300}}, Tick: 9, AtMs: 5_000,
	}
	for vez := 0; vez < 2; vez++ {
		require.NoError(t, c.repo.Capture(ctx, captura), "vez %d", vez)
	}

	var dueno uuid.UUID
	var ciudad, x int64
	var hp int32
	var estado string
	require.NoError(t, store.Pool().QueryRow(ctx,
		`SELECT player_id, city_id, hp, x, status FROM units WHERE id = $1`, c.creadas[0].UnitID).
		Scan(&dueno, &ciudad, &hp, &x, &estado))
	assert.Equal(t, tercero.Player.ID, dueno)
	assert.Equal(t, tercero.City.ID, ciudad)
	assert.Equal(t, int32(80), hp)
	assert.Equal(t, int64(20), x)
	assert.Equal(t, "MOVING", estado)
	assert.Equal(t, 1, contar(t, ctx, store,
		`SELECT count(*) FROM unit_movements WHERE unit_id = $1 AND status = 'ACTIVE'`, c.creadas[0].UnitID),
		"INV-MOVE-001: el movimiento de la salida se cerró")
	assert.Equal(t, 1, contar(t, ctx, store,
		`SELECT count(*) FROM unit_movements WHERE id = 2001 AND status = 'ACTIVE'`))
	assert.Equal(t, 1, contar(t, ctx, store,
		`SELECT count(*) FROM caravans WHERE id = $1 AND to_city_id = $2 AND original_to_city_id = $3 AND status = 'IN_TRANSIT'`,
		c.creadas[0].CaravanID, tercero.City.ID, c.anfitrion.City.ID))
	assert.Equal(t, 1, contar(t, ctx, store,
		`SELECT count(*) FROM caravan_cargo WHERE caravan_id = $1 AND resource = 'WOOD' AND amount = 300`, c.creadas[0].CaravanID),
		"INV-TRADE-005: la carga no cambia")
	assert.Equal(t, 1, eventos(t, ctx, store, "CaravanCaptured"), "el reintento no escribe otra vez")
	assert.Equal(t, 1, eventos(t, ctx, store, "CaravanIntercepted"))

	var victima, autor uuid.UUID
	require.NoError(t, store.Pool().QueryRow(ctx,
		`SELECT player_id, counterpart_player_id FROM world_events WHERE event_type = 'CaravanIntercepted'`).Scan(&victima, &autor))
	assert.Equal(t, [2]uuid.UUID{c.anfitrion.Player.ID, tercero.Player.ID}, [2]uuid.UUID{victima, autor})

	// La recarga ve el destino nuevo.
	enCamino, err := c.repo.ListInTransit(ctx)
	require.NoError(t, err)
	require.Len(t, enCamino, 2)
	assert.Equal(t, tercero.City.ID, enCamino[0].ToCity)
	assert.Equal(t, c.anfitrion.City.ID, enCamino[0].OriginalToCity)
	assert.True(t, enCamino[0].Captured())
}

// RN-CARAVAN-006: capturada sin ruta, la caravana se pierde con su carga; si
// el destinatario es el captor, no hay intercepción que escribir.
func TestUnaCapturaSinRutaPierdeLaCaravana(t *testing.T) {
	store, ctx := newTestStore(t)
	c := tratoEnCamino(t, ctx, store)
	perdida := postgres.CaravanCapture{
		CaravanID: c.creadas[0].CaravanID, UnitID: c.creadas[0].UnitID,
		PreviousOwner: c.visitante.Player.ID, Captor: c.anfitrion.Player.ID, CaptorCity: c.anfitrion.City.ID,
		OriginalRecipient: c.anfitrion.Player.ID, At: world.Tile{X: 20, Y: 20},
		Cargo: economy.Costs{{Resource: economy.ResourceWood, Amount: 300}}, Tick: 9, AtMs: 5_000,
	}
	require.NoError(t, c.repo.Capture(ctx, perdida))
	require.NoError(t, c.repo.Capture(ctx, perdida), "un reintento")
	assert.Equal(t, 1, contar(t, ctx, store,
		`SELECT count(*) FROM caravans WHERE id = $1 AND status = 'LOST' AND unit_id IS NULL`, c.creadas[0].CaravanID))
	assert.Zero(t, contar(t, ctx, store, `SELECT count(*) FROM units WHERE id = $1`, c.creadas[0].UnitID))
	assert.Equal(t, 1, eventos(t, ctx, store, "CaravanLost"))
	assert.Zero(t, eventos(t, ctx, store, "CaravanIntercepted"))
	madera, _ := stockDe(t, ctx, store, c.anfitrion.City.ID, economy.ResourceWood)
	assert.Zero(t, madera, "la carga se perdió")
}

// Las restricciones: una caravana tiene unidad si y sólo si está en camino; dos
// por trato como mucho, una por ciudad de salida; la carga, positiva, de un
// recurso del catálogo y una vez cada uno.
func TestLasRestriccionesDeLasCaravanas(t *testing.T) {
	store, ctx := newTestStore(t)
	c := tratoEnCamino(t, ctx, store)
	id := c.creadas[0].CaravanID

	_, err := store.Pool().Exec(ctx, `UPDATE caravans SET unit_id = NULL WHERE id = $1`, id)
	assert.True(t, postgres.IsCheckViolation(err), "en camino sin unidad: %v", err)
	_, err = store.Pool().Exec(ctx, `UPDATE caravans SET status = 'DELIVERED', closed_at = now() WHERE id = $1`, id)
	assert.True(t, postgres.IsCheckViolation(err), "entregada con unidad: %v", err)
	_, err = store.Pool().Exec(ctx, `DELETE FROM units WHERE id = $1`, c.creadas[0].UnitID)
	assert.Error(t, err, "la unidad de una caravana en camino no se borra sola")

	_, err = store.Pool().Exec(ctx,
		`INSERT INTO caravans (unit_id, offer_id, sender_player_id, from_city_id, to_city_id, original_to_city_id, departed_at)
		 SELECT NULL, offer_id, sender_player_id, from_city_id, to_city_id, original_to_city_id, departed_at
		   FROM caravans WHERE id = $1`, id)
	assert.Error(t, err, "una tercera caravana del mismo sentido")

	for nombre, sql := range map[string]string{
		"cantidad cero":     `INSERT INTO caravan_cargo (caravan_id, resource, amount) VALUES ($1, 'STONE', 0)`,
		"recurso inventado": `INSERT INTO caravan_cargo (caravan_id, resource, amount) VALUES ($1, 'GOLD', 5)`,
		"recurso repetido":  `INSERT INTO caravan_cargo (caravan_id, resource, amount) VALUES ($1, 'WOOD', 5)`,
	} {
		_, err := store.Pool().Exec(ctx, sql, id)
		assert.Error(t, err, nombre)
	}
}

// INV-TRADE-005 e INV-TRADE-007 al arrancar: una caravana con una carga que no
// es la de su lado del trato, o sin su unidad CARAVAN viva, sale en las
// violaciones; las que cuadran, no.
func TestLasCaravanasQueNoCuadranSeSenalanAlArrancar(t *testing.T) {
	store, ctx := newTestStore(t)
	c := tratoEnCamino(t, ctx, store)
	malas, err := c.repo.Violations(ctx)
	require.NoError(t, err)
	assert.Empty(t, malas, "recién creadas, las dos cuadran")

	_, err = store.Pool().Exec(ctx, `UPDATE caravan_cargo SET amount = 301 WHERE caravan_id = $1`, c.creadas[0].CaravanID)
	require.NoError(t, err)
	_, err = store.Pool().Exec(ctx, `UPDATE units SET unit_type = 'WARRIOR' WHERE id = $1`, c.creadas[1].UnitID)
	require.NoError(t, err)
	malas, err = c.repo.Violations(ctx)
	require.NoError(t, err)
	require.Len(t, malas, 2)
	assert.Equal(t, [2]any{c.creadas[0].CaravanID, "INV-TRADE-005"}, [2]any{malas[0].CaravanID, malas[0].InvID})
	assert.Equal(t, [2]any{c.creadas[1].CaravanID, "INV-TRADE-007"}, [2]any{malas[1].CaravanID, malas[1].InvID})
}
