package clan

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Las guerras entre clanes (SPEC-CLAN-WARS, ADR-045).
//
// Una guerra enfrenta a dos clanes: el que la declara (Attacker) y el que la
// recibe (Defender). Sus efectos son simétricos; el sentido sólo dice quién la
// declaró y cuenta para el tope. No da ni quita paz: compromete a los dos clanes
// (los pactos entre sus miembros terminan) y lleva un marcador por bando.

// Valores por defecto de la configuración (SPEC-CLAN-WARS Q6, Q7).
const (
	// DefaultMaxWars es el tope de guerras abiertas declaradas por un clan
	// (EO_CLAN_MAX_WARS).
	DefaultMaxWars = 1
	// DefaultWarDuration es lo que dura como mucho una guerra desde que empieza
	// (EO_CLAN_WAR_HOURS).
	DefaultWarDuration = 72 * time.Hour
	// DefaultWarCooldown es la tregua tras una guerra, sin nueva guerra entre
	// los dos clanes (EO_CLAN_WAR_COOLDOWN_HOURS).
	DefaultWarCooldown = 24 * time.Hour
	// DefaultWarScorePush es cada cuánto, como mucho, se difunde un marcador
	// que cambia (EO_CLAN_WAR_SCORE_PUSH_SECONDS).
	DefaultWarScorePush = 10 * time.Second
)

// WarStatus es el estado de una guerra.
type WarStatus string

// Estados de una guerra (SPEC-CLAN-WARS §4.1).
const (
	WarDeclared WarStatus = "DECLARED"
	WarActive   WarStatus = "ACTIVE"
	WarEnded    WarStatus = "ENDED"
)

// Motivos del final de una guerra (SPEC-CLAN-WARS §4.5).
const (
	WarEndSurrender = "SURRENDER"
	WarEndPeace     = "PEACE"
	WarEndTimeout   = "TIMEOUT"
	WarEndDissolved = "DISSOLVED"
)

// Resultado de una guerra terminada para uno de sus clanes.
const (
	WarWon   = "WON"
	WarLost  = "LOST"
	WarDrawn = "DRAWN"
)

// Puntos de cada hecho del marcador (RN-CLANWAR-007, Q3).
const (
	PointsKill      = 1
	PointsBuilding  = 3
	PointsTerritory = 10
	// PointsCity, desde E31: una ciudad caída (SPEC-CITY-FALL Q9).
	PointsCity = 30
)

// ErrWarTransition: el salto de estado no está permitido.
var ErrWarTransition = errors.New("transición de guerra no permitida")

// War es una guerra entre dos clanes. Los instantes son milisegundos de la
// simulación; cero si no aplican.
type War struct {
	ID         int64
	Attacker   int64
	Defender   int64
	DeclaredBy uuid.UUID
	RequestID  string
	Status     WarStatus
	// DeclaredAtMs es la declaración; StartsAtMs, el fin de la preparación;
	// EndsAtMs, el plazo máximo (fijado al empezar).
	DeclaredAtMs int64
	StartsAtMs   int64
	EndsAtMs     int64
	// Las ofertas de paz de cada lado.
	PeaceByAttacker bool
	PeaceByDefender bool
	// El final: el instante, el motivo y el clan vencedor (cero en empate).
	EndedAtMs int64
	EndReason string
	Winner    int64
	// El marcador de cada lado.
	AttackerScore int64
	DefenderScore int64
}

// WarPair es el par sin orden de dos clanes, para buscar la guerra de un par.
func WarPair(x, y int64) [2]int64 {
	a, b := CanonicalClans(x, y)
	return [2]int64{a, b}
}

// Pair es el par sin orden de la guerra.
func (w War) Pair() [2]int64 { return WarPair(w.Attacker, w.Defender) }

// Has dice si el clan es una de las dos partes.
func (w War) Has(clanID int64) bool { return w.Attacker == clanID || w.Defender == clanID }

// Other es la otra parte; cero si clanID no es parte.
func (w War) Other(clanID int64) int64 {
	switch clanID {
	case w.Attacker:
		return w.Defender
	case w.Defender:
		return w.Attacker
	}
	return 0
}

// Open dice si la guerra sigue abierta: en preparación o activa.
func (w War) Open() bool { return w.Status == WarDeclared || w.Status == WarActive }

// Scoring dice si el marcador cuenta en atMs: la guerra está activa
// (RN-CLANWAR-007).
func (w War) Scoring(atMs int64) bool {
	return w.Status == WarActive && atMs >= w.StartsAtMs
}

// ScoreOf es el marcador de un lado.
func (w War) ScoreOf(clanID int64) int64 {
	switch clanID {
	case w.Attacker:
		return w.AttackerScore
	case w.Defender:
		return w.DefenderScore
	}
	return 0
}

// AddPoints suma puntos al lado de clanID. Devuelve una copia.
func (w War) AddPoints(clanID int64, points int64) War {
	switch clanID {
	case w.Attacker:
		w.AttackerScore += points
	case w.Defender:
		w.DefenderScore += points
	}
	return w
}

// PeaceOfferedBy dice si el lado de clanID ofrece la paz.
func (w War) PeaceOfferedBy(clanID int64) bool {
	switch clanID {
	case w.Attacker:
		return w.PeaceByAttacker
	case w.Defender:
		return w.PeaceByDefender
	}
	return false
}

// WithPeaceOffer pone o quita la oferta de paz del lado de clanID. Devuelve una
// copia.
func (w War) WithPeaceOffer(clanID int64, offer bool) War {
	switch clanID {
	case w.Attacker:
		w.PeaceByAttacker = offer
	case w.Defender:
		w.PeaceByDefender = offer
	}
	return w
}

// Start pasa una guerra en preparación a activa en atMs, con su plazo.
func (w War) Start(atMs int64, duration time.Duration) (War, error) {
	if w.Status != WarDeclared {
		return w, fmt.Errorf("%w: %s → %s", ErrWarTransition, w.Status, WarActive)
	}
	w.Status = WarActive
	w.EndsAtMs = atMs + duration.Milliseconds()
	return w, nil
}

// End cierra una guerra abierta en atMs con su motivo y su vencedor (cero en
// empate). El marcador queda como está: es el congelado. Una guerra que termina
// en su preparación no tenía plazo: toma su final como plazo, que la base exige
// en toda guerra que no esté en preparación (CHECK clan_wars_ends_at).
func (w War) End(reason string, winner int64, atMs int64) (War, error) {
	if !w.Open() {
		return w, fmt.Errorf("%w: %s → %s", ErrWarTransition, w.Status, WarEnded)
	}
	if winner != 0 && !w.Has(winner) {
		return w, fmt.Errorf("%w: el vencedor %d no es parte", ErrWarTransition, winner)
	}
	if w.Status == WarDeclared {
		w.EndsAtMs = atMs
	}
	w.Status = WarEnded
	w.EndReason, w.Winner, w.EndedAtMs = reason, winner, atMs
	w.PeaceByAttacker, w.PeaceByDefender = false, false
	return w, nil
}

// LeaderByScore es el clan con más puntos, o cero si empatan: el vencedor por
// plazo (RN-CLANWAR-008).
func (w War) LeaderByScore() int64 {
	switch {
	case w.AttackerScore > w.DefenderScore:
		return w.Attacker
	case w.DefenderScore > w.AttackerScore:
		return w.Defender
	}
	return 0
}

// ResultFor es el resultado de una guerra terminada para uno de sus clanes.
func (w War) ResultFor(clanID int64) string {
	switch w.Winner {
	case 0:
		return WarDrawn
	case clanID:
		return WarWon
	}
	return WarLost
}

// InCooldown dice si una guerra terminada todavía impide otra entre los mismos
// clanes en nowMs (RN-CLANWAR-006).
func (w War) InCooldown(nowMs int64, cooldown time.Duration) bool {
	return w.Status == WarEnded && nowMs < w.EndedAtMs+cooldown.Milliseconds()
}

// WarRecord es el historial de guerras terminadas de un clan.
type WarRecord struct {
	Won   int
	Lost  int
	Drawn int
}

// Add suma el resultado de una guerra.
func (r WarRecord) Add(result string) WarRecord {
	switch result {
	case WarWon:
		r.Won++
	case WarLost:
		r.Lost++
	case WarDrawn:
		r.Drawn++
	}
	return r
}

// WarPoints es lo que un hecho suma al marcador de una guerra: va en el payload
// del evento que puntúa (ADR-045 §4).
type WarPoints struct {
	WarID  int64
	ClanID int64
	Points int64
}
