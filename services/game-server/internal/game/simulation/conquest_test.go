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
	"github.com/empires-online/empires-online/services/game-server/internal/domain/diplomacy"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/eventlog"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/territory"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// Tests de simulación de la conquista (SPEC-TERRITORY-CONQUEST §9, ADR-034).
//
// Mundo de newHarness (64 × 64) dividido en cuatro territorios de 32 × 32:
// 1 «Norte» (0-31, 0-31) con la ciudad propia en (10,10); 2 «Este» (32-63,
// 0-31); 3 «Sur» (0-31, 32-63) con la ciudad enemiga de conEnemigo en (20,40);
// 4 «Sureste» (32-63, 32-63).

// fakeConquest es el almacén de la conquista: apunta lo escrito y puede fallar.
type fakeConquest struct {
	mu         sync.Mutex
	nextID     int64
	starts     []simulation.ClaimStart
	ends       []simulation.ClaimEnd
	captures   []simulation.Capture
	startErr   error
	captureErr error
}

func (f *fakeConquest) StartClaim(_ context.Context, c simulation.ClaimStart) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.startErr != nil {
		return 0, f.startErr
	}
	f.nextID++
	f.starts = append(f.starts, c)
	return f.nextID, nil
}

func (f *fakeConquest) EndClaim(_ context.Context, e simulation.ClaimEnd) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ends = append(f.ends, e)
	return nil
}

func (f *fakeConquest) CaptureTerritory(_ context.Context, c simulation.Capture) (territory.Control, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.captureErr != nil {
		return territory.Control{}, f.captureErr
	}
	f.captures = append(f.captures, c)
	owner := c.NewOwner.String()
	at := c.CapturedAt
	return territory.Control{
		TerritoryID: c.TerritoryID, OwnerType: territory.OwnerPlayer, OwnerID: &owner, CapturedAt: &at,
		Version: c.ExpectedVersion + 1,
	}, nil
}

func (f *fakeConquest) endReasons() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, e := range f.ends {
		out = append(out, e.Reason)
	}
	return out
}

var cuatroTerritorios = []territory.Territory{
	{ID: 1, Name: "Norte", MinX: 0, MinY: 0, MaxX: 31, MaxY: 31},
	{ID: 2, Name: "Este", MinX: 32, MinY: 0, MaxX: 63, MaxY: 31},
	{ID: 3, Name: "Sur", MinX: 0, MinY: 32, MaxX: 31, MaxY: 63},
	{ID: 4, Name: "Sureste", MinX: 32, MinY: 32, MaxX: 63, MaxY: 63},
}

// conTerritorios instala los cuatro territorios; duenos dice de quién es cada uno.
func conCuatroTerritorios(h *harness, duenos map[int64]uuid.UUID) {
	h.t.Helper()
	set, overlaps, err := territory.BuildSet(cuatroTerritorios, 64, 64, 32)
	require.NoError(h.t, err)
	require.Empty(h.t, overlaps)
	var controls []territory.Control
	for _, t := range cuatroTerritorios {
		c := territory.Control{TerritoryID: t.ID, OwnerType: territory.OwnerNone}
		if p, ok := duenos[t.ID]; ok {
			id, at := p.String(), h.clk.Now()
			c.OwnerType, c.OwnerID, c.CapturedAt, c.Version = territory.OwnerPlayer, &id, &at, 1
		}
		controls = append(controls, c)
	}
	h.state.SetTerritories(set, controls)
}

func (h *harness) reclamar(unitID int64) {
	h.t.Helper()
	h.send(simulation.ClaimTerritory{PlayerID: h.playerID, RequestID: uuid.NewString(), UnitID: unitID})
}

func (h *harness) vistaDe(id int64) protocol.TerritoryView {
	h.t.Helper()
	views := h.state.TerritoryViews([]world.ChunkCoord{{CX: 0, CY: 0}, {CX: 1, CY: 0}, {CX: 0, CY: 1}, {CX: 1, CY: 1}})
	for _, v := range views {
		if v.ID == id {
			return v
		}
	}
	h.t.Fatalf("no hay territorio %d", id)
	return protocol.TerritoryView{}
}

func dueñoDe(v protocol.TerritoryView) string {
	if v.OwnerID == nil {
		return ""
	}
	return *v.OwnerID
}

func avisosDeTipo(h *harness, kind eventlog.Kind) []aviso {
	var out []aviso
	for _, a := range avisosDe(h) {
		if a.p.Kind == string(kind) {
			out = append(out, a)
		}
	}
	return out
}

// Golden «conquista» de un territorio libre (RN-CONQ-001, RN-CONQ-004,
// RN-CONQ-007, ADR-034 §1 y §3): la reclamación existe para los demás cuando
// la base la confirma; a los 300 s exactos vence y, confirmada la captura en
// el tick siguiente, el territorio es del jugador, su ciudad suma 5 y nadie
// recibe avisos: no había otro jugador.
func TestConquistaGoldenDeUnTerritorioLibre(t *testing.T) {
	h := newHarness(t)
	conCuatroTerritorios(h, map[int64]uuid.UUID{1: h.playerID})
	g := ponerUnidad(h, guerreroID, h.playerID, h.cityID, unit.TypeWarrior, world.Tile{X: 40, Y: 10}, 60)

	h.reclamar(guerreroID)
	require.Empty(t, h.codigos())
	_, confirmada, ok := h.state.ActiveClaim(2)
	require.True(t, ok)
	assert.False(t, confirmada, "hasta que la base la confirma no existe para nadie")
	assert.False(t, h.vistaDe(2).Contested)

	h.tick() // la confirmación vuelve por el buzón
	c, confirmada, _ := h.state.ActiveClaim(2)
	require.True(t, confirmada)
	assert.Equal(t, unit.StatusClaiming, g.Status)
	assert.Equal(t, int64(300_000), c.CompletesAtMs-c.StartedAtMs, "Q3: 5 minutos uno libre")
	v := h.vistaDe(2)
	assert.True(t, v.Contested)
	require.NotNil(t, v.Claim)
	assert.Equal(t, h.playerID.String(), v.Claim.PlayerID)
	assert.Equal(t, c.CompletesAtMs, v.Claim.EndsAtMs)
	require.Len(t, h.conquest.starts, 1)
	assert.Nil(t, h.conquest.starts[0].Claim.OwnerPlayerID)
	disputa := false
	for _, m := range h.rec.byType(protocol.TypeTerritoryUpdate) {
		if p := m.Payload.(protocol.TerritoryUpdatePayload); p.Territory.ID == 2 && p.Territory.Contested {
			disputa = true
		}
	}
	assert.True(t, disputa, "el área recibe el territorio en disputa")

	var capturado int64
	require.True(t, h.tickHasta(3100, func() bool {
		if dueñoDe(h.vistaDe(2)) == h.playerID.String() {
			capturado = h.clk.NowMs()
			return true
		}
		return false
	}))
	assert.Equal(t, c.CompletesAtMs+100, capturado, "vence en su tick y se aplica al confirmarse, en el siguiente")
	assert.Equal(t, int32(25), h.ciudad().PopulationLimit, "Q1: +5 por territorio conquistado")
	assert.Equal(t, unit.StatusIdle, g.Status)
	assert.False(t, h.vistaDe(2).Contested)
	_, _, sigue := h.state.ActiveClaim(2)
	assert.False(t, sigue)
	require.Len(t, h.conquest.captures, 1)
	assert.Equal(t, uuid.Nil, h.conquest.captures[0].PrevOwner)
	assert.Equal(t, int32(5), h.conquest.captures[0].NewBonus)
	assert.Empty(t, avisosDe(h), "uno libre no avisa a nadie (RN-CONQ-010)")
}

// RN-CONQ-007 y RN-CONQ-010: un territorio ajeno se conquista a los 600 s; la
// ciudad del conquistador suma 5 y la del anterior dueño resta 5, aunque quede
// por encima de su límite (ADR-029). El dueño recibe TERRITORY_CLAIMED al
// empezar y TERRITORY_LOST al perderlo; el conquistador, TERRITORY_TAKEN.
func TestConquistaDeUnTerritorioAjenoMueveLosDosLimitesYAvisa(t *testing.T) {
	h := newHarness(t)
	enemigo, ciudadE := conEnemigo(h)
	ciudadE.PopulationLimit, ciudadE.Population = 25, 23
	conCuatroTerritorios(h, map[int64]uuid.UUID{1: h.playerID, 3: enemigo, 4: enemigo})
	ponerUnidad(h, guerreroID, h.playerID, h.cityID, unit.TypeWarrior, world.Tile{X: 40, Y: 40}, 60)

	h.reclamar(guerreroID)
	h.tick()
	c, confirmada, _ := h.state.ActiveClaim(4)
	require.True(t, confirmada)
	assert.Equal(t, int64(600_000), c.CompletesAtMs-c.StartedAtMs, "Q3: 10 minutos uno con dueño")
	reclamado := avisosDeTipo(h, eventlog.KindTerritoryClaimed)
	require.Len(t, reclamado, 1)
	assert.Equal(t, enemigo, reclamado[0].to)
	assert.Equal(t, h.playerID.String(), reclamado[0].p.OtherPlayerID)
	require.NotNil(t, reclamado[0].p.TerritoryID)
	assert.Equal(t, int64(4), *reclamado[0].p.TerritoryID)
	assert.Equal(t, "Sureste", reclamado[0].p.TerritoryName)
	assert.Equal(t, world.Tile{X: 40, Y: 40}, world.Tile{X: *reclamado[0].p.X, Y: *reclamado[0].p.Y})

	require.True(t, h.tickHasta(6100, func() bool { return dueñoDe(h.vistaDe(4)) == h.playerID.String() }))
	assert.Equal(t, int32(25), h.ciudad().PopulationLimit)
	assert.Equal(t, int32(20), ciudadE.PopulationLimit, "pierde el bono y queda por encima de su límite")
	require.Len(t, h.conquest.captures, 1)
	cap := h.conquest.captures[0]
	assert.Equal(t, enemigo, cap.PrevOwner)
	assert.Equal(t, int64(2), cap.PrevCityID)
	assert.Equal(t, int32(5), cap.PrevBonus)
	assert.Equal(t, int32(1), cap.ExpectedVersion)

	perdido := avisosDeTipo(h, eventlog.KindTerritoryLost)
	require.Len(t, perdido, 1)
	assert.Equal(t, enemigo, perdido[0].to)
	ganado := avisosDeTipo(h, eventlog.KindTerritoryTaken)
	require.Len(t, ganado, 1)
	assert.Equal(t, h.playerID, ganado[0].to)
	assert.Equal(t, enemigo.String(), ganado[0].p.OtherPlayerID)
}

// §5 y RN-CONQ-002, RN-CONQ-003, INV-TERR-010, INV-TERR-011: cada rechazo de
// unit.claim con su código, sin reclamación ni cambio en el guerrero.
func TestRechazosDeLaReclamacion(t *testing.T) {
	casos := []struct {
		nombre   string
		preparar func(h *harness, enemigo uuid.UUID, ciudadE *city.City) int64
		codigo   string
	}{
		{"unidad ajena", func(h *harness, enemigo uuid.UUID, _ *city.City) int64 {
			ponerUnidad(h, enemigoID, enemigo, 2, unit.TypeWarrior, world.Tile{X: 40, Y: 10}, 60)
			return enemigoID
		}, protocol.CodeUnitNotOwned},
		{"el aldeano no reclama", func(h *harness, _ uuid.UUID, _ *city.City) int64 {
			ponerUnidad(h, 300, h.playerID, h.cityID, unit.TypeVillager, world.Tile{X: 40, Y: 10}, 40)
			return 300
		}, protocol.CodeUnitNotMovable},
		{"territorio propio", func(h *harness, _ uuid.UUID, _ *city.City) int64 {
			ponerUnidad(h, guerreroID, h.playerID, h.cityID, unit.TypeWarrior, world.Tile{X: 20, Y: 20}, 60)
			return guerreroID
		}, protocol.CodeInvalidTarget},
		{"la capital ajena (INV-TERR-011)", func(h *harness, _ uuid.UUID, _ *city.City) int64 {
			ponerUnidad(h, guerreroID, h.playerID, h.cityID, unit.TypeWarrior, world.Tile{X: 25, Y: 50}, 60)
			return guerreroID
		}, protocol.CodeInvalidTarget},
		{"un pacto (RN-CONQ-003)", func(h *harness, enemigo uuid.UUID, _ *city.City) int64 {
			h.state.SetPeaceTreaties([]diplomacy.Treaty{{ID: 1, PlayerA: h.playerID, PlayerB: enemigo,
				Type: diplomacy.TypeNonAggression, Status: diplomacy.StatusActive}})
			ponerUnidad(h, guerreroID, h.playerID, h.cityID, unit.TypeWarrior, world.Tile{X: 40, Y: 40}, 60)
			return guerreroID
		}, protocol.CodeInvalidTarget},
		{"dueño protegido (INV-CITY-006)", func(h *harness, _ uuid.UUID, ciudadE *city.City) int64 {
			ciudadE.PresenceState = city.PresenceProtected
			ponerUnidad(h, guerreroID, h.playerID, h.cityID, unit.TypeWarrior, world.Tile{X: 40, Y: 40}, 60)
			return guerreroID
		}, protocol.CodeCityProtected},
		{"ya en disputa (INV-TERR-010)", func(h *harness, _ uuid.UUID, _ *city.City) int64 {
			ponerUnidad(h, 301, h.playerID, h.cityID, unit.TypeWarrior, world.Tile{X: 50, Y: 10}, 60)
			h.reclamar(301)
			h.tick()
			h.rec.reset()
			ponerUnidad(h, guerreroID, h.playerID, h.cityID, unit.TypeWarrior, world.Tile{X: 40, Y: 10}, 60)
			return guerreroID
		}, protocol.CodeInvalidTarget},
	}
	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			h := newHarness(t)
			enemigo, ciudadE := conEnemigo(h)
			conCuatroTerritorios(h, map[int64]uuid.UUID{1: h.playerID, 3: enemigo, 4: enemigo})
			id := c.preparar(h, enemigo, ciudadE)
			antes := len(h.conquest.starts)

			h.reclamar(id)
			assert.Equal(t, []string{c.codigo}, h.codigos())
			u, _ := h.state.Unit(id)
			assert.NotEqual(t, unit.StatusClaiming, u.Status)
			h.tick()
			assert.Len(t, h.conquest.starts, antes, "nada escrito")
		})
	}
}

// RN-CONQ-005 y Q6: lo que rompe una reclamación, en la fase 5 del tick en que
// pasa; el guerrero vuelve a IDLE (si vive), el territorio deja de estar en
// disputa y la base recibe el fin con su motivo.
func TestQueRompeUnaReclamacion(t *testing.T) {
	type escenario struct {
		nombre string
		romper func(h *harness, enemigo uuid.UUID, ciudadE *city.City)
		motivo string
	}
	for _, c := range []escenario{
		{"otra orden", func(h *harness, _ uuid.UUID, _ *city.City) {
			h.send(simulation.MoveUnit{PlayerID: h.playerID, RequestID: uuid.NewString(), UnitID: guerreroID,
				Target: world.Tile{X: 45, Y: 45}})
		}, simulation.ClaimEndUnit},
		{"cancelar", func(h *harness, _ uuid.UUID, _ *city.City) {
			h.send(simulation.CancelMovement{PlayerID: h.playerID, RequestID: uuid.NewString(), UnitID: guerreroID})
		}, simulation.ClaimEndUnit},
		{"un pacto firmado", func(h *harness, enemigo uuid.UUID, _ *city.City) {
			h.state.SetPeaceTreaties([]diplomacy.Treaty{{ID: 1, PlayerA: h.playerID, PlayerB: enemigo,
				Type: diplomacy.TypeAlliance, Status: diplomacy.StatusActive}})
		}, simulation.ClaimEndBlocked},
		{"el dueño queda protegido", func(_ *harness, _ uuid.UUID, ciudadE *city.City) {
			ciudadE.PresenceState = city.PresenceProtected
		}, simulation.ClaimEndBlocked},
	} {
		t.Run(c.nombre, func(t *testing.T) {
			h := newHarness(t)
			enemigo, ciudadE := conEnemigo(h)
			conCuatroTerritorios(h, map[int64]uuid.UUID{1: h.playerID, 3: enemigo, 4: enemigo})
			g := ponerUnidad(h, guerreroID, h.playerID, h.cityID, unit.TypeWarrior, world.Tile{X: 40, Y: 40}, 60)
			h.reclamar(guerreroID)
			h.tick()
			_, confirmada, _ := h.state.ActiveClaim(4)
			require.True(t, confirmada)

			c.romper(h, enemigo, ciudadE)
			h.tick()
			_, _, sigue := h.state.ActiveClaim(4)
			assert.False(t, sigue)
			assert.NotEqual(t, unit.StatusClaiming, g.Status)
			assert.False(t, h.vistaDe(4).Contested)
			assert.Equal(t, []string{c.motivo}, h.conquest.endReasons())
			assert.Equal(t, enemigo.String(), dueñoDe(h.vistaDe(4)), "sin captura")
		})
	}
}

// Q6 y RN-CONQ-006: que le peguen no rompe la reclamación —el guerrero no se
// defiende—; matarlo sí. Y los guardias del dueño, parados cerca, responden
// solos al empezar.
func TestHayQueMatarAlQueReclama(t *testing.T) {
	h := newHarness(t)
	enemigo, _ := conEnemigo(h)
	conCuatroTerritorios(h, map[int64]uuid.UUID{1: h.playerID, 3: enemigo, 4: enemigo})
	g := ponerUnidad(h, guerreroID, h.playerID, h.cityID, unit.TypeWarrior, world.Tile{X: 40, Y: 40}, 60)
	guardia := ponerUnidad(h, enemigoID, enemigo, 2, unit.TypeWarrior, world.Tile{X: 42, Y: 40}, 60)

	h.reclamar(guerreroID)
	h.tick()
	objetivo, responde := h.state.Engaged(guardia.ID)
	require.True(t, responde, "el guardia del dueño responde al empezar la reclamación")
	assert.Equal(t, guerreroID, objetivo)

	require.True(t, h.tickHasta(100, func() bool { return g.HP < 60 }))
	_, _, sigue := h.state.ActiveClaim(4)
	assert.True(t, sigue, "un golpe no la rompe")
	assert.Equal(t, unit.StatusClaiming, g.Status, "y el guerrero no se defiende")
	_, contraataca := h.state.Engaged(guerreroID)
	assert.False(t, contraataca)

	require.True(t, h.tickHasta(300, func() bool { _, vive := h.state.Unit(guerreroID); return !vive }))
	h.tick()
	_, _, sigue = h.state.ActiveClaim(4)
	assert.False(t, sigue, "muerto, se rompe")
	assert.Equal(t, []string{simulation.ClaimEndUnit}, h.conquest.endReasons())
}

// ADR-034 §5: una reclamación activa vuelve con su guerrero al arrancar y, si
// venció con el servidor parado, se captura en el primer tick; la de un
// guerrero que ya no está en su tile termina.
func TestLasReclamacionesSobrevivenAUnReinicio(t *testing.T) {
	h := newHarness(t)
	enemigo, _ := conEnemigo(h)
	conCuatroTerritorios(h, map[int64]uuid.UUID{1: h.playerID, 3: enemigo})
	g := ponerUnidad(h, guerreroID, h.playerID, h.cityID, unit.TypeWarrior, world.Tile{X: 40, Y: 10}, 60)
	ponerUnidad(h, 301, h.playerID, h.cityID, unit.TypeWarrior, world.Tile{X: 50, Y: 50}, 60)
	now := h.clk.NowMs()
	h.state.SetClaims([]territory.Claim{
		{ID: 77, TerritoryID: 2, UnitID: guerreroID, PlayerID: h.playerID, CityID: h.cityID, X: 40, Y: 10,
			StartedAtMs: now - 400_000, CompletesAtMs: now - 100_000, RequestID: "r1"},
		{ID: 78, TerritoryID: 4, UnitID: 301, PlayerID: h.playerID, CityID: h.cityID, X: 40, Y: 40,
			StartedAtMs: now - 10_000, CompletesAtMs: now + 290_000, RequestID: "r2"},
	})
	assert.Equal(t, unit.StatusClaiming, g.Status, "vuelve a sostener el estandarte")

	h.tick() // el barrido: la 78 termina (su guerrero no está en su tile) y la 77 se captura
	assert.Equal(t, []string{simulation.ClaimEndUnit}, h.conquest.endReasons())
	h.tick()
	assert.Equal(t, h.playerID.String(), dueñoDe(h.vistaDe(2)))
	assert.Equal(t, unit.StatusIdle, g.Status)
}

// ADR-034 §3: una captura que la base rechaza termina la reclamación sin cambiar
// el dueño; una que no se pudo escribir sigue en vuelo y el volcado la reencola.
func TestLaCapturaQueLaBaseNoEscribe(t *testing.T) {
	t.Run("rechazada", func(t *testing.T) {
		h := newHarness(t)
		conCuatroTerritorios(h, nil)
		ponerUnidad(h, guerreroID, h.playerID, h.cityID, unit.TypeWarrior, world.Tile{X: 40, Y: 10}, 60)
		h.conquest.captureErr = simulation.ErrClaimConflict
		h.reclamar(guerreroID)
		h.advance(301 * time.Second)
		_, _, sigue := h.state.ActiveClaim(2)
		assert.False(t, sigue)
		assert.Equal(t, []string{simulation.ClaimEndConflict}, h.conquest.endReasons())
		assert.Empty(t, dueñoDe(h.vistaDe(2)))
		assert.Equal(t, int32(20), h.ciudad().PopulationLimit)
	})
	t.Run("sin base", func(t *testing.T) {
		h := newHarness(t)
		conCuatroTerritorios(h, nil)
		ponerUnidad(h, guerreroID, h.playerID, h.cityID, unit.TypeWarrior, world.Tile{X: 40, Y: 10}, 60)
		h.conquest.captureErr = errors.New("la base no responde")
		h.persister.permanent = true
		h.reclamar(guerreroID)
		h.advance(301 * time.Second)
		_, _, sigue := h.state.ActiveClaim(2)
		require.True(t, sigue, "sigue activa y en captura")
		assert.Empty(t, dueñoDe(h.vistaDe(2)))

		h.conquest.captureErr = nil
		h.advance(6 * time.Second) // un volcado la reencola
		assert.Equal(t, h.playerID.String(), dueñoDe(h.vistaDe(2)))
		assert.Empty(t, h.conquest.endReasons())
	})
}

// ADR-034 §5: CLAIMING no se escribe en la fila de la unidad; el volcado la
// escribe parada.
func TestElVolcadoEscribeClaimingComoIdle(t *testing.T) {
	h := newHarness(t)
	conCuatroTerritorios(h, nil)
	g := ponerUnidad(h, guerreroID, h.playerID, h.cityID, unit.TypeWarrior, world.Tile{X: 40, Y: 10}, 60)
	h.reclamar(guerreroID)
	h.tick()
	require.Equal(t, unit.StatusClaiming, g.Status)

	h.sim.FlushDirty()
	volcada, ok := h.repos.flushed[guerreroID]
	require.True(t, ok)
	assert.Equal(t, unit.StatusIdle, volcada.Status)
	assert.Equal(t, unit.StatusClaiming, g.Status, "en la RAM sigue reclamando")
}
