package market_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/economy"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/market"
)

var (
	vendedor  = uuid.MustParse("00000000-0000-0000-0000-00000000000a")
	comprador = uuid.MustParse("00000000-0000-0000-0000-00000000000b")
	ahora     = time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
)

func piedra(n int64) economy.Cost { return economy.Cost{Resource: economy.ResourceStone, Amount: n} }
func madera(n int64) economy.Cost { return economy.Cost{Resource: economy.ResourceWood, Amount: n} }

// El ejemplo de SPEC-MARKETS §4.2: 3 lotes de 20 de piedra por 30 de madera.
func ejemplo(t *testing.T) market.Offer {
	t.Helper()
	o, err := market.New(vendedor, 7, piedra(20), madera(30), 3, "r1", ahora, 24*time.Hour)
	require.NoError(t, err)
	return o
}

// RN-MARKET-006: la décima parte de cada lote, redondeada hacia arriba.
func TestLaComisionDeUnLote(t *testing.T) {
	casos := map[int64]int64{1: 1, 9: 1, 10: 1, 11: 2, 20: 2, 30: 3, 100: 10, 101: 11}
	for cantidad, comision := range casos {
		require.Equal(t, comision, market.Commission(cantidad), "lote de %d", cantidad)
	}
	require.Zero(t, market.Commission(0))
}

// SPEC-MARKETS §4.3: publicar el ejemplo cuesta 66 de piedra; comprar 2 lotes,
// 66 de madera; cada parte recibe exactamente lo anunciado.
func TestElEjemploDeLaSpec(t *testing.T) {
	o := ejemplo(t)
	require.Equal(t, int64(6), o.SellerCommission)
	require.Equal(t, piedra(66), o.PostCost())
	require.Equal(t, piedra(60), o.Custody())

	p, err := o.CanBuy(comprador, 2, 3)
	require.NoError(t, err)
	require.Equal(t, madera(60), p.Pay)
	require.Equal(t, int64(6), p.Commission)
	require.Equal(t, madera(66), p.Debit())
	require.Equal(t, piedra(40), p.Receive)

	require.NoError(t, o.Sell(2, ahora))
	require.Equal(t, piedra(20), o.Custody())

	// Caduca el lote que queda: vuelven 20 de piedra; la comisión, no.
	back, err := o.Close(market.StatusExpired, ahora.Add(24*time.Hour))
	require.NoError(t, err)
	require.Equal(t, piedra(20), back)
	require.Equal(t, int32(1), o.LotsLeft, "LotsLeft guarda los devueltos")
	require.Zero(t, o.Custody().Amount)
	require.NoError(t, o.Validate())
}

// RN-MARKET-002: cada condición de los términos.
func TestLosTerminosDeUnaOferta(t *testing.T) {
	require.ErrorIs(t, market.ValidateTerms(piedra(1), piedra(1), 1), market.ErrOfertaInvalida)
	require.ErrorIs(t, market.ValidateTerms(piedra(0), madera(1), 1), market.ErrOfertaInvalida)
	require.ErrorIs(t, market.ValidateTerms(piedra(1), madera(-1), 1), market.ErrOfertaInvalida)
	require.ErrorIs(t, market.ValidateTerms(piedra(1), madera(1), 0), market.ErrOfertaInvalida)
	require.ErrorIs(t, market.ValidateTerms(piedra(1), madera(1), 101), market.ErrOfertaInvalida)
	require.ErrorIs(t, market.ValidateTerms(economy.Cost{Resource: "GOLD", Amount: 1}, madera(1), 1), market.ErrOfertaInvalida)
	require.ErrorIs(t, market.ValidateTerms(piedra(10_001), madera(1), 100), market.ErrTotalExcesivo)
	require.ErrorIs(t, market.ValidateTerms(piedra(1), madera(1_000_001), 1), market.ErrTotalExcesivo)
	require.NoError(t, market.ValidateTerms(piedra(10_000), madera(1), 100))
	require.NoError(t, market.ValidateTerms(economy.Cost{Resource: economy.ResourceSilk, Amount: 5}, economy.Cost{Resource: economy.ResourceWine, Amount: 5}, 1))
}

// INV-TRADE-009 e INV-TRADE-010: nadie compra la suya ni más lotes de los que
// quedan sin reservar.
func TestQuienCompraYCuantosLotes(t *testing.T) {
	o := ejemplo(t)
	_, err := o.CanBuy(vendedor, 1, 3)
	require.ErrorIs(t, err, market.ErrPropia)
	_, err = o.CanBuy(comprador, 4, 4)
	require.ErrorIs(t, err, market.ErrSinLotes)
	_, err = o.CanBuy(comprador, 2, 1)
	require.ErrorIs(t, err, market.ErrSinLotes, "dos lotes reservados por otra compra")
	_, err = o.CanBuy(comprador, 0, 3)
	require.ErrorIs(t, err, market.ErrOfertaInvalida)

	require.NoError(t, o.Sell(3, ahora))
	require.Equal(t, market.StatusSoldOut, o.Status, "el último lote la agota")
	require.Zero(t, o.Custody().Amount)
	_, err = o.CanBuy(comprador, 1, 1)
	require.ErrorIs(t, err, market.ErrCerrada)
	require.ErrorIs(t, o.Sell(1, ahora), market.ErrCerrada)
	require.NoError(t, o.Validate())
}

// RN-MARKET-007: sólo retira quien publicó, y sólo una abierta.
func TestRetirarUnaOferta(t *testing.T) {
	o := ejemplo(t)
	require.ErrorIs(t, o.CanWithdraw(comprador), market.ErrPropia)
	require.NoError(t, o.CanWithdraw(vendedor))
	back, err := o.Close(market.StatusWithdrawn, ahora)
	require.NoError(t, err)
	require.Equal(t, piedra(60), back)
	require.ErrorIs(t, o.CanWithdraw(vendedor), market.ErrCerrada)
	_, err = o.Close(market.StatusExpired, ahora)
	require.ErrorIs(t, err, market.ErrCerrada, "no se devuelve dos veces")
	otra := ejemplo(t)
	_, err = otra.Close(market.StatusSoldOut, ahora)
	require.ErrorIs(t, err, market.ErrOfertaInvalida)
}

func TestLaCaducidad(t *testing.T) {
	o := ejemplo(t)
	require.False(t, o.ExpiredAt(ahora.Add(24*time.Hour-time.Millisecond)))
	require.True(t, o.ExpiredAt(ahora.Add(24*time.Hour)))
}

// La validación de una fila leída de la base.
func TestValidarUnaOfertaLeida(t *testing.T) {
	o := ejemplo(t)
	require.NoError(t, o.Validate())
	mal := o
	mal.LotsLeft = 4
	require.ErrorIs(t, mal.Validate(), market.ErrOfertaInvalida)
	mal = o
	mal.LotsLeft = 0
	require.ErrorIs(t, mal.Validate(), market.ErrOfertaInvalida, "abierta sin lotes")
	mal = o
	mal.SellerCommission = 5
	require.ErrorIs(t, mal.Validate(), market.ErrOfertaInvalida)
	mal = o
	mal.Status = "PENDING"
	require.ErrorIs(t, mal.Validate(), market.ErrOfertaInvalida)
	require.Len(t, market.Statuses(), 4)
}
