//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/territory"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/persistence/postgres"
)

// Integración de la conquista (SPEC-TERRITORY-CONQUEST §9 «integration»,
// ADR-034): la reclamación con su fila y su disputa, el índice de una activa por
// territorio, la captura en una transacción con el control, los dos límites y su
// evento, y el registro de cada lado, contra PostgreSQL real.
//
// Mundo de pruebas de 64 × 64 con cuatro territorios de 32 × 32. La dueña funda
// en (10,10) y el conquistador en (50,50): cada uno es dueño de su capital, y el
// territorio de (40,10) queda libre.

type conquista struct {
	repo          *postgres.ConquestRepo
	territorios   *postgres.TerritoryRepo
	duena, rival  *postgres.BootstrapResult
	capitalDuena  territory.Territory
	libre         territory.Territory
	instanteDeHoy time.Time
}

func territorioEn(t *testing.T, ts []territory.Territory, x, y int32) territory.Territory {
	t.Helper()
	for _, tr := range ts {
		if tr.Contains(x, y) {
			return tr
		}
	}
	t.Fatalf("ningún territorio contiene (%d,%d)", x, y)
	return territory.Territory{}
}

func nuevaConquista(t *testing.T, ctx context.Context, store *postgres.Store) conquista {
	t.Helper()
	repo, sembrados := seedTerritories(t, ctx, store)
	_, _, _, _, bootstrapper := newRepos(store)
	// Cada uno funda en su capital, que pasa a ser suya (RN-TERR-007).
	alta := func(nombre string, centro world.Tile) *postgres.BootstrapResult {
		req := bootstrapRequest(nombre, centro)
		req.TerritoryID = territorioEn(t, sembrados, centro.X, centro.Y).ID
		r, err := bootstrapper.Create(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, r.TerritoryControl, "la capital de %s es suya", nombre)
		return r
	}
	duena := alta("duena", world.Tile{X: 10, Y: 10})
	rival := alta("rival", world.Tile{X: 50, Y: 50})
	return conquista{
		repo: postgres.NewConquestRepo(store), territorios: repo, duena: duena, rival: rival,
		capitalDuena: territorioEn(t, sembrados, 10, 10), libre: territorioEn(t, sembrados, 40, 10),
		instanteDeHoy: time.Date(2026, 11, 4, 18, 0, 0, 0, time.UTC),
	}
}

func (c conquista) reclamacion(quien *postgres.BootstrapResult, tr territory.Territory, dueno *uuid.UUID, req string) postgres.ClaimStart {
	return postgres.ClaimStart{
		Claim: territory.Claim{
			TerritoryID: tr.ID, UnitID: quien.Units[0].ID, PlayerID: quien.Player.ID, CityID: quien.City.ID,
			OwnerPlayerID: dueno, X: 40, Y: 10, StartedAtMs: 1_000, CompletesAtMs: 301_000, RequestID: req,
		},
		TerritoryName: "Este", Tick: 3,
	}
}

func controlEnBase(t *testing.T, ctx context.Context, repo *postgres.TerritoryRepo, store *postgres.Store, id int64) territory.Control {
	t.Helper()
	c, err := repo.GetControl(ctx, store.Pool(), id)
	require.NoError(t, err)
	return c
}

func eventosDeConquista(t *testing.T, ctx context.Context, store *postgres.Store, tipo string) []struct {
	player, other *uuid.UUID
} {
	t.Helper()
	rows, err := store.Pool().Query(ctx,
		`SELECT player_id, counterpart_player_id FROM world_events WHERE event_type = $1 ORDER BY id`, tipo)
	require.NoError(t, err)
	defer rows.Close()
	var out []struct{ player, other *uuid.UUID }
	for rows.Next() {
		var e struct{ player, other *uuid.UUID }
		require.NoError(t, rows.Scan(&e.player, &e.other))
		out = append(out, e)
	}
	return out
}

// ADR-034 §1 y §3, INV-TERR-010, INV-ECON-004: la reclamación de un territorio
// libre se escribe ACTIVE con su disputa y sin evento (no hay otro jugador), un
// reintento devuelve la misma fila y otra activa sobre el mismo territorio se
// rechaza; la captura cambia el dueño, quita la disputa, suma el bono una vez y
// escribe el evento; un reintento no repite nada.
func TestUnaReclamacionLibreYSuCapturaSeEscribenUnaVez(t *testing.T) {
	store, ctx := newTestStore(t)
	c := nuevaConquista(t, ctx, store)

	id, err := c.repo.StartClaim(ctx, c.reclamacion(c.duena, c.libre, nil, "req-1"))
	require.NoError(t, err)
	otra, err := c.repo.StartClaim(ctx, c.reclamacion(c.duena, c.libre, nil, "req-1"))
	require.NoError(t, err)
	assert.Equal(t, id, otra, "un reintento devuelve la misma fila")
	assert.True(t, controlEnBase(t, ctx, c.territorios, store, c.libre.ID).Contested)
	assert.Empty(t, eventosDeConquista(t, ctx, store, territory.EventClaimStarted), "uno libre no tiene a quién contárselo")

	_, err = c.repo.StartClaim(ctx, c.reclamacion(c.rival, c.libre, nil, "req-2"))
	assert.ErrorIs(t, err, postgres.ErrConflict, "INV-TERR-010: una activa por territorio")

	activas, err := c.repo.ListActiveClaims(ctx)
	require.NoError(t, err)
	require.Len(t, activas, 1)
	assert.Equal(t, c.duena.Units[0].ID, activas[0].UnitID)
	assert.Equal(t, int64(301_000), activas[0].CompletesAtMs)

	limiteAntes, _ := limiteYPoblacion(t, ctx, store, c.duena.City.ID)
	captura := postgres.Capture{
		ClaimID: id, TerritoryID: c.libre.ID, TerritoryName: "Este", NewOwner: c.duena.Player.ID,
		NewCityID: c.duena.City.ID, NewBonus: 5, ExpectedVersion: controlEnBase(t, ctx, c.territorios, store, c.libre.ID).Version,
		CapturedAt: c.instanteDeHoy, Tick: 9, AtMs: 301_000, X: 40, Y: 10,
	}
	ctrl, err := c.repo.CaptureTerritory(ctx, captura)
	require.NoError(t, err)
	assert.Equal(t, territory.OwnerPlayer, ctrl.OwnerType)
	require.NotNil(t, ctrl.OwnerID)
	assert.Equal(t, c.duena.Player.ID.String(), *ctrl.OwnerID)
	assert.False(t, ctrl.Contested)
	limite, _ := limiteYPoblacion(t, ctx, store, c.duena.City.ID)
	assert.Equal(t, limiteAntes+5, limite)

	repetida, err := c.repo.CaptureTerritory(ctx, captura)
	require.NoError(t, err, "un reintento de la cola")
	assert.Equal(t, ctrl.Version, repetida.Version)
	limite, _ = limiteYPoblacion(t, ctx, store, c.duena.City.ID)
	assert.Equal(t, limiteAntes+5, limite, "el bono, una vez")
	evs := eventosDeConquista(t, ctx, store, territory.EventControlChanged)
	var deCaptura int
	for _, e := range evs {
		if e.player != nil && *e.player == c.duena.Player.ID && e.other == nil {
			deCaptura++
		}
	}
	assert.GreaterOrEqual(t, deCaptura, 1)
	activas, err = c.repo.ListActiveClaims(ctx)
	require.NoError(t, err)
	assert.Empty(t, activas)
}

// ADR-034 §3, INV-TERR-011 y RN-CONQ-010: la captura de un territorio ajeno con
// una versión vieja no escribe nada; con la buena, mueve los dos límites, deja el
// evento con el anterior dueño y el nuevo, y el registro de cada uno lo cuenta.
// La capital no se captura aunque la reclamación exista en la base.
func TestUnaCapturaAjenaMueveLosDosLimitesYSeRegistra(t *testing.T) {
	store, ctx := newTestStore(t)
	c := nuevaConquista(t, ctx, store)
	// La dueña conquista primero el territorio libre.
	id, err := c.repo.StartClaim(ctx, c.reclamacion(c.duena, c.libre, nil, "req-1"))
	require.NoError(t, err)
	_, err = c.repo.CaptureTerritory(ctx, postgres.Capture{
		ClaimID: id, TerritoryID: c.libre.ID, TerritoryName: "Este", NewOwner: c.duena.Player.ID,
		NewCityID: c.duena.City.ID, NewBonus: 5, ExpectedVersion: controlEnBase(t, ctx, c.territorios, store, c.libre.ID).Version,
		CapturedAt: c.instanteDeHoy, AtMs: 301_000,
	})
	require.NoError(t, err)
	limDuena, _ := limiteYPoblacion(t, ctx, store, c.duena.City.ID)
	limRival, _ := limiteYPoblacion(t, ctx, store, c.rival.City.ID)
	version := controlEnBase(t, ctx, c.territorios, store, c.libre.ID).Version

	duena := c.duena.Player.ID
	id2, err := c.repo.StartClaim(ctx, c.reclamacion(c.rival, c.libre, &duena, "req-2"))
	require.NoError(t, err)
	empieza := eventosDeConquista(t, ctx, store, territory.EventClaimStarted)
	require.Len(t, empieza, 1)
	assert.Equal(t, duena, *empieza[0].player)
	assert.Equal(t, c.rival.Player.ID, *empieza[0].other)

	captura := postgres.Capture{
		ClaimID: id2, TerritoryID: c.libre.ID, TerritoryName: "Este", NewOwner: c.rival.Player.ID,
		NewCityID: c.rival.City.ID, NewBonus: 5, PrevOwner: duena, PrevCityID: c.duena.City.ID, PrevBonus: 5,
		ExpectedVersion: version - 1, CapturedAt: c.instanteDeHoy, Tick: 12, AtMs: 1_000_000, X: 40, Y: 10,
	}
	_, err = c.repo.CaptureTerritory(ctx, captura)
	assert.ErrorIs(t, err, postgres.ErrConflict, "otra versión: no casa con la RAM")
	assert.Equal(t, duena.String(), *controlEnBase(t, ctx, c.territorios, store, c.libre.ID).OwnerID, "nada escrito")

	captura.ExpectedVersion = version
	ctrl, err := c.repo.CaptureTerritory(ctx, captura)
	require.NoError(t, err)
	assert.Equal(t, c.rival.Player.ID.String(), *ctrl.OwnerID)
	l, _ := limiteYPoblacion(t, ctx, store, c.duena.City.ID)
	assert.Equal(t, limDuena-5, l)
	l, _ = limiteYPoblacion(t, ctx, store, c.rival.City.ID)
	assert.Equal(t, limRival+5, l)

	paginaDe := func(quien uuid.UUID) map[string]string {
		page, _, err := registro(store, 50).Page(ctx, quien, 0)
		require.NoError(t, err)
		out := map[string]string{}
		for _, e := range page.Entries {
			out[e.Kind] = e.TerritoryName
		}
		return out
	}
	assert.Equal(t, map[string]string{"TERRITORY_CLAIMED": "Este", "TERRITORY_LOST": "Este"}, paginaDe(duena))
	assert.Equal(t, map[string]string{"TERRITORY_TAKEN": "Este"}, paginaDe(c.rival.Player.ID))

	// INV-TERR-011: la capital de la dueña no cambia de manos, aunque alguien
	// hubiera escrito una reclamación sobre ella.
	id3, err := c.repo.StartClaim(ctx, c.reclamacion(c.rival, c.capitalDuena, &duena, "req-3"))
	require.NoError(t, err)
	capital := controlEnBase(t, ctx, c.territorios, store, c.capitalDuena.ID)
	_, err = c.repo.CaptureTerritory(ctx, postgres.Capture{
		ClaimID: id3, TerritoryID: c.capitalDuena.ID, NewOwner: c.rival.Player.ID, NewCityID: c.rival.City.ID,
		NewBonus: 5, PrevOwner: duena, PrevCityID: c.duena.City.ID, PrevBonus: 5,
		ExpectedVersion: capital.Version, CapturedAt: c.instanteDeHoy,
	})
	require.True(t, errors.Is(err, postgres.ErrConflict), "la capital: %v", err)
	assert.Equal(t, duena.String(), *controlEnBase(t, ctx, c.territorios, store, c.capitalDuena.ID).OwnerID)
}

// ADR-034 §2: cerrar una reclamación sin captura es idempotente, quita la
// disputa y libera el territorio para otra.
func TestUnFinDeReclamacionEsIdempotente(t *testing.T) {
	store, ctx := newTestStore(t)
	c := nuevaConquista(t, ctx, store)
	id, err := c.repo.StartClaim(ctx, c.reclamacion(c.duena, c.libre, nil, "req-1"))
	require.NoError(t, err)

	fin := postgres.ClaimEnd{ClaimID: id, TerritoryID: c.libre.ID, Reason: "UNIT"}
	require.NoError(t, c.repo.EndClaim(ctx, fin))
	require.NoError(t, c.repo.EndClaim(ctx, fin), "un reintento")
	var estado, motivo string
	require.NoError(t, store.Pool().QueryRow(ctx,
		`SELECT status, end_reason FROM territory_claims WHERE id = $1`, id).Scan(&estado, &motivo))
	assert.Equal(t, "FAILED", estado)
	assert.Equal(t, "UNIT", motivo)
	assert.False(t, controlEnBase(t, ctx, c.territorios, store, c.libre.ID).Contested)

	_, err = c.repo.StartClaim(ctx, c.reclamacion(c.rival, c.libre, nil, "req-2"))
	assert.NoError(t, err, "cerrada, el territorio admite otra")
}
