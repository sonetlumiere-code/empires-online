//go:build integration

package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/caravan"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/economy"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/market"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/movement"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/persistence/postgres"
)

// Persistencia del mercado (SPEC-MARKETS §7 y §9 «integration», ADR-047), contra
// PostgreSQL real. visitante vende piedra en lotes; anfitrion paga en madera.

type escenarioDeMercado struct {
	escenarioDiplomatico
	mercado  *postgres.MarketRepo
	economia *postgres.EconomyRepo
}

func nuevoEscenarioDeMercado(t *testing.T, ctx context.Context, store *postgres.Store) escenarioDeMercado {
	t.Helper()
	e := nuevoEscenario(t, ctx, store)
	return escenarioDeMercado{
		escenarioDiplomatico: e,
		mercado:              postgres.NewMarketRepo(store, 32),
		economia:             postgres.NewEconomyRepo(store, e.units, e.movements),
	}
}

// llenar deja 100 de piedra al vendedor y 100 de madera al comprador, con la
// versión 1.
func (e escenarioDeMercado) llenar(t *testing.T, ctx context.Context) {
	t.Helper()
	require.NoError(t, e.economia.FlushStocks(ctx, []postgres.StockDelta{
		{CityID: e.visitante.City.ID, Resource: economy.ResourceStone, Delta: 100, Version: 1},
		{CityID: e.anfitrion.City.ID, Resource: economy.ResourceWood, Delta: 100, Version: 1},
	}))
}

// ejemplo es la oferta de SPEC-MARKETS §4.2: 3 lotes de 20 de piedra por 30 de
// madera.
func (e escenarioDeMercado) ejemplo(t *testing.T, requestID string) market.Offer {
	t.Helper()
	ahora := time.UnixMilli(time.Now().UnixMilli()).UTC()
	o, err := market.New(e.visitante.Player.ID, e.visitante.City.ID,
		economy.Cost{Resource: economy.ResourceStone, Amount: 20}, economy.Cost{Resource: economy.ResourceWood, Amount: 30},
		3, requestID, ahora, 24*time.Hour)
	require.NoError(t, err)
	return o
}

// publicar escribe la oferta con el débito que haría la RAM: 66 de piedra, de la
// versión 1 a la 2.
func (e escenarioDeMercado) publicar(t *testing.T, ctx context.Context, o market.Offer) market.Offer {
	t.Helper()
	id, err := e.mercado.Post(ctx, postgres.MarketPost{Offer: o, AtMs: time.Now().UnixMilli(),
		Stock: postgres.StockDelta{CityID: o.CityID, Resource: economy.ResourceStone, Delta: -o.PostCost().Amount, Version: 2}})
	require.NoError(t, err)
	o.ID = id
	return o
}

// compra es la de `lots` lotes por anfitrion, con su débito de la versión dada y
// sus dos caravanas.
func (e escenarioDeMercado) compra(t *testing.T, o market.Offer, lots int32, version int64) postgres.MarketPurchase {
	t.Helper()
	p, err := o.CanBuy(e.anfitrion.Player.ID, lots, o.LotsLeft)
	require.NoError(t, err)
	halves := caravan.PurchaseHalves(o.Seller, o.CityID, e.anfitrion.Player.ID, e.anfitrion.City.ID, p.Receive, p.Pay)
	spawns := make([]postgres.CaravanSpawn, 0, 2)
	for i, h := range halves {
		origen := world.Tile{X: int32(5 + 30*i), Y: int32(5 + 30*i)}
		path := movement.TimedPath{{X: origen.X, Y: origen.Y, TMs: 0}, {X: origen.X + 1, Y: origen.Y, TMs: 1200}}
		m := movement.New(0, path, path.Destination(), time.Now().UnixMilli())
		m.ID = 2001 + int64(i) + int64(version)*10
		spawns = append(spawns, postgres.CaravanSpawn{
			Sender: h.Sender, FromCity: h.FromCity, ToCity: h.ToCity, Cargo: h.Cargo, Origin: origen, MaxHP: 80, Movement: *m,
		})
	}
	return postgres.MarketPurchase{
		Offer: o, Buyer: e.anfitrion.Player.ID, BuyerCity: e.anfitrion.City.ID, RequestID: uuid.NewString(),
		Purchase: p, Caravans: spawns, AtMs: time.Now().UnixMilli(),
		Stock: postgres.StockDelta{CityID: e.anfitrion.City.ID, Resource: economy.ResourceWood, Delta: -p.Debit().Amount, Version: version},
	}
}

// RN-MARKET-003, INV-SEC-007: publicar debita la custodia con su comisión e
// inserta la oferta con su evento; repetirlo no debita otra vez, y la carga la
// devuelve.
func TestPublicarEnElMercadoEsIdempotente(t *testing.T) {
	store, ctx := newTestStore(t)
	e := nuevoEscenarioDeMercado(t, ctx, store)
	e.llenar(t, ctx)
	o := e.ejemplo(t, uuid.NewString())

	primera := e.publicar(t, ctx, o)
	otra, err := e.mercado.Post(ctx, postgres.MarketPost{Offer: o,
		Stock: postgres.StockDelta{CityID: o.CityID, Resource: economy.ResourceStone, Delta: -66, Version: 3}})
	require.NoError(t, err)
	assert.Equal(t, primera.ID, otra, "un reintento devuelve la misma oferta")
	piedra, version := stockDe(t, ctx, store, o.CityID, economy.ResourceStone)
	assert.Equal(t, [2]int64{34, 2}, [2]int64{piedra, version}, "un solo débito: 60 de custodia y 6 de comisión")
	assert.Equal(t, 1, eventos(t, ctx, store, postgres.EventMarketOfferPosted))

	abiertas, err := e.mercado.ListOpen(ctx)
	require.NoError(t, err)
	require.Len(t, abiertas, 1)
	assert.Equal(t, int32(3), abiertas[0].LotsLeft)
	assert.Equal(t, int64(6), abiertas[0].SellerCommission)
	assert.NoError(t, abiertas[0].Validate())
}

// RN-MARKET-005, INV-TRADE-007, INV-TRADE-009: una compra descuenta los lotes,
// debita al comprador y crea dos caravanas con market_purchase_id en una
// transacción; repetirla devuelve lo que ya escribió; la que compra los últimos
// lotes agota la oferta, y otra más ya no cabe.
func TestUnaCompraDescuentaLosLotesYCreaSusCaravanasUnaVez(t *testing.T) {
	store, ctx := newTestStore(t)
	e := nuevoEscenarioDeMercado(t, ctx, store)
	e.llenar(t, ctx)
	o := e.publicar(t, ctx, e.ejemplo(t, uuid.NewString()))

	b := e.compra(t, o, 2, 2)
	id, primera, err := e.mercado.Buy(ctx, b)
	require.NoError(t, err)
	require.Len(t, primera, 2)
	otroID, otra, err := e.mercado.Buy(ctx, b)
	require.NoError(t, err, "un reintento")
	assert.Equal(t, id, otroID)
	assert.Equal(t, primera, otra)

	madera, _ := stockDe(t, ctx, store, e.anfitrion.City.ID, economy.ResourceWood)
	assert.Equal(t, int64(34), madera, "un solo débito: 60 y 6 de comisión")
	actual, err := e.mercado.Get(ctx, o.ID)
	require.NoError(t, err)
	assert.Equal(t, int32(1), actual.LotsLeft)
	assert.Equal(t, market.StatusOpen, actual.Status)
	assert.Equal(t, 2, contar(t, ctx, store,
		`SELECT count(*) FROM caravans WHERE market_purchase_id = $1 AND offer_id IS NULL AND status = 'IN_TRANSIT'`, id))
	var recurso string
	var cantidad int64
	require.NoError(t, store.Pool().QueryRow(ctx,
		`SELECT resource, amount FROM caravan_cargo WHERE caravan_id = $1`, primera[0].CaravanID).Scan(&recurso, &cantidad))
	assert.Equal(t, [2]any{"STONE", int64(40)}, [2]any{recurso, cantidad}, "la del vendedor lleva lo comprado")
	require.NoError(t, store.Pool().QueryRow(ctx,
		`SELECT resource, amount FROM caravan_cargo WHERE caravan_id = $1`, primera[1].CaravanID).Scan(&recurso, &cantidad))
	assert.Equal(t, [2]any{"WOOD", int64(60)}, [2]any{recurso, cantidad}, "la del comprador, sin la comisión")
	assert.Equal(t, 1, eventos(t, ctx, store, postgres.EventMarketPurchase))
	var comprador uuid.UUID
	require.NoError(t, store.Pool().QueryRow(ctx,
		`SELECT counterpart_player_id FROM world_events WHERE event_type = $1`, postgres.EventMarketPurchase).Scan(&comprador))
	assert.Equal(t, e.anfitrion.Player.ID, comprador)

	// Las caravanas de la compra se cargan al arrancar, y cuadran.
	caravanRepo := postgres.NewCaravanRepo(store, 32)
	enCamino, err := caravanRepo.ListInTransit(ctx)
	require.NoError(t, err)
	require.Len(t, enCamino, 2)
	assert.Equal(t, id, enCamino[0].PurchaseID)
	assert.Zero(t, enCamino[0].OfferID)
	malas, err := caravanRepo.Violations(ctx)
	require.NoError(t, err)
	assert.Empty(t, malas)

	// INV-TRADE-009: con un lote abierto, la base no vende dos, aunque la orden
	// crea que quedan.
	creida := actual
	creida.LotsLeft = 2
	_, _, err = e.mercado.Buy(ctx, e.compra(t, creida, 2, 3))
	assert.ErrorIs(t, err, postgres.ErrVersionConflict, "quedaba un lote")
	sigue, err := e.mercado.Get(ctx, o.ID)
	require.NoError(t, err)
	assert.Equal(t, int32(1), sigue.LotsLeft)

	// El último lote la agota; uno más ya no cabe.
	actual.Seller = o.Seller
	_, _, err = e.mercado.Buy(ctx, e.compra(t, actual, 1, 3))
	require.NoError(t, err)
	agotada, err := e.mercado.Get(ctx, o.ID)
	require.NoError(t, err)
	assert.Equal(t, market.StatusSoldOut, agotada.Status)
	assert.False(t, agotada.ClosedAt.IsZero())
	actual.LotsLeft = 1
	_, _, err = e.mercado.Buy(ctx, e.compra(t, actual, 1, 4))
	assert.ErrorIs(t, err, postgres.ErrVersionConflict, "INV-TRADE-009: la base no vende un lote dos veces")
	mercadoViolations, err := e.mercado.Violations(ctx)
	require.NoError(t, err)
	assert.Empty(t, mercadoViolations)
}

// INV-TRADE-008: si el débito del comprador no se aplica, no se descuenta ningún
// lote ni se crea ninguna caravana.
func Test_INV_TRADE_008_UnaCompraConElDeltaSuperadoNoMueveNada(t *testing.T) {
	store, ctx := newTestStore(t)
	e := nuevoEscenarioDeMercado(t, ctx, store)
	e.llenar(t, ctx)
	o := e.publicar(t, ctx, e.ejemplo(t, uuid.NewString()))

	_, _, err := e.mercado.Buy(ctx, e.compra(t, o, 2, 1)) // la versión 1 ya está escrita
	assert.ErrorIs(t, err, postgres.ErrStaleStock)
	actual, err := e.mercado.Get(ctx, o.ID)
	require.NoError(t, err)
	assert.Equal(t, int32(3), actual.LotsLeft)
	assert.Zero(t, contar(t, ctx, store, `SELECT count(*) FROM caravans`))
	assert.Zero(t, contar(t, ctx, store, `SELECT count(*) FROM market_purchases`))

	b := e.compra(t, o, 2, 2)
	b.Stock.Delta = -101 // más madera de la que hay
	_, _, err = e.mercado.Buy(ctx, b)
	assert.ErrorIs(t, err, economy.ErrInsufficientResources)
	actual, err = e.mercado.Get(ctx, o.ID)
	require.NoError(t, err)
	assert.Equal(t, int32(3), actual.LotsLeft)
}

// RN-MARKET-007, RN-MARKET-008: cerrar devuelve lo que queda en custodia una sola
// vez, con su evento; la caducidad va sin otro jugador.
func TestCerrarUnaOfertaDevuelveUnaVez(t *testing.T) {
	store, ctx := newTestStore(t)
	e := nuevoEscenarioDeMercado(t, ctx, store)
	e.llenar(t, ctx)
	o := e.publicar(t, ctx, e.ejemplo(t, uuid.NewString()))

	cerrada := o
	back, err := cerrada.Close(market.StatusExpired, time.UnixMilli(time.Now().UnixMilli()).UTC())
	require.NoError(t, err)
	c := postgres.MarketClose{Offer: cerrada, Returned: back, AtMs: time.Now().UnixMilli(),
		Stock: postgres.StockDelta{CityID: o.CityID, Resource: economy.ResourceStone, Delta: back.Amount, Version: 3}}
	require.NoError(t, e.mercado.Close(ctx, c))
	require.NoError(t, e.mercado.Close(ctx, c), "un reintento")
	piedra, _ := stockDe(t, ctx, store, o.CityID, economy.ResourceStone)
	assert.Equal(t, int64(94), piedra, "34 + 60 devueltos, una vez; la comisión no vuelve")
	assert.Equal(t, 1, eventos(t, ctx, store, postgres.EventMarketOfferExpired))
	var otro *uuid.UUID
	require.NoError(t, store.Pool().QueryRow(ctx,
		`SELECT counterpart_player_id FROM world_events WHERE event_type = $1`, postgres.EventMarketOfferExpired).Scan(&otro))
	assert.Nil(t, otro)
	actual, err := e.mercado.Get(ctx, o.ID)
	require.NoError(t, err)
	assert.Equal(t, market.StatusExpired, actual.Status)
	assert.Equal(t, int32(3), actual.LotsLeft, "los lotes devueltos")
	abiertas, err := e.mercado.ListOpen(ctx)
	require.NoError(t, err)
	assert.Empty(t, abiertas)
}

// SPEC-MARKETS Q11: el registro del vendedor cuenta la venta, con el comprador y
// sus lotes, y la caducidad, sin otro jugador; al comprador no se le cuenta nada.
func TestElRegistroDelMercado(t *testing.T) {
	store, ctx := newTestStore(t)
	e := nuevoEscenarioDeMercado(t, ctx, store)
	e.llenar(t, ctx)
	o := e.publicar(t, ctx, e.ejemplo(t, uuid.NewString()))
	_, _, err := e.mercado.Buy(ctx, e.compra(t, o, 2, 2))
	require.NoError(t, err)
	actual, err := e.mercado.Get(ctx, o.ID)
	require.NoError(t, err)
	back, err := actual.Close(market.StatusExpired, time.UnixMilli(time.Now().UnixMilli()).UTC())
	require.NoError(t, err)
	require.NoError(t, e.mercado.Close(ctx, postgres.MarketClose{Offer: actual, Returned: back, AtMs: time.Now().UnixMilli(),
		Stock: postgres.StockDelta{CityID: o.CityID, Resource: economy.ResourceStone, Delta: back.Amount, Version: 3}}))

	r := registro(store, 50)
	vendedor, _, err := r.Page(ctx, e.visitante.Player.ID, 0)
	require.NoError(t, err)
	require.Len(t, vendedor.Entries, 2)
	caducada, venta := vendedor.Entries[0], vendedor.Entries[1]
	assert.Equal(t, "MARKET_EXPIRED", caducada.Kind)
	assert.Nil(t, caducada.OtherPlayerID, "la caducidad no tiene otro jugador")
	assert.Nil(t, caducada.OtherCityID)
	require.NotNil(t, caducada.Lots)
	assert.Equal(t, int32(1), *caducada.Lots)
	require.Len(t, caducada.TradeGet, 1)
	assert.Equal(t, int64(20), caducada.TradeGet[0].Amount, "el lote que volvió")
	assert.Equal(t, "MARKET_SOLD", venta.Kind)
	require.NotNil(t, venta.OtherPlayerID)
	assert.Equal(t, e.anfitrion.Player.ID.String(), *venta.OtherPlayerID)
	require.NotNil(t, venta.Lots)
	assert.Equal(t, int32(2), *venta.Lots)
	assert.Equal(t, int64(40), venta.TradeGive[0].Amount)
	assert.Equal(t, int64(60), venta.TradeGet[0].Amount)

	comprador, _, err := r.Page(ctx, e.anfitrion.Player.ID, 0)
	require.NoError(t, err)
	assert.Empty(t, comprador.Entries, "al comprador no se le cuenta su compra")
}

// ADR-047 §6: el tablón sólo lleva las abiertas de otros jugadores, con su
// ciudad, y filtra por lo que se recibe y por lo que se paga.
func TestElTablonDelMercado(t *testing.T) {
	store, ctx := newTestStore(t)
	e := nuevoEscenarioDeMercado(t, ctx, store)
	e.llenar(t, ctx)
	o := e.publicar(t, ctx, e.ejemplo(t, uuid.NewString()))

	propio, err := e.mercado.Board(ctx, e.visitante.Player.ID, "", "")
	require.NoError(t, err)
	assert.Empty(t, propio, "la propia no sale en el tablón de su dueño")
	ajeno, err := e.mercado.Board(ctx, e.anfitrion.Player.ID, "", "")
	require.NoError(t, err)
	require.Len(t, ajeno, 1)
	v := ajeno[0]
	assert.Equal(t, o.ID, v.ID)
	assert.Equal(t, "STONE", v.Give.Resource)
	assert.Equal(t, int64(30), v.Want.Amount)
	assert.Equal(t, int32(3), v.LotsLeft)
	assert.Equal(t, "OPEN", v.Status)
	require.NotNil(t, v.CityName)
	require.NotNil(t, v.CityX)
	assert.Equal(t, e.visitante.City.CenterX, *v.CityX)
	require.NotNil(t, v.Civilization)

	for _, f := range []struct {
		receive, pay string
		n            int
	}{{"STONE", "", 1}, {"WOOD", "", 0}, {"", "WOOD", 1}, {"", "STONE", 0}, {"STONE", "WOOD", 1}} {
		lista, err := e.mercado.Board(ctx, e.anfitrion.Player.ID, f.receive, f.pay)
		require.NoError(t, err)
		assert.Len(t, lista, f.n, "recibir %q, pagar %q", f.receive, f.pay)
	}
}

// Los CHECK de market_offers, el trigger de INV-TRADE-010 y el de las caravanas.
func TestLasRestriccionesDelMercado(t *testing.T) {
	store, ctx := newTestStore(t)
	e := nuevoEscenarioDeMercado(t, ctx, store)
	e.llenar(t, ctx)
	o := e.publicar(t, ctx, e.ejemplo(t, uuid.NewString()))
	db := store.Pool()

	insertar := func(give, want string, lots, left int, comision int64, estado string) error {
		_, err := db.Exec(ctx,
			`INSERT INTO market_offers (player_id, city_id, give_resource, give_per_lot, want_resource, want_per_lot,
			                            lots_total, lots_left, seller_commission, status, request_id, created_at, expires_at, closed_at)
			 VALUES ($1, $2, $3, 20, $4, 30, $5, $6, $7, $8, $9, now(), now() + interval '1 day',
			         CASE WHEN $8 = 'OPEN' THEN NULL ELSE now() END)`,
			e.visitante.Player.ID, e.visitante.City.ID, give, want, lots, left, comision, estado, uuid.NewString())
		return err
	}
	assert.NoError(t, insertar("STONE", "WOOD", 1, 1, 2, "OPEN"))
	for nombre, err := range map[string]error{
		"el mismo recurso":   insertar("STONE", "STONE", 1, 1, 2, "OPEN"),
		"recurso inventado":  insertar("GOLD", "WOOD", 1, 1, 2, "OPEN"),
		"101 lotes":          insertar("STONE", "WOOD", 101, 1, 202, "OPEN"),
		"más que publicados": insertar("STONE", "WOOD", 1, 2, 2, "OPEN"),
		"abierta sin lotes":  insertar("STONE", "WOOD", 1, 0, 2, "OPEN"),
		"agotada con lotes":  insertar("STONE", "WOOD", 1, 1, 2, "SOLD_OUT"),
		"comisión falsa":     insertar("STONE", "WOOD", 1, 1, 1, "OPEN"),
		"estado inventado":   insertar("STONE", "WOOD", 1, 1, 2, "PAUSED"),
	} {
		assert.True(t, postgres.IsCheckViolation(err), "%s: %v", nombre, err)
	}

	// INV-TRADE-010: el dueño de la oferta no se la compra.
	_, err := db.Exec(ctx,
		`INSERT INTO market_purchases (offer_id, buyer_player_id, buyer_city_id, lots, paid_per_lot, buyer_commission, request_id)
		 VALUES ($1, $2, $3, 1, 30, 3, $4)`,
		o.ID, e.visitante.Player.ID, e.visitante.City.ID, uuid.NewString())
	assert.True(t, postgres.IsCheckViolation(err), "%v", err)

	// INV-TRADE-007: una caravana sin trato ni compra, o con los dos, no entra.
	for _, fuente := range []string{"NULL, NULL", "1, 1"} {
		_, err := db.Exec(ctx,
			`INSERT INTO caravans (offer_id, market_purchase_id, sender_player_id, from_city_id, to_city_id, original_to_city_id, departed_at)
			 VALUES (`+fuente+`, $1, $2, $3, $3, now())`,
			e.visitante.Player.ID, e.visitante.City.ID, e.anfitrion.City.ID)
		assert.Error(t, err, "fuente %s", fuente)
	}

	// Un mercado por ciudad en pie.
	_, err = db.Exec(ctx,
		`INSERT INTO buildings (city_id, type, x, y, status, completes_at_ms, request_id) VALUES ($1, 'MARKET', 11, 9, 'COMPLETE', 0, 'm1')`,
		e.visitante.City.ID)
	require.NoError(t, err)
	_, err = db.Exec(ctx,
		`INSERT INTO buildings (city_id, type, x, y, status, completes_at_ms, request_id) VALUES ($1, 'MARKET', 9, 11, 'COMPLETE', 0, 'm2')`,
		e.visitante.City.ID)
	assert.True(t, postgres.IsUniqueViolation(err), "RN-MARKET-001: uno en pie por ciudad: %v", err)
}
