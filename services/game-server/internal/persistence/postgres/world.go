package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
)

// WorldRepo persiste el estado global del mundo y el terreno por chunks.
type WorldRepo struct{ store *Store }

// NewWorldRepo crea el repositorio.
func NewWorldRepo(s *Store) *WorldRepo { return &WorldRepo{store: s} }

// State es la fila única de world_state.
type State struct {
	Seed        uint64
	Width       int32
	Height      int32
	ChunkSize   int32
	EpochMs     int64
	CurrentTick uint64
}

// LoadOrInit devuelve el estado del mundo, creándolo la primera vez.
//
// Si ya existe y sus parámetros NO coinciden con la configuración actual, falla en
// lugar de continuar: arrancar con una semilla o unas dimensiones distintas a las
// del mundo persistido produciría unidades sobre terreno que no existe.
func (r *WorldRepo) LoadOrInit(ctx context.Context, want State) (State, bool, error) {
	var got State
	err := r.store.pool.QueryRow(ctx,
		`SELECT seed, width, height, chunk_size, epoch_ms, current_tick FROM world_state WHERE id = 1`,
	).Scan(&got.Seed, &got.Width, &got.Height, &got.ChunkSize, &got.EpochMs, &got.CurrentTick)

	if err == nil {
		if got.Seed != want.Seed || got.Width != want.Width || got.Height != want.Height || got.ChunkSize != want.ChunkSize {
			return State{}, false, fmt.Errorf(
				"el mundo persistido (seed=%d %dx%d chunk=%d) no coincide con la configuración (seed=%d %dx%d chunk=%d): "+
					"cambiarlos exige un mundo nuevo o una migración de datos",
				got.Seed, got.Width, got.Height, got.ChunkSize,
				want.Seed, want.Width, want.Height, want.ChunkSize)
		}
		return got, false, nil
	}
	if normalize(err) != ErrNotFound {
		return State{}, false, fmt.Errorf("leer world_state: %w", err)
	}

	// Primer arranque: se crea el mundo.
	_, err = r.store.pool.Exec(ctx,
		`INSERT INTO world_state (id, seed, width, height, chunk_size, epoch_ms, current_tick)
		 VALUES (1, $1, $2, $3, $4, $5, 0)`,
		want.Seed, want.Width, want.Height, want.ChunkSize, want.EpochMs)
	if err != nil {
		return State{}, false, fmt.Errorf("crear world_state: %w", err)
	}
	want.CurrentTick = 0
	return want, true, nil
}

// SaveTick persiste el número de tick actual.
//
// No se llama en cada tick: forma parte del volcado periódico. Perder unos pocos
// ticks al reiniciar es inocuo, porque el tiempo del juego se ancla en epoch_ms
// y en el reloj real, no en este contador.
func (r *WorldRepo) SaveTick(ctx context.Context, tick uint64) error {
	_, err := r.store.pool.Exec(ctx, `UPDATE world_state SET current_tick = $1 WHERE id = 1`, tick)
	return err
}

// SaveChunks persiste el terreno completo del mundo, chunk a chunk.
//
// Se ejecuta una sola vez, cuando el mundo se genera por primera vez. El terreno
// es inmutable, así que después sólo se lee.
func (r *WorldRepo) SaveChunks(ctx context.Context, w *world.World) error {
	rows := make([][]any, 0, int(w.ChunksPerRow())*int(w.ChunksPerColumn()))
	for cy := int32(0); cy < w.ChunksPerColumn(); cy++ {
		for cx := int32(0); cx < w.ChunksPerRow(); cx++ {
			terrain, err := w.ChunkTerrain(cx, cy)
			if err != nil {
				return err
			}
			rows = append(rows, []any{cx, cy, terrain})
		}
	}

	_, err := r.store.pool.CopyFrom(ctx,
		pgx.Identifier{"world_chunks"},
		[]string{"chunk_x", "chunk_y", "terrain"},
		pgx.CopyFromRows(rows))
	if err != nil {
		return fmt.Errorf("persistir %d chunks: %w", len(rows), err)
	}
	return nil
}

// LoadTerrain reconstruye el terreno completo desde world_chunks.
//
// Existe para poder verificar que el mapa persistido coincide byte a byte con el
// que genera la semilla (INV-WORLD-005) y, en el futuro, para permitir mapas
// editados a mano que ya no deriven de una semilla.
func (r *WorldRepo) LoadTerrain(ctx context.Context, width, height, chunkSize int32) ([]byte, error) {
	rows, err := r.store.pool.Query(ctx,
		`SELECT chunk_x, chunk_y, terrain FROM world_chunks ORDER BY chunk_y, chunk_x`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	terrain := make([]byte, int(width)*int(height))
	expected := int(chunkSize) * int(chunkSize)
	seen := 0

	for rows.Next() {
		var cx, cy int32
		var chunk []byte
		if err := rows.Scan(&cx, &cy, &chunk); err != nil {
			return nil, err
		}
		if len(chunk) != expected {
			return nil, fmt.Errorf("el chunk (%d,%d) tiene %d bytes; se esperaban %d", cx, cy, len(chunk), expected)
		}
		baseX, baseY := cx*chunkSize, cy*chunkSize
		for row := int32(0); row < chunkSize; row++ {
			dst := int(baseY+row)*int(width) + int(baseX)
			copy(terrain[dst:dst+int(chunkSize)], chunk[int(row)*int(chunkSize):int(row+1)*int(chunkSize)])
		}
		seen++
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	wantChunks := int(width/chunkSize) * int(height/chunkSize)
	if seen != wantChunks {
		return nil, fmt.Errorf("world_chunks tiene %d chunks; se esperaban %d", seen, wantChunks)
	}
	return terrain, nil
}

// CountChunks indica cuántos chunks hay persistidos.
func (r *WorldRepo) CountChunks(ctx context.Context) (int64, error) {
	var n int64
	err := r.store.pool.QueryRow(ctx, `SELECT count(*) FROM world_chunks`).Scan(&n)
	return n, err
}
