package main

import (
	"fmt"
	"log/slog"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/market"
	"github.com/empires-online/empires-online/services/game-server/internal/persistence/postgres"
)

// excludeMarketViolations deja fuera de la carga las ofertas que no cuadran con
// sus compras (INV-TRADE-009) o que no pasan la validación del dominio, y las
// señala; quedan en la base para revisarlas a mano.
func excludeMarketViolations(offers []market.Offer, malas []postgres.MarketViolation, log *slog.Logger) []market.Offer {
	excluidas := make(map[int64]bool, len(malas))
	for _, v := range malas {
		excluidas[v.OfferID] = true
		log.Error("invariant_violation", "inv_id", v.InvID, "severity", "CRITICO", "policy", "EXCLUDE",
			"entity", fmt.Sprintf("market_offer:%d", v.OfferID), "detail", v.Detail+": no se incorpora")
	}
	out := make([]market.Offer, 0, len(offers))
	for _, o := range offers {
		if excluidas[o.ID] {
			continue
		}
		if err := o.Validate(); err != nil {
			log.Error("invariant_violation", "inv_id", "INV-TRADE-008", "severity", "CRITICO", "policy", "EXCLUDE",
				"entity", fmt.Sprintf("market_offer:%d", o.ID), "detail", err.Error()+": no se incorpora")
			continue
		}
		out = append(out, o)
	}
	return out
}
