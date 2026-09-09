package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// DB es la superficie mínima común a *pgxpool.Pool y pgx.Tx.
//
// Los repositorios la reciben como parámetro en lugar de guardarse el pool: así
// la MISMA función sirve dentro y fuera de una transacción, sin duplicar consultas
// ni mantener dos rutas de código que puedan divergir.
type DB interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Comprobaciones en tiempo de compilación.
var (
	_ DB = (pgx.Tx)(nil)
)
