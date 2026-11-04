package simulation

import (
	"context"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/movement"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/pathfinding"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// El combate en la simulación (SPEC-COMBAT-RAID, ADR-024).
//
// Una orden unit.attack abre un enfrentamiento atacante → objetivo. La fase 4
// del tick lo hace avanzar: persigue mientras el objetivo esté lejos, se planta
// en ATTACKING al alcanzarlo y golpea cada AttackIntervalMs. Los golpes de un
// tick se calculan todos con el estado de fin de la fase 3 y se aplican a la
// vez; las muertes se procesan después, en orden de víctima. El enfrentamiento
// vive sólo en la RAM: un reinicio lo termina (ADR-024 §2).

// AttackUnit ordena a una unidad militar propia atacar a una unidad ajena.
type AttackUnit struct {
	PlayerID  uuid.UUID
	SessionID uuid.UUID
	RequestID string
	UnitID    int64
	TargetID  int64
}

func (AttackUnit) commandType() string { return protocol.TypeUnitAttack }

// DeathPersistFailed es la compensación de una muerte que no se escribió: el
// siguiente volcado la vuelve a encolar (ADR-024 §2). La transacción es
// idempotente: sólo actúa sobre una víctima que aún no está DEAD.
type DeathPersistFailed struct{ Death Death }

func (DeathPersistFailed) commandType() string { return "death.persist_failed" }

// Death es lo que escribe la transacción de una muerte (RN-COMBAT-009).
type Death struct {
	VictimID       int64
	VictimPlayer   uuid.UUID
	KillerID       int64
	KillerPlayer   uuid.UUID
	CityID         *int64
	PopulationCost int32
	At             world.Tile
	Tick           uint64
	// VictimType y AtMs son para el registro (SPEC-EVENT-LOG, ADR-028 §3):
	// qué murió y cuándo en la simulación, no cuándo se escribió.
	VictimType unit.Type
	AtMs       int64
	// Loot es lo que pasó al asesino y Lost lo que se perdió por falta de
	// hueco. La carga del asesino NO va en esta transacción: viaja en el
	// volcado de su unidad, así que una caída puede perderla pero nunca
	// duplicarla (INV-COMBAT-003).
	Loot int32
	Lost int32
}

// CombatStore escribe las muertes y el primer golpe de cada enfrentamiento.
// Opcional: sin él, la muerte sólo existe en la RAM, que es lo que hacen los
// tests de simulación que no la miran.
type CombatStore interface {
	PersistDeath(ctx context.Context, d Death) error
	PersistAttack(ctx context.Context, a Attack) error
	// PersistPillage escribe un golpe de saqueo: el débito versionado del stock
	// de la víctima y su CityPillaged, en una transacción (ADR-026). Ya escrito
	// es éxito; un débito que no cabe es economy.ErrInsufficientResources.
	PersistPillage(ctx context.Context, p Pillage) error
}

// JobDeath es el nombre del trabajo de la cola que escribe una muerte.
const JobDeath = "unit.death"

// engagement es un enfrentamiento en curso.
type engagement struct {
	attackerID int64
	targetID   int64
	// cityID es la ciudad saqueada; distinto de cero, el enfrentamiento es un
	// saqueo y targetID no se usa (SPEC-COMBAT-PILLAGE).
	cityID int64
	// nextHitAtMs es el instante del próximo golpe; 0 hasta el primer contacto.
	nextHitAtMs int64
	// lastRepathTick es el último tick en que la persecución recalculó ruta.
	lastRepathTick uint64
	// announced indica que su primer golpe ya escribió UnitAttacked.
	announced bool
	// auto marca una respuesta automática (SPEC-COMBAT-RESPONSE); post es el
	// tile al que vuelve el guerrero cuando termina y desde el que se mide la
	// correa.
	auto bool
	post world.Tile
}

// returnLeg es la vuelta al puesto de un guerrero tras una respuesta: el
// movimiento que la hace y el puesto original, que conserva si vuelve a
// responder por el camino (RN-COMBAT-013).
type returnLeg struct {
	movementID int64
	post       world.Tile
}

// combatState es lo que la simulación sabe del combate.
type combatState struct {
	engagements map[int64]*engagement // por atacante
	// pendingDeaths son las muertes cuya escritura se descartó (ADR-022).
	pendingDeaths map[int64]Death
	// returning son las vueltas al puesto en curso, por unidad. Una entrada
	// vale mientras su movimiento sea el de la unidad: State.ClearMovement la
	// borra cuando ese movimiento termina, se cancela o lo sustituye otro.
	returning map[int64]returnLeg
	// pendingLoot es la carga robada que espera la transacción de su golpe, por
	// unidad. No entra en unit.Loot hasta confirmarse: el volcado sólo escribe
	// lo confirmado (ADR-026 §2).
	pendingLoot map[int64]int32
}

func newCombatState() combatState {
	return combatState{
		engagements:   make(map[int64]*engagement),
		pendingDeaths: make(map[int64]Death),
		returning:     make(map[int64]returnLeg),
		pendingLoot:   make(map[int64]int32),
	}
}

// Engaged dice a quién ataca una unidad (tests).
func (s *State) Engaged(attackerID int64) (int64, bool) {
	e, ok := s.combat.engagements[attackerID]
	if !ok || e.cityID != 0 {
		return 0, false
	}
	return e.targetID, true
}

// Responding dice si el enfrentamiento de una unidad es una respuesta
// automática y desde qué puesto (tests).
func (s *State) Responding(attackerID int64) (world.Tile, bool) {
	e, ok := s.combat.engagements[attackerID]
	if !ok || !e.auto {
		return world.Tile{}, false
	}
	return e.post, true
}

// Returning dice si una unidad vuelve a su puesto tras una respuesta, y a cuál
// (tests).
func (s *State) Returning(unitID int64) (world.Tile, bool) {
	r, ok := s.combat.returning[unitID]
	return r.post, ok
}

// ─────────────────────────────────────────────────────────────
// unit.attack
// ─────────────────────────────────────────────────────────────

func (s *Simulation) handleAttackUnit(cmd AttackUnit) {
	nowMs := s.deps.Clock.NowMs()
	reject := func(code, msg string) { s.economyError(cmd.PlayerID, cmd.RequestID, code, msg) }

	a, ok := s.state.Unit(cmd.UnitID)
	if !ok {
		reject(protocol.CodeUnitNotFound, "la unidad no existe")
		return
	}
	// La propiedad antes que nada, como en unit.move (INV-PLAYER-001).
	if err := a.EnsureOwnedBy(cmd.PlayerID); err != nil {
		reject(protocol.CodeUnitNotOwned, "la unidad no te pertenece")
		return
	}
	switch a.Status {
	case unit.StatusDead:
		reject(protocol.CodeUnitDead, "la unidad está muerta")
		return
	case unit.StatusGarrisoned:
		reject(protocol.CodeUnitGarrisoned, "la unidad está guarnecida")
		return
	}
	if s.state.IsEntering(a.ID) {
		reject(protocol.CodeUnitNotMovable, "la unidad está entrando en una ciudad")
		return
	}
	def, err := unit.Lookup(a.Type)
	if err != nil || !def.CanAttack() {
		reject(protocol.CodeUnitNotMovable, "esta unidad no ataca")
		return
	}

	t, ok := s.state.Unit(cmd.TargetID)
	if !ok || !s.visibleTo(t, cmd.PlayerID) {
		// RN-COMBAT-004: lo que el jugador no ve no existe para él.
		reject(protocol.CodeUnitNotFound, "la unidad objetivo no existe")
		return
	}
	posA := s.authoritativePosition(a, nowMs)
	posT := s.authoritativePosition(t, nowMs)
	if code, msg := s.attackBlocked(a, t, posA, posT, s.deps.Clock.Now()); code != "" {
		reject(code, msg)
		return
	}

	// Sustituye cualquier orden anterior del atacante (RN-COMBAT-008).
	s.stopWorking(a)
	s.endEngagement(a, false)
	e := &engagement{attackerID: a.ID, targetID: t.ID, lastRepathTick: s.state.Tick()}

	if chebyshevTiles(posA, posT) > def.AttackRange {
		path, perr := s.chasePath(a, posA, posT, def)
		if perr != nil {
			reject(pathErrorCode(perr), perr.Error())
			return
		}
		s.state.combat.engagements[a.ID] = e
		s.beginMovement(a, posA, posT, path, nowMs, nil)
		return
	}
	s.state.combat.engagements[a.ID] = e
	s.startAttacking(a, e, nowMs)
}

// visibleTo indica si un jugador ve una unidad: la propia siempre; la ajena
// si está en el mapa y no oculta (INV-SAFE-004, INV-GARR-006).
func (s *Simulation) visibleTo(u *unit.Unit, playerID uuid.UUID) bool {
	if u.PlayerID == playerID {
		return true
	}
	switch u.Status {
	case unit.StatusHidden, unit.StatusGarrisoned, unit.StatusDead:
		return false
	}
	return !s.state.IsEntering(u.ID)
}

// attackBlocked devuelve el código que impide el golpe, o "" si nada lo
// impide. Es el mismo predicado al ordenar y en cada golpe (INV-COMBAT-002),
// y el punto único de la regla de protección para la acción hostil
// (INV-CITY-006, ADR-016).
func (s *Simulation) attackBlocked(a, t *unit.Unit, posA, posT world.Tile, now time.Time) (string, string) {
	if t.PlayerID == a.PlayerID {
		return protocol.CodeInvalidTarget, "no puedes atacar tus propias unidades"
	}
	if !s.visibleTo(t, a.PlayerID) {
		return protocol.CodeUnitNotFound, "la unidad objetivo no existe"
	}
	if c, ok := s.state.CityOf(t.PlayerID); ok && c.ProtectedAt(now) {
		return protocol.CodeCityProtected, "el dueño de esa unidad está protegido"
	}
	// RN-COMBAT-018: la protección es simétrica; quien está protegido tampoco
	// golpea.
	if c, ok := s.state.CityOf(a.PlayerID); ok && c.ProtectedAt(now) {
		return protocol.CodeCityProtected, "estás protegido: no puedes atacar"
	}
	if s.state.AtPeace(a.PlayerID, t.PlayerID, now) {
		return protocol.CodeInvalidTarget, "un tratado de paz impide atacar a ese jugador"
	}
	if _, ok := s.state.SafeZoneAt(posA.X, posA.Y); ok {
		return protocol.CodeInvalidTarget, "no se ataca desde una zona segura"
	}
	if _, ok := s.state.SafeZoneAt(posT.X, posT.Y); ok {
		return protocol.CodeInvalidTarget, "no se ataca dentro de una zona segura"
	}
	return "", ""
}

// chasePath calcula la ruta de persecución hasta el tile del objetivo.
func (s *Simulation) chasePath(a *unit.Unit, from, to world.Tile, def unit.Definition) (movement.TimedPath, error) {
	start := time.Now() //lint:reloj-real mide la duración del A* para la métrica; no afecta al resultado
	tiles, err := s.deps.Pathfinder.FindPath(context.Background(), s.state.World(), from, to, pathfinding.Options{
		MaxNodes:    s.deps.PathMaxNodes,
		MaxDistance: s.deps.PathMaxDistance,
	})
	s.LastPathfindingCalls++
	s.LastPathfindingTime += time.Since(start) //lint:reloj-real ídem
	if err != nil {
		return movement.TimedPath{}, err
	}
	return movement.BuildTimedPath(tiles, s.state.World(), def.BaseMsPerTile)
}

// startAttacking planta al atacante en ATTACKING. El primer golpe cae en el
// instante del contacto.
func (s *Simulation) startAttacking(a *unit.Unit, e *engagement, nowMs int64) {
	if _, moving := s.state.Movement(a.ID); moving {
		s.cancelActiveMovement(a, movement.ReasonReplaced, nowMs)
	}
	if e.nextHitAtMs == 0 || e.nextHitAtMs < nowMs {
		e.nextHitAtMs = nowMs
	}
	if a.Status == unit.StatusAttacking {
		return
	}
	a.Status = unit.StatusAttacking
	s.state.MarkDirty(a.ID)
	s.broadcastStatus(a)
}

// endEngagement termina el enfrentamiento de un atacante, si lo tiene. Con
// stop, además lo deja parado e IDLE: es el final por una causa del mundo
// (RN-COMBAT-008). Sin stop, quien llama le da a continuación otra orden.
//
// Una respuesta automática que termina por una causa del mundo vuelve además
// a su puesto (RN-COMBAT-017). Sin stop, la orden del jugador que llega
// descarta el puesto.
func (s *Simulation) endEngagement(a *unit.Unit, stop bool) {
	e, ok := s.state.combat.engagements[a.ID]
	if !ok {
		return
	}
	delete(s.state.combat.engagements, a.ID)
	if !stop {
		return
	}
	nowMs := s.deps.Clock.NowMs()
	if _, moving := s.state.Movement(a.ID); moving {
		s.cancelActiveMovement(a, movement.ReasonServer, nowMs)
	}
	if a.Status == unit.StatusAttacking {
		a.Status = unit.StatusIdle
		s.state.MarkDirty(a.ID)
		s.broadcastStatus(a)
	}
	if e.auto && a.IsAlive() {
		s.returnToPost(a, e.post, nowMs)
	}
}

// stopEngagementByPlayer detiene a una unidad que pelea porque su dueño se lo
// ordena (unit.cancel, o unit.move al tile donde ya está): queda IDLE donde
// está y, si respondía sola, sin volver a su puesto (SPEC-COMBAT-RESPONSE §4.3).
func (s *Simulation) stopEngagementByPlayer(u *unit.Unit) {
	if e, ok := s.state.combat.engagements[u.ID]; ok {
		e.auto = false
	}
	s.endEngagement(u, true)
}

// returnToPost lleva a un guerrero de vuelta a su puesto con un movimiento
// normal. Sin ruta, se queda IDLE donde está (RN-COMBAT-017).
func (s *Simulation) returnToPost(a *unit.Unit, post world.Tile, nowMs int64) {
	pos := s.authoritativePosition(a, nowMs)
	if pos == post {
		return
	}
	def, err := unit.Lookup(a.Type)
	if err != nil {
		return
	}
	path, err := s.chasePath(a, pos, post, def)
	if err != nil {
		s.deps.Log.Info("el guerrero no encuentra ruta de vuelta a su puesto", "unit_id", a.ID, "err", err)
		return
	}
	m := s.beginMovement(a, pos, post, path, nowMs, nil)
	s.state.combat.returning[a.ID] = returnLeg{movementID: m.ID, post: post}
}

func (s *Simulation) broadcastStatus(u *unit.Unit) {
	status := string(u.Status)
	cx, cy := s.state.World().ChunkOf(u.X, u.Y)
	s.deps.Broadcaster.BroadcastChunk(cx, cy, protocol.TypeEntityUpdate,
		protocol.EntityUpdatePayload{ID: u.ID, Status: &status})
}

// ─────────────────────────────────────────────────────────────
// Fase 4
// ─────────────────────────────────────────────────────────────

type hit struct {
	attacker *unit.Unit
	target   *unit.Unit
	damage   int32
	e        *engagement
}

// alert es un golpe aplicado que puede despertar una respuesta automática
// (RN-COMBAT-014): quién golpeó, a quién y dónde estaba la víctima.
type alert struct {
	attacker *unit.Unit
	victim   *unit.Unit
	at       world.Tile
}

// ResolveCombat es la fase 4 del tick (ADR-024 §1).
func (s *Simulation) ResolveCombat(nowMs int64) {
	if len(s.state.combat.engagements) == 0 {
		return
	}
	now := time.UnixMilli(nowMs).UTC()
	ids := make([]int64, 0, len(s.state.combat.engagements))
	for id := range s.state.combat.engagements {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	// 1. Calcular, con el estado de fin de la fase 3.
	var hits []hit
	var pillages []pillageHit
	for _, id := range ids {
		e, ok := s.state.combat.engagements[id]
		if !ok {
			continue
		}
		a, ok := s.state.Unit(id)
		if !ok || !a.IsAlive() {
			delete(s.state.combat.engagements, id)
			continue
		}
		if e.cityID != 0 {
			if ph, due := s.stepPillage(a, e, nowMs, now); due {
				pillages = append(pillages, ph)
			}
			continue
		}
		t, ok := s.state.Unit(e.targetID)
		if !ok || !t.IsAlive() {
			s.endEngagement(a, true)
			continue
		}
		def, err := unit.Lookup(a.Type)
		if err != nil || !def.CanAttack() {
			s.endEngagement(a, true)
			continue
		}
		posA := s.authoritativePosition(a, nowMs)
		posT := s.authoritativePosition(t, nowMs)
		if code, _ := s.attackBlocked(a, t, posA, posT, now); code != "" {
			s.endEngagement(a, true)
			continue
		}
		d := chebyshevTiles(posA, posT)
		if d > s.maxChase() {
			s.endEngagement(a, true)
			continue
		}
		// RN-COMBAT-017: la respuesta no se aleja de su puesto más que la correa.
		if e.auto && chebyshevTiles(e.post, posT) > s.leash() {
			s.endEngagement(a, true)
			continue
		}
		if d > def.AttackRange {
			s.chase(a, e, posA, posT, def, nowMs)
			continue
		}
		s.startAttacking(a, e, nowMs)
		if nowMs >= e.nextHitAtMs {
			hits = append(hits, hit{attacker: a, target: t, damage: def.AttackDamage, e: e})
			e.nextHitAtMs += def.AttackIntervalMs
		}
	}

	// 2. Aplicar todos a la vez. A igualdad, el golpe mortal es el del
	// atacante de id menor: el orden de cálculo, que es estable.
	killers := make(map[int64]*unit.Unit)
	var alerts []alert
	for _, h := range hits {
		if h.target.HP <= 0 {
			continue
		}
		if !h.e.announced {
			h.e.announced = true
			s.submitAttack(h, nowMs)
		}
		alerts = append(alerts, alert{attacker: h.attacker, victim: h.target, at: s.authoritativePosition(h.target, nowMs)})
		h.target.HP -= h.damage
		if h.target.HP < 0 {
			h.target.HP = 0
		}
		s.state.MarkDirty(h.target.ID)
		if h.target.HP == 0 {
			killers[h.target.ID] = h.attacker
		}
		hp := h.target.HP
		cx, cy := s.state.World().ChunkOf(h.target.X, h.target.Y)
		s.deps.Broadcaster.BroadcastChunk(cx, cy, protocol.TypeEntityUpdate,
			protocol.EntityUpdatePayload{ID: h.target.ID, HP: &hp})
	}

	// 3. Las muertes, en orden de víctima.
	victims := make([]int64, 0, len(killers))
	for id := range killers {
		victims = append(victims, id)
	}
	sort.Slice(victims, func(i, j int) bool { return victims[i] < victims[j] })
	for _, id := range victims {
		if v, ok := s.state.Unit(id); ok {
			s.kill(v, killers[id], nowMs)
		}
	}

	// 4. Los golpes de saqueo, después de los golpes a unidades y en orden de
	// guerrero, cada uno contra el stock que dejó el anterior (RN-COMBAT-022).
	var robbed []pillageHit
	for _, ph := range pillages {
		if s.applyPillage(ph, nowMs) {
			robbed = append(robbed, ph)
		}
	}

	// 5. Las respuestas automáticas, en el orden de los golpes (RN-COMBAT-015):
	// primero los golpes a unidades y después los saqueos (RN-COMBAT-027).
	for _, al := range alerts {
		s.raiseAlarm(al, nowMs)
	}
	for _, ph := range robbed {
		s.raiseCityAlarm(ph.attacker, ph.city, nowMs)
	}
}

func (s *Simulation) guardRadius() int32 {
	if s.deps.CombatGuardRadiusTiles > 0 {
		return s.deps.CombatGuardRadiusTiles
	}
	return 4
}

func (s *Simulation) leash() int32 {
	if s.deps.CombatLeashTiles > 0 {
		return s.deps.CombatLeashTiles
	}
	return 8
}

func (s *Simulation) maxChase() int32 {
	if s.deps.CombatMaxChaseTiles > 0 {
		return s.deps.CombatMaxChaseTiles
	}
	return 32
}

// chase lleva al atacante hacia el objetivo. Recalcula la ruta sólo si el
// objetivo ya no está al alcance del final de la actual, y como mucho cada
// CombatRepathTicks.
func (s *Simulation) chase(a *unit.Unit, e *engagement, posA, posT world.Tile, def unit.Definition, nowMs int64) {
	if a.Status == unit.StatusAttacking {
		a.Status = unit.StatusIdle
		s.state.MarkDirty(a.ID)
		s.broadcastStatus(a)
	}
	if m, moving := s.state.Movement(a.ID); moving && chebyshevTiles(m.Target, posT) <= def.AttackRange {
		return
	}
	every := uint64(s.deps.CombatRepathTicks)
	if every == 0 {
		every = 5
	}
	_, moving := s.state.Movement(a.ID)
	if moving && s.state.Tick()-e.lastRepathTick < every {
		return
	}
	e.lastRepathTick = s.state.Tick()
	path, err := s.chasePath(a, posA, posT, def)
	if err != nil {
		s.endEngagement(a, true)
		return
	}
	s.beginMovement(a, posA, posT, path, nowMs, nil)
}

// kill aplica una muerte (RN-COMBAT-009) y el botín (RN-COMBAT-010).
func (s *Simulation) kill(v, killer *unit.Unit, nowMs int64) {
	at := s.authoritativePosition(v, nowMs)
	// Su movimiento y su tarea mueren con ella; la transacción los cierra.
	s.state.ClearMovement(v.ID)
	carried := v.Loot
	if e, ok := s.state.tasks[v.ID]; ok {
		carried += e.Carrying
		delete(s.state.tasks, v.ID)
	}
	delete(s.state.combat.engagements, v.ID)
	// La carga pendiente de un saqueo no se hereda: si su transacción se
	// confirma, esa madera se pierde (ADR-026 §4).
	delete(s.state.combat.pendingLoot, v.ID)

	// INV-COMBAT-003: el botín es min(carga de la víctima, hueco del asesino).
	var gained int32
	if killer != nil && killer.IsAlive() && carried > 0 {
		if def, err := unit.Lookup(killer.Type); err == nil {
			room := def.LootCarry - s.state.carrying(killer)
			if room > 0 {
				gained = min(carried, room)
			}
		}
	}

	v.HP = 0
	v.Status = unit.StatusDead
	v.Loot = 0
	v.SetTile(at)

	d := Death{
		VictimID: v.ID, VictimPlayer: v.PlayerID, At: at, Tick: s.state.Tick(),
		Loot: gained, Lost: carried - gained, CityID: v.CityID,
		VictimType: v.Type, AtMs: nowMs,
	}
	if def, err := unit.Lookup(v.Type); err == nil {
		d.PopulationCost = def.PopulationCost
	}
	if killer != nil {
		d.KillerID, d.KillerPlayer = killer.ID, killer.PlayerID
	}

	// La población de su ciudad (INV-CITY-017).
	if c, ok := s.cityOfUnit(v); ok {
		c.Population -= d.PopulationCost
		if c.Population < 0 {
			c.Population = 0
		}
		pop := c.Population
		s.broadcastCity(c, protocol.CityUpdatePayload{Population: &pop})
	}

	cx, cy := s.state.World().ChunkOf(at.X, at.Y)
	s.deps.Broadcaster.BroadcastChunk(cx, cy, protocol.TypeEntityDespawn,
		protocol.EntityDespawnPayload{ID: v.ID, Reason: protocol.DespawnDead})
	s.state.RemoveUnit(v.ID)

	// Quien atacaba a la víctima se queda sin objetivo.
	for _, id := range s.attackersOf(v.ID) {
		if a, ok := s.state.Unit(id); ok {
			s.endEngagement(a, true)
		}
	}

	if gained > 0 {
		killer.Loot += gained
		s.state.MarkDirty(killer.ID)
		loot := s.state.carrying(killer)
		kx, ky := s.state.World().ChunkOf(killer.X, killer.Y)
		s.deps.Broadcaster.BroadcastChunk(kx, ky, protocol.TypeEntityUpdate,
			protocol.EntityUpdatePayload{ID: killer.ID, Carrying: &loot})
	}
	s.deps.Log.Info("unidad muerta", "unit_id", v.ID, "killer_id", d.KillerID, "loot", gained, "lost", d.Lost)
	s.submitDeath(d)
}

func (s *Simulation) attackersOf(targetID int64) []int64 {
	var out []int64
	for id, e := range s.state.combat.engagements {
		if e.targetID == targetID {
			out = append(out, id)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func (s *Simulation) submitDeath(d Death) {
	if s.deps.Combat == nil {
		return
	}
	store := s.deps.Combat
	s.deps.Persister.Submit(Job{
		Name: JobDeath,
		Key:  UnitKey(d.VictimID),
		Run:  func(ctx context.Context) error { return store.PersistDeath(ctx, d) },
		OnPermanentFailure: func(err error) {
			s.deps.Log.Error("no se pudo escribir la muerte; se reintentará en el siguiente volcado",
				"err", err, "unit_id", d.VictimID)
			s.reinject(DeathPersistFailed{Death: d})
		},
	})
}

func (s *Simulation) handleDeathPersistFailed(cmd DeathPersistFailed) {
	s.state.combat.pendingDeaths[cmd.Death.VictimID] = cmd.Death
}

// flushDeaths reencola las muertes descartadas, en orden de víctima.
func (s *Simulation) flushDeaths() {
	if len(s.state.combat.pendingDeaths) == 0 {
		return
	}
	ids := make([]int64, 0, len(s.state.combat.pendingDeaths))
	for id := range s.state.combat.pendingDeaths {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	pending := s.state.combat.pendingDeaths
	s.state.combat.pendingDeaths = make(map[int64]Death)
	for _, id := range ids {
		s.submitDeath(pending[id])
	}
}

// depositLoot deja el botín de una unidad que acaba de pararse en el tile de
// depósito de su ciudad (RN-COMBAT-010): lo que suma la ciudad es lo que
// pierde la unidad (INV-COMBAT-003).
func (s *Simulation) depositLoot(u *unit.Unit, at world.Tile) {
	if u.Loot <= 0 {
		return
	}
	c, ok := s.cityOfUnit(u)
	if !ok {
		return
	}
	depot, ok := s.depositTile(c)
	if !ok || depot != at {
		return
	}
	amount := u.Loot
	u.Loot = 0
	s.state.MarkDirty(u.ID)
	s.deposit(c, amount)
	// Lo que quede es carga de saqueo pendiente de confirmar (ADR-026 §4).
	left := s.state.carrying(u)
	cx, cy := s.state.World().ChunkOf(u.X, u.Y)
	s.deps.Broadcaster.BroadcastChunk(cx, cy, protocol.TypeEntityUpdate,
		protocol.EntityUpdatePayload{ID: u.ID, Carrying: &left})
}

func chebyshevTiles(a, b world.Tile) int32 {
	dx, dy := a.X-b.X, a.Y-b.Y
	if dx < 0 {
		dx = -dx
	}
	if dy < 0 {
		dy = -dy
	}
	if dx > dy {
		return dx
	}
	return dy
}
