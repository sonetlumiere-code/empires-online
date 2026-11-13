// Package caravan modela las caravanas que llevan la mercancía de un trato
// (SPEC-CARAVANS, ADR-037).
//
// Una caravana es una unidad del tipo unit.TypeCaravan: su posición, su
// movimiento y su vida son los de una unidad. Este paquete guarda lo que no
// cabe en ella: de qué trato nace, de dónde sale, adónde va y qué lleva. No
// escribe ni conoce el protocolo.
package caravan

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/economy"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/trade"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
)

// Status es el estado de una caravana.
type Status string

const (
	// StatusInTransit: en camino, con su unidad en el mapa.
	StatusInTransit Status = "IN_TRANSIT"
	// StatusDelivered: llegó y su carga entró en el almacén de su destino.
	StatusDelivered Status = "DELIVERED"
	// StatusLost: capturada sin ruta a la ciudad del captor; la carga se
	// perdió con ella (RN-CARAVAN-006).
	StatusLost Status = "LOST"
)

// Valid indica si el estado pertenece al catálogo.
func (s Status) Valid() bool {
	switch s {
	case StatusInTransit, StatusDelivered, StatusLost:
		return true
	}
	return false
}

// Statuses es el catálogo de estados, en orden estable (el CHECK de la base).
func Statuses() []Status { return []Status{StatusInTransit, StatusDelivered, StatusLost} }

// ErrInvalidCargo: una carga vacía, con una cantidad no positiva, un recurso
// fuera del catálogo o repetido.
var ErrInvalidCargo = errors.New("carga de caravana inválida")

// Caravan es una caravana. Su unidad (UnitID) lleva el dueño, la ciudad, la
// posición y la vida.
type Caravan struct {
	// ID es el de la fila de caravans.
	ID     int64
	UnitID int64
	// OfferID es el trato del que nace (INV-TRADE-007).
	OfferID int64
	// Sender es el jugador que la mandó: el que dio esta mitad del trato.
	Sender uuid.UUID
	// FromCity es la ciudad de la que salió; ToCity, adonde va ahora (la del
	// captor tras una captura); OriginalToCity, adonde iba al salir.
	FromCity       int64
	ToCity         int64
	OriginalToCity int64
	// Cargo es lo que lleva. No cambia en el camino (INV-TRADE-005).
	Cargo      economy.Costs
	Status     Status
	DepartedAt time.Time
}

// Captured indica si la caravana ya no va adonde iba al salir.
func (c Caravan) Captured() bool { return c.ToCity != c.OriginalToCity }

// ValidateCargo comprueba una carga: no vacía, cantidades positivas y cada
// recurso del catálogo una sola vez.
func ValidateCargo(cargo economy.Costs) error {
	if len(cargo) == 0 {
		return fmt.Errorf("%w: vacía", ErrInvalidCargo)
	}
	vistos := make(map[economy.Resource]bool, len(cargo))
	for _, c := range cargo {
		if !c.Resource.Valid() {
			return fmt.Errorf("%w: recurso %q fuera del catálogo", ErrInvalidCargo, c.Resource)
		}
		if c.Amount <= 0 {
			return fmt.Errorf("%w: %d de %s", ErrInvalidCargo, c.Amount, c.Resource)
		}
		if vistos[c.Resource] {
			return fmt.Errorf("%w: %s repetido", ErrInvalidCargo, c.Resource)
		}
		vistos[c.Resource] = true
	}
	return nil
}

// Half es una de las dos caravanas de un trato: quién la manda, de dónde a
// dónde y qué lleva.
type Half struct {
	Sender   uuid.UUID
	FromCity int64
	ToCity   int64
	Cargo    economy.Costs
}

// Halves son las dos caravanas de un trato aceptado, la de quien propuso
// primero (RN-CARAVAN-001, INV-TRADE-007): cada una lleva lo que da su parte,
// desde su ciudad hasta la del otro.
func Halves(o trade.Offer) [2]Half {
	return [2]Half{
		{Sender: o.Proposer, FromCity: o.ProposerCity, ToCity: o.TargetCity, Cargo: copyCosts(o.Give)},
		{Sender: o.Target, FromCity: o.TargetCity, ToCity: o.ProposerCity, Cargo: copyCosts(o.Want)},
	}
}

func copyCosts(cs economy.Costs) economy.Costs {
	return append(economy.Costs(nil), cs...)
}

// Reverse devuelve la ruta al revés: la de la caravana de vuelta
// (RN-CARAVAN-002). El A* no corta esquinas, así que lo que es transitable en
// un sentido lo es en el otro, y la ruta inversa de una más corta también lo es.
func Reverse(tiles []world.Tile) []world.Tile {
	out := make([]world.Tile, len(tiles))
	for i, t := range tiles {
		out[len(tiles)-1-i] = t
	}
	return out
}

// SeesCargo dice si un jugador ve la carga de una caravana (RN-CARAVAN-009):
// su dueño de ahora, quien la mandó y el destinatario original. Los demás ven la
// caravana, su dueño y su vida.
func SeesCargo(viewer, owner, sender, originalRecipient uuid.UUID) bool {
	if viewer == uuid.Nil {
		return false
	}
	return viewer == owner || viewer == sender || viewer == originalRecipient
}
