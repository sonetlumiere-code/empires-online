// Package trade implementa la oferta de comercio entre dos ciudades: sus
// líneas, quién puede responderla o retirarla, su ciclo de vida y lo que mueve
// un trato aceptado (SPEC-TRADE, ADR-032).
//
// Es dominio puro. No consulta la base de datos ni el reloj: recibe el instante
// como argumento. Qué tratado deja comerciar lo decide el paquete diplomacy
// (Treaty.AllowsTradeAt); si alcanzan los almacenes, el paquete economy.
//
// Spec: ../../../../../docs/specs/trade.md
package trade

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/economy"
)

// MaxAmount es la cota técnica de una línea (ADR-032 §7): no es una regla de
// juego, y el esquema del protocolo la impone igual.
const MaxAmount int64 = 1_000_000

var (
	// ErrOfertaInvalida: las líneas no cumplen §4.2 (vacías, cantidades fuera
	// de rango, un recurso repetido o en los dos lados).
	ErrOfertaInvalida = errors.New("oferta inválida")
	// ErrNoEsParte: quien da la orden no es ninguno de los dos jugadores.
	ErrNoEsParte = errors.New("no eres parte de esa oferta")
	// ErrNoPuede: la orden no corresponde a quien la da o al estado de la
	// oferta (aceptar la propia, retirar la ajena, responder a una cerrada).
	ErrNoPuede = errors.New("esa orden no se puede dar sobre esa oferta")
	// ErrTransicionInvalida: la máquina de estados no admite ese salto.
	ErrTransicionInvalida = errors.New("transición de oferta no permitida")
)

// Status es el estado de una oferta, con los valores del CHECK
// trade_offers_status_valid.
type Status string

const (
	StatusProposed  Status = "PROPOSED"
	StatusAccepted  Status = "ACCEPTED"
	StatusRejected  Status = "REJECTED"
	StatusWithdrawn Status = "WITHDRAWN"
	StatusExpired   Status = "EXPIRED"
)

// Valid indica si el estado es uno de los cinco.
func (s Status) Valid() bool {
	switch s {
	case StatusProposed, StatusAccepted, StatusRejected, StatusWithdrawn, StatusExpired:
		return true
	}
	return false
}

// Statuses es el catálogo cerrado de estados, en orden estable.
func Statuses() []Status {
	return []Status{StatusProposed, StatusAccepted, StatusRejected, StatusWithdrawn, StatusExpired}
}

// Side es el lado de una línea, con los valores del CHECK
// trade_offer_lines_side_valid.
type Side string

const (
	SideGive Side = "GIVE"
	SideWant Side = "WANT"
)

// Motivos de una caducidad, en el payload de TradeExpired.
const (
	ExpiryTimeout     = "TIMEOUT"
	ExpiryTreatyEnded = "TREATY_ENDED"
)

// Offer es una oferta «doy esto, pido aquello» de un jugador a otro.
type Offer struct {
	ID int64
	// Proposer ofrece y Target responde. En el MVP cada uno tiene una ciudad.
	Proposer     uuid.UUID
	Target       uuid.UUID
	ProposerCity int64
	TargetCity   int64
	// Give es lo que sale del almacén de Proposer; Want, lo que pide del de
	// Target.
	Give   economy.Costs
	Want   economy.Costs
	Status Status
	// RequestID es el de la orden que la propuso, para la idempotencia.
	RequestID string
	// AcceptedRequestID es el de la orden que la aceptó.
	AcceptedRequestID string
	CreatedAt         time.Time
	ExpiresAt         time.Time
	// ClosedAt es cuándo dejó de estar pendiente.
	ClosedAt *time.Time
}

// ValidateLines comprueba las listas de una oferta (SPEC-TRADE §4.2, Q3, Q4):
// las dos con algo, cantidades de 1 a MaxAmount, cada recurso del catálogo
// como mucho una vez y en un solo lado.
func ValidateLines(give, want economy.Costs) error {
	if len(give) == 0 || len(want) == 0 {
		return fmt.Errorf("%w: hay que dar algo y pedir algo", ErrOfertaInvalida)
	}
	seen := make(map[economy.Resource]bool, len(give)+len(want))
	for _, side := range []economy.Costs{give, want} {
		for _, l := range side {
			if !l.Resource.Valid() {
				return fmt.Errorf("%w: recurso %q fuera del catálogo", ErrOfertaInvalida, l.Resource)
			}
			if l.Amount < 1 || l.Amount > MaxAmount {
				return fmt.Errorf("%w: %d de %s (de 1 a %d)", ErrOfertaInvalida, l.Amount, l.Resource, MaxAmount)
			}
			if seen[l.Resource] {
				return fmt.Errorf("%w: %s aparece dos veces", ErrOfertaInvalida, l.Resource)
			}
			seen[l.Resource] = true
		}
	}
	return nil
}

// HasParty indica si el jugador es uno de los dos.
func (o Offer) HasParty(p uuid.UUID) bool { return o.Proposer == p || o.Target == p }

// Other devuelve al otro jugador.
func (o Offer) Other(p uuid.UUID) uuid.UUID {
	if o.Proposer == p {
		return o.Target
	}
	return o.Proposer
}

// Involves indica si la oferta es entre esos dos jugadores, en cualquier orden.
func (o Offer) Involves(x, y uuid.UUID) bool {
	return (o.Proposer == x && o.Target == y) || (o.Proposer == y && o.Target == x)
}

// CanRespond comprueba que `by` puede aceptar o rechazar: sólo el destinatario,
// y sólo una oferta pendiente (INV-TRADE-004).
func (o Offer) CanRespond(by uuid.UUID) error {
	if !o.HasParty(by) {
		return ErrNoEsParte
	}
	if o.Status != StatusProposed || o.Target != by {
		return ErrNoPuede
	}
	return nil
}

// CanWithdraw comprueba que `by` puede retirar: sólo quien ofreció, y sólo una
// oferta pendiente (INV-TRADE-004).
func (o Offer) CanWithdraw(by uuid.UUID) error {
	if !o.HasParty(by) {
		return ErrNoEsParte
	}
	if o.Status != StatusProposed || o.Proposer != by {
		return ErrNoPuede
	}
	return nil
}

// ExpiredAt indica si la oferta pendiente llegó a su plazo en `now`. El límite
// es inclusivo, como el de los tratados.
func (o Offer) ExpiredAt(now time.Time) bool {
	return o.Status == StatusProposed && !o.ExpiresAt.After(now)
}

// TransitionTo cierra la oferta: de PROPOSED a cualquiera de los cuatro
// finales, sellando closed_at. Devuelve una copia.
func (o Offer) TransitionTo(next Status, at time.Time) (Offer, error) {
	if o.Status != StatusProposed || !next.Valid() || next == StatusProposed {
		return o, fmt.Errorf("%w: %s → %s", ErrTransicionInvalida, o.Status, next)
	}
	sello := at.UTC()
	o.Status = next
	o.ClosedAt = &sello
	return o, nil
}

// For devuelve lo que la oferta haría salir del almacén de `viewer` y lo que
// haría entrar en él (SPEC-TRADE §6, la vista desde cada lado).
func (o Offer) For(viewer uuid.UUID) (give, get economy.Costs) {
	if viewer == o.Proposer {
		return o.Give, o.Want
	}
	return o.Want, o.Give
}

// Leg es lo que un trato aceptado hace con un recurso de una ciudad.
type Leg struct {
	CityID   int64
	Resource economy.Resource
	// Pays es lo que sale del almacén; Gets, lo que entra. Como un recurso está
	// en un solo lado de la oferta, en cada pata uno de los dos es cero.
	Pays int64
	Gets int64
}

// Legs son las patas de un trato aceptado, en orden de ciudad y de recurso.
// Lo que paga una ciudad en un recurso es exactamente lo que recibe la otra:
// salen de las mismas líneas (INV-TRADE-001).
func (o Offer) Legs() []Leg {
	legs := make([]Leg, 0, 2*(len(o.Give)+len(o.Want)))
	for _, l := range o.Give {
		legs = append(legs,
			Leg{CityID: o.ProposerCity, Resource: l.Resource, Pays: l.Amount},
			Leg{CityID: o.TargetCity, Resource: l.Resource, Gets: l.Amount})
	}
	for _, l := range o.Want {
		legs = append(legs,
			Leg{CityID: o.TargetCity, Resource: l.Resource, Pays: l.Amount},
			Leg{CityID: o.ProposerCity, Resource: l.Resource, Gets: l.Amount})
	}
	sort.Slice(legs, func(i, j int) bool {
		if legs[i].CityID != legs[j].CityID {
			return legs[i].CityID < legs[j].CityID
		}
		return legs[i].Resource < legs[j].Resource
	})
	return legs
}
