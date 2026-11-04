// Package economy modela los recursos y el stock de las ciudades.
//
// Es dominio puro: no lee el reloj, no escribe y no conoce el protocolo. Los
// costes de cada cosa viven en el catálogo de quien la tiene (unit.Definition,
// building.Definition); aquí sólo están el recurso, el coste como valor y el
// almacén con sus dos operaciones.
//
// Specs: ../../../../../docs/specs/economy-wood.md §4.1 y economy-stone.md ·
// ADR-023 · ADR-030.
package economy

import (
	"errors"
	"fmt"
)

// Resource identifica un recurso.
type Resource string

const (
	// ResourceWood es la madera del bosque (SPEC-ECON-WOOD).
	ResourceWood Resource = "WOOD"
	// ResourceStone es la piedra de la colina (SPEC-ECON-STONE).
	ResourceStone Resource = "STONE"
)

// Valid indica si el recurso pertenece al catálogo. Es el mismo conjunto que
// el CHECK city_stocks_resource_valid (migraciones 000006 y 000012).
func (r Resource) Valid() bool { return r == ResourceWood || r == ResourceStone }

// Resources es el catálogo cerrado de recursos, en orden estable.
func Resources() []Resource { return []Resource{ResourceWood, ResourceStone} }

// Cost es lo que cuesta algo, en un recurso.
type Cost struct {
	Resource Resource
	Amount   int64
}

// Costs es lo que cuesta algo que paga en varios recursos, cada uno como
// mucho una vez (ADR-030 §3). Se debita entero o no se debita
// (INV-ECON-002).
type Costs []Cost

// Validate comprueba que cada recurso es del catálogo, aparece una sola vez y
// tiene una cantidad no negativa.
func (cs Costs) Validate() error {
	seen := make(map[Resource]bool, len(cs))
	for _, c := range cs {
		if !c.Resource.Valid() || c.Amount < 0 || seen[c.Resource] {
			return fmt.Errorf("%w: %d de %q en un coste de %d recursos", ErrInvalidAmount, c.Amount, c.Resource, len(cs))
		}
		seen[c.Resource] = true
	}
	return nil
}

// CanAfford indica si los stocks cubren TODOS los recursos del coste. `stock`
// devuelve el stock de un recurso, o nil si la ciudad no lo tiene, que no
// cubre nada.
func (cs Costs) CanAfford(stock func(Resource) *Stock) bool {
	if cs.Validate() != nil {
		return false
	}
	for _, c := range cs {
		s := stock(c.Resource)
		if s == nil || !s.CanAfford(c) {
			return false
		}
	}
	return true
}

// Errores de dominio. La capa de aplicación los traduce al catálogo de
// códigos; el dominio no conoce el protocolo.
var (
	// ErrInsufficientResources: el stock no alcanza para el coste
	// (INSUFFICIENT_RESOURCES).
	ErrInsufficientResources = errors.New("recursos insuficientes")
	// ErrInvalidAmount: una cantidad negativa, o de otro recurso.
	ErrInvalidAmount = errors.New("cantidad inválida")
)

// Stock es el almacén de un recurso de una ciudad.
//
// Version sube con cada cambio: es la que acompaña a cada escritura durable, de
// modo que aplicar dos veces la misma escritura no haga nada y una vieja no
// pise a una nueva (ADR-023 §1).
type Stock struct {
	Resource Resource
	Amount   int64
	Version  int64
}

// NewStock crea un stock vacío del recurso.
func NewStock(r Resource) Stock { return Stock{Resource: r} }

// CanAfford indica si el stock cubre el coste. Un coste de otro recurso o
// negativo no se cubre nunca.
func (s *Stock) CanAfford(c Cost) bool {
	return c.Resource == s.Resource && c.Amount >= 0 && s.Amount >= c.Amount
}

// Debit resta el coste. INV-ECON-001: si no alcanza, devuelve
// ErrInsufficientResources y NO muta nada; el stock no baja nunca de cero.
func (s *Stock) Debit(c Cost) error {
	if c.Resource != s.Resource || c.Amount < 0 {
		return fmt.Errorf("%w: %d de %s contra un stock de %s", ErrInvalidAmount, c.Amount, c.Resource, s.Resource)
	}
	if s.Amount < c.Amount {
		return fmt.Errorf("%w: hacen falta %d de %s y hay %d", ErrInsufficientResources, c.Amount, c.Resource, s.Amount)
	}
	s.Amount -= c.Amount
	s.Version++
	return nil
}

// Credit suma una cantidad: un depósito, o la devolución de una reserva que
// no se confirmó (RN-ECON-008).
func (s *Stock) Credit(n int64) error {
	if n < 0 {
		return fmt.Errorf("%w: no se acredita una cantidad negativa (%d)", ErrInvalidAmount, n)
	}
	s.Amount += n
	s.Version++
	return nil
}
