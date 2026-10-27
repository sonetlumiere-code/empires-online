package simulation

import (
	"github.com/google/uuid"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// Cruce de frontera de chunk (docs/architecture/networking.md §4.3).
//
// El interest management suscribe por chunk (INV-WORLD-006), así que una unidad
// que pasa del chunk A al chunk B entra en el área de interés de las sesiones
// que observan B y no A, y sale de la de las que observan A y no B. Sin
// anunciarlo, las primeras recibirían entity.update de una entidad que no
// conocen —y lo descartan— y las segundas se quedarían con un fantasma hasta su
// siguiente snapshot.

// moveUnit es el único camino por el que la simulación cambia de tile a una
// unidad del mundo vivo. Reindexa el chunk (State.MoveUnitTo) y, si cambió,
// anuncia la transición ANTES de que el llamante emita su propio delta, para
// que una sesión que gana la unidad reciba primero el entity.spawn —con la
// vista completa, movimiento activo incluido— y después el entity.update,
// nunca al revés. La que la pierde recibe entity.despawn con OUT_OF_INTEREST:
// la unidad sigue existiendo, sólo ha dejado de estar a la vista.
//
// `prev` es el estado que tenía la unidad antes de la operación que la mueve, y
// se llama con el estado ya definitivo. La transición sólo va a quienes la ven
// antes Y después: los que la ganan o la pierden por un cambio de estado ya
// reciben el spawn o el despawn de ese cambio. El propietario siempre la ve;
// los terceros, sólo si en ningún momento estuvo oculta ni guarnecida
// (INV-SAFE-004, INV-GARR-006). Por eso el cruce de una unidad HIDDEN no la
// revela a nadie, y el de una que sale de guarnición no duplica el spawn que la
// salida ya emite a los terceros.
//
// Los payloads se construyen aquí, por valor: nada de lo que cruza hacia el
// Broadcaster es un puntero al mundo (docs/architecture/scalability.md §10).
func (s *Simulation) moveUnit(u *unit.Unit, to world.Tile, prev unit.Status, nowMs int64) {
	w := s.state.World()
	fromCX, fromCY := w.ChunkOf(u.X, u.Y)
	if !s.state.MoveUnitTo(u, to) {
		return
	}
	toCX, toCY := w.ChunkOf(u.X, u.Y)

	onlyPlayer := uuid.Nil
	if !visibleToThirdParties(prev) || !visibleToThirdParties(u.Status) {
		onlyPlayer = u.PlayerID
	}
	s.deps.Broadcaster.BroadcastChunkTransition(
		world.ChunkCoord{CX: fromCX, CY: fromCY}, world.ChunkCoord{CX: toCX, CY: toCY}, onlyPlayer,
		protocol.TypeEntitySpawn, protocol.EntitySpawnPayload{Unit: s.UnitView(u, nowMs)},
		protocol.TypeEntityDespawn, protocol.EntityDespawnPayload{ID: u.ID, Reason: protocol.DespawnOutOfInterest},
	)
}
