package websocket

import (
	"github.com/empires-online/empires-online/services/game-server/internal/domain/economy"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/trade"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// tradeLines traduce las líneas de trade.propose y comprueba lo mismo que el
// esquema Zod (DEBT-07): de una a tantas como recursos haya, cada recurso del
// catálogo y cada cantidad de 1 a trade.MaxAmount. Un recurso repetido o en
// los dos lados lo rechaza la simulación (INVALID_TARGET), no el esquema.
func tradeLines(in []protocol.ResourceAmount) (economy.Costs, bool) {
	if len(in) == 0 || len(in) > len(economy.Resources()) {
		return nil, false
	}
	out := make(economy.Costs, 0, len(in))
	for _, l := range in {
		r := economy.Resource(l.Resource)
		if !r.Valid() || l.Amount < 1 || l.Amount > trade.MaxAmount {
			return nil, false
		}
		out = append(out, economy.Cost{Resource: r, Amount: l.Amount})
	}
	return out, true
}
