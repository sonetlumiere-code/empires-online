package simulation_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/safezone"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// Cruce de frontera de chunk (docs/architecture/networking.md §4.3): una unidad
// que pasa del chunk A al B se anuncia a las sesiones que ganan o pierden el
// chunk con ella. Estos tests fijan QUÉ emite la simulación, en qué orden y a
// quién lo limita. Quién está suscrito a qué lo resuelve el Hub
// (internal/websocket/hub_transition_test.go), y el camino completo por la red,
// TestUnaUnidadQueCruzaDeChunkApareceYDesapareceEnElAreaDeOtraSesion.
//
// Mundo: el de newHarness, 64 × 64 con chunks de 32. La frontera vertical entre
// los chunks (0,0) y (1,0) está en x = 32.

// tipos devuelve, en orden de emisión, el tipo de cada mensaje capturado.
func tipos(r *recorder) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.messages))
	for _, m := range r.messages {
		out = append(out, m.Type)
	}
	return out
}

// mencionaUnidad dice si un payload habla de la unidad dada, sea cual sea el
// tipo de mensaje.
func mencionaUnidad(payload any, unitID int64) bool {
	switch p := payload.(type) {
	case protocol.EntitySpawnPayload:
		return p.Unit.ID == unitID
	case protocol.EntityUpdatePayload:
		return p.ID == unitID
	case protocol.EntityDespawnPayload:
		return p.ID == unitID
	case protocol.UnitMovementStartedPayload:
		return p.UnitID == unitID
	case protocol.UnitMovementCompletedPayload:
		return p.UnitID == unitID
	case protocol.UnitMovementCancelledPayload:
		return p.UnitID == unitID
	}
	return false
}

// unaTransicion exige que se haya emitido exactamente una transición de la
// unidad —un spawn y un despawn— y la devuelve.
func unaTransicion(t *testing.T, h *harness, unitID int64) (spawn, despawn capturedMessage) {
	t.Helper()
	var spawns, despawns []capturedMessage
	for _, m := range h.rec.byType(protocol.TypeEntitySpawn) {
		if m.Kind == "transition" && mencionaUnidad(m.Payload, unitID) {
			spawns = append(spawns, m)
		}
	}
	for _, m := range h.rec.byType(protocol.TypeEntityDespawn) {
		if m.Kind == "transition" && mencionaUnidad(m.Payload, unitID) {
			despawns = append(despawns, m)
		}
	}
	require.Len(t, spawns, 1, "un cruce, un spawn")
	require.Len(t, despawns, 1, "un cruce, un despawn")
	return spawns[0], despawns[0]
}

// Una unidad en (30,10) que va a (34,10) cruza de (0,0) a (1,0) en su segundo
// paso, y sólo en ése. Quien gana el chunk (1,0) la recibe con su polilínea,
// para interpolar desde el primer mensaje; quien pierde el (0,0), un despawn
// OUT_OF_INTEREST; y la transición precede al entity.update del paso.
func TestCruzarDeChunkAnunciaLaUnidadConSuMovimientoAQuienGanaElChunk(t *testing.T) {
	h := newHarness(t)
	u := h.ponerUnidad(10, h.playerID, world.Tile{X: 30, Y: 10}, unit.StatusIdle, 40)
	h.loop.Step(h.clk.NowMs())
	h.rec.reset()

	h.mover(u.ID, world.Tile{X: 34, Y: 10})
	started := h.rec.byType(protocol.TypeUnitMovementStarted)
	require.Len(t, started, 1)
	movementID := started[0].Payload.(protocol.UnitMovementStartedPayload).Movement.MovementID

	// Primer paso, (31,10): mismo chunk, nada que anunciar.
	h.rec.reset()
	h.advance(600 * time.Millisecond)
	require.Equal(t, world.Tile{X: 31, Y: 10}, u.Tile())
	assert.Equal(t, []string{protocol.TypeEntityUpdate}, tipos(h.rec))

	// Segundo paso, (32,10): del chunk (0,0) al (1,0).
	h.rec.reset()
	h.advance(600 * time.Millisecond)
	require.Equal(t, world.Tile{X: 32, Y: 10}, u.Tile())
	assert.Equal(t, []string{protocol.TypeEntitySpawn, protocol.TypeEntityDespawn, protocol.TypeEntityUpdate}, tipos(h.rec),
		"la transición precede al delta del paso: quien gana la unidad la conoce antes de recibir su entity.update")

	spawn, despawn := unaTransicion(t, h, u.ID)
	assert.Equal(t, [4]int32{1, 0, 0, 0}, [4]int32{spawn.CX, spawn.CY, spawn.MinusCX, spawn.MinusCY},
		"el spawn va a quien observa (1,0) y no (0,0)")
	assert.Equal(t, uuid.Nil, spawn.PlayerID, "una unidad visible se anuncia a todas las sesiones")
	vista := spawn.Payload.(protocol.EntitySpawnPayload).Unit
	assert.Equal(t, u.ID, vista.ID)
	assert.Equal(t, string(unit.StatusMoving), vista.Status)
	assert.Equal(t, [2]int32{32, 10}, [2]int32{vista.X, vista.Y})
	require.NotNil(t, vista.Movement, "el spawn lleva el movimiento activo")
	assert.Equal(t, movementID, vista.Movement.MovementID)
	assert.Len(t, vista.Movement.Path, 5)

	assert.Equal(t, [4]int32{0, 0, 1, 0}, [4]int32{despawn.CX, despawn.CY, despawn.MinusCX, despawn.MinusCY},
		"el despawn va a quien observa (0,0) y no (1,0)")
	assert.Equal(t, uuid.Nil, despawn.PlayerID)
	assert.Equal(t, protocol.EntityDespawnPayload{ID: u.ID, Reason: protocol.DespawnOutOfInterest}, despawn.Payload)

	// El resto del camino queda dentro del chunk (1,0): sólo deltas y la llegada.
	h.rec.reset()
	h.advance(1200 * time.Millisecond)
	require.Equal(t, world.Tile{X: 34, Y: 10}, u.Tile())
	assert.Equal(t, []string{
		protocol.TypeEntityUpdate, protocol.TypeEntityUpdate, protocol.TypeUnitMovementCompleted,
	}, tipos(h.rec))
}

// Una cancelación que llega en el tick en que la unidad acaba de cruzar la
// frontera se aplica en la fase 2, antes de que la fase 3 la mueva: la unidad
// queda parada al otro lado y quien gana el chunk la recibe ya IDLE, antes del
// unit.movement.cancelled.
func TestDetenerseJustoTrasCruzarAnunciaLaUnidadYaParada(t *testing.T) {
	h := newHarness(t)
	u := h.ponerUnidad(10, h.playerID, world.Tile{X: 30, Y: 10}, unit.StatusIdle, 40)
	h.loop.Step(h.clk.NowMs())
	h.mover(u.ID, world.Tile{X: 34, Y: 10})
	h.advance(600 * time.Millisecond)
	require.Equal(t, world.Tile{X: 31, Y: 10}, u.Tile())

	h.rec.reset()
	h.clk.AdvanceMs(600) // sin tick: la cancelación encuentra la unidad en (32,10)
	h.send(simulation.CancelMovement{PlayerID: h.playerID, RequestID: "req", UnitID: u.ID})

	require.Equal(t, world.Tile{X: 32, Y: 10}, u.Tile())
	require.Equal(t, unit.StatusIdle, u.Status)
	assert.Equal(t, []string{
		protocol.TypeEntitySpawn, protocol.TypeEntityDespawn, protocol.TypeUnitMovementCancelled,
	}, tipos(h.rec))

	spawn, despawn := unaTransicion(t, h, u.ID)
	vista := spawn.Payload.(protocol.EntitySpawnPayload).Unit
	assert.Equal(t, string(unit.StatusIdle), vista.Status, "se anuncia con el estado ya definitivo")
	assert.Nil(t, vista.Movement)
	assert.Equal(t, [2]int32{32, 10}, [2]int32{vista.X, vista.Y})
	assert.Equal(t, protocol.DespawnOutOfInterest, despawn.Payload.(protocol.EntityDespawnPayload).Reason)

	cancelled := h.rec.byType(protocol.TypeUnitMovementCancelled)
	require.Len(t, cancelled, 1)
	assert.Equal(t, "chunk", cancelled[0].Kind)
	assert.Equal(t, [2]int32{1, 0}, [2]int32{cancelled[0].CX, cancelled[0].CY}, "el cancelled va al chunk donde se detuvo")
}

// INV-SAFE-004: una unidad HIDDEN que cambia de chunk —apartada por la muralla
// de una ciudad recién fundada, RN-CITY-022— se anuncia sólo a su dueño. Ningún
// mensaje dirigido a un chunk entero ni a terceros la menciona.
func TestUnaUnidadOcultaQueCruzaDeChunkSoloSeAnunciaASuDueno(t *testing.T) {
	// Bosque de x=30 a x=33: una zona segura a caballo de la frontera entre los
	// chunks (0,0) y (1,0).
	h := newHarnessConTerreno(t, func(terrain []byte) {
		for y := 0; y < 64; y++ {
			for x := 30; x <= 33; x++ {
				terrain[y*64+x] = byte(world.Forest)
			}
		}
	})
	idx, rep, err := safezone.BuildIndex([]safezone.Zone{
		{ID: 1, Name: "Bosque fronterizo", Type: safezone.DenseForest, MinX: 30, MinY: 0, MaxX: 33, MaxY: 63},
	}, h.state.World())
	require.NoError(t, err)
	require.Empty(t, rep.Overlaps)
	h.state.SetSafeZones(idx)

	u := h.ponerUnidad(10, h.playerID, world.Tile{X: 31, Y: 20}, unit.StatusIdle, 40)
	h.loop.Step(h.clk.NowMs())
	require.Equal(t, unit.StatusHidden, u.Status, "en reposo dentro del bosque se oculta")
	h.rec.reset()

	// Una ciudad ajena en (30,20) amuralla 29..31 × 19..21 y deja la unidad
	// dentro: se aparta a (32,19), en el chunk (1,0) y todavía en el bosque.
	h.fundar(99, world.Tile{X: 30, Y: 20})
	require.Equal(t, world.Tile{X: 32, Y: 19}, u.Tile())
	require.Equal(t, unit.StatusHidden, u.Status, "sigue en la zona: sigue oculta")
	h.verificarOcultamiento()

	spawn, despawn := unaTransicion(t, h, u.ID)
	assert.Equal(t, h.playerID, spawn.PlayerID, "el spawn del cruce se limita al dueño")
	assert.Equal(t, h.playerID, despawn.PlayerID, "y el despawn también")
	assert.Equal(t, string(unit.StatusHidden), spawn.Payload.(protocol.EntitySpawnPayload).Unit.Status)
	assert.Equal(t, [4]int32{1, 0, 0, 0}, [4]int32{spawn.CX, spawn.CY, spawn.MinusCX, spawn.MinusCY})

	h.rec.mu.Lock()
	defer h.rec.mu.Unlock()
	for _, m := range h.rec.messages {
		if m.Kind == "chunk" || m.Kind == "chunk-except" {
			assert.False(t, mencionaUnidad(m.Payload, u.ID),
				"una unidad oculta no viaja a un chunk entero ni a terceros: %s %s", m.Kind, m.Type)
		}
	}
}

// ciudadFronteriza incorpora una ciudad propia con la zona urbana 30..32 × 9..11:
// una unidad que entra desde (33,10), chunk (1,0), sale por el primer tile
// libre del anillo, (29,8), en el chunk (0,0).
func ciudadFronteriza(h *harness) (*unit.Unit, int64) {
	h.t.Helper()
	const cityID int64 = 2
	h.state.AddCity(&city.City{
		ID: cityID, OwnerPlayerID: h.playerID, Name: "Fronteriza",
		CenterX: 31, CenterY: 10, Era: city.EraStone, PopulationLimit: 20,
		PresenceState: city.PresenceOnline,
	})
	u := h.ponerUnidad(10, h.playerID, world.Tile{X: 33, Y: 10}, unit.StatusIdle, 40)
	h.guarnecer(u.ID, cityID, "req-entrar")
	require.Equal(h.t, unit.StatusGarrisoned, u.Status)
	h.rec.reset()
	return u, cityID
}

// Salir de la guarnición a un tile de otro chunk: los terceros no veían la
// unidad guarnecida (INV-GARR-006) y la reciben una sola vez, por el
// entity.spawn de la salida; la transición de chunk se limita al dueño, cuyas
// sesiones sí la tenían o la ganan.
func TestSalirDeLaGuarnicionAOtroChunkSoloAnunciaLaTransicionAlDueno(t *testing.T) {
	h := newHarness(t)
	u, _ := ciudadFronteriza(h)

	h.send(simulation.UngarrisonUnit{PlayerID: h.playerID, RequestID: "req-salir", UnitID: u.ID})
	require.Equal(t, world.Tile{X: 29, Y: 8}, u.Tile())
	require.Equal(t, unit.StatusIdle, u.Status)

	assert.Equal(t, []string{
		protocol.TypeEntitySpawn, protocol.TypeEntityDespawn, // la transición, sólo al dueño
		protocol.TypeEntityUpdate, // la respuesta al dueño
		protocol.TypeEntitySpawn,  // los terceros del chunk de salida
	}, tipos(h.rec))

	spawn, despawn := unaTransicion(t, h, u.ID)
	assert.Equal(t, h.playerID, spawn.PlayerID)
	assert.Equal(t, h.playerID, despawn.PlayerID)
	assert.Equal(t, [4]int32{0, 0, 1, 0}, [4]int32{spawn.CX, spawn.CY, spawn.MinusCX, spawn.MinusCY})
	vista := spawn.Payload.(protocol.EntitySpawnPayload).Unit
	assert.Equal(t, string(unit.StatusIdle), vista.Status, "con el estado ya definitivo")
	assert.Nil(t, vista.GarrisonCityID)
	assert.Equal(t, [2]int32{29, 8}, [2]int32{vista.X, vista.Y})

	terceros := h.rec.byType(protocol.TypeEntitySpawn)[1]
	assert.Equal(t, "chunk-except", terceros.Kind)
	assert.Equal(t, h.playerID, terceros.PlayerID, "el spawn de la salida sigue excluyendo al dueño")
	assert.Equal(t, [2]int32{0, 0}, [2]int32{terceros.CX, terceros.CY})
}

// ADR-017 §2.3 con cruce de chunk: la compensación devuelve la unidad a su
// ciudad, en el chunk de origen. Los terceros reciben el despawn GARRISONED de
// siempre; la transición de vuelta, con la unidad ya GARRISONED, sólo al dueño.
func TestUnaSalidaFallidaDevuelveLaUnidadASuChunkSoloParaElDueno(t *testing.T) {
	h := newHarness(t)
	u, cityID := ciudadFronteriza(h)
	h.persister.permanent = true
	h.garrisons.leaveErr = errors.New("base caída")

	h.send(simulation.UngarrisonUnit{PlayerID: h.playerID, RequestID: "req-salir", UnitID: u.ID})
	h.loop.Step(h.clk.NowMs()) // la compensación vuelve por el inbox

	require.Equal(t, unit.StatusGarrisoned, u.Status)
	require.Equal(t, world.Tile{X: 33, Y: 10}, u.Tile())

	var transiciones, aTerceros []capturedMessage
	h.rec.mu.Lock()
	for _, m := range h.rec.messages {
		switch m.Kind {
		case "transition":
			transiciones = append(transiciones, m)
		case "chunk-except":
			aTerceros = append(aTerceros, m)
		case "chunk":
			assert.False(t, mencionaUnidad(m.Payload, u.ID), "nada va a un chunk entero: %s", m.Type)
		}
	}
	h.rec.mu.Unlock()

	require.Len(t, transiciones, 4, "la ida y la vuelta")
	for _, m := range transiciones {
		assert.Equal(t, h.playerID, m.PlayerID, "ninguna transición alcanza a terceros: %s", m.Type)
	}
	vuelta := transiciones[2]
	assert.Equal(t, protocol.TypeEntitySpawn, vuelta.Type)
	assert.Equal(t, [4]int32{1, 0, 0, 0}, [4]int32{vuelta.CX, vuelta.CY, vuelta.MinusCX, vuelta.MinusCY})
	vista := vuelta.Payload.(protocol.EntitySpawnPayload).Unit
	assert.Equal(t, string(unit.StatusGarrisoned), vista.Status, "el dueño la recibe ya dentro de la ciudad")
	require.NotNil(t, vista.GarrisonCityID)
	assert.Equal(t, cityID, *vista.GarrisonCityID)

	// Lo que ven los terceros no cambia: la vieron salir y la ven volver a entrar.
	require.Len(t, aTerceros, 2)
	assert.Equal(t, protocol.TypeEntitySpawn, aTerceros[0].Type)
	assert.Equal(t, protocol.TypeEntityDespawn, aTerceros[1].Type)
	assert.Equal(t, protocol.DespawnGarrisoned, aTerceros[1].Payload.(protocol.EntityDespawnPayload).Reason)
}
