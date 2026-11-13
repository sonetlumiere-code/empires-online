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

	"github.com/empires-online/empires-online/services/game-server/internal/domain/caravan"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/diplomacy"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/economy"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/movement"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// Tests de las caravanas (SPEC-CARAVANS §9). Mundo de newHarness: el jugador
// del arnés con su ciudad 1 en (10,10) y un vecino con la ciudad 2 en (20,40),
// con un tratado TRADE. El trato: 300 de madera del arnés por 100 de piedra
// del vecino. La caravana 9001 lleva la madera a la ciudad 2 y la 9002 la
// piedra a la ciudad 1.

// fakeCaravans hace de base de las caravanas: anota las entregas y las
// capturas, en orden.
type fakeCaravans struct {
	mu         sync.Mutex
	deliveries []simulation.CaravanDelivery
	captures   []simulation.CaravanCapture
	deliverErr error
}

func (f *fakeCaravans) DeliverCaravan(_ context.Context, d simulation.CaravanDelivery) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.deliverErr != nil {
		return f.deliverErr
	}
	f.deliveries = append(f.deliveries, d)
	return nil
}

func (f *fakeCaravans) CaptureCaravan(_ context.Context, c simulation.CaravanCapture) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.captures = append(f.captures, c)
	return nil
}

func (f *fakeCaravans) entregas() []simulation.CaravanDelivery {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]simulation.CaravanDelivery(nil), f.deliveries...)
}

func (f *fakeCaravans) capturas() []simulation.CaravanCapture {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]simulation.CaravanCapture(nil), f.captures...)
}

const (
	caravanaDeMadera = primeraCaravana     // la del arnés, hacia la ciudad 2
	caravanaDePiedra = primeraCaravana + 1 // la del vecino, hacia la ciudad 1
	asaltanteID      = int64(300)
	escoltaID        = int64(301)
)

// tratoEnCamino firma y acepta el trato del arnés: devuelve al vecino. Al
// volver, las dos caravanas ya están en el mapa.
func tratoEnCamino(h *harness) uuid.UUID {
	h.t.Helper()
	vecino := conComercio(h, diplomacy.TypeTrade)
	h.almacen(1, 300, 0)
	h.almacen(2, 0, 100)
	h.ofrecer(h.playerID, vecino, economy.Costs{madera(300)}, economy.Costs{piedra(100)})
	h.responderTrato(vecino, primeraOferta, true)
	require.Empty(h.t, h.codigos())
	require.Equal(h.t, 2, h.state.CaravanCount(), "las dos caravanas salen al confirmarse el trato")
	return vecino
}

// conTercero añade un tercer jugador con su ciudad 3 en (45,12).
func conTercero(h *harness) uuid.UUID {
	h.t.Helper()
	p := uuid.MustParse("33333333-3333-4333-8333-333333333333")
	h.state.AddCity(&city.City{
		ID: 3, OwnerPlayerID: p, Name: "Tercera", CenterX: 45, CenterY: 12, Era: city.EraStone,
		PopulationLimit: 20, PresenceState: city.PresenceOnline,
	})
	return p
}

// depositoDe es el depósito de una ciudad (RN-ECON-004).
func depositoDe(h *harness, cityID int64) world.Tile {
	h.t.Helper()
	c, ok := h.state.City(cityID)
	require.True(h.t, ok)
	t, ok := c.FirstSpawnTile(h.state.World().IsWalkable)
	require.True(h.t, ok)
	return t
}

func (h *harness) movimientoDe(unitID int64) *movement.Movement {
	h.t.Helper()
	m, ok := h.state.Movement(unitID)
	require.True(h.t, ok, "la unidad %d debería moverse", unitID)
	return m
}

// avisosPara son los event_log.notice que recibió un jugador.
func (h *harness) avisosPara(p uuid.UUID) []protocol.EventLogNoticePayload {
	var out []protocol.EventLogNoticePayload
	for _, m := range h.rec.byType(protocol.TypeEventLogNotice) {
		if m.Kind == "player" && m.PlayerID == p {
			out = append(out, m.Payload.(protocol.EventLogNoticePayload))
		}
	}
	return out
}

// caravanasPara son los caravan.update que recibió un jugador.
func (h *harness) caravanasPara(p uuid.UUID) []protocol.CaravanView {
	var out []protocol.CaravanView
	for _, m := range h.rec.byType(protocol.TypeCaravanUpdate) {
		if m.Kind == "player" && m.PlayerID == p {
			out = append(out, m.Payload.(protocol.CaravanUpdatePayload).Caravan)
		}
	}
	return out
}

// Golden «caravanas» (RN-CARAVAN-001 a RN-CARAVAN-004, INV-TRADE-001,
// INV-TRADE-005 a INV-TRADE-007): el trato sólo debita; salen dos caravanas,
// una por sentido, por la misma ruta al revés; cada una llega al depósito de la
// otra ciudad en el tick exacto de su polilínea, y su carga entra entonces en
// el almacén.
func TestGoldenCaravanasDelTratoALaLlegada(t *testing.T) {
	h := newHarness(t)
	vecino := tratoEnCamino(h)
	poblacion := h.ciudad().Population

	// El trato debitó y no acreditó nada.
	w, s := h.recursos(1)
	assert.Equal(t, [2]int64{0, 0}, [2]int64{w, s})
	w, s = h.recursos(2)
	assert.Equal(t, [2]int64{0, 0}, [2]int64{w, s})
	require.Len(t, h.trade.accepts, 1)
	a := h.trade.accepts[0]
	assert.ElementsMatch(t, []simulation.StockDelta{
		{CityID: 1, Resource: economy.ResourceWood, Delta: -300, Version: a.Stocks[0].Version},
		{CityID: 2, Resource: economy.ResourceStone, Delta: -100, Version: a.Stocks[1].Version},
	}, a.Stocks, "sólo los débitos: lo que recibe cada uno viaja en su caravana")
	require.Len(t, a.Caravans, 2)

	// Las dos caravanas: cada una con lo que da su parte, de su ciudad a la otra.
	madera, ok := h.state.Caravan(caravanaDeMadera)
	require.True(t, ok)
	assert.Equal(t, h.playerID, madera.Sender)
	assert.Equal(t, [3]int64{1, 2, 2}, [3]int64{madera.FromCity, madera.ToCity, madera.OriginalToCity})
	assert.Equal(t, economy.Costs{madera300()}, madera.Cargo)
	assert.Equal(t, primeraOferta, madera.OfferID)
	piedra, ok := h.state.Caravan(caravanaDePiedra)
	require.True(t, ok)
	assert.Equal(t, vecino, piedra.Sender)
	assert.Equal(t, economy.Costs{piedra100()}, piedra.Cargo)

	for id, dueno := range map[int64]uuid.UUID{caravanaDeMadera: h.playerID, caravanaDePiedra: vecino} {
		u, ok := h.state.Unit(id)
		require.True(t, ok)
		assert.Equal(t, unit.TypeCaravan, u.Type)
		assert.Equal(t, unit.StatusMoving, u.Status)
		assert.Equal(t, dueno, u.PlayerID)
		assert.Equal(t, int32(80), u.HP)
	}
	assert.Equal(t, poblacion, h.ciudad().Population, "RN-CARAVAN-003: no ocupan población")

	// RN-CARAVAN-002: la misma ruta al revés, del depósito de una ciudad al de
	// la otra, a 1 200 ms por tile de hierba (Q4).
	mm, mp := h.movimientoDe(caravanaDeMadera), h.movimientoDe(caravanaDePiedra)
	assert.Equal(t, depositoDe(h, 1), mm.Path.Origin())
	assert.Equal(t, depositoDe(h, 2), mm.Destination())
	assert.Equal(t, depositoDe(h, 2), mp.Path.Origin())
	assert.Equal(t, depositoDe(h, 1), mp.Destination())
	require.Len(t, mp.Path, len(mm.Path))
	for i := range mm.Path {
		assert.Equal(t, mm.Path[i].Tile(), mp.Path[len(mp.Path)-1-i].Tile())
	}
	assert.Equal(t, mm.ArrivalTimeMs, mp.ArrivalTimeMs, "salen a la vez y tardan lo mismo")
	tiles := make([]world.Tile, len(mm.Path))
	for i, w := range mm.Path {
		tiles[i] = w.Tile()
	}
	esperada, err := movement.BuildTimedPath(tiles, h.state.World(), 1200)
	require.NoError(t, err)
	assert.Equal(t, esperada, mm.Path, "1 200 ms por tile de hierba, el rasgo romano neutro")

	// Las dos salen a la vista de todo el chunk, y cada jugador recibe las dos
	// con su carga (RN-CARAVAN-009).
	spawns := 0
	for _, m := range h.rec.byType(protocol.TypeEntitySpawn) {
		if m.Payload.(protocol.EntitySpawnPayload).Unit.UnitType == string(unit.TypeCaravan) {
			spawns++
		}
	}
	assert.Equal(t, 2, spawns)
	for _, p := range []uuid.UUID{h.playerID, vecino} {
		vistas := h.caravanasPara(p)
		require.Len(t, vistas, 2)
		for _, v := range vistas {
			assert.Equal(t, string(caravan.StatusInTransit), v.Status)
			require.NotNil(t, v.ArrivesAtMs)
			assert.Equal(t, mm.ArrivalTimeMs, *v.ArrivesAtMs)
		}
	}

	// Un tick antes de llegar, nada ha entrado.
	h.hasta(mm.ArrivalTimeMs - 100)
	w, _ = h.recursos(2)
	assert.Zero(t, w)
	assert.Empty(t, h.caravans.entregas())

	// En el tick de la llegada, las dos cargas entran y las caravanas
	// desaparecen (INV-TRADE-001: lo que salió es lo que entra).
	h.hasta(mm.ArrivalTimeMs)
	w, s = h.recursos(1)
	assert.Equal(t, [2]int64{0, 100}, [2]int64{w, s})
	w, s = h.recursos(2)
	assert.Equal(t, [2]int64{300, 0}, [2]int64{w, s})
	assert.Zero(t, h.state.CaravanCount())
	_, sigue := h.state.Unit(caravanaDeMadera)
	assert.False(t, sigue)

	entregas := h.caravans.entregas()
	require.Len(t, entregas, 2)
	porCiudad := map[int64]simulation.CaravanDelivery{}
	for _, d := range entregas {
		porCiudad[d.CityID] = d
	}
	assert.Equal(t, vecino, porCiudad[2].Recipient)
	assert.Equal(t, h.playerID, porCiudad[2].Counterpart)
	assert.Equal(t, int64(300), porCiudad[2].Stocks[0].Delta)
	assert.Equal(t, economy.ResourceWood, porCiudad[2].Stocks[0].Resource)
	assert.Equal(t, int64(100), porCiudad[1].Stocks[0].Delta)
	assert.Equal(t, h.state.StockOf(2, economy.ResourceWood).Version, porCiudad[2].Stocks[0].Version)

	despawns := 0
	for _, m := range h.rec.byType(protocol.TypeEntityDespawn) {
		if m.Payload.(protocol.EntityDespawnPayload).Reason == protocol.DespawnRemoved {
			despawns++
		}
	}
	assert.Equal(t, 2, despawns)
	ultimas := h.caravanasPara(vecino)
	assert.Equal(t, string(caravan.StatusDelivered), ultimas[len(ultimas)-1].Status)
	assert.Empty(t, h.avisosPara(vecino), "Q11: la llegada no se avisa")
}

func madera300() economy.Cost { return madera(300) }
func piedra100() economy.Cost { return piedra(100) }

// RN-CARAVAN-003: nadie da órdenes a una caravana, ni su dueño.
func TestNadieDaOrdenesAUnaCaravana(t *testing.T) {
	h := newHarness(t)
	tratoEnCamino(h)
	destino := h.movimientoDe(caravanaDeMadera).Destination()

	h.send(simulation.MoveUnit{PlayerID: h.playerID, RequestID: uuid.NewString(), UnitID: caravanaDeMadera,
		Target: world.Tile{X: 5, Y: 5}})
	rechazos := h.rec.byType(protocol.TypeUnitMoveRejected)
	require.Len(t, rechazos, 1)
	assert.Equal(t, protocol.CodeUnitNotMovable, rechazos[0].Payload.(protocol.UnitMoveRejectedPayload).Code)

	h.send(simulation.CancelMovement{PlayerID: h.playerID, RequestID: uuid.NewString(), UnitID: caravanaDeMadera})
	h.send(simulation.GarrisonUnit{PlayerID: h.playerID, RequestID: uuid.NewString(), UnitID: caravanaDeMadera, CityID: 1})
	h.send(simulation.Gather{PlayerID: h.playerID, RequestID: uuid.NewString(), UnitID: caravanaDeMadera,
		Target: world.Tile{X: 5, Y: 5}})
	h.send(simulation.AttackUnit{PlayerID: h.playerID, RequestID: uuid.NewString(), UnitID: caravanaDeMadera,
		TargetID: caravanaDePiedra})
	assert.Equal(t, []string{protocol.CodeUnitNotMovable, protocol.CodeUnitNotMovable, protocol.CodeUnitNotMovable,
		protocol.CodeUnitNotMovable}, h.codigos())

	assert.Equal(t, destino, h.movimientoDe(caravanaDeMadera).Destination(), "sigue su ruta")
}

// RN-CARAVAN-002: sin camino entre las dos ciudades, ni se propone ni se
// acepta; con las ciudades más lejos que el tope del pathfinding, tampoco.
func TestSinRutaNoHayTrato(t *testing.T) {
	// Un río de punta a punta entre las dos ciudades.
	h := newHarnessConTerreno(t, func(terrain []byte) {
		for x := 0; x < 64; x++ {
			terrain[25*64+x] = byte(world.Water)
		}
	})
	vecino := conComercio(h, diplomacy.TypeTrade)
	h.almacen(1, 300, 0)
	h.almacen(2, 0, 100)
	h.ofrecer(h.playerID, vecino, economy.Costs{madera(300)}, economy.Costs{piedra(100)})
	assert.Equal(t, []string{protocol.CodePathNotFound}, h.codigos())
	_, hay := h.state.TradeOffer(primeraOferta)
	assert.False(t, hay)

	lejos := newHarnessConDeps(t, nil, func(d *simulation.Deps) { d.PathMaxDistance = 10 })
	otro := conComercio(lejos, diplomacy.TypeTrade)
	lejos.almacen(1, 300, 0)
	lejos.ofrecer(lejos.playerID, otro, economy.Costs{madera(300)}, economy.Costs{piedra(100)})
	assert.Equal(t, []string{protocol.CodePathTooLong}, lejos.codigos())
}

// RN-CARAVAN-005 y RN-CARAVAN-006, golden de la captura: un tercero intercepta
// la caravana de madera. El golpe mortal se la da con la vida entera y la carga
// intacta, y sigue hacia su ciudad; quien la mandó y quien la esperaba reciben
// el aviso, el captor no. Al llegar, la madera es del captor.
func TestUnTerceroCapturaLaCaravanaYSeLaLleva(t *testing.T) {
	h := newHarness(t)
	vecino := tratoEnCamino(h)
	tercero := conTercero(h)
	m := h.movimientoDe(caravanaDeMadera)
	junto := m.Path[6].Tile()
	ponerUnidad(h, asaltanteID, tercero, 3, unit.TypeWarrior, world.Tile{X: junto.X + 1, Y: junto.Y}, 60)

	h.send(simulation.AttackUnit{PlayerID: tercero, RequestID: uuid.NewString(), UnitID: asaltanteID,
		TargetID: caravanaDeMadera})
	require.Empty(t, h.codigos())
	require.True(t, h.tickHasta(1000, func() bool {
		c, ok := h.state.Caravan(caravanaDeMadera)
		return ok && c.ToCity == 3
	}), "el guerrero alcanza la caravana y la captura")

	c, _ := h.state.Caravan(caravanaDeMadera)
	assert.Equal(t, int64(2), c.OriginalToCity)
	assert.Equal(t, economy.Costs{madera300()}, c.Cargo, "INV-TRADE-005: la carga no cambia")
	u, ok := h.state.Unit(caravanaDeMadera)
	require.True(t, ok, "una caravana nunca queda DEAD")
	assert.Equal(t, tercero, u.PlayerID)
	require.NotNil(t, u.CityID)
	assert.Equal(t, int64(3), *u.CityID)
	assert.Equal(t, int32(80), u.HP, "con la vida entera")
	assert.Equal(t, depositoDe(h, 3), h.movimientoDe(caravanaDeMadera).Destination())
	_, peleando := h.state.Engaged(asaltanteID)
	assert.False(t, peleando, "el captor deja de atacarla: ya es suya")

	capturas := h.caravans.capturas()
	require.Len(t, capturas, 1)
	assert.Equal(t, h.playerID, capturas[0].PreviousOwner)
	assert.Equal(t, tercero, capturas[0].Captor)
	assert.Equal(t, vecino, capturas[0].OriginalRecipient)
	require.NotNil(t, capturas[0].Movement)
	assert.Equal(t, int64(3), capturas[0].CaptorCity)

	propios := h.avisosPara(h.playerID)
	require.Len(t, propios, 1)
	assert.Equal(t, "CARAVAN_CAPTURED", propios[0].Kind)
	assert.Equal(t, tercero.String(), propios[0].OtherPlayerID)
	assert.Equal(t, []protocol.ResourceAmount{{Resource: "WOOD", Amount: 300}}, propios[0].Cargo)
	ajenos := h.avisosPara(vecino)
	require.Len(t, ajenos, 1)
	assert.Equal(t, "CARAVAN_INTERCEPTED", ajenos[0].Kind)
	assert.Empty(t, h.avisosPara(tercero), "RN-LOG-001: al captor no se le cuenta su captura")
	vistas := h.caravanasPara(tercero)
	require.NotEmpty(t, vistas)
	assert.Equal(t, tercero.String(), vistas[len(vistas)-1].OwnerPlayerID)

	// Llega a la ciudad del captor y la madera es suya.
	h.hasta(h.movimientoDe(caravanaDeMadera).ArrivalTimeMs)
	assert.Equal(t, int64(300), h.state.StockOf(3, economy.ResourceWood).Amount)
	assert.Zero(t, h.state.StockOf(2, economy.ResourceWood).Amount)
	var alCaptor *simulation.CaravanDelivery
	for _, d := range h.caravans.entregas() {
		if d.CityID == 3 {
			d := d
			alCaptor = &d
		}
	}
	require.NotNil(t, alCaptor)
	assert.Equal(t, tercero, alCaptor.Recipient)
	assert.Equal(t, uuid.Nil, alCaptor.Counterpart, "lo que se llevó él no le cuenta nada")
}

// RN-CARAVAN-006: el destinatario original la recupera, y llega adonde iba.
func TestElDestinatarioRecuperaLaCaravana(t *testing.T) {
	h := newHarness(t)
	vecino := tratoEnCamino(h)
	tercero := conTercero(h)
	m := h.movimientoDe(caravanaDeMadera)
	junto := m.Path[6].Tile()
	ponerUnidad(h, asaltanteID, tercero, 3, unit.TypeWarrior, world.Tile{X: junto.X + 1, Y: junto.Y}, 60)
	h.send(simulation.AttackUnit{PlayerID: tercero, RequestID: uuid.NewString(), UnitID: asaltanteID,
		TargetID: caravanaDeMadera})
	require.True(t, h.tickHasta(1000, func() bool {
		c, ok := h.state.Caravan(caravanaDeMadera)
		return ok && c.ToCity == 3
	}))
	// El asaltante sigue a su lado y la escolta como guardia; el vecino manda dos
	// guerreros, que la recapturan antes de que el guardia mate a ninguno.
	at := h.sim.AuthoritativePosition(func() *unit.Unit { u, _ := h.state.Unit(caravanaDeMadera); return u }(), h.clk.NowMs())
	for i, id := range []int64{escoltaID, escoltaID + 1} {
		ponerUnidad(h, id, vecino, 2, unit.TypeWarrior, world.Tile{X: at.X + int32(i), Y: at.Y + 1}, 60)
		h.send(simulation.AttackUnit{PlayerID: vecino, RequestID: uuid.NewString(), UnitID: id,
			TargetID: caravanaDeMadera})
	}
	require.True(t, h.tickHasta(1500, func() bool {
		c, ok := h.state.Caravan(caravanaDeMadera)
		return ok && c.ToCity == 2
	}), "el vecino la recaptura")

	u, _ := h.state.Unit(caravanaDeMadera)
	assert.Equal(t, vecino, u.PlayerID)
	h.hasta(h.movimientoDe(caravanaDeMadera).ArrivalTimeMs)
	assert.Equal(t, int64(300), h.state.StockOf(2, economy.ResourceWood).Amount, "la madera llega adonde iba")
	assert.Len(t, h.caravans.capturas(), 2)
}

// RN-CARAVAN-005 (Q7): la protección offline del dueño no cubre a su caravana;
// a sus demás unidades, sí. Un pacto con el dueño la protege.
func TestLaProteccionDelDuenoNoCubreSuCaravana(t *testing.T) {
	h := newHarness(t)
	tratoEnCamino(h)
	tercero := conTercero(h)
	propia := h.ciudad()
	hasta := time.UnixMilli(h.clk.NowMs()).Add(time.Hour).UTC()
	propia.PresenceState, propia.ProtectionUntil = city.PresenceProtected, &hasta

	at := h.sim.AuthoritativePosition(func() *unit.Unit { u, _ := h.state.Unit(caravanaDeMadera); return u }(), h.clk.NowMs())
	ponerUnidad(h, asaltanteID, tercero, 3, unit.TypeWarrior, world.Tile{X: at.X + 3, Y: at.Y + 3}, 60)

	h.send(simulation.AttackUnit{PlayerID: tercero, RequestID: uuid.NewString(), UnitID: asaltanteID,
		TargetID: h.units[0].ID})
	assert.Equal(t, []string{protocol.CodeCityProtected}, h.codigos(), "a un aldeano protegido, no")
	h.send(simulation.AttackUnit{PlayerID: tercero, RequestID: uuid.NewString(), UnitID: asaltanteID,
		TargetID: caravanaDeMadera})
	assert.Equal(t, []string{protocol.CodeCityProtected}, h.codigos(), "a la caravana, sí")
	objetivo, ok := h.state.Engaged(asaltanteID)
	require.True(t, ok)
	assert.Equal(t, caravanaDeMadera, objetivo)

	// Con un pacto entre los dos, no.
	h.state.SetTreaties([]diplomacy.Treaty{vigente(9, h.playerID, tercero, diplomacy.TypeNonAggression)}, nil)
	h.send(simulation.AttackUnit{PlayerID: tercero, RequestID: uuid.NewString(), UnitID: asaltanteID,
		TargetID: caravanaDeMadera})
	assert.Equal(t, []string{protocol.CodeCityProtected, protocol.CodeInvalidTarget}, h.codigos())
}

// RN-CARAVAN-005, la escolta: los guerreros del dueño que están cerca
// responden solos al golpe sobre su caravana (RN-COMBAT-014).
func TestLaEscoltaRespondeAlAsaltoDeSuCaravana(t *testing.T) {
	h := newHarness(t)
	tratoEnCamino(h)
	tercero := conTercero(h)
	at := h.sim.AuthoritativePosition(func() *unit.Unit { u, _ := h.state.Unit(caravanaDeMadera); return u }(), h.clk.NowMs())
	ponerUnidad(h, asaltanteID, tercero, 3, unit.TypeWarrior, world.Tile{X: at.X + 1, Y: at.Y + 1}, 60)
	ponerUnidad(h, escoltaID, h.playerID, 1, unit.TypeWarrior, world.Tile{X: at.X + 2, Y: at.Y + 2}, 60)

	h.send(simulation.AttackUnit{PlayerID: tercero, RequestID: uuid.NewString(), UnitID: asaltanteID,
		TargetID: caravanaDeMadera})
	require.True(t, h.tickHasta(200, func() bool {
		objetivo, ok := h.state.Engaged(escoltaID)
		return ok && objetivo == asaltanteID
	}), "la escolta responde al primer golpe")
	_, auto := h.state.Responding(escoltaID)
	assert.True(t, auto)
}

// RN-CARAVAN-007: una caravana que llega y recibe el golpe mortal en el mismo
// tick se captura: el combate va antes que la entrega.
func TestLlegarYCaerEnElMismoTickEsUnaCaptura(t *testing.T) {
	h := newHarness(t)
	tratoEnCamino(h)
	tercero := conTercero(h)
	m := h.movimientoDe(caravanaDeMadera)
	destino := m.Destination()
	ponerUnidad(h, asaltanteID, tercero, 3, unit.TypeWarrior, world.Tile{X: destino.X + 1, Y: destino.Y}, 60)
	u, _ := h.state.Unit(caravanaDeMadera)
	u.HP = 8 // un golpe

	h.hasta(m.ArrivalTimeMs - 100)
	h.commands <- simulation.AttackUnit{PlayerID: tercero, RequestID: uuid.NewString(), UnitID: asaltanteID,
		TargetID: caravanaDeMadera}
	h.clk.AdvanceMs(100)
	h.loop.Step(h.clk.NowMs()) // el tick de la llegada

	c, ok := h.state.Caravan(caravanaDeMadera)
	require.True(t, ok, "no se entregó")
	assert.Equal(t, int64(3), c.ToCity)
	assert.Zero(t, h.state.StockOf(2, economy.ResourceWood).Amount)
	for _, d := range h.caravans.entregas() {
		assert.NotEqual(t, int64(2), d.CityID, "la madera no entró en la ciudad 2")
	}
}

// RN-CARAVAN-006: capturada sin camino a la ciudad del captor, la caravana se
// pierde con su carga, y el aviso lo dice.
func TestCapturadaSinRutaSePierde(t *testing.T) {
	// La ciudad del tercero, en (45,12), rodeada de agua.
	h := newHarnessConTerreno(t, func(terrain []byte) {
		for y := 5; y <= 19; y++ {
			for x := 38; x <= 52; x++ {
				if y == 5 || y == 19 || x == 38 || x == 52 {
					terrain[y*64+x] = byte(world.Water)
				}
			}
		}
	})
	vecino := tratoEnCamino(h)
	tercero := conTercero(h)
	at := h.sim.AuthoritativePosition(func() *unit.Unit { u, _ := h.state.Unit(caravanaDeMadera); return u }(), h.clk.NowMs())
	ponerUnidad(h, asaltanteID, tercero, 3, unit.TypeWarrior, world.Tile{X: at.X + 1, Y: at.Y + 1}, 60)
	h.send(simulation.AttackUnit{PlayerID: tercero, RequestID: uuid.NewString(), UnitID: asaltanteID,
		TargetID: caravanaDeMadera})
	require.True(t, h.tickHasta(1000, func() bool {
		_, ok := h.state.Caravan(caravanaDeMadera)
		return !ok
	}))
	_, sigue := h.state.Unit(caravanaDeMadera)
	assert.False(t, sigue)
	capturas := h.caravans.capturas()
	require.Len(t, capturas, 1)
	assert.Nil(t, capturas[0].Movement, "sin movimiento: se pierde")
	propios := h.avisosPara(h.playerID)
	require.Len(t, propios, 1)
	assert.Equal(t, "CARAVAN_LOST", propios[0].Kind)
	require.Len(t, h.avisosPara(vecino), 1)
	assert.Equal(t, "CARAVAN_INTERCEPTED", h.avisosPara(vecino)[0].Kind)

	// La otra sigue y llega.
	h.hasta(h.movimientoDe(caravanaDePiedra).ArrivalTimeMs)
	assert.Equal(t, int64(100), h.state.StockOf(1, economy.ResourceStone).Amount)
	assert.Zero(t, h.state.StockOf(2, economy.ResourceWood).Amount)
}

// RN-CARAVAN-008 (Q10): el fin del tratado no detiene a las caravanas.
func TestElFinDelTratadoNoDetieneLasCaravanas(t *testing.T) {
	h := newHarness(t)
	tratoEnCamino(h)
	h.state.SetTreaties(nil, nil)
	h.advance(2 * time.Second) // un barrido de tratados y de ofertas
	assert.Equal(t, 2, h.state.CaravanCount())
	h.hasta(h.movimientoDe(caravanaDeMadera).ArrivalTimeMs)
	assert.Equal(t, int64(300), h.state.StockOf(2, economy.ResourceWood).Amount)
}

// RN-CARAVAN-009: el snapshot lleva las caravanas, con su carga, a quien las
// manda o las espera; a un tercero, sólo la unidad.
func TestElSnapshotLlevaLasCaravanasASusImplicados(t *testing.T) {
	h := newHarness(t)
	vecino := tratoEnCamino(h)
	tercero := conTercero(h)
	chunks := h.state.World().ChunksInRadius(world.Tile{X: 10, Y: 10}, 2)

	for _, p := range []uuid.UUID{h.playerID, vecino} {
		snap := h.sim.BuildSnapshot(p, chunks, h.clk.NowMs(), false)
		require.Len(t, snap.Caravans, 2, "%s", p)
		assert.Equal(t, caravanaDeMadera, snap.Caravans[0].UnitID)
		assert.NotEmpty(t, snap.Caravans[0].Cargo)
	}
	snap := h.sim.BuildSnapshot(tercero, chunks, h.clk.NowMs(), false)
	assert.Empty(t, snap.Caravans)
	unidades := 0
	for _, u := range snap.Units {
		if u.UnitType == string(unit.TypeCaravan) {
			unidades++
		}
	}
	assert.Equal(t, 2, unidades, "la unidad se ve: lo que no se ve es la carga")
}

// INV-TRADE-006 y ADR-037 §4: una entrega descartada se reintenta en el
// volcado con una versión nueva; la RAM ya la había acreditado una sola vez.
func TestUnaEntregaDescartadaSeReintentaConVersionNueva(t *testing.T) {
	h := newHarness(t)
	tratoEnCamino(h)
	h.persister.permanent = true
	h.caravans.deliverErr = errBaseCaida
	h.hasta(h.movimientoDe(caravanaDeMadera).ArrivalTimeMs)
	assert.Equal(t, int64(300), h.state.StockOf(2, economy.ResourceWood).Amount)
	assert.Empty(t, h.caravans.entregas())

	h.caravans.mu.Lock()
	h.caravans.deliverErr = nil
	h.caravans.mu.Unlock()
	h.advance(6 * time.Second) // un volcado
	entregas := h.caravans.entregas()
	require.Len(t, entregas, 2)
	for _, d := range entregas {
		assert.Equal(t, h.state.StockOf(d.CityID, d.Stocks[0].Resource).Version, d.Stocks[0].Version)
	}
	assert.Equal(t, int64(300), h.state.StockOf(2, economy.ResourceWood).Amount, "la RAM no acredita otra vez")
}

// Recuperación (SPEC-CARAVANS §9 «recovery»): tras un reinicio, una caravana en
// camino sigue por su polilínea y llega en su instante absoluto; una que llegó
// con el servidor parado se entrega en el primer tick.
func TestTrasUnReinicioLasCaravanasLleganASuHora(t *testing.T) {
	h := newHarness(t)
	conVecinos(h, 1)
	origen, destino := depositoDe(h, 1), depositoDe(h, 2)
	// Hacia fuera de cada ciudad, donde no hay muralla: la de en camino se aleja
	// tres tiles de su depósito; la que llegó venía del tile de fuera del suyo.
	fuera := func(cityID int64, t world.Tile) world.Tile {
		c, _ := h.state.City(cityID)
		paso := func(a, b int32) int32 {
			switch {
			case a > b:
				return 1
			case a < b:
				return -1
			}
			return 0
		}
		return world.Tile{X: t.X + paso(t.X, c.CenterX), Y: t.Y + paso(t.Y, c.CenterY)}
	}
	var tiles []world.Tile
	for t := origen; len(tiles) < 4; t = fuera(1, t) {
		tiles = append(tiles, t)
	}
	ruta, err := movement.BuildTimedPath(tiles, h.state.World(), 1200)
	require.NoError(t, err)
	enCamino := movement.New(901, ruta, ruta.Destination(), h.clk.NowMs()-1000)
	enCamino.ID = 77
	antes := fuera(2, destino)
	rutaLlegada, err := movement.BuildTimedPath([]world.Tile{antes, destino}, h.state.World(), 1200)
	require.NoError(t, err)
	llegada := movement.New(902, rutaLlegada, destino, h.clk.NowMs()-60_000)
	llegada.ID = 78
	uno := int64(1)
	caravanas := []*unit.Unit{
		{ID: 901, PlayerID: h.playerID, CityID: &uno, Type: unit.TypeCaravan, X: origen.X, Y: origen.Y, HP: 80, MaxHP: 80, Status: unit.StatusMoving},
		{ID: 902, PlayerID: h.playerID, CityID: &uno, Type: unit.TypeCaravan, X: antes.X, Y: antes.Y, HP: 80, MaxHP: 80, Status: unit.StatusMoving},
	}
	simulation.Hydrate(h.state, caravanas, nil, []*movement.Movement{enCamino, llegada}, h.clk.NowMs(),
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	h.state.SetCaravans([]caravan.Caravan{
		{ID: 1, UnitID: 901, OfferID: 5, Sender: h.playerID, FromCity: 1, ToCity: 1, OriginalToCity: 1,
			Cargo: economy.Costs{madera(40)}, Status: caravan.StatusInTransit},
		{ID: 2, UnitID: 902, OfferID: 5, Sender: h.playerID, FromCity: 1, ToCity: 2, OriginalToCity: 2,
			Cargo: economy.Costs{madera(25)}, Status: caravan.StatusInTransit},
	})

	h.tick() // la que llegó con el servidor parado se entrega
	assert.Equal(t, int64(25), h.state.StockOf(2, economy.ResourceWood).Amount)
	_, sigue := h.state.Caravan(902)
	assert.False(t, sigue)

	// La otra, cuyo destino es la ciudad 1 pero su ruta termina antes del
	// depósito, llega al final de su polilínea y, parada fuera del depósito,
	// busca el camino que le falta.
	h.hasta(enCamino.ArrivalTimeMs + 100)
	require.True(t, h.tickHasta(200, func() bool { _, ok := h.state.Caravan(901); return !ok }))
	assert.Equal(t, int64(40), h.state.StockOf(1, economy.ResourceWood).Amount)
}
