//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/clock"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/safezone"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/pathfinding"
	"github.com/empires-online/empires-online/services/game-server/internal/persistence"
	"github.com/empires-online/empires-online/services/game-server/internal/persistence/postgres"
)

// Tests de integración de Safe Zones (docs/specs/safe-zones.md §13). Lo que
// sólo PostgreSQL puede garantizar: los CHECK de la tabla, el orden de carga y
// que HIDDEN sobrevive al ciclo dirty-flag → flush → reinicio.

func constraintDe(t *testing.T, err error) string {
	t.Helper()
	require.Error(t, err)
	var pgErr *pgconn.PgError
	require.True(t, errors.As(err, &pgErr), "se esperaba un error de PostgreSQL, llegó %v", err)
	return pgErr.ConstraintName
}

// INV-SAFE-005, mitad DB: el tipo y el orden de los límites los rechaza el
// motor, con el nombre exacto de la restricción.
func TestLosCheckDeSafeZonesRechazanTipoYLimitesInvalidos(t *testing.T) {
	store, ctx := newTestStore(t)
	repo := postgres.NewSafeZoneRepo(store)

	_, err := repo.Insert(ctx, store.Pool(), safezone.Zone{
		Name: "pantano", Type: "SWAMP", MinX: 0, MinY: 0, MaxX: 1, MaxY: 1,
	})
	assert.Equal(t, "safe_zones_type_valid", constraintDe(t, err))

	_, err = repo.Insert(ctx, store.Pool(), safezone.Zone{
		Name: "al revés", Type: safezone.DenseForest, MinX: 5, MinY: 0, MaxX: 2, MaxY: 1,
	})
	assert.Equal(t, "safe_zones_bounds_ordered", constraintDe(t, err))

	// Un rectángulo de un solo tile es válido: los bordes son inclusivos.
	_, err = repo.Insert(ctx, store.Pool(), safezone.Zone{
		Name: "punto", Type: safezone.Cavern, MinX: 3, MinY: 3, MaxX: 3, MaxY: 3,
	})
	require.NoError(t, err)

	zonas, err := repo.LoadAll(ctx)
	require.NoError(t, err)
	assert.Len(t, zonas, 1, "las filas rechazadas no dejan rastro")
}

// RN-SAFE-006: la carga sale en orden ascendente de id y la ida y vuelta por la
// base conserva todos los campos.
func TestLasZonasSeCarganEnOrdenDeIDYSinPerderCampos(t *testing.T) {
	store, ctx := newTestStore(t)
	repo := postgres.NewSafeZoneRepo(store)

	escritas := []safezone.Zone{
		{Name: "Bosque Norte", Type: safezone.DenseForest, MinX: 10, MinY: 11, MaxX: 12, MaxY: 13},
		{Name: "Gruta", Type: safezone.Cavern, MinX: 40, MinY: 20, MaxX: 43, MaxY: 22},
		{Name: "Bosque Sur", Type: safezone.DenseForest, MinX: 0, MinY: 50, MaxX: 5, MaxY: 63},
	}
	for i := range escritas {
		id, err := repo.Insert(ctx, store.Pool(), escritas[i])
		require.NoError(t, err)
		escritas[i].ID = id
	}

	leidas, err := repo.LoadAll(ctx)
	require.NoError(t, err)
	assert.Equal(t, escritas, leidas)
	for i := 1; i < len(leidas); i++ {
		assert.Less(t, leidas[i-1].ID, leidas[i].ID)
	}
}

// inmediato ejecuta las escrituras durables al instante, para que el test no
// dependa de los tiempos de los workers.
type inmediato struct {
	errs []error
	jobs []string
}

func (p *inmediato) Submit(job simulation.Job) {
	p.jobs = append(p.jobs, job.Name)
	if err := job.Run(context.Background()); err != nil {
		p.errs = append(p.errs, err)
	}
}
func (p *inmediato) Depth() int { return 0 }

// arrancar reproduce el orden de arranque de cmd/server: hidratar desde la
// base, amurallar las ciudades y SÓLO después construir el índice de zonas.
func arrancar(t *testing.T, ctx context.Context, store *postgres.Store, w *world.World, clk clock.Clock) (*simulation.Simulation, *inmediato) {
	t.Helper()
	_, cities, units, movements, _ := newRepos(store)

	vivas, err := units.ListAlive(ctx)
	require.NoError(t, err)
	ciudades, err := cities.ListAll(ctx)
	require.NoError(t, err)

	state := simulation.NewState(w)
	simulation.Hydrate(state, vivas, ciudades, nil, clk.NowMs(), discardLogger())
	for _, c := range ciudades {
		w.SetBlocked(c.CenterX-1, c.CenterY-1, c.CenterX+1, c.CenterY+1, true)
	}

	zonas, err := postgres.NewSafeZoneRepo(store).LoadAll(ctx)
	require.NoError(t, err)
	idx, _, err := safezone.BuildIndex(zonas, w)
	require.NoError(t, err)
	state.SetSafeZones(idx)

	p := &inmediato{}
	sim := simulation.New(state, simulation.Deps{
		Clock:      clk,
		Pathfinder: pathfinding.NewAStar(20000, 256),
		Persister:  p,
		Repos:      persistence.NewGameStore(store, units, cities, movements),
		Log:        discardLogger(),

		ProtectionCooldown: 300 * time.Second,
		DisconnectGrace:    30 * time.Second,
	})
	return sim, p
}

func mundoConBosque(t *testing.T) *world.World {
	t.Helper()
	terrain := make([]byte, 64*64)
	for i := range terrain {
		terrain[i] = byte(world.Grassland)
	}
	// Bosque de (22,18) a (25,22): cubre el aldeano que nace en (22,20).
	for y := 18; y <= 22; y++ {
		for x := 22; x <= 25; x++ {
			terrain[y*64+x] = byte(world.Forest)
		}
	}
	w, err := world.New(64, 64, 32, 1, terrain)
	require.NoError(t, err)
	return w
}

func estadoEnBase(t *testing.T, ctx context.Context, store *postgres.Store, unitID int64) unit.Status {
	t.Helper()
	var s string
	require.NoError(t, store.Pool().QueryRow(ctx, `SELECT status FROM units WHERE id = $1`, unitID).Scan(&s))
	return unit.Status(s)
}

// Recuperación de extremo a extremo contra PostgreSQL real: HIDDEN se persiste
// por dirty-flag, sobrevive a un reinicio y, si la zona desaparece de la tabla,
// el primer tick tras el arranque lo revierte a IDLE (transición T8).
func TestHiddenSePersisteSobreviveAlReinicioYSeRevierteSiLaZonaDesaparece(t *testing.T) {
	store, ctx := newTestStore(t)
	_, _, _, _, bootstrapper := newRepos(store)
	clk := clock.NewFakeClock(testEpoch)

	alta, err := bootstrapper.Create(ctx, bootstrapRequest("ermitano", world.Tile{X: 20, Y: 20}))
	require.NoError(t, err)
	var enBosque int64
	for _, u := range alta.Units {
		if u.X == 22 && u.Y == 20 {
			enBosque = u.ID
		}
	}
	require.NotZero(t, enBosque, "el escenario necesita un aldeano en (22,20)")

	zonaID, err := postgres.NewSafeZoneRepo(store).Insert(ctx, store.Pool(), safezone.Zone{
		Name: "Espesura", Type: safezone.DenseForest, MinX: 22, MinY: 18, MaxX: 25, MaxY: 22,
	})
	require.NoError(t, err)

	// ── Primer arranque: la fase 5 la oculta y el flush lo escribe ──
	sim, p := arrancar(t, ctx, store, mundoConBosque(t), clk)
	sim.ProcessTimers(clk.Now())
	u, _ := sim.State().Unit(enBosque)
	require.Equal(t, unit.StatusHidden, u.Status)
	assert.Equal(t, unit.StatusIdle, estadoEnBase(t, ctx, store, enBosque),
		"el tick no escribe: hasta el flush la base sigue diciendo IDLE")

	sim.FlushDirty()
	require.Empty(t, p.errs)
	assert.Equal(t, unit.StatusHidden, estadoEnBase(t, ctx, store, enBosque), "CHECK units_status_valid admite HIDDEN")

	// ── Reinicio: la unidad vuelve oculta y la primera fase 5 no la toca ──
	sim, _ = arrancar(t, ctx, store, mundoConBosque(t), clk)
	u, _ = sim.State().Unit(enBosque)
	require.Equal(t, unit.StatusHidden, u.Status)
	sim.ProcessTimers(clk.Now())
	assert.Equal(t, unit.StatusHidden, u.Status)

	// ── La zona deja de existir y el servidor se reinicia ──
	_, err = store.Pool().Exec(ctx, `DELETE FROM safe_zones WHERE id = $1`, zonaID)
	require.NoError(t, err)
	sim, p = arrancar(t, ctx, store, mundoConBosque(t), clk)
	sim.ProcessTimers(clk.Now().Add(100 * time.Millisecond))
	u, _ = sim.State().Unit(enBosque)
	assert.Equal(t, unit.StatusIdle, u.Status)
	sim.FlushDirty()
	require.Empty(t, p.errs)
	assert.Equal(t, unit.StatusIdle, estadoEnBase(t, ctx, store, enBosque))
}

// INV-SAFE-006 sobre datos reales: una zona cuya caja cubre una ciudad
// sembrada no incluye ninguno de sus tiles amurallados.
func TestUnaZonaCargadaSobreUnaCiudadExcluyeSuMuralla(t *testing.T) {
	store, ctx := newTestStore(t)
	_, _, _, _, bootstrapper := newRepos(store)
	clk := clock.NewFakeClock(testEpoch)

	_, err := bootstrapper.Create(ctx, bootstrapRequest("lenador", world.Tile{X: 23, Y: 20}))
	require.NoError(t, err)
	_, err = postgres.NewSafeZoneRepo(store).Insert(ctx, store.Pool(), safezone.Zone{
		Name: "Bosque urbano", Type: safezone.DenseForest, MinX: 22, MinY: 18, MaxX: 25, MaxY: 22,
	})
	require.NoError(t, err)

	sim, _ := arrancar(t, ctx, store, mundoConBosque(t), clk)
	for y := int32(19); y <= 21; y++ {
		for x := int32(22); x <= 24; x++ {
			_, ok := sim.State().SafeZoneAt(x, y)
			assert.False(t, ok, "(%d,%d) es muralla", x, y)
		}
	}
	_, ok := sim.State().SafeZoneAt(25, 18)
	assert.True(t, ok, "el resto de la zona sigue en el índice")
}

// Recovery de la presencia (M5, aceptación 7). Un reinicio no pierde el
// presence_state ni reinicia el cooldown: la simulación rehidratada calcula el
// vencimiento desde el last_offline_at persistido, no desde el arranque.
func TestUnReinicioNoReiniciaElCooldownDeProteccion(t *testing.T) {
	store, ctx := newTestStore(t)
	_, cities, _, _, bootstrapper := newRepos(store)
	clk := clock.NewFakeClock(testEpoch)

	alta, err := bootstrapper.Create(ctx, bootstrapRequest("ausente", world.Tile{X: 20, Y: 20}))
	require.NoError(t, err)
	desconexion := clk.Now()
	require.NoError(t, cities.SetPresence(ctx, store.Pool(), alta.City.ID, city.PresenceOfflinePending, desconexion))

	// El proceso cae y vuelve a arrancar 200 s después de la desconexión.
	clk.AdvanceMs(200_000)
	sim, p := arrancar(t, ctx, store, testWorld(t), clk)
	c, ok := sim.State().City(alta.City.ID)
	require.True(t, ok)
	require.Equal(t, city.PresenceOfflinePending, c.PresenceState, "el estado sobrevive al reinicio")

	// A los 299 s de la desconexión todavía no: si el cooldown contara desde el
	// arranque, faltarían 201 s y no 1.
	sim.ProcessTimers(desconexion.Add(299 * time.Second))
	require.Equal(t, city.PresenceOfflinePending, c.PresenceState)

	sim.ProcessTimers(desconexion.Add(300 * time.Second))
	require.Equal(t, city.PresenceProtected, c.PresenceState)
	require.Empty(t, p.errs)

	releida, err := cities.GetByID(ctx, alta.City.ID)
	require.NoError(t, err)
	assert.Equal(t, city.PresenceProtected, releida.PresenceState, "la transición se persistió")
}
