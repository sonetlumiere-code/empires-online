// Package unit modela las unidades del juego.
//
// Las estadísticas viven en un CATÁLOGO de datos, no repartidas por el código:
// añadir un tipo de unidad no debe obligar a tocar la lógica de movimiento ni la
// de combate. Ver ../../../../../docs/specs/unit.md
package unit

import (
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
)

// Status es el estado de una unidad.
type Status string

const (
	StatusIdle       Status = "IDLE"
	StatusMoving     Status = "MOVING"
	StatusGarrisoned Status = "GARRISONED"
	StatusHidden     Status = "HIDDEN"
	StatusDead       Status = "DEAD"
)

// Valid indica si el estado pertenece al conjunto conocido.
func (s Status) Valid() bool {
	switch s {
	case StatusIdle, StatusMoving, StatusGarrisoned, StatusHidden, StatusDead:
		return true
	}
	return false
}

// Type identifica un tipo de unidad del catálogo.
type Type string

const (
	// TypeVillager es la única unidad del MVP.
	TypeVillager Type = "VILLAGER"
)

// Definition son las estadísticas inmutables de un tipo de unidad.
type Definition struct {
	Type Type
	// MaxHP son los puntos de vida a pleno.
	MaxHP int32
	// BaseMsPerTile es lo que tarda en cruzar un tile de coste 1.0 sin diagonal.
	BaseMsPerTile int64
	// PopulationCost es cuánta población consume del límite de la ciudad.
	PopulationCost int32
}

// catalog es la tabla de definiciones. En el MVP vive en código porque sólo hay
// una unidad; cuando haya decenas se moverá a la base de datos sin cambiar la
// interfaz de consulta.
var catalog = map[Type]Definition{
	TypeVillager: {Type: TypeVillager, MaxHP: 40, BaseMsPerTile: 600, PopulationCost: 1},
}

// ErrUnknownType indica un tipo de unidad ausente del catálogo.
var ErrUnknownType = errors.New("tipo de unidad desconocido")

// Lookup devuelve la definición de un tipo de unidad.
func Lookup(t Type) (Definition, error) {
	d, ok := catalog[t]
	if !ok {
		return Definition{}, fmt.Errorf("%w: %q", ErrUnknownType, t)
	}
	return d, nil
}

// Unit es una unidad del mundo.
//
// X e Y son la posición autoritativa CONSOLIDADA. Mientras la unidad tiene un
// movimiento activo, la posición vigente se deriva de la polilínea: estos campos
// son la última posición confirmada, no necesariamente la actual.
type Unit struct {
	ID       int64
	PlayerID uuid.UUID
	CityID   *int64
	Type     Type
	X        int32
	Y        int32
	HP       int32
	MaxHP    int32
	Status   Status
}

// Tile devuelve la posición consolidada.
func (u *Unit) Tile() world.Tile { return world.Tile{X: u.X, Y: u.Y} }

// SetTile fija la posición consolidada.
func (u *Unit) SetTile(t world.Tile) { u.X, u.Y = t.X, t.Y }

// IsAlive indica si la unidad sigue en juego.
func (u *Unit) IsAlive() bool { return u.Status != StatusDead && u.HP > 0 }

// Errores de dominio. Cada uno se traduce a un código estable del protocolo en
// la capa de aplicación; el dominio no conoce el protocolo.
var (
	ErrNotOwned    = errors.New("la unidad no pertenece al jugador")
	ErrDead        = errors.New("la unidad está muerta")
	ErrGarrisoned  = errors.New("la unidad está guarnecida")
	ErrNotMovable  = errors.New("la unidad no puede moverse en su estado actual")
	ErrNotFound    = errors.New("unidad no encontrada")
	ErrPopulation  = errors.New("se alcanzó el límite de población")
	ErrInvalidType = errors.New("tipo de unidad inválido para esta operación")
)

// EnsureOwnedBy comprueba la propiedad de la unidad.
//
// Es la primera validación de TODO comando (INV-PLAYER-001). Se comprueba antes
// que nada para no filtrar, mediante mensajes de error distintos, la existencia
// de unidades ajenas.
func (u *Unit) EnsureOwnedBy(playerID uuid.UUID) error {
	if u.PlayerID != playerID {
		return ErrNotOwned
	}
	return nil
}

// EnsureCanMove comprueba que el estado de la unidad admite una orden de movimiento.
func (u *Unit) EnsureCanMove() error {
	switch u.Status {
	case StatusIdle, StatusMoving:
		// Moverse mientras se mueve es válido: reemplaza el movimiento anterior.
		return nil
	case StatusDead:
		return ErrDead
	case StatusGarrisoned:
		return ErrGarrisoned
	case StatusHidden:
		// Salir de una Safe Zone es legítimo: moverse revela la unidad.
		return nil
	default:
		return ErrNotMovable
	}
}
