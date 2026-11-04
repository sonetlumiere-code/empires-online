package trade

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/economy"
)

var (
	quienOfrece = uuid.MustParse("11111111-1111-4111-8111-111111111111")
	quienRecibe = uuid.MustParse("22222222-2222-4222-8222-222222222222")
	ahora       = time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
)

func madera(n int64) economy.Cost { return economy.Cost{Resource: economy.ResourceWood, Amount: n} }
func piedra(n int64) economy.Cost { return economy.Cost{Resource: economy.ResourceStone, Amount: n} }

func ofertaPendiente() Offer {
	return Offer{
		ID: 7, Proposer: quienOfrece, Target: quienRecibe, ProposerCity: 1, TargetCity: 5,
		Give: economy.Costs{madera(300)}, Want: economy.Costs{piedra(100)},
		Status: StatusProposed, RequestID: "r1", CreatedAt: ahora, ExpiresAt: ahora.Add(24 * time.Hour),
	}
}

// SPEC-TRADE §4.2, Q3 y Q4: hay que dar y pedir algo, de 1 a la cota técnica,
// cada recurso en un solo lado.
func TestLasLineasDeUnaOferta(t *testing.T) {
	require.NoError(t, ValidateLines(economy.Costs{madera(300)}, economy.Costs{piedra(100)}))
	require.NoError(t, ValidateLines(economy.Costs{piedra(1)}, economy.Costs{madera(MaxAmount)}))

	casos := []struct {
		nombre     string
		give, want economy.Costs
	}{
		{"sin dar nada (un regalo pedido)", nil, economy.Costs{piedra(10)}},
		{"sin pedir nada (un regalo)", economy.Costs{madera(10)}, nil},
		{"pedir 0", economy.Costs{madera(10)}, economy.Costs{piedra(0)}},
		{"una cantidad negativa", economy.Costs{madera(-5)}, economy.Costs{piedra(10)}},
		{"por encima de la cota", economy.Costs{madera(MaxAmount + 1)}, economy.Costs{piedra(10)}},
		{"el mismo recurso en los dos lados", economy.Costs{madera(10)}, economy.Costs{madera(20)}},
		{"un recurso repetido en un lado", economy.Costs{madera(10), madera(5)}, economy.Costs{piedra(1)}},
		{"un recurso fuera del catálogo", economy.Costs{{Resource: "GOLD", Amount: 5}}, economy.Costs{piedra(1)}},
	}
	for _, c := range casos {
		err := ValidateLines(c.give, c.want)
		assert.ErrorIs(t, err, ErrOfertaInvalida, c.nombre)
	}
}

// INV-TRADE-004: sólo responde el destinatario y sólo retira quien ofreció; un
// tercero no puede nada, y una oferta cerrada no admite ninguna de las dos.
func TestSoloRespondeElDestinatarioYSoloRetiraQuienOfrece(t *testing.T) {
	o := ofertaPendiente()
	tercero := uuid.New()

	assert.NoError(t, o.CanRespond(quienRecibe))
	assert.ErrorIs(t, o.CanRespond(quienOfrece), ErrNoPuede, "nadie acepta su propia oferta")
	assert.ErrorIs(t, o.CanRespond(tercero), ErrNoEsParte)

	assert.NoError(t, o.CanWithdraw(quienOfrece))
	assert.ErrorIs(t, o.CanWithdraw(quienRecibe), ErrNoPuede, "nadie retira la oferta ajena")
	assert.ErrorIs(t, o.CanWithdraw(tercero), ErrNoEsParte)

	cerrada, err := o.TransitionTo(StatusRejected, ahora)
	require.NoError(t, err)
	assert.ErrorIs(t, cerrada.CanRespond(quienRecibe), ErrNoPuede)
	assert.ErrorIs(t, cerrada.CanWithdraw(quienOfrece), ErrNoPuede)
}

// Una oferta sólo sale de PROPOSED, a uno de sus cuatro finales, y sella su
// cierre; un final no vuelve a moverse.
func TestElCicloDeVidaDeUnaOferta(t *testing.T) {
	for _, fin := range []Status{StatusAccepted, StatusRejected, StatusWithdrawn, StatusExpired} {
		cerrada, err := ofertaPendiente().TransitionTo(fin, ahora)
		require.NoError(t, err, fin)
		assert.Equal(t, fin, cerrada.Status)
		require.NotNil(t, cerrada.ClosedAt)
		assert.Equal(t, ahora, *cerrada.ClosedAt)

		_, err = cerrada.TransitionTo(StatusExpired, ahora)
		assert.True(t, errors.Is(err, ErrTransicionInvalida), "%s es un final", fin)
	}
	_, err := ofertaPendiente().TransitionTo(StatusProposed, ahora)
	assert.ErrorIs(t, err, ErrTransicionInvalida)
	_, err = ofertaPendiente().TransitionTo("PERDIDA", ahora)
	assert.ErrorIs(t, err, ErrTransicionInvalida)

	o := ofertaPendiente()
	assert.False(t, o.ExpiredAt(o.ExpiresAt.Add(-time.Millisecond)))
	assert.True(t, o.ExpiredAt(o.ExpiresAt), "el plazo es inclusivo")
}

// La vista desde cada lado: lo que saldría del almacén de quien mira y lo que
// entraría.
func TestLaOfertaVistaDesdeCadaLado(t *testing.T) {
	o := ofertaPendiente()
	give, get := o.For(quienOfrece)
	assert.Equal(t, economy.Costs{madera(300)}, give)
	assert.Equal(t, economy.Costs{piedra(100)}, get)

	give, get = o.For(quienRecibe)
	assert.Equal(t, economy.Costs{piedra(100)}, give)
	assert.Equal(t, economy.Costs{madera(300)}, get)
}

// INV-TRADE-001: las patas de un trato conservan cada recurso, en orden de
// ciudad y de recurso.
func TestLasPatasDeUnTratoConservanCadaRecurso(t *testing.T) {
	o := ofertaPendiente()
	assert.Equal(t, []Leg{
		{CityID: 1, Resource: economy.ResourceStone, Gets: 100},
		{CityID: 1, Resource: economy.ResourceWood, Pays: 300},
		{CityID: 5, Resource: economy.ResourceStone, Pays: 100},
		{CityID: 5, Resource: economy.ResourceWood, Gets: 300},
	}, o.Legs())

	neto := map[economy.Resource]int64{}
	for _, l := range o.Legs() {
		neto[l.Resource] += l.Gets - l.Pays
	}
	for r, n := range neto {
		assert.Zero(t, n, "el trato no crea ni destruye %s", r)
	}
}
