package simulation_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/diplomacy"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/economy"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// Tests del saqueo (SPEC-COMBAT-PILLAGE §9). Mundo de newHarness: la ciudad
// propia en (10,10); la enemiga de conEnemigo en (20,40), con su muralla de
// (19,39) a (21,41) y el anillo de alcance de (18,38) a (22,42).

// maderaDe instala el stock de madera de una ciudad.
func (h *harness) maderaDe(cityID, n int64) {
	h.t.Helper()
	h.state.SetEconomy(simulation.EconomyLoad{Stocks: []simulation.StockRow{
		{CityID: cityID, Stock: economy.Stock{Resource: economy.ResourceWood, Amount: n, Version: 1}},
	}})
}

func (h *harness) saquear(atacante, ciudad int64) {
	h.t.Helper()
	h.send(simulation.PillageCity{PlayerID: h.playerID, RequestID: uuid.NewString(), UnitID: atacante, CityID: ciudad})
}

// conSaqueo prepara al jugador conectado, la ciudad enemiga con `madera` y un
// guerrero propio en `at`.
func conSaqueo(h *harness, madera int64, at world.Tile) (uuid.UUID, *city.City, *unit.Unit) {
	h.t.Helper()
	h.conectar()
	enemigo, c := conEnemigo(h)
	h.maderaDe(c.ID, madera)
	g := ponerUnidad(h, guerreroID, h.playerID, h.cityID, unit.TypeWarrior, at, 60)
	return enemigo, c, g
}

// Golden «saqueo» (RN-COMBAT-021, RN-COMBAT-025, RN-COMBAT-026): el guerrero va
// al tile del anillo más cercano —(18,38): empate en distancia, menor x—, roba
// 5 por golpe cada 1 500 ms exactos, se para con la carga llena (20) junto a la
// muralla, y al llevarla a su depósito la entrega entera. INV-COMBAT-003: lo que
// pierde el almacén es lo que gana el guerrero y lo que llega a casa.
func TestElSaqueoGoldenCuatroGolpesYDeposito(t *testing.T) {
	h := newHarness(t)
	_, ciudad, g := conSaqueo(h, 200, world.Tile{X: 20, Y: 30})
	h.darMadera(0)

	h.saquear(guerreroID, ciudad.ID)
	require.Empty(t, h.codigos())
	m, va := h.state.Movement(guerreroID)
	require.True(t, va, "lejos de la muralla, se pone en marcha")
	assert.Equal(t, world.Tile{X: 18, Y: 38}, m.Target)
	cid, ok := h.state.Pillaging(guerreroID)
	require.True(t, ok)
	assert.Equal(t, ciudad.ID, cid)

	var golpes []int64
	var stocks []int64
	stock := h.state.Stock(ciudad.ID).Amount
	require.True(t, h.tickHasta(300, func() bool {
		if s := h.state.Stock(ciudad.ID).Amount; s != stock {
			stock = s
			golpes = append(golpes, h.clk.NowMs())
			stocks = append(stocks, s)
		}
		_, sigue := h.state.Pillaging(guerreroID)
		return !sigue
	}))
	h.tick() // la confirmación del último golpe vuelve por el buzón
	assert.Equal(t, []int64{195, 190, 185, 180}, stocks)
	for i := 1; i < len(golpes); i++ {
		assert.Equal(t, int64(1_500), golpes[i]-golpes[i-1], "golpe %d", i)
	}
	assert.Equal(t, int32(20), g.Loot, "toda la carga confirmada")
	assert.Zero(t, h.state.PendingLoot(guerreroID))
	assert.Equal(t, unit.StatusIdle, g.Status, "Q6: queda parado junto a la muralla")
	assert.True(t, ciudad.IsAdjacent(g.X, g.Y))
	require.Len(t, h.combat.pillages, 4)
	for _, p := range h.combat.pillages {
		assert.Equal(t, int32(5), p.Amount)
		assert.Equal(t, ciudad.ID, p.CityID)
	}

	h.send(simulation.MoveUnit{PlayerID: h.playerID, RequestID: uuid.NewString(), UnitID: guerreroID,
		Target: world.Tile{X: 12, Y: 10}})
	h.advance(40 * time.Second)
	assert.Equal(t, int32(0), g.Loot)
	assert.Equal(t, int64(20), h.madera(), "deposita exactamente lo robado")
}

// §5: cada rechazo de unit.pillage con su código, sin efecto sobre el mundo.
func TestRechazosDelSaqueo(t *testing.T) {
	casos := []struct {
		nombre   string
		preparar func(h *harness, enemigo uuid.UUID, c *city.City) (atacante, ciudad int64)
		codigo   string
	}{
		{"guerrero inexistente", func(h *harness, _ uuid.UUID, c *city.City) (int64, int64) {
			return 999, c.ID
		}, protocol.CodeUnitNotFound},
		{"guerrero ajeno", func(h *harness, enemigo uuid.UUID, c *city.City) (int64, int64) {
			ponerUnidad(h, enemigoID, enemigo, c.ID, unit.TypeWarrior, world.Tile{X: 30, Y: 30}, 60)
			return enemigoID, h.cityID
		}, protocol.CodeUnitNotOwned},
		{"el aldeano no saquea", func(h *harness, _ uuid.UUID, c *city.City) (int64, int64) {
			return 1, c.ID
		}, protocol.CodeUnitNotMovable},
		{"guerrero guarnecido", func(h *harness, _ uuid.UUID, c *city.City) (int64, int64) {
			g, _ := h.state.Unit(guerreroID)
			g.Status = unit.StatusGarrisoned
			return guerreroID, c.ID
		}, protocol.CodeUnitGarrisoned},
		{"ciudad inexistente", func(h *harness, _ uuid.UUID, _ *city.City) (int64, int64) {
			return guerreroID, 99
		}, protocol.CodeCityNotFound},
		{"ciudad propia", func(h *harness, _ uuid.UUID, _ *city.City) (int64, int64) {
			return guerreroID, h.cityID
		}, protocol.CodeInvalidTarget},
		{"ciudad protegida", func(h *harness, _ uuid.UUID, c *city.City) (int64, int64) {
			c.PresenceState = city.PresenceProtected
			return guerreroID, c.ID
		}, protocol.CodeCityProtected},
		{"saqueador protegido (RN-COMBAT-018)", func(h *harness, _ uuid.UUID, c *city.City) (int64, int64) {
			h.ciudad().PresenceState = city.PresenceProtected
			return guerreroID, c.ID
		}, protocol.CodeCityProtected},
		{"tratado de no agresión", func(h *harness, enemigo uuid.UUID, c *city.City) (int64, int64) {
			h.state.SetPeaceTreaties([]diplomacy.Treaty{{ID: 1, PlayerA: h.playerID, PlayerB: enemigo,
				Type: diplomacy.TypeNonAggression, Status: diplomacy.StatusActive}})
			return guerreroID, c.ID
		}, protocol.CodeInvalidTarget},
	}
	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			h := newHarness(t)
			enemigo, ciudad, _ := conSaqueo(h, 200, world.Tile{X: 20, Y: 30})
			atacante, objetivo := c.preparar(h, enemigo, ciudad)
			h.saquear(atacante, objetivo)
			assert.Equal(t, []string{c.codigo}, h.codigos())
			_, saquea := h.state.Pillaging(atacante)
			assert.False(t, saquea)
			assert.Equal(t, int64(200), h.state.Stock(ciudad.ID).Amount)
		})
	}
}

// RN-COMBAT-020 e INV-COMBAT-002: no se saquea desde una Safe Zone.
func TestNoSeSaqueaDesdeUnaZonaSegura(t *testing.T) {
	h := newHarnessConZona(t)
	h.conectar()
	h.state.AddCity(&city.City{ID: 3, OwnerPlayerID: otroJugador, Name: "Lejana", CenterX: 40, CenterY: 40,
		Era: city.EraStone, PopulationLimit: 20, PresenceState: city.PresenceOnline})
	h.maderaDe(3, 200)
	ponerUnidad(h, guerreroID, h.playerID, h.cityID, unit.TypeWarrior, world.Tile{X: franjaMinX, Y: 30}, 60)

	h.saquear(guerreroID, 3)
	assert.Equal(t, []string{protocol.CodeInvalidTarget}, h.codigos())
}

// RN-COMBAT-021, RN-COMBAT-023 e INV-COMBAT-004: el saqueo no baja el almacén
// del suelo (50). Con 57, roba 5 y luego 2, y el saqueo termina con 50 aunque
// al guerrero le quede hueco.
func TestElSaqueoRespetaElSuelo(t *testing.T) {
	h := newHarness(t)
	_, ciudad, g := conSaqueo(h, 57, world.Tile{X: 20, Y: 37})

	h.saquear(guerreroID, ciudad.ID)
	require.Empty(t, h.codigos())
	require.True(t, h.tickHasta(100, func() bool {
		_, sigue := h.state.Pillaging(guerreroID)
		return !sigue
	}))
	assert.Equal(t, int64(50), h.state.Stock(ciudad.ID).Amount)
	assert.Equal(t, int32(7), g.Loot)
	require.Len(t, h.combat.pillages, 2)
	assert.Equal(t, int32(5), h.combat.pillages[0].Amount)
	assert.Equal(t, int32(2), h.combat.pillages[1].Amount)
	// ADR-028 §3: cada golpe lleva su instante en la simulación, en orden.
	assert.Positive(t, h.combat.pillages[0].AtMs)
	assert.Greater(t, h.combat.pillages[1].AtMs, h.combat.pillages[0].AtMs)

	// Un almacén ya en el suelo no es un error al ordenar: el guerrero llega y
	// el primer golpe no roba nada (§5).
	h.saquear(guerreroID, ciudad.ID)
	assert.Empty(t, h.codigos())
	h.advance(3 * time.Second)
	assert.Equal(t, int64(50), h.state.Stock(ciudad.ID).Amount)
	_, sigue := h.state.Pillaging(guerreroID)
	assert.False(t, sigue)
}

// RN-COMBAT-022: dos saqueadores en el mismo tick se aplican en orden de id,
// cada uno contra lo que dejó el anterior. Con 58, el de id menor roba 5 y el
// otro 3, y el almacén queda en el suelo.
func TestDosSaqueadoresSeRepartenLoQueQuedaPorEncimaDelSuelo(t *testing.T) {
	h := newHarness(t)
	_, ciudad, a := conSaqueo(h, 58, world.Tile{X: 20, Y: 38})
	b := ponerUnidad(h, guerreroID+1, h.playerID, h.cityID, unit.TypeWarrior, world.Tile{X: 21, Y: 38}, 60)

	h.commands <- simulation.PillageCity{PlayerID: h.playerID, RequestID: uuid.NewString(), UnitID: b.ID, CityID: ciudad.ID}
	h.commands <- simulation.PillageCity{PlayerID: h.playerID, RequestID: uuid.NewString(), UnitID: a.ID, CityID: ciudad.ID}
	h.tick()
	h.tick()

	assert.Equal(t, int64(50), h.state.Stock(ciudad.ID).Amount)
	assert.Equal(t, int32(5), a.Loot, "el de id menor, primero")
	assert.Equal(t, int32(3), b.Loot)
}

// RN-COMBAT-024 e INV-COMBAT-005: dentro del margen de reconexión el saqueo
// sigue; con el dueño ausente, termina.
func TestElSaqueoTerminaConElDuenoAusente(t *testing.T) {
	h := newHarness(t)
	_, ciudad, _ := conSaqueo(h, 500, world.Tile{X: 20, Y: 38})
	h.saquear(guerreroID, ciudad.ID)
	h.tick()
	require.Len(t, h.combat.pillages, 1)

	h.send(simulation.PlayerDisconnected{PlayerID: h.playerID, SessionID: sesion})
	h.advance(1600 * time.Millisecond)
	assert.Len(t, h.combat.pillages, 2, "en el margen de reconexión, sigue saqueando")

	h.ciudad().PresenceState = city.PresenceOfflinePending
	h.tick()
	_, sigue := h.state.Pillaging(guerreroID)
	assert.False(t, sigue, "con el dueño ausente, el saqueo termina")
	h.advance(5 * time.Second)
	assert.Len(t, h.combat.pillages, 2)
}

// RN-COMBAT-025: la protección de la ciudad a mitad del saqueo lo termina.
func TestLaProteccionAMitadDelSaqueoLoTermina(t *testing.T) {
	h := newHarness(t)
	_, ciudad, g := conSaqueo(h, 500, world.Tile{X: 20, Y: 38})
	h.saquear(guerreroID, ciudad.ID)
	h.tick()
	require.Len(t, h.combat.pillages, 1)

	ciudad.PresenceState = city.PresenceProtected
	h.advance(3 * time.Second)
	_, sigue := h.state.Pillaging(guerreroID)
	assert.False(t, sigue)
	assert.Len(t, h.combat.pillages, 1)
	assert.Equal(t, unit.StatusIdle, g.Status)
}

// RN-COMBAT-028: el aviso del saqueo es un city.update con pillagedAtMs que
// sólo recibe el dueño de la ciudad; el stock va a todo el chunk.
func TestElAvisoDeSaqueoSoloLoRecibeElDueno(t *testing.T) {
	h := newHarness(t)
	enemigo, ciudad, _ := conSaqueo(h, 200, world.Tile{X: 20, Y: 38})
	h.rec.reset()
	h.saquear(guerreroID, ciudad.ID)
	h.tick()

	var avisos []capturedMessage
	for _, m := range h.rec.byType(protocol.TypeCityUpdate) {
		if p := m.Payload.(protocol.CityUpdatePayload); p.PillagedAtMs != nil {
			avisos = append(avisos, m)
		}
	}
	require.Len(t, avisos, 1)
	assert.Equal(t, "player", avisos[0].Kind)
	assert.Equal(t, enemigo, avisos[0].PlayerID)
	assert.Equal(t, ciudad.ID, avisos[0].Payload.(protocol.CityUpdatePayload).ID)
}

// RN-COMBAT-027 y RN-COMBAT-026: un guerrero parado del saqueado a ≤ 4 del
// centro de su ciudad responde al primer golpe de saqueo; si mata al
// saqueador, se queda con lo que llevaba.
func TestLosGuerrerosDeLaCiudadDefiendenDelSaqueo(t *testing.T) {
	h := newHarness(t)
	enemigo, ciudad, _ := conSaqueo(h, 500, world.Tile{X: 20, Y: 37})
	guardia := ponerUnidad(h, enemigoID, enemigo, ciudad.ID, unit.TypeWarrior, world.Tile{X: 20, Y: 43}, 60)

	h.saquear(guerreroID, ciudad.ID)
	require.True(t, h.tickHasta(50, func() bool { return len(h.combat.pillages) > 0 }))
	objetivo, responde := h.state.Engaged(enemigoID)
	require.True(t, responde, "el guardia responde al saqueo")
	assert.Equal(t, guerreroID, objetivo)

	require.True(t, h.tickHasta(400, func() bool {
		_, vive := h.state.Unit(guerreroID)
		return !vive
	}), "el guardia, que golpea primero, mata al saqueador")
	assert.Positive(t, guardia.Loot, "recupera lo robado")
	require.Len(t, h.combat.deaths, 1)
	assert.Equal(t, guardia.Loot, h.combat.deaths[0].Loot)
}

// ADR-026 §1: si la transacción de un golpe no se escribe, lo robado vuelve
// al almacén de la víctima y la carga pendiente desaparece.
func TestUnGolpeDeSaqueoQueNoSeEscribeDevuelveLaMadera(t *testing.T) {
	h := newHarness(t)
	_, ciudad, g := conSaqueo(h, 200, world.Tile{X: 20, Y: 38})
	h.combat.pillageErr = economy.ErrInsufficientResources

	h.saquear(guerreroID, ciudad.ID)
	h.tick()
	h.tick() // la respuesta vuelve por el buzón
	assert.Equal(t, int64(200), h.state.Stock(ciudad.ID).Amount, "la madera vuelve")
	assert.Zero(t, g.Loot)
	assert.Zero(t, h.state.PendingLoot(guerreroID))
}

// ADR-026 §2 y §4: mientras la transacción no vuelve, la carga es pendiente y
// no está en unit.Loot (el volcado no la escribe); si el guerrero muere, su
// asesino no la hereda, y la confirmación posterior no se la da a nadie.
func TestLaCargaPendienteNoSeVuelcaNiSeHereda(t *testing.T) {
	h := newHarness(t)
	enemigo, ciudad, g := conSaqueo(h, 200, world.Tile{X: 20, Y: 38})
	// Un fallo transitorio: el trabajo queda «en vuelo», sin respuesta.
	h.combat.pillageErr = errors.New("conexión perdida")

	h.saquear(guerreroID, ciudad.ID)
	h.tick()
	require.Equal(t, int32(5), h.state.PendingLoot(guerreroID))
	assert.Zero(t, g.Loot, "lo pendiente no está en la carga que se vuelca")
	assert.Equal(t, int64(195), h.state.Stock(ciudad.ID).Amount)

	// Muere a manos de un guardia antes de que vuelva la transacción.
	g.HP = 8
	asesino := ponerUnidad(h, enemigoID, enemigo, ciudad.ID, unit.TypeWarrior, world.Tile{X: 19, Y: 37}, 60)
	h.commands <- simulation.AttackUnit{PlayerID: enemigo, RequestID: uuid.NewString(), UnitID: enemigoID, TargetID: guerreroID}
	h.tick()
	_, vive := h.state.Unit(guerreroID)
	require.False(t, vive)
	assert.Zero(t, asesino.Loot, "la carga pendiente no se hereda")

	// La transacción se confirma tarde: esa madera se pierde, no aparece.
	h.send(simulation.PillageResolved{UnitID: guerreroID, CityID: ciudad.ID, Amount: 5})
	assert.Equal(t, int64(195), h.state.Stock(ciudad.ID).Amount)
	assert.Zero(t, asesino.Loot)
}

// ADR-024 §2 y ADR-026 §2, nivel «recovery»: el volcado escribe al saqueador
// IDLE (ATTACKING no se persiste) y con la carga confirmada solamente. Una
// caída tras el débito de la víctima pierde lo pendiente; nunca lo duplica.
func TestElVolcadoEscribeAlSaqueadorIdleYSinLaCargaPendiente(t *testing.T) {
	h := newHarness(t)
	_, ciudad, g := conSaqueo(h, 200, world.Tile{X: 20, Y: 38})
	h.saquear(guerreroID, ciudad.ID)
	h.tick() // el primer golpe se confirma por el buzón
	require.Equal(t, int32(5), g.Loot)

	h.combat.pillageErr = errors.New("conexión perdida") // el segundo, en vuelo
	h.advance(1500 * time.Millisecond)
	require.Equal(t, int32(5), h.state.PendingLoot(guerreroID))
	require.Equal(t, unit.StatusAttacking, g.Status)

	h.sim.FlushDirty()
	volcada, ok := h.repos.flushed[guerreroID]
	require.True(t, ok)
	assert.Equal(t, unit.StatusIdle, volcada.Status)
	assert.Equal(t, int32(5), volcada.Loot, "sólo la carga confirmada")
}
