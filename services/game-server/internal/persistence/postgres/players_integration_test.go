//go:build integration

package postgres_test

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/player"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
)

const hashDePrueba = "$2a$10$hashdepruebahashdepruebahashdepruebahashdepru"

// La mitad DB de INV-PLAYER-002, 005 y 006: cada fila inválida la rechaza el
// motor con el nombre exacto de la restricción.
func TestLasRestriccionesDeJugadoresRechazanFilasInvalidas(t *testing.T) {
	store, ctx := newTestStore(t)
	db := store.Pool()

	insertar := func(id uuid.UUID, username string, civ, faction any) error {
		_, err := db.Exec(ctx,
			`INSERT INTO players (id, username, password_hash, civilization_id, faction_id)
			 VALUES ($1, $2, $3, $4, $5)`, id, username, hashDePrueba, civ, faction)
		return err
	}

	primero := uuid.New()
	require.NoError(t, insertar(primero, "primero", 1, 3))

	// INV-PLAYER-005: el id es único.
	assert.Equal(t, "players_pkey", constraintDe(t, insertar(primero, "otro_nombre", 1, 3)))
	// INV-PLAYER-006: el nombre es único.
	assert.Equal(t, "players_username_key", constraintDe(t, insertar(uuid.New(), "primero", 1, 3)))
	// INV-PLAYER-002: una civilización y una facción, existentes y obligatorias.
	assert.Equal(t, "players_civilization_id_fkey", constraintDe(t, insertar(uuid.New(), "sin_civ", 99, 3)))
	assert.Equal(t, "players_faction_id_fkey", constraintDe(t, insertar(uuid.New(), "sin_bando", 1, 99)))
	assert.Equal(t, "23502", codigoDe(t, insertar(uuid.New(), "civ_nula", nil, 3)))
	assert.Equal(t, "23502", codigoDe(t, insertar(uuid.New(), "bando_nulo", 1, nil)))

	var n int
	require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM players`).Scan(&n))
	assert.Equal(t, 1, n, "las filas rechazadas no dejan rastro")
}

// INV-PLAYER-006: la regla se declara en dos capas y las dos dicen lo mismo.
// Para cada nombre de la tabla, ValidateUsername lo acepta si y sólo si el
// CHECK players_username_format lo acepta.
func TestValidateUsernameYElCheckDicenLoMismo(t *testing.T) {
	store, ctx := newTestStore(t)
	db := store.Pool()

	nombres := []string{
		"abc", "ABC_def-9", strings.Repeat("x", 24), "a_b",
		"", "ab", strings.Repeat("y", 25), "con espacio", "punto.final", "arroba@",
		"ñandú", "café", "zero​width", "tab\tulador",
	}
	for _, nombre := range nombres {
		_, err := db.Exec(ctx,
			`INSERT INTO players (id, username, password_hash, civilization_id, faction_id)
			 VALUES ($1, $2, $3, 1, 3)`, uuid.New(), nombre, hashDePrueba)
		enBorde := player.ValidateUsername(nombre) == nil
		enBase := err == nil
		assert.Equal(t, enBorde, enBase, "%q: borde=%v base=%v (%v)", nombre, enBorde, enBase, err)
		if err != nil {
			assert.Equal(t, "players_username_format", constraintDe(t, err), "%q", nombre)
		}
	}
}

// INV-PLAYER-002: los catálogos sembrados por 000002_seed_catalogs son los del
// canon, ni más ni menos.
func TestLosCatalogosDeCivilizacionYFaccionSonLosDelCanon(t *testing.T) {
	store, ctx := newTestStore(t)
	players, _, _, _, _ := newRepos(store)

	civs, err := players.ListCivilizations(ctx)
	require.NoError(t, err)
	var codigosCiv []string
	for _, c := range civs {
		codigosCiv = append(codigosCiv, c.Code)
	}
	sort.Strings(codigosCiv)
	assert.Equal(t, []string{"BYZANTINE", "NORSE", "PERSIAN", "ROMAN"}, codigosCiv)

	facciones, err := players.ListFactions(ctx)
	require.NoError(t, err)
	var codigosFac []string
	for _, f := range facciones {
		codigosFac = append(codigosFac, f.Code)
	}
	sort.Strings(codigosFac)
	assert.Equal(t, []string{player.FactionChaos, player.FactionNeutral, player.FactionOrder}, codigosFac)
}

// INV-PLAYER-002: civilización y facción son ejes ortogonales. Las doce
// combinaciones dan de alta un jugador; el test falla si alguien acopla los
// ejes con una restricción o una regla.
func TestLosDosEjesSonOrtogonales(t *testing.T) {
	store, ctx := newTestStore(t)
	players, _, _, _, bootstrapper := newRepos(store)

	civs, err := players.ListCivilizations(ctx)
	require.NoError(t, err)
	facciones, err := players.ListFactions(ctx)
	require.NoError(t, err)

	i := 0
	for _, c := range civs {
		for _, f := range facciones {
			req := bootstrapRequest(fmt.Sprintf("eje_%d_%d", c.ID, f.ID), world.Tile{X: int32(10 + 4*i), Y: 30})
			req.CivilizationID, req.FactionID = c.ID, f.ID
			alta, err := bootstrapper.Create(ctx, req)
			require.NoError(t, err, "%s × %s", c.Code, f.Code)
			assert.Equal(t, c.ID, alta.Player.CivilizationID)
			assert.Equal(t, f.ID, alta.Player.FactionID)
			i++
		}
	}
	assert.Equal(t, 12, i)
}

// INV-PLAYER-007, mitad del esquema: la única columna de credencial es
// players.password_hash. Ninguna otra columna de ninguna tabla se llama como
// algo que guarde una contraseña, un secreto, un ticket o un token.
func TestLaUnicaColumnaDeCredencialEsElHash(t *testing.T) {
	store, ctx := newTestStore(t)

	rows, err := store.Pool().Query(ctx,
		`SELECT table_name || '.' || column_name FROM information_schema.columns
		  WHERE table_schema = 'public'
		    AND column_name ~* '(passw|secret|ticket|token|credential)'
		  ORDER BY 1`)
	require.NoError(t, err)
	defer rows.Close()
	var columnas []string
	for rows.Next() {
		var c string
		require.NoError(t, rows.Scan(&c))
		columnas = append(columnas, c)
	}
	require.NoError(t, rows.Err())
	assert.Equal(t, []string{"players.password_hash"}, columnas)
}

// INV-PLAYER-003: el alta exige exactamente player.InitialVillagers aldeanos.
// Ni el llamante puede pedir dos ni seis.
func TestUnAltaConOtroNumeroDeAldeanosSeRechazaSinEscribir(t *testing.T) {
	store, ctx := newTestStore(t)
	players, _, _, _, bootstrapper := newRepos(store)

	for _, n := range []int{0, 2, 4} {
		req := bootstrapRequest(fmt.Sprintf("aldeanos_%d", n), world.Tile{X: 30, Y: 30})
		req.VillagerSpawns = req.VillagerSpawns[:0]
		for i := 0; i < n; i++ {
			req.VillagerSpawns = append(req.VillagerSpawns, world.Tile{X: int32(28 + i), Y: 33})
		}
		_, err := bootstrapper.Create(ctx, req)
		require.Error(t, err, "%d aldeanos", n)
		_, _, err = players.GetByUsername(ctx, req.Username)
		assert.Error(t, err, "%d aldeanos: no queda jugador", n)
	}
}
