// Package clan contiene las reglas de los clanes (SPEC-CLANS, ADR-040): el
// formato del nombre y la sigla, los rangos, quién hereda el liderazgo, qué
// bandos caben en un clan y quién sigue en paz tras una salida. Todo son
// funciones puras: la simulación guarda el estado y decide cuándo aplicarlas.
package clan

import (
	"errors"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/player"
)

// Longitud del nombre, en caracteres (SPEC-CLANS §4.1).
const (
	NameMinChars = 3
	NameMaxChars = 24
)

// Valores por defecto de la configuración (SPEC-CLANS §10).
const (
	// DefaultMaxMembers es el tope de miembros (Q5).
	DefaultMaxMembers = 20
	// DefaultInviteTTL es la vida de una invitación (Q3).
	DefaultInviteTTL = 24 * time.Hour
	// DefaultPeaceGrace es la paz residual tras una salida (Q6).
	DefaultPeaceGrace = 15 * time.Minute
	// DefaultLeaderInactive es lo que tiene que llevar el líder sin conectarse
	// para que el liderazgo pase a quien sigue jugando (SPEC-CLAN-GOVERNANCE
	// Q6).
	DefaultLeaderInactive = 7 * 24 * time.Hour
)

// Rangos de un miembro (Q4; SPEC-CLAN-GOVERNANCE Q1). La base guarda el de
// oficial y el de miembro en clan_members.rank; el líder es el de
// clans.leader_player_id.
const (
	RoleLeader  = "LEADER"
	RoleOfficer = "OFFICER"
	RoleMember  = "MEMBER"
)

// CanInvite dice si un rango invita y retira invitaciones (RN-CLANGOV-002).
func CanInvite(role string) bool { return role == RoleLeader || role == RoleOfficer }

// CanExpel dice si un rango expulsa a otro (RN-CLANGOV-002): el líder, a
// cualquiera menos a sí mismo; un oficial, sólo a un miembro raso. Al líder no
// lo expulsa nadie.
func CanExpel(by, target string) bool {
	switch by {
	case RoleLeader:
		return target != RoleLeader
	case RoleOfficer:
		return target == RoleMember
	}
	return false
}

// CanGovern dice si un rango nombra o quita oficiales y cede el liderazgo
// (RN-CLANGOV-003): sólo el líder.
func CanGovern(role string) bool { return role == RoleLeader }

// Motivos de una salida, como los guarda clan_members.left_reason.
const (
	ReasonLeft     = "LEFT"
	ReasonExpelled = "EXPELLED"
)

// Errores del formato. Su texto es el que ve el jugador.
var (
	ErrInvalidName = errors.New("el nombre del clan tiene de 3 a 24 letras, dígitos, espacios o guiones, sin espacios en los extremos ni dobles")
	ErrInvalidTag  = errors.New("la sigla del clan tiene de 2 a 4 letras mayúsculas o dígitos")
)

// ValidateName comprueba el nombre tal como llega (RN-CLAN-002): de 3 a 24
// caracteres, letras, dígitos, espacios y guiones, sin espacios en los extremos
// ni dos seguidos. No lo normaliza: un nombre con espacios de sobra se rechaza,
// para que el jugador vea exactamente el nombre que tendrá.
func ValidateName(name string) error {
	if !utf8.ValidString(name) {
		return ErrInvalidName
	}
	n := utf8.RuneCountInString(name)
	if n < NameMinChars || n > NameMaxChars {
		return ErrInvalidName
	}
	if strings.HasPrefix(name, " ") || strings.HasSuffix(name, " ") || strings.Contains(name, "  ") {
		return ErrInvalidName
	}
	for _, r := range name {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != ' ' && r != '-' {
			return ErrInvalidName
		}
	}
	return nil
}

// ValidateTag comprueba la sigla: de 2 a 4 caracteres de A–Z y 0–9.
func ValidateTag(tag string) error {
	if len(tag) < 2 || len(tag) > 4 {
		return ErrInvalidTag
	}
	for i := 0; i < len(tag); i++ {
		c := tag[i]
		if (c < 'A' || c > 'Z') && (c < '0' || c > '9') {
			return ErrInvalidTag
		}
	}
	return nil
}

// NameKey es la clave de unicidad del nombre: sin distinguir mayúsculas
// (RN-CLAN-002). La base usa lower(name); esto es lo mismo para la RAM.
func NameKey(name string) string { return strings.ToLower(name) }

// Member es un miembro de un clan, el instante en que entró y si es oficial.
type Member struct {
	PlayerID   uuid.UUID
	JoinedAtMs int64
	Officer    bool
}

// NextLeader es quien hereda el liderazgo al salir el líder (RN-CLAN-007,
// RN-CLANGOV-004): el oficial más antiguo y, si no hay oficiales, el miembro
// más antiguo; a igualdad de entrada, el de menor id de jugador. La antigüedad
// es la de entrada en el clan, no la del nombramiento. ok es falso si no queda
// nadie: el clan se disuelve.
func NextLeader(remaining []Member) (uuid.UUID, bool) {
	return firstOf(remaining, func(Member) bool { return true })
}

// Successor es quien recibe el liderazgo de un líder ausente
// (RN-CLANGOV-005): el oficial activo más antiguo y, si no hay ninguno, el
// miembro activo más antiguo. active dice quién sigue jugando. ok es falso si
// nadie está activo: el liderazgo no cambia.
func Successor(others []Member, active func(uuid.UUID) bool) (uuid.UUID, bool) {
	return firstOf(others, func(m Member) bool { return active(m.PlayerID) })
}

// firstOf es el primer oficial que cumple ok, por antigüedad, o si no hay
// ninguno el primer miembro que lo cumple.
func firstOf(ms []Member, ok func(Member) bool) (uuid.UUID, bool) {
	sorted := append([]Member(nil), ms...)
	SortMembers(sorted)
	for _, officers := range []bool{true, false} {
		for _, m := range sorted {
			if m.Officer == officers && ok(m) {
				return m.PlayerID, true
			}
		}
	}
	return uuid.Nil, false
}

// Absent dice si el líder está ausente (RN-CLANGOV-005): su ciudad no está
// ONLINE y su marca de desconexión tiene threshold o más. Sin marca
// (lastOfflineAtMs 0) no está ausente: la carga la fecha (INV-CITY-011).
func Absent(online bool, lastOfflineAtMs, nowMs int64, threshold time.Duration) bool {
	return !online && lastOfflineAtMs > 0 && nowMs-lastOfflineAtMs >= threshold.Milliseconds()
}

// SortMembers ordena los miembros por antigüedad y, a igualdad, por id de
// jugador: el orden de la sucesión y el de las vistas.
func SortMembers(ms []Member) {
	sort.Slice(ms, func(i, j int) bool {
		if ms[i].JoinedAtMs != ms[j].JoinedAtMs {
			return ms[i].JoinedAtMs < ms[j].JoinedAtMs
		}
		return ms[i].PlayerID.String() < ms[j].PlayerID.String()
	})
}

// Stance es el bando de un miembro para la regla de bandos: su facción efectiva
// y la de su entrada pendiente, si la tiene (vacía si no).
type Stance struct {
	Faction string
	Joining string
}

// sides devuelve los bandos (ORDER, CHAOS) que cuenta una postura.
func (s Stance) sides() (order, chaos bool) {
	for _, f := range []string{player.EffectiveFaction(s.Faction), s.Joining} {
		switch f {
		case player.FactionOrder:
			order = true
		case player.FactionChaos:
			chaos = true
		}
	}
	return order, chaos
}

// Mixes dice si un conjunto de posturas junta a la Orden y al Caos
// (RN-CLAN-006, INV-CLAN-002), contando las entradas pendientes.
func Mixes(stances ...Stance) bool {
	var order, chaos bool
	for _, s := range stances {
		o, c := s.sides()
		order = order || o
		chaos = chaos || c
	}
	return order && chaos
}

// OpposedSide es el bando que impediría entrar en un clan con esas posturas, o
// vacío si cabe cualquiera: ORDER si el clan ya cuenta al Caos, y al revés.
// Sirve para el motivo del rechazo.
func OpposedSide(stances ...Stance) string {
	var order, chaos bool
	for _, s := range stances {
		o, c := s.sides()
		order = order || o
		chaos = chaos || c
	}
	switch {
	case order && !chaos:
		return player.FactionChaos
	case chaos && !order:
		return player.FactionOrder
	}
	return ""
}

// Stay es una estancia de un jugador en un clan: cuándo entró y, si ya salió,
// cuándo (LeftAtMs 0 mientras sigue).
type Stay struct {
	ClanID     int64
	PlayerID   uuid.UUID
	JoinedAtMs int64
	LeftAtMs   int64
	// Officer: la estancia (abierta) tiene rango de oficial
	// (SPEC-CLAN-GOVERNANCE).
	Officer bool
}

// Open dice si la estancia sigue abierta.
func (s Stay) Open() bool { return s.LeftAtMs == 0 }

// coveredAt dice si la estancia estaba abierta en el instante atMs.
func (s Stay) coveredAt(atMs int64) bool {
	return s.JoinedAtMs <= atMs && (s.Open() || s.LeftAtMs >= atMs)
}

// ResidualPeace dice si dos jugadores siguen en paz por una salida reciente
// (RN-CLAN-005, ADR-040 §3): uno de ellos cerró una estancia en un clan hace
// menos de grace y el otro tenía, en el instante de esa salida, una estancia
// abierta en el mismo clan. stays son las estancias de los dos, abiertas y
// cerradas recientes; nowMs es el instante de la consulta.
func ResidualPeace(x, y uuid.UUID, stays []Stay, nowMs int64, grace time.Duration) bool {
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
			if o.PlayerID == other && o.ClanID == d.ClanID && o.coveredAt(d.LeftAtMs) {
				return true
			}
		}
	}
	return false
}

// Clan es un clan vivo tal como lo carga el arranque.
type Clan struct {
	ID       int64
	Name     string
	Tag      string
	LeaderID uuid.UUID
}

// Invite es una invitación viva: el clan, el invitado, quién invitó y cuándo
// caduca.
type Invite struct {
	ClanID      int64
	PlayerID    uuid.UUID
	InvitedBy   uuid.UUID
	ExpiresAtMs int64
}

// Errores del canal y de la vista pública. Su texto es el que ve el jugador.
var (
	ErrNotMember   = errors.New("no estás en ningún clan")
	ErrUnknownClan = errors.New("ese clan no existe")
)

// Sent es un mensaje del canal recién guardado (o el que ya existía con ese
// requestId), con los miembros abiertos a los que se entrega.
type Sent struct {
	ID           int64
	ClanID       int64
	From         uuid.UUID
	FromCityID   int64
	FromCityName string
	Body         string
	SentAtMs     int64
	// Members son los miembros abiertos al confirmar el envío.
	Members   []uuid.UUID
	Duplicate bool
}
