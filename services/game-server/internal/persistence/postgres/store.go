// Package postgres implementa la persistencia durable sobre PostgreSQL.
//
// PostgreSQL es la ÚNICA fuente de verdad durable del juego (ADR-003). Redis es
// estado caliente y su pérdida total no debe costar datos.
//
// Regla de capas: el dominio no importa este paquete. Es este paquete el que
// depende del dominio, nunca al revés.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store envuelve el pool de conexiones y expone los repositorios.
type Store struct {
	pool *pgxpool.Pool
}

// New abre el pool y verifica la conectividad. Falla rápido: arrancar sin base de
// datos sólo desplaza el error a un momento peor.
func New(ctx context.Context, url string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("EO_POSTGRES_URL inválida: %w", err)
	}
	cfg.MaxConns = 16
	cfg.MinConns = 2
	cfg.MaxConnLifetime = time.Hour
	cfg.MaxConnIdleTime = 15 * time.Minute
	cfg.HealthCheckPeriod = 30 * time.Second

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("no se pudo crear el pool de PostgreSQL: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("PostgreSQL no responde: %w", err)
	}
	return &Store{pool: pool}, nil
}

// Pool expone el pool para los repositorios y para los tests de integración.
func (s *Store) Pool() *pgxpool.Pool { return s.pool }

// Close libera el pool.
func (s *Store) Close() {
	if s.pool != nil {
		s.pool.Close()
	}
}

// Ping comprueba la conectividad. Lo usa el endpoint /ready.
func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

// InTx ejecuta fn dentro de una transacción y hace rollback ante cualquier error
// o pánico.
//
// Es la primitiva que garantiza la atomicidad de las operaciones compuestas: crear
// un jugador con su ciudad y sus tres aldeanos, o cancelar el movimiento anterior
// y crear el nuevo, ocurren enteras o no ocurren.
func (s *Store) InTx(ctx context.Context, fn func(pgx.Tx) error) (err error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("no se pudo iniciar la transacción: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback(context.WithoutCancel(ctx))
			panic(p)
		}
		if err != nil {
			// El rollback usa un contexto sin cancelar: si el original ya expiró,
			// aun así queremos deshacer la transacción y no dejarla colgando.
			_ = tx.Rollback(context.WithoutCancel(ctx))
		}
	}()

	if err = fn(tx); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("no se pudo confirmar la transacción: %w", err)
	}
	return nil
}

// Errores de persistencia traducidos al lenguaje del dominio.
var (
	// ErrNotFound: la fila no existe.
	ErrNotFound = errors.New("registro no encontrado")
	// ErrConflict: una restricción de unicidad rechazó la escritura. En el caso de
	// unit_movements es exactamente el mecanismo que hace cumplir INV-MOVE-001.
	ErrConflict = errors.New("conflicto de unicidad")
)

// IsUniqueViolation indica si el error es una violación de unicidad (SQLSTATE 23505).
func IsUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// IsCheckViolation indica si el error es una violación de CHECK (SQLSTATE 23514).
func IsCheckViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23514"
}

// normalize traduce errores de pgx a los errores estables de este paquete.
func normalize(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if IsUniqueViolation(err) {
		return fmt.Errorf("%w: %v", ErrConflict, err)
	}
	return err
}
