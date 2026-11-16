package simulation_test

import (
	"testing"

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

// Tests de los territorios de clan (SPEC-CLAN-TERRITORIES §9, ADR-041). Mundo
// de conCuatroTerritorios: 1 «Norte» con la ciudad del arnés en (10,10), 2
// «Este» (32-63, 0-31), 3 «Sur» con las ciudades de conVecinos en (20,40) y
// (28,40), 4 «Sureste» (32-63, 32-63).

// territorioDelClan pone el territorio tid a nombre del clan, como si lo
// hubiera ganado antes.
func territorioDelClan(h *harness, tid, clanID int64) {
	h.t.Helper()
	id, at := territory.ClanOwnerID(clanID), h.clk.Now()
	h.state.ApplyTerritoryControl(territory.Control{
		TerritoryID: tid, OwnerType: territory.OwnerClan, OwnerID: &id, CapturedAt: &at, Version: 1,
	})
}

// reclamarComo da unit.claim en nombre de p.
func (h *harness) reclamarComo(p uuid.UUID, unitID int64, forClan bool) {
	h.t.Helper()
	h.send(simulation.ClaimTerritory{PlayerID: p, RequestID: uuid.NewString(), UnitID: unitID, ForClan: forClan})
}

// pactoEntre firma un pacto de no agresión vigente entre a y b.
func pactoEntre(h *harness, a, b uuid.UUID) {
	h.state.SetPeaceTreaties([]diplomacy.Treaty{{ID: 1, PlayerA: a, PlayerB: b,
		Type: diplomacy.TypeNonAggression, Status: diplomacy.StatusActive}})
}

// Golden «territorio de clan» (RN-CLANTERR-001 a 007, ADR-041 §3 y §5): un
// miembro reclama uno libre para su clan y, a los 300 s, es del clan, sin bono
// para nadie y sin avisos; los dos miembros reciben su lista de territorios. Un
// tercero se lo quita a los 600 s: cada miembro recibe el aviso de que lo
// reclaman y de que lo pierden, con la sigla, y el tercero, uno solo de que lo
// gana.
func TestGoldenTerritorioDeClan(t *testing.T) {
	h, _ := conClanes(t, nil)
	vecinos := conVecinos(h, 2)
	miembro, tercero := vecinos[0], vecinos[1]
	conCuatroTerritorios(h, map[int64]uuid.UUID{1: h.playerID, 3: miembro})
	clanID := h.clanConMiembros(miembro)
	g := ponerUnidad(h, guerreroID, h.playerID, h.cityID, unit.TypeWarrior, world.Tile{X: 40, Y: 10}, 60)

	h.reclamarComo(h.playerID, guerreroID, true)
	require.Empty(t, h.codigos())
	h.tick()
	c, confirmada, _ := h.state.ActiveClaim(2)
	require.True(t, confirmada)
	assert.Equal(t, clanID, c.ClanID)
	assert.Equal(t, int64(300_000), c.CompletesAtMs-c.StartedAtMs, "uno libre, el plazo de E12")
	v := h.vistaDe(2)
	require.NotNil(t, v.Claim)
	require.NotNil(t, v.Claim.Clan, "la reclamación dice para qué clan")
	assert.Equal(t, protocol.CityClanView{ID: clanID, Tag: "LOB"}, *v.Claim.Clan)
	require.Len(t, h.conquest.starts, 1)
	assert.Equal(t, clanID, h.conquest.starts[0].Claim.ClanID)
	assert.Zero(t, h.conquest.starts[0].OwnerClanID)

	h.rec.reset()
	require.True(t, h.tickHasta(3100, func() bool { return h.vistaDe(2).OwnerType == string(territory.OwnerClan) }))
	v = h.vistaDe(2)
	assert.Equal(t, territory.ClanOwnerID(clanID), dueñoDe(v))
	require.NotNil(t, v.OwnerClan)
	assert.Equal(t, protocol.TerritoryOwnerClanView{ID: clanID, Name: "Los Lobos", Tag: "LOB"}, *v.OwnerClan)
	assert.Nil(t, v.OwnerFaction, "un clan no tiene bando")
	assert.Equal(t, unit.StatusIdle, g.Status)
	require.Len(t, h.conquest.captures, 1)
	assert.Equal(t, clanID, h.conquest.captures[0].NewClanID)
	assert.Equal(t, int32(0), h.conquest.captures[0].NewBonus, "Q3: el territorio del clan no da población")
	assert.Equal(t, int32(20), h.ciudad().PopulationLimit)
	assert.Equal(t, []int64{2}, h.state.ClanTerritoryIDs(clanID))
	for _, p := range []uuid.UUID{h.playerID, miembro} {
		ups := h.clanUpdatesPara(p)
		require.NotEmpty(t, ups, "cada miembro recibe su clan con la lista nueva")
		assert.Equal(t, []protocol.ClanTerritoryView{{ID: 2, Name: "Este", X: 47, Y: 15}}, ups[len(ups)-1].Clan.Territories)
	}
	assert.Empty(t, avisosDe(h), "uno libre no avisa a nadie")

	// Un tercero se lo quita (RN-CLANTERR-004 y 006).
	h.rec.reset()
	ponerUnidad(h, enemigoID, tercero, 3, unit.TypeWarrior, world.Tile{X: 60, Y: 28}, 60)
	h.reclamarComo(tercero, enemigoID, false)
	require.Empty(t, h.codigos())
	h.tick()
	c, confirmada, _ = h.state.ActiveClaim(2)
	require.True(t, confirmada)
	assert.Equal(t, int64(600_000), c.CompletesAtMs-c.StartedAtMs, "con dueño, 10 minutos")
	require.Len(t, h.conquest.starts, 2)
	assert.Equal(t, clanID, h.conquest.starts[1].OwnerClanID)
	assert.Equal(t, "LOB", h.conquest.starts[1].OwnerClanTag)
	reclamado := avisosDeTipo(h, eventlog.KindTerritoryClaimed)
	require.Len(t, reclamado, 2, "a cada miembro, a nadie más")
	assert.ElementsMatch(t, []uuid.UUID{h.playerID, miembro}, []uuid.UUID{reclamado[0].to, reclamado[1].to})
	for _, a := range reclamado {
		assert.Equal(t, "LOB", a.p.ClanTag)
		assert.Equal(t, tercero.String(), a.p.OtherPlayerID)
	}

	require.True(t, h.tickHasta(6100, func() bool { return dueñoDe(h.vistaDe(2)) == tercero.String() }))
	require.Len(t, h.conquest.captures, 2)
	cap := h.conquest.captures[1]
	assert.Equal(t, clanID, cap.PrevClanID)
	assert.Equal(t, uuid.Nil, cap.PrevOwner)
	assert.Equal(t, []uuid.UUID{h.playerID, miembro}, cap.PrevClanMembers)
	assert.Equal(t, int32(5), cap.NewBonus, "para sí, el bono de E12")
	assert.Empty(t, h.state.ClanTerritoryIDs(clanID))
	assert.Nil(t, h.vistaDe(2).OwnerClan)
	perdido := avisosDeTipo(h, eventlog.KindTerritoryLost)
	require.Len(t, perdido, 2)
	for _, a := range perdido {
		assert.Equal(t, "LOB", a.p.ClanTag)
	}
	ganado := avisosDeTipo(h, eventlog.KindTerritoryTaken)
	require.Len(t, ganado, 1, "el que lo gana, una sola vez")
	assert.Equal(t, tercero, ganado[0].to)
	assert.Empty(t, ganado[0].p.ClanTag, "lo ganó para sí")
	assert.Equal(t, h.playerID.String(), ganado[0].p.OtherPlayerID, "el miembro más antiguo del clan que lo pierde")
}

// §5, RN-CLANTERR-001 y RN-CLANTERR-004: cada rechazo con su código y su motivo,
// sin reclamación.
func TestRechazosConUnClanDePorMedio(t *testing.T) {
	type entorno struct {
		h                *harness
		miembro, tercero uuid.UUID
		ciudadM, ciudadL *city.City
		clanID           int64
		guerreroMiembro  int64
		guerreroTercero  int64
		guerreroDelLider int64
	}
	preparar := func(t *testing.T) entorno {
		h, _ := conClanes(t, nil)
		vecinos := conVecinos(h, 2)
		e := entorno{h: h, miembro: vecinos[0], tercero: vecinos[1]}
		conCuatroTerritorios(h, map[int64]uuid.UUID{1: h.playerID, 3: e.miembro, 4: h.playerID})
		e.clanID = h.clanConMiembros(e.miembro)
		territorioDelClan(h, 2, e.clanID)
		e.ciudadL = h.ciudad()
		e.ciudadM, _ = h.state.City(2)
		e.guerreroDelLider, e.guerreroMiembro, e.guerreroTercero = guerreroID, 300, enemigoID
		ponerUnidad(h, guerreroID, h.playerID, h.cityID, unit.TypeWarrior, world.Tile{X: 40, Y: 40}, 60)
		ponerUnidad(h, 300, e.miembro, 2, unit.TypeWarrior, world.Tile{X: 50, Y: 5}, 60)
		ponerUnidad(h, enemigoID, e.tercero, 3, unit.TypeWarrior, world.Tile{X: 40, Y: 10}, 60)
		return e
	}
	casos := []struct {
		nombre string
		hacer  func(e entorno)
		codigo string
		motivo string
	}{
		{"para el clan, sin clan", func(e entorno) {
			e.h.reclamarComo(e.tercero, e.guerreroTercero, true)
		}, protocol.CodeInvalidTarget, "no estás en ningún clan"},
		{"el del propio clan", func(e entorno) {
			e.h.reclamarComo(e.miembro, e.guerreroMiembro, false)
		}, protocol.CodeInvalidTarget, territory.ErrClanTerritory.Error()},
		{"el suyo, para el clan", func(e entorno) {
			e.h.reclamarComo(e.h.playerID, e.guerreroDelLider, true)
		}, protocol.CodeInvalidTarget, territory.ErrOwnTerritory.Error()},
		{"un pacto con un miembro", func(e entorno) {
			pactoEntre(e.h, e.tercero, e.miembro)
			e.h.reclamarComo(e.tercero, e.guerreroTercero, false)
		}, protocol.CodeInvalidTarget, "un tratado de paz con un miembro de ese clan impide reclamarlo"},
		{"todos los miembros protegidos", func(e entorno) {
			e.ciudadL.PresenceState = city.PresenceProtected
			e.ciudadM.PresenceState = city.PresenceProtected
			e.h.reclamarComo(e.tercero, e.guerreroTercero, false)
		}, protocol.CodeCityProtected, "todos los miembros del clan dueño están protegidos"},
		{"la paz residual de quien acaba de salir", func(e entorno) {
			e.h.send(simulation.LeaveClan{PlayerID: e.miembro, RequestID: uuid.NewString()})
			e.h.tick()
			e.h.reclamarComo(e.miembro, e.guerreroMiembro, false)
		}, protocol.CodeInvalidTarget, "sois del mismo clan"},
	}
	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			e := preparar(t)
			c.hacer(e)
			assert.Equal(t, []string{c.codigo}, e.h.codigos())
			assert.Equal(t, c.motivo, e.h.ultimoMotivo())
			_, _, hay := e.h.state.ActiveClaim(2)
			_, _, hay4 := e.h.state.ActiveClaim(4)
			assert.False(t, hay || hay4)
		})
	}

	t.Run("con un solo miembro sin proteger, se puede", func(t *testing.T) {
		e := preparar(t)
		e.ciudadL.PresenceState = city.PresenceProtected
		e.h.reclamarComo(e.tercero, e.guerreroTercero, false)
		assert.Empty(t, e.h.codigos())
		_, _, hay := e.h.state.ActiveClaim(2)
		assert.True(t, hay)
	})
}

// RN-CLANTERR-008 e INV-CLAN-004: una reclamación para el clan termina sin
// captura en el tick en que quien la sostiene deja el clan; y la de un tercero
// sobre la tierra de un clan, cuando firma la paz con un miembro o cuando todos
// pasan a estar protegidos (RN-CLANTERR-004).
func TestQueRompeUnaReclamacionConUnClan(t *testing.T) {
	t.Run("para el clan", func(t *testing.T) {
		for _, c := range []struct {
			nombre string
			salir  func(h *harness, miembro uuid.UUID)
		}{
			{"quien reclama sale", func(h *harness, miembro uuid.UUID) {
				h.send(simulation.LeaveClan{PlayerID: miembro, RequestID: uuid.NewString()})
			}},
			{"a quien reclama lo expulsan", func(h *harness, miembro uuid.UUID) {
				h.send(simulation.ExpelFromClan{PlayerID: h.playerID, RequestID: uuid.NewString(), TargetID: miembro})
			}},
		} {
			t.Run(c.nombre, func(t *testing.T) {
				h, _ := conClanes(t, nil)
				miembro := conVecinos(h, 1)[0]
				conCuatroTerritorios(h, map[int64]uuid.UUID{1: h.playerID, 3: miembro})
				h.clanConMiembros(miembro)
				g := ponerUnidad(h, 300, miembro, 2, unit.TypeWarrior, world.Tile{X: 40, Y: 10}, 60)
				h.reclamarComo(miembro, 300, true)
				h.tick()
				_, confirmada, _ := h.state.ActiveClaim(2)
				require.True(t, confirmada)

				c.salir(h, miembro)
				_, _, sigue := h.state.ActiveClaim(2)
				assert.False(t, sigue, "en el mismo tick de la salida")
				assert.Equal(t, []string{simulation.ClaimEndClan}, h.conquest.endReasons())
				assert.Equal(t, unit.StatusIdle, g.Status)
				assert.Equal(t, string(territory.OwnerNone), h.vistaDe(2).OwnerType, "sin captura")
			})
		}
	})

	t.Run("sobre la tierra de un clan", func(t *testing.T) {
		for _, c := range []struct {
			nombre string
			romper func(h *harness, miembro, tercero uuid.UUID)
		}{
			{"firma un pacto con un miembro", func(h *harness, miembro, tercero uuid.UUID) {
				pactoEntre(h, tercero, miembro)
			}},
			{"todos los miembros quedan protegidos", func(h *harness, _, _ uuid.UUID) {
				c2, _ := h.state.City(2)
				c2.PresenceState = city.PresenceProtected
				h.ciudad().PresenceState = city.PresenceProtected
			}},
		} {
			t.Run(c.nombre, func(t *testing.T) {
				h, _ := conClanes(t, nil)
				vecinos := conVecinos(h, 2)
				miembro, tercero := vecinos[0], vecinos[1]
				conCuatroTerritorios(h, map[int64]uuid.UUID{1: h.playerID, 3: miembro})
				clanID := h.clanConMiembros(miembro)
				territorioDelClan(h, 2, clanID)
				ponerUnidad(h, enemigoID, tercero, 3, unit.TypeWarrior, world.Tile{X: 40, Y: 10}, 60)
				h.reclamarComo(tercero, enemigoID, false)
				h.tick()
				_, confirmada, _ := h.state.ActiveClaim(2)
				require.True(t, confirmada)

				c.romper(h, miembro, tercero)
				h.tick()
				_, _, sigue := h.state.ActiveClaim(2)
				assert.False(t, sigue)
				assert.Equal(t, []string{simulation.ClaimEndBlocked}, h.conquest.endReasons())
				assert.Equal(t, territory.ClanOwnerID(clanID), dueñoDe(h.vistaDe(2)))
			})
		}
	})
}

// RN-CLANTERR-005: al empezar la reclamación de la tierra de un clan, responden
// los guerreros parados de cualquier miembro, no sólo los del líder.
func TestLosGuerrerosDeCualquierMiembroDefienden(t *testing.T) {
	h, _ := conClanes(t, nil)
	vecinos := conVecinos(h, 2)
	miembro, tercero := vecinos[0], vecinos[1]
	conCuatroTerritorios(h, map[int64]uuid.UUID{1: h.playerID, 3: miembro})
	clanID := h.clanConMiembros(miembro)
	territorioDelClan(h, 2, clanID)
	ponerUnidad(h, enemigoID, tercero, 3, unit.TypeWarrior, world.Tile{X: 40, Y: 10}, 60)
	guardia := ponerUnidad(h, 300, miembro, 2, unit.TypeWarrior, world.Tile{X: 42, Y: 10}, 60)
	lejos := ponerUnidad(h, 301, h.playerID, h.cityID, unit.TypeWarrior, world.Tile{X: 60, Y: 28}, 60)

	h.reclamarComo(tercero, enemigoID, false)
	h.tick()
	objetivo, responde := h.state.Engaged(guardia.ID)
	require.True(t, responde, "el guerrero de un miembro que no es el líder responde")
	assert.Equal(t, enemigoID, objetivo)
	_, respondeLejos := h.state.Engaged(lejos.ID)
	assert.False(t, respondeLejos, "fuera del radio de guardia, no")
}

// RN-CLANTERR-003 y RN-FAC-007: la tierra del clan es afín a sus miembros
// abiertos (8 × 800 / 1000 = 6) y a nadie más; quien sale la pierde en el acto.
func TestLaTierraDelClanEsAfinASusMiembros(t *testing.T) {
	preparar := func(t *testing.T) (*harness, uuid.UUID, uuid.UUID, int64) {
		h, _ := conClanes(t, nil)
		vecinos := conVecinos(h, 2)
		a, b := vecinos[0], vecinos[1]
		conCuatroTerritorios(h, nil)
		h.fundarClan(a, "Los Osos", "OSO")
		clanID, ok := h.state.ClanOf(a)
		require.True(t, ok)
		h.invitarAlClan(a, b)
		h.aceptarClan(b, clanID)
		territorioDelClan(h, 2, clanID)
		ponerUnidad(h, guerreroID, h.playerID, h.cityID, unit.TypeWarrior, world.Tile{X: 44, Y: 15}, 60)
		return h, a, b, clanID
	}
	t.Run("un miembro, en la tierra de su clan", func(t *testing.T) {
		h, a, _, _ := preparar(t)
		v := ponerUnidad(h, enemigoID, a, 2, unit.TypeVillager, world.Tile{X: 45, Y: 15}, 40)
		assert.Equal(t, int32(6), primerGolpe(t, h, v))
	})
	t.Run("un miembro, fuera de ella", func(t *testing.T) {
		h, a, _, _ := preparar(t)
		h.ponerGuerreroEn(world.Tile{X: 44, Y: 50})
		v := ponerUnidad(h, enemigoID, a, 2, unit.TypeVillager, world.Tile{X: 45, Y: 50}, 40)
		assert.Equal(t, int32(8), primerGolpe(t, h, v))
	})
	t.Run("quien acaba de salir, ya no", func(t *testing.T) {
		h, a, _, _ := preparar(t)
		h.send(simulation.LeaveClan{PlayerID: a, RequestID: uuid.NewString()})
		v := ponerUnidad(h, enemigoID, a, 2, unit.TypeVillager, world.Tile{X: 45, Y: 15}, 40)
		assert.Equal(t, int32(8), primerGolpe(t, h, v))
	})
}

// ponerGuerreroEn mueve el guerrero del arnés a otro tile.
func (h *harness) ponerGuerreroEn(at world.Tile) {
	h.t.Helper()
	u, ok := h.state.Unit(guerreroID)
	require.True(h.t, ok)
	u.X, u.Y = at.X, at.Y
}

// ADR-041 §4: la última salida de un clan con territorios va en dos fases. Hasta
// la confirmación el clan sigue vivo y rechaza entradas; al confirmarse, el
// clan se disuelve y sus territorios quedan libres, con su territory.update. Si
// la escritura no llega, el clan sigue como estaba.
func TestLaDisolucionDeUnClanConTerritorios(t *testing.T) {
	t.Run("confirmada", func(t *testing.T) {
		h, fc := conClanes(t, nil)
		conCuatroTerritorios(h, map[int64]uuid.UUID{1: h.playerID})
		clanID := h.clanConMiembros()
		territorioDelClan(h, 2, clanID)
		fc.released = []territory.Control{{TerritoryID: 2, OwnerType: territory.OwnerNone, Version: 2}}

		h.send(simulation.LeaveClan{PlayerID: h.playerID, RequestID: uuid.NewString()})
		require.Empty(t, h.codigos())
		_, sigue := h.state.ClanOf(h.playerID)
		assert.True(t, sigue, "hasta la confirmación sigue siendo del clan")
		assert.Equal(t, territory.ClanOwnerID(clanID), dueñoDe(h.vistaDe(2)))
		require.Len(t, fc.leaves, 1)
		assert.True(t, fc.leaves[0].Dissolve)

		h.rec.reset()
		h.tick()
		_, sigue = h.state.ClanOf(h.playerID)
		assert.False(t, sigue)
		assert.Empty(t, h.state.ClanMembers(clanID))
		v := h.vistaDe(2)
		assert.Equal(t, string(territory.OwnerNone), v.OwnerType)
		assert.Nil(t, v.OwnerClan)
		liberado := false
		for _, m := range h.rec.byType(protocol.TypeTerritoryUpdate) {
			if p := m.Payload.(protocol.TerritoryUpdatePayload); p.Territory.ID == 2 && p.Territory.OwnerType == "NONE" {
				liberado = true
			}
		}
		assert.True(t, liberado, "el área recibe el territorio libre")
		assert.Nil(t, h.ciudad().Clan)
	})

	t.Run("mientras tanto, el clan no admite entradas", func(t *testing.T) {
		h, _ := conClanes(t, nil)
		conCuatroTerritorios(h, map[int64]uuid.UUID{1: h.playerID})
		clanID := h.clanConMiembros()
		territorioDelClan(h, 2, clanID)
		invitado := conVecinos(h, 1)[0]
		h.invitarAlClan(h.playerID, invitado)
		// La salida y la entrada en el mismo tick: la salida marca el clan en
		// disolución antes de que la entrada se valide.
		h.commands <- simulation.LeaveClan{PlayerID: h.playerID, RequestID: uuid.NewString()}
		h.commands <- simulation.RespondClan{PlayerID: invitado, RequestID: uuid.NewString(), ClanID: clanID, Accept: true}
		h.loop.Step(h.clk.NowMs())
		assert.Equal(t, []string{protocol.CodeInvalidTarget}, h.codigos())
		assert.Equal(t, "el clan se está disolviendo", h.ultimoMotivo())
	})

	t.Run("fallida", func(t *testing.T) {
		h, fc := conClanes(t, nil)
		conCuatroTerritorios(h, map[int64]uuid.UUID{1: h.playerID})
		clanID := h.clanConMiembros()
		territorioDelClan(h, 2, clanID)
		fc.leaveErr = assert.AnError
		h.persister.permanent = true
		h.send(simulation.LeaveClan{PlayerID: h.playerID, RequestID: uuid.NewString()})
		h.tick()
		assert.Equal(t, []string{protocol.CodeInternalError}, h.codigos())
		_, sigue := h.state.ClanOf(h.playerID)
		assert.True(t, sigue, "no se pudo escribir: sigue en el clan, con su territorio")
		assert.Equal(t, territory.ClanOwnerID(clanID), dueñoDe(h.vistaDe(2)))

		// Y puede volver a intentarlo.
		fc.leaveErr = nil
		fc.released = []territory.Control{{TerritoryID: 2, OwnerType: territory.OwnerNone, Version: 2}}
		h.send(simulation.LeaveClan{PlayerID: h.playerID, RequestID: uuid.NewString()})
		h.tick()
		_, sigue = h.state.ClanOf(h.playerID)
		assert.False(t, sigue)
		assert.Equal(t, string(territory.OwnerNone), h.vistaDe(2).OwnerType)
	})

	t.Run("sin territorios, como en E18", func(t *testing.T) {
		h, fc := conClanes(t, nil)
		conCuatroTerritorios(h, map[int64]uuid.UUID{1: h.playerID})
		h.clanConMiembros()
		h.send(simulation.LeaveClan{PlayerID: h.playerID, RequestID: uuid.NewString()})
		_, sigue := h.state.ClanOf(h.playerID)
		assert.False(t, sigue, "optimista: en el acto")
		assert.True(t, fc.lastLeave(t).Dissolve)
	})
}

// ADR-041 §1: una reclamación para un clan vuelve al arrancar con su clan; si su
// dueño ya no es de ese clan, el primer barrido la termina sin captura.
func TestUnaReclamacionParaElClanTrasUnReinicio(t *testing.T) {
	h, _ := conClanes(t, nil)
	conCuatroTerritorios(h, map[int64]uuid.UUID{1: h.playerID})
	clanID := h.clanConMiembros()
	ponerUnidad(h, guerreroID, h.playerID, h.cityID, unit.TypeWarrior, world.Tile{X: 40, Y: 10}, 60)
	now := h.clk.NowMs()
	base := territory.Claim{ID: 9, TerritoryID: 2, UnitID: guerreroID, PlayerID: h.playerID, CityID: h.cityID,
		X: 40, Y: 10, StartedAtMs: now, CompletesAtMs: now + 300_000, RequestID: "r"}

	t.Run("de su clan, sigue", func(t *testing.T) {
		c := base
		c.ClanID = clanID
		h.state.SetClaims([]territory.Claim{c})
		h.tick()
		got, confirmada, ok := h.state.ActiveClaim(2)
		require.True(t, ok && confirmada)
		assert.Equal(t, clanID, got.ClanID)
		require.NotNil(t, h.vistaDe(2).Claim.Clan)
	})
	t.Run("de un clan que ya no es el suyo, termina", func(t *testing.T) {
		c := base
		c.ClanID = clanID + 40
		c.UnitID, c.ID = guerreroID, 10
		h.state.SetClaims([]territory.Claim{c})
		h.tick()
		_, _, ok := h.state.ActiveClaim(2)
		assert.False(t, ok)
		assert.Contains(t, h.conquest.endReasons(), simulation.ClaimEndClan)
	})
}
