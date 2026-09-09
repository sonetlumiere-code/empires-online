// Package player modela al jugador y sus dos ejes de identidad.
//
// Civilization (identidad cultural: unidades, tecnologías, bonos) y Faction
// (bando global: ORDER / CHAOS / NEUTRAL) son ORTOGONALES. Un jugador Roman puede
// militar en Chaos y un Norse en Order. Modelarlos como una sola entidad haría
// imposible esa combinación, así que son dos referencias independientes.
// Ver ../../../../../docs/specs/player.md
package player

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// Player es la identidad durable de un jugador.
type Player struct {
	ID             uuid.UUID
	Username       string
	CivilizationID int32
	FactionID      int32
	CreatedAt      time.Time
	LastSeenAt     *time.Time
}

// Civilization es una fila del catálogo de civilizaciones.
//
// Traits es un mapa de rasgos data-driven. Existe precisamente para que NO haya
// condicionales del tipo `if civilization == "ROMAN"` repartidos por el código:
// los sistemas consultan el rasgo que les concierne y aplican el modificador.
type Civilization struct {
	ID     int32
	Code   string
	Name   string
	Traits map[string]float64
}

// Trait devuelve el valor de un rasgo, o el valor por defecto si la civilización
// no lo define. Ésta es la ÚNICA vía admitida para consultar bonos culturales.
func (c Civilization) Trait(key string, def float64) float64 {
	if c.Traits == nil {
		return def
	}
	if v, ok := c.Traits[key]; ok {
		return v
	}
	return def
}

// Faction es un bando global del mundo.
type Faction struct {
	ID   int32
	Code string
	Name string
}

// Códigos de facción del MVP.
const (
	FactionOrder   = "ORDER"
	FactionChaos   = "CHAOS"
	FactionNeutral = "NEUTRAL"
)

// Errores de dominio.
var (
	ErrNotFound            = errors.New("jugador no encontrado")
	ErrUsernameTaken       = errors.New("el nombre de usuario ya está en uso")
	ErrInvalidUsername     = errors.New("nombre de usuario inválido")
	ErrAlreadyBootstrapped = errors.New("el jugador ya tiene su ciudad inicial")
)

// ValidateUsername aplica las reglas de nombre de usuario en el boundary.
func ValidateUsername(name string) error {
	if len(name) < 3 || len(name) > 24 {
		return ErrInvalidUsername
	}
	for _, r := range name {
		valid := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-'
		if !valid {
			return ErrInvalidUsername
		}
	}
	return nil
}
