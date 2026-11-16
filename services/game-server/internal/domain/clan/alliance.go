package clan

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/diplomacy"
)

// Las alianzas entre clanes (SPEC-CLAN-ALLIANCES, ADR-042).
//
// Una alianza es una relación entre dos clanes, no entre jugadores: tiene los
// estados y las transiciones de un tratado (diplomacy.Status) y da paz a todos
// los miembros de los dos mientras está ACTIVE, también durante su preaviso.

// DefaultMaxAllies es el tope de alianzas activas de un clan
// (EO_CLAN_MAX_ALLIES, Q4).
const DefaultMaxAllies = 2

// Motivos de un preaviso de ruptura (SPEC-CLAN-ALLIANCES §6).
const (
	// BreakReasonCancel: la rompió el líder de uno de los dos clanes.
	BreakReasonCancel = "CANCEL"
	// BreakReasonFactions: la mezcla de la Orden y el Caos apareció con la
	// alianza activa (RN-CLANALLY-005).
	BreakReasonFactions = "FACTIONS"
)

// ErrAllianceTransition: el salto de estado no está permitido.
var ErrAllianceTransition = errors.New("transición de alianza no permitida")

// Alliance es una alianza entre dos clanes. ClanA < ClanB siempre (el par
// canónico). Los instantes son milisegundos de la simulación; cero si no
// aplican.
type Alliance struct {
	ID    int64
	ClanA int64
	ClanB int64
	// ProposedByClan es el clan que propuso y ProposedBy el líder que lo hizo.
	ProposedByClan int64
	ProposedBy     uuid.UUID
	Status         diplomacy.Status
	RequestID      string
	CreatedAtMs    int64
	AcceptedAtMs   int64
	// BreakEffectiveAtMs es el fin del preaviso, con su motivo; cero sin
	// preaviso.
	BreakEffectiveAtMs int64
	BreakReason        string
	ClosedAtMs         int64
}

// CanonicalClans ordena un par de clanes.
func CanonicalClans(x, y int64) (int64, int64) {
	if x < y {
		return x, y
	}
	return y, x
}

// Pair es el par canónico de la alianza.
func (a Alliance) Pair() [2]int64 { return [2]int64{a.ClanA, a.ClanB} }

// Has dice si el clan es una de las dos partes.
func (a Alliance) Has(clanID int64) bool { return a.ClanA == clanID || a.ClanB == clanID }

// Other es la otra parte; cero si clanID no es parte.
func (a Alliance) Other(clanID int64) int64 {
	switch clanID {
	case a.ClanA:
		return a.ClanB
	case a.ClanB:
		return a.ClanA
	}
	return 0
}

// Open dice si la alianza sigue abierta: una oferta o una alianza activa.
func (a Alliance) Open() bool {
	return a.Status == diplomacy.StatusProposed || a.Status == diplomacy.StatusActive
}

// Active dice si la alianza da paz ahora: ACTIVE, también durante su preaviso
// (RN-CLANALLY-003).
func (a Alliance) Active() bool { return a.Status == diplomacy.StatusActive }

// ActiveAt dice si la alianza estaba activa en el instante atMs: aceptada
// antes o en ese instante y no cerrada antes. Es lo que mira la paz residual
// con los aliados (ADR-042 §2): el último en salir de un clan que se disuelve
// sale en el mismo instante en que expira la alianza, y su paz residual cuenta
// (SPEC-CLAN-ALLIANCES §4.3).
func (a Alliance) ActiveAt(atMs int64) bool {
	return a.AcceptedAtMs > 0 && a.AcceptedAtMs <= atMs && (a.ClosedAtMs == 0 || a.ClosedAtMs >= atMs)
}

// InNotice dice si la alianza está en preaviso de ruptura.
func (a Alliance) InNotice() bool {
	return a.Status == diplomacy.StatusActive && a.BreakEffectiveAtMs > 0
}

// TransitionTo aplica un cambio de estado en atMs y sella el instante que le
// toca. Devuelve una copia.
func (a Alliance) TransitionTo(next diplomacy.Status, atMs int64) (Alliance, error) {
	if !diplomacy.AllowedTransition(a.Status, next) {
		return a, fmt.Errorf("%w: %s → %s", ErrAllianceTransition, a.Status, next)
	}
	a.Status = next
	switch next {
	case diplomacy.StatusActive:
		a.AcceptedAtMs = atMs
	default:
		a.ClosedAtMs = atMs
	}
	return a, nil
}

// AllyResidualPeace dice si dos jugadores siguen en paz porque uno salió hace
// poco de un clan aliado del clan del otro (RN-CLANALLY-006, ADR-042 §2): uno
// cerró una estancia en un clan A hace menos de grace, el otro tenía en el
// instante de esa salida una estancia abierta en otro clan B, y A y B tenían
// en ese instante una alianza activa. stays son las estancias de los dos,
// abiertas y cerradas recientes; alliances, las alianzas abiertas y las
// cerradas recientes que tocan a sus clanes.
func AllyResidualPeace(x, y uuid.UUID, stays []Stay, alliances []Alliance, nowMs int64, grace time.Duration) bool {
	if len(alliances) == 0 {
		return false
	}
	graceMs := grace.Milliseconds()
	for _, d := range stays {
		if d.Open() || nowMs-d.LeftAtMs >= graceMs {
			continue
		}
		var other uuid.UUID
		switch d.PlayerID {
		case x:
			other = y
		case y:
			other = x
		default:
			continue
		}
		for _, o := range stays {
			if o.PlayerID != other || o.ClanID == d.ClanID || !o.coveredAt(d.LeftAtMs) {
				continue
			}
			a, b := CanonicalClans(d.ClanID, o.ClanID)
			for _, al := range alliances {
				if al.ClanA == a && al.ClanB == b && al.ActiveAt(d.LeftAtMs) {
					return true
				}
			}
		}
	}
	return false
}
