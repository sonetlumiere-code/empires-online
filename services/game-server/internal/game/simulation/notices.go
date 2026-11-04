package simulation

import (
	"github.com/empires-online/empires-online/services/game-server/internal/domain/eventlog"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// sendNotices manda los avisos en vivo de un suceso que la fase 4 acaba de
// decidir (SPEC-LIVE-NOTICES, ADR-033). e es el evento que su fila de
// world_events va a tener: qué aviso le toca a quién lo decide la misma
// clasificación que el registro (eventlog.Notices). El aviso no espera a la
// base, no lleva id y no cambia nada de la simulación.
func (s *Simulation) sendNotices(e eventlog.Event) {
	for _, n := range eventlog.Notices(e) {
		p := protocol.EventLogNoticePayload{Kind: string(n.Kind), AtMs: e.AtMs, OtherPlayerID: n.Other.String()}
		// La ciudad del otro, como la nombra el registro (SPEC-EVENT-LOG Q6).
		if c, ok := s.state.CityOf(n.Other); ok && c.Name != "" {
			id, name := c.ID, c.Name
			p.OtherCityID, p.OtherCityName = &id, &name
		}
		switch n.Kind {
		case eventlog.KindUnitLost, eventlog.KindEnemyKilled:
			p.UnitType = e.UnitType
		case eventlog.KindPillageDone:
			amount := e.Amount
			p.Amount = &amount
		}
		if e.HasTile {
			x, y := e.X, e.Y
			p.X, p.Y = &x, &y
		}
		// La conquista nombra el territorio (SPEC-TERRITORY-CONQUEST §6).
		if e.TerritoryID != 0 {
			id := e.TerritoryID
			p.TerritoryID, p.TerritoryName = &id, e.TerritoryName
		}
		s.deps.Broadcaster.SendToPlayer(n.To, protocol.TypeEventLogNotice, "", p)
	}
}
