package simulation

import (
	"context"
	"sort"

	"github.com/google/uuid"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
)

// La respuesta automática (SPEC-COMBAT-RESPONSE).
//
// No hay vigilancia por tick: la dispara un golpe aplicado en la fase 4. Cada
// golpe alerta a la víctima, si es un guerrero, y a los guerreros de su dueño
// dentro del radio de guardia. Un guerrero disponible responde abriendo un
// enfrentamiento automático contra quien golpeó, con su puesto anotado; el
// resto (persecución, golpes, fin) es el enfrentamiento de unit.attack.

// Attack es lo que escribe el primer golpe de un enfrentamiento: el evento
// UnitAttacked (SPEC-COMBAT-RESPONSE Q8).
type Attack struct {
	AttackerID     int64
	AttackerPlayer uuid.UUID
	TargetID       int64
	TargetPlayer   uuid.UUID
	At             world.Tile
	Tick           uint64
	// Auto marca una respuesta automática.
	Auto bool
	// AtMs es el instante del golpe en la simulación (ADR-028 §3).
	AtMs int64
}

// JobAttack es el nombre del trabajo de la cola que escribe UnitAttacked.
const JobAttack = "unit.attacked"

// submitAttack encola el UnitAttacked del primer golpe de un enfrentamiento.
// Es auditoría: si la cola lo descarta tras sus reintentos, se registra y se
// pierde; no hay estado del mundo que dependa de él.
func (s *Simulation) submitAttack(h hit, nowMs int64) {
	if s.deps.Combat == nil {
		return
	}
	a := Attack{
		AttackerID: h.attacker.ID, AttackerPlayer: h.attacker.PlayerID,
		TargetID: h.target.ID, TargetPlayer: h.target.PlayerID,
		At: s.authoritativePosition(h.target, nowMs), Tick: s.state.Tick(), Auto: h.e.auto,
		AtMs: nowMs,
	}
	store := s.deps.Combat
	s.deps.Persister.Submit(Job{
		Name: JobAttack,
		Key:  UnitKey(a.AttackerID),
		Run:  func(ctx context.Context) error { return store.PersistAttack(ctx, a) },
		OnPermanentFailure: func(err error) {
			s.deps.Log.Error("no se pudo escribir UnitAttacked; se pierde el evento",
				"err", err, "attacker_id", a.AttackerID, "target_id", a.TargetID)
		},
	})
}

// raiseAlarm despierta a quien debe responder a un golpe (RN-COMBAT-014): la
// víctima primero y después los guardias, por id (RN-COMBAT-015).
func (s *Simulation) raiseAlarm(al alert, nowMs int64) {
	if !al.attacker.IsAlive() {
		return
	}
	if al.victim.IsAlive() {
		s.respond(al.victim, al.attacker, nowMs)
	}
	for _, g := range s.guardsNear(al.victim.PlayerID, al.at) {
		if g.ID != al.victim.ID {
			s.respond(g, al.attacker, nowMs)
		}
	}
}

// guardsNear devuelve, por id, los guerreros vivos de un jugador cuya posición
// autoritativa está a ≤ guardRadius de un tile.
func (s *Simulation) guardsNear(playerID uuid.UUID, at world.Tile) []*unit.Unit {
	r := s.guardRadius()
	w := s.state.World()
	// El índice de chunks usa el tile consolidado de cada unidad, que puede ir
	// un tile por detrás del autoritativo: se amplía la caja en uno.
	minX, minY := max(at.X-r-1, 0), max(at.Y-r-1, 0)
	maxX, maxY := min(at.X+r+1, w.Width()-1), min(at.Y+r+1, w.Height()-1)
	cx0, cy0 := w.ChunkOf(minX, minY)
	cx1, cy1 := w.ChunkOf(maxX, maxY)
	nowMs := s.deps.Clock.NowMs()
	var out []*unit.Unit
	for cy := cy0; cy <= cy1; cy++ {
		for cx := cx0; cx <= cx1; cx++ {
			for _, u := range s.state.UnitsInChunk(cx, cy) {
				if u.PlayerID != playerID || u.Type != unit.TypeWarrior || !u.IsAlive() {
					continue
				}
				if chebyshevTiles(s.authoritativePosition(u, nowMs), at) <= r {
					out = append(out, u)
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// available dice si un guerrero puede responder y desde qué puesto
// (RN-COMBAT-012, RN-COMBAT-013): parado y sin nada que hacer, o volviendo a
// su puesto de una respuesta anterior, que conserva.
func (s *Simulation) available(g *unit.Unit, nowMs int64) (world.Tile, bool) {
	if s.state.IsEntering(g.ID) {
		return world.Tile{}, false
	}
	if _, engaged := s.state.combat.engagements[g.ID]; engaged {
		return world.Tile{}, false
	}
	if _, working := s.state.tasks[g.ID]; working {
		return world.Tile{}, false
	}
	m, moving := s.state.Movement(g.ID)
	switch g.Status {
	case unit.StatusIdle:
		if moving {
			return world.Tile{}, false
		}
		return s.authoritativePosition(g, nowMs), true
	case unit.StatusMoving:
		r, ok := s.state.combat.returning[g.ID]
		if ok && moving && m.ID == r.movementID {
			return r.post, true
		}
	}
	return world.Tile{}, false
}

// respond abre la respuesta de un guerrero contra quien golpeó, si está
// disponible y nada lo impide (RN-COMBAT-016, INV-COMBAT-002).
func (s *Simulation) respond(g, attacker *unit.Unit, nowMs int64) {
	def, err := unit.Lookup(g.Type)
	if err != nil || !def.CanAttack() {
		return
	}
	post, ok := s.available(g, nowMs)
	if !ok {
		return
	}
	posG := s.authoritativePosition(g, nowMs)
	posA := s.authoritativePosition(attacker, nowMs)
	if code, _ := s.attackBlocked(g, attacker, posG, posA, s.deps.Clock.Now()); code != "" {
		return
	}
	if chebyshevTiles(post, posA) > s.leash() {
		return
	}
	e := &engagement{attackerID: g.ID, targetID: attacker.ID, lastRepathTick: s.state.Tick(), auto: true, post: post}
	s.deps.Log.Debug("respuesta automática", "unit_id", g.ID, "target_id", attacker.ID)
	if chebyshevTiles(posG, posA) > def.AttackRange {
		path, perr := s.chasePath(g, posG, posA, def)
		if perr != nil {
			return
		}
		s.state.combat.engagements[g.ID] = e
		s.beginMovement(g, posG, posA, path, nowMs, nil)
		return
	}
	s.state.combat.engagements[g.ID] = e
	s.startAttacking(g, e, nowMs)
}
