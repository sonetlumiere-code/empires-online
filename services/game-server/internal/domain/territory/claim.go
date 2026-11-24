package territory

import (
	"errors"
	"strconv"

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
	// ErrNotClaimable: el dueño no es un jugador ni un clan (FACTION), que nada
	// escribe.
	ErrNotClaimable = errors.New("ese territorio no se puede reclamar")
	// ErrClanTerritory: el territorio ya es del clan de quien lo reclama
	// (SPEC-CLAN-TERRITORIES RN-CLANTERR-001).
	ErrClanTerritory = errors.New("ese territorio ya es de tu clan")
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
	// OwnerPlayerID es el dueño del territorio al empezar, si era un jugador;
	// nil si era libre o de un clan.
	OwnerPlayerID *uuid.UUID
	// ClanID es el clan para el que se reclama; 0 si es para el propio jugador
	// (SPEC-CLAN-TERRITORIES §4.2, ADR-041 §1).
	ClanID        int64
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

// ClanOwner devuelve el clan dueño del control, si lo es (ADR-041 §1): el
// owner_id de un dueño CLAN es el id del clan en decimal.
func (c Control) ClanOwner() (int64, bool) {
	if c.OwnerType != OwnerClan || c.OwnerID == nil {
		return 0, false
	}
	id, err := strconv.ParseInt(*c.OwnerID, 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

// ClanOwnerID es el owner_id de un dueño CLAN.
func ClanOwnerID(clanID int64) string { return strconv.FormatInt(clanID, 10) }

// IsCapital indica si el territorio contiene el centro de alguna de las
// ciudades de su dueño (RN-CONQ-008): desde E34, una o dos (SPEC-MULTI-CITY).
func IsCapital(t Territory, ownerCityCenters []world.Tile) bool {
	for _, c := range ownerCityCenters {
		if t.Contains(c.X, c.Y) {
			return true
		}
	}
	return false
}

// CheckClaimable es la parte de RN-CONQ-002 que sólo depende del territorio:
// no es ya de quien reclama ni de su clan, y no es la capital de su dueño. Lo
// demás —el tile, la reclamación activa, la paz y la protección— lo comprueba la
// simulación. claimerClan es el clan de quien reclama, o 0
// (SPEC-CLAN-TERRITORIES RN-CLANTERR-001).
func CheckClaimable(t Territory, c Control, claimer uuid.UUID, claimerClan int64, ownerCityCenters []world.Tile) error {
	switch c.OwnerType {
	case OwnerNone, "":
		return nil
	case OwnerPlayer:
	case OwnerClan:
		owner, ok := c.ClanOwner()
		if !ok {
			return ErrNotClaimable
		}
		if claimerClan != 0 && owner == claimerClan {
			return ErrClanTerritory
		}
		// Un clan no tiene ciudad: su territorio no es la capital de nadie.
		return nil
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
// de ninguna de sus ciudades en pie: los conquistados. Ninguna capital
// territorial cuenta (SPEC-TERRITORY-CONQUEST Q1, SPEC-MULTI-CITY RN-CITY-034).
func CountConquered(ts []Territory, controls map[int64]Control, playerID uuid.UUID, cityCenters []world.Tile) int {
	n := 0
	for _, t := range ts {
		c, ok := controls[t.ID]
		if !ok {
			continue
		}
		if owner, ok := c.PlayerOwner(); !ok || owner != playerID {
			continue
		}
		if IsCapital(t, cityCenters) {
			continue
		}
		n++
	}
	return n
}

// CaptureBonus es lo que la captura de t cambia el límite de la capital de un
// jugador con sus ciudades en cityCenters: el bono si t no contiene ninguno de
// esos centros, cero si es una de sus capitales territoriales (ADR-034 §6,
// SPEC-MULTI-CITY RN-CITY-034: el bono sólo lo cobra la capital).
func CaptureBonus(t Territory, cityCenters []world.Tile) int32 {
	if IsCapital(t, cityCenters) {
		return 0
	}
	return PopulationBonus
}
