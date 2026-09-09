package redis

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
)

// TicketStore impide que un game ticket se canjee dos veces.
//
// El ticket es un JWT de vida muy corta. Aun así, un atacante que lo intercepte
// podría reutilizarlo dentro de su ventana de validez; consumir el `jti` de forma
// atómica cierra esa puerta. Ver ADR-010.
type TicketStore struct {
	client *Client
	ttl    time.Duration
}

// NewTicketStore crea el almacén de tickets consumidos.
func NewTicketStore(c *Client, ttl time.Duration) *TicketStore {
	return &TicketStore{client: c, ttl: ttl}
}

// Consume marca un jti como usado. Devuelve false si YA estaba usado, en cuyo
// caso el handshake debe rechazarse.
func (t *TicketStore) Consume(ctx context.Context, jti string) (bool, error) {
	ok, err := t.client.rdb.SetNX(ctx, "ticket:jti:"+jti, "1", t.ttl).Result()
	if err != nil {
		return false, fmt.Errorf("consumir el ticket %s: %w", jti, err)
	}
	return ok, nil
}

// IdempotencyStore deduplica comandos por requestId.
//
// Un cliente que reintenta tras un corte de red reenvía el mismo requestId. Sin
// esta capa, ese reintento produciría un segundo movimiento, una segunda compra o
// una segunda unidad. Ver INV-SEC-007.
type IdempotencyStore struct {
	client *Client
	ttl    time.Duration
}

// NewIdempotencyStore crea el almacén de idempotencia.
func NewIdempotencyStore(c *Client, ttl time.Duration) *IdempotencyStore {
	return &IdempotencyStore{client: c, ttl: ttl}
}

func idemKey(playerID uuid.UUID, requestID string) string {
	return "idem:" + playerID.String() + ":" + requestID
}

// ErrDuplicate indica que el requestId ya se procesó.
var ErrDuplicate = errors.New("requestId duplicado")

// Claim reserva un requestId. Devuelve:
//   - (true, "", nil)         si es la primera vez: el comando debe ejecutarse.
//   - (false, respuesta, nil) si es un duplicado: hay que devolver `respuesta`
//     y NO volver a ejecutar nada.
func (i *IdempotencyStore) Claim(ctx context.Context, playerID uuid.UUID, requestID string) (bool, string, error) {
	key := idemKey(playerID, requestID)
	// El marcador "pending" reserva la clave antes de ejecutar. Si dos copias del
	// mismo comando llegan a la vez, sólo una gana la reserva.
	ok, err := i.client.rdb.SetNX(ctx, key, "pending", i.ttl).Result()
	if err != nil {
		return false, "", fmt.Errorf("reservar idempotencia %s: %w", requestID, err)
	}
	if ok {
		return true, "", nil
	}
	prev, err := i.client.rdb.Get(ctx, key).Result()
	if errors.Is(err, goredis.Nil) {
		// Expiró entre el SetNX y el Get: se trata como primera vez.
		return true, "", nil
	}
	if err != nil {
		return false, "", fmt.Errorf("leer idempotencia %s: %w", requestID, err)
	}
	return false, prev, nil
}

// Complete guarda la respuesta asociada a un requestId ya reservado, para poder
// devolverla tal cual si el comando se repite.
func (i *IdempotencyStore) Complete(ctx context.Context, playerID uuid.UUID, requestID, response string) error {
	if err := i.client.rdb.Set(ctx, idemKey(playerID, requestID), response, i.ttl).Err(); err != nil {
		return fmt.Errorf("guardar respuesta idempotente %s: %w", requestID, err)
	}
	return nil
}

// Release libera la reserva de un comando que falló de forma transitoria, para
// que el cliente pueda reintentarlo con el mismo requestId.
func (i *IdempotencyStore) Release(ctx context.Context, playerID uuid.UUID, requestID string) error {
	return i.client.rdb.Del(ctx, idemKey(playerID, requestID)).Err()
}
