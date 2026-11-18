package clan_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/clan"
)

// SPEC-CLAN-WARS §4.1: la guerra se declara, empieza con su plazo y termina una
// sola vez; los saltos ilegales se rechazan.
func TestElCicloDeUnaGuerra(t *testing.T) {
	w := clan.War{ID: 1, Attacker: 7, Defender: 3, Status: clan.WarDeclared, DeclaredAtMs: 100, StartsAtMs: 900}
	assert.True(t, w.Open())
	assert.Equal(t, [2]int64{3, 7}, w.Pair(), "el par no tiene orden")
	assert.Equal(t, clan.WarPair(7, 3), clan.WarPair(3, 7))
	assert.Equal(t, int64(3), w.Other(7))
	assert.Equal(t, int64(0), w.Other(9))
	assert.False(t, w.Scoring(900), "la preparación no puntúa")

	activa, err := w.Start(900, time.Hour)
	require.NoError(t, err)
	assert.Equal(t, clan.WarActive, activa.Status)
	assert.Equal(t, int64(900+3_600_000), activa.EndsAtMs)
	assert.Equal(t, clan.WarDeclared, w.Status, "devuelve una copia")
	assert.True(t, activa.Scoring(900))
	_, err = activa.Start(1000, time.Hour)
	assert.ErrorIs(t, err, clan.ErrWarTransition, "no empieza dos veces")

	fin, err := activa.End(clan.WarEndSurrender, 7, 5000)
	require.NoError(t, err)
	assert.False(t, fin.Open())
	assert.Equal(t, int64(5000), fin.EndedAtMs)
	_, err = fin.End(clan.WarEndPeace, 0, 6000)
	assert.ErrorIs(t, err, clan.ErrWarTransition, "no termina dos veces")
	_, err = activa.End(clan.WarEndSurrender, 99, 5000)
	assert.ErrorIs(t, err, clan.ErrWarTransition, "el vencedor es una de las partes")

	assert.Equal(t, activa.EndsAtMs, fin.EndsAtMs, "una guerra activa conserva su plazo")

	// También se termina en preparación (la paz y la rendición valen ahí), y su
	// final pasa a ser su plazo: la base lo exige fuera de la preparación.
	enPreparacion, err := w.End(clan.WarEndPeace, 0, 500)
	require.NoError(t, err)
	assert.Equal(t, int64(500), enPreparacion.EndsAtMs)
	assert.Equal(t, int64(500), enPreparacion.EndedAtMs)
}

// RN-CLANWAR-007 y RN-CLANWAR-008: el marcador por lado, el vencedor por plazo
// y el resultado de cada lado.
func TestElMarcadorYElResultado(t *testing.T) {
	w := clan.War{Attacker: 1, Defender: 2, Status: clan.WarActive}
	w = w.AddPoints(1, clan.PointsKill).AddPoints(2, clan.PointsBuilding).AddPoints(9, 100)
	assert.Equal(t, int64(1), w.ScoreOf(1))
	assert.Equal(t, int64(3), w.ScoreOf(2))
	assert.Equal(t, int64(0), w.ScoreOf(9), "un clan ajeno no puntúa")
	assert.Equal(t, int64(2), w.LeaderByScore())

	w = w.AddPoints(1, 2)
	assert.Equal(t, int64(0), w.LeaderByScore(), "con los mismos puntos, empate")

	fin, err := w.End(clan.WarEndTimeout, w.LeaderByScore(), 10)
	require.NoError(t, err)
	assert.Equal(t, clan.WarDrawn, fin.ResultFor(1))
	assert.Equal(t, clan.WarDrawn, fin.ResultFor(2))
	gana, err := w.End(clan.WarEndSurrender, 2, 10)
	require.NoError(t, err)
	assert.Equal(t, clan.WarWon, gana.ResultFor(2))
	assert.Equal(t, clan.WarLost, gana.ResultFor(1))

	r := clan.WarRecord{}.Add(clan.WarWon).Add(clan.WarLost).Add(clan.WarDrawn).Add(clan.WarWon)
	assert.Equal(t, clan.WarRecord{Won: 2, Lost: 1, Drawn: 1}, r)
	assert.Equal(t, int64(1), int64(clan.PointsKill))
	assert.Equal(t, int64(10), int64(clan.PointsTerritory))
}

// Las ofertas de paz de cada lado, que el final borra, y la tregua.
func TestLaPazYLaTregua(t *testing.T) {
	w := clan.War{Attacker: 1, Defender: 2, Status: clan.WarActive}
	w = w.WithPeaceOffer(2, true)
	assert.True(t, w.PeaceOfferedBy(2))
	assert.False(t, w.PeaceOfferedBy(1))
	w = w.WithPeaceOffer(2, false).WithPeaceOffer(1, true)
	assert.False(t, w.PeaceOfferedBy(2))
	assert.True(t, w.PeaceOfferedBy(1))

	fin, err := w.End(clan.WarEndPeace, 0, 1000)
	require.NoError(t, err)
	assert.False(t, fin.PeaceOfferedBy(1), "el final borra las ofertas")
	assert.True(t, fin.InCooldown(1000+3_599_999, time.Hour))
	assert.False(t, fin.InCooldown(1000+3_600_000, time.Hour))
	assert.False(t, w.InCooldown(1000, time.Hour), "una guerra abierta no está en tregua")
}
