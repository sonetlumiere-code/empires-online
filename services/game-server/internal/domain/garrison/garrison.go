// Package garrison implementa las condiciones de entrada y salida de una unidad
// guarnecida en una ciudad.
//
// Es dominio puro: decide, no ejecuta. No escribe en la base de datos, no emite
// mensajes y no conoce el protocolo — los códigos de error de red los asigna la
// capa de simulación a partir de los errores que este paquete devuelve, igual
// que hace con el movimiento.
//
// Spec: ../../../../../docs/specs/garrison.md §6.1 y §6.3
package garrison

import (
	"errors"
	"sort"

	"github.com/google/uuid"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/diplomacy"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
)

// Motivos de rechazo. Son valores del DOMINIO; su traducción a códigos del
// protocolo la hace quien atiende la petición.
var (
	// ErrNotOwned: quien pide no es el dueño de la unidad. El anfitrión nunca
	// adquiere control sobre lo que aloja (RN-GARR-001).
	ErrNotOwned = errors.New("la unidad no pertenece a quien la reclama")
	// ErrDead: una unidad muerta no se guarnece.
	ErrDead = errors.New("la unidad está muerta")
	// ErrAlreadyGarrisoned: ya está dentro de una ciudad.
	ErrAlreadyGarrisoned = errors.New("la unidad ya está guarnecida")
	// ErrNotIdle: sólo se entra desde IDLE. No existe "entrar en marcha"
	// (RN-GARR-002).
	ErrNotIdle = errors.New("la unidad no está inmóvil")
	// ErrCityNotFound: no hay ciudad con ese identificador.
	ErrCityNotFound = errors.New("la ciudad no existe")
	// ErrNotAdjacent: la unidad no toca la zona urbana (RN-GARR-003).
	ErrNotAdjacent = errors.New("la unidad no es adyacente a la ciudad")
	// ErrTreatyRequired: ciudad ajena sin tratado ACTIVE con allows_garrison
	// (RN-GARR-005).
	ErrTreatyRequired = errors.New("se requiere un tratado activo que autorice guarnición")
	// ErrNoReentryTile: no hay dónde dejar la unidad al salir. No es un fallo del
	// jugador: la salida queda pendiente y se reintenta (RN-GARR-014).
	ErrNoReentryTile = errors.New("no hay tile libre para reentrar")
)

// CanEnter decide si una unidad puede guarnecerse en una ciudad.
//
// El orden de las comprobaciones NO es arbitrario: primero lo que depende sólo
// de quien pide (propiedad), después el estado de la unidad, después la
// existencia y la geometría de la ciudad, y sólo al final el tratado. Así el
// error que recibe el jugador describe el primer obstáculo real y no filtra
// información sobre tratados a quien ni siquiera es dueño de la unidad.
//
// `treaties` debe venir en orden estable; el repositorio los devuelve por `id`.
func CanEnter(u *unit.Unit, c *city.City, requester uuid.UUID, treaties []diplomacy.Treaty) error {
	if u == nil {
		return ErrCityNotFound // no debería ocurrir; se trata como "nada que hacer"
	}
	if u.PlayerID != requester {
		return ErrNotOwned
	}

	switch u.Status {
	case unit.StatusIdle:
		// Única entrada válida.
	case unit.StatusDead:
		return ErrDead
	case unit.StatusGarrisoned:
		return ErrAlreadyGarrisoned
	default:
		return ErrNotIdle
	}
	if !u.IsAlive() {
		// El estado y los puntos de vida podrían discrepar tras una recuperación
		// parcial; se comprueban los dos porque una unidad con 0 HP marcada IDLE
		// no debe poder actuar sólo porque su `status` no se actualizó.
		return ErrDead
	}

	if c == nil {
		return ErrCityNotFound
	}
	if !c.IsAdjacent(u.X, u.Y) {
		return ErrNotAdjacent
	}

	// RN-GARR-004: la ciudad propia no exige nada más. Ni tratado, ni presencia,
	// ni la protección offline del anfitrión (RN-GARR-009): la protección es
	// contra agresión, no contra logística.
	if c.OwnerPlayerID == u.PlayerID {
		return nil
	}

	if _, ok := diplomacy.FindAuthorizing(treaties, u.PlayerID, c.OwnerPlayerID); !ok {
		return ErrTreatyRequired
	}
	return nil
}

// CanLeave decide si una unidad puede salir de la guarnición.
//
// La salida la ejecuta siempre el servidor (RN-GARR-010), pero sigue exigiendo
// propiedad: el anfitrión no puede echar a mano lo que aloja, y para eso está la
// expulsión diferida.
func CanLeave(u *unit.Unit, requester uuid.UUID) error {
	if u == nil {
		return ErrCityNotFound
	}
	if u.PlayerID != requester {
		return ErrNotOwned
	}
	if u.Status != unit.StatusGarrisoned {
		return ErrNotIdle
	}
	return nil
}

// Occupancy responde si un tile está ocupado por otra unidad o entidad.
type Occupancy func(x, y int32) bool

// ReentryTile elige el tile por el que sale una unidad guarnecida.
//
// RN-GARR-011: el primer tile transitable y desocupado del anillo exterior de la
// zona urbana, recorrido en orden ascendente por `(y, x)`. El criterio de
// desempate es determinista a propósito y es el mismo que usa el pathfinder
// (canon §8): sin él, dos ejecuciones del mismo escenario podrían sacar la unidad
// por lados distintos y ningún test de simulación sería reproducible.
//
// Devuelve ErrNoReentryTile si la ciudad está completamente rodeada. Eso NO es un
// error del jugador: la salida queda pendiente y se reintenta (RN-GARR-014).
// Colocar la unidad en un tile arbitrario rompería INV-UNIT-001.
func ReentryTile(w *world.World, c *city.City, occupied Occupancy) (world.Tile, error) {
	if w == nil || c == nil {
		return world.Tile{}, ErrCityNotFound
	}

	minX, minY, maxX, maxY := c.UrbanBounds()
	candidatos := make([]world.Tile, 0, 16)

	for y := minY - 1; y <= maxY+1; y++ {
		for x := minX - 1; x <= maxX+1; x++ {
			// Sólo el anillo: el interior es la zona amurallada.
			if x >= minX && x <= maxX && y >= minY && y <= maxY {
				continue
			}
			if !w.InBounds(x, y) || !w.IsWalkable(x, y) {
				continue
			}
			if occupied != nil && occupied(x, y) {
				continue
			}
			candidatos = append(candidatos, world.Tile{X: x, Y: y})
		}
	}

	if len(candidatos) == 0 {
		return world.Tile{}, ErrNoReentryTile
	}

	// El bucle ya recorre en orden (y, x), pero se ordena de forma explícita: si
	// alguien cambia el recorrido, el contrato de determinismo no debe depender
	// de que recuerde por qué el orden importaba.
	sort.Slice(candidatos, func(i, j int) bool {
		if candidatos[i].Y != candidatos[j].Y {
			return candidatos[i].Y < candidatos[j].Y
		}
		return candidatos[i].X < candidatos[j].X
	})
	return candidatos[0], nil
}
