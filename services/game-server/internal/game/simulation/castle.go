package simulation

import (
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/building"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// El castillo en la simulación (SPEC-CASTLE-AGE §4.3, ADR-044).
//
// Un castillo completo golpea, en la fase 4, a la unidad más cercana de las que
// a su alcance están atacando algo de su dueño: una unidad suya, su almacén o
// uno de sus edificios. Sus golpes se calculan después de los de las unidades
// (cuando cada una ya decidió si persigue o se planta en ATTACKING) y antes de
// aplicar ninguno, y se aplican detrás de ellos. No es una unidad: su estado
// vive sólo en la RAM y un reinicio lo deja listo y sin objetivo (ADR-044 §2).

// castleShot es el estado de disparo de un castillo completo.
type castleShot struct {
	// nextShotAtMs es el instante de su próximo golpe; con objetivo, si quedó
	// atrás se lleva a ahora.
	nextShotAtMs int64
	// targetID es la unidad a la que apunta; 0 si ninguna.
	targetID int64
}

// castleState es lo que la simulación sabe de los castillos, por id de
// edificio. Un castillo sin entrada está listo y sin objetivo.
type castleState struct {
	shots map[int64]*castleShot
}

func newCastleState() castleState {
	return castleState{shots: make(map[int64]*castleShot)}
}

// CastleTarget dice a qué unidad apunta un castillo (tests).
func (s *State) CastleTarget(castleID int64) (int64, bool) {
	sh, ok := s.castles.shots[castleID]
	if !ok || sh.targetID == 0 {
		return 0, false
	}
	return sh.targetID, true
}

// castleTargetOf es el objetivo de un castillo para su vista, o nil.
func (s *State) castleTargetOf(castleID int64) *int64 {
	if id, ok := s.CastleTarget(castleID); ok {
		return &id
	}
	return nil
}

// castleHit es un golpe de castillo que vence en el tick.
type castleHit struct {
	castle *building.Building
	owner  uuid.UUID
	target *unit.Unit
	damage int32
}

// killer es el autor de una muerte: una unidad o, desde E23, un castillo
// (ADR-044 §5). Con castle, unit es nil y owner es el dueño del castillo.
type killer struct {
	unit   *unit.Unit
	castle *building.Building
	owner  uuid.UUID
}

func unitKiller(u *unit.Unit) killer {
	if u == nil {
		return killer{}
	}
	return killer{unit: u, owner: u.PlayerID}
}

// castleBlocked dice si un castillo de `owner` no puede golpear a `t` en `posT`
// (RN-CASTLE-005, INV-COMBAT-007): es del propio dueño, no se ve, alguno de los
// dos está protegido, hay paz entre ellos o pisa una Safe Zone. Es el mismo
// predicado al elegir el objetivo y al aplicar el golpe.
func (s *Simulation) castleBlocked(owner uuid.UUID, t *unit.Unit, posT world.Tile, now time.Time) bool {
	if t.PlayerID == owner || !s.visibleTo(t, owner) {
		return true
	}
	if c, ok := s.state.CityOf(t.PlayerID); ok && c.ProtectedAt(now) {
		return true
	}
	if c, ok := s.state.CityOf(owner); ok && c.ProtectedAt(now) {
		return true
	}
	if s.state.OrderPeace(owner, t.PlayerID) || s.state.ClanPeace(owner, t.PlayerID, now) ||
		s.state.AllyPeace(owner, t.PlayerID, now) || s.state.AtPeace(owner, t.PlayerID, now) {
		return true
	}
	if _, ok := s.state.SafeZoneAt(posT.X, posT.Y); ok {
		return true
	}
	return false
}

// engagementAgainst dice si el blanco de un enfrentamiento es de `owner`: una
// unidad suya, su ciudad (saqueo) o un edificio de su ciudad (asedio).
func (s *Simulation) engagementAgainst(e *engagement, owner uuid.UUID) bool {
	switch {
	case e.cityID != 0:
		c, ok := s.state.City(e.cityID)
		return ok && c.OwnerPlayerID == owner
	case e.buildingID != 0:
		b, ok := s.state.buildings[e.buildingID]
		if !ok {
			return false
		}
		c, ok := s.state.City(b.CityID)
		return ok && c.OwnerPlayerID == owner
	default:
		t, ok := s.state.Unit(e.targetID)
		return ok && t.PlayerID == owner
	}
}

// stepCastles calcula los golpes de los castillos que vencen en el tick
// (RN-CASTLE-004), en orden de id de castillo, y difunde los cambios de
// objetivo. No aplica nada.
func (s *Simulation) stepCastles(nowMs int64, now time.Time) []castleHit {
	// Los atacantes, en orden de id: sólo cuentan los que están plantados.
	attackers := make([]int64, 0, len(s.state.combat.engagements))
	for id := range s.state.combat.engagements {
		attackers = append(attackers, id)
	}
	sort.Slice(attackers, func(i, j int) bool { return attackers[i] < attackers[j] })

	var hits []castleHit
	for _, id := range s.state.buildingIDs {
		b := s.state.buildings[id]
		if b.Type != building.TypeCastle || !b.IsComplete() {
			continue
		}
		def, err := building.Lookup(b.Type)
		if err != nil || !def.CanShoot() {
			continue
		}
		c, ok := s.state.City(b.CityID)
		if !ok {
			continue
		}
		owner := c.OwnerPlayerID

		var best *unit.Unit
		bestDist := int32(-1)
		for _, aid := range attackers {
			e, ok := s.state.combat.engagements[aid]
			if !ok {
				continue
			}
			a, ok := s.state.Unit(aid)
			if !ok || !a.IsAlive() || a.Status != unit.StatusAttacking || s.state.IsEntering(a.ID) {
				continue
			}
			if !s.engagementAgainst(e, owner) {
				continue
			}
			posA := s.authoritativePosition(a, nowMs)
			d := chebyshevTiles(posA, b.Tile())
			if d > def.AttackRange || s.castleBlocked(owner, a, posA, now) {
				continue
			}
			// El más cercano; a igual distancia, el de menor id, que es el
			// primero en este recorrido.
			if best == nil || d < bestDist {
				best, bestDist = a, d
			}
		}

		sh, ok := s.state.castles.shots[b.ID]
		if !ok {
			sh = &castleShot{}
			s.state.castles.shots[b.ID] = sh
		}
		if best == nil {
			s.setCastleTarget(b, sh, 0)
			continue
		}
		s.setCastleTarget(b, sh, best.ID)
		if sh.nextShotAtMs < nowMs {
			sh.nextShotAtMs = nowMs
		}
		if nowMs >= sh.nextShotAtMs {
			hits = append(hits, castleHit{castle: b, owner: owner, target: best, damage: def.AttackDamage})
			sh.nextShotAtMs += def.AttackIntervalMs
		}
	}
	return hits
}

// idleCastles deja sin objetivo a los castillos que apuntaban a alguien: un tick
// sin enfrentamientos no tiene a quién golpear.
func (s *Simulation) idleCastles() {
	if len(s.state.castles.shots) == 0 {
		return
	}
	ids := make([]int64, 0, len(s.state.castles.shots))
	for id, sh := range s.state.castles.shots {
		if sh.targetID != 0 {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		if b, ok := s.state.buildings[id]; ok {
			s.setCastleTarget(b, s.state.castles.shots[id], 0)
		}
	}
}

// setCastleTarget cambia el objetivo de un castillo y, si cambia, lo difunde en
// building.update (SPEC-CASTLE-AGE §6): la unidad, o null si deja de apuntar.
func (s *Simulation) setCastleTarget(b *building.Building, sh *castleShot, targetID int64) {
	if sh.targetID == targetID {
		return
	}
	sh.targetID = targetID
	var target *int64
	if targetID != 0 {
		id := targetID
		target = &id
	}
	cx, cy := s.state.World().ChunkOf(b.X, b.Y)
	s.deps.Broadcaster.BroadcastChunk(cx, cy, protocol.TypeBuildingUpdate,
		protocol.BuildingUpdatePayload{ID: b.ID, Status: string(b.Status), TargetUnitID: &target})
}

// applyCastleHits aplica los golpes de los castillos, detrás de los de las
// unidades y en orden de castillo, cada uno sobre la vida que dejó el anterior
// (ADR-044 §4). No despiertan la respuesta automática ni escriben UnitAttacked
// (RN-CASTLE-006). Los que matan quedan en killers.
func (s *Simulation) applyCastleHits(hits []castleHit, killers map[int64]killer, nowMs int64) {
	now := time.UnixMilli(nowMs).UTC()
	for _, h := range hits {
		t := h.target
		if t.HP <= 0 || !t.IsAlive() {
			continue
		}
		pos := s.authoritativePosition(t, nowMs)
		// INV-COMBAT-007: el objetivo se eligió con este mismo predicado; no hay
		// ruta que llegue aquí sin pasarlo.
		if s.castleBlocked(h.owner, t, pos, now) {
			s.deps.Log.Error("invariant_violation",
				"inv_id", "INV-COMBAT-007", "severity", "CRITICO", "policy", "REJECT",
				"entity", fmt.Sprintf("building:%d", h.castle.ID), "tick", s.state.Tick(),
				"detail", "golpe de castillo sobre una unidad que no puede recibirlo: se descarta")
			continue
		}
		// RN-FAC-008: menos daño en tierra afín al dueño del golpeado.
		t.HP -= s.damageTaken(t, h.damage, nowMs)
		if t.HP < 0 {
			t.HP = 0
		}
		s.state.MarkDirty(t.ID)
		if t.HP == 0 {
			killers[t.ID] = killer{castle: h.castle, owner: h.owner}
		}
		hp := t.HP
		cx, cy := s.state.World().ChunkOf(t.X, t.Y)
		s.deps.Broadcaster.BroadcastChunk(cx, cy, protocol.TypeEntityUpdate,
			protocol.EntityUpdatePayload{ID: t.ID, HP: &hp})
	}
}
