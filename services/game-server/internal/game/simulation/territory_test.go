package simulation_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/territory"
	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// conTerritorios instala dos territorios en el mundo de 64 × 64 del harness,
// con chunks de 32: el 1 cabe en el chunk (0,0) y el 2 cruza los cuatro.
func conTerritorios(t *testing.T, h *harness) {
	t.Helper()
	set, solapes, err := territory.BuildSet([]territory.Territory{
		{ID: 1, Name: "Rincón", MinX: 0, MinY: 0, MaxX: 15, MaxY: 15},
		{ID: 2, Name: "Encrucijada", MinX: 20, MinY: 20, MaxX: 50, MaxY: 40},
	}, 64, 64, 32)
	require.NoError(t, err)
	require.Empty(t, solapes)
	h.state.SetTerritories(set, []territory.Control{
		{TerritoryID: 1, OwnerType: territory.OwnerNone},
		{TerritoryID: 2, OwnerType: territory.OwnerNone},
	})
}

func fundacionCon(control *territory.Control) simulation.IntroducePlayer {
	return simulation.IntroducePlayer{
		City: &city.City{
			ID: 77, OwnerPlayerID: uuid.New(), Name: "Encrucijadópolis",
			CenterX: 30, CenterY: 30, Era: city.EraStone, PopulationLimit: 20,
			PresenceState: city.PresenceOnline,
		},
		BlockedMinX: 29, BlockedMinY: 29, BlockedMaxX: 31, BlockedMaxY: 31,
		TerritoryControl: control,
	}
}

// INV-TERR-009: un cambio de control llega a la RAM y se emite UNA vez a la
// huella completa del territorio, con la vista ya actualizada.
func TestUnCambioDeControlSeAplicaYSeEmiteUnaVezALaHuella(t *testing.T) {
	h := newHarness(t)
	conTerritorios(t, h)
	h.rec.reset()

	dueño := uuid.NewString()
	capturado := time.UnixMilli(epoch).UTC()
	h.send(fundacionCon(&territory.Control{
		TerritoryID: 2, OwnerType: territory.OwnerPlayer, OwnerID: &dueño,
		CapturedAt: &capturado, Version: 1,
	}))

	enRAM, ok := h.state.TerritoryControl(2)
	require.True(t, ok)
	assert.Equal(t, territory.OwnerPlayer, enRAM.OwnerType)
	assert.EqualValues(t, 1, enRAM.Version)

	updates := h.rec.byType(protocol.TypeTerritoryUpdate)
	require.Len(t, updates, 1, "una sola emisión aunque la huella abarque cuatro chunks (RN-TERR-012)")
	assert.Equal(t, "chunks", updates[0].Kind)
	assert.ElementsMatch(t, []world.ChunkCoord{
		{CX: 0, CY: 0}, {CX: 1, CY: 0}, {CX: 0, CY: 1}, {CX: 1, CY: 1},
	}, updates[0].Chunks, "la huella completa del territorio, ni más ni menos")

	vista := updates[0].Payload.(protocol.TerritoryUpdatePayload).Territory
	assert.EqualValues(t, 2, vista.ID)
	assert.Equal(t, "PLAYER", vista.OwnerType)
	require.NotNil(t, vista.OwnerID)
	assert.Equal(t, dueño, *vista.OwnerID)
}

// La otra mitad de INV-TERR-009: sin cambio de control no se emite nada.
// Fundar en territorio ajeno o fuera de todo territorio llega sin control.
func TestUnaFundacionSinCambioDeControlNoEmiteTerritoryUpdate(t *testing.T) {
	h := newHarness(t)
	conTerritorios(t, h)
	h.rec.reset()

	h.send(fundacionCon(nil))

	assert.Empty(t, h.rec.byType(protocol.TypeTerritoryUpdate))
	c, _ := h.state.TerritoryControl(2)
	assert.Equal(t, territory.OwnerNone, c.OwnerType)
}

// Un control que referencia un territorio que el índice no conoce es
// INV-TERR-003 roto: no hay huella a la que emitir y no se inventa una.
func TestUnControlDeTerritorioInexistenteNoEmiteNada(t *testing.T) {
	h := newHarness(t)
	conTerritorios(t, h)
	h.rec.reset()

	dueño := uuid.NewString()
	capturado := time.UnixMilli(epoch).UTC()
	h.send(fundacionCon(&territory.Control{
		TerritoryID: 999, OwnerType: territory.OwnerPlayer, OwnerID: &dueño,
		CapturedAt: &capturado, Version: 1,
	}))

	assert.Empty(t, h.rec.byType(protocol.TypeTerritoryUpdate))
	_, incorporado := h.state.TerritoryControl(999)
	assert.False(t, incorporado, "un control sin territorio no entra en la RAM")
}

// RN-TERR-011: el snapshot transporta los territorios que intersectan el área
// pedida, en orden ascendente de id, con su control vigente; los que caen
// fuera no viajan. Y con el mundo sin territorios cargados, la lista va vacía
// y no nula, que es lo que exige el esquema.
func TestElSnapshotLlevaLosTerritoriosDelAreaYSuControl(t *testing.T) {
	h := newHarness(t)
	sinTerritorios := h.sim.BuildSnapshot(h.playerID, []world.ChunkCoord{{CX: 0, CY: 0}}, h.clk.NowMs(), false)
	assert.NotNil(t, sinTerritorios.Territories)
	assert.Empty(t, sinTerritorios.Territories)

	conTerritorios(t, h)
	dueño := uuid.NewString()
	capturado := time.UnixMilli(epoch).UTC()
	h.send(fundacionCon(&territory.Control{
		TerritoryID: 2, OwnerType: territory.OwnerPlayer, OwnerID: &dueño,
		CapturedAt: &capturado, Version: 1,
	}))

	ids := func(vs []protocol.TerritoryView) []int64 {
		out := make([]int64, 0, len(vs))
		for _, v := range vs {
			out = append(out, v.ID)
		}
		return out
	}

	origen := h.sim.BuildSnapshot(h.playerID, []world.ChunkCoord{{CX: 0, CY: 0}}, h.clk.NowMs(), false)
	assert.Equal(t, []int64{1, 2}, ids(origen.Territories), "los dos tocan el chunk (0,0)")
	assert.Equal(t, "NONE", origen.Territories[0].OwnerType)
	assert.Equal(t, "PLAYER", origen.Territories[1].OwnerType, "con el control vigente, no el sembrado")

	esquina := h.sim.BuildSnapshot(h.playerID, []world.ChunkCoord{{CX: 1, CY: 1}}, h.clk.NowMs(), false)
	assert.Equal(t, []int64{2}, ids(esquina.Territories), "el 1 no toca el chunk (1,1)")
}
