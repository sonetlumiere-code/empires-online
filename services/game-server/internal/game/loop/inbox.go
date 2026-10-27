package loop

import (
	"log/slog"

	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
	"github.com/empires-online/empires-online/services/game-server/internal/observability"
)

// Inbox es el buzón del loop: el canal por el que el propio servidor se
// devuelve comandos —las compensaciones y respuestas de la cola de persistencia
// y las bajas de sesión— sin competir con los comandos de los jugadores por el
// sitio en el canal de comandos (ADR-022).
//
// El canal de comandos lo llenan los jugadores a su ritmo; si una compensación
// tuviera que caber en él, una avalancha de órdenes podría dejar RAM y base de
// datos sin reconciliar hasta el siguiente arranque, y una baja de sesión que
// no cabe deja una ciudad ONLINE para siempre. Por eso el loop drena este
// buzón ANTES que el canal de comandos en cada tick, y por eso Offer nunca
// bloquea: lo llaman los workers de la cola y las goroutines de las conexiones,
// y a veces el propio loop.
//
// Lo que no cabe ni aquí se cuenta en eo_commands_dropped_total y se registra
// como error: nunca se pierde en silencio.
type Inbox struct {
	ch      chan simulation.Command
	metrics *observability.Metrics
	log     *slog.Logger
}

// NewInbox crea un buzón con la capacidad dada.
func NewInbox(capacity int, metrics *observability.Metrics, log *slog.Logger) *Inbox {
	if capacity <= 0 {
		capacity = 1
	}
	if log == nil {
		log = slog.Default()
	}
	return &Inbox{ch: make(chan simulation.Command, capacity), metrics: metrics, log: log}
}

// Offer deposita un comando sin bloquear y dice si cupo. Si no cupo, el comando
// no se aplicará nunca: se cuenta y se registra aquí, en el único sitio que lo
// sabe. Sirve tal cual como simulation.Deps.Inbox.
func (b *Inbox) Offer(cmd simulation.Command) bool {
	select {
	case b.ch <- cmd:
		return true
	default:
		tipo := simulation.CommandType(cmd)
		b.log.Error("buzón del loop saturado; comando descartado", "type", tipo, "capacity", cap(b.ch))
		if b.metrics != nil {
			b.metrics.CommandsDropped.WithLabelValues(tipo).Inc()
		}
		return false
	}
}

// Len es cuántos comandos esperan en el buzón.
func (b *Inbox) Len() int { return len(b.ch) }
