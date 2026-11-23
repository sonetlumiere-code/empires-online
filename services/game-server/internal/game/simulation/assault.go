package simulation

import (
	"context"
	"errors"
	"hash/fnv"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/cityfall"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/clan"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/eventlog"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/market"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/movement"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/trade"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/founding"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// La caída de una ciudad (SPEC-CITY-FALL, ADR-050).
//
// Una unidad que derriba asalta una ciudad en brecha: va al anillo de la
// ciudad, planta el estandarte y, si aguanta el plazo, la ciudad cae. El
// comienzo es un encargo en dos fases, como una reclamación de territorio
// (ADR-050 §2): el tick valida y aparta la ciudad y la unidad, la cola escribe
// la fila, y sólo con la confirmación el asalto existe para los demás. Lo que
// lo rompe se decide en la RAM y se escribe después. La caída es una orden en
// dos fases con las ciudades de los dos como claves: la cola arrasa y refunda
// en una transacción, y la RAM lo aplica todo en un solo comando al volver
// (ADR-050 §3), así que el vencido nunca está sin ciudad ni con dos
// (INV-CITY-020).

// Nombres de los trabajos de la caída (catálogo JobNames).
const (
	JobCityAssault    = "city.assault"
	JobCityAssaultEnd = "city.assault_end"
	JobCityFall       = "city.fall"
)

// msgCityFalling es el rechazo de una orden que cambiaría una ciudad mientras su
// caída espera la base (RN-CITY-026).
const msgCityFalling = "la ciudad está cayendo"

// ErrFallConflict es un rechazo de dominio de la base: otro asalto activo sobre
// la ciudad o con la unidad, un asalto que ya no está activo o una ciudad que
// ya cayó.
var ErrFallConflict = errors.New("el asalto choca con el estado de la base")

// CityFallStore escribe los asaltos y las caídas (ADR-050).
type CityFallStore interface {
	// StartAssault escribe el asalto ACTIVE y CityAssaultStarted. Devuelve el
	// id. Idempotente por petición.
	StartAssault(ctx context.Context, a AssaultStart) (int64, error)
	// EndAssault cierra un asalto sin caída. Idempotente.
	EndAssault(ctx context.Context, e AssaultEnd) error
	// FallCity es la transacción de ADR-050 §3.
	FallCity(ctx context.Context, f cityfall.Fall) (*cityfall.FallResult, error)
}

// RefoundSiteFunc busca el sitio de la ciudad nueva de un vencido fuera del
// tick: las ciudades en pie sin la caída, los tiles de los que alejarse y la
// semilla de la espiral (RN-CITY-027).
type RefoundSiteFunc func(ctx context.Context, existing, avoid []world.Tile, minDist int32, seed uint64) (cityfall.Site, error)

// AssaultStart es el comienzo de un asalto.
type AssaultStart struct {
	Assault cityfall.Assault
	Owner   uuid.UUID
	Tick    uint64
}

// AssaultEnd es el fin sin caída de un asalto.
type AssaultEnd struct {
	AssaultID int64
	CityID    int64
	Reason    string
}

// AssaultCity es unit.assault.
type AssaultCity struct {
	PlayerID  uuid.UUID
	SessionID uuid.UUID
	RequestID string
	UnitID    int64
	CityID    int64
}

func (AssaultCity) commandType() string { return protocol.TypeUnitAssault }

// AssaultResolved lo devuelve la cola con el resultado del comienzo. Code
// vacío: la fila está escrita con su id.
type AssaultResolved struct {
	CityID    int64
	PlayerID  uuid.UUID
	RequestID string
	AssaultID int64
	Code      string
	Message   string
}

func (AssaultResolved) commandType() string { return "city.assault_resolved" }

// AssaultEndPersistFailed es la compensación de un fin que no se escribió: el
// volcado siguiente lo reencola.
type AssaultEndPersistFailed struct{ End AssaultEnd }

func (AssaultEndPersistFailed) commandType() string { return "city.assault_end_failed" }

// FallResolved lo devuelve la cola con el resultado de una caída: lo escrito,
// un rechazo de dominio (Conflict) o la base caída (Failed).
type FallResolved struct {
	CityID   int64
	Result   *cityfall.FallResult
	Conflict bool
	Failed   bool
}

func (FallResolved) commandType() string { return "city.fall_resolved" }

// assaultState es un asalto con su situación en sus dos órdenes.
type assaultState struct {
	cityfall.Assault
	// pending: su comienzo espera la transacción; todavía no existe para nadie.
	pending bool
	// falling: su caída espera la transacción; nada lo rompe ya, y la ciudad
	// no admite órdenes que la cambien.
	falling bool
	fall    cityfall.Fall
	owner   uuid.UUID
}

// approachState es una unidad que va al anillo de una ciudad para asaltarla.
type approachState struct {
	cityID    int64
	requestID string
	sessionID uuid.UUID
}

type cityFallState struct {
	assaults    map[int64]*assaultState // por ciudad asaltada
	byUnit      map[int64]int64         // unidad → ciudad
	approaching map[int64]approachState // por unidad
	pendingEnds map[int64]AssaultEnd    // por id de asalto
	pendingFall map[int64]cityfall.Fall // por ciudad
	immunity    time.Duration
}

func newCityFallState() cityFallState {
	return cityFallState{
		assaults:    make(map[int64]*assaultState),
		byUnit:      make(map[int64]int64),
		approaching: make(map[int64]approachState),
		pendingEnds: make(map[int64]AssaultEnd),
		pendingFall: make(map[int64]cityfall.Fall),
	}
}

// ─────────────────────────────────────────────────────────────
// Estado
// ─────────────────────────────────────────────────────────────

// SetAssaults instala los asaltos activos leídos de la base al arrancar
// (ADR-050 §2). Una unidad viva, parada en su tile, vuelve a CLAIMING —el
// volcado la escribió IDLE—; una que no, se queda como está, y el primer
// barrido termina su asalto.
func (s *State) SetAssaults(as []cityfall.Assault) {
	for _, a := range as {
		c, ok := s.cities[a.CityID]
		if !ok {
			continue
		}
		s.cityFall.assaults[a.CityID] = &assaultState{Assault: a, owner: c.OwnerPlayerID}
		s.cityFall.byUnit[a.UnitID] = a.CityID
		u, ok := s.units[a.UnitID]
		if !ok || !u.IsAlive() || u.Status != unit.StatusIdle || u.Tile() != a.Tile() {
			continue
		}
		if _, moving := s.movements[u.ID]; moving {
			continue
		}
		u.Status = unit.StatusClaiming
	}
}

// ActiveAssault devuelve el asalto de una ciudad y si ya está confirmado.
func (s *State) ActiveAssault(cityID int64) (cityfall.Assault, bool, bool) {
	as, ok := s.cityFall.assaults[cityID]
	if !ok {
		return cityfall.Assault{}, false, false
	}
	return as.Assault, !as.pending, true
}

// CityFalling dice si una ciudad está cayendo: su caída espera la base y no
// admite órdenes que la cambien (RN-CITY-026).
func (s *State) CityFalling(cityID int64) bool {
	as, ok := s.cityFall.assaults[cityID]
	return ok && as.falling
}

// assaultView es el asalto confirmado de una ciudad, o nil.
func (s *State) assaultView(cityID int64) *protocol.CityAssaultView {
	as, ok := s.cityFall.assaults[cityID]
	if !ok || as.pending {
		return nil
	}
	return &protocol.CityAssaultView{
		PlayerID: as.PlayerID.String(), UnitID: as.UnitID, StartedAtMs: as.StartedAtMs, EndsAtMs: as.EndsAtMs,
	}
}

// fallImmuneView es el fin de la inmunidad de una ciudad, si aún no venció.
func (s *State) fallImmuneView(c *city.City, nowMs int64) *int64 {
	if !c.FallImmuneAt(nowMs, s.cityFall.immunity) {
		return nil
	}
	until := c.FallImmuneUntilMs(s.cityFall.immunity)
	return &until
}

// ─────────────────────────────────────────────────────────────
// unit.assault
// ─────────────────────────────────────────────────────────────

func (s *Simulation) assaultDuration() time.Duration {
	if s.deps.CityAssaultDuration > 0 {
		return s.deps.CityAssaultDuration
	}
	return cityfall.DefaultAssaultDuration
}

func (s *Simulation) refoundMinDistance() int32 {
	if s.deps.CityRefoundMinDistance > 0 {
		return s.deps.CityRefoundMinDistance
	}
	return cityfall.DefaultRefoundMinDistance
}

var assaultMessages = cityAttackMessages{
	own:           cityfall.ErrOwnCity.Error(),
	selfProtected: "estás protegido: no puedes asaltar ciudades",
	peace:         "un tratado de paz impide asaltar la ciudad de ese jugador",
	safeZone:      "no se asalta desde una zona segura",
}

// assaultBlocked es el predicado de agresión del asalto (RN-COMBAT-041): el del
// saqueo y el derribo, con la protección del asaltado sólo al ordenar.
func (s *Simulation) assaultBlocked(a *unit.Unit, c *city.City, posA world.Tile, now time.Time, ordering bool) (string, string) {
	return s.cityAttackBlockedFor(a, c, posA, now, assaultMessages, ordering)
}

// assaultTarget aplica RN-SIEGE-010 y RN-CITY-029 a una ciudad.
func (s *Simulation) assaultTarget(c *city.City, attacker uuid.UUID, nowMs int64) error {
	_, busy := s.state.cityFall.assaults[c.ID]
	return cityfall.CheckTarget(cityfall.Target{City: c, Walled: s.state.Walled(c.ID), Busy: busy},
		attacker, nowMs, s.state.cityFall.immunity)
}

// handleAssaultCity valida en el tick, lleva la unidad al anillo y, si ya está
// en él, empieza el asalto (RN-SIEGE-009 a RN-SIEGE-011, RN-COMBAT-041).
func (s *Simulation) handleAssaultCity(cmd AssaultCity) {
	nowMs := s.deps.Clock.NowMs()
	now := s.deps.Clock.Now()
	reject := func(code, msg string) { s.economyError(cmd.PlayerID, cmd.RequestID, code, msg) }

	u, ok := s.state.Unit(cmd.UnitID)
	if !ok {
		reject(protocol.CodeUnitNotFound, "la unidad no existe")
		return
	}
	if err := u.EnsureOwnedBy(cmd.PlayerID); err != nil {
		reject(protocol.CodeUnitNotOwned, "la unidad no te pertenece")
		return
	}
	switch u.Status {
	case unit.StatusDead:
		reject(protocol.CodeUnitDead, "la unidad está muerta")
		return
	case unit.StatusGarrisoned:
		reject(protocol.CodeUnitGarrisoned, "la unidad está guarnecida")
		return
	}
	if s.state.IsEntering(u.ID) {
		reject(protocol.CodeUnitNotMovable, "la unidad está entrando en una ciudad")
		return
	}
	// RN-SIEGE-009 e INV-COMBAT-009: una unidad que derriba, y no el ariete.
	def, err := unit.Lookup(u.Type)
	if err != nil || !def.CanDemolish() || def.IsSiegeEngine() {
		reject(protocol.CodeUnitNotMovable, "esta unidad no asalta ciudades")
		return
	}
	if s.deps.CityFall == nil {
		reject(protocol.CodeInternalError, "el asalto no está disponible")
		return
	}
	c, ok := s.state.City(cmd.CityID)
	if !ok {
		reject(protocol.CodeCityNotFound, "esa ciudad no existe")
		return
	}
	if _, ok := s.state.CityOf(cmd.PlayerID); !ok {
		reject(protocol.CodeInvalidTarget, "no tienes ciudad")
		return
	}
	if err := s.assaultTarget(c, cmd.PlayerID, nowMs); err != nil {
		reject(protocol.CodeInvalidTarget, err.Error())
		return
	}
	posA := s.authoritativePosition(u, nowMs)
	if code, msg := s.assaultBlocked(u, c, posA, now, true); code != "" {
		reject(code, msg)
		return
	}

	// Sustituye cualquier orden anterior (RN-SIEGE-009).
	s.stopWorking(u)
	s.endEngagement(u, false)
	s.abandonClaim(u)
	s.leaveAssault(u)
	if c.IsAdjacent(posA.X, posA.Y) {
		s.cancelActiveMovement(u, movement.ReasonReplaced, nowMs)
		s.startAssault(u, c, posA, cmd.RequestID, nowMs)
		return
	}
	if err := s.approachRing(u, c, posA, def, nowMs); err != nil {
		reject(pathErrorCode(err), err.Error())
		return
	}
	s.state.cityFall.approaching[u.ID] = approachState{cityID: c.ID, requestID: cmd.RequestID, sessionID: cmd.SessionID}
}

// approachRing lleva la unidad al tile transitable del anillo de la ciudad más
// cercano a ella (Chebyshev; empate: menor y, después menor x) al que haya
// ruta, como el saqueo (SPEC-CITY-FALL §4.1).
func (s *Simulation) approachRing(u *unit.Unit, c *city.City, posA world.Tile, def unit.Definition, nowMs int64) error {
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
		path, err := s.chasePath(u, posA, t, def)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		s.beginMovement(u, posA, t, path, nowMs, nil)
		return nil
	}
	return firstErr
}

// leaveAssault libera a una unidad de un asalto o de su camino hacia él. Un
// asalto activo lo termina el barrido.
func (s *Simulation) leaveAssault(u *unit.Unit) {
	delete(s.state.cityFall.approaching, u.ID)
	if u.Status == unit.StatusClaiming {
		if _, ok := s.state.cityFall.byUnit[u.ID]; ok {
			u.Status = unit.StatusIdle
			s.state.MarkDirty(u.ID)
			s.broadcastStatus(u)
		}
	}
}

// startAssault planta el estandarte y encola la fila (ADR-050 §2).
func (s *Simulation) startAssault(u *unit.Unit, c *city.City, pos world.Tile, requestID string, nowMs int64) {
	own, ok := s.state.CityOf(u.PlayerID)
	if !ok {
		return
	}
	u.Status = unit.StatusClaiming
	s.state.MarkDirty(u.ID)
	a := cityfall.Assault{
		CityID: c.ID, UnitID: u.ID, PlayerID: u.PlayerID, FromCityID: own.ID, X: pos.X, Y: pos.Y,
		StartedAtMs: nowMs, EndsAtMs: nowMs + s.assaultDuration().Milliseconds(), RequestID: requestID,
	}
	s.state.cityFall.assaults[c.ID] = &assaultState{Assault: a, pending: true, owner: c.OwnerPlayerID}
	s.state.cityFall.byUnit[u.ID] = c.ID

	start := AssaultStart{Assault: a, Owner: c.OwnerPlayerID, Tick: s.state.Tick()}
	resolved := AssaultResolved{CityID: c.ID, PlayerID: u.PlayerID, RequestID: requestID}
	store := s.deps.CityFall
	s.deps.Persister.Submit(Job{
		Name: JobCityAssault,
		Key:  CityKey(c.ID),
		Run: func(ctx context.Context) error {
			id, err := store.StartAssault(ctx, start)
			if err == nil {
				r := resolved
				r.AssaultID = id
				s.reinject(r)
				return nil
			}
			if errors.Is(err, ErrFallConflict) {
				r := resolved
				r.Code, r.Message = protocol.CodeInvalidTarget, cityfall.ErrBusy.Error()
				s.reinject(r)
				return nil
			}
			return err
		},
		OnPermanentFailure: func(err error) {
			s.deps.Log.Error("no se pudo escribir el asalto; la unidad queda libre", "err", err, "city_id", c.ID)
			r := resolved
			r.Code, r.Message = protocol.CodeInternalError, "no se pudo empezar el asalto"
			s.reinject(r)
		},
	})
}

// handleAssaultResolved confirma el asalto o lo deshace.
func (s *Simulation) handleAssaultResolved(cmd AssaultResolved) {
	as, ok := s.state.cityFall.assaults[cmd.CityID]
	if !ok || !as.pending || as.RequestID != cmd.RequestID || as.PlayerID != cmd.PlayerID {
		return
	}
	nowMs := s.deps.Clock.NowMs()
	if cmd.Code != "" {
		s.dropAssault(as)
		if u, ok := s.state.Unit(as.UnitID); ok && u.Status == unit.StatusClaiming {
			u.Status = unit.StatusIdle
			s.state.MarkDirty(u.ID)
		}
		s.economyError(cmd.PlayerID, cmd.RequestID, cmd.Code, cmd.Message)
		return
	}
	as.ID = cmd.AssaultID
	as.pending = false

	u, ok := s.state.Unit(as.UnitID)
	if !ok || !u.IsAlive() || u.Status != unit.StatusClaiming {
		s.endAssault(as, cityfall.EndUnit, nowMs)
		return
	}
	// Ahora existe para los demás (ADR-050 §2).
	s.broadcastStatus(u)
	if c, ok := s.state.City(as.CityID); ok {
		view := s.state.assaultView(c.ID)
		s.broadcastCity(c, protocol.CityUpdatePayload{Assault: &view})
		// RN-SIEGE-011: el aviso al asaltado, esté donde esté, y sus guardias
		// responden como a un golpe en el centro de su ciudad (RN-COMBAT-037).
		s.sendNotices(eventlog.Event{
			Type: eventlog.EventCityAssaultStarted, PlayerID: as.owner, Counterpart: as.PlayerID,
			AtMs: as.StartedAtMs, FallsAtMs: as.EndsAtMs, X: c.CenterX, Y: c.CenterY, HasTile: true,
		})
		for _, g := range s.guardsNear(as.owner, c.Center()) {
			s.respond(g, u, nowMs)
		}
	}
}

func (s *Simulation) dropAssault(as *assaultState) {
	delete(s.state.cityFall.assaults, as.CityID)
	if s.state.cityFall.byUnit[as.UnitID] == as.CityID {
		delete(s.state.cityFall.byUnit, as.UnitID)
	}
}

// endAssault termina un asalto sin caída: la RAM en el acto y la base después,
// con su reencolado.
func (s *Simulation) endAssault(as *assaultState, reason string, nowMs int64) {
	s.dropAssault(as)
	if u, ok := s.state.Unit(as.UnitID); ok && u.Status == unit.StatusClaiming {
		u.Status = unit.StatusIdle
		s.state.MarkDirty(u.ID)
		s.broadcastStatus(u)
	}
	if !as.pending {
		if c, ok := s.state.City(as.CityID); ok {
			var ninguno *protocol.CityAssaultView
			s.broadcastCity(c, protocol.CityUpdatePayload{Assault: &ninguno})
		}
	}
	s.deps.Log.Info("asalto terminado", "city_id", as.CityID, "unit_id", as.UnitID, "reason", reason)
	if as.ID == 0 {
		return
	}
	s.submitAssaultEnd(AssaultEnd{AssaultID: as.ID, CityID: as.CityID, Reason: reason})
}

func (s *Simulation) submitAssaultEnd(e AssaultEnd) {
	if s.deps.CityFall == nil {
		return
	}
	store := s.deps.CityFall
	s.deps.Persister.Submit(Job{
		Name: JobCityAssaultEnd,
		Key:  CityKey(e.CityID),
		Run:  func(ctx context.Context) error { return store.EndAssault(ctx, e) },
		OnPermanentFailure: func(err error) {
			s.deps.Log.Error("no se pudo escribir el fin del asalto; se reintentará en el siguiente volcado",
				"err", err, "assault_id", e.AssaultID)
			s.reinject(AssaultEndPersistFailed{End: e})
		},
	})
}

func (s *Simulation) handleAssaultEndPersistFailed(cmd AssaultEndPersistFailed) {
	s.state.cityFall.pendingEnds[cmd.End.AssaultID] = cmd.End
}

// ─────────────────────────────────────────────────────────────
// Fase 5: los que llegan, los que se rompen y los que vencen
// ─────────────────────────────────────────────────────────────

// sweepAssaults empieza los asaltos de las unidades que llegaron al anillo y
// recorre los activos en orden de ciudad: termina los que se rompieron
// (RN-SIEGE-011) y encola la caída de los que vencieron (RN-CITY-026).
func (s *Simulation) sweepAssaults(now time.Time) {
	nowMs := now.UnixMilli()
	if len(s.state.cityFall.approaching) > 0 {
		ids := make([]int64, 0, len(s.state.cityFall.approaching))
		for id := range s.state.cityFall.approaching {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		for _, id := range ids {
			s.arriveForAssault(id, now)
		}
	}
	if len(s.state.cityFall.assaults) == 0 {
		return
	}
	ids := make([]int64, 0, len(s.state.cityFall.assaults))
	for id := range s.state.cityFall.assaults {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		as, ok := s.state.cityFall.assaults[id]
		if !ok || as.pending || as.falling {
			continue
		}
		if reason := s.assaultBroken(as, now); reason != "" {
			s.endAssault(as, reason, nowMs)
			continue
		}
		if as.Due(nowMs) {
			s.startFall(as, now)
		}
	}
}

// arriveForAssault empieza el asalto de una unidad que llegó al anillo, o la
// olvida si dejó de ir hacia él.
func (s *Simulation) arriveForAssault(unitID int64, now time.Time) {
	ap := s.state.cityFall.approaching[unitID]
	u, ok := s.state.Unit(unitID)
	if !ok || !u.IsAlive() {
		delete(s.state.cityFall.approaching, unitID)
		return
	}
	if _, moving := s.state.Movement(unitID); moving {
		return
	}
	delete(s.state.cityFall.approaching, unitID)
	c, ok := s.state.City(ap.cityID)
	if !ok || u.Status != unit.StatusIdle || !c.IsAdjacent(u.X, u.Y) {
		return
	}
	nowMs := now.UnixMilli()
	reject := func(code, msg string) { s.economyError(u.PlayerID, ap.requestID, code, msg) }
	// Al llegar se vuelve a comprobar todo lo de la orden.
	if err := s.assaultTarget(c, u.PlayerID, nowMs); err != nil {
		reject(protocol.CodeInvalidTarget, err.Error())
		return
	}
	if code, msg := s.assaultBlocked(u, c, u.Tile(), now, true); code != "" {
		reject(code, msg)
		return
	}
	s.startAssault(u, c, u.Tile(), ap.requestID, nowMs)
}

// assaultBroken dice por qué un asalto ya no puede seguir, o "".
func (s *Simulation) assaultBroken(as *assaultState, now time.Time) string {
	u, ok := s.state.Unit(as.UnitID)
	if !ok || !u.IsAlive() || u.Status != unit.StatusClaiming || u.Tile() != as.Tile() {
		return cityfall.EndUnit
	}
	c, ok := s.state.City(as.CityID)
	if !ok {
		return cityfall.EndUnit
	}
	// RN-SIEGE-011: la ciudad que vuelve a estar amurallada lo rompe.
	if s.state.Walled(c.ID) {
		return cityfall.EndWalled
	}
	// RN-COMBAT-041: la paz y la protección del asaltante, en cada tick.
	if code, _ := s.assaultBlocked(u, c, u.Tile(), now, false); code != "" {
		return cityfall.EndBlocked
	}
	// INV-COMBAT-005: con el asaltante ausente no cuenta.
	if own, ok := s.state.CityOf(as.PlayerID); !ok || !s.ownerPresent(own) {
		return cityfall.EndBlocked
	}
	return ""
}

// startFall encola la caída con las dos ciudades como claves (ADR-050 §3).
func (s *Simulation) startFall(as *assaultState, now time.Time) {
	c, ok := s.state.City(as.CityID)
	if !ok {
		s.endAssault(as, cityfall.EndUnit, now.UnixMilli())
		return
	}
	victor, ok := s.state.CityOf(as.PlayerID)
	if !ok {
		s.endAssault(as, cityfall.EndBlocked, now.UnixMilli())
		return
	}
	f := cityfall.Fall{
		AssaultID: as.ID, CityID: c.ID, OwnerID: c.OwnerPlayerID, VictorID: as.PlayerID, VictorCityID: victor.ID,
		Name: c.Name, Era: c.Era, Center: c.Center(), Tick: s.state.Tick(), Now: now, AtMs: now.UnixMilli(),
		// RN-CLANWAR-007: una ciudad caída puntúa en una guerra entre los dos.
		War: s.state.warPoints(as.PlayerID, c.OwnerPlayerID, now.UnixMilli(), clan.PointsCity),
	}
	as.falling = true
	as.fall = f
	s.submitFall(f, victor.Center())
}

func (s *Simulation) submitFall(f cityfall.Fall, victorCenter world.Tile) {
	if s.deps.CityFall == nil || s.deps.RefoundSite == nil {
		return
	}
	store, refound, minDist := s.deps.CityFall, s.deps.RefoundSite, s.refoundMinDistance()
	avoid := []world.Tile{f.Center, victorCenter}
	seed := refoundSeed(f.Name)
	job := Job{
		Name: JobCityFall,
		Key:  CityKey(f.CityID),
		Key2: CityKey(f.VictorCityID),
		Run: func(ctx context.Context) error {
			ff := f
			ff.Place = func(existing []world.Tile) (cityfall.Site, error) {
				return refound(ctx, existing, avoid, minDist, seed)
			}
			ff.Verify = founding.FarEnough
			res, err := store.FallCity(ctx, ff)
			if err == nil {
				s.reinject(FallResolved{CityID: f.CityID, Result: res})
				return nil
			}
			if errors.Is(err, ErrFallConflict) {
				s.reinject(FallResolved{CityID: f.CityID, Conflict: true})
				return nil
			}
			return err
		},
		OnPermanentFailure: func(err error) {
			s.deps.Log.Error("no se pudo escribir la caída; se reintentará en el siguiente volcado",
				"err", err, "city_id", f.CityID)
			s.reinject(FallResolved{CityID: f.CityID, Failed: true})
		},
	}
	s.deps.Persister.Submit(job)
}

// refoundSeed es la semilla de la espiral de la refundación: la del nombre de
// la ciudad, determinista (RN-CITY-005).
func refoundSeed(name string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(name))
	return h.Sum64()
}

// handleFallResolved aplica la caída confirmada en un solo comando
// (ADR-050 §3), termina el asalto si la base la rechazó o lo deja para el
// volcado si la base no respondió.
func (s *Simulation) handleFallResolved(cmd FallResolved) {
	as, ok := s.state.cityFall.assaults[cmd.CityID]
	if !ok || !as.falling {
		return
	}
	nowMs := s.deps.Clock.NowMs()
	if cmd.Failed {
		// Sigue cayendo: el volcado la reencola.
		s.state.cityFall.pendingFall[cmd.CityID] = as.fall
		return
	}
	as.falling = false
	if cmd.Conflict || cmd.Result == nil || cmd.Result.City == nil {
		s.endAssault(as, cityfall.EndConflict, nowMs)
		return
	}
	s.applyFall(as, cmd.Result, nowMs)
}

// applyFall quita la ciudad caída y pone la nueva, y resuelve lo que colgaba
// de ella como la transacción (SPEC-CITY-FALL §4.2, §4.3).
func (s *Simulation) applyFall(as *assaultState, res *cityfall.FallResult, nowMs int64) {
	f := as.fall
	fallen, ok := s.state.City(f.CityID)
	if !ok {
		return
	}
	s.dropAssault(as)
	w := s.state.World()
	newCity := res.City

	// La unidad que asaltaba vuelve a IDLE donde está.
	if u, ok := s.state.Unit(as.UnitID); ok && u.Status == unit.StatusClaiming {
		u.Status = unit.StatusIdle
		s.state.MarkDirty(u.ID)
		s.broadcastStatus(u)
	}
	// Lo que atacaba a la ciudad caída o a sus edificios termina.
	atacantes := make([]int64, 0, len(s.state.combat.engagements))
	for id := range s.state.combat.engagements {
		atacantes = append(atacantes, id)
	}
	sort.Slice(atacantes, func(i, j int) bool { return atacantes[i] < atacantes[j] })
	for _, id := range atacantes {
		e := s.state.combat.engagements[id]
		if e == nil {
			continue
		}
		hit := e.cityID == fallen.ID
		if b, ok := s.state.buildings[e.buildingID]; ok && b.CityID == fallen.ID {
			hit = true
		}
		if hit {
			if a, ok := s.state.Unit(id); ok {
				s.endEngagement(a, true)
			}
		}
	}
	// Los asaltos de otros contra ella no existen ya; los que salían de ella
	// salen ahora de la nueva, como sus reclamaciones (RN-CITY-028).
	for _, other := range s.state.cityFall.assaults {
		if other.FromCityID == fallen.ID {
			other.FromCityID = newCity.ID
		}
	}
	for _, cs := range s.state.conquest.claims {
		if cs.CityID == fallen.ID {
			cs.CityID = newCity.ID
		}
	}

	// Las guarniciones salen por el procedimiento normal (RN-CITY-028).
	var guarnecidas []int64
	for uid, cid := range s.state.garrisonedIn {
		if cid == fallen.ID {
			guarnecidas = append(guarnecidas, uid)
		}
	}
	sort.Slice(guarnecidas, func(i, j int) bool { return guarnecidas[i] < guarnecidas[j] })

	// Arrasar en la RAM: sus edificios, su economía y su muralla en la capa de
	// ocupación.
	for _, id := range append([]int64(nil), s.state.buildingIDs...) {
		if b, ok := s.state.buildings[id]; ok && b.CityID == fallen.ID {
			s.state.removeBuilding(b)
			delete(s.state.siege.destroying, b.Tile())
		}
	}
	delete(s.state.economies, fallen.ID)
	delete(s.state.stockDirty, fallen.ID)
	minX, minY, maxX, maxY := fallen.UrbanBounds()
	w.SetBlocked(minX, minY, maxX, maxY, false)
	for _, uid := range guarnecidas {
		u, ok := s.state.Unit(uid)
		if !ok {
			continue
		}
		if err := s.exit(u, fallen, exitRequest{PlayerID: u.PlayerID, CityID: fallen.ID}); err != nil {
			s.deps.Log.Warn("una unidad guarnecida en la ciudad caída no encuentra salida", "unit_id", uid, "err", err)
		}
	}
	s.state.RemoveCity(fallen.ID)

	// Las unidades del vencido pasan a la ciudad nueva (RN-CITY-028).
	for _, u := range s.state.units {
		if u.CityID != nil && *u.CityID == fallen.ID {
			id := newCity.ID
			u.CityID = &id
		}
	}

	// La civilización, el bando y el clan son del jugador, no de la ciudad: la
	// transacción no los lee, y la RAM los copia de la caída (precisión de
	// 2026-11-23). Sin ellos, el CityView de la nueva no cabe en el contrato.
	newCity.Civilization, newCity.Faction = fallen.Civilization, fallen.Faction
	newCity.FactionJoin, newCity.Clan = fallen.FactionJoin, fallen.Clan
	// La ciudad nueva entra como un alta (RN-CITY-027).
	s.handleIntroducePlayer(IntroducePlayer{
		City: newCity, Units: res.Villagers,
		BlockedMinX: newCity.CenterX - 1, BlockedMinY: newCity.CenterY - 1,
		BlockedMaxX: newCity.CenterX + 1, BlockedMaxY: newCity.CenterY + 1,
		TerritoryControl: res.Claimed,
	})
	if res.Freed != nil {
		s.applyTerritoryControl(*res.Freed)
	}

	// Los tratos pendientes caducaron y las ofertas del mercado se retiraron en
	// la transacción: la RAM los cierra sin volver a escribirlos.
	for _, id := range res.ExpiredTrades {
		o, ok := s.state.trade.open[id]
		if !ok {
			continue
		}
		if after, err := o.TransitionTo(trade.StatusExpired, s.deps.Clock.Now()); err == nil {
			s.storeTradeOffer(after)
			s.broadcastTrade(after, uuid.Nil, "")
		}
	}
	for _, id := range res.WithdrawnMarket {
		o, ok := s.state.market.open[id]
		if !ok {
			continue
		}
		cerrada := *o
		back, err := cerrada.Close(market.StatusWithdrawn, s.treatyNow())
		delete(s.state.market.open, id)
		if err != nil {
			continue
		}
		// La custodia no estaba en la ciudad: va a la nueva.
		s.deposit(newCity, back.Resource, int32(back.Amount))
		s.broadcastMarket(cerrada, uuid.Nil, "")
	}
	// Las caravanas que iban a la caída van a la nueva (RN-CITY-028).
	for _, id := range res.RedirectedCaravans {
		for uid, e := range s.state.caravans.byUnit {
			if e.ID != id {
				continue
			}
			e.ToCity = newCity.ID
			e.rerouteAtMs = 0
			if u, ok := s.state.Unit(uid); ok {
				s.cancelActiveMovement(u, movement.ReasonServer, nowMs)
			}
		}
	}

	// ADR-045 §4: los puntos de la caída, cuando la base la confirmó.
	s.state.addWarPoints(f.War)

	// city.fallen al área de la ciudad caída, y los avisos a los dos.
	cx, cy := w.ChunkOf(fallen.CenterX, fallen.CenterY)
	s.deps.Broadcaster.BroadcastChunk(cx, cy, protocol.TypeCityFallen, protocol.CityFallenPayload{
		CityID: fallen.ID, OwnerPlayerID: fallen.OwnerPlayerID.String(), ByPlayerID: f.VictorID.String(), AtMs: f.AtMs,
	})
	s.deps.Broadcaster.SendToPlayer(fallen.OwnerPlayerID, protocol.TypeCityFallen, "", protocol.CityFallenPayload{
		CityID: fallen.ID, OwnerPlayerID: fallen.OwnerPlayerID.String(), ByPlayerID: f.VictorID.String(), AtMs: f.AtMs,
	})
	s.sendNotices(eventlog.Event{
		Type: eventlog.EventCityFell, PlayerID: fallen.OwnerPlayerID, Counterpart: f.VictorID, AtMs: f.AtMs,
		X: fallen.CenterX, Y: fallen.CenterY, NewX: newCity.CenterX, NewY: newCity.CenterY, HasTile: true,
	})
	// ADR-050 §5: el vencido recibe su mundo nuevo sin reconectar.
	if s.deps.Resnapshot != nil {
		s.deps.Resnapshot(fallen.OwnerPlayerID)
	}
	s.deps.Log.Info("ciudad caída", "city_id", fallen.ID, "owner", fallen.OwnerPlayerID, "victor", f.VictorID,
		"new_city_id", newCity.ID, "new_x", newCity.CenterX, "new_y", newCity.CenterY)
}

// flushAssaults reencola, en orden, los fines y las caídas que la cola
// descartó.
func (s *Simulation) flushAssaults() {
	if n := len(s.state.cityFall.pendingEnds); n > 0 {
		ids := make([]int64, 0, n)
		for id := range s.state.cityFall.pendingEnds {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		pend := s.state.cityFall.pendingEnds
		s.state.cityFall.pendingEnds = make(map[int64]AssaultEnd)
		for _, id := range ids {
			s.submitAssaultEnd(pend[id])
		}
	}
	if n := len(s.state.cityFall.pendingFall); n > 0 {
		ids := make([]int64, 0, n)
		for id := range s.state.cityFall.pendingFall {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		pend := s.state.cityFall.pendingFall
		s.state.cityFall.pendingFall = make(map[int64]cityfall.Fall)
		for _, id := range ids {
			f := pend[id]
			if victor, ok := s.state.City(f.VictorCityID); ok {
				s.submitFall(f, victor.Center())
			}
		}
	}
}
