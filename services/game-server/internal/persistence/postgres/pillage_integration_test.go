//go:build integration

package postgres_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/economy"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/persistence/postgres"
)

// Persistencia del saqueo (SPEC-COMBAT-PILLAGE §7 y §9 «integration», ADR-026).

type escenarioSaqueo struct {
	units              *postgres.UnitRepo
	economia           *postgres.EconomyRepo
	victima, saqueador *postgres.BootstrapResult
}

func nuevoSaqueo(t *testing.T, ctx context.Context, store *postgres.Store, madera int64) escenarioSaqueo {
	t.Helper()
	_, _, units, moves, bootstrapper := newRepos(store)
	victima, err := bootstrapper.Create(ctx, bootstrapRequest("saqueada", world.Tile{X: 20, Y: 20}))
	require.NoError(t, err)
	saqueador, err := bootstrapper.Create(ctx, bootstrapRequest("saqueador", world.Tile{X: 60, Y: 20}))
	require.NoError(t, err)
	e := escenarioSaqueo{units: units, economia: postgres.NewEconomyRepo(store, units, moves), victima: victima, saqueador: saqueador}
	require.NoError(t, e.economia.FlushStocks(ctx, []postgres.StockDelta{debito(victima.City.ID, madera, 1)}))
	return e
}

func (e escenarioSaqueo) golpe(robado int32, llevaba, version int64) postgres.Pillaged {
	return postgres.Pillaged{
		AttackerID: e.saqueador.Units[0].ID, AttackerPlayer: e.saqueador.Player.ID,
		CityID: e.victima.City.ID, VictimPlayer: e.victima.Player.ID, Amount: robado,
		Stock: debito(e.victima.City.ID, llevaba-int64(robado), version), At: world.Tile{X: 18, Y: 18}, Tick: 42,
	}
}

func (e escenarioSaqueo) escribir(t *testing.T, ctx context.Context, store *postgres.Store, p postgres.Pillaged) (bool, error) {
	t.Helper()
	var escrito bool
	err := store.InTx(ctx, func(tx pgx.Tx) error {
		var err error
		escrito, err = e.units.Pillaged(ctx, tx, p)
		return err
	})
	return escrito, err
}

func eventosDeSaqueo(t *testing.T, ctx context.Context, store *postgres.Store, cityID int64) int {
	t.Helper()
	var n int
	require.NoError(t, store.Pool().QueryRow(ctx,
		`SELECT count(*) FROM world_events WHERE event_type = 'CityPillaged' AND (payload->>'cityId')::bigint = $1`,
		cityID).Scan(&n))
	return n
}

// ADR-026 §1 y §3: el golpe debita el stock de la víctima y deja un
// CityPillaged a su nombre. Reintentarlo con la misma versión no debita otra
// vez ni duplica el evento: es idempotente por la versión.
func TestUnGolpeDeSaqueoDebitaYEsIdempotentePorLaVersion(t *testing.T) {
	store, ctx := newTestStore(t)
	e := nuevoSaqueo(t, ctx, store, 100)
	p := e.golpe(5, 0, 2)

	escrito, err := e.escribir(t, ctx, store, p)
	require.NoError(t, err)
	assert.True(t, escrito)
	escrito, err = e.escribir(t, ctx, store, p)
	require.NoError(t, err)
	assert.False(t, escrito, "el reintento encuentra la versión ya escrita")

	amount, version := stockEnBase(t, ctx, store, e.victima.City.ID)
	assert.Equal(t, int64(95), amount)
	assert.Equal(t, int64(2), version)
	assert.Equal(t, 1, eventosDeSaqueo(t, ctx, store, e.victima.City.ID))

	var player uuid.UUID
	var attacker, cantidad string
	require.NoError(t, store.Pool().QueryRow(ctx,
		`SELECT player_id, payload->>'attackerId', payload->>'amount' FROM world_events
		 WHERE event_type = 'CityPillaged' AND (payload->>'cityId')::bigint = $1`, e.victima.City.ID).
		Scan(&player, &attacker, &cantidad))
	assert.Equal(t, e.victima.Player.ID, player)
	assert.Equal(t, "5", cantidad)
}

// ADR-026 §1: los depósitos sin volcar de la víctima viajan con el débito.
func TestElGolpeDeSaqueoLlevaLosDepositosSinVolcar(t *testing.T) {
	store, ctx := newTestStore(t)
	e := nuevoSaqueo(t, ctx, store, 100)

	_, err := e.escribir(t, ctx, store, e.golpe(5, 10, 3))
	require.NoError(t, err)
	amount, _ := stockEnBase(t, ctx, store, e.victima.City.ID)
	assert.Equal(t, int64(105), amount, "100 + 10 depositados − 5 robados")
}

// INV-ECON-001 e INV-ECON-002: un golpe que no cabe en el stock es un rechazo
// de dominio, y uno cuyo evento falla no deja el débito: nada a medias.
func TestUnGolpeDeSaqueoQueFallaNoDejaNadaAMedias(t *testing.T) {
	store, ctx := newTestStore(t)
	e := nuevoSaqueo(t, ctx, store, 3)

	_, err := e.escribir(t, ctx, store, e.golpe(5, 0, 2))
	require.ErrorIs(t, err, economy.ErrInsufficientResources)

	p := e.golpe(2, 0, 2)
	p.VictimPlayer = uuid.New() // FK de world_events: el evento falla
	_, err = e.escribir(t, ctx, store, p)
	require.Error(t, err)

	amount, version := stockEnBase(t, ctx, store, e.victima.City.ID)
	assert.Equal(t, int64(3), amount)
	assert.Equal(t, int64(1), version)
	assert.Zero(t, eventosDeSaqueo(t, ctx, store, e.victima.City.ID))
}
