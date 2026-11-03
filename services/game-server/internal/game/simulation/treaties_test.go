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
	"github.com/empires-online/empires-online/services/game-server/internal/domain/diplomacy"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// Tests de la diplomacia (SPEC-DIPLOMACY §9). Mundo de newHarness: el jugador
// del arnés con su ciudad en (10,10) y un vecino de conVecinos en (20,40).

// fakeTreaties hace de base: da ids a las ofertas y anota cada escritura.
type fakeTreaties struct {
	mu     sync.Mutex
	nextID int64
	writes []simulation.TreatyWrite
	err    error
}

func (f *fakeTreaties) ApplyTreaty(_ context.Context, w simulation.TreatyWrite) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return 0, f.err
	}
	f.writes = append(f.writes, w)
	if w.Insert {
		f.nextID++
		return f.nextID, nil
	}
	return w.Treaty.ID, nil
}

func (f *fakeTreaties) events() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.writes))
	for _, w := range f.writes {
		out = append(out, w.Event)
	}
	return out
}

func (h *harness) proponer(de, a uuid.UUID, tipo diplomacy.Type) {
	h.t.Helper()
	h.send(simulation.ProposeTreaty{PlayerID: de, RequestID: uuid.NewString(), Target: a, Type: tipo})
	h.tick() // la confirmación vuelve por el buzón
}

func (h *harness) responder(quien uuid.UUID, id int64, acepta bool) {
	h.t.Helper()
	h.send(simulation.RespondTreaty{PlayerID: quien, RequestID: uuid.NewString(), TreatyID: id, Accept: acepta})
	h.tick()
}

func (h *harness) cancelar(quien uuid.UUID, id int64) {
	h.t.Helper()
	h.send(simulation.CancelTreaty{PlayerID: quien, RequestID: uuid.NewString(), TreatyID: id})
	h.tick()
}

// unicoTratado devuelve el único tratado abierto entre dos jugadores.
func (h *harness) unicoTratado(x, y uuid.UUID) diplomacy.Treaty {
	h.t.Helper()
	ts := h.state.TreatiesBetween(x, y)
	require.Len(h.t, ts, 1)
	return ts[0]
}

// actualizacionesPara son los treaty.update recibidos por un jugador.
func (h *harness) actualizacionesPara(p uuid.UUID) []protocol.TreatyView {
	var out []protocol.TreatyView
	for _, m := range h.rec.byType(protocol.TypeTreatyUpdate) {
		if m.Kind == "player" && m.PlayerID == p {
			out = append(out, m.Payload.(protocol.TreatyUpdatePayload).Treaty)
		}
	}
	return out
}

// Golden «alianza» (RN-DIPLO-001 a RN-DIPLO-005, INV-GARR-008): una oferta, su
// aceptación, la paz, la ruptura con 15 minutos de preaviso en los que la paz
// sigue, y al vencer el fin del tratado y la expulsión de la unidad guarnecida
// en la ciudad del aliado.
func TestGoldenAlianzaPropuestaAceptacionRupturaYExpulsion(t *testing.T) {
	h := newHarness(t)
	vecino := conVecinos(h, 1)[0]
	anfitriona, _ := h.state.City(2)

	h.proponer(h.playerID, vecino, diplomacy.TypeAlliance)
	require.Empty(t, h.codigos())
	oferta := h.unicoTratado(h.playerID, vecino)
	assert.Equal(t, diplomacy.StatusProposed, oferta.Status)
	assert.True(t, oferta.AllowsGarrison, "Q1: la alianza deja guarnecer")
	require.NotEmpty(t, h.actualizacionesPara(vecino), "el otro firmante recibe la oferta")
	assert.False(t, h.actualizacionesPara(vecino)[0].ProposedByMe)
	assert.True(t, h.actualizacionesPara(h.playerID)[0].ProposedByMe)

	h.responder(vecino, oferta.ID, true)
	vigente := h.unicoTratado(h.playerID, vecino)
	assert.Equal(t, diplomacy.StatusActive, vigente.Status)
	assert.True(t, h.state.AtPeace(h.playerID, vecino, h.clk.Now()))

	// Una unidad propia guarnecida en la ciudad del aliado.
	u := ponerUnidad(h, guerreroID, h.playerID, h.cityID, unit.TypeWarrior, world.Tile{X: 20, Y: 40}, 60)
	u.Status = unit.StatusGarrisoned
	h.state.SetGarrisons(map[int64]int64{u.ID: anfitriona.ID})

	h.cancelar(vecino, vigente.ID)
	enPreaviso := h.unicoTratado(h.playerID, vecino)
	require.NotNil(t, enPreaviso.BreakEffectiveAt)
	assert.Equal(t, h.clk.Now().Add(15*time.Minute).UnixMilli()-100, enPreaviso.BreakEffectiveAt.UnixMilli(),
		"Q4: 15 minutos desde la orden (un tick antes de ahora)")

	h.clk.AdvanceMs(14 * 60 * 1000)
	h.tick()
	assert.True(t, h.state.AtPeace(h.playerID, vecino, h.clk.Now()), "en el preaviso, la paz sigue")
	assert.Equal(t, unit.StatusGarrisoned, u.Status, "y la guarnición sigue dentro")

	h.clk.AdvanceMs(60 * 1000)
	h.tick()
	assert.Empty(t, h.state.TreatiesBetween(h.playerID, vecino), "el tratado terminó")
	assert.False(t, h.state.AtPeace(h.playerID, vecino, h.clk.Now()))
	assert.Equal(t, unit.StatusIdle, u.Status, "INV-GARR-008: la unidad sale")
	_, sigue := h.state.GarrisonCity(u.ID)
	assert.False(t, sigue)
	assert.Equal(t, []string{"TreatyProposed", "TreatyAccepted", "TreatyBreakAnnounced", "TreatyBroken"}, h.treaties.events())
	ultima := h.actualizacionesPara(h.playerID)
	assert.Equal(t, string(diplomacy.StatusBroken), ultima[len(ultima)-1].Status)
}

// §5: cada rechazo con INVALID_TARGET, sin escribir nada. INV-DIPLO-001: ni
// quien propuso responde a su oferta ni el otro la retira.
func TestRechazosDeLaDiplomacia(t *testing.T) {
	h := newHarness(t)
	vecinos := conVecinos(h, 2)
	vecino, tercero := vecinos[0], vecinos[1]
	h.proponer(h.playerID, vecino, diplomacy.TypeNonAggression)
	oferta := h.unicoTratado(h.playerID, vecino)
	escritas := len(h.treaties.events())
	h.rec.reset()

	casos := []struct {
		nombre string
		cmd    simulation.Command
	}{
		{"conmigo mismo", simulation.ProposeTreaty{PlayerID: h.playerID, Target: h.playerID, Type: diplomacy.TypeAlliance}},
		{"TRADE no se propone", simulation.ProposeTreaty{PlayerID: h.playerID, Target: tercero, Type: diplomacy.TypeTrade}},
		{"jugador inexistente", simulation.ProposeTreaty{PlayerID: h.playerID, Target: uuid.New(), Type: diplomacy.TypeAlliance}},
		{"ya hay una oferta pendiente", simulation.ProposeTreaty{PlayerID: vecino, Target: h.playerID, Type: diplomacy.TypeAlliance}},
		{"responder a la oferta propia", simulation.RespondTreaty{PlayerID: h.playerID, TreatyID: oferta.ID, Accept: true}},
		{"responder sin ser parte", simulation.RespondTreaty{PlayerID: tercero, TreatyID: oferta.ID, Accept: true}},
		{"retirar la oferta ajena", simulation.CancelTreaty{PlayerID: vecino, TreatyID: oferta.ID}},
		{"tratado inexistente", simulation.CancelTreaty{PlayerID: h.playerID, TreatyID: 999}},
	}
	for _, c := range casos {
		h.rec.reset()
		h.send(c.cmd)
		assert.Equal(t, []string{protocol.CodeInvalidTarget}, h.codigos(), c.nombre)
	}
	assert.Len(t, h.treaties.events(), escritas, "ningún rechazo escribe")
	assert.Equal(t, diplomacy.StatusProposed, h.unicoTratado(h.playerID, vecino).Status)
}

// RN-DIPLO-003: quien propuso retira la oferta; queda WITHDRAWN y sale de la RAM.
func TestQuienProponeRetiraLaOferta(t *testing.T) {
	h := newHarness(t)
	vecino := conVecinos(h, 1)[0]
	h.proponer(h.playerID, vecino, diplomacy.TypeAlliance)
	oferta := h.unicoTratado(h.playerID, vecino)

	h.cancelar(h.playerID, oferta.ID)
	assert.Empty(t, h.state.TreatiesBetween(h.playerID, vecino))
	ultima := h.actualizacionesPara(vecino)
	assert.Equal(t, string(diplomacy.StatusWithdrawn), ultima[len(ultima)-1].Status)
}

// RN-DIPLO-008: tras un rechazo, quien propuso espera 10 minutos para insistir.
func TestTrasUnRechazoHayQueEsperarParaInsistir(t *testing.T) {
	h := newHarness(t)
	vecino := conVecinos(h, 1)[0]
	h.proponer(h.playerID, vecino, diplomacy.TypeAlliance)
	h.responder(vecino, h.unicoTratado(h.playerID, vecino).ID, false)
	assert.Empty(t, h.state.TreatiesBetween(h.playerID, vecino))

	h.rec.reset()
	h.proponer(h.playerID, vecino, diplomacy.TypeAlliance)
	assert.Equal(t, []string{protocol.CodeInvalidTarget}, h.codigos(), "antes de 10 minutos, no")
	h.rec.reset()
	h.proponer(vecino, h.playerID, diplomacy.TypeAlliance)
	assert.Empty(t, h.codigos(), "el otro sí puede proponer")
	h.cancelar(vecino, h.unicoTratado(h.playerID, vecino).ID)

	h.clk.AdvanceMs(10 * 60 * 1000)
	h.rec.reset()
	h.proponer(h.playerID, vecino, diplomacy.TypeAlliance)
	assert.Empty(t, h.codigos(), "pasados 10 minutos, sí")
}

// RN-DIPLO-007: una oferta sin respuesta caduca a las 24 horas.
func TestUnaOfertaCaducaALas24Horas(t *testing.T) {
	h := newHarness(t)
	vecino := conVecinos(h, 1)[0]
	h.proponer(h.playerID, vecino, diplomacy.TypeNonAggression)

	h.clk.AdvanceMs((24*60 - 1) * 60 * 1000)
	h.tick()
	require.Len(t, h.state.TreatiesBetween(h.playerID, vecino), 1)
	h.clk.AdvanceMs(60 * 1000)
	h.tick()
	assert.Empty(t, h.state.TreatiesBetween(h.playerID, vecino))
	assert.Equal(t, "TreatyExpired", h.treaties.events()[len(h.treaties.events())-1])
}

// §4.1: con un pacto vigente se propone una alianza; aceptarla cierra el pacto
// en la misma escritura. INV-DIPLO-002: nunca dos vigentes.
func TestUnaAlianzaSustituyeAlPactoVigente(t *testing.T) {
	h := newHarness(t)
	vecino := conVecinos(h, 1)[0]
	h.proponer(h.playerID, vecino, diplomacy.TypeNonAggression)
	h.responder(vecino, h.unicoTratado(h.playerID, vecino).ID, true)
	pacto := h.unicoTratado(h.playerID, vecino)

	h.proponer(vecino, h.playerID, diplomacy.TypeAlliance)
	require.Len(t, h.state.TreatiesBetween(h.playerID, vecino), 2, "un vigente y una oferta")
	var alianza diplomacy.Treaty
	for _, tr := range h.state.TreatiesBetween(h.playerID, vecino) {
		if tr.Status == diplomacy.StatusProposed {
			alianza = tr
		}
	}
	h.responder(h.playerID, alianza.ID, true)

	vigente := h.unicoTratado(h.playerID, vecino)
	assert.Equal(t, diplomacy.TypeAlliance, vigente.Type)
	ultima := h.treaties.writes[len(h.treaties.writes)-1]
	require.NotNil(t, ultima.Supersede)
	assert.Equal(t, pacto.ID, ultima.Supersede.ID)
}

// RN-DIPLO-006: un pacto que nace a mitad de un saqueo lo termina en el tick
// siguiente.
func TestUnPactoAMitadDeUnSaqueoLoTermina(t *testing.T) {
	h := newHarness(t)
	enemigo, ciudad, _ := conSaqueo(h, 500, world.Tile{X: 20, Y: 38})
	h.saquear(guerreroID, ciudad.ID)
	h.tick()
	_, saquea := h.state.Pillaging(guerreroID)
	require.True(t, saquea)

	h.proponer(enemigo, h.playerID, diplomacy.TypeNonAggression)
	h.responder(h.playerID, h.unicoTratado(h.playerID, enemigo).ID, true)
	h.tick()
	_, saquea = h.state.Pillaging(guerreroID)
	assert.False(t, saquea)
}

// ADR-027 §5 y Q8: el snapshot trae los tratados de quien lo recibe, y sólo los
// suyos.
func TestElSnapshotTraeSoloLosTratadosDeQuienLoRecibe(t *testing.T) {
	h := newHarness(t)
	vecinos := conVecinos(h, 2)
	h.proponer(h.playerID, vecinos[0], diplomacy.TypeAlliance)

	propio := h.sim.BuildSnapshot(h.playerID, nil, h.clk.NowMs(), false)
	require.Len(t, propio.Treaties, 1)
	assert.Equal(t, vecinos[0].String(), propio.Treaties[0].OtherPlayerID)
	require.NotNil(t, propio.Treaties[0].OtherCityName)
	ajeno := h.sim.BuildSnapshot(vecinos[1], nil, h.clk.NowMs(), false)
	assert.Empty(t, ajeno.Treaties)
}

// Nivel «recovery»: un preaviso que venció con el servidor parado se aplica en
// el primer barrido, y la paz ya no vale desde su instante.
func TestUnPreavisoVencidoDuranteLaCaidaSeAplicaAlArrancar(t *testing.T) {
	h := newHarness(t)
	vecino := conVecinos(h, 1)[0]
	a, b := diplomacy.CanonicalPair(h.playerID, vecino)
	antes := h.clk.Now().Add(-time.Minute)
	aceptado := h.clk.Now().Add(-time.Hour)
	h.state.SetTreaties([]diplomacy.Treaty{{ID: 7, PlayerA: a, PlayerB: b, Type: diplomacy.TypeAlliance,
		Status: diplomacy.StatusActive, AllowsGarrison: true, AcceptedAt: &aceptado, BreakEffectiveAt: &antes}}, nil)
	assert.False(t, h.state.AtPeace(h.playerID, vecino, h.clk.Now()), "por fecha, ya no hay paz")

	h.clk.AdvanceMs(1000)
	h.tick()
	assert.Empty(t, h.state.TreatiesBetween(h.playerID, vecino))
	assert.Equal(t, []string{"TreatyBroken"}, h.treaties.events())
}

// ADR-027 §2: una orden que la base no admite se rechaza y libera el par.
func TestUnaOrdenQueLaBaseRechazaLiberaElPar(t *testing.T) {
	h := newHarness(t)
	vecino := conVecinos(h, 1)[0]
	h.treaties.err = simulation.ErrTreatyConflict
	h.proponer(h.playerID, vecino, diplomacy.TypeAlliance)
	assert.Equal(t, []string{protocol.CodeInvalidTarget}, h.codigos())
	assert.Empty(t, h.state.TreatiesBetween(h.playerID, vecino))

	h.treaties.err = nil
	h.rec.reset()
	h.proponer(h.playerID, vecino, diplomacy.TypeAlliance)
	assert.Empty(t, h.codigos(), "el par quedó libre")
}

// RN-DIPLO-009: se propone a un jugador protegido; la diplomacia no es hostil.
func TestSePuedeProponerAUnJugadorProtegido(t *testing.T) {
	h := newHarness(t)
	vecino := conVecinos(h, 1)[0]
	c, _ := h.state.City(2)
	c.PresenceState = city.PresenceProtected
	h.proponer(h.playerID, vecino, diplomacy.TypeNonAggression)
	assert.Empty(t, h.codigos())
	assert.Len(t, h.state.TreatiesBetween(h.playerID, vecino), 1)
}

// Regresión (2026-11-03, la CI de 800a269): SetTreaties guardaba el par tal como
// llegaba y AtPeace lo busca en orden canónico, así que un tratado con
// PlayerA > PlayerB no daba paz. El jugador del arnés tiene un UUID fijo que
// empieza por 1111…, así que TestRechazosDelSaqueo y TestRechazosDelAtaque sólo
// fallaban cuando el UUID aleatorio del enemigo salía menor: unas 6 de cada 100
// ejecuciones. Aquí, con UUID fijos en el orden que lo rompía.
func TestUnTratadoConElParSinOrdenarDaPazEnLasDosDirecciones(t *testing.T) {
	h := newHarness(t)
	mayor := uuid.MustParse("ffffffff-ffff-4fff-8fff-ffffffffffff")
	menor := uuid.MustParse("00000000-0000-4000-8000-000000000001")
	h.state.SetPeaceTreaties([]diplomacy.Treaty{{ID: 1, PlayerA: mayor, PlayerB: menor,
		Type: diplomacy.TypeNonAggression, Status: diplomacy.StatusActive}})

	assert.True(t, h.state.AtPeace(mayor, menor, h.clk.Now()))
	assert.True(t, h.state.AtPeace(menor, mayor, h.clk.Now()))
	require.Len(t, h.state.TreatiesBetween(menor, mayor), 1)
	tr := h.state.TreatiesBetween(menor, mayor)[0]
	assert.Equal(t, menor, tr.PlayerA, "se guarda en orden canónico")
}
