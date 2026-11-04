package simulation

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/economy"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// El saqueo en la simulación (SPEC-COMBAT-PILLAGE, ADR-026).
//
// Una orden unit.pillage abre un enfrentamiento cuyo objetivo es una ciudad: el
// mismo engagement de unit.attack con cityID en lugar de targetID, de modo que
// toda orden nueva, cancelación o muerte lo termina por los caminos de siempre.
// La fase 4 lo lleva hasta el anillo de la muralla y, desde allí, cada intervalo
// de ataque roba madera del almacén. Cada golpe es un débito en dos fases: el
// tick reserva (stock −, carga pendiente +) y la transacción, con la clave de la
// ciudad víctima, lo confirma o lo devuelve por el buzón.

// PillageCity ordena a un guerrero propio saquear una ciudad ajena.
type PillageCity struct {
	PlayerID  uuid.UUID
	SessionID uuid.UUID
	RequestID string
	UnitID    int64
	CityID    int64
}

func (PillageCity) commandType() string { return protocol.TypeUnitPillage }

// Pillage es lo que escribe un golpe de saqueo: el débito del stock de la
// víctima, como delta versionado, y su CityPillaged (ADR-026 §1).
type Pillage struct {
	AttackerID     int64
	AttackerPlayer uuid.UUID
	CityID         int64
	VictimPlayer   uuid.UUID
	// Amount es lo robado; Stock.Delta es «depósitos sin volcar − Amount».
	Amount int32
	Stock  StockDelta
	At     world.Tile
	Tick   uint64
	// AtMs es el instante del golpe en la simulación (ADR-028 §3).
	AtMs int64
}

// PillageResolved lo devuelve la cola con el resultado de un golpe de saqueo.
type PillageResolved struct {
	UnitID int64
	CityID int64
	Amount int32
	// Carried son los depósitos sin volcar que viajaban con el débito.
	Carried int64
	// Failed: la transacción no escribió nada y lo robado vuelve a la víctima.
	Failed bool
}

func (PillageResolved) commandType() string { return "city.pillage_resolved" }

// JobPillage es el nombre del trabajo de la cola que escribe un golpe de saqueo.
const JobPillage = "city.pillage"

// Valores por defecto de SPEC-COMBAT-PILLAGE §4.2.
const (
	defaultPillageWoodPerHit int32 = 5
	defaultPillageStockFloor int64 = 50
)

func (s *Simulation) pillagePerHit() int32 {
	if s.deps.PillageWoodPerHit > 0 {
		return s.deps.PillageWoodPerHit
	}
	return defaultPillageWoodPerHit
}

func (s *Simulation) pillageFloor() int64 {
	if s.deps.PillageStockFloor > 0 {
		return s.deps.PillageStockFloor
	}
	return defaultPillageStockFloor
}

// PendingLoot es la carga robada que espera su transacción (tests).
func (s *State) PendingLoot(unitID int64) int32 { return s.combat.pendingLoot[unitID] }

// Pillaging dice qué ciudad saquea una unidad (tests).
func (s *State) Pillaging(attackerID int64) (int64, bool) {
	e, ok := s.combat.engagements[attackerID]
	if !ok || e.cityID == 0 {
		return 0, false
	}
	return e.cityID, true
}

// carrying es la carga que ve el cliente: la confirmada y la pendiente.
func (s *State) carrying(u *unit.Unit) int32 { return u.Loot + s.combat.pendingLoot[u.ID] }

// ─────────────────────────────────────────────────────────────
// unit.pillage
// ─────────────────────────────────────────────────────────────

func (s *Simulation) handlePillageCity(cmd PillageCity) {
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
		reject(protocol.CodeUnitNotMovable, "esta unidad no saquea")
		return
	}
	c, ok := s.state.City(cmd.CityID)
	if !ok {
		reject(protocol.CodeCityNotFound, "la ciudad no existe")
		return
	}
	posA := s.authoritativePosition(a, nowMs)
	if code, msg := s.pillageBlocked(a, c, posA, s.deps.Clock.Now()); code != "" {
		reject(code, msg)
		return
	}

	// Sustituye cualquier orden anterior del guerrero (RN-COMBAT-025).
	s.stopWorking(a)
	s.endEngagement(a, false)
	e := &engagement{attackerID: a.ID, cityID: c.ID, lastRepathTick: s.state.Tick()}

	if c.IsAdjacent(posA.X, posA.Y) {
		s.state.combat.engagements[a.ID] = e
		s.startAttacking(a, e, nowMs)
		return
	}
	if err := s.approachWall(a, e, c, posA, def, nowMs); err != nil {
		reject(pathErrorCode(err), err.Error())
		return
	}
}

// pillageBlocked devuelve el código que impide saquear la ciudad, o "" si nada
// lo impide. Es el mismo predicado al ordenar y en cada golpe (RN-COMBAT-020,
// INV-COMBAT-002), y el punto único de la protección para el saqueo
// (INV-CITY-006): la ciudad cuenta como objetivo y su dueño como el de la
// víctima.
func (s *Simulation) pillageBlocked(a *unit.Unit, c *city.City, posA world.Tile, now time.Time) (string, string) {
	if c.OwnerPlayerID == a.PlayerID {
		return protocol.CodeInvalidTarget, "no puedes saquear tu propia ciudad"
	}
	if c.ProtectedAt(now) {
		return protocol.CodeCityProtected, "esa ciudad está protegida"
	}
	// RN-COMBAT-018: quien está protegido tampoco saquea.
	if own, ok := s.state.CityOf(a.PlayerID); ok && own.ProtectedAt(now) {
		return protocol.CodeCityProtected, "estás protegido: no puedes saquear"
	}
	if s.state.AtPeace(a.PlayerID, c.OwnerPlayerID, now) {
		return protocol.CodeInvalidTarget, "un tratado de paz impide saquear a ese jugador"
	}
	if _, ok := s.state.SafeZoneAt(posA.X, posA.Y); ok {
		return protocol.CodeInvalidTarget, "no se saquea desde una zona segura"
	}
	return "", ""
}

// errNoWallTile: ningún tile del anillo de la muralla es transitable.
var errNoWallTile = errors.New("no hay ningún tile transitable junto a la muralla")

// approachWall lleva al guerrero al tile transitable del anillo de la muralla
// más cercano a él (Chebyshev; empate: menor y, después menor x) al que haya
// ruta (SPEC-COMBAT-PILLAGE §4.1). Si encuentra ruta, instala el saqueo.
func (s *Simulation) approachWall(a *unit.Unit, e *engagement, c *city.City, posA world.Tile, def unit.Definition, nowMs int64) error {
	w := s.state.World()
	var candidates []world.Tile
	minX, minY, maxX, maxY := c.UrbanBounds()
	for y := minY - 1; y <= maxY+1; y++ {
		for x := minX - 1; x <= maxX+1; x++ {
			if c.IsAdjacent(x, y) && w.InBounds(x, y) && w.IsWalkable(x, y) {
				candidates = append(candidates, world.Tile{X: x, Y: y})
			}
		}
	}
	if len(candidates) == 0 {
		return errNoWallTile
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

// pillageHit es un golpe de saqueo que vence en el tick.
type pillageHit struct {
	attacker *unit.Unit
	city     *city.City
	e        *engagement
}

// stepPillage hace avanzar un saqueo en la fase 4 y devuelve su golpe si vence
// en este tick. Los golpes se aplican después de los golpes a unidades
// (RN-COMBAT-022).
func (s *Simulation) stepPillage(a *unit.Unit, e *engagement, nowMs int64, now time.Time) (pillageHit, bool) {
	c, ok := s.state.City(e.cityID)
	if !ok || a.Status == unit.StatusGarrisoned || s.state.IsEntering(a.ID) {
		s.endEngagement(a, true)
		return pillageHit{}, false
	}
	def, err := unit.Lookup(a.Type)
	if err != nil || !def.CanAttack() {
		s.endEngagement(a, true)
		return pillageHit{}, false
	}
	posA := s.authoritativePosition(a, nowMs)
	if code, _ := s.pillageBlocked(a, c, posA, now); code != "" {
		s.endEngagement(a, true)
		return pillageHit{}, false
	}
	// INV-COMBAT-005: saquear exige al dueño del guerrero presente.
	if own, ok := s.state.CityOf(a.PlayerID); !ok || !s.ownerPresent(own) {
		s.endEngagement(a, true)
		return pillageHit{}, false
	}
	if !c.IsAdjacent(posA.X, posA.Y) {
		if _, moving := s.state.Movement(a.ID); moving {
			return pillageHit{}, false // de camino a la muralla
		}
		// Parado fuera del anillo: la ruta se detuvo o se perdió. Un intento más.
		if err := s.approachWall(a, e, c, posA, def, nowMs); err != nil {
			s.endEngagement(a, true)
		}
		return pillageHit{}, false
	}
	s.startAttacking(a, e, nowMs)
	if nowMs < e.nextHitAtMs {
		return pillageHit{}, false
	}
	e.nextHitAtMs += def.AttackIntervalMs
	return pillageHit{attacker: a, city: c, e: e}, true
}

// applyPillage aplica un golpe de saqueo contra el stock que dejaron los
// anteriores del tick (RN-COMBAT-021 a RN-COMBAT-023) y dice si robó algo.
func (s *Simulation) applyPillage(h pillageHit, nowMs int64) bool {
	a, c := h.attacker, h.city
	if !a.IsAlive() {
		return false
	}
	if _, ok := s.state.combat.engagements[a.ID]; !ok {
		return false // lo terminó algo del mismo tick
	}
	def, err := unit.Lookup(a.Type)
	if err != nil {
		s.endEngagement(a, true)
		return false
	}
	eco := s.state.economyOf(c.ID)
	room := int64(def.LootCarry - s.state.carrying(a))
	amount := min(int64(s.pillagePerHit()), room, eco.wood.Amount-s.pillageFloor())
	if amount <= 0 {
		// Carga llena o almacén en el suelo: no hay golpe (RN-COMBAT-021).
		s.endEngagement(a, true)
		return false
	}
	if eco.wood.Amount-amount < s.pillageFloor() {
		// INV-COMBAT-004. No hay ruta conocida: el cálculo de arriba lo impide.
		s.deps.Log.Error("invariant_violation", "inv_id", "INV-COMBAT-004", "city_id", c.ID, "amount", amount)
		s.endEngagement(a, true)
		return false
	}

	// La reserva (ADR-026 §1): la madera sale ya de la RAM y entra como carga
	// pendiente; los depósitos sin volcar viajan con el débito.
	if err := eco.wood.Debit(economy.Cost{Resource: economy.ResourceWood, Amount: amount}); err != nil {
		s.endEngagement(a, true)
		return false
	}
	carried := eco.unflushed
	eco.unflushed = 0
	delete(s.state.stockDirty, c.ID)
	s.state.combat.pendingLoot[a.ID] += int32(amount)

	p := Pillage{
		AttackerID: a.ID, AttackerPlayer: a.PlayerID, CityID: c.ID, VictimPlayer: c.OwnerPlayerID,
		Amount: int32(amount), At: s.authoritativePosition(a, nowMs), Tick: s.state.Tick(), AtMs: nowMs,
		Stock: StockDelta{CityID: c.ID, Resource: economy.ResourceWood, Delta: carried - amount, Version: eco.wood.Version},
	}
	s.submitPillage(p, carried)

	s.broadcastStocks(c)
	carry := s.state.carrying(a)
	cx, cy := s.state.World().ChunkOf(a.X, a.Y)
	s.deps.Broadcaster.BroadcastChunk(cx, cy, protocol.TypeEntityUpdate,
		protocol.EntityUpdatePayload{ID: a.ID, Carrying: &carry})
	// RN-COMBAT-028: el aviso, sólo para el dueño.
	at := nowMs
	s.deps.Broadcaster.SendToPlayer(c.OwnerPlayerID, protocol.TypeCityUpdate, "",
		protocol.CityUpdatePayload{ID: c.ID, PillagedAtMs: &at})

	// Carga llena: el saqueo termina y el guerrero queda IDLE junto a la
	// muralla; volver es decisión de su dueño (RN-COMBAT-025, Q6).
	if carry >= def.LootCarry {
		s.endEngagement(a, true)
	}
	return true
}

// raiseCityAlarm despierta a los guerreros del saqueado cerca del centro de su
// ciudad (RN-COMBAT-027): el golpe cuenta como uno sobre una unidad suya en el
// centro.
func (s *Simulation) raiseCityAlarm(attacker *unit.Unit, c *city.City, nowMs int64) {
	if !attacker.IsAlive() {
		return
	}
	for _, g := range s.guardsNear(c.OwnerPlayerID, c.Center()) {
		s.respond(g, attacker, nowMs)
	}
}

// ─────────────────────────────────────────────────────────────
// Persistencia en dos fases (ADR-026)
// ─────────────────────────────────────────────────────────────

func (s *Simulation) submitPillage(p Pillage, carried int64) {
	resolved := PillageResolved{UnitID: p.AttackerID, CityID: p.CityID, Amount: p.Amount, Carried: carried}
	if s.deps.Combat == nil {
		// Sin almacén durable (tests de simulación que no lo miran): se
		// confirma en el acto, como si la transacción hubiera ido bien.
		s.handlePillageResolved(resolved)
		return
	}
	store := s.deps.Combat
	s.deps.Persister.Submit(Job{
		Name: JobPillage,
		Key:  CityKey(p.CityID),
		Run: func(ctx context.Context) error {
			err := store.PersistPillage(ctx, p)
			if err == nil {
				s.reinject(resolved)
				return nil
			}
			if errors.Is(err, economy.ErrInsufficientResources) {
				// Un rechazo de dominio no se reintenta: no se escribió nada.
				r := resolved
				r.Failed = true
				s.reinject(r)
				return nil
			}
			return err
		},
		OnPermanentFailure: func(err error) {
			s.deps.Log.Error("no se pudo escribir el golpe de saqueo; la madera vuelve a la ciudad",
				"err", err, "city_id", p.CityID, "unit_id", p.AttackerID, "amount", p.Amount)
			r := resolved
			r.Failed = true
			s.reinject(r)
		},
	})
}

// handlePillageResolved confirma la carga pendiente o devuelve lo robado
// (ADR-026 §1, §4).
func (s *Simulation) handlePillageResolved(cmd PillageResolved) {
	u, alive := s.state.Unit(cmd.UnitID)
	if alive {
		left := s.state.combat.pendingLoot[u.ID] - cmd.Amount
		if left > 0 {
			s.state.combat.pendingLoot[u.ID] = left
		} else {
			delete(s.state.combat.pendingLoot, u.ID)
		}
	}

	if cmd.Failed {
		if c, ok := s.state.City(cmd.CityID); ok {
			eco := s.state.economyOf(c.ID)
			_ = eco.wood.Credit(int64(cmd.Amount))
			eco.unflushed += cmd.Carried
			if eco.unflushed > 0 {
				s.state.stockDirty[c.ID] = struct{}{}
			}
			s.broadcastStocks(c)
		}
		if alive {
			carry := s.state.carrying(u)
			cx, cy := s.state.World().ChunkOf(u.X, u.Y)
			s.deps.Broadcaster.BroadcastChunk(cx, cy, protocol.TypeEntityUpdate,
				protocol.EntityUpdatePayload{ID: u.ID, Carrying: &carry})
		}
		return
	}

	// Confirmado. Si la unidad murió entretanto, la pendiente no se heredó y
	// esa madera se pierde (ADR-026 §4).
	if !alive {
		return
	}
	u.Loot += cmd.Amount
	s.state.MarkDirty(u.ID)
	// Si ya espera parada en su depósito, deja la carga ahora.
	if _, moving := s.state.Movement(u.ID); !moving && u.Status == unit.StatusIdle {
		s.depositLoot(u, s.authoritativePosition(u, s.deps.Clock.NowMs()))
	}
}
