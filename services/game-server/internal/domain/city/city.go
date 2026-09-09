// Package city modela las ciudades y su estado de presencia/protección.
//
// La presencia de la CIUDAD es estado durable en PostgreSQL y la decide siempre el
// servidor. No debe confundirse con la presencia del JUGADOR, que es estado caliente
// en Redis con TTL. Ver ../../../../../docs/specs/presence.md
package city

import (
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
)

// PresenceState es el estado de presencia de una ciudad.
type PresenceState string

const (
	// PresenceOnline: su dueño tiene una sesión viva.
	PresenceOnline PresenceState = "ONLINE"
	// PresenceOfflinePending: el dueño se desconectó y corre el cooldown de seguridad.
	PresenceOfflinePending PresenceState = "OFFLINE_PENDING"
	// PresenceProtected: venció el cooldown; la ciudad está protegida.
	PresenceProtected PresenceState = "PROTECTED"
)

// Valid indica si el estado pertenece al conjunto conocido.
func (p PresenceState) Valid() bool {
	switch p {
	case PresenceOnline, PresenceOfflinePending, PresenceProtected:
		return true
	}
	return false
}

// Era identifica una era tecnológica. El límite de población depende de ella y se
// lee de la tabla `eras`: jamás está hardcodeado en la lógica de juego.
type Era string

const (
	EraStone  Era = "STONE_AGE"
	EraBronze Era = "BRONZE_AGE"
	EraIron   Era = "IRON_AGE"
	EraCastle Era = "CASTLE_AGE"
)

// EraDefinition es una fila del catálogo de eras.
type EraDefinition struct {
	Code          Era
	Name          string
	Ordinal       int32
	PopulationCap int32
}

// City es una ciudad del mundo.
type City struct {
	ID              int64
	OwnerPlayerID   uuid.UUID
	Name            string
	CenterX         int32
	CenterY         int32
	Era             Era
	Population      int32
	PopulationLimit int32
	PresenceState   PresenceState
	LastOnlineAt    *time.Time
	LastOfflineAt   *time.Time
	ProtectionUntil *time.Time
	Version         int32
}

// Center devuelve el tile central de la ciudad.
func (c *City) Center() world.Tile { return world.Tile{X: c.CenterX, Y: c.CenterY} }

// HasPopulationRoom indica si caben `n` puntos de población más.
func (c *City) HasPopulationRoom(n int32) bool { return c.Population+n <= c.PopulationLimit }

// IsProtected indica si la ciudad goza de protección por ausencia de su dueño.
func (c *City) IsProtected() bool { return c.PresenceState == PresenceProtected }

// Errores de dominio.
var (
	ErrNotFound          = errors.New("ciudad no encontrada")
	ErrProtected         = errors.New("la ciudad está protegida")
	ErrPopulationLimit   = errors.New("se alcanzó el límite de población de la ciudad")
	ErrInvalidTransition = errors.New("transición de presencia no permitida")
)

// CanTransition valida una transición del autómata de presencia.
//
// El autómata completo (INV-CITY-004):
//
//	ONLINE          -> OFFLINE_PENDING            (el dueño se desconecta y vence el grace)
//	OFFLINE_PENDING -> PROTECTED                  (vence el cooldown de seguridad)
//	OFFLINE_PENDING -> ONLINE                     (el dueño reconecta a tiempo)
//	PROTECTED       -> ONLINE                     (el dueño vuelve)
//
// Cualquier otra transición es un bug y debe rechazarse en lugar de aplicarse.
func CanTransition(from, to PresenceState) bool {
	if !from.Valid() || !to.Valid() {
		return false
	}
	if from == to {
		return true // idempotente: reaplicar el estado actual no es un error
	}
	switch from {
	case PresenceOnline:
		return to == PresenceOfflinePending
	case PresenceOfflinePending:
		return to == PresenceProtected || to == PresenceOnline
	case PresenceProtected:
		return to == PresenceOnline
	}
	return false
}

// NextStateOnDisconnect devuelve el estado inmediato tras perder la presencia.
func NextStateOnDisconnect(current PresenceState) PresenceState {
	if current == PresenceOnline {
		return PresenceOfflinePending
	}
	return current
}

// ShouldEngageProtection indica si una ciudad en OFFLINE_PENDING ya cumplió su
// cooldown y debe pasar a PROTECTED.
//
// La comparación usa el reloj inyectado del game loop, nunca time.Now().
func ShouldEngageProtection(state PresenceState, lastOfflineAt *time.Time, cooldown time.Duration, now time.Time) bool {
	if state != PresenceOfflinePending || lastOfflineAt == nil {
		return false
	}
	return !now.Before(lastOfflineAt.Add(cooldown))
}
