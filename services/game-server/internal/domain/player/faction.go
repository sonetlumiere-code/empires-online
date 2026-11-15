package player

// Reglas del bando (SPEC-FACTIONS, ADR-039).
//
// Todas trabajan con el código de la facción efectiva, nunca con la entrada
// pendiente (RN-FAC-002). Un código vacío es NEUTRAL: es el de un jugador sin
// ciudad o el de una ciudad de un test que no lo fija.

// EffectiveFaction normaliza un código: vacío es NEUTRAL.
func EffectiveFaction(code string) string {
	if code == "" {
		return FactionNeutral
	}
	return code
}

// KnownFaction dice si un código es uno de los tres del catálogo.
func KnownFaction(code string) bool {
	switch code {
	case FactionOrder, FactionChaos, FactionNeutral:
		return true
	}
	return false
}

// CanJoin dice si un jugador con la facción from puede unirse a to
// (RN-FAC-001, INV-PLAYER-013): sólo desde NEUTRAL y sólo a ORDER o a CHAOS.
func CanJoin(from, to string) bool {
	return EffectiveFaction(from) == FactionNeutral && (to == FactionOrder || to == FactionChaos)
}

// OrderPeace dice si dos jugadores están en paz por su bando: los dos son de
// ORDER (RN-FAC-003).
func OrderPeace(a, b string) bool {
	return a == FactionOrder && b == FactionOrder
}

// Opposed dice si dos bandos son contrarios: uno es ORDER y el otro CHAOS. Entre
// ellos no hay pacto ni alianza (RN-FAC-005).
func Opposed(a, b string) bool {
	return (a == FactionOrder && b == FactionChaos) || (a == FactionChaos && b == FactionOrder)
}

// Affine dice si un territorio es afín al dueño de una unidad (RN-FAC-007): es
// suyo, o los dos son del mismo bando y ese bando no es NEUTRAL. ownTerritory
// dice si el dueño del territorio es el mismo jugador que el de la unidad.
func Affine(ownTerritory bool, unitFaction, ownerFaction string) bool {
	if ownTerritory {
		return true
	}
	u, o := EffectiveFaction(unitFaction), EffectiveFaction(ownerFaction)
	return u != FactionNeutral && u == o
}

// DefaultAffineDamagePerMille es el daño que recibe una unidad en tierra afín,
// por mil (SPEC-FACTIONS Q6): un 20 % menos.
const DefaultAffineDamagePerMille int32 = 800

// AffineDamage es el daño de un golpe a una unidad en tierra afín
// (RN-FAC-008): el daño por perMille, redondeado al más cercano y nunca menos de
// 1, con la aritmética de los rasgos (RN-CIV-004, INV-MOVE-009).
func AffineDamage(damage, perMille int32) int32 {
	return ScaleAmount(damage, perMille)
}
