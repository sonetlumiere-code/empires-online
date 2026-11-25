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
	// KindTradeOffered es una oferta de comercio recibida (SPEC-TRADE §6).
	KindTradeOffered Kind = "TRADE_OFFERED"
	// KindTradeAccepted es una oferta de comercio propia que el otro aceptó.
	KindTradeAccepted Kind = "TRADE_ACCEPTED"
	// KindTradeRejected es una oferta de comercio propia que el otro rechazó.
	KindTradeRejected Kind = "TRADE_REJECTED"
	// KindTradeWithdrawn es una oferta de comercio recibida que el otro retiró.
	KindTradeWithdrawn Kind = "TRADE_WITHDRAWN"
	// KindTradeOfferExpired es una oferta de comercio, propia o recibida, que
	// caducó.
	KindTradeOfferExpired Kind = "TRADE_OFFER_EXPIRED"
	// KindTerritoryClaimed es un territorio propio que otro empezó a reclamar
	// (SPEC-TERRITORY-CONQUEST §6).
	KindTerritoryClaimed Kind = "TERRITORY_CLAIMED"
	// KindTerritoryLost es un territorio propio que otro conquistó.
	KindTerritoryLost Kind = "TERRITORY_LOST"
	// KindTerritoryTaken es un territorio ajeno que las unidades propias
	// conquistaron.
	KindTerritoryTaken Kind = "TERRITORY_TAKEN"
	// Caravanas (SPEC-CARAVANS §6): la que llegó a quien lee, la suya que le
	// capturaron, la que iba a él y capturaron, y la suya que se perdió al
	// capturarla.
	KindCaravanArrived     Kind = "CARAVAN_ARRIVED"
	KindCaravanCaptured    Kind = "CARAVAN_CAPTURED"
	KindCaravanIntercepted Kind = "CARAVAN_INTERCEPTED"
	KindCaravanLost        Kind = "CARAVAN_LOST"
	// Clanes (SPEC-CLANS Q10): al expulsado y al líder que expulsó.
	KindClanExpelled       Kind = "CLAN_EXPELLED"
	KindClanMemberExpelled Kind = "CLAN_MEMBER_EXPELLED"
	// Gobierno del clan (SPEC-CLAN-GOVERNANCE Q8): el liderazgo que se recibe
	// por traspaso o porque el líder llevaba días sin jugar, y el que se cede o
	// se pierde por esa ausencia.
	KindClanLeadershipReceived  Kind = "CLAN_LEADERSHIP_RECEIVED"
	KindClanLeadershipGiven     Kind = "CLAN_LEADERSHIP_GIVEN"
	KindClanLeadershipInherited Kind = "CLAN_LEADERSHIP_INHERITED"
	KindClanLeadershipLost      Kind = "CLAN_LEADERSHIP_LOST"
	// Alianzas entre clanes (SPEC-CLAN-ALLIANCES Q9): la oferta que recibe el
	// líder de un clan, la aceptación y el rechazo que recibe el que propuso, y
	// el preaviso de ruptura, que recibe cada miembro de los dos clanes.
	KindClanAllianceOffered        Kind = "CLAN_ALLIANCE_OFFERED"
	KindClanAllianceAccepted       Kind = "CLAN_ALLIANCE_ACCEPTED"
	KindClanAllianceRejected       Kind = "CLAN_ALLIANCE_REJECTED"
	KindClanAllianceBreakAnnounced Kind = "CLAN_ALLIANCE_BREAK_ANNOUNCED"
	// El castillo (SPEC-CASTLE-AGE §6, ADR-044 §6): una unidad propia que
	// mató el castillo de otro, y una unidad ajena que mató el castillo propio.
	// Son tipos aparte para no agruparse con lo que hizo un ejército.
	KindUnitLostToCastle Kind = "UNIT_LOST_TO_CASTLE"
	KindCastleKill       Kind = "CASTLE_KILL"
	// Guerras entre clanes (SPEC-CLAN-WARS Q11): la declaración y el final, que
	// recibe cada miembro de los dos clanes, y la oferta de paz, que reciben el
	// líder y los oficiales del clan al que se ofrece.
	KindClanWarDeclared     Kind = "CLAN_WAR_DECLARED"
	KindClanWarPeaceOffered Kind = "CLAN_WAR_PEACE_OFFERED"
	KindClanWarEnded        Kind = "CLAN_WAR_ENDED"
	// El mercado (SPEC-MARKETS Q11): al vendedor, la venta de lotes de su
	// oferta y su caducidad. La caducidad no tiene otro jugador.
	KindMarketSold    Kind = "MARKET_SOLD"
	KindMarketExpired Kind = "MARKET_EXPIRED"
	// La caída de una ciudad (SPEC-CITY-FALL Q9): al asaltado, que asaltan su
	// ciudad; al vencido, que cayó; al vencedor, que la tomó y la arrasó.
	KindCityAssaulted Kind = "CITY_ASSAULTED"
	KindCityLost      Kind = "CITY_LOST"
	KindCityTaken     Kind = "CITY_TAKEN"
	// La segunda ciudad (SPEC-MULTI-CITY Q11): al fundador, sin otro jugador. No se
	// avisa en vivo: lo hizo él.
	KindCityFounded Kind = "CITY_FOUNDED"
)

// Kinds es el catálogo cerrado de tipos de entrada, en orden estable. El enum
// del protocolo es el mismo (lo vigila el contract test).
func Kinds() []Kind {
	return []Kind{
		KindUnitLost, KindEnemyKilled, KindPillaged, KindPillageDone, KindHouseLost, KindHouseDestroyed,
		KindTreatyOffered, KindTreatyWithdrawn, KindTreatyAccepted, KindTreatyRejected,
		KindTreatyOfferExpired, KindTreatyBreakAnnounced, KindTreatyEnded,
		KindTradeOffered, KindTradeAccepted, KindTradeRejected, KindTradeWithdrawn, KindTradeOfferExpired,
		KindTerritoryClaimed, KindTerritoryLost, KindTerritoryTaken,
		KindCaravanArrived, KindCaravanCaptured, KindCaravanIntercepted, KindCaravanLost,
		KindClanExpelled, KindClanMemberExpelled,
		KindClanLeadershipReceived, KindClanLeadershipGiven, KindClanLeadershipInherited, KindClanLeadershipLost,
		KindClanAllianceOffered, KindClanAllianceAccepted, KindClanAllianceRejected, KindClanAllianceBreakAnnounced,
		KindUnitLostToCastle, KindCastleKill,
		KindClanWarDeclared, KindClanWarPeaceOffered, KindClanWarEnded,
		KindMarketSold, KindMarketExpired,
		KindCityAssaulted, KindCityLost, KindCityTaken,
		KindCityFounded,
	}
}

// NoticeKinds son los tipos de entrada que también se avisan en vivo
// (SPEC-LIVE-NOTICES §4.1, ADR-033 §3): lo que hoy no le llega a quien mira otra
// parte del mapa. PILLAGED y HOUSE_LOST no están porque la víctima ya tiene
// pillagedAtMs y besiegedAtMs (Q1). Desde E12, también lo que pasa con un
// territorio (SPEC-TERRITORY-CONQUEST Q8), y desde E15 las caravanas
// capturadas que le tocan, pero no las que llegan (SPEC-CARAVANS Q11). El enum
// del aviso en el protocolo es el mismo (lo vigila el contract test).
func NoticeKinds() []Kind {
	return []Kind{
		KindUnitLost, KindEnemyKilled, KindPillageDone, KindHouseDestroyed,
		KindTerritoryClaimed, KindTerritoryLost, KindTerritoryTaken,
		KindCaravanCaptured, KindCaravanIntercepted, KindCaravanLost,
		KindClanExpelled,
		KindClanLeadershipReceived, KindClanLeadershipInherited,
		KindClanAllianceOffered, KindClanAllianceAccepted, KindClanAllianceBreakAnnounced,
		KindUnitLostToCastle, KindCastleKill,
		KindClanWarDeclared, KindClanWarPeaceOffered, KindClanWarEnded,
		KindCityAssaulted, KindCityLost, KindCityTaken,
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
	// Comercio (SPEC-TRADE, ADR-032 §7): player_id es quien propuso y
	// counterpart_player_id quien recibió la oferta.
	EventTradeProposed  = "TradeProposed"
	EventTradeAccepted  = "TradeAccepted"
	EventTradeRejected  = "TradeRejected"
	EventTradeWithdrawn = "TradeWithdrawn"
	EventTradeExpired   = "TradeExpired"
	// Conquista (SPEC-TERRITORY-CONQUEST §6, ADR-034): en el comienzo,
	// player_id es el dueño y counterpart_player_id quien reclama; en la
	// captura, el anterior dueño y el nuevo. Un TerritoryControlChanged sin
	// counterpart (la fundación, o la captura de uno libre) no cuenta nada.
	EventTerritoryClaimStarted   = "TerritoryClaimStarted"
	EventTerritoryControlChanged = "TerritoryControlChanged"
	// Caravanas (SPEC-CARAVANS §6, ADR-037 §7): player_id es a quien le pasa
	// —el destinatario en la llegada, el dueño que la pierde en la captura y la
	// pérdida, el destinatario original en la intercepción— y
	// counterpart_player_id el otro: quien la mandó o quien la capturó. Sólo
	// cuenta algo a player_id: lo que hizo el captor no entra (RN-LOG-001).
	EventCaravanArrived     = "CaravanArrived"
	EventCaravanCaptured    = "CaravanCaptured"
	EventCaravanIntercepted = "CaravanIntercepted"
	EventCaravanLost        = "CaravanLost"
	// Clanes (SPEC-CLANS §6): player_id es el expulsado y
	// counterpart_player_id el líder que lo expulsó.
	EventClanMemberExpelled = "ClanMemberExpelled"
	// Gobierno del clan (SPEC-CLAN-GOVERNANCE §6): player_id es el nuevo líder
	// y counterpart_player_id el anterior; Reason dice si fue un traspaso o la
	// ausencia del anterior.
	EventClanLeaderChanged = "ClanLeaderChanged"
	// Alianzas entre clanes (SPEC-CLAN-ALLIANCES §6, ADR-042 §6): player_id es
	// el líder que dio la orden y counterpart_player_id el líder del otro clan;
	// el preaviso, una fila por miembro de los dos clanes (Only = OnlyPlayer),
	// con counterpart_player_id quien lo provocó. ownerClanTag es la sigla del
	// otro clan para player_id, y forClanTag la del clan de player_id para el
	// otro. Retirada, ruptura y caducidad no van al registro.
	EventClanAllianceProposed       = "ClanAllianceProposed"
	EventClanAllianceAccepted       = "ClanAllianceAccepted"
	EventClanAllianceRejected       = "ClanAllianceRejected"
	EventClanAllianceWithdrawn      = "ClanAllianceWithdrawn"
	EventClanAllianceBreakAnnounced = "ClanAllianceBreakAnnounced"
	EventClanAllianceBroken         = "ClanAllianceBroken"
	EventClanAllianceExpired        = "ClanAllianceExpired"
	// Guerras entre clanes (SPEC-CLAN-WARS §6, ADR-045 §6): la declaración y el
	// final, una fila por miembro de los dos clanes (Only = OnlyPlayer) con
	// counterpart_player_id quien la dio (o el líder del otro clan); la oferta de
	// paz, una fila por líder y oficial del clan que la recibe. ownerClanTag es
	// la sigla del otro clan para player_id. El comienzo y la retirada de una
	// oferta no van al registro.
	EventClanWarDeclared       = "ClanWarDeclared"
	EventClanWarStarted        = "ClanWarStarted"
	EventClanWarPeaceOffered   = "ClanWarPeaceOffered"
	EventClanWarPeaceWithdrawn = "ClanWarPeaceWithdrawn"
	EventClanWarEnded          = "ClanWarEnded"
	// El mercado (SPEC-MARKETS §6, ADR-047): en la compra, player_id es el
	// vendedor y counterpart_player_id el comprador; la caducidad es sólo del
	// vendedor, sin counterpart. La publicación y la retirada no van al
	// registro: las ordenó el propio jugador.
	EventMarketOfferPosted    = "MarketOfferPosted"
	EventMarketPurchase       = "MarketPurchase"
	EventMarketOfferWithdrawn = "MarketOfferWithdrawn"
	EventMarketOfferExpired   = "MarketOfferExpired"
	// La caída de una ciudad (SPEC-CITY-FALL §6, ADR-050): player_id es el
	// asaltado (el vencido en la caída) y counterpart_player_id quien asalta
	// (el vencedor). El comienzo sólo se cuenta al asaltado; la caída, a los
	// dos. Un asalto roto no se escribe.
	EventCityAssaultStarted = "CityAssaultStarted"
	EventCityFell           = "CityFell"
	// La fundación de una segunda ciudad (SPEC-MULTI-CITY §6, ADR-051): player_id
	// es el fundador, sin otro jugador.
	EventCityFounded = "CityFounded"
)

// BreakReasonWar es el motivo de un TreatyBreakAnnounced que abrió una guerra
// entre los clanes de los dos firmantes (SPEC-CLAN-WARS §6).
const BreakReasonWar = "WAR"

// Motivos de un ClanLeaderChanged (SPEC-CLAN-GOVERNANCE §6).
const (
	LeaderReasonTransfer = "TRANSFER"
	LeaderReasonInactive = "INACTIVE"
)

// EventTypes son los event_type que pueden dar una entrada: la consulta sólo
// lee éstos. UnitAttacked no está (SPEC-EVENT-LOG Q1).
func EventTypes() []string {
	return []string{
		EventUnitKilled, EventCityPillaged, EventBuildingDestroyed,
		EventTreatyProposed, EventTreatyWithdrawn, EventTreatyAccepted, EventTreatyRejected,
		EventTreatyExpired, EventTreatyBreakAnnounced, EventTreatyBroken,
		EventTradeProposed, EventTradeAccepted, EventTradeRejected, EventTradeWithdrawn, EventTradeExpired,
		EventTerritoryClaimStarted, EventTerritoryControlChanged,
		EventCaravanArrived, EventCaravanCaptured, EventCaravanIntercepted, EventCaravanLost,
		EventClanMemberExpelled, EventClanLeaderChanged,
		EventClanAllianceProposed, EventClanAllianceAccepted, EventClanAllianceRejected, EventClanAllianceBreakAnnounced,
		EventClanWarDeclared, EventClanWarPeaceOffered, EventClanWarEnded,
		EventMarketPurchase, EventMarketOfferExpired,
		EventCityAssaultStarted, EventCityFell,
		EventCityFounded,
	}
}

// Line es una línea de una oferta de comercio: un recurso y una cantidad.
type Line struct {
	Resource string
	Amount   int64
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
	// OfferID, Give y Want, en los tratos: la oferta, lo que daba quien la
	// propuso (PlayerID) y lo que pedía.
	OfferID int64
	Give    []Line
	Want    []Line
	// TerritoryID y TerritoryName, en la conquista.
	TerritoryID   int64
	TerritoryName string
	// OwnerClanTag y ForClanTag, en la conquista con un clan de por medio
	// (SPEC-CLAN-TERRITORIES §6, ADR-041 §5): la sigla del clan dueño, para el
	// lado de player_id, y la del clan para el que se ganó, para el de
	// counterpart_player_id.
	OwnerClanTag string
	ForClanTag   string
	// Only limita la fila a uno de sus dos lados: OnlyPlayer u OnlyCounterpart.
	// Las filas por miembro de un clan cuentan su suceso sólo a ese miembro, y
	// la del que reclamó, sólo a él (ADR-041 §5). Vacío: a los dos.
	Only string
	// Cargo, en las caravanas: lo que llevaba.
	Cargo []Line
	// Reason, en un cambio de líder: LeaderReasonTransfer o
	// LeaderReasonInactive.
	Reason string
	// KillerBuildingType, en una muerte: el tipo del edificio que la causó, o
	// "" si fue una unidad (ADR-044 §5).
	KillerBuildingType string
	// BuildingType, en un derribo: el tipo del edificio que cayó
	// (SPEC-SIEGE-ENGINES §6).
	BuildingType string
	// La guerra entre clanes (SPEC-CLAN-WARS §6): cuándo empieza, el resultado
	// para player_id y el marcador visto desde su clan. Reason lleva además el
	// motivo del final, y en un TreatyBreakAnnounced, BreakReasonWar.
	StartsAtMs int64
	WarResult  string
	OurScore   int64
	TheirScore int64
	// Lots, en el mercado: los lotes vendidos o devueltos.
	Lots int32
	// FallsAtMs, en el comienzo de un asalto: cuándo cae la ciudad. NewX y
	// NewY, en la caída: el centro de la ciudad nueva del vencido; X e Y son
	// el de la arrasada (SPEC-CITY-FALL §6).
	FallsAtMs  int64
	NewX, NewY int32
}

// KillerCastle es el valor de Event.KillerBuildingType de una muerte que causó
// un castillo.
const KillerCastle = "CASTLE"

// Valores de Event.Only.
const (
	OnlyPlayer      = "player"
	OnlyCounterpart = "counterpart"
)

// ClanTagFor es la sigla del clan que la fila nombra para viewer: la del clan
// dueño para el lado de player_id y la del clan para el que se ganó para el
// otro (SPEC-CLAN-TERRITORIES §6).
func ClanTagFor(viewer uuid.UUID, e Event) string {
	if viewer == e.PlayerID {
		return e.OwnerClanTag
	}
	return e.ForClanTag
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
	// BuildingType, en los derribos: qué edificio cayó (SPEC-SIEGE-ENGINES §6).
	BuildingType string
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
	// TradeGive y TradeGet, en los tratos: lo que la oferta sacaba del almacén
	// de quien lee y lo que metía en él.
	TradeGive []Line
	TradeGet  []Line
	// TerritoryID y TerritoryName, en la conquista.
	TerritoryID   int64
	TerritoryName string
	// ClanTag, en la conquista con un clan de por medio (ClanTagFor).
	ClanTag string
	// Cargo, en las caravanas: lo que llevaba.
	Cargo []Line
	// Reason, en una ruptura que abrió una guerra (BreakReasonWar) y en el
	// final de una guerra (su motivo).
	Reason string
	// StartsAtMs, WarResult, OurScore y TheirScore, en la guerra entre clanes.
	StartsAtMs int64
	WarResult  string
	OurScore   int64
	TheirScore int64
	// Lots, en el mercado: los lotes vendidos o devueltos.
	Lots int32
	// FallsAtMs, en el asalto de una ciudad (SPEC-CITY-FALL §6).
	FallsAtMs int64
}

// Classify dice qué entrada da e para viewer y quién es el otro jugador. ok es
// false si la fila no le cuenta nada: no es parte del suceso (INV-SEC-009), lo
// hizo él mismo en la diplomacia (SPEC-EVENT-LOG §4.1), o le faltan los datos
// para saberlo.
func Classify(viewer uuid.UUID, e Event) (kind Kind, other uuid.UUID, ok bool) {
	if viewer == uuid.Nil || (e.PlayerID != viewer && e.Counterpart != viewer) {
		return "", uuid.Nil, false
	}
	// Una fila de un solo lado (ADR-041 §5) no le cuenta nada al otro.
	if (e.Only == OnlyPlayer && viewer != e.PlayerID) || (e.Only == OnlyCounterpart && viewer != e.Counterpart) {
		return "", uuid.Nil, false
	}
	// El mercado (SPEC-MARKETS Q11): sólo al vendedor. La caducidad es la única
	// entrada sin otro jugador; la compra no se le cuenta a quien la hizo.
	switch e.Type {
	case EventMarketOfferExpired:
		if e.PlayerID == viewer {
			return KindMarketExpired, uuid.Nil, true
		}
		return "", uuid.Nil, false
	case EventCityFounded:
		// Sólo al fundador, sin otro jugador (SPEC-MULTI-CITY Q11).
		if e.PlayerID == viewer {
			return KindCityFounded, uuid.Nil, true
		}
		return "", uuid.Nil, false
	case EventMarketPurchase:
		if e.PlayerID == viewer && e.Counterpart != uuid.Nil && e.Counterpart != viewer {
			return KindMarketSold, e.Counterpart, true
		}
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
		// SPEC-CASTLE-AGE §6: lo que mató un castillo, con sus propios tipos.
		if e.KillerBuildingType == KillerCastle {
			if e.PlayerID == viewer {
				return KindUnitLostToCastle, other, true
			}
			return KindCastleKill, other, true
		}
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
	case EventTerritoryClaimStarted:
		// Al dueño; a quien reclama no se le cuenta lo que ordenó él.
		if e.PlayerID == viewer {
			return KindTerritoryClaimed, other, true
		}
		return "", uuid.Nil, false
	case EventTerritoryControlChanged:
		if e.PlayerID == viewer {
			return KindTerritoryLost, other, true
		}
		return KindTerritoryTaken, other, true
	case EventCityAssaultStarted:
		// Al asaltado; a quien asalta no se le cuenta lo que ordenó él.
		if e.PlayerID == viewer {
			return KindCityAssaulted, other, true
		}
		return "", uuid.Nil, false
	case EventCityFell:
		if e.PlayerID == viewer {
			return KindCityLost, other, true
		}
		return KindCityTaken, other, true

	case EventClanMemberExpelled:
		// Al expulsado, que le expulsaron; al líder, que expulsó (RN-LOG-001).
		if e.PlayerID == viewer {
			return KindClanExpelled, other, true
		}
		return KindClanMemberExpelled, other, true
	case EventClanLeaderChanged:
		// Al nuevo líder, que lo recibió; al anterior, que lo cedió o lo perdió
		// (SPEC-CLAN-GOVERNANCE Q8).
		inactive := e.Reason == LeaderReasonInactive
		switch {
		case e.PlayerID == viewer && inactive:
			return KindClanLeadershipInherited, other, true
		case e.PlayerID == viewer:
			return KindClanLeadershipReceived, other, true
		case inactive:
			return KindClanLeadershipLost, other, true
		default:
			return KindClanLeadershipGiven, other, true
		}
	case EventClanAllianceProposed, EventClanAllianceAccepted, EventClanAllianceRejected:
		// Al líder del otro clan: a quien la dio no se le cuenta lo que ordenó
		// él (SPEC-CLAN-ALLIANCES Q9).
		if e.Counterpart != viewer {
			return "", uuid.Nil, false
		}
		switch e.Type {
		case EventClanAllianceProposed:
			return KindClanAllianceOffered, other, true
		case EventClanAllianceAccepted:
			return KindClanAllianceAccepted, other, true
		default:
			return KindClanAllianceRejected, other, true
		}
	case EventClanAllianceBreakAnnounced:
		// Una fila por miembro (Only = OnlyPlayer): a ese miembro.
		if e.PlayerID == viewer {
			return KindClanAllianceBreakAnnounced, other, true
		}
		return "", uuid.Nil, false
	case EventClanWarDeclared, EventClanWarPeaceOffered, EventClanWarEnded:
		// Una fila por miembro o por mando (Only = OnlyPlayer): a ese jugador
		// (SPEC-CLAN-WARS Q11).
		if e.PlayerID != viewer {
			return "", uuid.Nil, false
		}
		switch e.Type {
		case EventClanWarDeclared:
			return KindClanWarDeclared, other, true
		case EventClanWarPeaceOffered:
			return KindClanWarPeaceOffered, other, true
		default:
			return KindClanWarEnded, other, true
		}
	}

	// Una caravana: sólo a quien le pasa (player_id); al captor no se le cuenta
	// su captura, como a nadie lo que ordenó él.
	if kind, ok := caravanKind(e.Type); ok {
		if e.PlayerID == viewer {
			return kind, other, true
		}
		return "", uuid.Nil, false
	}

	// Un trato (SPEC-TRADE §6): como los tratados, no entra lo que el propio
	// jugador ordenó.
	if kind, ok := tradeKind(e.Type); ok {
		if kind == KindTradeOfferExpired {
			return kind, other, true
		}
		actor := e.By
		if actor == uuid.Nil {
			switch e.Type {
			case EventTradeProposed, EventTradeWithdrawn:
				actor = e.PlayerID // quien propuso
			default:
				actor = e.Counterpart // quien respondió
			}
		}
		if actor == viewer {
			return "", uuid.Nil, false
		}
		return kind, other, true
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

// Notice es un aviso en vivo para un jugador: el tipo de entrada que el suceso
// le contaría y quién es el otro.
type Notice struct {
	To    uuid.UUID
	Kind  Kind
	Other uuid.UUID
}

// Notices dice qué avisos da un suceso y a quién (SPEC-LIVE-NOTICES RN-NOTICE-002,
// ADR-033 §3). Lo clasifica como el registro para cada uno de sus dos jugadores,
// primero la víctima (PlayerID) y después el autor (Counterpart), y se queda con
// los tipos de NoticeKinds: nadie recibe un aviso que su registro no le
// contaría, y un tercero nunca recibe ninguno (INV-SEC-009).
func Notices(e Event) []Notice {
	var out []Notice
	for _, viewer := range []uuid.UUID{e.PlayerID, e.Counterpart} {
		kind, other, ok := Classify(viewer, e)
		if !ok || !isNotice(kind) {
			continue
		}
		out = append(out, Notice{To: viewer, Kind: kind, Other: other})
	}
	return out
}

// caravanKind es el tipo de entrada de un evento de las caravanas.
func caravanKind(eventType string) (Kind, bool) {
	switch eventType {
	case EventCaravanArrived:
		return KindCaravanArrived, true
	case EventCaravanCaptured:
		return KindCaravanCaptured, true
	case EventCaravanIntercepted:
		return KindCaravanIntercepted, true
	case EventCaravanLost:
		return KindCaravanLost, true
	}
	return "", false
}

func isCaravan(k Kind) bool {
	switch k {
	case KindCaravanArrived, KindCaravanCaptured, KindCaravanIntercepted, KindCaravanLost:
		return true
	}
	return false
}

func isNotice(k Kind) bool {
	for _, n := range NoticeKinds() {
		if n == k {
			return true
		}
	}
	return false
}

// tradeKind es el tipo de entrada de un evento del comercio.
func tradeKind(eventType string) (Kind, bool) {
	switch eventType {
	case EventTradeProposed:
		return KindTradeOffered, true
	case EventTradeAccepted:
		return KindTradeAccepted, true
	case EventTradeRejected:
		return KindTradeRejected, true
	case EventTradeWithdrawn:
		return KindTradeWithdrawn, true
	case EventTradeExpired:
		return KindTradeOfferExpired, true
	}
	return "", false
}

func isTrade(k Kind) bool {
	switch k {
	case KindTradeOffered, KindTradeAccepted, KindTradeRejected, KindTradeWithdrawn, KindTradeOfferExpired:
		return true
	}
	return false
}

// groupKey decide qué filas se juntan: mismo tipo, mismo otro jugador, mismo
// tipo de unidad o de tratado, y del mismo lado del cursor (SPEC-EVENT-LOG §4.2).
// Los tratos no se juntan nunca: cada uno es su propia entrada, porque lo que
// importa de él son sus cantidades (ADR-032 §7).
type groupKey struct {
	kind         Kind
	other        uuid.UUID
	unitType     string
	buildingType string
	treatyType   string
	offerID      int64
	mine         bool
	unseen       bool
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
	mine := (kind == KindTreatyOfferExpired || kind == KindTradeOfferExpired) && e.PlayerID == b.viewer
	k := groupKey{
		kind: kind, other: other, unitType: e.UnitType, buildingType: e.BuildingType, mine: mine, unseen: e.ID > b.cursor,
	}
	if isTreaty(kind) {
		k.treatyType = e.TreatyType
	}
	if isTrade(kind) || isTerritory(kind) || isCaravan(kind) || isAlliance(kind) || isWar(kind) || isMarket(kind) || isCityFall(kind) {
		k.offerID = e.ID // cada trato, cada suceso de un territorio, cada caravana, cada alianza y cada guerra, su entrada
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
	case KindUnitLost, KindEnemyKilled, KindUnitLostToCastle, KindCastleKill:
		entry.UnitType = e.UnitType
		entry.X, entry.Y, entry.HasTile = e.X, e.Y, e.HasTile
	case KindPillaged, KindPillageDone:
		entry.Amount = e.Amount
		entry.X, entry.Y, entry.HasTile = e.X, e.Y, e.HasTile
	case KindHouseLost, KindHouseDestroyed:
		entry.BuildingType = e.BuildingType
		entry.X, entry.Y, entry.HasTile = e.X, e.Y, e.HasTile
	case KindTreatyBreakAnnounced:
		entry.TreatyType = e.TreatyType
		entry.BreaksAtMs = e.BreaksAtMs
		if e.Reason == BreakReasonWar {
			entry.Reason = e.Reason
		}
	case KindClanWarDeclared, KindClanWarPeaceOffered:
		// La sigla del otro clan y cuándo empieza (SPEC-CLAN-WARS §4.7).
		entry.ClanTag = ClanTagFor(b.viewer, e)
		entry.StartsAtMs = e.StartsAtMs
	case KindClanWarEnded:
		entry.ClanTag = ClanTagFor(b.viewer, e)
		entry.Reason, entry.WarResult = e.Reason, e.WarResult
		entry.OurScore, entry.TheirScore = e.OurScore, e.TheirScore
	case KindTerritoryClaimed, KindTerritoryLost, KindTerritoryTaken:
		entry.TerritoryID, entry.TerritoryName = e.TerritoryID, e.TerritoryName
		entry.ClanTag = ClanTagFor(b.viewer, e)
		entry.X, entry.Y, entry.HasTile = e.X, e.Y, e.HasTile
	case KindCityAssaulted:
		entry.FallsAtMs = e.FallsAtMs
		entry.X, entry.Y, entry.HasTile = e.X, e.Y, e.HasTile
	case KindCityLost:
		// Dónde refundó quien lee (SPEC-CITY-FALL §4.6).
		entry.X, entry.Y, entry.HasTile = e.NewX, e.NewY, e.HasTile
	case KindCityTaken, KindCityFounded:
		entry.X, entry.Y, entry.HasTile = e.X, e.Y, e.HasTile
	case KindClanLeadershipReceived, KindClanLeadershipGiven, KindClanLeadershipInherited, KindClanLeadershipLost:
		// La sigla del clan cuyo liderazgo cambió (SPEC-CLAN-GOVERNANCE §4.6).
		entry.ClanTag = ClanTagFor(b.viewer, e)
	case KindClanAllianceOffered, KindClanAllianceAccepted, KindClanAllianceRejected:
		// La sigla del otro clan (SPEC-CLAN-ALLIANCES §4.6).
		entry.ClanTag = ClanTagFor(b.viewer, e)
	case KindClanAllianceBreakAnnounced:
		entry.ClanTag = ClanTagFor(b.viewer, e)
		entry.BreaksAtMs = e.BreaksAtMs
	case KindCaravanArrived, KindCaravanCaptured, KindCaravanIntercepted, KindCaravanLost:
		entry.Cargo = e.Cargo
		entry.X, entry.Y, entry.HasTile = e.X, e.Y, e.HasTile
	case KindMarketSold:
		// Lo que salió de la custodia y lo que viene en la caravana del comprador.
		entry.TradeGive, entry.TradeGet, entry.Lots = e.Give, e.Want, e.Lots
	case KindMarketExpired:
		// Lo que volvió al almacén.
		entry.TradeGet, entry.Lots = e.Give, e.Lots
	case KindTradeOffered, KindTradeAccepted, KindTradeRejected, KindTradeWithdrawn, KindTradeOfferExpired:
		// Desde quien lee: si propuso él, da Give y recibe Want; si no, al revés.
		if e.PlayerID == b.viewer {
			entry.TradeGive, entry.TradeGet = e.Give, e.Want
		} else {
			entry.TradeGive, entry.TradeGet = e.Want, e.Give
		}
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

func isMarket(k Kind) bool { return k == KindMarketSold || k == KindMarketExpired }

// isCityFall: cada asalto y cada caída, su entrada.
func isCityFall(k Kind) bool {
	return k == KindCityAssaulted || k == KindCityLost || k == KindCityTaken || k == KindCityFounded
}

func isTreaty(k Kind) bool {
	switch k {
	case KindUnitLost, KindEnemyKilled, KindPillaged, KindPillageDone, KindHouseLost, KindHouseDestroyed:
		return false
	}
	return !isTrade(k) && !isTerritory(k) && !isCaravan(k)
}

func isTerritory(k Kind) bool {
	switch k {
	case KindTerritoryClaimed, KindTerritoryLost, KindTerritoryTaken:
		return true
	}
	return false
}

// isWar dice si el tipo es de una guerra entre clanes.
func isWar(k Kind) bool {
	switch k {
	case KindClanWarDeclared, KindClanWarPeaceOffered, KindClanWarEnded:
		return true
	}
	return false
}

// isAlliance dice si el tipo es de una alianza entre clanes.
func isAlliance(k Kind) bool {
	switch k {
	case KindClanAllianceOffered, KindClanAllianceAccepted, KindClanAllianceRejected, KindClanAllianceBreakAnnounced:
		return true
	}
	return false
}
