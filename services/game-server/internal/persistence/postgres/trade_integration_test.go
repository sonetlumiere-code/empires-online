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
	"github.com/empires-online/empires-online/services/game-server/internal/domain/movement"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/trade"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/persistence/postgres"
)

// Persistencia del comercio (SPEC-TRADE §7 y §9 «integration», ADR-032), contra
// PostgreSQL real. visitante ofrece madera; anfitrion paga en piedra.

type escenarioComercial struct {
	escenarioDiplomatico
	trade    *postgres.TradeRepo
	economia *postgres.EconomyRepo
}

func nuevoEscenarioComercial(t *testing.T, ctx context.Context, store *postgres.Store) escenarioComercial {
	t.Helper()
	e := nuevoEscenario(t, ctx, store)
	return escenarioComercial{
		escenarioDiplomatico: e,
		trade:                postgres.NewTradeRepo(store, 32),
		economia:             postgres.NewEconomyRepo(store, e.units, e.movements),
	}
}

// llenar deja madera y piedra en las dos ciudades, con la versión 1.
func (e escenarioComercial) llenar(t *testing.T, ctx context.Context) {
	t.Helper()
	require.NoError(t, e.economia.FlushStocks(ctx, []postgres.StockDelta{
		{CityID: e.visitante.City.ID, Resource: economy.ResourceWood, Delta: 300, Version: 1},
		{CityID: e.anfitrion.City.ID, Resource: economy.ResourceStone, Delta: 100, Version: 1},
	}))
}

func (e escenarioComercial) ofertaNueva(requestID string) trade.Offer {
	ahora := time.UnixMilli(time.Now().UnixMilli()).UTC()
	return trade.Offer{
		Proposer: e.visitante.Player.ID, Target: e.anfitrion.Player.ID,
		ProposerCity: e.visitante.City.ID, TargetCity: e.anfitrion.City.ID,
		Give:   economy.Costs{{Resource: economy.ResourceWood, Amount: 300}},
		Want:   economy.Costs{{Resource: economy.ResourceStone, Amount: 100}},
		Status: trade.StatusProposed, RequestID: requestID, CreatedAt: ahora, ExpiresAt: ahora.Add(24 * time.Hour),
	}
}

func (e escenarioComercial) proponer(t *testing.T, ctx context.Context, o trade.Offer) trade.Offer {
	t.Helper()
	id, err := e.trade.ApplyOffer(ctx, postgres.TradeWrite{Insert: true, Offer: o, Event: postgres.EventTradeProposed,
		AtMs: o.CreatedAt.UnixMilli(), By: o.Proposer})
	require.NoError(t, err)
	o.ID = id
	return o
}

// aceptacion es el trato de la oferta con las versiones que tendría la RAM:
// sólo los débitos —la madera de visitante y la piedra de anfitrion pasan de 1
// a 2— y las dos caravanas que se llevan lo debitado (ADR-037 §3).
func (e escenarioComercial) aceptacion(o trade.Offer, requestID string) postgres.TradeAcceptance {
	cerrada, _ := o.TransitionTo(trade.StatusAccepted, time.UnixMilli(time.Now().UnixMilli()).UTC())
	cerrada.AcceptedRequestID = requestID
	stocks := make([]postgres.StockDelta, 0, 2)
	for _, l := range o.Legs() {
		if l.Pays > 0 {
			stocks = append(stocks, postgres.StockDelta{CityID: l.CityID, Resource: l.Resource, Delta: -l.Pays, Version: 2})
		}
	}
	return postgres.TradeAcceptance{
		Offer: cerrada, AtMs: time.Now().UnixMilli(), By: o.Target,
		Stocks: stocks, Caravans: caravanasDe(o, 1001),
	}
}

// caravanasDe son las dos caravanas de un trato, con movimientos numerados
// desde primerMovimiento: una ruta corta, que la base guarda tal cual.
func caravanasDe(o trade.Offer, primerMovimiento int64) []postgres.CaravanSpawn {
	out := make([]postgres.CaravanSpawn, 0, 2)
	for i, h := range caravan.Halves(o) {
		origen := world.Tile{X: int32(5 + 30*i), Y: int32(5 + 30*i)}
		path := movement.TimedPath{{X: origen.X, Y: origen.Y, TMs: 0}, {X: origen.X + 1, Y: origen.Y, TMs: 1200}}
		m := movement.New(0, path, path.Destination(), time.Now().UnixMilli())
		m.ID = primerMovimiento + int64(i)
		out = append(out, postgres.CaravanSpawn{
			Sender: h.Sender, FromCity: h.FromCity, ToCity: h.ToCity, Cargo: h.Cargo,
			Origin: origen, MaxHP: 80, Movement: *m,
		})
	}
	return out
}

// contar cuenta las filas de una consulta.
func contar(t *testing.T, ctx context.Context, store *postgres.Store, sql string, args ...any) int {
	t.Helper()
	var n int
	require.NoError(t, store.Pool().QueryRow(ctx, sql, args...).Scan(&n))
	return n
}

func eventos(t *testing.T, ctx context.Context, store *postgres.Store, tipo string) int {
	t.Helper()
	var n int
	require.NoError(t, store.Pool().QueryRow(ctx, `SELECT count(*) FROM world_events WHERE event_type = $1`, tipo).Scan(&n))
	return n
}

// ADR-032 §1: una oferta se inserta con sus líneas y su evento; repetirla con el
// mismo request_id devuelve la misma fila, y la carga la devuelve entera.
func TestUnaOfertaDeComercioEsIdempotenteYSeCargaConSusLineas(t *testing.T) {
	store, ctx := newTestStore(t)
	e := nuevoEscenarioComercial(t, ctx, store)
	o := e.ofertaNueva(uuid.NewString())

	primera := e.proponer(t, ctx, o)
	otra := e.proponer(t, ctx, o)
	assert.Equal(t, primera.ID, otra.ID)
	assert.Equal(t, 1, eventos(t, ctx, store, postgres.EventTradeProposed))

	pendientes, err := e.trade.ListPending(ctx)
	require.NoError(t, err)
	require.Len(t, pendientes, 1)
	p := pendientes[0]
	assert.Equal(t, o.Give, p.Give)
	assert.Equal(t, o.Want, p.Want)
	assert.Equal(t, o.Proposer, p.Proposer)
	assert.Equal(t, o.TargetCity, p.TargetCity)
	assert.True(t, o.ExpiresAt.Equal(p.ExpiresAt))

	// El evento nombra a los dos jugadores (ADR-028 §2).
	var jugador, otroJugador uuid.UUID
	require.NoError(t, store.Pool().QueryRow(ctx,
		`SELECT player_id, counterpart_player_id FROM world_events WHERE event_type = $1`, postgres.EventTradeProposed).
		Scan(&jugador, &otroJugador))
	assert.Equal(t, o.Proposer, jugador)
	assert.Equal(t, o.Target, otroJugador)
}

// INV-TRADE-003: como mucho una oferta pendiente por par, en las dos direcciones;
// cerrada la primera, cabe otra.
func Test_INV_TRADE_003_UnaOfertaPendientePorPar(t *testing.T) {
	store, ctx := newTestStore(t)
	e := nuevoEscenarioComercial(t, ctx, store)
	o := e.proponer(t, ctx, e.ofertaNueva(uuid.NewString()))

	inversa := e.ofertaNueva(uuid.NewString())
	inversa.Proposer, inversa.Target = inversa.Target, inversa.Proposer
	inversa.ProposerCity, inversa.TargetCity = inversa.TargetCity, inversa.ProposerCity
	for _, otra := range []trade.Offer{e.ofertaNueva(uuid.NewString()), inversa} {
		_, err := e.trade.ApplyOffer(ctx, postgres.TradeWrite{Insert: true, Offer: otra, Event: postgres.EventTradeProposed})
		assert.ErrorIs(t, err, postgres.ErrConflict)
	}

	retirada, err := o.TransitionTo(trade.StatusWithdrawn, time.Now())
	require.NoError(t, err)
	_, err = e.trade.ApplyOffer(ctx, postgres.TradeWrite{Offer: retirada, Event: postgres.EventTradeWithdrawn})
	require.NoError(t, err)
	_, err = e.trade.ApplyOffer(ctx, postgres.TradeWrite{Insert: true, Offer: inversa, Event: postgres.EventTradeProposed})
	assert.NoError(t, err, "cerrada la primera, cabe otra")
}

// Las líneas: nada de regalos, un recurso en un solo lado, sólo recursos del
// catálogo.
func TestLosCheckDeLasLineasDeUnaOferta(t *testing.T) {
	store, ctx := newTestStore(t)
	e := nuevoEscenarioComercial(t, ctx, store)
	o := e.proponer(t, ctx, e.ofertaNueva(uuid.NewString()))

	for nombre, sql := range map[string]string{
		"cantidad cero":         `INSERT INTO trade_offer_lines (offer_id, side, resource, amount) VALUES ($1, 'WANT', 'STONE', 0)`,
		"recurso inventado":     `INSERT INTO trade_offer_lines (offer_id, side, resource, amount) VALUES ($1, 'WANT', 'GOLD', 5)`,
		"lado inventado":        `INSERT INTO trade_offer_lines (offer_id, side, resource, amount) VALUES ($1, 'LEND', 'STONE', 5)`,
		"recurso en dos lados":  `INSERT INTO trade_offer_lines (offer_id, side, resource, amount) VALUES ($1, 'WANT', 'WOOD', 5)`,
		"por encima de la cota": `INSERT INTO trade_offer_lines (offer_id, side, resource, amount) VALUES ($1, 'WANT', 'STONE', 1000001)`,
	} {
		_, err := store.Pool().Exec(ctx, sql, o.ID)
		assert.Error(t, err, nombre)
	}
}

// INV-TRADE-001, INV-TRADE-007 e INV-ECON-002: el trato debita los dos
// almacenes, crea sus dos caravanas con lo debitado y cierra la oferta en una
// transacción; repetirlo no mueve nada otra vez y devuelve las mismas
// caravanas.
func TestUnTratoDebitaLosDosAlmacenesYCreaSusCaravanasUnaVez(t *testing.T) {
	store, ctx := newTestStore(t)
	e := nuevoEscenarioComercial(t, ctx, store)
	e.llenar(t, ctx)
	o := e.proponer(t, ctx, e.ofertaNueva(uuid.NewString()))
	a := e.aceptacion(o, uuid.NewString())

	primera, err := e.trade.Accept(ctx, a)
	require.NoError(t, err)
	require.Len(t, primera, 2)
	otra, err := e.trade.Accept(ctx, a)
	require.NoError(t, err, "un reintento")
	assert.Equal(t, primera, otra, "el reintento devuelve las caravanas que ya creó")

	// Nada entra en ningún almacén hasta que llegue su caravana.
	madera, _ := stockDe(t, ctx, store, o.ProposerCity, economy.ResourceWood)
	piedra, _ := stockDe(t, ctx, store, o.ProposerCity, economy.ResourceStone)
	assert.Equal(t, [2]int64{0, 0}, [2]int64{madera, piedra})
	madera, _ = stockDe(t, ctx, store, o.TargetCity, economy.ResourceWood)
	piedra, _ = stockDe(t, ctx, store, o.TargetCity, economy.ResourceStone)
	assert.Equal(t, [2]int64{0, 0}, [2]int64{madera, piedra})
	assert.Equal(t, 1, eventos(t, ctx, store, postgres.EventTradeAccepted))

	// Las dos caravanas, cada una con su unidad CARAVAN en marcha, su
	// movimiento ACTIVE y su carga: la de quien propuso lleva lo que daba.
	assert.Equal(t, 2, contar(t, ctx, store,
		`SELECT count(*) FROM caravans WHERE offer_id = $1 AND status = 'IN_TRANSIT'`, o.ID))
	for i, c := range primera {
		var tipo, estado string
		var dueno uuid.UUID
		require.NoError(t, store.Pool().QueryRow(ctx,
			`SELECT unit_type, status, player_id FROM units WHERE id = $1`, c.UnitID).Scan(&tipo, &estado, &dueno))
		assert.Equal(t, [2]string{"CARAVAN", "MOVING"}, [2]string{tipo, estado})
		assert.Equal(t, a.Caravans[i].Sender, dueno)
		assert.Equal(t, 1, contar(t, ctx, store,
			`SELECT count(*) FROM unit_movements WHERE id = $1 AND unit_id = $2 AND status = 'ACTIVE'`,
			a.Caravans[i].Movement.ID, c.UnitID))
	}
	var recurso string
	var cantidad int64
	require.NoError(t, store.Pool().QueryRow(ctx,
		`SELECT resource, amount FROM caravan_cargo WHERE caravan_id = $1`, primera[0].CaravanID).Scan(&recurso, &cantidad))
	assert.Equal(t, "WOOD", recurso)
	assert.Equal(t, int64(300), cantidad)
	var ids []int64
	require.NoError(t, store.Pool().QueryRow(ctx,
		`SELECT ARRAY(SELECT jsonb_array_elements_text(payload->'caravanIds')::bigint) FROM world_events WHERE event_type = $1`,
		postgres.EventTradeAccepted).Scan(&ids))
	assert.Equal(t, []int64{primera[0].CaravanID, primera[1].CaravanID}, ids)

	cerrada, err := e.trade.Get(ctx, o.ID)
	require.NoError(t, err)
	assert.Equal(t, trade.StatusAccepted, cerrada.Status)
	assert.Equal(t, a.Offer.AcceptedRequestID, cerrada.AcceptedRequestID)
	pendientes, err := e.trade.ListPending(ctx)
	require.NoError(t, err)
	assert.Empty(t, pendientes)

	// Otra aceptación, con otra orden, sobre la oferta ya cerrada: conflicto.
	_, err = e.trade.Accept(ctx, e.aceptacion(o, uuid.NewString()))
	assert.ErrorIs(t, err, postgres.ErrVersionConflict)
}

// INV-TRADE-001: si un delta no se aplica (la base va por delante) o dejaría un
// stock negativo, no se mueve nada y la oferta sigue pendiente.
func Test_INV_TRADE_001_UnTratoConFalloEnUnDeltaNoMueveNada(t *testing.T) {
	store, ctx := newTestStore(t)
	e := nuevoEscenarioComercial(t, ctx, store)
	e.llenar(t, ctx)
	o := e.proponer(t, ctx, e.ofertaNueva(uuid.NewString()))

	// La base ya tiene la versión con la que llega el débito de la piedra de
	// anfitrion: el delta no se aplica, y nada de lo demás tampoco.
	adelantada := e.aceptacion(o, uuid.NewString())
	_, versionPiedraAnfitrion := stockDe(t, ctx, store, o.TargetCity, economy.ResourceStone)
	adelantada.Stocks[1].Version = versionPiedraAnfitrion
	_, err := e.trade.Accept(ctx, adelantada)
	assert.ErrorIs(t, err, postgres.ErrStaleStock)

	sinPiedra := e.aceptacion(o, uuid.NewString())
	sinPiedra.Stocks[1].Delta = -101
	_, err = e.trade.Accept(ctx, sinPiedra)
	assert.ErrorIs(t, err, economy.ErrInsufficientResources)

	// INV-TRADE-007: sin trato, ninguna caravana ni ninguna unidad.
	assert.Zero(t, contar(t, ctx, store, `SELECT count(*) FROM caravans`))
	assert.Zero(t, contar(t, ctx, store, `SELECT count(*) FROM units WHERE unit_type = 'CARAVAN'`))

	madera, version := stockDe(t, ctx, store, o.ProposerCity, economy.ResourceWood)
	assert.Equal(t, [2]int64{300, 1}, [2]int64{madera, version}, "la madera de quien ofrece no se movió")
	piedra, version := stockDe(t, ctx, store, o.TargetCity, economy.ResourceStone)
	assert.Equal(t, [2]int64{100, 1}, [2]int64{piedra, version})
	pendientes, err := e.trade.ListPending(ctx)
	require.NoError(t, err)
	assert.Len(t, pendientes, 1, "la oferta sigue pendiente")
	assert.Zero(t, eventos(t, ctx, store, postgres.EventTradeAccepted))
}

// Una caducidad repetida termina sin escribir el evento otra vez; sobre una
// oferta ya aceptada, es un conflicto.
func TestUnaCaducidadEsIdempotente(t *testing.T) {
	store, ctx := newTestStore(t)
	e := nuevoEscenarioComercial(t, ctx, store)
	o := e.proponer(t, ctx, e.ofertaNueva(uuid.NewString()))
	caducada, err := o.TransitionTo(trade.StatusExpired, time.Now())
	require.NoError(t, err)
	w := postgres.TradeWrite{Offer: caducada, Event: postgres.EventTradeExpired, Reason: trade.ExpiryTimeout}
	for vez := 0; vez < 2; vez++ {
		_, err := e.trade.ApplyOffer(ctx, w)
		require.NoError(t, err)
	}
	assert.Equal(t, 1, eventos(t, ctx, store, postgres.EventTradeExpired))

	var motivo string
	require.NoError(t, store.Pool().QueryRow(ctx,
		`SELECT payload->>'reason' FROM world_events WHERE event_type = $1`, postgres.EventTradeExpired).Scan(&motivo))
	assert.Equal(t, trade.ExpiryTimeout, motivo)

	rechazada, err := o.TransitionTo(trade.StatusRejected, time.Now())
	require.NoError(t, err)
	_, err = e.trade.ApplyOffer(ctx, postgres.TradeWrite{Offer: rechazada, Event: postgres.EventTradeRejected})
	assert.ErrorIs(t, err, postgres.ErrVersionConflict)
}
