package config

import (
	"encoding/json"
	"fmt"
	"log/slog"
)

// Secret es un valor de configuración que no puede aparecer en un log ni en un
// volcado de la configuración (INV-SEC-006): las URL de PostgreSQL y de Redis
// llevan credenciales, y EO_AUTH_JWT_SECRET permite falsificar tickets.
//
// Todas las formas de imprimirlo —fmt con cualquier verbo, slog y JSON— dan la
// redacción. El valor sólo sale con Reveal, que se llama en el punto exacto en
// que se usa. Así, el modo de fuga más común, loguear la estructura de
// configuración para depurar, imprime «[REDACTED]» y no la credencial.
type Secret struct {
	value string
}

// redacted es lo que se imprime en lugar de un secreto no vacío.
const redacted = "[REDACTED]"

// NewSecret envuelve un valor secreto.
func NewSecret(v string) Secret { return Secret{value: v} }

// Reveal devuelve el valor. Sólo para usarlo, nunca para imprimirlo.
func (s Secret) Reveal() string { return s.value }

// IsZero indica si el secreto está vacío.
func (s Secret) IsZero() bool { return s.value == "" }

// String devuelve la redacción; vacío si no hay secreto, para que un volcado
// distinga «no configurado» de «configurado».
func (s Secret) String() string {
	if s.value == "" {
		return ""
	}
	return redacted
}

// Format cubre todos los verbos de fmt, %#v y %x incluidos.
func (s Secret) Format(f fmt.State, _ rune) { _, _ = fmt.Fprint(f, s.String()) }

// LogValue redacta en slog.
func (s Secret) LogValue() slog.Value { return slog.StringValue(s.String()) }

// MarshalJSON redacta en JSON.
func (s Secret) MarshalJSON() ([]byte, error) { return json.Marshal(s.String()) }
