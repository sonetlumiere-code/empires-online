package simulation

import (
	"context"
	"time"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/diplomacy"
)

// treatySweepInterval es cada cuánto se busca tratados vencidos.
//
// La caducidad NO se comprueba en cada tick. A 10 Hz serían diez consultas por
// segundo para descartar, casi siempre, cero filas. Un tratado que caduca un
// segundo tarde no cambia nada del juego; diez consultas por segundo contra la
// base sí. Se muestrea con el reloj y no con un contador de ticks porque lo que
// importa es el tiempo real transcurrido, no cuántas veces avanzó el bucle.
const treatySweepInterval = time.Second

// TreatyExpirer caduca los tratados vencidos.
//
// Lo implementa el repositorio de tratados. La simulación sólo decide CUÁNDO
// mirar; el trabajo ocurre fuera del tick, en la cola de persistencia, porque el
// tick nunca hace I/O contra PostgreSQL.
type TreatyExpirer interface {
	ExpireDue(ctx context.Context, now time.Time, tick uint64) ([]diplomacy.Treaty, error)
}

// sweepTreaties encola la búsqueda de tratados vencidos si toca.
func (s *Simulation) sweepTreaties(now time.Time) {
	if s.deps.Treaties == nil {
		return
	}
	if now.Sub(s.lastTreatySweep) < treatySweepInterval {
		return
	}
	s.lastTreatySweep = now

	tick := s.state.Tick()
	expirer := s.deps.Treaties
	log := s.deps.Log

	s.deps.Persister.Submit(Job{
		Name: "treaty.expire",
		Run: func(ctx context.Context) error {
			caducados, err := expirer.ExpireDue(ctx, now, tick)
			if err != nil {
				return err
			}
			for _, t := range caducados {
				log.Info("tratado caducado",
					"treaty_id", t.ID, "type", string(t.Type),
					"player_a", t.PlayerA, "player_b", t.PlayerB)
			}
			return nil
		},
	})
}
