package simulation_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/garrison"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// Guarnición en la simulación (docs/specs/garrison.md, ADR-017).

type fakeEnter struct {
	UnitID, CityID int64
	Requester      uuid.UUID
	Status         unit.Status // el de la copia que recibe la transacción
}

type fakeLeave struct {
	UnitID       int64
	X, Y, CX, CY int32
}

// fakeGarrisons hace de postgres.GarrisonRepo: registra y devuelve el error
// configurado. Como el repositorio, sólo toca la copia que recibe.
type fakeGarrisons struct {
	mu       sync.Mutex
	enterErr error
	leaveErr error
	enters   []fakeEnter
	leaves   []fakeLeave
}

func (f *fakeGarrisons) Enter(_ context.Context, u *unit.Unit, c *city.City, requester uuid.UUID, _ uint64) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.enters = append(f.enters, fakeEnter{UnitID: u.ID, CityID: c.ID, Requester: requester, Status: u.Status})
	if f.enterErr != nil {
		return 0, f.enterErr
	}
	u.Status = unit.StatusGarrisoned
	return 0, nil
}

func (f *fakeGarrisons) Leave(_ context.Context, u *unit.Unit, x, y, cx, cy int32, _ uint64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.leaves = append(f.leaves, fakeLeave{UnitID: u.ID, X: x, Y: y, CX: cx, CY: cy})
	if f.leaveErr != nil {
		return f.leaveErr
	}
	u.Status = unit.StatusIdle
	return nil
}

func (f *fakeGarrisons) entradas() []fakeEnter {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakeEnter(nil), f.enters...)
}

func (f *fakeGarrisons) salidas() []fakeLeave {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakeLeave(nil), f.leaves...)
}

// errorDe devuelve el único system.error emitido, con su requestId.
func errorDe(t *testing.T, h *harness) (string, string) {
	t.Helper()
	errs := h.rec.byType(protocol.TypeSystemError)
	require.Len(t, errs, 1)
	return errs[0].Payload.(protocol.SystemErrorPayload).Code, errs[0].RequestID
}

// guarnecer pide la entrada de una unidad en la ciudad del jugador y deja que
// llegue el resultado.
func (h *harness) guarnecer(unitID, cityID int64, requestID string) {
	h.t.Helper()
	h.send(simulation.GarrisonUnit{PlayerID: h.playerID, RequestID: requestID, UnitID: unitID, CityID: cityID})
	h.loop.Step(h.clk.NowMs())
}

// La unidad del jugador (12,10) toca la zona urbana de su ciudad (9..11).
func TestGuarnecerEnLaCiudadPropiaEnDosFases(t *testing.T) {
	h := newHarness(t)
	u := h.units[0]
	c, _ := h.state.City(h.cityID)
	h.rec.reset()

	h.guarnecer(u.ID, h.cityID, "req-entrar")

	// La transacción recibió la unidad tal como estaba en el tick: IDLE.
	require.Equal(t, []fakeEnter{{UnitID: u.ID, CityID: h.cityID, Requester: h.playerID, Status: unit.StatusIdle}},
		h.garrisons.entradas())
	assert.Equal(t, unit.StatusGarrisoned, u.Status)
	anfitriona, ok := h.state.GarrisonCity(u.ID)
	require.True(t, ok)
	assert.Equal(t, h.cityID, anfitriona)
	assert.False(t, h.state.IsEntering(u.ID))

	// El dueño recibe la respuesta con su requestId y la ciudad anfitriona.
	updates := h.rec.byType(protocol.TypeEntityUpdate)
	require.Len(t, updates, 1)
	assert.Equal(t, "req-entrar", updates[0].RequestID)
	assert.Equal(t, h.playerID, updates[0].PlayerID)
	p := updates[0].Payload.(protocol.EntityUpdatePayload)
	require.NotNil(t, p.Status)
	assert.Equal(t, "GARRISONED", *p.Status)
	require.NotNil(t, p.GarrisonCityID)
	require.NotNil(t, *p.GarrisonCityID)
	assert.Equal(t, h.cityID, **p.GarrisonCityID)

	// Los demás la pierden de vista; el dueño no recibe ese despawn.
	despawns := h.rec.byType(protocol.TypeEntityDespawn)
	require.Len(t, despawns, 1)
	assert.Equal(t, "chunk-except", despawns[0].Kind)
	assert.Equal(t, h.playerID, despawns[0].PlayerID)
	assert.Equal(t, protocol.DespawnGarrisoned, despawns[0].Payload.(protocol.EntityDespawnPayload).Reason)

	// RN-GARR-021: ni la ciudad de origen ni la población cambian, y por eso no
	// hay city.update.
	require.NotNil(t, u.CityID)
	assert.Equal(t, h.cityID, *u.CityID)
	assert.EqualValues(t, 3, c.Population)
	assert.Empty(t, h.rec.byType(protocol.TypeCityUpdate))

	// INV-GARR-005: guarnecer no toca la capa de ocupación.
	assert.True(t, h.state.World().IsWalkable(12, 10))
}

func TestRechazosDeGuarnicion(t *testing.T) {
	ajeno := uuid.MustParse("22222222-2222-4222-8222-222222222222")
	casos := []struct {
		nombre   string
		preparar func(h *harness) (unitID, cityID int64)
		codigo   string
	}{
		{"unidad inexistente", func(h *harness) (int64, int64) { return 999, h.cityID }, protocol.CodeUnitNotFound},
		{"unidad ajena", func(h *harness) (int64, int64) {
			h.state.AddUnit(&unit.Unit{ID: 50, PlayerID: ajeno, Type: unit.TypeVillager, X: 12, Y: 11,
				HP: 40, MaxHP: 40, Status: unit.StatusIdle})
			return 50, h.cityID
		}, protocol.CodeUnitNotOwned},
		{"unidad muerta", func(h *harness) (int64, int64) {
			h.units[0].Status, h.units[0].HP = unit.StatusDead, 0
			return h.units[0].ID, h.cityID
		}, protocol.CodeUnitDead},
		{"unidad oculta", func(h *harness) (int64, int64) {
			h.units[0].Status = unit.StatusHidden
			return h.units[0].ID, h.cityID
		}, protocol.CodeUnitNotMovable},
		{"unidad en marcha", func(h *harness) (int64, int64) {
			h.send(simulation.MoveUnit{PlayerID: h.playerID, RequestID: "m", UnitID: h.units[0].ID,
				Target: world.Tile{X: 30, Y: 30}})
			h.rec.reset()
			return h.units[0].ID, h.cityID
		}, protocol.CodeUnitNotMovable},
		{"unidad ya guarnecida", func(h *harness) (int64, int64) {
			h.units[0].Status = unit.StatusGarrisoned
			return h.units[0].ID, h.cityID
		}, protocol.CodeUnitGarrisoned},
		{"ciudad inexistente", func(h *harness) (int64, int64) { return h.units[0].ID, 77 }, protocol.CodeCityNotFound},
		{"unidad lejos de la ciudad", func(h *harness) (int64, int64) {
			h.state.MoveUnitTo(h.units[0], world.Tile{X: 20, Y: 20})
			return h.units[0].ID, h.cityID
		}, protocol.CodeInvalidTarget},
	}
	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			h := newHarness(t)
			unitID, cityID := c.preparar(h)
			h.rec.reset()

			h.send(simulation.GarrisonUnit{PlayerID: h.playerID, RequestID: "req", UnitID: unitID, CityID: cityID})

			codigo, req := errorDe(t, h)
			assert.Equal(t, c.codigo, codigo)
			assert.Equal(t, "req", req)
			assert.Empty(t, h.garrisons.entradas(), "un rechazo del tick no llega a la base")
			assert.False(t, h.state.IsEntering(unitID))
			assert.Empty(t, h.rec.byType(protocol.TypeEntityDespawn))
		})
	}
}

// RN-GARR-005: el tratado lo decide la transacción. Su rechazo vuelve como
// TREATY_REQUIRED con el requestId, y la unidad sigue siendo ordenable.
func TestLaCiudadAjenaSinTratadoSeRechazaEnLaTransaccion(t *testing.T) {
	h := newHarness(t)
	ajeno := uuid.MustParse("22222222-2222-4222-8222-222222222222")
	h.state.AddCity(&city.City{ID: 2, OwnerPlayerID: ajeno, Name: "Ajena", CenterX: 40, CenterY: 40,
		Era: city.EraStone, PopulationLimit: 20, PresenceState: city.PresenceProtected})
	u := h.units[0]
	h.state.MoveUnitTo(u, world.Tile{X: 42, Y: 41})
	h.garrisons.enterErr = garrison.ErrTreatyRequired
	h.rec.reset()

	h.guarnecer(u.ID, 2, "req-ajena")

	require.Len(t, h.garrisons.entradas(), 1, "la ciudad ajena llega a la transacción aunque esté PROTECTED")
	codigo, req := errorDe(t, h)
	assert.Equal(t, protocol.CodeTreatyRequired, codigo)
	assert.Equal(t, "req-ajena", req)
	assert.Equal(t, unit.StatusIdle, u.Status)
	assert.False(t, h.state.IsEntering(u.ID))

	h.rec.reset()
	h.send(simulation.MoveUnit{PlayerID: h.playerID, RequestID: "m", UnitID: u.ID, Target: world.Tile{X: 30, Y: 30}})
	assert.Len(t, h.rec.byType(protocol.TypeUnitMoveAccepted), 1, "rechazada la entrada, vuelve a aceptar órdenes")
}

// RN-GARR-020: mientras su transacción no responde, la unidad no acepta
// órdenes y el volcado no la escribe.
func TestMientrasEntraLaUnidadNoAceptaOrdenesNiSeVuelca(t *testing.T) {
	h := newHarness(t)
	u := h.units[0]
	h.garrisons.enterErr = errors.New("base caída") // transitorio: no hay respuesta todavía
	h.send(simulation.GarrisonUnit{PlayerID: h.playerID, RequestID: "req", UnitID: u.ID, CityID: h.cityID})
	require.True(t, h.state.IsEntering(u.ID))
	assert.Equal(t, unit.StatusIdle, u.Status)
	h.rec.reset()

	h.send(simulation.MoveUnit{PlayerID: h.playerID, RequestID: "m", UnitID: u.ID, Target: world.Tile{X: 30, Y: 30}})
	rechazos := h.rec.byType(protocol.TypeUnitMoveRejected)
	require.Len(t, rechazos, 1)
	assert.Equal(t, protocol.CodeUnitNotMovable, rechazos[0].Payload.(protocol.UnitMoveRejectedPayload).Code)

	for _, cmd := range []simulation.Command{
		simulation.GarrisonUnit{PlayerID: h.playerID, RequestID: "g", UnitID: u.ID, CityID: h.cityID},
		simulation.UngarrisonUnit{PlayerID: h.playerID, RequestID: "u", UnitID: u.ID},
	} {
		h.rec.reset()
		h.send(cmd)
		codigo, _ := errorDe(t, h)
		assert.Equal(t, protocol.CodeUnitNotMovable, codigo)
	}

	// Sucia o no, el volcado la aplaza.
	h.state.MarkDirty(u.ID)
	antes := h.repos.unitsFlushed
	h.sim.FlushDirty()
	assert.Equal(t, antes, h.repos.unitsFlushed, "volcaría IDLE encima del GARRISONED que se está escribiendo")
	assert.Equal(t, 1, h.state.DirtyCount(), "y sigue pendiente para el siguiente volcado")

	// Llega el resultado: entra.
	h.send(simulation.GarrisonEntryResolved{PlayerID: h.playerID, RequestID: "req", UnitID: u.ID, CityID: h.cityID})
	assert.Equal(t, unit.StatusGarrisoned, u.Status)
	assert.False(t, h.state.IsEntering(u.ID))
}

// Un fallo definitivo de la transacción se responde con INTERNAL_ERROR y libera
// la unidad.
func TestUnFalloDefinitivoDeLaEntradaRespondeInternalError(t *testing.T) {
	h := newHarness(t)
	h.persister.permanent = true
	h.garrisons.enterErr = errors.New("base caída")
	u := h.units[0]
	h.rec.reset()

	h.guarnecer(u.ID, h.cityID, "req")

	codigo, req := errorDe(t, h)
	assert.Equal(t, protocol.CodeInternalError, codigo)
	assert.Equal(t, "req", req)
	assert.Equal(t, unit.StatusIdle, u.Status)
	assert.False(t, h.state.IsEntering(u.ID))
}

// guarnecida deja la primera unidad dentro de la ciudad del jugador sin pasar
// por la transacción, como la deja el arranque desde `garrisons`.
func guarnecida(h *harness) *unit.Unit {
	u := h.units[0]
	u.Status = unit.StatusGarrisoned
	h.state.SetGarrisons(map[int64]int64{u.ID: h.cityID})
	return u
}

// RN-GARR-011: el anillo exterior de la zona urbana (9..11) en orden (y, x):
// el primero es (8,8).
func TestSalirDeLaGuarnicionVuelveAlAnilloEnOrden(t *testing.T) {
	h := newHarness(t)
	u := guarnecida(h)
	h.rec.reset()

	h.send(simulation.UngarrisonUnit{PlayerID: h.playerID, RequestID: "req-salir", UnitID: u.ID})

	assert.Equal(t, unit.StatusIdle, u.Status)
	assert.Equal(t, world.Tile{X: 8, Y: 8}, u.Tile())
	_, dentro := h.state.GarrisonCity(u.ID)
	assert.False(t, dentro)
	require.Equal(t, []fakeLeave{{UnitID: u.ID, X: 8, Y: 8, CX: 0, CY: 0}}, h.garrisons.salidas())

	updates := h.rec.byType(protocol.TypeEntityUpdate)
	require.Len(t, updates, 1)
	assert.Equal(t, "req-salir", updates[0].RequestID)
	p := updates[0].Payload.(protocol.EntityUpdatePayload)
	assert.Equal(t, "IDLE", *p.Status)
	assert.Equal(t, int32(8), *p.X)
	assert.Equal(t, int32(8), *p.Y)
	require.NotNil(t, p.GarrisonCityID, "la clave viaja")
	assert.Nil(t, *p.GarrisonCityID, "con null: ya no está en ninguna ciudad")

	spawns := h.rec.byType(protocol.TypeEntitySpawn)
	require.Len(t, spawns, 1)
	assert.Equal(t, "chunk-except", spawns[0].Kind)
	assert.Equal(t, h.playerID, spawns[0].PlayerID)
	vista := spawns[0].Payload.(protocol.EntitySpawnPayload).Unit
	assert.Nil(t, vista.GarrisonCityID)
	assert.Equal(t, "IDLE", vista.Status)
}

func TestElPrimerTileDeReentradaIntransitablePasaAlSiguiente(t *testing.T) {
	h := newHarnessConTerreno(t, func(terrain []byte) { terrain[8*64+8] = byte(world.Water) })
	u := guarnecida(h)

	h.send(simulation.UngarrisonUnit{PlayerID: h.playerID, RequestID: "req", UnitID: u.ID})

	assert.Equal(t, world.Tile{X: 9, Y: 8}, u.Tile())
}

// RN-GARR-014: sin tile de reentrada la salida no falla: queda pendiente, se
// reintenta en la fase 5 y la respuesta llega cuando sale.
func TestUnaCiudadRodeadaDejaLaSalidaPendienteYLaReintenta(t *testing.T) {
	h := newHarness(t)
	u := guarnecida(h)
	w := h.state.World()
	w.SetBlocked(8, 8, 12, 12, true) // el anillo, cerrado
	h.rec.reset()

	h.send(simulation.UngarrisonUnit{PlayerID: h.playerID, RequestID: "req-pendiente", UnitID: u.ID})
	h.advance(500 * time.Millisecond)

	assert.Equal(t, unit.StatusGarrisoned, u.Status)
	assert.Empty(t, h.rec.byType(protocol.TypeSystemError), "no es un error del jugador")
	assert.Empty(t, h.rec.byType(protocol.TypeEntityUpdate))

	// Un segundo intento mientras espera se rechaza: ya está saliendo.
	h.send(simulation.UngarrisonUnit{PlayerID: h.playerID, RequestID: "otra", UnitID: u.ID})
	codigo, _ := errorDe(t, h)
	assert.Equal(t, protocol.CodeUnitNotMovable, codigo)
	h.rec.reset()

	w.SetBlocked(12, 12, 12, 12, false) // se abre la esquina (12,12)
	h.advance(100 * time.Millisecond)

	assert.Equal(t, unit.StatusIdle, u.Status)
	assert.Equal(t, world.Tile{X: 12, Y: 12}, u.Tile())
	updates := h.rec.byType(protocol.TypeEntityUpdate)
	require.Len(t, updates, 1)
	assert.Equal(t, "req-pendiente", updates[0].RequestID, "contesta a la orden original")
}

// ADR-017 §2.3: si la salida no se puede escribir, la base sigue teniéndola
// dentro, y la RAM vuelve a coincidir con ella.
func TestUnaSalidaQueNoSePuedeEscribirDevuelveLaUnidadALaCiudad(t *testing.T) {
	h := newHarness(t)
	u := guarnecida(h)
	antes := u.Tile()
	h.persister.permanent = true
	h.garrisons.leaveErr = errors.New("base caída")
	h.rec.reset()

	h.send(simulation.UngarrisonUnit{PlayerID: h.playerID, RequestID: "req", UnitID: u.ID})
	h.loop.Step(h.clk.NowMs())

	assert.Equal(t, unit.StatusGarrisoned, u.Status)
	assert.Equal(t, antes, u.Tile())
	anfitriona, ok := h.state.GarrisonCity(u.ID)
	require.True(t, ok)
	assert.Equal(t, h.cityID, anfitriona)

	despawns := h.rec.byType(protocol.TypeEntityDespawn)
	require.Len(t, despawns, 1, "los terceros que la vieron salir la pierden otra vez")
	assert.Equal(t, protocol.DespawnGarrisoned, despawns[0].Payload.(protocol.EntityDespawnPayload).Reason)
	updates := h.rec.byType(protocol.TypeEntityUpdate)
	require.Len(t, updates, 2, "la salida y su corrección")
	assert.Equal(t, "GARRISONED", *updates[1].Payload.(protocol.EntityUpdatePayload).Status)
}

func TestSoloSePuedeSacarUnaUnidadGuarnecidaPropia(t *testing.T) {
	h := newHarness(t)
	ajeno := uuid.MustParse("22222222-2222-4222-8222-222222222222")
	u := guarnecida(h)

	h.rec.reset()
	h.send(simulation.UngarrisonUnit{PlayerID: ajeno, RequestID: "req", UnitID: u.ID})
	codigo, _ := errorDe(t, h)
	assert.Equal(t, protocol.CodeUnitNotOwned, codigo, "el anfitrión no echa a mano lo que aloja")

	h.rec.reset()
	h.send(simulation.UngarrisonUnit{PlayerID: h.playerID, RequestID: "req", UnitID: h.units[1].ID})
	codigo, _ = errorDe(t, h)
	assert.Equal(t, protocol.CodeUnitNotMovable, codigo, "una unidad en el mapa no sale de ninguna parte")
	assert.Empty(t, h.garrisons.salidas())
}

// INV-GARR-006: el snapshot aplica el mismo filtro que los deltas. El dueño ve
// su unidad guarnecida, con su anfitriona; nadie más la ve.
func TestUnaUnidadGuarnecidaSoloLaVeSuDuenoEnElSnapshot(t *testing.T) {
	h := newHarness(t)
	u := guarnecida(h)
	ajeno := uuid.MustParse("22222222-2222-4222-8222-222222222222")
	chunks := []world.ChunkCoord{{CX: 0, CY: 0}}

	propio := h.sim.BuildSnapshot(h.playerID, chunks, h.clk.NowMs(), false)
	var vista *protocol.UnitView
	for i := range propio.Units {
		if propio.Units[i].ID == u.ID {
			vista = &propio.Units[i]
		}
	}
	require.NotNil(t, vista, "su dueño la encuentra al reconectar")
	assert.Equal(t, "GARRISONED", vista.Status)
	require.NotNil(t, vista.GarrisonCityID)
	assert.Equal(t, h.cityID, *vista.GarrisonCityID)

	otro := h.sim.BuildSnapshot(ajeno, chunks, h.clk.NowMs(), false)
	for _, v := range otro.Units {
		assert.NotEqual(t, u.ID, v.ID, "un tercero no ve la unidad guarnecida")
	}
	assert.Len(t, otro.Units, len(propio.Units)-1)
}
