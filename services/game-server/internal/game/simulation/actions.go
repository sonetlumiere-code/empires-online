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
	// rechaza con CITY_PROTECTED. Ninguna lo es en el MVP.
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
}

// sessionMessages son los mensajes que no actúan sobre el mundo.
var sessionMessages = map[string]bool{
	protocol.TypeSessionHello: true,
	protocol.TypeSessionPing:  true,
	protocol.TypeSessionView:  true,
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
