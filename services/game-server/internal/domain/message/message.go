// Package message tiene las reglas de los mensajes entre jugadores que no
// dependen de la base ni de la red (SPEC-MESSAGES, ADR-038): qué texto se admite,
// cómo se ordena la pareja de una conversación, cómo se recorta la vista previa
// de la bandeja y los límites de ritmo de cada jugador. Los mensajes no son
// estado del mundo: la simulación nunca usa este paquete (RN-MSG-009).
package message

import (
	"bytes"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
)

// MaxChars es el máximo de caracteres (puntos de código) de un mensaje tras
// recortar los extremos (RN-MSG-003, Q3). El esquema del protocolo lo acota en
// unidades UTF-16, el doble.
const MaxChars = 500

// PreviewChars es el largo del último mensaje en la bandeja (SPEC-MESSAGES §6).
const PreviewChars = 80

// Errores de NormalizeBody. Los tres son INVALID_MESSAGE en el protocolo.
var (
	ErrEmpty   = errors.New("el mensaje está vacío")
	ErrTooLong = errors.New("el mensaje es demasiado largo")
	ErrControl = errors.New("el mensaje lleva caracteres de control")
)

// Errores de un envío, un bloqueo o una lectura que no dependen del texto. Los
// cuatro primeros son INVALID_TARGET en el protocolo y el último RATE_LIMITED.
var (
	ErrUnknownPlayer  = errors.New("ese jugador no existe")
	ErrSelf           = errors.New("no puedes escribirte ni bloquearte a ti mismo")
	ErrBlockedByOther = errors.New("ese jugador no acepta tus mensajes")
	ErrBlockedByMe    = errors.New("tienes bloqueado a ese jugador; desbloquéalo para escribirle")
	ErrRateLimited    = errors.New("demasiadas conversaciones nuevas; espera un rato")
)

// Sent es un mensaje recién guardado, con la ciudad de cada uno de sus dos
// jugadores para nombrarlos desde cada lado (RN-MSG-010). Duplicate es cierto
// cuando el requestId ya tenía mensaje y éste es aquél (INV-SEC-007).
type Sent struct {
	ID        int64
	From, To  uuid.UUID
	Body      string
	SentAtMs  int64
	FromCity  *City
	ToCity    *City
	Duplicate bool
}

// City es la ciudad con la que se nombra a un jugador.
type City struct {
	ID   int64
	Name string
}

// NormalizeBody devuelve el texto que se guarda: recortado en los extremos, con
// los saltos de línea de Windows convertidos en «\n», de 1 a MaxChars
// caracteres y sin más caracteres de control que «\n» (RN-MSG-003). Los
// controles de dirección del texto (U+202E y familia) también se rechazan:
// sirven para que un texto se lea distinto de lo que dice.
func NormalizeBody(raw string) (string, error) {
	if !utf8.ValidString(raw) {
		return "", ErrControl
	}
	body := strings.TrimSpace(strings.ReplaceAll(raw, "\r\n", "\n"))
	if body == "" {
		return "", ErrEmpty
	}
	if utf8.RuneCountInString(body) > MaxChars {
		return "", ErrTooLong
	}
	for _, r := range body {
		if r == '\n' {
			continue
		}
		if unicode.IsControl(r) || unicode.Is(unicode.Bidi_Control, r) {
			return "", ErrControl
		}
	}
	return body, nil
}

// Preview recorta un texto a PreviewChars caracteres para la bandeja, con «…»
// si lo corta, y con los saltos de línea como espacios.
func Preview(body string) string {
	flat := strings.ReplaceAll(body, "\n", " ")
	if utf8.RuneCountInString(flat) <= PreviewChars {
		return flat
	}
	runes := []rune(flat)
	return strings.TrimRight(string(runes[:PreviewChars-1]), " ") + "…"
}

// Pair ordena una pareja de jugadores como la ordena PostgreSQL (byte a byte):
// la conversación de A con B es la misma que la de B con A. Es el orden del
// CHECK de player_messages y la clave del candado por pareja (ADR-038 §2, §3).
func Pair(a, b uuid.UUID) (low, high uuid.UUID) {
	if bytes.Compare(a[:], b[:]) <= 0 {
		return a, b
	}
	return b, a
}
