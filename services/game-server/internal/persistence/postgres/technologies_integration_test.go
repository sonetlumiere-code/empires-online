//go:build integration

package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/advance"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/economy"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/technology"
	"github.com/empires-online/empires-online/services/game-server/internal/persistence/postgres"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// SPEC-TECHNOLOGIES §9, integración: la migración 000027 (el catálogo sembrado,
// las dos tablas y los tres triggers de INV-TECH-001), el encargo con su débito
// y su fila, idempotente, el fin que da la tecnología y borra la fila, y la
// carga (ADR-046 §3 y §4).

var civilizacionesDelCatalogo = []string{"ROMAN", "BYZANTINE", "PERSIAN", "NORSE"}

// deltaDeRecurso es un delta de un recurso cualquiera de una ciudad.
func deltaDeRecurso(cityID int64, r economy.Resource, delta, version int64) postgres.StockDelta {
	return postgres.StockDelta{CityID: cityID, Resource: r, Delta: delta, Version: version}
}

// conAlmacen pone madera y piedra en la ciudad del escenario.
func (e escenarioEconomico) conAlmacen(t *testing.T, ctx context.Context, madera, piedra int64) {
	t.Helper()
	require.NoError(t, e.economia.FlushStocks(ctx, []postgres.StockDelta{
		deltaDeRecurso(e.ciudad(), economy.ResourceWood, madera, 1), deltaDeRecurso(e.ciudad(), economy.ResourceStone, piedra, 1),
	}))
}

func eraDeLaCiudad(t *testing.T, ctx context.Context, store *postgres.Store, cityID int64, era city.Era) {
	t.Helper()
	_, err := store.Pool().Exec(ctx, `UPDATE cities SET era = $2 WHERE id = $1`, cityID, string(era))
	require.NoError(t, err)
}

// La tabla es un catálogo válido y el de presentación (technologies.json) la
// copia campo a campo: una migración de balance que no lo actualice rompe aquí
// (ADR-046 §1).
func TestElCatalogoDeTecnologiasEnLaBase(t *testing.T) {
	store, ctx := newTestStore(t)
	repo := postgres.NewEconomyRepo(store, nil, nil)
	filas, err := repo.ListTechnologies(ctx)
	require.NoError(t, err)
	require.Len(t, filas, 12)
	cat, err := technology.NewCatalog(filas, civilizacionesDelCatalogo)
	require.NoError(t, err, "INV-TECH-002: el catálogo sembrado es válido")
	assert.Equal(t, 12, cat.Len())

	exportado, err := protocol.ExportedTechnologies()
	require.NoError(t, err)
	require.Len(t, exportado, len(filas))
	for i, f := range filas {
		e := exportado[i]
		assert.Equal(t, f.Code, e.Code, "en el orden de la tabla")
		assert.Equal(t, f.Name, e.Name, f.Code)
		assert.Equal(t, string(f.Era), e.Era, f.Code)
		if f.Civilization == "" {
			assert.Nil(t, e.Civilization, f.Code)
		} else if assert.NotNil(t, e.Civilization, f.Code) {
			assert.Equal(t, f.Civilization, *e.Civilization, f.Code)
		}
		costes := map[string]int64{}
		for _, c := range f.Costs {
			costes[string(c.Resource)] = c.Amount
		}
		assert.Equal(t, costes, e.Costs, f.Code)
		assert.Equal(t, f.ForeignGoods, e.ForeignGoods, f.Code)
		assert.Equal(t, f.OwnGood, e.OwnGood, f.Code)
		assert.Equal(t, f.DurationMs, e.DurationMs, f.Code)
		assert.ElementsMatch(t, f.Requires, e.Requires, f.Code)
		assert.Equal(t, f.Effects, e.Effects, f.Code)
	}
}

// RN-TECH-004 y RN-TECH-005, INV-ECON-002: el encargo debita y escribe la fila
// en una transacción, un reintento no debita dos veces y otra orden en la misma
// ciudad se rechaza; el fin da la tecnología, borra la fila y escribe su evento
// una sola vez. La carga lee las dos cosas.
func TestLaInvestigacionEnLaBase(t *testing.T) {
	store, ctx := newTestStore(t)
	e := nuevaEconomia(t, ctx, store)
	e.conAlmacen(t, ctx, 500, 500)
	p := e.jugador.Player.ID
	r := technology.Research{CityID: e.ciudad(), Technology: "STONE_TOOLS", CompletesAtMs: 60_000, RequestID: "t-1"}
	debito := []postgres.StockDelta{
		deltaDeRecurso(e.ciudad(), economy.ResourceWood, -150, 2), deltaDeRecurso(e.ciudad(), economy.ResourceStone, -50, 2),
	}

	require.NoError(t, e.economia.Research(ctx, "t-1", p, r, debito, 3))
	require.NoError(t, e.economia.Research(ctx, "t-1", p, r, debito, 3), "un reintento")
	madera, _ := stockEnBase(t, ctx, store, e.ciudad())
	assert.Equal(t, int64(350), madera, "el débito, una vez")
	otra := r
	otra.Technology, otra.RequestID = "BASKETRY", "t-2"
	assert.ErrorIs(t, e.economia.Research(ctx, "t-2", p, otra, nil, 3), technology.ErrCityBusy)
	assert.ErrorIs(t, e.economia.Research(ctx, "t-3", uuid.New(), technology.Research{CityID: e.ciudad(), Technology: "BASKETRY", RequestID: "t-3"}, nil, 3),
		technology.ErrCityBusy, "la fila de la ciudad va antes que el dueño")

	enCurso, err := e.economia.ListResearch(ctx)
	require.NoError(t, err)
	assert.Equal(t, []technology.Research{r}, enCurso)

	// RN-ECON-026: con la investigación en curso, la ciudad no avanza.
	err = e.economia.Advance(ctx, "a-1", p, advance.Advance{CityID: e.ciudad(), From: city.EraStone, To: city.EraBronze,
		CompletesAtMs: 90_000, RequestID: "a-1"}, nil, 4)
	assert.ErrorIs(t, err, advance.ErrCityBusy)

	en := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	for vez := 0; vez < 2; vez++ {
		require.NoError(t, e.economia.CompleteResearch(ctx, e.ciudad(), p, "STONE_TOOLS", en, 5))
	}
	tenidas, err := e.economia.ListPlayerTechnologies(ctx)
	require.NoError(t, err)
	assert.Equal(t, map[uuid.UUID][]string{p: {"STONE_TOOLS"}}, tenidas)
	enCurso, err = e.economia.ListResearch(ctx)
	require.NoError(t, err)
	assert.Empty(t, enCurso)
	assert.Equal(t, 1, filasDe(t, ctx, store, postgres.EventTechnologyResearched, p), "el evento, una vez")
	assert.Equal(t, 1, filasDe(t, ctx, store, postgres.EventResearchOrdered, p))
}

// INV-TECH-001: cada rama de los tres triggers rechaza con 23514, por debajo del
// tick y de la transacción del encargo.
func TestLosTriggersDeLaInvestigacion(t *testing.T) {
	store, ctx := newTestStore(t)
	e := nuevaEconomia(t, ctx, store)
	p := e.jugador.Player.ID
	idDe := func(code string) int32 {
		var id int32
		require.NoError(t, store.Pool().QueryRow(ctx, `SELECT id FROM technologies WHERE code = $1`, code).Scan(&id))
		return id
	}
	investigar := func(code string) error {
		_, err := store.Pool().Exec(ctx,
			`INSERT INTO city_research (city_id, technology_id, completes_at_ms, request_id) VALUES ($1, $2, 1, 'x')`,
			e.ciudad(), idDe(code))
		return err
	}
	dar := func(code string) error {
		_, err := store.Pool().Exec(ctx,
			`INSERT INTO player_technologies (player_id, technology_id, researched_at) VALUES ($1, $2, now())`, p, idDe(code))
		return err
	}
	rechazo := func(err error, que string) {
		t.Helper()
		require.Error(t, err, que)
		assert.True(t, postgres.IsCheckViolation(err), "%s: %v", que, err)
	}

	rechazo(investigar("BRONZE_WEAPONS"), "una era posterior a la de la ciudad")
	eraDeLaCiudad(t, ctx, store, e.ciudad(), city.EraCastle)
	rechazo(investigar("RAIDING"), "la exclusiva de otra civilización")
	rechazo(investigar("IRON_WEAPONS"), "sin su requisito")
	rechazo(dar("RAIDING"), "una exclusiva de otra civilización entre las tenidas")
	require.NoError(t, dar("STONE_TOOLS"))
	rechazo(investigar("STONE_TOOLS"), "una que ya tiene")
	require.NoError(t, dar("BRONZE_WEAPONS"))
	require.NoError(t, investigar("IRON_WEAPONS"), "con su requisito sí")

	_, err := store.Pool().Exec(ctx,
		`INSERT INTO city_era_advances (city_id, from_era, to_era, completes_at_ms, request_id)
		 VALUES ($1, 'CASTLE_AGE', 'IRON_AGE', 1, 'y')`, e.ciudad())
	rechazo(err, "un avance en una ciudad que investiga")

	_, err = store.Pool().Exec(ctx, `DELETE FROM city_research WHERE city_id = $1`, e.ciudad())
	require.NoError(t, err)
	eraDeLaCiudad(t, ctx, store, e.ciudad(), city.EraStone)
	_, err = store.Pool().Exec(ctx,
		`INSERT INTO city_era_advances (city_id, from_era, to_era, completes_at_ms, request_id)
		 VALUES ($1, 'STONE_AGE', 'BRONZE_AGE', 1, 'z')`, e.ciudad())
	require.NoError(t, err)
	rechazo(investigar("BASKETRY"), "una investigación en una ciudad que avanza")

	// Por el repositorio, el avance en curso es un CITY_BUSY limpio, sin llegar
	// al trigger.
	err = e.economia.Research(ctx, "t-9", p, technology.Research{CityID: e.ciudad(), Technology: "BASKETRY",
		CompletesAtMs: 1, RequestID: "t-9"}, nil, 1)
	assert.ErrorIs(t, err, technology.ErrCityBusy)
	assert.Zero(t, contar(t, ctx, store, `SELECT count(*) FROM city_research`))
}

// Un trigger que rechaza el encargo vuelve como ErrUnavailable (INVALID_TARGET)
// y deshace el débito.
func TestUnTriggerRechazaElEncargoSinDebitar(t *testing.T) {
	store, ctx := newTestStore(t)
	e := nuevaEconomia(t, ctx, store)
	e.conAlmacen(t, ctx, 500, 500)
	err := e.economia.Research(ctx, "t-1", e.jugador.Player.ID,
		technology.Research{CityID: e.ciudad(), Technology: "BRONZE_WEAPONS", CompletesAtMs: 1, RequestID: "t-1"},
		[]postgres.StockDelta{deltaDeRecurso(e.ciudad(), economy.ResourceWood, -250, 2), deltaDeRecurso(e.ciudad(), economy.ResourceStone, -150, 2)}, 1)
	assert.ErrorIs(t, err, technology.ErrUnavailable)
	madera, _ := stockEnBase(t, ctx, store, e.ciudad())
	assert.Equal(t, int64(500), madera, "nada debitado")
}
