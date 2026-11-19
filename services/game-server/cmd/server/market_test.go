package main

import (
	"bytes"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/economy"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/market"
	"github.com/empires-online/empires-online/services/game-server/internal/persistence/postgres"
)

// La carga deja fuera, con su invariant_violation, una oferta que la base señala
// (INV-TRADE-009) y una que no pasa la validación del dominio (INV-TRADE-008).
func TestLaCargaExcluyeLasOfertasQueNoCuadran(t *testing.T) {
	ahora := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	nueva := func(id int64) market.Offer {
		o, err := market.New(uuid.New(), 1, economy.Cost{Resource: economy.ResourceStone, Amount: 20},
			economy.Cost{Resource: economy.ResourceWood, Amount: 30}, 3, "r", ahora, 24*time.Hour)
		require.NoError(t, err)
		o.ID = id
		return o
	}
	buena, señalada, rota := nueva(1), nueva(2), nueva(3)
	rota.SellerCommission = 1
	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, nil))

	out := excludeMarketViolations([]market.Offer{buena, señalada, rota},
		[]postgres.MarketViolation{{OfferID: 2, InvID: "INV-TRADE-009", Detail: "no cuadra"}}, log)
	require.Len(t, out, 1)
	assert.Equal(t, int64(1), out[0].ID)
	assert.Contains(t, logs.String(), "INV-TRADE-009")
	assert.Contains(t, logs.String(), "INV-TRADE-008")
	assert.Contains(t, logs.String(), "market_offer:3")
}
