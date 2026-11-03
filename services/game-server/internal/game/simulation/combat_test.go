package simulation_test

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/diplomacy"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// Tests de simulación del combate (SPEC-COMBAT-RAID §9, ADR-024).
//
// Mundo: el de newHarness —64 × 64 de hierba, la ciudad 1 del jugador en
// (10,10)— más un vecino con su ciudad 2 en (20,40). Las unidades del combate
// se colocan a mano en la fila y = 20, lejos de las dos ciudades.

type fakeCombat struct {
	mu      sync.Mutex
	deaths  []simulation.Death
	attacks []simulation.Attack
	// pillages son los golpes de saqueo escritos; pillageErr, el error que
	// devuelve PersistPillage si no es nil.
	pillages   []simulation.Pillage
	pillageErr error
}

func (f *fakeCombat) PersistAttack(_ context.Context, a simulation.Attack) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.attacks = append(f.attacks, a)
	return nil
}

func (f *fakeCombat) PersistDeath(_ context.Context, d simulation.Death) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deaths = append(f.deaths, d)
	return nil
}

const (
	guerreroID = int64(100)
	enemigoID  = int64(200)
)

// conEnemigo añade un vecino con población 3 y devuelve su id y su ciudad.
func conEnemigo(h *harness) (uuid.UUID, *city.City) {
	h.t.Helper()
	p := conVecinos(h, 1)[0]
	c, ok := h.state.City(2)
	require.True(h.t, ok)
	c.Population = 3
	return p, c
}

func ponerUnidad(h *harness, id int64, owner uuid.UUID, cityID int64, tipo unit.Type, at world.Tile, hp int32) *unit.Unit {
	h.t.Helper()
	def, err := unit.Lookup(tipo)
	require.NoError(h.t, err)
	cid := cityID
	u := &unit.Unit{ID: id, PlayerID: owner, CityID: &cid, Type: tipo, X: at.X, Y: at.Y,
		HP: hp, MaxHP: def.MaxHP, Status: unit.StatusIdle}
	h.state.AddUnit(u)
	return u
}

func (h *harness) atacar(atacante, objetivo int64) {
	h.t.Helper()
	h.send(simulation.AttackUnit{PlayerID: h.playerID, RequestID: uuid.NewString(), UnitID: atacante, TargetID: objetivo})
}

// RN-COMBAT-007, golden «incursión»: el guerrero persigue al aldeano, se
// planta al alcanzarlo y golpea cada 1 500 ms exactos, anclados al primer
// contacto. Cinco golpes de 8 matan a un aldeano de 40: la víctima muere,
// desaparece para todos, su ciudad pierde un punto de población, la muerte se
// escribe y el guerrero se queda IDLE.
func TestElGuerreroPersigueYMataAlAldeano(t *testing.T) {
	h := newHarness(t)
	enemigo, ciudad := conEnemigo(h)
	g := ponerUnidad(h, guerreroID, h.playerID, h.cityID, unit.TypeWarrior, world.Tile{X: 15, Y: 20}, 60)
	v := ponerUnidad(h, enemigoID, enemigo, 2, unit.TypeVillager, world.Tile{X: 20, Y: 20}, 40)

	h.atacar(guerreroID, enemigoID)
	require.Empty(t, h.codigos())
	_, persigue := h.state.Movement(guerreroID)
	require.True(t, persigue, "lejos del objetivo, el guerrero se pone en marcha")
	objetivo, ok := h.state.Engaged(guerreroID)
	require.True(t, ok)
	require.Equal(t, enemigoID, objetivo)

	var golpes []int64
	var vidas []int32
	hp := v.HP
	for i := 0; i < 200; i++ {
		h.tick()
		if v.HP != hp {
			hp = v.HP
			golpes = append(golpes, h.clk.NowMs())
			vidas = append(vidas, hp)
		}
		if _, vive := h.state.Unit(enemigoID); !vive {
			break
		}
	}
	require.Equal(t, []int32{32, 24, 16, 8, 0}, vidas)
	for i := 1; i < len(golpes); i++ {
		assert.Equal(t, int64(1_500), golpes[i]-golpes[i-1], "golpe %d", i)
	}

	_, vive := h.state.Unit(enemigoID)
	assert.False(t, vive, "la víctima sale del mundo")
	assert.Equal(t, int32(2), ciudad.Population, "INV-CITY-017: su ciudad pierde la población")
	var muertos []int64
	for _, m := range h.rec.byType(protocol.TypeEntityDespawn) {
		if p := m.Payload.(protocol.EntityDespawnPayload); p.Reason == protocol.DespawnDead {
			muertos = append(muertos, p.ID)
		}
	}
	assert.Equal(t, []int64{enemigoID}, muertos)
	require.Len(t, h.combat.deaths, 1)
	assert.Equal(t, enemigoID, h.combat.deaths[0].VictimID)
	assert.Equal(t, guerreroID, h.combat.deaths[0].KillerID)
	assert.Equal(t, unit.StatusIdle, g.Status)
	_, sigue := h.state.Engaged(guerreroID)
	assert.False(t, sigue)
}

// RN-COMBAT-007: los golpes de un tick se aplican a la vez. Dos guerreros
// iguales que se atacan en el mismo tick mueren en el mismo tick: no gana el
// de id menor.
func TestDosGuerrerosIgualesMuerenEnElMismoTick(t *testing.T) {
	h := newHarness(t)
	enemigo, _ := conEnemigo(h)
	ponerUnidad(h, guerreroID, h.playerID, h.cityID, unit.TypeWarrior, world.Tile{X: 15, Y: 20}, 60)
	ponerUnidad(h, enemigoID, enemigo, 2, unit.TypeWarrior, world.Tile{X: 16, Y: 20}, 60)

	h.commands <- simulation.AttackUnit{PlayerID: h.playerID, RequestID: uuid.NewString(), UnitID: guerreroID, TargetID: enemigoID}
	h.commands <- simulation.AttackUnit{PlayerID: enemigo, RequestID: uuid.NewString(), UnitID: enemigoID, TargetID: guerreroID}
	h.tick()

	muerte := map[int64]int64{}
	for i := 0; i < 300 && len(muerte) < 2; i++ {
		h.tick()
		for _, id := range []int64{guerreroID, enemigoID} {
			if _, vive := h.state.Unit(id); !vive {
				if _, ya := muerte[id]; !ya {
					muerte[id] = h.clk.NowMs()
				}
			}
		}
	}
	require.Len(t, muerte, 2, "mueren los dos")
	assert.Equal(t, muerte[guerreroID], muerte[enemigoID], "en el mismo tick")
	assert.Len(t, h.combat.deaths, 2)
}

// §5: cada rechazo de unit.attack con su código, sin efecto sobre el mundo.
func TestRechazosDelAtaque(t *testing.T) {
	casos := []struct {
		nombre   string
		preparar func(h *harness, enemigo uuid.UUID, ciudad *city.City) (atacante, objetivo int64)
		codigo   string
	}{
		{"atacante inexistente", func(h *harness, _ uuid.UUID, _ *city.City) (int64, int64) {
			return 999, enemigoID
		}, protocol.CodeUnitNotFound},
		{"atacante ajeno", func(h *harness, _ uuid.UUID, _ *city.City) (int64, int64) {
			return enemigoID, guerreroID
		}, protocol.CodeUnitNotOwned},
		{"el aldeano no ataca", func(h *harness, _ uuid.UUID, _ *city.City) (int64, int64) {
			return 1, enemigoID
		}, protocol.CodeUnitNotMovable},
		{"objetivo propio", func(h *harness, _ uuid.UUID, _ *city.City) (int64, int64) {
			return guerreroID, 1
		}, protocol.CodeInvalidTarget},
		{"objetivo oculto: como si no existiera", func(h *harness, _ uuid.UUID, _ *city.City) (int64, int64) {
			v, _ := h.state.Unit(enemigoID)
			v.Status = unit.StatusHidden
			return guerreroID, enemigoID
		}, protocol.CodeUnitNotFound},
		{"dueño protegido", func(h *harness, _ uuid.UUID, c *city.City) (int64, int64) {
			c.PresenceState = city.PresenceProtected
			return guerreroID, enemigoID
		}, protocol.CodeCityProtected},
		{"tratado de no agresión", func(h *harness, enemigo uuid.UUID, _ *city.City) (int64, int64) {
			h.state.SetPeaceTreaties([]diplomacy.Treaty{{ID: 1, PlayerA: h.playerID, PlayerB: enemigo,
				Type: diplomacy.TypeNonAggression, Status: diplomacy.StatusActive}})
			return guerreroID, enemigoID
		}, protocol.CodeInvalidTarget},
	}
	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			h := newHarness(t)
			enemigo, ciudad := conEnemigo(h)
			ponerUnidad(h, guerreroID, h.playerID, h.cityID, unit.TypeWarrior, world.Tile{X: 15, Y: 20}, 60)
			ponerUnidad(h, enemigoID, enemigo, 2, unit.TypeVillager, world.Tile{X: 20, Y: 20}, 40)
			a, o := c.preparar(h, enemigo, ciudad)

			h.atacar(a, o)
			assert.Equal(t, []string{c.codigo}, h.codigos())
			_, engaged := h.state.Engaged(a)
			assert.False(t, engaged)
			_, moving := h.state.Movement(a)
			assert.False(t, moving)
		})
	}
}

// RN-PRESENCE-036: una protección vencida ya no frena el ataque, aunque la
// ciudad siga PROTECTED; y un tratado caducado tampoco.
func TestLaProteccionVencidaYElTratadoCaducadoNoFrenan(t *testing.T) {
	h := newHarness(t)
	enemigo, ciudad := conEnemigo(h)
	ponerUnidad(h, guerreroID, h.playerID, h.cityID, unit.TypeWarrior, world.Tile{X: 15, Y: 20}, 60)
	ponerUnidad(h, enemigoID, enemigo, 2, unit.TypeVillager, world.Tile{X: 16, Y: 20}, 40)
	vencida := h.clk.Now().Add(-time.Second)
	ciudad.PresenceState = city.PresenceProtected
	ciudad.ProtectionUntil = &vencida
	h.state.SetPeaceTreaties([]diplomacy.Treaty{{ID: 1, PlayerA: h.playerID, PlayerB: enemigo,
		Type: diplomacy.TypeAlliance, Status: diplomacy.StatusActive, ExpiresAt: &vencida}})

	h.atacar(guerreroID, enemigoID)
	assert.Empty(t, h.codigos())
	g, _ := h.state.Unit(guerreroID)
	assert.Equal(t, unit.StatusAttacking, g.Status, "adyacente: ataca sin moverse")
}

// RN-COMBAT-008 e INV-COMBAT-002: si el dueño del objetivo pasa a protegido a
// mitad de la persecución, el siguiente tick termina el enfrentamiento y el
// guerrero se para. Ningún golpe cae.
func TestLaProteccionAMitadDePersecucionTerminaElEnfrentamiento(t *testing.T) {
	h := newHarness(t)
	enemigo, ciudad := conEnemigo(h)
	g := ponerUnidad(h, guerreroID, h.playerID, h.cityID, unit.TypeWarrior, world.Tile{X: 10, Y: 20}, 60)
	v := ponerUnidad(h, enemigoID, enemigo, 2, unit.TypeVillager, world.Tile{X: 20, Y: 20}, 40)
	h.atacar(guerreroID, enemigoID)
	h.advance(time.Second)

	ciudad.PresenceState = city.PresenceProtected
	h.tick()
	_, engaged := h.state.Engaged(guerreroID)
	assert.False(t, engaged)
	_, moving := h.state.Movement(guerreroID)
	assert.False(t, moving, "se detiene")
	assert.Equal(t, unit.StatusIdle, g.Status)
	h.advance(10 * time.Second)
	assert.Equal(t, int32(40), v.HP)
}

// RN-COMBAT-010 e INV-COMBAT-003: el botín es lo que llevaba la víctima hasta
// el hueco del asesino; el resto se pierde. Al llegar al depósito de su ciudad,
// el guerrero entrega exactamente lo que lleva y se queda vacío.
func TestElBotinPasaAlAsesinoYSeDeposita(t *testing.T) {
	h := newHarness(t)
	enemigo, _ := conEnemigo(h)
	g := ponerUnidad(h, guerreroID, h.playerID, h.cityID, unit.TypeWarrior, world.Tile{X: 15, Y: 20}, 60)
	// Carga máxima 20 (SPEC-COMBAT-PILLAGE Q2): con 15, le quedan cinco de hueco.
	g.Loot = 15
	v := ponerUnidad(h, enemigoID, enemigo, 2, unit.TypeWarrior, world.Tile{X: 16, Y: 20}, 8)
	v.Loot = 10
	madera := h.madera()

	h.atacar(guerreroID, enemigoID)
	h.tick()
	_, vive := h.state.Unit(enemigoID)
	require.False(t, vive, "un golpe de 8 mata a quien tiene 8")
	assert.Equal(t, int32(20), g.Loot, "cinco de hueco: gana cinco")
	require.Len(t, h.combat.deaths, 1)
	assert.Equal(t, int32(5), h.combat.deaths[0].Loot)
	assert.Equal(t, int32(5), h.combat.deaths[0].Lost)

	h.send(simulation.MoveUnit{PlayerID: h.playerID, RequestID: uuid.NewString(), UnitID: guerreroID,
		Target: world.Tile{X: 12, Y: 10}})
	h.advance(15 * time.Second)
	assert.Equal(t, int32(0), g.Loot)
	assert.Equal(t, madera+20, h.madera(), "deposita exactamente lo que llevaba")
}

// RN-COMBAT-005 e INV-COMBAT-002: no se ataca a una unidad que está en una
// Safe Zone, aunque se la vea moverse por ella.
func TestNingunGolpeCaeSobreUnaUnidadEnSafeZone(t *testing.T) {
	h := newHarnessConZona(t)
	ponerUnidad(h, guerreroID, h.playerID, h.cityID, unit.TypeWarrior, world.Tile{X: 18, Y: 30}, 60)
	v := ponerUnidad(h, enemigoID, otroJugador, h.cityID, unit.TypeVillager, world.Tile{X: franjaMinX, Y: 30}, 40)
	v.Status = unit.StatusMoving // visible: no está en reposo

	h.atacar(guerreroID, enemigoID)
	assert.Equal(t, []string{protocol.CodeInvalidTarget}, h.codigos())
	assert.Equal(t, int32(40), v.HP)
}

// ADR-024 §2: ATTACKING no se persiste; el volcado escribe la unidad parada,
// y con su vida y su botín.
func TestElVolcadoEscribeAttackingComoIdle(t *testing.T) {
	h := newHarness(t)
	enemigo, _ := conEnemigo(h)
	ponerUnidad(h, guerreroID, h.playerID, h.cityID, unit.TypeWarrior, world.Tile{X: 15, Y: 20}, 60)
	ponerUnidad(h, enemigoID, enemigo, 2, unit.TypeWarrior, world.Tile{X: 16, Y: 20}, 60)
	h.atacar(guerreroID, enemigoID)
	g, _ := h.state.Unit(guerreroID)
	require.Equal(t, unit.StatusAttacking, g.Status)

	h.sim.FlushDirty()
	volcada, ok := h.repos.flushed[guerreroID]
	require.True(t, ok)
	assert.Equal(t, unit.StatusIdle, volcada.Status)
	herida, ok := h.repos.flushed[enemigoID]
	require.True(t, ok, "la herida también se vuelca")
	assert.Equal(t, int32(52), herida.HP)
}

// ADR-024 §2, recuperación: lo que vuelve de la base tras un reinicio a mitad
// de una pelea es la vida y el botín del último volcado, y ningún
// enfrentamiento: los dos bandos quedan parados.
func TestTrasUnReinicioVuelvenLaVidaYElBotinPeroNoElEnfrentamiento(t *testing.T) {
	terrain := make([]byte, 64*64)
	for i := range terrain {
		terrain[i] = byte(world.Grassland)
	}
	w, err := world.New(64, 64, 32, 1, terrain)
	require.NoError(t, err)
	herido := &unit.Unit{ID: guerreroID, PlayerID: uuid.New(), Type: unit.TypeWarrior,
		X: 15, Y: 20, HP: 23, MaxHP: 60, Status: unit.StatusIdle, Loot: 7}

	state := simulation.NewState(w)
	simulation.Hydrate(state, []*unit.Unit{herido}, nil, nil, epoch, slog.New(slog.NewTextHandler(io.Discard, nil)))

	u, ok := state.Unit(guerreroID)
	require.True(t, ok)
	assert.Equal(t, int32(23), u.HP)
	assert.Equal(t, int32(7), u.Loot)
	_, engaged := state.Engaged(guerreroID)
	assert.False(t, engaged)

	sim := simulation.New(state, simulation.Deps{})
	assert.Equal(t, int32(7), sim.UnitView(u, epoch).Carrying, "el botín viaja como carga")
}

// PersistPillage anota el golpe; con pillageErr, falla como pida el test.
func (f *fakeCombat) PersistPillage(_ context.Context, p simulation.Pillage) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.pillageErr != nil {
		return f.pillageErr
	}
	f.pillages = append(f.pillages, p)
	return nil
}
