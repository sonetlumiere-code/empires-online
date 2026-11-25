// Package city modela las ciudades y su estado de presencia/protección.
//
// La presencia de la CIUDAD es estado durable en PostgreSQL y la decide siempre el
// servidor. No debe confundirse con la presencia del JUGADOR, que es estado caliente
// en Redis con TTL. Ver ../../../../../docs/specs/presence.md
package city

import (
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
)

// PresenceState es el estado de presencia de una ciudad.
type PresenceState string

const (
	// PresenceOnline: su dueño tiene una sesión viva.
	PresenceOnline PresenceState = "ONLINE"
	// PresenceOfflinePending: el dueño se desconectó y corre el cooldown de seguridad.
	PresenceOfflinePending PresenceState = "OFFLINE_PENDING"
	// PresenceProtected: venció el cooldown; la ciudad está protegida.
	PresenceProtected PresenceState = "PROTECTED"
)

// Valid indica si el estado pertenece al conjunto conocido.
func (p PresenceState) Valid() bool {
	switch p {
	case PresenceOnline, PresenceOfflinePending, PresenceProtected:
		return true
	}
	return false
}

// Era identifica una era tecnológica. El límite de población depende de ella y se
// lee de la tabla `eras`: jamás está hardcodeado en la lógica de juego.
type Era string

const (
	EraStone  Era = "STONE_AGE"
	EraBronze Era = "BRONZE_AGE"
	EraIron   Era = "IRON_AGE"
	EraCastle Era = "CASTLE_AGE"
)

// eraOrder es el orden de las eras: el mismo que `eras.ordinal`. El límite de
// población sigue en la tabla; aquí sólo está cuál va antes.
var eraOrder = []Era{EraStone, EraBronze, EraIron, EraCastle}

// AtLeast indica si la era es `other` o una posterior. Una era desconocida no
// alcanza ninguna.
func (e Era) AtLeast(other Era) bool {
	ie, io := -1, -1
	for i, x := range eraOrder {
		if x == e {
			ie = i
		}
		if x == other {
			io = i
		}
	}
	return ie >= 0 && io >= 0 && ie >= io
}

// EraDefinition es una fila del catálogo de eras.
type EraDefinition struct {
	Code          Era
	Name          string
	Ordinal       int32
	PopulationCap int32
}

// City es una ciudad del mundo.
type City struct {
	ID              int64
	OwnerPlayerID   uuid.UUID
	Name            string
	CenterX         int32
	CenterY         int32
	Era             Era
	Population      int32
	PopulationLimit int32
	PresenceState   PresenceState
	// Civilization es el código de la civilización de su dueño
	// (SPEC-CIVILIZATION). No es una columna de cities: la carga la lee de
	// players, y no cambia nunca (INV-PLAYER-011). Vacía sólo en los tests que
	// no la fijan, y entonces sus rasgos son neutros.
	Civilization string
	// Faction es el código de la facción efectiva de su dueño, y FactionJoin su
	// entrada confirmada en un bando, o nil (SPEC-FACTIONS, ADR-039 §1). Como
	// la civilización, no son columnas de cities: la carga las lee de players.
	// Faction vacía, sólo en los tests que no la fijan, es NEUTRAL.
	Faction     string
	FactionJoin *FactionJoin
	// Clan es el clan de su dueño, o nil (SPEC-CLANS, ADR-040 §1). Tampoco es
	// una columna de cities: la carga lo deriva de clan_members.
	Clan            *ClanTag
	LastOnlineAt    *time.Time
	LastOfflineAt   *time.Time
	ProtectionUntil *time.Time
	Version         int32
	// FoundedAtMs es el instante de su fundación (cities.created_at), del que
	// sale su inmunidad ante un asalto (SPEC-CITY-FALL RN-CITY-029). Una
	// ciudad refundada es una fila nueva y estrena el suyo.
	FoundedAtMs int64
	// Slot es su hueco entre las ciudades en pie de su dueño, 1 o 2
	// (SPEC-MULTI-CITY, ADR-051 §2): la base no deja dos en pie en el mismo.
	// Cero vale 1 al insertar.
	Slot int16
}

// MaxStandingPerOwner es el techo de ciudades en pie por jugador
// (SPEC-MORE-CITIES RN-CITY-040, INV-CITY-022): el mismo que el CHECK de
// cities.slot. El tope de cada jugador es el de la era de su capital.
const MaxStandingPerOwner = 4

// MaxStandingFor es el tope de ciudades en pie de un jugador cuya capital está
// en la era e (SPEC-MORE-CITIES RN-CITY-040): 1 en Piedra, 2 en Bronce, 3 en
// Hierro y 4 en Castillos.
func MaxStandingFor(e Era) int {
	switch e {
	case EraBronze:
		return 2
	case EraIron:
		return 3
	case EraCastle:
		return 4
	default:
		return 1
	}
}

// FreeSlot es el menor hueco entre 1 y MaxStandingPerOwner que no está en used,
// o 0 si no queda ninguno.
func FreeSlot(used []int16) int16 {
	for s := int16(1); s <= MaxStandingPerOwner; s++ {
		libre := true
		for _, u := range used {
			if u == s {
				libre = false
				break
			}
		}
		if libre {
			return s
		}
	}
	return 0
}

// SecondCityName es el nombre de la ciudad que funda un jugador: el de su
// capital seguido de « II» (RN-CITY-032).
func SecondCityName(capital string) string { return capital + " II" }

// FallImmuneUntilMs es el instante en que vence la inmunidad de la ciudad ante
// un asalto (RN-CITY-029): su fundación más immunity.
func (c *City) FallImmuneUntilMs(immunity time.Duration) int64 {
	return c.FoundedAtMs + immunity.Milliseconds()
}

// CityName es el nombre de la n-ésima ciudad que funda un jugador: el de su
// capital con « II», « III» o « IV» (SPEC-MORE-CITIES, precisión de §9.1).
func CityName(capital string, n int) string {
	switch n {
	case 3:
		return capital + " III"
	case 4:
		return capital + " IV"
	default:
		return SecondCityName(capital)
	}
}

// FallImmuneAt dice si la ciudad aún no se puede asaltar en nowMs.
func (c *City) FallImmuneAt(nowMs int64, immunity time.Duration) bool {
	return immunity > 0 && nowMs < c.FallImmuneUntilMs(immunity)
}

// FactionJoin es la entrada de un jugador NEUTRAL en un bando, que cuenta en
// AtMs (SPEC-FACTIONS RN-FAC-002).
type FactionJoin struct {
	Faction string
	AtMs    int64
}

// ClanTag es el clan del dueño de una ciudad: su id y su sigla, públicos
// (SPEC-CLANS RN-CLAN-008).
type ClanTag struct {
	ID  int64
	Tag string
}

// Center devuelve el tile central de la ciudad.
func (c *City) Center() world.Tile { return world.Tile{X: c.CenterX, Y: c.CenterY} }

// HasPopulationRoom indica si caben `n` puntos de población más.
func (c *City) HasPopulationRoom(n int32) bool { return c.Population+n <= c.PopulationLimit }

// IsProtected indica si la ciudad está en el estado PROTECTED, venza o no su
// tope. Para decidir si la protección frena un ataque se usa ProtectedAt.
func (c *City) IsProtected() bool { return c.PresenceState == PresenceProtected }

// ProtectedAt indica si la protección frena un ataque en `now`: la ciudad está
// PROTECTED y su tope no ha vencido (INV-CITY-006; SPEC-COMBAT-RAID Q1). Sin
// tope (ProtectionUntil nil) protege mientras dure el estado.
func (c *City) ProtectedAt(now time.Time) bool {
	if !c.IsProtected() {
		return false
	}
	return c.ProtectionUntil == nil || now.Before(*c.ProtectionUntil)
}

// ProtectionEndsAt es el fin de la protección de una ciudad que se desconectó
// en `lastOfflineAt`: el cooldown más el tope (ADR-024 §3). Se deriva de datos
// durables, así que vale igual al concederla que al rehidratar. Con tope cero,
// o sin marca de desconexión, no hay fin: devuelve nil.
func ProtectionEndsAt(lastOfflineAt *time.Time, cooldown, max time.Duration) *time.Time {
	if lastOfflineAt == nil || max <= 0 {
		return nil
	}
	end := lastOfflineAt.Add(cooldown + max)
	return &end
}

// Errores de dominio.
var (
	ErrNotFound          = errors.New("ciudad no encontrada")
	ErrProtected         = errors.New("la ciudad está protegida")
	ErrPopulationLimit   = errors.New("se alcanzó el límite de población de la ciudad")
	ErrInvalidTransition = errors.New("transición de presencia no permitida")
)

// CanTransition valida una transición del autómata de presencia.
//
// El autómata completo (INV-CITY-004):
//
//	ONLINE          -> OFFLINE_PENDING            (el dueño se desconecta y vence el grace)
//	OFFLINE_PENDING -> PROTECTED                  (vence el cooldown de seguridad)
//	OFFLINE_PENDING -> ONLINE                     (el dueño reconecta a tiempo)
//	PROTECTED       -> ONLINE                     (el dueño vuelve)
//
// Cualquier otra transición es un bug y debe rechazarse en lugar de aplicarse.
func CanTransition(from, to PresenceState) bool {
	if !from.Valid() || !to.Valid() {
		return false
	}
	if from == to {
		return true // idempotente: reaplicar el estado actual no es un error
	}
	switch from {
	case PresenceOnline:
		return to == PresenceOfflinePending
	case PresenceOfflinePending:
		return to == PresenceProtected || to == PresenceOnline
	case PresenceProtected:
		return to == PresenceOnline
	}
	return false
}

// NextStateOnDisconnect devuelve el estado inmediato tras perder la presencia.
func NextStateOnDisconnect(current PresenceState) PresenceState {
	if current == PresenceOnline {
		return PresenceOfflinePending
	}
	return current
}

// ShouldEngageProtection indica si una ciudad en OFFLINE_PENDING ya cumplió su
// cooldown y debe pasar a PROTECTED.
//
// La comparación usa el reloj inyectado del game loop, nunca time.Now().
func ShouldEngageProtection(state PresenceState, lastOfflineAt *time.Time, cooldown time.Duration, now time.Time) bool {
	if state != PresenceOfflinePending || lastOfflineAt == nil {
		return false
	}
	return !now.Before(lastOfflineAt.Add(cooldown))
}

// UrbanRadius es el radio de la zona urbana amurallada, en tiles.
//
// Con radio 1 el Centro Urbano ocupa un cuadrado de 3×3 alrededor del centro.
// Vive aquí y no en `founding` porque no es una regla de la fundación sino una
// propiedad de la ciudad: la usan también la ocupación del mundo y las
// condiciones de entrada en guarnición, y tenerla en tres sitios garantizaba que
// un día dejaran de coincidir.
const UrbanRadius int32 = 1

// UrbanBounds devuelve el rectángulo, inclusivo, que ocupa la zona urbana.
func (c *City) UrbanBounds() (minX, minY, maxX, maxY int32) {
	return c.CenterX - UrbanRadius, c.CenterY - UrbanRadius,
		c.CenterX + UrbanRadius, c.CenterY + UrbanRadius
}

// Occupies indica si el tile cae dentro de la zona urbana.
func (c *City) Occupies(x, y int32) bool {
	minX, minY, maxX, maxY := c.UrbanBounds()
	return x >= minX && x <= maxX && y >= minY && y <= maxY
}

// SpawnRadius es la distancia Chebyshev al centro del anillo de nacimiento:
// justo fuera de la muralla, adyacente a la zona urbana.
const SpawnRadius int32 = 2

// spawnOffsets es el orden fijo del anillo de nacimiento. No depende de ningún
// mapa: dos ejecuciones eligen siempre el mismo tile.
var spawnOffsets = [...]world.Tile{
	{X: SpawnRadius, Y: 0}, {X: -SpawnRadius, Y: 0},
	{X: 0, Y: SpawnRadius}, {X: 0, Y: -SpawnRadius},
	{X: SpawnRadius, Y: SpawnRadius}, {X: -SpawnRadius, Y: -SpawnRadius},
	{X: SpawnRadius, Y: -SpawnRadius}, {X: -SpawnRadius, Y: SpawnRadius},
}

// SpawnTilesAround devuelve, en su orden fijo, los ocho tiles del anillo de
// nacimiento de un centro. Es el criterio con el que nacen los aldeanos
// iniciales (founding), el tile de depósito de la madera (RN-ECON-004) y el
// tile en el que nace un aldeano entrenado (RN-ECON-011): un solo sitio para
// que los tres no puedan dejar de coincidir.
func SpawnTilesAround(center world.Tile) []world.Tile {
	out := make([]world.Tile, len(spawnOffsets))
	for i, off := range spawnOffsets {
		out[i] = world.Tile{X: center.X + off.X, Y: center.Y + off.Y}
	}
	return out
}

// FirstSpawnTile devuelve el primer tile del anillo de nacimiento que cumple
// `free`, o false si ninguno. En el MVP las unidades no ocupan tile, así que
// «libre» es transitable: terreno y murallas.
func (c *City) FirstSpawnTile(free func(x, y int32) bool) (world.Tile, bool) {
	for _, t := range SpawnTilesAround(c.Center()) {
		if free(t.X, t.Y) {
			return t, true
		}
	}
	return world.Tile{}, false
}

// IsAdjacent indica si el tile toca la zona urbana en cualquiera de las ocho
// direcciones, sin estar dentro de ella.
//
// Es la misma noción de vecindad que usa el movimiento (canon §4): las diagonales
// cuentan. Un tile DENTRO de la zona no es adyacente — además de que la zona está
// bloqueada y ninguna unidad puede estar ahí.
func (c *City) IsAdjacent(x, y int32) bool {
	if c.Occupies(x, y) {
		return false
	}
	minX, minY, maxX, maxY := c.UrbanBounds()
	return x >= minX-1 && x <= maxX+1 && y >= minY-1 && y <= maxY+1
}
