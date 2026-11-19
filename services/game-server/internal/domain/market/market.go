// Package market implementa la oferta pública del mercado: sus lotes, la
// custodia de lo ofrecido, la comisión, quién puede comprarla o retirarla y su
// ciclo de vida (SPEC-MARKETS, ADR-047).
//
// Es dominio puro. No consulta la base de datos ni el reloj: recibe el instante
// como argumento. Si hay mercado en la ciudad lo decide el catálogo de
// edificios; si alcanza el almacén, el paquete economy.
//
// Spec: ../../../../../docs/specs/markets.md
package market

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/economy"
)

// Límites de una oferta (SPEC-MARKETS §4.2, Q10).
const (
	// MaxLots es el máximo de lotes de una oferta.
	MaxLots int32 = 100
	// MaxOpenPerPlayer es el máximo de ofertas abiertas de un jugador.
	MaxOpenPerPlayer = 3
	// MaxTotal es la cota técnica del total de un lado, la de los tratos de E10
	// (ADR-032 §7).
	MaxTotal int64 = 1_000_000
	// CommissionDivisor: la comisión de un lote es la décima parte de lo que da
	// cada parte, redondeada hacia arriba (RN-MARKET-006).
	CommissionDivisor int64 = 10
)

var (
	// ErrOfertaInvalida: la oferta no cumple §4.2 (un recurso fuera del
	// catálogo, el mismo en los dos lados, cantidades o lotes fuera de rango).
	ErrOfertaInvalida = errors.New("oferta de mercado inválida")
	// ErrTotalExcesivo: un lado pasa de MaxTotal (INVALID_MESSAGE).
	ErrTotalExcesivo = errors.New("el total de la oferta pasa de la cota")
	// ErrPropia: comprar la oferta propia (INV-TRADE-010) o retirar la ajena.
	ErrPropia = errors.New("esa orden no se puede dar sobre esa oferta")
	// ErrCerrada: la oferta ya no está abierta.
	ErrCerrada = errors.New("la oferta ya no está abierta")
	// ErrSinLotes: no quedan tantos lotes (INV-TRADE-009).
	ErrSinLotes = errors.New("no quedan tantos lotes")
)

// Status es el estado de una oferta, con los valores del CHECK
// market_offers_status_valid.
type Status string

const (
	StatusOpen      Status = "OPEN"
	StatusSoldOut   Status = "SOLD_OUT"
	StatusWithdrawn Status = "WITHDRAWN"
	StatusExpired   Status = "EXPIRED"
)

// Valid indica si el estado es uno de los cuatro.
func (s Status) Valid() bool {
	switch s {
	case StatusOpen, StatusSoldOut, StatusWithdrawn, StatusExpired:
		return true
	}
	return false
}

// Statuses es el catálogo cerrado de estados, en orden estable.
func Statuses() []Status {
	return []Status{StatusOpen, StatusSoldOut, StatusWithdrawn, StatusExpired}
}

// Commission es la comisión de un lote que da `amount` de un recurso:
// ⌈amount / 10⌉ (RN-MARKET-006). Un lote de 1 paga 1.
func Commission(amount int64) int64 {
	if amount <= 0 {
		return 0
	}
	return (amount + CommissionDivisor - 1) / CommissionDivisor
}

// Offer es una oferta del mercado: cada lote da Give y pide Want.
type Offer struct {
	ID     int64
	Seller uuid.UUID
	CityID int64
	// Give y Want son lo que da y lo que pide cada lote.
	Give economy.Cost
	Want economy.Cost
	// LotsTotal son los publicados; LotsLeft, los que quedan en custodia. Al
	// cerrarse por retirada o caducidad, LotsLeft guarda los devueltos.
	LotsTotal int32
	LotsLeft  int32
	// SellerCommission es la comisión que pagó el vendedor al publicar, por
	// todos los lotes; no se devuelve nunca.
	SellerCommission int64
	Status           Status
	RequestID        string
	CreatedAt        time.Time
	ExpiresAt        time.Time
	ClosedAt         time.Time
}

// ValidateTerms comprueba lo que pide publicar: dos recursos del catálogo y
// distintos, cantidades por lote positivas, de 1 a MaxLots lotes y cada total
// dentro de MaxTotal (RN-MARKET-002).
func ValidateTerms(give, want economy.Cost, lots int32) error {
	if !give.Resource.Valid() || !want.Resource.Valid() {
		return fmt.Errorf("%w: recurso fuera del catálogo", ErrOfertaInvalida)
	}
	if give.Resource == want.Resource {
		return fmt.Errorf("%w: das y pides el mismo recurso", ErrOfertaInvalida)
	}
	if give.Amount <= 0 || want.Amount <= 0 {
		return fmt.Errorf("%w: cada lote da y pide al menos 1", ErrOfertaInvalida)
	}
	if lots < 1 || lots > MaxLots {
		return fmt.Errorf("%w: de 1 a %d lotes", ErrOfertaInvalida, MaxLots)
	}
	if give.Amount*int64(lots) > MaxTotal || want.Amount*int64(lots) > MaxTotal {
		return fmt.Errorf("%w: un lado pasa de %d", ErrTotalExcesivo, MaxTotal)
	}
	return nil
}

// New es una oferta que se va a publicar, abierta y con todos sus lotes.
func New(seller uuid.UUID, cityID int64, give, want economy.Cost, lots int32, requestID string, now time.Time, ttl time.Duration) (Offer, error) {
	if err := ValidateTerms(give, want, lots); err != nil {
		return Offer{}, err
	}
	return Offer{
		Seller: seller, CityID: cityID, Give: give, Want: want, LotsTotal: lots, LotsLeft: lots,
		SellerCommission: int64(lots) * Commission(give.Amount),
		Status:           StatusOpen, RequestID: requestID, CreatedAt: now, ExpiresAt: now.Add(ttl),
	}, nil
}

// PostCost es lo que sale del almacén del vendedor al publicar: la custodia de
// todos los lotes más su comisión (RN-MARKET-003).
func (o Offer) PostCost() economy.Cost {
	return economy.Cost{Resource: o.Give.Resource, Amount: int64(o.LotsTotal)*o.Give.Amount + o.SellerCommission}
}

// Custody es lo que guarda en custodia: los lotes restantes de una oferta
// abierta, cero en una cerrada (INV-TRADE-008).
func (o Offer) Custody() economy.Cost {
	if o.Status != StatusOpen {
		return economy.Cost{Resource: o.Give.Resource}
	}
	return economy.Cost{Resource: o.Give.Resource, Amount: int64(o.LotsLeft) * o.Give.Amount}
}

// IsOpen indica si la oferta sigue en el tablón.
func (o Offer) IsOpen() bool { return o.Status == StatusOpen }

// ExpiredAt indica si la oferta abierta llegó a su caducidad en `now`.
func (o Offer) ExpiredAt(now time.Time) bool {
	return o.Status == StatusOpen && !now.Before(o.ExpiresAt)
}

// Purchase es lo que mueve una compra de `Lots` lotes: lo que paga el
// comprador, su comisión y lo que recibe (RN-MARKET-004, RN-MARKET-006).
type Purchase struct {
	Lots int32
	// Pay es lo que el comprador manda al vendedor; Commission, lo que
	// además desaparece de su almacén.
	Pay        economy.Cost
	Commission int64
	// Receive es lo que sale de la custodia hacia el comprador.
	Receive economy.Cost
}

// Debit es lo que sale del almacén del comprador: lo que paga más su comisión.
func (p Purchase) Debit() economy.Cost {
	return economy.Cost{Resource: p.Pay.Resource, Amount: p.Pay.Amount + p.Commission}
}

// CanBuy decide si `buyer` puede comprar `lots` lotes cuando quedan `available`
// sin reservar (RN-MARKET-004, RN-MARKET-009, INV-TRADE-009, INV-TRADE-010).
func (o Offer) CanBuy(buyer uuid.UUID, lots, available int32) (Purchase, error) {
	if buyer == o.Seller {
		return Purchase{}, fmt.Errorf("%w: no puedes comprar tu propia oferta", ErrPropia)
	}
	if o.Status != StatusOpen {
		return Purchase{}, ErrCerrada
	}
	if lots < 1 || lots > MaxLots {
		return Purchase{}, fmt.Errorf("%w: de 1 a %d lotes", ErrOfertaInvalida, MaxLots)
	}
	if lots > available || lots > o.LotsLeft {
		return Purchase{}, fmt.Errorf("%w: quedan %d", ErrSinLotes, min(available, o.LotsLeft))
	}
	k := int64(lots)
	return Purchase{
		Lots:       lots,
		Pay:        economy.Cost{Resource: o.Want.Resource, Amount: k * o.Want.Amount},
		Commission: k * Commission(o.Want.Amount),
		Receive:    economy.Cost{Resource: o.Give.Resource, Amount: k * o.Give.Amount},
	}, nil
}

// Sell descuenta los lotes de una compra confirmada y la agota si eran los
// últimos (RN-MARKET-005).
func (o *Offer) Sell(lots int32, now time.Time) error {
	if o.Status != StatusOpen {
		return ErrCerrada
	}
	if lots < 1 || lots > o.LotsLeft {
		return fmt.Errorf("%w: quedan %d", ErrSinLotes, o.LotsLeft)
	}
	o.LotsLeft -= lots
	if o.LotsLeft == 0 {
		o.Status = StatusSoldOut
		o.ClosedAt = now
	}
	return nil
}

// CanWithdraw decide si `player` puede retirar la oferta (RN-MARKET-007).
func (o Offer) CanWithdraw(player uuid.UUID) error {
	if player != o.Seller {
		return fmt.Errorf("%w: sólo quien la publicó puede retirarla", ErrPropia)
	}
	if o.Status != StatusOpen {
		return ErrCerrada
	}
	return nil
}

// Close cierra una oferta abierta por retirada o caducidad y devuelve lo que
// vuelve al almacén del vendedor: la custodia restante (RN-MARKET-007,
// RN-MARKET-008). LotsLeft queda con los lotes devueltos.
func (o *Offer) Close(to Status, now time.Time) (economy.Cost, error) {
	if to != StatusWithdrawn && to != StatusExpired {
		return economy.Cost{}, fmt.Errorf("%w: no se cierra a %s", ErrOfertaInvalida, to)
	}
	if o.Status != StatusOpen {
		return economy.Cost{}, ErrCerrada
	}
	back := o.Custody()
	o.Status = to
	o.ClosedAt = now
	return back, nil
}

// Validate comprueba una oferta leída de la base (los CHECK de la migración
// 000028 y INV-TRADE-008, INV-TRADE-009).
func (o Offer) Validate() error {
	if !o.Status.Valid() {
		return fmt.Errorf("%w: estado %q", ErrOfertaInvalida, o.Status)
	}
	if err := ValidateTerms(o.Give, o.Want, o.LotsTotal); err != nil {
		return err
	}
	if o.LotsLeft < 0 || o.LotsLeft > o.LotsTotal {
		return fmt.Errorf("%w: %d lotes de %d", ErrOfertaInvalida, o.LotsLeft, o.LotsTotal)
	}
	if o.Status == StatusOpen && o.LotsLeft == 0 {
		return fmt.Errorf("%w: abierta sin lotes", ErrOfertaInvalida)
	}
	if o.Status == StatusSoldOut && o.LotsLeft != 0 {
		return fmt.Errorf("%w: agotada con lotes", ErrOfertaInvalida)
	}
	if o.SellerCommission != int64(o.LotsTotal)*Commission(o.Give.Amount) {
		return fmt.Errorf("%w: comisión %d", ErrOfertaInvalida, o.SellerCommission)
	}
	return nil
}
