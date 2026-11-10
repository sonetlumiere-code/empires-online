//go:build integration

package postgres_test

import (
	"errors"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/player"
	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// SPEC-CIVILIZATION §9, integración: el alta escribe la civilización elegida, el
// trigger impide cambiarla, el catálogo se lee en enteros y coincide con el de
// presentación, y uno inválido no se carga.

// RN-CIV-001: la fundación escribe la civilización que eligió el jugador, la
// ciudad la lleva al cargarse y la RAM la recibe con IntroducePlayer.
func TestLaFundacionEscribeLaCivilizacionElegida(t *testing.T) {
	store, ctx := newTestStore(t)
	players, cities, _, _, _ := newRepos(store)
	f := montarFundacion(t, store, mundoDeFundacion(t, 64), city.EraStone, make(chan simulation.Command, 1))
	id := uuid.New()

	body := cuerpo(id, "vikinga")
	body["civilizationCode"] = "NORSE"
	rec := f.fundar(t, f.interno, id, body)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	civs, err := players.ListCivilizations(ctx)
	require.NoError(t, err)
	var norse player.Civilization
	for _, c := range civs {
		if c.Code == "NORSE" {
			norse = c
		}
	}
	p, err := players.GetByUsername(ctx, "vikinga")
	require.NoError(t, err)
	assert.Equal(t, norse.ID, p.CivilizationID)

	c, err := cities.GetByOwner(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "NORSE", c.Civilization)
	todas, err := cities.ListAll(ctx)
	require.NoError(t, err)
	require.Len(t, todas, 1)
	assert.Equal(t, "NORSE", todas[0].Civilization, "la carga del mundo la lee igual")

	intro := (<-f.commands).(simulation.IntroducePlayer)
	assert.Equal(t, "NORSE", intro.City.Civilization)
}

// SPEC-CIVILIZATION §5: sin civilización o con una fuera del catálogo, la
// fundación se rechaza con 400 y no escribe nada.
func TestUnaFundacionSinCivilizacionDelCatalogoSeRechazaSinEscribir(t *testing.T) {
	store, ctx := newTestStore(t)
	players, _, _, _, _ := newRepos(store)
	f := montarFundacion(t, store, mundoDeFundacion(t, 64), city.EraStone, make(chan simulation.Command, 1))

	for i, codigo := range []string{"", "AZTEC", "roman"} {
		id := uuid.New()
		nombre := []string{"sin_civ", "azteca", "minusculas"}[i]
		body := cuerpo(id, nombre)
		body["civilizationCode"] = codigo
		rec := f.fundar(t, f.interno, id, body)
		assert.Equal(t, http.StatusBadRequest, rec.Code, "%q: %s", codigo, rec.Body.String())
		assert.Equal(t, "INVALID_MESSAGE", codigoHTTP(t, rec))
		_, err := players.GetByUsername(ctx, nombre)
		assert.ErrorIs(t, err, player.ErrNotFound, "%q no deja jugador", codigo)
	}
	assert.Empty(t, f.commands, "nada entra en el mundo")
}

// INV-PLAYER-011: la civilización de un jugador no cambia después del alta. El
// trigger rechaza cambiarla y deja pasar cualquier otra escritura del jugador.
func TestLaCivilizacionDeUnJugadorNoCambia(t *testing.T) {
	store, ctx := newTestStore(t)
	_, _, _, _, bootstrapper := newRepos(store)
	alta, err := bootstrapper.Create(ctx, bootstrapRequest("inmutable", world.Tile{X: 20, Y: 20}))
	require.NoError(t, err)
	id := alta.Player.ID

	_, err = store.Pool().Exec(ctx, `UPDATE players SET civilization_id = 4 WHERE id = $1`, id)
	var pgErr *pgconn.PgError
	require.True(t, errors.As(err, &pgErr), "se esperaba el rechazo del trigger, llegó %v", err)
	assert.Equal(t, "23514", pgErr.Code, "check_violation")
	assert.Contains(t, pgErr.Message, "INV-PLAYER-011")

	_, err = store.Pool().Exec(ctx, `UPDATE players SET civilization_id = civilization_id, last_seen_at = now() WHERE id = $1`, id)
	assert.NoError(t, err, "el mismo valor y las demás columnas sí se escriben")

	var civ int32
	require.NoError(t, store.Pool().QueryRow(ctx, `SELECT civilization_id FROM players WHERE id = $1`, id).Scan(&civ))
	assert.Equal(t, int32(1), civ)
}

// INV-PLAYER-012 y ADR-035 §7: el catálogo de 000015 se lee en enteros y es
// exactamente el de presentación que exporta packages/protocol, en el mismo
// orden. Una migración de balance que no lo actualice rompe aquí.
func TestElCatalogoDeCivilizacionesEsElDePresentacion(t *testing.T) {
	store, ctx := newTestStore(t)
	players, _, _, _, _ := newRepos(store)
	civs, err := players.ListCivilizations(ctx)
	require.NoError(t, err)
	exportado, err := protocol.ExportedCivilizations()
	require.NoError(t, err)

	require.Len(t, civs, len(exportado))
	for i, c := range civs {
		assert.Equal(t, exportado[i].Code, c.Code)
		assert.Equal(t, exportado[i].Name, c.Name, c.Code)
		assert.Equal(t, exportado[i].Good, string(c.Good), c.Code)
		assert.Equal(t, exportado[i].Traits, c.Traits, c.Code)
	}
}

// INV-PLAYER-012: un rasgo decimal, cero o de una clave desconocida impide leer
// el catálogo, y con él arrancar.
func TestUnCatalogoConUnRasgoInvalidoNoSeCarga(t *testing.T) {
	store, ctx := newTestStore(t)
	players, _, _, _, _ := newRepos(store)
	var original string
	require.NoError(t, store.Pool().QueryRow(ctx, `SELECT traits::text FROM civilizations WHERE code = 'NORSE'`).Scan(&original))
	t.Cleanup(func() {
		_, err := store.Pool().Exec(ctx, `UPDATE civilizations SET traits = $1::jsonb WHERE code = 'NORSE'`, original)
		require.NoError(t, err)
	})

	for _, traits := range []string{
		`{"unit.move_speed": 1.1}`,
		`{"unit.move_speed": 0}`,
		`{"economy.trade_yield": 1150}`,
		`{"unit.move_speed": "rápido"}`,
	} {
		_, err := store.Pool().Exec(ctx, `UPDATE civilizations SET traits = $1::jsonb WHERE code = 'NORSE'`, traits)
		require.NoError(t, err)
		_, err = players.ListCivilizations(ctx)
		assert.ErrorIs(t, err, player.ErrInvalidTrait, traits)
	}
}
