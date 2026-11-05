package simulation

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/building"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/eventlog"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// El asedio en la simulación (SPEC-COMBAT-SIEGE, ADR-029).
//
// Una orden unit.demolish abre un enfrentamiento cuyo objetivo es una casa: el
// mismo engagement de unit.attack con buildingID, de modo que toda orden nueva,
// cancelación o muerte lo termina por los caminos de siempre. La fase 4 lleva
// al guerrero hasta un tile junto a la casa y, desde allí, cada intervalo de
// ataque le quita vida. La vida de una casa dañada vive sólo en la RAM y se
// olvida (RN-COMBAT-032); lo durable es la destrucción, optimista como la
// compleción de una casa: la RAM y los mensajes van primero y la escritura se
// reencola si la cola la descarta (ADR-029 §4).

// DemolishBuilding ordena a un guerrero propio derribar una casa ajena.
type DemolishBuilding struct {
	PlayerID   uuid.UUID
	SessionID  uuid.UUID
	RequestID  string
	UnitID     int64
	BuildingID int64
}

func (DemolishBuilding) commandType() string { return protocol.TypeUnitDemolish }

// BuildingDestruction es lo que escribe la destrucción de una casa
// (ADR-029 §4).
type BuildingDestruction struct {
	BuildingID int64
	CityID     int64
	// VictimPlayer es el dueño de la ciudad; AttackerPlayer y AttackerID, quien
	// la derribó.
	VictimPlayer   uuid.UUID
	AttackerPlayer uuid.UUID
	AttackerID     int64
	Type           building.Type
	At             world.Tile
	// Bonus es lo que la casa suma al límite si en la base está completa: la
	// escritura lo resta según el estado de la base, no el de la RAM.
	Bonus int32
	Tick  uint64
	// AtMs es el instante del golpe en la simulación (ADR-028 §3).
	AtMs int64
}

// BuildingDestroyResolved lo devuelve la cola cuando la destrucción está
// escrita: el tile de la casa vuelve a admitir otra (ADR-029 §5).
type BuildingDestroyResolved struct {
	BuildingID int64
	Tile       world.Tile
}

func (BuildingDestroyResolved) commandType() string { return "building.destroy_resolved" }

// BuildingDestroyPersistFailed lo devuelve la cola cuando descarta la
// escritura de una destrucción: se reencola en el siguiente volcado
// (ADR-029 §4, ADR-022).
type BuildingDestroyPersistFailed struct{ Destruction BuildingDestruction }

func (BuildingDestroyPersistFailed) commandType() string { return "building.destroy_persist_failed" }

// JobBuildingDestroy es el nombre del trabajo de la cola que escribe la
// destrucción de una casa.
const JobBuildingDestroy = "building.destroy"

// Valor por defecto de SPEC-COMBAT-SIEGE §4.2.
const defaultSiegeDamageReset = 5 * time.Minute

func (s *Simulation) siegeDamageResetMs() int64 {
	if s.deps.SiegeDamageReset > 0 {
		return s.deps.SiegeDamageReset.Milliseconds()
	}
	return defaultSiegeDamageReset.Milliseconds()
}

// houseDamage es la vida que le queda a una casa dañada y el instante de su
// último golpe.
type houseDamage struct {
	hp        int32
	lastHitMs int64
}

// siegeState es lo que la simulación sabe del asedio.
type siegeState struct {
	// damage es la vida de las casas dañadas, por id. Una casa sin entrada
	// está entera.
	damage map[int64]houseDamage
	// destroying son las casas derribadas cuya escritura no ha vuelto, por
	// tile: el tile sigue ocupado para city.build (ADR-029 §5).
	destroying map[world.Tile]int64
	// pendingDestructions son las destrucciones que la cola descartó, por casa:
	// el volcado las reencola (ADR-029 §4).
	pendingDestructions map[int64]BuildingDestruction
}

func newSiegeState() siegeState {
	return siegeState{
		damage:              make(map[int64]houseDamage),
		destroying:          make(map[world.Tile]int64),
		pendingDestructions: make(map[int64]BuildingDestruction),
	}
}

// Demolishing dice qué casa derriba una unidad (tests).
func (s *State) Demolishing(attackerID int64) (int64, bool) {
	e, ok := s.combat.engagements[attackerID]
	if !ok || e.buildingID == 0 {
		return 0, false
	}
	return e.buildingID, true
}

// BuildingHP devuelve la vida de una casa en pie y su máximo.
func (s *State) BuildingHP(id int64) (hp, maxHP int32, ok bool) {
	b, ok := s.buildings[id]
	if !ok {
		return 0, 0, false
	}
	hp, maxHP = s.houseHP(b)
	return hp, maxHP, true
}

// houseHP es la vida de una casa en pie: la de su daño, o la entera.
func (s *State) houseHP(b *building.Building) (hp, maxHP int32) {
	maxHP, ok := s.buildingMaxHP(*b)
	if !ok {
		return 0, 0
	}
	if d, ok := s.siege.damage[b.ID]; ok {
		return d.hp, maxHP
	}
	return maxHP, maxHP
}

// buildingMaxHP es la vida entera de un edificio: la de su tipo por el rasgo
// building.house_hp de la civilización del dueño de su ciudad, leída cada vez
// (SPEC-CIVILIZATION RN-CIV-006). El daño ya hecho no se reescala.
func (s *State) buildingMaxHP(b building.Building) (int32, bool) {
	def, err := building.Lookup(b.Type)
	if err != nil {
		return 0, false
	}
	c, ok := s.cities[b.CityID]
	if !ok {
		return def.MaxHP, true
	}
	return s.houseMaxHP(c.OwnerPlayerID, def.MaxHP), true
}

// buildingView es la vista de red de un edificio en pie, con su vida.
func (s *State) buildingView(b building.Building) protocol.BuildingView {
	v := BuildingView(b)
	if cur, ok := s.buildings[b.ID]; ok {
		v.HP, v.MaxHP = s.houseHP(cur)
	} else if maxHP, ok := s.buildingMaxHP(b); ok {
		v.HP, v.MaxHP = maxHP, maxHP
	}
	return v
}

// removeBuilding quita una casa derribada de la RAM. Su tile queda ocupado
// hasta que la destrucción esté escrita (ADR-029 §5).
func (s *State) removeBuilding(b *building.Building) {
	delete(s.buildings, b.ID)
	if s.buildingAt[b.Tile()] == b.ID {
		delete(s.buildingAt, b.Tile())
	}
	s.buildingIDs = removeSorted(s.buildingIDs, b.ID)
	delete(s.siege.damage, b.ID)
	delete(s.pendingCompletions, b.ID)
	s.siege.destroying[b.Tile()] = b.ID
}

// ─────────────────────────────────────────────────────────────
// unit.demolish
// ─────────────────────────────────────────────────────────────

func (s *Simulation) handleDemolishBuilding(cmd DemolishBuilding) {
	nowMs := s.deps.Clock.NowMs()
	reject := func(code, msg string) { s.economyError(cmd.PlayerID, cmd.RequestID, code, msg) }

	a, ok := s.state.Unit(cmd.UnitID)
	if !ok {
		reject(protocol.CodeUnitNotFound, "la unidad no existe")
		return
	}
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
		reject(protocol.CodeUnitNotMovable, "esta unidad no derriba casas")
		return
	}
	// Una casa que no existe, ya derribada o todavía sin confirmar responde
	// lo mismo: el catálogo no tiene un código de edificio (spec §5).
	b, ok := s.state.buildings[cmd.BuildingID]
	if !ok {
		reject(protocol.CodeInvalidTarget, "esa casa no existe")
		return
	}
	c, ok := s.state.City(b.CityID)
	if !ok {
		reject(protocol.CodeInvalidTarget, "esa casa no existe")
		return
	}
	posA := s.authoritativePosition(a, nowMs)
	if code, msg := s.siegeBlocked(a, c, posA, s.deps.Clock.Now()); code != "" {
		reject(code, msg)
		return
	}

	// Sustituye cualquier orden anterior del guerrero (RN-COMBAT-036).
	s.stopWorking(a)
	s.endEngagement(a, false)
	e := &engagement{attackerID: a.ID, buildingID: b.ID, lastRepathTick: s.state.Tick()}

	if chebyshevTiles(posA, b.Tile()) <= def.AttackRange {
		s.state.combat.engagements[a.ID] = e
		s.startAttacking(a, e, nowMs)
		return
	}
	if err := s.approachHouse(a, e, b, posA, def, nowMs); err != nil {
		reject(pathErrorCode(err), err.Error())
		return
	}
}

// siegeBlocked es el predicado del saqueo aplicado a la ciudad de la casa
// (RN-COMBAT-030, INV-COMBAT-002), con los mensajes del asedio.
func (s *Simulation) siegeBlocked(a *unit.Unit, c *city.City, posA world.Tile, now time.Time) (string, string) {
	return s.cityAttackBlocked(a, c, posA, now, siegeMessages)
}

var siegeMessages = cityAttackMessages{
	own:           "no puedes derribar las casas de tu propia ciudad",
	selfProtected: "estás protegido: no puedes derribar casas",
	peace:         "un tratado de paz impide derribar las casas de ese jugador",
	safeZone:      "no se derriba desde una zona segura",
}

// errNoHouseTile: ningún tile junto a la casa es transitable.
var errNoHouseTile = errors.New("no hay ningún tile transitable junto a esa casa")

// approachHouse lleva al guerrero al tile transitable junto a la casa más
// cercano a él (Chebyshev; empate: menor y, después menor x) al que haya ruta
// (SPEC-COMBAT-SIEGE §4.1). Si encuentra ruta, instala el asedio.
func (s *Simulation) approachHouse(a *unit.Unit, e *engagement, b *building.Building, posA world.Tile, def unit.Definition, nowMs int64) error {
	w := s.state.World()
	var candidates []world.Tile
	for y := b.Y - def.AttackRange; y <= b.Y+def.AttackRange; y++ {
		for x := b.X - def.AttackRange; x <= b.X+def.AttackRange; x++ {
			if (x == b.X && y == b.Y) || !w.InBounds(x, y) || !w.IsWalkable(x, y) {
				continue
			}
			candidates = append(candidates, world.Tile{X: x, Y: y})
		}
	}
	if len(candidates) == 0 {
		return errNoHouseTile
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		di, dj := chebyshevTiles(posA, candidates[i]), chebyshevTiles(posA, candidates[j])
		if di != dj {
			return di < dj
		}
		if candidates[i].Y != candidates[j].Y {
			return candidates[i].Y < candidates[j].Y
		}
		return candidates[i].X < candidates[j].X
	})
	var firstErr error
	for _, t := range candidates {
		path, err := s.chasePath(a, posA, t, def)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		s.state.combat.engagements[a.ID] = e
		s.beginMovement(a, posA, t, path, nowMs, nil)
		return nil
	}
	return firstErr
}

// ─────────────────────────────────────────────────────────────
// Fase 4
// ─────────────────────────────────────────────────────────────

// siegeHit es un golpe de asedio que vence en el tick.
type siegeHit struct {
	attacker *unit.Unit
	house    *building.Building
	city     *city.City
	damage   int32
}

// stepSiege hace avanzar un asedio en la fase 4 y devuelve su golpe si vence en
// este tick. Los golpes se aplican después de los golpes a unidades y de los de
// saqueo (RN-COMBAT-031).
func (s *Simulation) stepSiege(a *unit.Unit, e *engagement, nowMs int64, now time.Time) (siegeHit, bool) {
	b, ok := s.state.buildings[e.buildingID]
	if !ok || a.Status == unit.StatusGarrisoned || s.state.IsEntering(a.ID) {
		// La casa cayó (otro la derribó) o el guerrero ya no puede seguir.
		s.endEngagement(a, true)
		return siegeHit{}, false
	}
	c, ok := s.state.City(b.CityID)
	if !ok {
		s.endEngagement(a, true)
		return siegeHit{}, false
	}
	def, err := unit.Lookup(a.Type)
	if err != nil || !def.CanAttack() {
		s.endEngagement(a, true)
		return siegeHit{}, false
	}
	posA := s.authoritativePosition(a, nowMs)
	if code, _ := s.siegeBlocked(a, c, posA, now); code != "" {
		s.endEngagement(a, true)
		return siegeHit{}, false
	}
	// INV-COMBAT-005: derribar exige al dueño del guerrero presente
	// (RN-COMBAT-035).
	if own, ok := s.state.CityOf(a.PlayerID); !ok || !s.ownerPresent(own) {
		s.endEngagement(a, true)
		return siegeHit{}, false
	}
	if chebyshevTiles(posA, b.Tile()) > def.AttackRange {
		if _, moving := s.state.Movement(a.ID); moving {
			return siegeHit{}, false // de camino a la casa
		}
		// Parado lejos de la casa: la ruta se detuvo o se perdió. Un intento más.
		if err := s.approachHouse(a, e, b, posA, def, nowMs); err != nil {
			s.endEngagement(a, true)
		}
		return siegeHit{}, false
	}
	s.startAttacking(a, e, nowMs)
	if nowMs < e.nextHitAtMs {
		return siegeHit{}, false
	}
	e.nextHitAtMs += def.AttackIntervalMs
	return siegeHit{attacker: a, house: b, city: c, damage: def.AttackDamage}, true
}

// applySiege aplica un golpe de asedio contra la vida que dejaron los anteriores
// del tick (RN-COMBAT-031 a RN-COMBAT-033) y dice si golpeó.
func (s *Simulation) applySiege(h siegeHit, nowMs int64) bool {
	a, b, c := h.attacker, h.house, h.city
	if !a.IsAlive() {
		return false
	}
	if e, ok := s.state.combat.engagements[a.ID]; !ok || e.buildingID != b.ID {
		return false // lo terminó algo del mismo tick
	}
	if cur, ok := s.state.buildings[b.ID]; !ok || cur != b {
		// Otro golpe del mismo tick ya la derribó.
		s.endEngagement(a, true)
		return false
	}

	hp, _ := s.state.houseHP(b)
	hp -= h.damage
	if hp < 0 {
		hp = 0
	}
	s.state.siege.damage[b.ID] = houseDamage{hp: hp, lastHitMs: nowMs}

	// RN-COMBAT-038: el aviso, sólo para el dueño.
	at := nowMs
	s.deps.Broadcaster.SendToPlayer(c.OwnerPlayerID, protocol.TypeCityUpdate, "",
		protocol.CityUpdatePayload{ID: c.ID, BesiegedAtMs: &at})

	if hp > 0 {
		cx, cy := s.state.World().ChunkOf(b.X, b.Y)
		s.deps.Broadcaster.BroadcastChunk(cx, cy, protocol.TypeBuildingUpdate,
			protocol.BuildingUpdatePayload{ID: b.ID, Status: string(b.Status), HP: &hp})
		return true
	}
	s.destroyHouse(b, c, a, nowMs)
	// El asedio termina con la casa en el suelo; no sigue con la siguiente
	// (RN-COMBAT-036, Q10).
	s.endEngagement(a, true)
	return true
}

// destroyHouse derriba una casa en la RAM, lo difunde y encola su escritura
// (RN-COMBAT-033, ADR-029 §4).
func (s *Simulation) destroyHouse(b *building.Building, c *city.City, attacker *unit.Unit, nowMs int64) {
	def, err := building.Lookup(b.Type)
	if err != nil {
		return
	}
	wasComplete, err := b.Destroy()
	if err != nil {
		return
	}
	s.state.removeBuilding(b)
	// INV-ECON-004: el límite pierde el bono de la casa sólo si estaba
	// completa; la población no cambia y puede quedar por encima (ADR-029 §1).
	if wasComplete {
		c.PopulationLimit -= def.PopulationBonus
	}

	cx, cy := s.state.World().ChunkOf(b.X, b.Y)
	s.deps.Broadcaster.BroadcastChunk(cx, cy, protocol.TypeBuildingDestroyed,
		protocol.BuildingDestroyedPayload{ID: b.ID, CityID: c.ID})
	// El aviso en vivo, al dueño del atacante; el de la casa ya tiene
	// besiegedAtMs (SPEC-LIVE-NOTICES Q1). Aquí y no en submitDestruction, que
	// el volcado repite (ADR-033 §2).
	tile := b.Tile()
	s.sendNotices(eventlog.Event{
		Type: eventlog.EventBuildingDestroyed, PlayerID: c.OwnerPlayerID, Counterpart: attacker.PlayerID,
		AtMs: nowMs, X: tile.X, Y: tile.Y, HasTile: true,
	})
	if wasComplete {
		limit := c.PopulationLimit
		s.broadcastCity(c, protocol.CityUpdatePayload{PopulationLimit: &limit})
	}

	s.submitDestruction(BuildingDestruction{
		BuildingID: b.ID, CityID: c.ID, VictimPlayer: c.OwnerPlayerID,
		AttackerPlayer: attacker.PlayerID, AttackerID: attacker.ID,
		Type: b.Type, At: b.Tile(), Bonus: def.PopulationBonus, Tick: s.state.Tick(), AtMs: nowMs,
	})
}

// ─────────────────────────────────────────────────────────────
// Persistencia (ADR-029 §4)
// ─────────────────────────────────────────────────────────────

func (s *Simulation) submitDestruction(d BuildingDestruction) {
	resolved := BuildingDestroyResolved{BuildingID: d.BuildingID, Tile: d.At}
	if s.deps.Economy == nil {
		// Sin almacén durable (tests de simulación que no lo miran): se
		// confirma en el acto, como si la transacción hubiera ido bien.
		s.handleBuildingDestroyResolved(resolved)
		return
	}
	store := s.deps.Economy
	s.deps.Persister.Submit(Job{
		Name: JobBuildingDestroy,
		Key:  CityKey(d.CityID),
		Run: func(ctx context.Context) error {
			if err := store.DestroyBuilding(ctx, d); err != nil {
				return err
			}
			s.reinject(resolved)
			return nil
		},
		OnPermanentFailure: func(err error) {
			s.deps.Log.Error("no se pudo escribir la destrucción de la casa; se reintentará en el siguiente volcado",
				"err", err, "building_id", d.BuildingID, "city_id", d.CityID)
			s.reinject(BuildingDestroyPersistFailed{Destruction: d})
		},
	})
}

// handleBuildingDestroyResolved libera el tile de la casa derribada.
func (s *Simulation) handleBuildingDestroyResolved(cmd BuildingDestroyResolved) {
	if s.state.siege.destroying[cmd.Tile] == cmd.BuildingID {
		delete(s.state.siege.destroying, cmd.Tile)
	}
	delete(s.state.siege.pendingDestructions, cmd.BuildingID)
}

func (s *Simulation) handleBuildingDestroyPersistFailed(cmd BuildingDestroyPersistFailed) {
	s.state.siege.pendingDestructions[cmd.Destruction.BuildingID] = cmd.Destruction
}

// flushDestructions reencola las destrucciones descartadas, en orden de casa.
// Es idempotente: la escritura no toca una casa ya derribada.
func (s *Simulation) flushDestructions() {
	if len(s.state.siege.pendingDestructions) == 0 {
		return
	}
	ids := make([]int64, 0, len(s.state.siege.pendingDestructions))
	for id := range s.state.siege.pendingDestructions {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	pending := s.state.siege.pendingDestructions
	s.state.siege.pendingDestructions = make(map[int64]BuildingDestruction)
	for _, id := range ids {
		s.submitDestruction(pending[id])
	}
}

// ─────────────────────────────────────────────────────────────
// Fase 5: el daño que se olvida
// ─────────────────────────────────────────────────────────────

// forgetSiegeDamage deja enteras, en orden de id, las casas que llevan
// EO_SIEGE_DAMAGE_RESET_SECONDS sin golpes (RN-COMBAT-032).
func (s *Simulation) forgetSiegeDamage(nowMs int64) {
	if len(s.state.siege.damage) == 0 {
		return
	}
	reset := s.siegeDamageResetMs()
	var ids []int64
	for id, d := range s.state.siege.damage {
		if nowMs-d.lastHitMs >= reset {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		delete(s.state.siege.damage, id)
		b, ok := s.state.buildings[id]
		if !ok {
			continue
		}
		hp, _ := s.state.houseHP(b)
		cx, cy := s.state.World().ChunkOf(b.X, b.Y)
		s.deps.Broadcaster.BroadcastChunk(cx, cy, protocol.TypeBuildingUpdate,
			protocol.BuildingUpdatePayload{ID: b.ID, Status: string(b.Status), HP: &hp})
	}
}
