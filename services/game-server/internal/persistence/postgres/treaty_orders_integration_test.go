//go:build integration

package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/diplomacy"
	"github.com/empires-online/empires-online/services/game-server/internal/persistence/postgres"
)

// Persistencia de los tratados en caliente (SPEC-DIPLOMACY §7 y §9
// «integration», ADR-027 §2 y §4).

func eventosDeTratado(t *testing.T, ctx context.Context, store *postgres.Store, nombre string) int {
	t.Helper()
	var n int
	require.NoError(t, store.Pool().QueryRow(ctx,
		`SELECT count(*) FROM world_events WHERE event_type = $1`, nombre).Scan(&n))
	return n
}

func oferta(e escenarioDiplomatico, tipo diplomacy.Type, requestID string) diplomacy.Treaty {
	a, b := diplomacy.CanonicalPair(e.visitante.Player.ID, e.anfitrion.Player.ID)
	quien := e.visitante.Player.ID
	return diplomacy.Treaty{
		PlayerA: a, PlayerB: b, Type: tipo, Status: diplomacy.StatusProposed,
		AllowsGarrison: tipo.GrantsGarrison(), ProposedAt: time.UnixMilli(time.Now().UnixMilli()).UTC(),
		ProposedBy: &quien, RequestID: requestID,
	}
}

// ADR-027 §2: una oferta se inserta con quién la propuso; repetirla con el mismo
// request_id devuelve la misma fila y no duplica el evento.
func TestUnaOfertaEsIdempotentePorRequestID(t *testing.T) {
	store, ctx := newTestStore(t)
	e := nuevoEscenario(t, ctx, store)
	o := oferta(e, diplomacy.TypeAlliance, uuid.NewString())

	id, err := e.treaties.ApplyTreaty(ctx, postgres.TreatyWrite{Insert: true, Treaty: o, From: diplomacy.StatusProposed, Event: "TreatyProposed"})
	require.NoError(t, err)
	otra, err := e.treaties.ApplyTreaty(ctx, postgres.TreatyWrite{Insert: true, Treaty: o, From: diplomacy.StatusProposed, Event: "TreatyProposed"})
	require.NoError(t, err)
	assert.Equal(t, id, otra)
	assert.Equal(t, 1, eventosDeTratado(t, ctx, store, "TreatyProposed"))

	abiertos, err := e.treaties.ListOpen(ctx)
	require.NoError(t, err)
	require.Len(t, abiertos, 1)
	require.NotNil(t, abiertos[0].ProposedBy)
	assert.Equal(t, e.visitante.Player.ID, *abiertos[0].ProposedBy)
	assert.True(t, abiertos[0].AllowsGarrison)
}

// §4.1: aceptar una alianza cierra el pacto vigente en la misma transacción; y
// la aceptación repetida no escribe dos veces.
func TestAceptarUnaAlianzaCierraElPactoEnLaMismaTransaccion(t *testing.T) {
	store, ctx := newTestStore(t)
	e := nuevoEscenario(t, ctx, store)
	ahora := time.UnixMilli(time.Now().UnixMilli()).UTC()
	a, b := diplomacy.CanonicalPair(e.visitante.Player.ID, e.anfitrion.Player.ID)
	pacto, err := e.treaties.Create(ctx, store.Pool(), diplomacy.Treaty{
		PlayerA: a, PlayerB: b, Type: diplomacy.TypeNonAggression, Status: diplomacy.StatusActive, AcceptedAt: &ahora,
	})
	require.NoError(t, err)
	o := oferta(e, diplomacy.TypeAlliance, uuid.NewString())
	o.ID, err = e.treaties.ApplyTreaty(ctx, postgres.TreatyWrite{Insert: true, Treaty: o, From: diplomacy.StatusProposed, Event: "TreatyProposed"})
	require.NoError(t, err)

	aceptada, err := o.TransitionTo(diplomacy.StatusActive, ahora)
	require.NoError(t, err)
	w := postgres.TreatyWrite{Treaty: aceptada, From: diplomacy.StatusProposed, Supersede: &pacto, Event: "TreatyAccepted"}
	for vez := 0; vez < 2; vez++ {
		_, err = e.treaties.ApplyTreaty(ctx, w)
		require.NoError(t, err, "vez %d", vez)
	}
	abiertos, err := e.treaties.ListOpen(ctx)
	require.NoError(t, err)
	require.Len(t, abiertos, 1)
	assert.Equal(t, diplomacy.TypeAlliance, abiertos[0].Type)
	assert.Equal(t, 1, eventosDeTratado(t, ctx, store, "TreatyAccepted"))
}

// RN-DIPLO-004: anunciar la ruptura guarda su fin y el tratado sigue ACTIVE;
// repetir el anuncio es idempotente; al vencer pasa a BROKEN con broken_at en
// ese instante. Escribir desde un estado que ya no es el de la fila es un
// conflicto.
func TestLaRupturaGuardaElPreavisoYDespuesRompe(t *testing.T) {
	store, ctx := newTestStore(t)
	e := nuevoEscenario(t, ctx, store)
	vigente := tratadoActivo(t, ctx, store, e.treaties, e.visitante.Player.ID, e.anfitrion.Player.ID, true)

	fin := time.UnixMilli(time.Now().Add(15 * time.Minute).UnixMilli()).UTC()
	anuncio := vigente
	anuncio.BreakEffectiveAt = &fin
	w := postgres.TreatyWrite{Treaty: anuncio, From: diplomacy.StatusActive, Event: "TreatyBreakAnnounced"}
	for vez := 0; vez < 2; vez++ {
		_, err := e.treaties.ApplyTreaty(ctx, w)
		require.NoError(t, err)
	}
	abiertos, err := e.treaties.ListOpen(ctx)
	require.NoError(t, err)
	require.Len(t, abiertos, 1)
	require.NotNil(t, abiertos[0].BreakEffectiveAt)
	assert.True(t, fin.Equal(*abiertos[0].BreakEffectiveAt))
	assert.Equal(t, 1, eventosDeTratado(t, ctx, store, "TreatyBreakAnnounced"))

	roto, err := anuncio.TransitionTo(diplomacy.StatusBroken, fin)
	require.NoError(t, err)
	_, err = e.treaties.ApplyTreaty(ctx, postgres.TreatyWrite{Treaty: roto, From: diplomacy.StatusActive, Event: "TreatyBroken"})
	require.NoError(t, err)
	abiertos, err = e.treaties.ListOpen(ctx)
	require.NoError(t, err)
	assert.Empty(t, abiertos)

	_, err = e.treaties.ApplyTreaty(ctx, postgres.TreatyWrite{Treaty: vigente, From: diplomacy.StatusProposed, Event: "TreatyAccepted"})
	assert.ErrorIs(t, err, postgres.ErrVersionConflict, "la fila ya no está en PROPOSED")
}

// RN-DIPLO-008: ListRejectedSince devuelve los rechazos recientes con su cierre.
func TestLosRechazosRecientesSeListanConSuCierre(t *testing.T) {
	store, ctx := newTestStore(t)
	e := nuevoEscenario(t, ctx, store)
	o := oferta(e, diplomacy.TypeNonAggression, uuid.NewString())
	var err error
	o.ID, err = e.treaties.ApplyTreaty(ctx, postgres.TreatyWrite{Insert: true, Treaty: o, From: diplomacy.StatusProposed, Event: "TreatyProposed"})
	require.NoError(t, err)
	rechazada, err := o.TransitionTo(diplomacy.StatusRejected, time.UnixMilli(time.Now().UnixMilli()).UTC())
	require.NoError(t, err)
	_, err = e.treaties.ApplyTreaty(ctx, postgres.TreatyWrite{Treaty: rechazada, From: diplomacy.StatusProposed, Event: "TreatyRejected"})
	require.NoError(t, err)

	recientes, err := e.treaties.ListRejectedSince(ctx, time.Now().Add(-time.Minute))
	require.NoError(t, err)
	require.Len(t, recientes, 1)
	require.NotNil(t, recientes[0].ClosedAt)
	viejos, err := e.treaties.ListRejectedSince(ctx, time.Now().Add(time.Minute))
	require.NoError(t, err)
	assert.Empty(t, viejos)
}

// Nada a medias: una oferta cuyo proponente no es del par la rechaza el CHECK y
// no deja fila ni evento.
func TestUnaOfertaQueFallaNoDejaNada(t *testing.T) {
	store, ctx := newTestStore(t)
	e := nuevoEscenario(t, ctx, store)
	o := oferta(e, diplomacy.TypeAlliance, uuid.NewString())
	extrano := uuid.New()
	o.ProposedBy = &extrano

	_, err := e.treaties.ApplyTreaty(ctx, postgres.TreatyWrite{Insert: true, Treaty: o, From: diplomacy.StatusProposed, Event: "TreatyProposed"})
	require.Error(t, err)
	abiertos, err := e.treaties.ListOpen(ctx)
	require.NoError(t, err)
	assert.Empty(t, abiertos)
	assert.Zero(t, eventosDeTratado(t, ctx, store, "TreatyProposed"))
}
