package simulation_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/cityfall"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/eventlog"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// Tests de simulación de la caída de una ciudad (SPEC-CITY-FALL §9, ADR-050).
//
// Mundo de newHarness (64 × 64): la ciudad propia en (10,10) y la enemiga de
// conEnemigo en (20,40), que aquí pasa a la Edad de Bronce para que se pueda
// asaltar (RN-SIEGE-010). El guerrero asaltante empieza en (22,40), en el
// anillo de la ciudad enemiga.

// fakeCityFall es el almacén de la caída: apunta lo escrito y arma la ciudad
// nueva con el sitio que le da la simulación.
type fakeCityFall struct {
	mu      sync.Mutex
	nextID  int64
	starts  []simulation.AssaultStart
	ends    []simulation.AssaultEnd
	falls   []cityfall.Fall
	centers func() []world.Tile
	fallErr error
	avoid   []world.Tile
	minDist int32
}

func (f *fakeCityFall) StartAssault(_ context.Context, a simulation.AssaultStart) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	f.starts = append(f.starts, a)
	return f.nextID, nil
}

func (f *fakeCityFall) EndAssault(_ context.Context, e simulation.AssaultEnd) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ends = append(f.ends, e)
	return nil
}

func (f *fakeCityFall) FallCity(_ context.Context, fl cityfall.Fall) (*cityfall.FallResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fallErr != nil {
		return nil, f.fallErr
	}
	f.falls = append(f.falls, fl)
	site, err := fl.Place(f.centers())
	if err != nil {
		return nil, err
	}
	now := fl.Now
	c := &city.City{
		ID: 99, OwnerPlayerID: fl.OwnerID, Name: fl.Name, CenterX: site.Center.X, CenterY: site.Center.Y,
		Era: fl.Era, PopulationLimit: 40, PresenceState: city.PresenceOnline, LastOnlineAt: &now,
		FoundedAtMs: now.UnixMilli(),
	}
	res := &cityfall.FallResult{City: c}
	for i, sp := range site.Spawns {
		id := c.ID
		res.Villagers = append(res.Villagers, &unit.Unit{
			ID: int64(900 + i), PlayerID: fl.OwnerID, CityID: &id, Type: unit.TypeVillager,
			X: sp.X, Y: sp.Y, HP: 40, MaxHP: 40, Status: unit.StatusIdle,
		})
	}
	return res, nil
}

func (f *fakeCityFall) endReasons() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, e := range f.ends {
		out = append(out, e.Reason)
	}
	return out
}

func (f *fakeCityFall) fallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.falls)
}

const sitiadorID = int64(300)

// escenarioDeCaida monta el harness con la caída activa, la ciudad enemiga en
// Bronce con un aldeano fuera y el guerrero asaltante en su anillo, y conecta
// al asaltante (INV-COMBAT-005).
func escenarioDeCaida(t *testing.T, ajustar func(*simulation.Deps)) (*harness, *fakeCityFall, uuid.UUID, *city.City) {
	t.Helper()
	store := &fakeCityFall{}
	h := newHarnessConDeps(t, nil, func(d *simulation.Deps) {
		d.CityFall = store
		// El sitio lo busca founding.FindRefoundSite (sus tests, en
		// internal/game/founding); aquí, uno fijo a 30 tiles de la caída, que
		// apunta con qué se le llamó.
		d.RefoundSite = func(_ context.Context, existing, avoid []world.Tile, minDist int32, _ uint64) (cityfall.Site, error) {
			// Se llama dentro de FallCity, con el cerrojo del almacén tomado.
			store.avoid, store.minDist = avoid, minDist
			return cityfall.Site{Center: world.Tile{X: 50, Y: 50},
				Spawns: []world.Tile{{X: 52, Y: 50}, {X: 48, Y: 50}, {X: 50, Y: 52}}}, nil
		}
		d.CityRefoundMinDistance = 24
		if ajustar != nil {
			ajustar(d)
		}
	})
	store.centers = func() []world.Tile { return []world.Tile{{X: 10, Y: 10}} }
	enemigo, ciudad := conEnemigo(h)
	ciudad.Era = city.EraBronze
	ciudad.Civilization, ciudad.Faction = "NORSE", "CHAOS"
	ponerUnidad(h, enemigoID, enemigo, ciudad.ID, unit.TypeVillager, world.Tile{X: 30, Y: 50}, 40)
	ponerUnidad(h, sitiadorID, h.playerID, h.cityID, unit.TypeWarrior, world.Tile{X: 22, Y: 40}, 60)
	h.conectar()
	return h, store, enemigo, ciudad
}

func (h *harness) asaltar(unitID, cityID int64) {
	h.t.Helper()
	h.send(simulation.AssaultCity{PlayerID: h.playerID, RequestID: uuid.NewString(), UnitID: unitID, CityID: cityID})
}

func (h *harness) estadoDe(id int64) unit.Status {
	h.t.Helper()
	u, ok := h.state.Unit(id)
	require.True(h.t, ok)
	return u.Status
}

// RN-SIEGE-009, RN-SIEGE-011, RN-CITY-026 a RN-CITY-028, golden «caída»: el
// guerrero planta el estandarte, el asalto existe para todos cuando vuelve su
// fila, el asaltado recibe el aviso, y a los 900 s exactos la ciudad cae: sale
// del mundo, su dueño tiene ya la nueva (con su nombre, su era y su aldeano de
// fuera), los dos reciben city.fallen y su aviso, y el guerrero queda IDLE.
func TestUnAsaltoQueAguantaElPlazoHaceCaerLaCiudad(t *testing.T) {
	h, store, enemigo, ciudad := escenarioDeCaida(t, nil)
	inicio := h.clk.NowMs()

	h.asaltar(sitiadorID, ciudad.ID)
	assert.Equal(t, unit.StatusClaiming, h.estadoDe(sitiadorID))
	require.Len(t, store.starts, 1)
	a := store.starts[0].Assault
	assert.Equal(t, enemigo, store.starts[0].Owner)
	assert.Equal(t, inicio+(15*time.Minute).Milliseconds(), a.EndsAtMs, "EO_CITY_ASSAULT_SECONDS por defecto")
	assert.Equal(t, world.Tile{X: 22, Y: 40}, a.Tile())

	h.tick() // vuelve la fila por el buzón
	asalto, confirmado, ok := h.state.ActiveAssault(ciudad.ID)
	require.True(t, ok)
	assert.True(t, confirmado)
	assert.Equal(t, int64(1), asalto.ID)
	avisos := avisosDeTipo(h, eventlog.KindCityAssaulted)
	require.Len(t, avisos, 1)
	assert.Equal(t, enemigo, avisos[0].to)
	require.NotNil(t, avisos[0].p.FallsAtMs)
	assert.Equal(t, a.EndsAtMs, *avisos[0].p.FallsAtMs)
	abierto := false
	for _, m := range h.rec.byType(protocol.TypeCityUpdate) {
		if p := m.Payload.(protocol.CityUpdatePayload); p.ID == ciudad.ID && p.Assault != nil && *p.Assault != nil {
			abierto = true
		}
	}
	assert.True(t, abierto, "city.update abre el asalto para todos los que ven la ciudad")

	// Un tick antes del plazo, nada.
	h.hasta(a.EndsAtMs - 100)
	assert.Zero(t, store.fallCount())
	h.hasta(a.EndsAtMs)
	require.Equal(t, 1, store.fallCount(), "la caída se encola en el tick en que vence el plazo")
	f := store.falls[0]
	assert.Equal(t, []world.Tile{{X: 20, Y: 40}, {X: 10, Y: 10}}, store.avoid, "RN-CITY-027: lejos de la caída y del vencedor")
	assert.Equal(t, int32(24), store.minDist)
	assert.Equal(t, ciudad.ID, f.CityID)
	assert.Equal(t, enemigo, f.OwnerID)
	assert.Equal(t, h.playerID, f.VictorID)
	assert.Nil(t, f.War, "sin guerra entre clanes no puntúa")
	h.tick() // vuelve la caída por el buzón

	_, sigue := h.state.City(ciudad.ID)
	assert.False(t, sigue, "la ciudad caída sale del mundo")
	nueva, ok := h.state.CityOf(enemigo)
	require.True(t, ok, "INV-CITY-020: el vencido tiene ya su ciudad nueva")
	assert.Equal(t, int64(99), nueva.ID)
	assert.Equal(t, "Vecina", nueva.Name)
	assert.Equal(t, city.EraBronze, nueva.Era)
	assert.Equal(t, ciudad.Civilization, nueva.Civilization, "la civilización y el bando son del jugador")
	assert.Equal(t, ciudad.Faction, nueva.Faction)
	assert.GreaterOrEqual(t, chebyshevDe(nueva.Center(), ciudad.Center()), int32(24), "lejos de la caída")
	aldeano, _ := h.state.Unit(enemigoID)
	require.NotNil(t, aldeano.CityID)
	assert.Equal(t, int64(99), *aldeano.CityID, "las unidades de fuera pasan a la ciudad nueva")
	for i := int64(900); i < 903; i++ {
		_, ok := h.state.Unit(i)
		assert.True(t, ok, "los tres aldeanos nuevos")
	}
	assert.Equal(t, unit.StatusIdle, h.estadoDe(sitiadorID))
	assert.True(t, h.state.World().IsWalkable(ciudad.CenterX, ciudad.CenterY), "su zona urbana deja de estar bloqueada")
	_, _, ok = h.state.ActiveAssault(ciudad.ID)
	assert.False(t, ok)

	caidas := h.rec.byType(protocol.TypeCityFallen)
	require.NotEmpty(t, caidas)
	p := caidas[0].Payload.(protocol.CityFallenPayload)
	assert.Equal(t, ciudad.ID, p.CityID)
	assert.Equal(t, h.playerID.String(), p.ByPlayerID)
	perdida := avisosDeTipo(h, eventlog.KindCityLost)
	require.Len(t, perdida, 1)
	assert.Equal(t, enemigo, perdida[0].to)
	assert.Equal(t, nueva.CenterX, *perdida[0].p.X, "al vencido, dónde refundó")
	tomada := avisosDeTipo(h, eventlog.KindCityTaken)
	require.Len(t, tomada, 1)
	assert.Equal(t, h.playerID, tomada[0].to)
}

func chebyshevDe(a, b world.Tile) int32 {
	dx, dy := a.X-b.X, a.Y-b.Y
	if dx < 0 {
		dx = -dx
	}
	if dy < 0 {
		dy = -dy
	}
	return max(dx, dy)
}

// RN-SIEGE-010, RN-CITY-029 y RN-SIEGE-009: cada rechazo de la orden.
func TestLosRechazosDelAsalto(t *testing.T) {
	casos := []struct {
		nombre string
		montar func(h *harness, c *city.City)
		unidad int64
		codigo string
	}{
		{"de la Edad de Piedra", func(_ *harness, c *city.City) { c.Era = city.EraStone }, sitiadorID, protocol.CodeInvalidTarget},
		{"amurallada", func(h *harness, c *city.City) { conMuralla(h, c, true, 0) }, sitiadorID, protocol.CodeInvalidTarget},
		{"protegida al ordenar", func(_ *harness, c *city.City) { c.PresenceState = city.PresenceProtected }, sitiadorID, protocol.CodeCityProtected},
		{"la Orden contra sí misma", func(h *harness, c *city.City) {
			c.Faction = "ORDER"
			propia, _ := h.state.City(h.cityID)
			propia.Faction = "ORDER"
		}, sitiadorID, protocol.CodeInvalidTarget},
		{"un aldeano no asalta", func(*harness, *city.City) {}, 1, protocol.CodeUnitNotMovable},
		{"un ariete no asalta", func(h *harness, _ *city.City) {
			ponerUnidad(h, 301, h.playerID, h.cityID, unit.TypeRam, world.Tile{X: 18, Y: 40}, 250)
		}, 301, protocol.CodeUnitNotMovable},
	}
	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			h, store, _, ciudad := escenarioDeCaida(t, nil)
			c.montar(h, ciudad)
			h.asaltar(c.unidad, ciudad.ID)
			assert.Equal(t, []string{c.codigo}, h.codigos())
			assert.Empty(t, store.starts)
		})
	}

	t.Run("recién fundada", func(t *testing.T) {
		h, store, _, ciudad := escenarioDeCaida(t, func(d *simulation.Deps) { d.CityFallImmunity = 72 * time.Hour })
		ciudad.FoundedAtMs = h.clk.NowMs() - (71 * time.Hour).Milliseconds()
		h.asaltar(sitiadorID, ciudad.ID)
		assert.Equal(t, []string{protocol.CodeInvalidTarget}, h.codigos())
		assert.Empty(t, store.starts)
		ciudad.FoundedAtMs = h.clk.NowMs() - (72 * time.Hour).Milliseconds()
		h.asaltar(sitiadorID, ciudad.ID)
		assert.Len(t, store.starts, 1, "a las 72 h exactas ya se puede")
	})

	t.Run("con otro asalto", func(t *testing.T) {
		h, store, _, ciudad := escenarioDeCaida(t, nil)
		h.asaltar(sitiadorID, ciudad.ID)
		h.tick()
		ponerUnidad(h, 302, h.playerID, h.cityID, unit.TypeWarrior, world.Tile{X: 18, Y: 40}, 60)
		h.asaltar(302, ciudad.ID)
		assert.Equal(t, []string{protocol.CodeInvalidTarget}, h.codigos())
		assert.Len(t, store.starts, 1, "INV-CITY-021: un asalto activo por ciudad")
	})
}

// RN-SIEGE-011 y RN-COMBAT-041: lo que rompe un asalto en vuelo, con su motivo,
// y lo que no lo rompe: la protección del asaltado (SPEC-CITY-FALL Q7).
func TestLoQueRompeUnAsalto(t *testing.T) {
	casos := []struct {
		nombre string
		romper func(h *harness, c *city.City)
		motivo string
	}{
		{"la unidad muere", func(h *harness, _ *city.City) {
			u, _ := h.state.Unit(sitiadorID)
			u.HP, u.Status = 0, unit.StatusDead
		}, cityfall.EndUnit},
		{"otra orden", func(h *harness, _ *city.City) {
			h.send(simulation.MoveUnit{PlayerID: h.playerID, RequestID: uuid.NewString(), UnitID: sitiadorID, Target: world.Tile{X: 26, Y: 40}})
		}, cityfall.EndUnit},
		{"la paz de la Orden", func(h *harness, c *city.City) {
			c.Faction = "ORDER"
			propia, _ := h.state.City(h.cityID)
			propia.Faction = "ORDER"
		}, cityfall.EndBlocked},
		{"la muralla se completa", func(h *harness, c *city.City) { conMuralla(h, c, true, 0) }, cityfall.EndWalled},
		{"el asaltante se protege", func(h *harness, _ *city.City) {
			propia, _ := h.state.City(h.cityID)
			propia.PresenceState = city.PresenceProtected
		}, cityfall.EndBlocked},
		{"el asaltante se ausenta", func(h *harness, _ *city.City) {
			h.send(simulation.PlayerDisconnected{PlayerID: h.playerID, SessionID: sesion})
			h.advance(31 * time.Second)
		}, cityfall.EndBlocked},
	}
	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			h, store, _, ciudad := escenarioDeCaida(t, nil)
			h.asaltar(sitiadorID, ciudad.ID)
			h.tick()
			c.romper(h, ciudad)
			h.advance(time.Second)
			assert.Equal(t, []string{c.motivo}, store.endReasons())
			_, _, ok := h.state.ActiveAssault(ciudad.ID)
			assert.False(t, ok)
			if u, ok := h.state.Unit(sitiadorID); ok && u.IsAlive() {
				assert.NotEqual(t, unit.StatusClaiming, u.Status)
			}
			h.advance(16 * time.Minute)
			assert.Zero(t, store.fallCount())
		})
	}

	t.Run("la protección del asaltado no lo rompe", func(t *testing.T) {
		h, store, _, ciudad := escenarioDeCaida(t, nil)
		h.asaltar(sitiadorID, ciudad.ID)
		h.tick()
		ciudad.PresenceState = city.PresenceProtected
		h.advance(15 * time.Minute)
		assert.Empty(t, store.endReasons())
		assert.Equal(t, 1, store.fallCount(), "cae aunque su dueño se haya desconectado")
	})
}

// SPEC-CITY-FALL §4.1: lejos del anillo, la orden lleva la unidad al tile más
// cercano y el asalto empieza al llegar.
func TestLaUnidadLejanaVaAlAnilloYAsaltaAlLlegar(t *testing.T) {
	h, store, _, ciudad := escenarioDeCaida(t, nil)
	u, _ := h.state.Unit(sitiadorID)
	u.X, u.Y = 30, 40
	h.asaltar(sitiadorID, ciudad.ID)
	assert.Equal(t, unit.StatusMoving, h.estadoDe(sitiadorID))
	assert.Empty(t, store.starts)
	h.advance(10 * time.Second)
	require.Len(t, store.starts, 1, "al llegar al anillo")
	// Los cinco tiles de la columna x = 22 empatan a 8: gana el de menor y.
	assert.Equal(t, world.Tile{X: 22, Y: 38}, store.starts[0].Assault.Tile())
	assert.Equal(t, unit.StatusClaiming, h.estadoDe(sitiadorID))
}

// ADR-050 §2: un asalto leído de la base al arrancar vuelve con su unidad y su
// plazo, y cae a su hora.
func TestUnAsaltoSobreviveAUnReinicio(t *testing.T) {
	h, store, enemigo, ciudad := escenarioDeCaida(t, nil)
	ahora := h.clk.NowMs()
	h.state.SetAssaults([]cityfall.Assault{{
		ID: 7, CityID: ciudad.ID, UnitID: sitiadorID, PlayerID: h.playerID, FromCityID: h.cityID, X: 22, Y: 40,
		StartedAtMs: ahora - 60_000, EndsAtMs: ahora + 60_000, RequestID: "r",
	}})
	assert.Equal(t, unit.StatusClaiming, h.estadoDe(sitiadorID))
	h.advance(61 * time.Second)
	require.Equal(t, 1, store.fallCount())
	assert.Equal(t, int64(7), store.falls[0].AssaultID)
	h.tick()
	_, ok := h.state.CityOf(enemigo)
	assert.True(t, ok)
	_, sigue := h.state.City(ciudad.ID)
	assert.False(t, sigue)
}

// RN-CITY-026: mientras la caída espera la base, la ciudad no admite encargos.
func TestUnaCiudadCayendoNoAdmiteEncargos(t *testing.T) {
	h, store, enemigo, ciudad := escenarioDeCaida(t, nil)
	store.fallErr = context.DeadlineExceeded
	h.asaltar(sitiadorID, ciudad.ID)
	h.tick()
	h.advance(15 * time.Minute)
	require.True(t, h.state.CityFalling(ciudad.ID))
	h.send(simulation.TrainUnit{PlayerID: enemigo, RequestID: uuid.NewString(), CityID: ciudad.ID, Type: unit.TypeVillager})
	assert.Contains(t, h.codigos(), protocol.CodeInvalidTarget)
}
