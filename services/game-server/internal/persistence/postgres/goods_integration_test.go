//go:build integration

package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/building"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/craft"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/economy"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/trade"
	"github.com/empires-online/empires-online/services/game-server/internal/persistence/postgres"
)

// Integración de los bienes de civilización (SPEC-CIVILIZATION-GOODS §9): los
// CHECK y el único parcial de 000016, el encargo y la entrega de la elaboración
// en su transacción, el derribo del taller y un trato con bienes.

func taller(cityID int64, x, y int32) building.Building {
	return building.Building{CityID: cityID, Type: building.TypeWorkshop, X: x, Y: y,
		Status: building.StatusUnderConstruction, CompletesAtMs: 1_757_376_045_000}
}

// recursosEnBase deja madera y piedra en la ciudad, con la versión 1.
func (e escenarioEconomico) recursos(t *testing.T, ctx context.Context, madera, piedra int64) {
	t.Helper()
	require.NoError(t, e.economia.FlushStocks(ctx, []postgres.StockDelta{
		{CityID: e.ciudad(), Resource: economy.ResourceWood, Delta: madera, Version: 1},
		{CityID: e.ciudad(), Resource: economy.ResourceStone, Delta: piedra, Version: 1},
	}))
}

// debitos son los dos débitos de un coste en madera y piedra, con la versión 2.
func (e escenarioEconomico) debitos(madera, piedra, version int64) []postgres.StockDelta {
	return []postgres.StockDelta{
		{CityID: e.ciudad(), Resource: economy.ResourceWood, Delta: -madera, Version: version},
		{CityID: e.ciudad(), Resource: economy.ResourceStone, Delta: -piedra, Version: version},
	}
}

// conTallerCompleto construye y completa el taller de la ciudad.
func (e escenarioEconomico) conTallerCompleto(t *testing.T, ctx context.Context) int64 {
	t.Helper()
	e.recursos(t, ctx, 500, 200)
	id, err := e.economia.Build(ctx, "req-taller", e.jugador.Player.ID, taller(e.ciudad(), 21, 21), e.debitos(100, 50, 2), 1)
	require.NoError(t, err)
	require.NoError(t, e.economia.CompleteBuilding(ctx, id, e.ciudad(), e.jugador.Player.ID, 0, 2))
	return id
}

func (e escenarioEconomico) elaboracion(requestID string) craft.Craft {
	return craft.Craft{CityID: e.ciudad(), Good: economy.ResourceWine, Amount: 5, CompletesAtMs: 1_757_376_030_000, RequestID: requestID}
}

// INV-ECON-010: el único parcial impide un segundo taller en pie; derribado el
// primero, se puede volver a construir. Las dos líneas del coste se debitan en
// la misma transacción.
func Test_INV_ECON_010_UnTallerEnPiePorCiudad(t *testing.T) {
	store, ctx := newTestStore(t)
	e := nuevaEconomia(t, ctx, store)
	e.recursos(t, ctx, 500, 200)

	id, err := e.economia.Build(ctx, "req-1", e.jugador.Player.ID, taller(e.ciudad(), 21, 21), e.debitos(100, 50, 2), 1)
	require.NoError(t, err)
	madera, _ := stockDe(t, ctx, store, e.ciudad(), economy.ResourceWood)
	piedra, _ := stockDe(t, ctx, store, e.ciudad(), economy.ResourceStone)
	assert.Equal(t, [2]int64{400, 150}, [2]int64{madera, piedra})

	_, err = e.economia.Build(ctx, "req-2", e.jugador.Player.ID, taller(e.ciudad(), 19, 19), e.debitos(100, 50, 3), 1)
	assert.ErrorIs(t, err, building.ErrInvalidSite)
	madera, _ = stockDe(t, ctx, store, e.ciudad(), economy.ResourceWood)
	assert.Equal(t, int64(400), madera, "el rechazo no debita")

	require.NoError(t, e.economia.DestroyBuilding(ctx, postgres.BuildingDestruction{
		BuildingID: id, CityID: e.ciudad(), VictimPlayer: e.jugador.Player.ID, AttackerPlayer: e.jugador.Player.ID,
		Type: building.TypeWorkshop, Tick: 2,
	}))
	_, err = e.economia.Build(ctx, "req-3", e.jugador.Player.ID, taller(e.ciudad(), 19, 19), e.debitos(100, 50, 4), 3)
	assert.NoError(t, err, "con el anterior derribado, otro taller")
}

// RN-GOODS-004 e INV-ECON-002: la elaboración debita y escribe su fila en una
// transacción, idempotente por request_id; otra con otra orden es CITY_BUSY, y
// sin taller completo no se escribe ni se debita nada.
func TestLaElaboracionSeEncargaEnUnaTransaccion(t *testing.T) {
	store, ctx := newTestStore(t)
	e := nuevaEconomia(t, ctx, store)
	e.recursos(t, ctx, 500, 200)
	assert.ErrorIs(t, e.economia.Craft(ctx, "sin-taller", e.jugador.Player.ID, e.elaboracion("sin-taller"), e.debitos(30, 15, 2), 1),
		craft.ErrNoWorkshop)
	madera, _ := stockDe(t, ctx, store, e.ciudad(), economy.ResourceWood)
	assert.Equal(t, int64(500), madera, "sin taller no se debita nada")

	id, err := e.economia.Build(ctx, "req-taller", e.jugador.Player.ID, taller(e.ciudad(), 21, 21), e.debitos(100, 50, 2), 1)
	require.NoError(t, err)
	require.NoError(t, e.economia.CompleteBuilding(ctx, id, e.ciudad(), e.jugador.Player.ID, 0, 2))
	for vez := 0; vez < 2; vez++ {
		require.NoError(t, e.economia.Craft(ctx, "req-c", e.jugador.Player.ID, e.elaboracion("req-c"), e.debitos(30, 15, 3), 4), "vez %d", vez)
	}
	madera, _ = stockDe(t, ctx, store, e.ciudad(), economy.ResourceWood)
	piedra, _ := stockDe(t, ctx, store, e.ciudad(), economy.ResourceStone)
	assert.Equal(t, [2]int64{370, 135}, [2]int64{madera, piedra})
	assert.Equal(t, 1, eventos(t, ctx, store, postgres.EventCraftOrdered))

	assert.ErrorIs(t, e.economia.Craft(ctx, "otra", e.jugador.Player.ID, e.elaboracion("otra"), e.debitos(30, 15, 4), 5), craft.ErrBusy)

	filas, err := e.economia.ListCrafts(ctx)
	require.NoError(t, err)
	require.Len(t, filas, 1)
	assert.Equal(t, e.elaboracion("req-c"), filas[0])
}

// ADR-036 §4 e INV-ECON-009: la entrega borra la fila y acredita el bien en una
// transacción; un reintento no acredita otra vez.
func TestLaEntregaAcreditaElBienUnaSolaVez(t *testing.T) {
	store, ctx := newTestStore(t)
	e := nuevaEconomia(t, ctx, store)
	e.conTallerCompleto(t, ctx)
	require.NoError(t, e.economia.Craft(ctx, "req-c", e.jugador.Player.ID, e.elaboracion("req-c"), e.debitos(30, 15, 3), 4))

	entrega := postgres.StockDelta{CityID: e.ciudad(), Resource: economy.ResourceWine, Delta: 5, Version: 1}
	for vez := 0; vez < 2; vez++ {
		require.NoError(t, e.economia.CompleteCraft(ctx, e.ciudad(), "req-c", e.jugador.Player.ID, entrega, 5), "vez %d", vez)
	}
	vino, _ := stockDe(t, ctx, store, e.ciudad(), economy.ResourceWine)
	assert.Equal(t, int64(5), vino)
	filas, err := e.economia.ListCrafts(ctx)
	require.NoError(t, err)
	assert.Empty(t, filas)
	assert.Equal(t, 1, eventos(t, ctx, store, postgres.EventCraftCompleted))
}

// RN-GOODS-007: derribar el taller borra la elaboración en curso, sin devolver
// nada; su entrega posterior no acredita.
func TestDerribarElTallerBorraLaElaboracion(t *testing.T) {
	store, ctx := newTestStore(t)
	e := nuevaEconomia(t, ctx, store)
	id := e.conTallerCompleto(t, ctx)
	require.NoError(t, e.economia.Craft(ctx, "req-c", e.jugador.Player.ID, e.elaboracion("req-c"), e.debitos(30, 15, 3), 4))
	require.NoError(t, e.economia.DestroyBuilding(ctx, postgres.BuildingDestruction{
		BuildingID: id, CityID: e.ciudad(), VictimPlayer: e.jugador.Player.ID, AttackerPlayer: e.jugador.Player.ID,
		Type: building.TypeWorkshop, Tick: 5,
	}))
	filas, err := e.economia.ListCrafts(ctx)
	require.NoError(t, err)
	assert.Empty(t, filas)
	require.NoError(t, e.economia.CompleteCraft(ctx, e.ciudad(), "req-c", e.jugador.Player.ID,
		postgres.StockDelta{CityID: e.ciudad(), Resource: economy.ResourceWine, Delta: 5, Version: 1}, 6))
	vino, _ := stockDe(t, ctx, store, e.ciudad(), economy.ResourceWine)
	assert.Zero(t, vino)
}

// INV-ECON-009 y INV-ECON-010 en el esquema: una elaboración sólo de un bien y
// de una cantidad positiva, una por ciudad; el bien de cada civilización es único.
func TestLosCheckDeLosBienes(t *testing.T) {
	store, ctx := newTestStore(t)
	e := nuevaEconomia(t, ctx, store)
	for _, c := range []struct {
		good   string
		amount int
	}{{"WOOD", 5}, {"WINE", 0}} {
		_, err := store.Pool().Exec(ctx,
			`INSERT INTO city_crafts (city_id, good, amount, completes_at_ms, request_id) VALUES ($1, $2, $3, 1, 'x')`,
			e.ciudad(), c.good, c.amount)
		assert.True(t, postgres.IsCheckViolation(err), "%s × %d: %v", c.good, c.amount, err)
	}
	_, err := store.Pool().Exec(ctx,
		`INSERT INTO city_crafts (city_id, good, amount, completes_at_ms, request_id) VALUES ($1, 'WINE', 5, 1, 'a'), ($1, 'WINE', 5, 1, 'b')`,
		e.ciudad())
	assert.True(t, postgres.IsUniqueViolation(err), "una por ciudad: %v", err)
	_, err = store.Pool().Exec(ctx, `UPDATE civilizations SET good = 'WINE' WHERE code = 'NORSE'`)
	assert.True(t, postgres.IsUniqueViolation(err), "un bien por civilización: %v", err)
	_, err = store.Pool().Exec(ctx, `INSERT INTO city_stocks (city_id, resource) VALUES ($1, 'GOLD')`, e.ciudad())
	assert.True(t, postgres.IsCheckViolation(err), "GOLD no es un recurso: %v", err)
}

// RN-GOODS-008 e INV-TRADE-001: un trato de vino por pieles sale en dos
// caravanas como uno de madera por piedra, y cada una entrega su bien.
func TestUnTratoDeVinoPorPieles(t *testing.T) {
	store, ctx := newTestStore(t)
	e := nuevoEscenarioComercial(t, ctx, store)
	require.NoError(t, e.economia.FlushStocks(ctx, []postgres.StockDelta{
		{CityID: e.visitante.City.ID, Resource: economy.ResourceWine, Delta: 30, Version: 1},
		{CityID: e.anfitrion.City.ID, Resource: economy.ResourceFurs, Delta: 20, Version: 1},
	}))
	ahora := time.UnixMilli(time.Now().UnixMilli()).UTC()
	o := e.proponer(t, ctx, trade.Offer{
		Proposer: e.visitante.Player.ID, Target: e.anfitrion.Player.ID,
		ProposerCity: e.visitante.City.ID, TargetCity: e.anfitrion.City.ID,
		Give:   economy.Costs{{Resource: economy.ResourceWine, Amount: 30}},
		Want:   economy.Costs{{Resource: economy.ResourceFurs, Amount: 20}},
		Status: trade.StatusProposed, RequestID: uuid.NewString(), CreatedAt: ahora, ExpiresAt: ahora.Add(24 * time.Hour),
	})
	cerrada, _ := o.TransitionTo(trade.StatusAccepted, ahora)
	cerrada.AcceptedRequestID = uuid.NewString()
	creadas, err := e.trade.Accept(ctx, postgres.TradeAcceptance{
		Offer: cerrada, AtMs: ahora.UnixMilli(), By: o.Target,
		Stocks: []postgres.StockDelta{
			{CityID: o.ProposerCity, Resource: economy.ResourceWine, Delta: -30, Version: 2},
			{CityID: o.TargetCity, Resource: economy.ResourceFurs, Delta: -20, Version: 2},
		},
		Caravans: caravanasDe(o, 1001),
	})
	require.NoError(t, err)
	require.Len(t, creadas, 2)

	caravanas := postgres.NewCaravanRepo(store, 32)
	require.NoError(t, caravanas.Deliver(ctx, postgres.CaravanDelivery{
		CaravanID: creadas[0].CaravanID, UnitID: creadas[0].UnitID, CityID: o.TargetCity,
		Recipient: o.Target, Counterpart: o.Proposer, Cargo: o.Give,
		Stocks: []postgres.StockDelta{{CityID: o.TargetCity, Resource: economy.ResourceWine, Delta: 30, Version: 1}},
	}))
	require.NoError(t, caravanas.Deliver(ctx, postgres.CaravanDelivery{
		CaravanID: creadas[1].CaravanID, UnitID: creadas[1].UnitID, CityID: o.ProposerCity,
		Recipient: o.Proposer, Counterpart: o.Target, Cargo: o.Want,
		Stocks: []postgres.StockDelta{{CityID: o.ProposerCity, Resource: economy.ResourceFurs, Delta: 20, Version: 1}},
	}))
	vino, _ := stockDe(t, ctx, store, o.TargetCity, economy.ResourceWine)
	pieles, _ := stockDe(t, ctx, store, o.ProposerCity, economy.ResourceFurs)
	assert.Equal(t, [2]int64{30, 20}, [2]int64{vino, pieles})
	quedan, _ := stockDe(t, ctx, store, o.ProposerCity, economy.ResourceWine)
	assert.Zero(t, quedan)
}
