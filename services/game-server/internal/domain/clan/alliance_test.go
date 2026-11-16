package clan_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/clan"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/diplomacy"
)

// SPEC-CLAN-ALLIANCES §4.1: la alianza tiene los estados y los saltos de un
// tratado, y cada salto sella su instante.
func TestElCicloDeUnaAlianza(t *testing.T) {
	a := clan.Alliance{ID: 1, ClanA: 2, ClanB: 5, ProposedByClan: 5, Status: diplomacy.StatusProposed, CreatedAtMs: 10}
	assert.True(t, a.Open())
	assert.False(t, a.Active())
	assert.Equal(t, [2]int64{2, 5}, a.Pair())
	assert.Equal(t, int64(2), a.Other(5))
	assert.Equal(t, int64(0), a.Other(9))

	activa, err := a.TransitionTo(diplomacy.StatusActive, 20)
	require.NoError(t, err)
	assert.Equal(t, int64(20), activa.AcceptedAtMs)
	assert.Equal(t, diplomacy.StatusProposed, a.Status, "devuelve una copia")
	activa.BreakEffectiveAtMs = 900
	assert.True(t, activa.InNotice())
	rota, err := activa.TransitionTo(diplomacy.StatusBroken, 900)
	require.NoError(t, err)
	assert.Equal(t, int64(900), rota.ClosedAtMs)
	assert.False(t, rota.Open())

	_, err = rota.TransitionTo(diplomacy.StatusActive, 1000)
	assert.ErrorIs(t, err, clan.ErrAllianceTransition, "una alianza rota no vuelve")
	_, err = a.TransitionTo(diplomacy.StatusBroken, 30)
	assert.ErrorIs(t, err, clan.ErrAllianceTransition, "una oferta no se rompe")
	for _, fin := range []diplomacy.Status{diplomacy.StatusRejected, diplomacy.StatusWithdrawn, diplomacy.StatusExpired} {
		cerrada, err := a.TransitionTo(fin, 40)
		require.NoError(t, err, fin)
		assert.Equal(t, int64(40), cerrada.ClosedAtMs)
		assert.False(t, cerrada.ActiveAt(40), "%s: nunca estuvo activa", fin)
	}

	x, y := clan.CanonicalClans(7, 3)
	assert.Equal(t, [2]int64{3, 7}, [2]int64{x, y})
}

// ADR-042 §2: activa desde su aceptación hasta su cierre, los dos incluidos.
func TestCuandoEstabaActivaUnaAlianza(t *testing.T) {
	a := clan.Alliance{AcceptedAtMs: 100, ClosedAtMs: 500}
	assert.False(t, a.ActiveAt(99))
	assert.True(t, a.ActiveAt(100))
	assert.True(t, a.ActiveAt(500), "el último en salir de un clan que se disuelve")
	assert.False(t, a.ActiveAt(501))
	assert.True(t, clan.Alliance{AcceptedAtMs: 100}.ActiveAt(1_000_000), "sin cerrar")
}

// RN-CLANALLY-006: la paz residual con los aliados alcanza a quien era miembro
// del clan aliado en el instante de la salida, mientras la alianza estaba
// activa, y dura la paz residual; no alcanza al propio clan (eso es la paz del
// clan), ni a quien entró después, ni sin alianza en ese instante.
func TestLaPazResidualConLosAliadosEnElDominio(t *testing.T) {
	x, y := uuid.New(), uuid.New()
	grace := time.Minute
	salida := clan.Stay{ClanID: 1, PlayerID: x, JoinedAtMs: 0, LeftAtMs: 10_000}
	enB := clan.Stay{ClanID: 2, PlayerID: y, JoinedAtMs: 5_000}
	alianza := clan.Alliance{ClanA: 1, ClanB: 2, AcceptedAtMs: 1_000}

	paz := func(stays []clan.Stay, als []clan.Alliance, nowMs int64) bool {
		return clan.AllyResidualPeace(x, y, stays, als, nowMs, grace)
	}
	assert.True(t, paz([]clan.Stay{salida, enB}, []clan.Alliance{alianza}, 10_000))
	assert.True(t, clan.AllyResidualPeace(y, x, []clan.Stay{salida, enB}, []clan.Alliance{alianza}, 69_999, grace), "simétrica, hasta el último ms")
	assert.False(t, paz([]clan.Stay{salida, enB}, []clan.Alliance{alianza}, 70_000), "vence al cumplirse")
	assert.False(t, paz([]clan.Stay{salida, enB}, nil, 10_000), "sin alianza")
	assert.False(t, paz([]clan.Stay{salida, {ClanID: 2, PlayerID: y, JoinedAtMs: 10_001}}, []clan.Alliance{alianza}, 20_000),
		"entró después de la salida")
	assert.False(t, paz([]clan.Stay{salida, {ClanID: 1, PlayerID: y, JoinedAtMs: 0}}, []clan.Alliance{alianza}, 20_000),
		"el propio clan no es un aliado")
	assert.False(t, paz([]clan.Stay{salida, enB}, []clan.Alliance{{ClanA: 1, ClanB: 2, AcceptedAtMs: 1_000, ClosedAtMs: 9_000}}, 20_000),
		"rota antes de la salida")
	assert.False(t, paz([]clan.Stay{salida, enB}, []clan.Alliance{{ClanA: 1, ClanB: 3, AcceptedAtMs: 1_000}}, 20_000),
		"una alianza con otro clan")
}
