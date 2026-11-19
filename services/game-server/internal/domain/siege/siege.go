// Package siege decide qué unidad puede dañar qué edificio de una ciudad, según
// la ciudad esté amurallada o en brecha (SPEC-SIEGE-ENGINES §4.3, ADR-048 §2).
//
// Es la única regla: la llaman la orden unit.demolish y cada golpe de asedio de
// la fase 4, de modo que una muralla que se completa a mitad de un derribo lo
// detiene sin que nadie más tenga que acordarse (RN-SIEGE-006, INV-COMBAT-008).
package siege

import (
	"errors"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/building"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
)

// Motivos por los que un golpe de asedio no se admite. Los dos son
// INVALID_TARGET en el protocolo.
var (
	// ErrCannotDemolish: la unidad no derriba edificios.
	ErrCannotDemolish = errors.New("esta unidad no derriba edificios")
	// ErrWallStanding: la ciudad está amurallada y la unidad no es una máquina
	// de asedio.
	ErrWallStanding = errors.New("la muralla está en pie: sólo un ariete la derriba")
	// ErrWallFirst: la ciudad está amurallada y el objetivo no es la muralla.
	ErrWallFirst = errors.New("primero, la muralla")
)

// Check aplica RN-SIEGE-004: contra una ciudad amurallada sólo una máquina de
// asedio golpea, y sólo a la muralla; contra una en brecha, quien derriba
// golpea cualquier edificio.
func Check(attacker unit.Definition, target building.Type, walled bool) error {
	if !attacker.CanDemolish() {
		return ErrCannotDemolish
	}
	if !walled {
		return nil
	}
	if !attacker.IsSiegeEngine() {
		return ErrWallStanding
	}
	if target != building.TypeWall {
		return ErrWallFirst
	}
	return nil
}

// CanDamage dice si Check admite el golpe.
func CanDamage(attacker unit.Definition, target building.Type, walled bool) bool {
	return Check(attacker, target, walled) == nil
}
