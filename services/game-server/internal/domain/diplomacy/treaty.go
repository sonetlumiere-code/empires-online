// Package diplomacy implementa el ciclo de vida de los tratados, quién puede
// proponerlos, responderlos, retirarlos y romperlos, y lo que autorizan: la paz
// y la guarnición.
//
// Es dominio puro. No consulta la base de datos ni el reloj: recibe el instante
// como argumento, lo que permite ejercitar la caducidad con un reloj falso y de
// forma determinista.
//
// Specs: ../../../../../docs/specs/garrison.md §6.1, ../../../../../docs/specs/diplomacy.md
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
	// ErrNoEsParte: quien da la orden no es uno de los dos firmantes.
	ErrNoEsParte = errors.New("no eres parte de ese tratado")
	// ErrNoPuede: la orden no corresponde a quien la da o al estado del tratado
	// (responder a la oferta propia, retirar la ajena, romper dos veces).
	ErrNoPuede = errors.New("esa orden no se puede dar sobre ese tratado")
	// ErrTipoNoProponible: el tipo no se puede proponer desde el cliente (uno
	// fuera del catálogo). Desde E10 los tres tipos son proponibles.
	ErrTipoNoProponible = errors.New("ese tipo de tratado no se puede proponer")
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
	// Los dos finales de una oferta que no llega a firmarse (SPEC-DIPLOMACY §4.2).
	StatusRejected  Status = "REJECTED"
	StatusWithdrawn Status = "WITHDRAWN"
)

// Valid indica si el estado es uno de los cuatro admitidos.
func (s Status) Valid() bool {
	switch s {
	case StatusProposed, StatusActive, StatusExpired, StatusBroken, StatusRejected, StatusWithdrawn:
		return true
	}
	return false
}

// transicionesPermitidas es la máquina de estados COMPLETA.
//
// Una oferta se acepta, se rechaza, se retira o caduca; un tratado vigente
// caduca o se rompe. Todo lo demás se rechaza, incluidas cosas que parecen
// inocuas: reactivar un tratado roto o volver a `PROPOSED`. Un tratado
// terminado es terminado; para volver a aliarse se firma uno nuevo, que además
// deja rastro de que hubo dos (SPEC-DIPLOMACY §4.2).
var transicionesPermitidas = map[Status]map[Status]bool{
	StatusProposed:  {StatusActive: true, StatusRejected: true, StatusWithdrawn: true, StatusExpired: true},
	StatusActive:    {StatusExpired: true, StatusBroken: true},
	StatusExpired:   {},
	StatusBroken:    {},
	StatusRejected:  {},
	StatusWithdrawn: {},
}

// Proposable indica si el tipo se puede proponer desde el cliente. Desde E10
// lo son los tres (SPEC-TRADE Q1).
func (t Type) Proposable() bool { return t.Valid() }

// Rank es el escalón del tipo en la escalera de tratados (SPEC-TRADE §4.1):
// cada escalón incluye lo del anterior. 0 para un tipo desconocido.
func (t Type) Rank() int {
	switch t {
	case TypeTrade:
		return 1
	case TypeNonAggression:
		return 2
	case TypeAlliance:
		return 3
	}
	return 0
}

// Supersedes indica si una oferta de este tipo puede convivir con un tratado
// vigente de tipo `current` y sustituirlo al aceptarse: sólo subiendo de
// escalón (INV-DIPLO-002, ADR-032 §5).
func (t Type) Supersedes(current Type) bool {
	return t.Rank() > 0 && current.Rank() > 0 && t.Rank() > current.Rank()
}

// GrantsGarrison es el flag que fija el tipo (Q1): la alianza deja guarnecer y
// el pacto no.
func (t Type) GrantsGarrison() bool { return t == TypeAlliance }

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
	// ProposedBy es quien propuso; nil en los tratados administrativos.
	ProposedBy *uuid.UUID
	// RequestID es el de la orden que lo propuso, para la idempotencia.
	RequestID string
	// BreakEffectiveAt, con valor, es el fin del preaviso de una ruptura: el
	// tratado sigue ACTIVE hasta entonces (RN-DIPLO-004).
	BreakEffectiveAt *time.Time
	// ClosedAt es cuándo se rechazó, retiró o caducó una oferta.
	ClosedAt *time.Time
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

// HasParty indica si el jugador es uno de los dos firmantes.
func (t Treaty) HasParty(p uuid.UUID) bool { return t.PlayerA == p || t.PlayerB == p }

// Other devuelve el otro firmante.
func (t Treaty) Other(p uuid.UUID) uuid.UUID {
	if t.PlayerA == p {
		return t.PlayerB
	}
	return t.PlayerA
}

// GivesPeaceAt indica si el tratado impide atacar y saquear entre los dos en
// `now` (RN-COMBAT-006, RN-COMBAT-020): un pacto o una alianza vigentes, sin la
// fecha de caducidad ni el fin de un preaviso alcanzados. Durante el preaviso la
// paz sigue (RN-DIPLO-004).
func (t Treaty) GivesPeaceAt(now time.Time) bool {
	if t.Status != StatusActive || (t.Type != TypeNonAggression && t.Type != TypeAlliance) {
		return false
	}
	return !t.endedAt(now)
}

// AllowsTradeAt indica si el tratado deja comerciar entre los dos en `now`
// (SPEC-TRADE §4.1, INV-TRADE-002): uno vigente de cualquier escalón, también
// durante su preaviso, sin su fin alcanzado.
func (t Treaty) AllowsTradeAt(now time.Time) bool {
	if t.Status != StatusActive || t.Type.Rank() == 0 {
		return false
	}
	return !t.endedAt(now)
}

// EndsAt devuelve cuándo deja de valer un tratado vigente por el reloj: el
// fin de su preaviso o su caducidad, lo que llegue antes; false si ninguno.
func (t Treaty) EndsAt() (time.Time, Status, bool) {
	var fin time.Time
	var estado Status
	if t.BreakEffectiveAt != nil {
		fin, estado = *t.BreakEffectiveAt, StatusBroken
	}
	if t.ExpiresAt != nil && (estado == "" || t.ExpiresAt.Before(fin)) {
		fin, estado = *t.ExpiresAt, StatusExpired
	}
	return fin, estado, estado != ""
}

func (t Treaty) endedAt(now time.Time) bool {
	fin, _, ok := t.EndsAt()
	return ok && !fin.After(now)
}

// CanRespond comprueba que `by` puede aceptar o rechazar la oferta: es el otro
// firmante, no quien la propuso (INV-DIPLO-001).
func (t Treaty) CanRespond(by uuid.UUID) error {
	if !t.HasParty(by) {
		return ErrNoEsParte
	}
	if t.Status != StatusProposed || t.ProposedBy == nil || *t.ProposedBy == by {
		return ErrNoPuede
	}
	return nil
}

// CanWithdraw comprueba que `by` puede retirar la oferta: sólo quien la propuso.
func (t Treaty) CanWithdraw(by uuid.UUID) error {
	if !t.HasParty(by) {
		return ErrNoEsParte
	}
	if t.Status != StatusProposed || t.ProposedBy == nil || *t.ProposedBy != by {
		return ErrNoPuede
	}
	return nil
}

// CanBreak comprueba que `by` puede romper el tratado: cualquiera de los dos,
// sobre uno vigente que no esté ya en preaviso (RN-DIPLO-003, RN-DIPLO-004).
func (t Treaty) CanBreak(by uuid.UUID) error {
	if !t.HasParty(by) {
		return ErrNoEsParte
	}
	if t.Status != StatusActive || t.BreakEffectiveAt != nil {
		return ErrNoPuede
	}
	return nil
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
	case StatusRejected, StatusWithdrawn:
		t.ClosedAt = &sello
	case StatusExpired:
		// Una oferta que caduca sella su cierre. Un tratado vigente no sella nada
		// nuevo: `expires_at` ya decía cuándo iba a pasar, y sobrescribirlo con el
		// instante en que el tick se dio cuenta borraría la única prueba de si el
		// servidor llegó tarde.
		if t.AcceptedAt == nil {
			t.ClosedAt = &sello
		}
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
	if t.ProposedBy != nil && !t.HasParty(*t.ProposedBy) {
		return fmt.Errorf("%w: quien propone no es uno de los firmantes", ErrTratadoInvalido)
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
