// Package diplomacy implementa el ciclo de vida de los tratados y la única
// regla que hoy depende de ellos: si autorizan o no una guarnición.
//
// Es dominio puro. No consulta la base de datos ni el reloj: recibe el instante
// como argumento, lo que permite ejercitar la caducidad con un reloj falso y de
// forma determinista.
//
// Spec: ../../../../../docs/specs/garrison.md §6.1
package diplomacy

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

var (
	// ErrTransicionInvalida: la máquina de estados no admite ese salto.
	ErrTransicionInvalida = errors.New("transición de tratado no permitida")
	// ErrTratadoInvalido: la fila no cumple sus propias restricciones.
	ErrTratadoInvalido = errors.New("tratado inválido")
)

// Type es el tipo de tratado. Los valores coinciden con el CHECK
// `treaties_type_valid` y no se traducen en ningún punto del sistema.
type Type string

const (
	TypeNonAggression Type = "NON_AGGRESSION"
	TypeAlliance      Type = "ALLIANCE"
	TypeTrade         Type = "TRADE"
)

// Valid indica si el tipo es uno de los tres admitidos.
func (t Type) Valid() bool {
	switch t {
	case TypeNonAggression, TypeAlliance, TypeTrade:
		return true
	}
	return false
}

// Status es el estado del tratado, con los valores del CHECK
// `treaties_status_valid`.
type Status string

const (
	StatusProposed Status = "PROPOSED"
	StatusActive   Status = "ACTIVE"
	StatusExpired  Status = "EXPIRED"
	StatusBroken   Status = "BROKEN"
)

// Valid indica si el estado es uno de los cuatro admitidos.
func (s Status) Valid() bool {
	switch s {
	case StatusProposed, StatusActive, StatusExpired, StatusBroken:
		return true
	}
	return false
}

// transicionesPermitidas es la máquina de estados COMPLETA.
//
// Sólo hay tres saltos legales y todo lo demás se rechaza, incluidas cosas que
// parecen inocuas: reactivar un tratado roto, caducar uno que nunca llegó a
// estar activo, o volver a `PROPOSED`. Un tratado terminado es terminado; para
// volver a aliarse se firma uno nuevo, que además deja rastro de que hubo dos.
var transicionesPermitidas = map[Status]map[Status]bool{
	StatusProposed: {StatusActive: true},
	StatusActive:   {StatusExpired: true, StatusBroken: true},
	StatusExpired:  {},
	StatusBroken:   {},
}

// Treaty es un tratado entre dos jugadores.
//
// El par va SIEMPRE en forma canónica (`PlayerA < PlayerB`), igual que en la
// base de datos: es lo que permite que una sola fila cubra las dos direcciones
// y que las consultas no tengan que hacer un `OR` de ambas.
type Treaty struct {
	ID             int64
	PlayerA        uuid.UUID
	PlayerB        uuid.UUID
	Type           Type
	Status         Status
	AllowsGarrison bool
	ProposedAt     time.Time
	AcceptedAt     *time.Time
	ExpiresAt      *time.Time
	BrokenAt       *time.Time
}

// CanonicalPair ordena dos identificadores de jugador.
//
// Toda consulta y toda construcción pasa por aquí (RN-GARR-007). La alternativa
// —buscar `(a,b) OR (b,a)`— no sólo es más lenta: hace posible insertar la misma
// relación dos veces, y entonces el índice único de "un tratado activo por par y
// tipo" deja de significar lo que dice.
func CanonicalPair(x, y uuid.UUID) (uuid.UUID, uuid.UUID) {
	if x.String() > y.String() {
		return y, x
	}
	return x, y
}

// Involves indica si el tratado es entre esos dos jugadores, en cualquier orden.
func (t Treaty) Involves(x, y uuid.UUID) bool {
	a, b := CanonicalPair(x, y)
	return t.PlayerA == a && t.PlayerB == b
}

// AuthorizesGarrison indica si este tratado habilita guarnición.
//
// RN-GARR-006: lo que autoriza es el par (estado, flag), NO el tipo de tratado.
// Los tres tipos autorizan por igual si llevan el flag, y `PROPOSED`, `EXPIRED`
// y `BROKEN` no autorizan nada aunque lo lleven. Un tratado propuesto y no
// aceptado no es un tratado: es una oferta.
func (t Treaty) AuthorizesGarrison() bool {
	return t.Status == StatusActive && t.AllowsGarrison
}

// HasExpired indica si un tratado activo ha rebasado su fecha de caducidad.
//
// El límite es inclusivo: un tratado que expira exactamente en `now` ya expiró.
// Un tratado sin `ExpiresAt` no caduca nunca por sí solo.
func (t Treaty) HasExpired(now time.Time) bool {
	if t.Status != StatusActive || t.ExpiresAt == nil {
		return false
	}
	return !t.ExpiresAt.After(now)
}

// CanTransitionTo indica si el salto de estado está permitido.
func (t Treaty) CanTransitionTo(next Status) bool {
	return transicionesPermitidas[t.Status][next]
}

// TransitionTo aplica el cambio de estado y sella la marca temporal que le
// corresponde.
//
// Devuelve una copia: el dominio no muta al llamante, para que un error a mitad
// de una transacción no deje el objeto en memoria contando una historia que la
// base de datos no confirmó.
func (t Treaty) TransitionTo(next Status, at time.Time) (Treaty, error) {
	if !next.Valid() {
		return t, fmt.Errorf("%w: estado %q desconocido", ErrTratadoInvalido, next)
	}
	if !t.CanTransitionTo(next) {
		return t, fmt.Errorf("%w: %s → %s", ErrTransicionInvalida, t.Status, next)
	}

	sello := at.UTC()
	t.Status = next
	switch next {
	case StatusActive:
		t.AcceptedAt = &sello
	case StatusBroken:
		t.BrokenAt = &sello
	case StatusExpired:
		// EXPIRED no sella nada nuevo: `expires_at` ya decía cuándo iba a pasar,
		// y sobrescribirlo con el instante en que el tick se dio cuenta borraría
		// la única prueba de si el servidor llegó tarde.
	}
	return t, nil
}

// Validate comprueba las restricciones que el esquema impone y las que no puede
// imponer.
func (t Treaty) Validate() error {
	if !t.Type.Valid() {
		return fmt.Errorf("%w: tipo %q desconocido", ErrTratadoInvalido, t.Type)
	}
	if !t.Status.Valid() {
		return fmt.Errorf("%w: estado %q desconocido", ErrTratadoInvalido, t.Status)
	}
	if t.PlayerA == t.PlayerB {
		return fmt.Errorf("%w: un jugador no firma tratados consigo mismo", ErrTratadoInvalido)
	}
	// El CHECK `treaties_canonical_pair` lo impone en la base; comprobarlo aquí
	// evita construir en memoria un tratado que la base rechazaría después, con
	// el error apareciendo lejos de quien lo creó mal.
	if t.PlayerA.String() > t.PlayerB.String() {
		return fmt.Errorf("%w: el par no está en orden canónico (%s > %s)",
			ErrTratadoInvalido, t.PlayerA, t.PlayerB)
	}
	if t.Status == StatusActive && t.AcceptedAt == nil {
		return fmt.Errorf("%w: un tratado ACTIVE exige accepted_at", ErrTratadoInvalido)
	}
	if t.Status == StatusBroken && t.BrokenAt == nil {
		return fmt.Errorf("%w: un tratado BROKEN exige broken_at", ErrTratadoInvalido)
	}
	return nil
}

// FindAuthorizing busca, entre un conjunto de tratados, el primero que autoriza
// guarnición entre dos jugadores.
//
// Recorre el slice en el orden recibido; el llamante es responsable de que ese
// orden sea estable —el repositorio ordena por `id`— porque de lo contrario dos
// ejecuciones podrían atribuir la autorización a tratados distintos y los logs
// dejarían de ser reproducibles.
func FindAuthorizing(treaties []Treaty, x, y uuid.UUID) (Treaty, bool) {
	for _, t := range treaties {
		if t.Involves(x, y) && t.AuthorizesGarrison() {
			return t, true
		}
	}
	return Treaty{}, false
}
