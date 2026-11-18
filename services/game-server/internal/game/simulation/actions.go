package simulation

import "github.com/empires-online/empires-online/services/game-server/internal/protocol"

// Clasificación de las acciones de mundo (ADR-016).
//
// Cada mensaje cliente→servidor que actúa sobre el mundo declara dos cosas: si
// es hostil —y por tanto lo frena la protección de una ciudad, INV-CITY-006— y
// dónde se permite. Un test recorre el catálogo cerrado de mensajes y falla si
// alguno no está ni aquí ni entre los de sesión: añadir un comando sin decidir
// esas dos cosas deja de ser un olvido posible.

// TerritoryRule dice dónde se permite una acción.
type TerritoryRule string

// TerritoryFree: en cualquier territorio, propio, ajeno o libre. En el MVP es
// la regla de todas las acciones (ADR-016 §2.1).
const TerritoryFree TerritoryRule = "LIBRE"

// ActionPolicy es la clasificación de una acción.
type ActionPolicy struct {
	// Hostile: dirigida contra una ciudad PROTECTED o su territorio, se
	// rechaza con CITY_PROTECTED. La primera es unit.attack (SPEC-COMBAT-RAID).
	Hostile   bool
	Territory TerritoryRule
}

// actionPolicies clasifica los mensajes que actúan sobre el mundo.
var actionPolicies = map[string]ActionPolicy{
	protocol.TypeUnitMove:       {Hostile: false, Territory: TerritoryFree},
	protocol.TypeUnitCancelMove: {Hostile: false, Territory: TerritoryFree},
	// La protección es contra agresión, no contra logística (RN-GARR-009).
	protocol.TypeUnitGarrison:   {Hostile: false, Territory: TerritoryFree},
	protocol.TypeUnitUngarrison: {Hostile: false, Territory: TerritoryFree},
	// La economía no es agresión: recolectar en un bosque de territorio ajeno,
	// construir y entrenar en la ciudad propia no tocan a nadie (ADR-016 §2.1).
	// Robar madera es unit.pillage, más abajo.
	protocol.TypeUnitGather: {Hostile: false, Territory: TerritoryFree},
	protocol.TypeCityBuild:  {Hostile: false, Territory: TerritoryFree},
	protocol.TypeCityTrain:  {Hostile: false, Territory: TerritoryFree},
	// Avanzar de era es invertir en la ciudad propia (SPEC-ECON-STONE §6).
	protocol.TypeCityAdvance: {Hostile: false, Territory: TerritoryFree},
	// Elaborar en el taller propio tampoco toca a nadie (SPEC-CIVILIZATION-GOODS §6).
	protocol.TypeCityCraft: {Hostile: false, Territory: TerritoryFree},
	// Investigar en la ciudad propia, tampoco (SPEC-TECHNOLOGIES §6).
	protocol.TypeCityResearch: {Hostile: false, Territory: TerritoryFree},
	// La primera acción hostil (RN-COMBAT-002): la frena la protección del
	// dueño del objetivo, no el suelo. Su punto único es attackBlocked.
	protocol.TypeUnitAttack: {Hostile: true, Territory: TerritoryFree},
	// El saqueo (RN-COMBAT-020): hostil contra la ciudad, con el mismo punto
	// único de la protección, pillageBlocked.
	protocol.TypeUnitPillage: {Hostile: true, Territory: TerritoryFree},
	// El asedio (RN-COMBAT-030): hostil contra la ciudad de la casa, con el
	// mismo predicado que el saqueo, cityAttackBlocked.
	protocol.TypeUnitDemolish: {Hostile: true, Territory: TerritoryFree},
	// La conquista (RN-CONQ-003): hostil contra el dueño del territorio, si lo
	// tiene; la protección se comprueba en claimBlocked.
	protocol.TypeUnitClaim: {Hostile: true, Territory: TerritoryFree},
	// La diplomacia no es agresión (RN-DIPLO-010): se propone, se responde y se
	// rompe aunque el otro esté protegido.
	protocol.TypeTreatyPropose: {Hostile: false, Territory: TerritoryFree},
	protocol.TypeTreatyRespond: {Hostile: false, Territory: TerritoryFree},
	protocol.TypeTreatyCancel:  {Hostile: false, Territory: TerritoryFree},
	// El comercio tampoco (RN-TRADE-008): se ofrece y se acepta con el otro
	// desconectado o protegido.
	protocol.TypeTradePropose: {Hostile: false, Territory: TerritoryFree},
	protocol.TypeTradeRespond: {Hostile: false, Territory: TerritoryFree},
	protocol.TypeTradeCancel:  {Hostile: false, Territory: TerritoryFree},
	// Unirse a un bando (SPEC-FACTIONS §4.2): no es hostil y no lo frena la
	// protección propia ni la ajena.
	protocol.TypeFactionJoin: {Hostile: false, Territory: TerritoryFree},
	// Los clanes (SPEC-CLANS §6): fundar, invitar, responder, salir y expulsar
	// cambian la paz pero no agreden a nadie, y no los frena la protección.
	protocol.TypeClanCreate:  {Hostile: false, Territory: TerritoryFree},
	protocol.TypeClanInvite:  {Hostile: false, Territory: TerritoryFree},
	protocol.TypeClanRespond: {Hostile: false, Territory: TerritoryFree},
	protocol.TypeClanLeave:   {Hostile: false, Territory: TerritoryFree},
	protocol.TypeClanExpel:   {Hostile: false, Territory: TerritoryFree},
	// El gobierno del clan (SPEC-CLAN-GOVERNANCE §6): rangos y liderazgo, sin
	// cambiar la paz.
	protocol.TypeClanPromote:  {Hostile: false, Territory: TerritoryFree},
	protocol.TypeClanTransfer: {Hostile: false, Territory: TerritoryFree},
	// Las alianzas entre clanes (SPEC-CLAN-ALLIANCES §4.3): cambian la paz pero
	// no agreden a nadie, y no las frena la protección.
	protocol.TypeClanAlliancePropose: {Hostile: false, Territory: TerritoryFree},
	protocol.TypeClanAllianceRespond: {Hostile: false, Territory: TerritoryFree},
	protocol.TypeClanAllianceCancel:  {Hostile: false, Territory: TerritoryFree},
	// Las guerras entre clanes (SPEC-CLAN-WARS §4.3): comprometen a los dos
	// clanes pero no agreden a nadie, y no las frena la protección.
	protocol.TypeClanWarDeclare:   {Hostile: false, Territory: TerritoryFree},
	protocol.TypeClanWarPeace:     {Hostile: false, Territory: TerritoryFree},
	protocol.TypeClanWarSurrender: {Hostile: false, Territory: TerritoryFree},
}

// sessionMessages son los mensajes que no actúan sobre el mundo. Los del
// registro sólo leen la historia y marcan lo visto (SPEC-EVENT-LOG, ADR-028 §1).
var sessionMessages = map[string]bool{
	protocol.TypeSessionHello:  true,
	protocol.TypeSessionPing:   true,
	protocol.TypeSessionView:   true,
	protocol.TypeEventLogFetch: true,
	protocol.TypeEventLogSeen:  true,
	// Los mensajes entre jugadores tampoco tocan el mundo: los guarda y los lee
	// la conexión (SPEC-MESSAGES RN-MSG-009, ADR-038 §1).
	protocol.TypeMessageSend:  true,
	protocol.TypeMessageList:  true,
	protocol.TypeMessageFetch: true,
	protocol.TypeMessageRead:  true,
	protocol.TypeMessageBlock: true,
	// El canal y la vista pública de un clan los atiende la conexión
	// (SPEC-CLANS §4.6, ADR-040 §5).
	protocol.TypeClanLookup:    true,
	protocol.TypeClanChatSend:  true,
	protocol.TypeClanChatFetch: true,
	protocol.TypeClanChatRead:  true,
}

// ActionPolicyOf devuelve la clasificación de un mensaje que actúa sobre el
// mundo. El segundo valor es falso para los mensajes de sesión y los
// desconocidos.
func ActionPolicyOf(msgType string) (ActionPolicy, bool) {
	p, ok := actionPolicies[msgType]
	return p, ok
}

// IsSessionMessage indica si un mensaje es de sesión y no actúa sobre el mundo.
func IsSessionMessage(msgType string) bool { return sessionMessages[msgType] }
