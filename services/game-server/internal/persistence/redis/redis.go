// Package redis implementa el estado CALIENTE y transitorio.
//
// Regla que define este paquete (ADR-004): nada que viva aquí puede ser la única
// copia de un dato durable. Si Redis se vacía entero, el juego debe degradarse
// —sesiones cortadas, jugadores marcados offline— pero NO perder mundo.
package redis

import (
	"context"
	"fmt"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

// Client envuelve el cliente de Redis con los ajustes del servidor.
type Client struct {
	rdb *goredis.Client
}

// New abre la conexión y verifica que responde.
func New(ctx context.Context, url string) (*Client, error) {
	opts, err := goredis.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("EO_REDIS_URL inválida: %w", err)
	}
	opts.PoolSize = 20
	opts.MinIdleConns = 2
	opts.DialTimeout = 5 * time.Second
	opts.ReadTimeout = 3 * time.Second
	opts.WriteTimeout = 3 * time.Second

	rdb := goredis.NewClient(opts)

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := rdb.Ping(pingCtx).Err(); err != nil {
		_ = rdb.Close()
		return nil, fmt.Errorf("Redis no responde: %w", err)
	}
	return &Client{rdb: rdb}, nil
}

// Raw expone el cliente subyacente (tests de integración y usos avanzados).
func (c *Client) Raw() *goredis.Client { return c.rdb }

// Close cierra la conexión.
func (c *Client) Close() error { return c.rdb.Close() }

// Ping comprueba la conectividad. Lo usa el endpoint /ready.
func (c *Client) Ping(ctx context.Context) error { return c.rdb.Ping(ctx).Err() }
