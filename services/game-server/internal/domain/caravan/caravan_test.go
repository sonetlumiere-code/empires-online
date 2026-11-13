package caravan_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/caravan"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/economy"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/trade"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
)

var (
	quienOfrece = uuid.MustParse("11111111-1111-4111-8111-111111111111")
	quienAcepta = uuid.MustParse("22222222-2222-4222-8222-222222222222")
	tercero     = uuid.MustParse("33333333-3333-4333-8333-333333333333")
)

// RN-CARAVAN-001 e INV-TRADE-007: un trato da dos caravanas, la de quien propuso
// primero, cada una con lo que da su parte, de su ciudad a la otra.
func TestUnTratoDaDosCaravanasUnaPorSentido(t *testing.T) {
	o := trade.Offer{
		Proposer: quienOfrece, Target: quienAcepta, ProposerCity: 1, TargetCity: 2,
		Give: economy.Costs{{Resource: economy.ResourceWood, Amount: 300}},
		Want: economy.Costs{{Resource: economy.ResourceStone, Amount: 100}, {Resource: economy.ResourceSilk, Amount: 5}},
	}
	h := caravan.Halves(o)
	assert.Equal(t, caravan.Half{Sender: quienOfrece, FromCity: 1, ToCity: 2, Cargo: o.Give}, h[0])
	assert.Equal(t, caravan.Half{Sender: quienAcepta, FromCity: 2, ToCity: 1, Cargo: o.Want}, h[1])

	// La carga es una copia: cambiar la oferta no cambia la caravana (INV-TRADE-005).
	o.Give[0].Amount = 1
	assert.Equal(t, int64(300), h[0].Cargo[0].Amount)
}

// RN-CARAVAN-002: la ruta de vuelta es la misma al revés.
func TestLaRutaDeVueltaEsLaMismaAlReves(t *testing.T) {
	ida := []world.Tile{{X: 1, Y: 1}, {X: 2, Y: 2}, {X: 3, Y: 2}}
	assert.Equal(t, []world.Tile{{X: 3, Y: 2}, {X: 2, Y: 2}, {X: 1, Y: 1}}, caravan.Reverse(ida))
	assert.Equal(t, []world.Tile{{X: 1, Y: 1}, {X: 2, Y: 2}, {X: 3, Y: 2}}, ida, "no toca la de ida")
	assert.Empty(t, caravan.Reverse(nil))
}

// Una carga válida: no vacía, positiva, del catálogo y sin repetir.
func TestLaCargaDeUnaCaravana(t *testing.T) {
	require.NoError(t, caravan.ValidateCargo(economy.Costs{{Resource: economy.ResourceFurs, Amount: 20}}))
	for nombre, carga := range map[string]economy.Costs{
		"vacía":       nil,
		"cero":        {{Resource: economy.ResourceWood, Amount: 0}},
		"inventada":   {{Resource: "GOLD", Amount: 5}},
		"repetida":    {{Resource: economy.ResourceWood, Amount: 5}, {Resource: economy.ResourceWood, Amount: 1}},
		"en negativo": {{Resource: economy.ResourceStone, Amount: -3}},
	} {
		assert.ErrorIs(t, caravan.ValidateCargo(carga), caravan.ErrInvalidCargo, nombre)
	}
}

// RN-CARAVAN-009: ven la carga su dueño, quien la mandó y el destinatario
// original; un tercero, no.
func TestQuienVeLaCarga(t *testing.T) {
	assert.True(t, caravan.SeesCargo(quienOfrece, quienOfrece, quienOfrece, quienAcepta))
	assert.True(t, caravan.SeesCargo(quienAcepta, quienOfrece, quienOfrece, quienAcepta))
	assert.True(t, caravan.SeesCargo(tercero, tercero, quienOfrece, quienAcepta), "el captor es su dueño")
	assert.False(t, caravan.SeesCargo(tercero, quienOfrece, quienOfrece, quienAcepta))
	assert.False(t, caravan.SeesCargo(uuid.Nil, quienOfrece, quienOfrece, quienAcepta))
}

func TestLosEstadosDeUnaCaravana(t *testing.T) {
	for _, s := range caravan.Statuses() {
		assert.True(t, s.Valid(), s)
	}
	assert.False(t, caravan.Status("ROBADA").Valid())
	c := caravan.Caravan{ToCity: 3, OriginalToCity: 2}
	assert.True(t, c.Captured())
	c.ToCity = 2
	assert.False(t, c.Captured(), "recuperada por quien la esperaba, va adonde iba")
}
