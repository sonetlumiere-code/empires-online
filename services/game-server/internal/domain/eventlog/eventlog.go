// Package eventlog traduce los eventos del mundo al registro de un jugador
// (SPEC-EVENT-LOG, ADR-028 §4 y §5): qué fila da una entrada para quién, cómo se
// agrupan y dónde se corta la página. No tiene estado ni toca la base.
package eventlog

import (
	"time"

	"github.com/google/uuid"
)

// Kind es el tipo de una entrada, siempre desde el punto de vista de quien la
// recibe.
type Kind string

const (
	// KindUnitLost es una unidad propia muerta.
	KindUnitLost Kind = "UNIT_LOST"
	// KindEnemyKilled es una unidad ajena que mataron las propias.
	KindEnemyKilled Kind = "ENEMY_KILLED"
	// KindPillaged es madera robada del almacén propio.
	KindPillaged Kind = "PILLAGED"
	// KindPillageDone es madera que una unidad propia robó a otro.
	KindPillageDone Kind = "PILLAGE_DONE"
	// KindHouseLost es una casa propia derribada (SPEC-COMBAT-SIEGE).
	KindHouseLost Kind = "HOUSE_LOST"
	// KindHouseDestroyed es una casa ajena que derribaron las unidades propias.
	KindHouseDestroyed Kind = "HOUSE_DESTROYED"
	// KindTreatyOffered es una oferta recibida.
	KindTreatyOffered Kind = "TREATY_OFFERED"
	// KindTreatyWithdrawn es una oferta recibida que el otro retiró.
	KindTreatyWithdrawn Kind = "TREATY_WITHDRAWN"
	// KindTreatyAccepted es una oferta propia aceptada.
	KindTreatyAccepted Kind = "TREATY_ACCEPTED"
	// KindTreatyRejected es una oferta propia rechazada.
	KindTreatyRejected Kind = "TREATY_REJECTED"
	// KindTreatyOfferExpired es una oferta, propia o recibida, que caducó.
	KindTreatyOfferExpired Kind = "TREATY_OFFER_EXPIRED"
	// KindTreatyBreakAnnounced es una ruptura que anunció el otro.
	KindTreatyBreakAnnounced Kind = "TREATY_BREAK_ANNOUNCED"
	// KindTreatyEnded es un tratado vigente que terminó.
	KindTreatyEnded Kind = "TREATY_ENDED"
)

// Kinds es el catálogo cerrado de tipos de entrada, en orden estable. El enum
// del protocolo es el mismo (lo vigila el contract test).
func Kinds() []Kind {
	return []Kind{
		KindUnitLost, KindEnemyKilled, KindPillaged, KindPillageDone, KindHouseLost, KindHouseDestroyed,
		KindTreatyOffered, KindTreatyWithdrawn, KindTreatyAccepted, KindTreatyRejected,
		KindTreatyOfferExpired, KindTreatyBreakAnnounced, KindTreatyEnded,
	}
}

// Nombres de los eventos de world_events que el registro lee.
const (
	EventUnitKilled           = "UnitKilled"
	EventCityPillaged         = "CityPillaged"
	EventBuildingDestroyed    = "BuildingDestroyed"
	EventTreatyProposed       = "TreatyProposed"
	EventTreatyWithdrawn      = "TreatyWithdrawn"
	EventTreatyAccepted       = "TreatyAccepted"
	EventTreatyRejected       = "TreatyRejected"
	EventTreatyExpired        = "TreatyExpired"
	EventTreatyBreakAnnounced = "TreatyBreakAnnounced"
	EventTreatyBroken         = "TreatyBroken"
)

// EventTypes son los event_type que pueden dar una entrada: la consulta sólo
// lee éstos. UnitAttacked no está (SPEC-EVENT-LOG Q1).
func EventTypes() []string {
	return []string{
		EventUnitKilled, EventCityPillaged, EventBuildingDestroyed,
		EventTreatyProposed, EventTreatyWithdrawn, EventTreatyAccepted, EventTreatyRejected,
		EventTreatyExpired, EventTreatyBreakAnnounced, EventTreatyBroken,
	}
}

// Event es una fila de world_events ya leída, con lo que el registro usa de su
// payload. Lo que la fila no tiene vale cero: uuid.Nil, "" o 0.
type Event struct {
	ID   int64
	Type string
	// PlayerID es player_id: la víctima en las muertes y los saqueos, quien
	// propuso (o player_a) en los tratados.
	PlayerID uuid.UUID
	// Counterpart es counterpart_player_id: el autor, o el otro firmante.
	Counterpart uuid.UUID
	// AtMs es el instante del suceso en la simulación; en las filas anteriores
	// a ADR-028, el de su escritura.
	AtMs       int64
	UnitType   string
	Amount     int64
	X, Y       int32
	HasTile    bool
	TreatyType string
	// From es el estado del tratado antes de la transición.
	From string
	// By es quien dio la orden del tratado; uuid.Nil en un vencimiento y en las
	// filas anteriores a ADR-028.
	By         uuid.UUID
	BreaksAtMs int64
}

// Entry es una entrada del registro: uno o varios sucesos del mismo tipo con el
// mismo otro jugador. ID es el del evento más reciente del grupo.
type Entry struct {
	ID        int64
	Kind      Kind
	FirstAtMs int64
	LastAtMs  int64
	Count     int
	Other     uuid.UUID
	// UnitType, en las muertes.
	UnitType string
	// Amount, en los saqueos: la madera del grupo.
	Amount int64
	// TreatyType, en los tratados.
	TreatyType string
	// ProposedByMe, en una oferta caducada.
	ProposedByMe bool
	// X, Y: el último suceso del grupo, en las muertes y los saqueos.
	X, Y    int32
	HasTile bool
	// BreaksAtMs, en una ruptura anunciada.
	BreaksAtMs int64
}

// Classify dice qué entrada da e para viewer y quién es el otro jugador. ok es
// false si la fila no le cuenta nada: no es parte del suceso (INV-SEC-009), lo
// hizo él mismo en la diplomacia (SPEC-EVENT-LOG §4.1), o le faltan los datos
// para saberlo.
func Classify(viewer uuid.UUID, e Event) (kind Kind, other uuid.UUID, ok bool) {
	if viewer == uuid.Nil || (e.PlayerID != viewer && e.Counterpart != viewer) {
		return "", uuid.Nil, false
	}
	other = e.Counterpart
	if e.Counterpart == viewer {
		other = e.PlayerID
	}
	if other == uuid.Nil || other == viewer {
		return "", uuid.Nil, false
	}

	switch e.Type {
	case EventUnitKilled:
		if e.PlayerID == viewer {
			return KindUnitLost, other, true
		}
		return KindEnemyKilled, other, true
	case EventCityPillaged:
		if e.PlayerID == viewer {
			return KindPillaged, other, true
		}
		return KindPillageDone, other, true
	case EventBuildingDestroyed:
		if e.PlayerID == viewer {
			return KindHouseLost, other, true
		}
		return KindHouseDestroyed, other, true
	}

	// Un tratado: no entra lo que el propio jugador ordenó.
	actor := e.By
	if actor == uuid.Nil {
		switch e.Type {
		case EventTreatyProposed, EventTreatyWithdrawn:
			actor = e.PlayerID // quien propuso
		case EventTreatyAccepted, EventTreatyRejected:
			actor = e.Counterpart // quien respondió
		}
	}
	switch e.Type {
	case EventTreatyProposed:
		kind = KindTreatyOffered
	case EventTreatyWithdrawn:
		kind = KindTreatyWithdrawn
	case EventTreatyAccepted:
		kind = KindTreatyAccepted
	case EventTreatyRejected:
		kind = KindTreatyRejected
	case EventTreatyBreakAnnounced:
		if actor == uuid.Nil {
			// Una fila anterior a ADR-028: no se sabe quién rompió, y no se le
			// cuenta su propia ruptura a quien la hizo.
			return "", uuid.Nil, false
		}
		kind = KindTreatyBreakAnnounced
	case EventTreatyExpired:
		switch e.From {
		case "PROPOSED":
			return KindTreatyOfferExpired, other, true
		case "ACTIVE":
			return KindTreatyEnded, other, true
		}
		return "", uuid.Nil, false
	case EventTreatyBroken:
		return KindTreatyEnded, other, true
	default:
		return "", uuid.Nil, false
	}
	if actor == viewer {
		return "", uuid.Nil, false
	}
	return kind, other, true
}

// groupKey decide qué filas se juntan: mismo tipo, mismo otro jugador, mismo
// tipo de unidad o de tratado, y del mismo lado del cursor (SPEC-EVENT-LOG §4.2).
type groupKey struct {
	kind       Kind
	other      uuid.UUID
	unitType   string
	treatyType string
	mine       bool
	unseen     bool
}

// Builder arma una página. Recibe las filas de la más nueva a la más vieja
// (por id descendente) y corta antes de la que abriría la entrada número
// pageSize+1 (ADR-028 §5).
type Builder struct {
	viewer   uuid.UUID
	cursor   int64
	gapMs    int64
	pageSize int

	entries []Entry
	open    map[groupKey]int
	minID   int64
}

// NewBuilder prepara una página para viewer, cuyo cursor de lo visto es cursor.
func NewBuilder(viewer uuid.UUID, cursor int64, gap time.Duration, pageSize int) *Builder {
	if pageSize < 1 {
		pageSize = 1
	}
	return &Builder{
		viewer: viewer, cursor: cursor, gapMs: gap.Milliseconds(), pageSize: pageSize,
		open: make(map[groupKey]int),
	}
}

// Add procesa la siguiente fila y dice si entró en la página. false significa
// que la página está llena y la fila queda para la siguiente; una fila que no
// le cuenta nada al jugador entra siempre, sin dar entrada.
func (b *Builder) Add(e Event) bool {
	kind, other, ok := Classify(b.viewer, e)
	if !ok {
		b.take(e.ID)
		return true
	}
	mine := kind == KindTreatyOfferExpired && e.PlayerID == b.viewer
	k := groupKey{
		kind: kind, other: other, unitType: e.UnitType, mine: mine, unseen: e.ID > b.cursor,
	}
	if isTreaty(kind) {
		k.treatyType = e.TreatyType
	}
	if i, abierto := b.open[k]; abierto && e.AtMs >= b.entries[i].FirstAtMs-b.gapMs {
		g := &b.entries[i]
		g.Count++
		g.Amount += e.Amount
		g.FirstAtMs = min(g.FirstAtMs, e.AtMs)
		g.LastAtMs = max(g.LastAtMs, e.AtMs)
		b.take(e.ID)
		return true
	}
	if len(b.entries) == b.pageSize {
		return false
	}
	entry := Entry{
		ID: e.ID, Kind: kind, FirstAtMs: e.AtMs, LastAtMs: e.AtMs, Count: 1, Other: other,
		ProposedByMe: mine,
	}
	switch kind {
	case KindUnitLost, KindEnemyKilled:
		entry.UnitType = e.UnitType
		entry.X, entry.Y, entry.HasTile = e.X, e.Y, e.HasTile
	case KindPillaged, KindPillageDone:
		entry.Amount = e.Amount
		entry.X, entry.Y, entry.HasTile = e.X, e.Y, e.HasTile
	case KindHouseLost, KindHouseDestroyed:
		entry.X, entry.Y, entry.HasTile = e.X, e.Y, e.HasTile
	case KindTreatyBreakAnnounced:
		entry.TreatyType = e.TreatyType
		entry.BreaksAtMs = e.BreaksAtMs
	default:
		entry.TreatyType = e.TreatyType
	}
	b.entries = append(b.entries, entry)
	b.open[k] = len(b.entries) - 1
	b.take(e.ID)
	return true
}

func (b *Builder) take(id int64) {
	if b.minID == 0 || id < b.minID {
		b.minID = id
	}
}

// Entries son las entradas de la página, de la más reciente a la más vieja.
func (b *Builder) Entries() []Entry { return b.entries }

// MinID es el menor id que entró en la página, 0 si no entró ninguno: el
// beforeId de la siguiente.
func (b *Builder) MinID() int64 { return b.minID }

func isTreaty(k Kind) bool {
	switch k {
	case KindUnitLost, KindEnemyKilled, KindPillaged, KindPillageDone, KindHouseLost, KindHouseDestroyed:
		return false
	}
	return true
}
