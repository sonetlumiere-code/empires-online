package simulation_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/building"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/diplomacy"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/production"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// Tests del asedio (SPEC-COMBAT-SIEGE §9). Mundo de newHarness: la ciudad
// propia en (10,10); la enemiga de conEnemigo en (20,40), con su muralla de
// (19,39) a (21,41). La casa de conAsedio está en (20,39), en el lado norte:
// se golpea desde (19,38), (20,38) o (21,38).

const casaID = int64(50)

var tileCasa = world.Tile{X: 20, Y: 39}

// conCasa añade a una ciudad una casa, completa (con su bono ya en el límite,
// como la deja la compleción) o en obra.
func conCasa(h *harness, c *city.City, id int64, at world.Tile, completa bool) {
	h.t.Helper()
	estado := building.StatusUnderConstruction
	termina := h.clk.NowMs() + 3_600_000
	if completa {
		estado = building.StatusComplete
		termina = h.clk.NowMs()
		c.PopulationLimit += 5
	}
	h.state.SetEconomy(simulation.EconomyLoad{Buildings: []building.Building{{
		ID: id, CityID: c.ID, Type: building.TypeHouse, X: at.X, Y: at.Y, Status: estado, CompletesAtMs: termina,
	}}})
}

func (h *harness) derribar(atacante, casa int64) {
	h.t.Helper()
	h.send(simulation.DemolishBuilding{PlayerID: h.playerID, RequestID: uuid.NewString(), UnitID: atacante, BuildingID: casa})
}

// conAsedio prepara al jugador conectado, la ciudad enemiga con una casa
// completa en (20,39) y un guerrero propio en `at`.
func conAsedio(h *harness, at world.Tile) (uuid.UUID, *city.City, *unit.Unit) {
	h.t.Helper()
	h.conectar()
	enemigo, c := conEnemigo(h)
	conCasa(h, c, casaID, tileCasa, true)
	g := ponerUnidad(h, guerreroID, h.playerID, h.cityID, unit.TypeWarrior, at, 60)
	return enemigo, c, g
}

func vidaDe(h *harness, id int64) (int32, bool) {
	hp, _, ok := h.state.BuildingHP(id)
	return hp, ok
}

// Golden «asedio» (RN-COMBAT-031 a RN-COMBAT-033, RN-COMBAT-036): el guerrero va
// al tile junto a la casa más cercano —(19,38): empate en distancia y en y,
// menor x—, la golpea con 8 cada 1 500 ms exactos, y en el golpe 19 la derriba.
// La casa desaparece para todos, el límite de su ciudad pierde los 5 de la casa,
// la destrucción se escribe con el atacante y su instante, y el guerrero queda
// IDLE junto a la muralla: no sigue con otra casa.
func TestElAsedioGoldenDerribaUnaCasa(t *testing.T) {
	h := newHarness(t)
	enemigo, ciudad, g := conAsedio(h, world.Tile{X: 20, Y: 30})
	conCasa(h, ciudad, casaID+1, world.Tile{X: 21, Y: 39}, true)
	require.EqualValues(t, 30, ciudad.PopulationLimit)

	h.derribar(guerreroID, casaID)
	require.Empty(t, h.codigos())
	m, va := h.state.Movement(guerreroID)
	require.True(t, va, "lejos de la casa, se pone en marcha")
	assert.Equal(t, world.Tile{X: 19, Y: 38}, m.Target)
	objetivo, ok := h.state.Demolishing(guerreroID)
	require.True(t, ok)
	assert.Equal(t, casaID, objetivo)

	var golpes []int64
	var vidas []int32
	vida := int32(150)
	require.True(t, h.tickHasta(600, func() bool {
		hp, enPie := vidaDe(h, casaID)
		if !enPie {
			hp = 0
		}
		if hp != vida {
			vida = hp
			golpes = append(golpes, h.clk.NowMs())
			vidas = append(vidas, hp)
		}
		return !enPie
	}))

	require.Len(t, golpes, 19, "150 de vida a 8 por golpe")
	for i := 1; i < len(golpes); i++ {
		assert.Equal(t, int64(1500), golpes[i]-golpes[i-1], "golpe %d", i)
	}
	assert.Equal(t, int32(142), vidas[0])
	assert.Equal(t, int32(6), vidas[17])
	assert.Equal(t, int32(0), vidas[18])

	// La casa, fuera de la RAM y para todos; el límite, sin su bono.
	_, sigue := h.state.Building(casaID)
	assert.False(t, sigue)
	assert.EqualValues(t, 25, ciudad.PopulationLimit)
	assert.EqualValues(t, 3, ciudad.Population, "la población no cambia")
	destruidas := h.rec.byType(protocol.TypeBuildingDestroyed)
	require.Len(t, destruidas, 1)
	assert.Equal(t, protocol.BuildingDestroyedPayload{ID: casaID, CityID: ciudad.ID}, destruidas[0].Payload)

	// La escritura (ADR-029 §4): el atacante, la víctima, el bono y el instante.
	require.Len(t, h.economy.destroys, 1)
	d := h.economy.destroys[0]
	assert.Equal(t, casaID, d.BuildingID)
	assert.Equal(t, enemigo, d.VictimPlayer)
	assert.Equal(t, h.playerID, d.AttackerPlayer)
	assert.Equal(t, guerreroID, d.AttackerID)
	assert.Equal(t, tileCasa, d.At)
	assert.Equal(t, int32(5), d.Bonus)
	assert.Equal(t, golpes[18], d.AtMs)
	assert.Contains(t, h.persister.names(), simulation.JobBuildingDestroy)

	// El asedio termina; la otra casa sigue entera.
	_, asedia := h.state.Demolishing(guerreroID)
	assert.False(t, asedia)
	assert.Equal(t, unit.StatusIdle, g.Status)
	hp, enPie := vidaDe(h, casaID+1)
	assert.True(t, enPie)
	assert.Equal(t, int32(150), hp)
}

// §5: cada rechazo de unit.demolish con su código, sin efecto sobre el mundo.
func TestRechazosDelAsedio(t *testing.T) {
	casos := []struct {
		nombre   string
		preparar func(h *harness, enemigo uuid.UUID, c *city.City) (atacante, casa int64)
		codigo   string
	}{
		{"guerrero inexistente", func(h *harness, _ uuid.UUID, _ *city.City) (int64, int64) {
			return 999, casaID
		}, protocol.CodeUnitNotFound},
		{"guerrero ajeno", func(h *harness, enemigo uuid.UUID, c *city.City) (int64, int64) {
			ponerUnidad(h, enemigoID, enemigo, c.ID, unit.TypeWarrior, world.Tile{X: 30, Y: 30}, 60)
			return enemigoID, casaID
		}, protocol.CodeUnitNotOwned},
		{"el aldeano no derriba", func(h *harness, _ uuid.UUID, _ *city.City) (int64, int64) {
			return 1, casaID
		}, protocol.CodeUnitNotMovable},
		{"guerrero guarnecido", func(h *harness, _ uuid.UUID, _ *city.City) (int64, int64) {
			g, _ := h.state.Unit(guerreroID)
			g.Status = unit.StatusGarrisoned
			return guerreroID, casaID
		}, protocol.CodeUnitGarrisoned},
		{"casa inexistente", func(h *harness, _ uuid.UUID, _ *city.City) (int64, int64) {
			return guerreroID, 999
		}, protocol.CodeInvalidTarget},
		{"casa propia", func(h *harness, _ uuid.UUID, _ *city.City) (int64, int64) {
			conCasa(h, h.ciudad(), casaID+9, world.Tile{X: 11, Y: 11}, true)
			return guerreroID, casaID + 9
		}, protocol.CodeInvalidTarget},
		{"ciudad protegida", func(h *harness, _ uuid.UUID, c *city.City) (int64, int64) {
			c.PresenceState = city.PresenceProtected
			return guerreroID, casaID
		}, protocol.CodeCityProtected},
		{"atacante protegido (RN-COMBAT-018)", func(h *harness, _ uuid.UUID, _ *city.City) (int64, int64) {
			h.ciudad().PresenceState = city.PresenceProtected
			return guerreroID, casaID
		}, protocol.CodeCityProtected},
		{"tratado de no agresión", func(h *harness, enemigo uuid.UUID, _ *city.City) (int64, int64) {
			h.state.SetPeaceTreaties([]diplomacy.Treaty{{ID: 1, PlayerA: h.playerID, PlayerB: enemigo,
				Type: diplomacy.TypeNonAggression, Status: diplomacy.StatusActive}})
			return guerreroID, casaID
		}, protocol.CodeInvalidTarget},
	}
	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			h := newHarness(t)
			enemigo, ciudad, _ := conAsedio(h, world.Tile{X: 20, Y: 30})
			atacante, casa := c.preparar(h, enemigo, ciudad)
			h.derribar(atacante, casa)
			assert.Equal(t, []string{c.codigo}, h.codigos())
			_, asedia := h.state.Demolishing(atacante)
			assert.False(t, asedia)
			hp, enPie := vidaDe(h, casaID)
			assert.True(t, enPie)
			assert.Equal(t, int32(150), hp)
		})
	}
}

// RN-COMBAT-031: dos atacantes sobre la misma casa en el mismo tick se aplican
// en orden de id, cada uno contra la vida que dejó el anterior. Tras nueve
// rondas de 16 quedan 6: el de id menor la derriba y el golpe del otro no cae.
func TestDosAtacantesSobreUnaCasa(t *testing.T) {
	h := newHarness(t)
	_, _, a := conAsedio(h, world.Tile{X: 20, Y: 38})
	b := ponerUnidad(h, guerreroID+1, h.playerID, h.cityID, unit.TypeWarrior, world.Tile{X: 21, Y: 38}, 60)

	h.commands <- simulation.DemolishBuilding{PlayerID: h.playerID, RequestID: uuid.NewString(), UnitID: b.ID, BuildingID: casaID}
	h.commands <- simulation.DemolishBuilding{PlayerID: h.playerID, RequestID: uuid.NewString(), UnitID: a.ID, BuildingID: casaID}
	require.True(t, h.tickHasta(300, func() bool {
		_, enPie := vidaDe(h, casaID)
		return !enPie
	}))

	require.Len(t, h.economy.destroys, 1, "una sola destrucción")
	assert.Equal(t, a.ID, h.economy.destroys[0].AttackerID, "el de id menor, primero")
	for _, u := range []*unit.Unit{a, b} {
		_, asedia := h.state.Demolishing(u.ID)
		assert.False(t, asedia)
		assert.Equal(t, unit.StatusIdle, u.Status)
	}
}

// RN-COMBAT-033 y Q8: una casa en obra se derriba igual, sin tocar el límite
// (nunca sumó su bono); su madera se pierde y ya no se completa.
func TestUnaCasaEnObraSeDerribaSinTocarElLimite(t *testing.T) {
	h := newHarness(t)
	h.conectar()
	_, ciudad := conEnemigo(h)
	conCasa(h, ciudad, casaID, tileCasa, false)
	ponerUnidad(h, guerreroID, h.playerID, h.cityID, unit.TypeWarrior, world.Tile{X: 20, Y: 38}, 60)

	h.derribar(guerreroID, casaID)
	require.True(t, h.tickHasta(400, func() bool {
		_, enPie := vidaDe(h, casaID)
		return !enPie
	}))
	assert.EqualValues(t, 20, ciudad.PopulationLimit)
	for _, m := range h.rec.byType(protocol.TypeCityUpdate) {
		assert.Nil(t, m.Payload.(protocol.CityUpdatePayload).PopulationLimit, "el límite no cambia")
	}
	require.Len(t, h.economy.destroys, 1)

	h.advance(2 * time.Hour)
	assert.EqualValues(t, 20, ciudad.PopulationLimit, "una casa derribada no se completa")
	assert.Empty(t, h.economy.completions)
}

// RN-COMBAT-034, Q1 y ADR-029: con la población al tope, perder una casa deja
// la ciudad por encima de su límite. Conserva su población, lo que entrenaba
// nace (su plaza estaba reservada) y ningún encargo nuevo cabe.
func TestUnaCiudadPorEncimaDeSuLimiteConservaYNoEntrena(t *testing.T) {
	h := newHarness(t)
	enemigo, ciudad, _ := conAsedio(h, world.Tile{X: 20, Y: 38})
	ciudad.Population = 25 // al tope de 25: 20 de la era y 5 de la casa
	h.maderaDe(ciudad.ID, 500)
	h.state.SetEconomy(simulation.EconomyLoad{Productions: []production.Production{{
		CityID: ciudad.ID, UnitType: unit.TypeVillager, CompletesAtMs: h.clk.NowMs() + 40_000, RequestID: "en-curso",
	}}})

	h.derribar(guerreroID, casaID)
	require.True(t, h.tickHasta(400, func() bool {
		_, enPie := vidaDe(h, casaID)
		return !enPie
	}))
	assert.EqualValues(t, 20, ciudad.PopulationLimit)
	assert.EqualValues(t, 25, ciudad.Population, "nadie muere")

	h.advance(15 * time.Second)
	require.Len(t, h.economy.births, 1, "lo que entrenaba nace")
	assert.EqualValues(t, 25, ciudad.Population, "con la plaza que tenía reservada")

	h.rec.reset()
	h.send(simulation.TrainUnit{PlayerID: enemigo, RequestID: uuid.NewString(), CityID: ciudad.ID, Type: unit.TypeVillager})
	assert.Equal(t, []string{protocol.CodePopulationLimitReached}, h.codigos())
	assert.EqualValues(t, 25, ciudad.Population)
}

// RN-COMBAT-032: el daño vive en la RAM y se olvida a los 5 minutos sin golpes;
// un golpe nuevo vuelve a contar desde él. Al olvidarse, la casa vuelve a estar
// entera para todos.
func TestElDanoDeUnaCasaSeOlvida(t *testing.T) {
	h := newHarness(t)
	conAsedio(h, world.Tile{X: 20, Y: 38})
	h.derribar(guerreroID, casaID)
	h.advance(3 * time.Second) // golpes en 0, 1,5 y 3 s
	h.send(simulation.CancelMovement{PlayerID: h.playerID, RequestID: uuid.NewString(), UnitID: guerreroID})
	hp, _ := vidaDe(h, casaID)
	require.Less(t, hp, int32(150))
	golpeada := hp

	h.advance(4*time.Minute + 50*time.Second)
	hp, _ = vidaDe(h, casaID)
	assert.Equal(t, golpeada, hp, "a los 4 min 50 s sigue dañada")

	h.rec.reset()
	h.advance(15 * time.Second)
	hp, _ = vidaDe(h, casaID)
	assert.Equal(t, int32(150), hp, "a los 5 min sin golpes, entera")
	var enteras []int32
	for _, m := range h.rec.byType(protocol.TypeBuildingUpdate) {
		if p := m.Payload.(protocol.BuildingUpdatePayload); p.ID == casaID && p.HP != nil {
			enteras = append(enteras, *p.HP)
		}
	}
	assert.Equal(t, []int32{150}, enteras)
}

// RN-COMBAT-035 e INV-COMBAT-005: dentro del margen de reconexión el asedio
// sigue; con el dueño ausente, termina.
func TestElAsedioTerminaConElDuenoAusente(t *testing.T) {
	h := newHarness(t)
	conAsedio(h, world.Tile{X: 20, Y: 38})
	h.derribar(guerreroID, casaID)
	h.tick()
	hp, _ := vidaDe(h, casaID)
	require.Equal(t, int32(142), hp)

	h.send(simulation.PlayerDisconnected{PlayerID: h.playerID, SessionID: sesion})
	h.advance(1600 * time.Millisecond)
	hp, _ = vidaDe(h, casaID)
	assert.Equal(t, int32(134), hp, "en el margen de reconexión, sigue golpeando")

	h.ciudad().PresenceState = city.PresenceOfflinePending
	h.tick()
	_, asedia := h.state.Demolishing(guerreroID)
	assert.False(t, asedia, "con el dueño ausente, el asedio termina")
	h.advance(5 * time.Second)
	hp, _ = vidaDe(h, casaID)
	assert.Equal(t, int32(134), hp)
}

// RN-COMBAT-030 e INV-COMBAT-002: la protección de la ciudad a mitad del asedio
// lo termina; el predicado se revalida en cada golpe.
func TestLaProteccionAMitadDelAsedioLoTermina(t *testing.T) {
	h := newHarness(t)
	_, ciudad, g := conAsedio(h, world.Tile{X: 20, Y: 38})
	h.derribar(guerreroID, casaID)
	h.tick()

	ciudad.PresenceState = city.PresenceProtected
	h.advance(3 * time.Second)
	_, asedia := h.state.Demolishing(guerreroID)
	assert.False(t, asedia)
	assert.Equal(t, unit.StatusIdle, g.Status)
	hp, _ := vidaDe(h, casaID)
	assert.Equal(t, int32(142), hp, "un solo golpe, el de antes de la protección")
}

// RN-COMBAT-038: el aviso del asedio es un city.update con besiegedAtMs que
// sólo recibe el dueño; la vida de la casa va a todo el chunk.
func TestElAvisoDeAsedioSoloLoRecibeElDueno(t *testing.T) {
	h := newHarness(t)
	enemigo, ciudad, _ := conAsedio(h, world.Tile{X: 20, Y: 38})
	h.rec.reset()
	h.derribar(guerreroID, casaID)
	h.tick()

	var avisos []capturedMessage
	for _, m := range h.rec.byType(protocol.TypeCityUpdate) {
		if p := m.Payload.(protocol.CityUpdatePayload); p.BesiegedAtMs != nil {
			avisos = append(avisos, m)
		}
	}
	require.Len(t, avisos, 1)
	assert.Equal(t, "player", avisos[0].Kind)
	assert.Equal(t, enemigo, avisos[0].PlayerID)
	assert.Equal(t, ciudad.ID, avisos[0].Payload.(protocol.CityUpdatePayload).ID)

	vidas := h.rec.byType(protocol.TypeBuildingUpdate)
	require.Len(t, vidas, 1)
	assert.Equal(t, "chunk", vidas[0].Kind)
	p := vidas[0].Payload.(protocol.BuildingUpdatePayload)
	require.NotNil(t, p.HP)
	assert.Equal(t, int32(142), *p.HP)
	assert.Equal(t, string(building.StatusComplete), p.Status)
}

// RN-COMBAT-037: un guerrero parado del atacado a ≤ 4 del centro de su ciudad
// responde al primer golpe de asedio.
func TestLosGuerrerosDeLaCiudadDefiendenSusCasas(t *testing.T) {
	h := newHarness(t)
	enemigo, ciudad, _ := conAsedio(h, world.Tile{X: 20, Y: 37})
	ponerUnidad(h, enemigoID, enemigo, ciudad.ID, unit.TypeWarrior, world.Tile{X: 20, Y: 43}, 60)

	h.derribar(guerreroID, casaID)
	require.True(t, h.tickHasta(50, func() bool {
		hp, _ := vidaDe(h, casaID)
		return hp < 150
	}))
	objetivo, responde := h.state.Engaged(enemigoID)
	require.True(t, responde, "el guardia responde al asedio")
	assert.Equal(t, guerreroID, objetivo)
}

// ADR-029 §5 y §4: hasta que la destrucción está escrita, el tile sigue ocupado
// para city.build; si la cola la descarta, se reencola en el volcado, y con la
// escritura hecha el tile se libera.
func TestElTileDeUnaCasaDerribadaSeLiberaAlEscribirse(t *testing.T) {
	h := newHarness(t)
	enemigo, ciudad, _ := conAsedio(h, world.Tile{X: 20, Y: 38})
	h.maderaDe(ciudad.ID, 500)
	h.economy.destroyErr = errors.New("la base no responde")
	h.persister.permanent = true

	h.derribar(guerreroID, casaID)
	require.True(t, h.tickHasta(400, func() bool {
		_, enPie := vidaDe(h, casaID)
		return !enPie
	}))
	h.tick()
	assert.Empty(t, h.economy.destroys)

	construir := func() []string {
		h.rec.reset()
		h.send(simulation.BuildBuilding{PlayerID: enemigo, RequestID: uuid.NewString(), CityID: ciudad.ID,
			Type: building.TypeHouse, Tile: tileCasa})
		return h.codigos()
	}
	assert.Equal(t, []string{protocol.CodeInvalidTarget}, construir(), "sin escribir, el tile sigue ocupado")

	h.economy.destroyErr = nil
	h.advance(6 * time.Second) // un volcado reencola la destrucción
	require.Len(t, h.economy.destroys, 1)
	h.tick()
	assert.Empty(t, construir(), "escrita la destrucción, se reconstruye")
}

// Recuperación (§9 «recovery»): el daño no se persiste. Una simulación que
// arranca de la base ve las casas enteras, también la que tenía dañada.
func TestUnReinicioDejaLasCasasEnteras(t *testing.T) {
	h := newHarness(t)
	_, ciudad, _ := conAsedio(h, world.Tile{X: 20, Y: 38})
	h.derribar(guerreroID, casaID)
	h.advance(2 * time.Second)
	hp, _ := vidaDe(h, casaID)
	require.Less(t, hp, int32(150))

	// El arranque carga la fila tal como está en la base.
	b, ok := h.state.Building(casaID)
	require.True(t, ok)
	otra := newHarness(t)
	_, c2 := conEnemigo(otra)
	require.Equal(t, ciudad.ID, c2.ID)
	otra.state.SetEconomy(simulation.EconomyLoad{Buildings: []building.Building{b}})
	hp, maxHP, ok := otra.state.BuildingHP(casaID)
	require.True(t, ok)
	assert.Equal(t, int32(150), hp)
	assert.Equal(t, int32(150), maxHP)
}
