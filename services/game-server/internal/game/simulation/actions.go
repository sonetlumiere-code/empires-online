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
	// La primera acción hostil (RN-COMBAT-002): la frena la protección del
	// dueño del objetivo, no el suelo. Su punto único es attackBlocked.
	protocol.TypeUnitAttack: {Hostile: true, Territory: TerritoryFree},
	// El saqueo (RN-COMBAT-020): hostil contra la ciudad, con el mismo punto
	// único de la protección, pillageBlocked.
	protocol.TypeUnitPillage: {Hostile: true, Territory: TerritoryFree},
	// El asedio (RN-COMBAT-030): hostil contra la ciudad de la casa, con el
	// mismo predicado que el saqueo, cityAttackBlocked.
	protocol.TypeUnitDemolish: {Hostile: true, Territory: TerritoryFree},
	// La diplomacia no es agresión (RN-DIPLO-010): se propone, se responde y se
	// rompe aunque el otro esté protegido.
	protocol.TypeTreatyPropose: {Hostile: false, Territory: TerritoryFree},
	protocol.TypeTreatyRespond: {Hostile: false, Territory: TerritoryFree},
	protocol.TypeTreatyCancel:  {Hostile: false, Territory: TerritoryFree},
}

// sessionMessages son los mensajes que no actúan sobre el mundo. Los del
// registro sólo leen la historia y marcan lo visto (SPEC-EVENT-LOG, ADR-028 §1).
var sessionMessages = map[string]bool{
	protocol.TypeSessionHello:  true,
	protocol.TypeSessionPing:   true,
	protocol.TypeSessionView:   true,
	protocol.TypeEventLogFetch: true,
	protocol.TypeEventLogSeen:  true,
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
