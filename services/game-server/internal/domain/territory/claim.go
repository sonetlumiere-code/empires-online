package territory

import (
	"errors"

	"github.com/google/uuid"

	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
)

// La conquista (SPEC-TERRITORY-CONQUEST, ADR-034): un guerrero reclama el
// territorio en que está y, si aguanta el plazo, el territorio pasa a su dueño.
// Aquí sólo lo que es función de sus argumentos: qué territorio se puede
// reclamar, qué es una capital y cuánto suma al límite lo conquistado.

// EventClaimStarted es el `event_type` del comienzo de una reclamación sobre un
// territorio con dueño: player_id es el dueño y counterpart_player_id quien
// reclama (SPEC-TERRITORY-CONQUEST §6).
const EventClaimStarted = "TerritoryClaimStarted"

// PopulationBonus es lo que suma al límite de población de la ciudad de su
// dueño cada territorio conquistado (SPEC-TERRITORY-CONQUEST §4.2, Q1).
const PopulationBonus int32 = 5

var (
	// ErrOwnTerritory: el territorio ya es de quien lo reclama.
	ErrOwnTerritory = errors.New("ese territorio ya es tuyo")
	// ErrCapital: el territorio contiene el centro de la ciudad de su dueño
	// (RN-CONQ-008, INV-TERR-011).
	ErrCapital = errors.New("es la capital de su dueño: no se puede reclamar")
	// ErrNotClaimable: el dueño no es un jugador (CLAN, FACTION), que el MVP no
	// escribe nunca.
	ErrNotClaimable = errors.New("ese territorio no se puede reclamar")
)

// ClaimStatus es el estado de una fila de territory_claims.
type ClaimStatus string

const (
	ClaimActive    ClaimStatus = "ACTIVE"
	ClaimCompleted ClaimStatus = "COMPLETED"
	ClaimFailed    ClaimStatus = "FAILED"
)

// Claim es una reclamación activa (ADR-034 §1): el guerrero, el tile en que se
// plantó, quién era el dueño al empezar y cuándo vence.
type Claim struct {
	ID          int64
	TerritoryID int64
	UnitID      int64
	PlayerID    uuid.UUID
	CityID      int64
	// OwnerPlayerID es el dueño del territorio al empezar; nil si era libre.
	OwnerPlayerID *uuid.UUID
	X, Y          int32
	StartedAtMs   int64
	CompletesAtMs int64
	RequestID     string
}

// Tile es el tile en que se plantó el guerrero.
func (c Claim) Tile() world.Tile { return world.Tile{X: c.X, Y: c.Y} }

// Due indica si el plazo se cumplió.
func (c Claim) Due(nowMs int64) bool { return nowMs >= c.CompletesAtMs }

// PlayerOwner devuelve el jugador dueño del control, si lo es.
func (c Control) PlayerOwner() (uuid.UUID, bool) {
	if c.OwnerType != OwnerPlayer || c.OwnerID == nil {
		return uuid.Nil, false
	}
	id, err := uuid.Parse(*c.OwnerID)
	if err != nil {
		return uuid.Nil, false
	}
	return id, true
}

// IsCapital indica si el territorio contiene el centro de alguna de las
// ciudades de su dueño (RN-CONQ-008). En el MVP cada jugador tiene una.
func IsCapital(t Territory, ownerCityCenters []world.Tile) bool {
	for _, c := range ownerCityCenters {
		if t.Contains(c.X, c.Y) {
			return true
		}
	}
	return false
}

// CheckClaimable es la parte de RN-CONQ-002 que sólo depende del territorio:
// no es ya de quien reclama y no es la capital de su dueño. Lo demás —el tile,
// la reclamación activa, la paz y la protección— lo comprueba la simulación.
func CheckClaimable(t Territory, c Control, claimer uuid.UUID, ownerCityCenters []world.Tile) error {
	switch c.OwnerType {
	case OwnerNone, "":
		return nil
	case OwnerPlayer:
	default:
		return ErrNotClaimable
	}
	owner, ok := c.PlayerOwner()
	if !ok {
		return ErrNotClaimable
	}
	if owner == claimer {
		return ErrOwnTerritory
	}
	if IsCapital(t, ownerCityCenters) {
		return ErrCapital
	}
	return nil
}

// ConqueredBonus es el término de los territorios en el límite de población
// (INV-ECON-004): 5 por territorio conquistado.
func ConqueredBonus(n int) int32 { return PopulationBonus * int32(n) }

// CountConquered cuenta los territorios de un jugador que no contienen el centro
// de su ciudad: los conquistados. La capital no cuenta (SPEC-TERRITORY-CONQUEST
// Q1).
func CountConquered(ts []Territory, controls map[int64]Control, playerID uuid.UUID, cityCenter world.Tile) int {
	n := 0
	for _, t := range ts {
		c, ok := controls[t.ID]
		if !ok {
			continue
		}
		if owner, ok := c.PlayerOwner(); !ok || owner != playerID {
			continue
		}
		if t.Contains(cityCenter.X, cityCenter.Y) {
			continue
		}
		n++
	}
	return n
}

// CaptureBonus es lo que la captura de t cambia el límite de una ciudad con el
// centro en cityCenter: el bono si t no contiene ese centro, cero si es su
// capital (ADR-034 §6).
func CaptureBonus(t Territory, cityCenter world.Tile) int32 {
	if t.Contains(cityCenter.X, cityCenter.Y) {
		return 0
	}
	return PopulationBonus
}
