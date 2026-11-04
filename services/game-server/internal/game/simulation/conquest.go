package simulation

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/eventlog"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/movement"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/territory"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// La conquista de territorios (SPEC-TERRITORY-CONQUEST, ADR-034).
//
// Un guerrero reclama el territorio en que está. El comienzo es un encargo en
// dos fases: el tick valida y aparta el territorio y el guerrero, la cola
// escribe la fila, y sólo con la confirmación la reclamación existe para los
// demás (§1). Lo que la rompe se decide en la RAM y se escribe después (§2). La
// captura es una orden en dos fases con las dos ciudades como claves: la RAM
// cambia el dueño después del COMMIT (§3).

// Nombres de los trabajos de la conquista (catálogo JobNames).
const (
	JobTerritoryClaim    = "territory.claim"
	JobTerritoryClaimEnd = "territory.claim_end"
	JobTerritoryCapture  = "territory.capture"
)

// Plazos por defecto (SPEC-TERRITORY-CONQUEST Q3).
const (
	defaultClaimFree = 5 * time.Minute
	defaultClaimHeld = 10 * time.Minute
)

// Motivos con los que termina una reclamación sin captura (end_reason).
const (
	// ClaimEndUnit: el guerrero murió, se movió o recibió otra orden.
	ClaimEndUnit = "UNIT"
	// ClaimEndBlocked: la paz o la protección la impiden.
	ClaimEndBlocked = "BLOCKED"
	// ClaimEndTarget: el territorio dejó de poder reclamarse (una fundación lo
	// hizo capital).
	ClaimEndTarget = "TARGET"
	// ClaimEndConflict: la base rechazó la captura.
	ClaimEndConflict = "CONFLICT"
)

// ErrClaimConflict es un rechazo de dominio de la base: otra reclamación activa
// sobre el territorio o el guerrero, o una captura que no casa con el control
// (versión, dueño o capital).
var ErrClaimConflict = errors.New("la reclamación choca con el estado de la base")

// ConquestStore escribe las reclamaciones y las capturas (ADR-034).
type ConquestStore interface {
	// StartClaim escribe la reclamación ACTIVE, contested y, si el territorio
	// tiene dueño, TerritoryClaimStarted. Devuelve el id. Idempotente por
	// petición.
	StartClaim(ctx context.Context, c ClaimStart) (int64, error)
	// EndClaim cierra una reclamación sin captura. Idempotente.
	EndClaim(ctx context.Context, e ClaimEnd) error
	// CaptureTerritory es la transacción de ADR-034 §3: devuelve el control
	// nuevo. Idempotente.
	CaptureTerritory(ctx context.Context, c Capture) (territory.Control, error)
}

// ClaimStart es el comienzo de una reclamación.
type ClaimStart struct {
	Claim         territory.Claim
	TerritoryName string
	Tick          uint64
}

// ClaimEnd es el fin sin captura de una reclamación.
type ClaimEnd struct {
	ClaimID     int64
	TerritoryID int64
	CityID      int64
	Reason      string
	Tick        uint64
	AtMs        int64
}

// Capture es la captura de un territorio (ADR-034 §3).
type Capture struct {
	ClaimID       int64
	TerritoryID   int64
	TerritoryName string
	NewOwner      uuid.UUID
	NewCityID     int64
	// NewBonus es lo que sube el límite de la ciudad del conquistador.
	NewBonus int32
	// PrevOwner es el dueño anterior; uuid.Nil si el territorio era libre.
	PrevOwner  uuid.UUID
	PrevCityID int64
	// PrevBonus es lo que baja el límite de la ciudad del anterior dueño.
	PrevBonus       int32
	ExpectedVersion int32
	CapturedAt      time.Time
	Tick            uint64
	AtMs            int64
	X, Y            int32
}

// ClaimTerritory es unit.claim: un guerrero reclama el territorio en que está.
type ClaimTerritory struct {
	PlayerID  uuid.UUID
	SessionID uuid.UUID
	RequestID string
	UnitID    int64
}

func (ClaimTerritory) commandType() string { return protocol.TypeUnitClaim }

// ClaimResolved lo devuelve la cola con el resultado del comienzo. Code vacío:
// la fila está escrita con su id.
type ClaimResolved struct {
	TerritoryID int64
	PlayerID    uuid.UUID
	RequestID   string
	ClaimID     int64
	Code        string
	Message     string
}

func (ClaimResolved) commandType() string { return "territory.claim_resolved" }

// ClaimEndPersistFailed es la compensación de un fin que no se escribió: el
// volcado siguiente lo reencola (ADR-034 §2).
type ClaimEndPersistFailed struct{ End ClaimEnd }

func (ClaimEndPersistFailed) commandType() string { return "territory.claim_end_failed" }

// CaptureResolved lo devuelve la cola con el resultado de una captura: el
// control nuevo, un rechazo de dominio (Conflict) o la base caída (Failed).
type CaptureResolved struct {
	TerritoryID int64
	Control     territory.Control
	Conflict    bool
	Failed      bool
}

func (CaptureResolved) commandType() string { return "territory.capture_resolved" }

// claimState es una reclamación con su situación en sus dos órdenes.
type claimState struct {
	territory.Claim
	// pending: su comienzo espera la transacción; todavía no existe para nadie.
	pending bool
	// capturing: su captura espera la transacción; nada la rompe ya.
	capturing bool
	capture   Capture
}

type conquestState struct {
	claims          map[int64]*claimState // por territorio
	byUnit          map[int64]int64       // guerrero → territorio
	pendingEnds     map[int64]ClaimEnd    // por id de reclamación
	pendingCaptures map[int64]Capture     // por territorio
}

func newConquestState() conquestState {
	return conquestState{
		claims:          make(map[int64]*claimState),
		byUnit:          make(map[int64]int64),
		pendingEnds:     make(map[int64]ClaimEnd),
		pendingCaptures: make(map[int64]Capture),
	}
}

// ─────────────────────────────────────────────────────────────
// Estado
// ─────────────────────────────────────────────────────────────

// SetClaims instala las reclamaciones activas leídas de la base al arrancar
// (ADR-034 §5). Un guerrero vivo, parado en su tile, vuelve a CLAIMING —el
// volcado lo escribió IDLE—; uno que no, se queda como está, y el primer
// barrido termina su reclamación.
func (s *State) SetClaims(claims []territory.Claim) {
	for _, c := range claims {
		s.conquest.claims[c.TerritoryID] = &claimState{Claim: c}
		s.conquest.byUnit[c.UnitID] = c.TerritoryID
		u, ok := s.units[c.UnitID]
		if !ok || !u.IsAlive() || u.Status != unit.StatusIdle || u.Tile() != c.Tile() {
			continue
		}
		if _, moving := s.movements[u.ID]; moving {
			continue
		}
		u.Status = unit.StatusClaiming
	}
}

// ActiveClaim devuelve la reclamación de un territorio y si ya está confirmada.
func (s *State) ActiveClaim(territoryID int64) (territory.Claim, bool, bool) {
	cs, ok := s.conquest.claims[territoryID]
	if !ok {
		return territory.Claim{}, false, false
	}
	return cs.Claim, !cs.pending, true
}

// claimView es la reclamación confirmada de un territorio, o nil.
func (s *State) claimView(territoryID int64) *protocol.TerritoryClaimView {
	cs, ok := s.conquest.claims[territoryID]
	if !ok || cs.pending {
		return nil
	}
	v := &protocol.TerritoryClaimView{
		PlayerID: cs.PlayerID.String(), StartedAtMs: cs.StartedAtMs, EndsAtMs: cs.CompletesAtMs,
	}
	if c, ok := s.cities[cs.CityID]; ok && c.Name != "" {
		id, name := c.ID, c.Name
		v.CityID, v.CityName = &id, &name
	}
	return v
}

// ─────────────────────────────────────────────────────────────
// unit.claim
// ─────────────────────────────────────────────────────────────

func (s *Simulation) claimDuration(owned bool) time.Duration {
	if owned {
		if s.deps.TerritoryClaimHeld > 0 {
			return s.deps.TerritoryClaimHeld
		}
		return defaultClaimHeld
	}
	if s.deps.TerritoryClaimFree > 0 {
		return s.deps.TerritoryClaimFree
	}
	return defaultClaimFree
}

// handleClaimTerritory valida en el tick y aparta el territorio y el guerrero;
// la cola escribe la fila (RN-CONQ-001 a 003, ADR-034 §1).
func (s *Simulation) handleClaimTerritory(cmd ClaimTerritory) {
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
	if def, err := unit.Lookup(u.Type); err != nil || !def.CanAttack() {
		reject(protocol.CodeUnitNotMovable, "sólo un guerrero reclama territorios")
		return
	}
	if s.deps.Conquest == nil {
		reject(protocol.CodeInternalError, "la conquista no está disponible")
		return
	}
	own, ok := s.state.CityOf(cmd.PlayerID)
	if !ok {
		reject(protocol.CodeInvalidTarget, "no tienes ciudad")
		return
	}

	pos := s.authoritativePosition(u, nowMs)
	t, ok := s.state.TerritoryAt(pos.X, pos.Y)
	if !ok {
		reject(protocol.CodeInvalidTarget, "ese tile no pertenece a ningún territorio")
		return
	}
	if _, zone := s.state.SafeZoneAt(pos.X, pos.Y); zone {
		reject(protocol.CodeInvalidTarget, "no se reclama desde una zona segura")
		return
	}
	if s.urbanAt(pos) {
		reject(protocol.CodeInvalidTarget, "no se reclama dentro de una zona urbana")
		return
	}
	if _, busy := s.state.conquest.claims[t.ID]; busy {
		reject(protocol.CodeInvalidTarget, "ya hay una reclamación en ese territorio")
		return
	}
	if code, msg := s.claimBlocked(cmd.PlayerID, t, now); code != "" {
		reject(code, msg)
		return
	}

	// Se planta: la reclamación sustituye cualquier otra orden (RN-CONQ-001).
	s.stopWorking(u)
	s.endEngagement(u, false)
	s.cancelActiveMovement(u, movement.ReasonReplaced, nowMs)
	u.Status = unit.StatusClaiming
	s.state.MarkDirty(u.ID)

	control, _ := s.state.TerritoryControl(t.ID)
	owner, owned := control.PlayerOwner()
	c := territory.Claim{
		TerritoryID: t.ID, UnitID: u.ID, PlayerID: cmd.PlayerID, CityID: own.ID, X: pos.X, Y: pos.Y,
		StartedAtMs: nowMs, CompletesAtMs: nowMs + s.claimDuration(owned).Milliseconds(), RequestID: cmd.RequestID,
	}
	if owned {
		c.OwnerPlayerID = &owner
	}
	s.state.conquest.claims[t.ID] = &claimState{Claim: c, pending: true}
	s.state.conquest.byUnit[u.ID] = t.ID

	start := ClaimStart{Claim: c, TerritoryName: t.Name, Tick: s.state.Tick()}
	resolved := ClaimResolved{TerritoryID: t.ID, PlayerID: cmd.PlayerID, RequestID: cmd.RequestID}
	store := s.deps.Conquest
	s.deps.Persister.Submit(Job{
		Name: JobTerritoryClaim,
		Key:  CityKey(own.ID),
		Run: func(ctx context.Context) error {
			id, err := store.StartClaim(ctx, start)
			if err == nil {
				r := resolved
				r.ClaimID = id
				s.reinject(r)
				return nil
			}
			if errors.Is(err, ErrClaimConflict) {
				// Un rechazo de dominio no se reintenta: es la respuesta.
				r := resolved
				r.Code, r.Message = protocol.CodeInvalidTarget, "ya hay una reclamación en ese territorio"
				s.reinject(r)
				return nil
			}
			return err
		},
		OnPermanentFailure: func(err error) {
			s.deps.Log.Error("no se pudo escribir la reclamación; el guerrero queda libre",
				"err", err, "territory_id", resolved.TerritoryID)
			r := resolved
			r.Code, r.Message = protocol.CodeInternalError, "no se pudo empezar la reclamación"
			s.reinject(r)
		},
	})
}

// urbanAt dice si el tile cae en la zona urbana de alguna ciudad.
func (s *State) urbanAt(t world.Tile) bool {
	for _, c := range s.cities {
		if c.Occupies(t.X, t.Y) {
			return true
		}
	}
	return false
}

func (s *Simulation) urbanAt(t world.Tile) bool { return s.state.urbanAt(t) }

// claimBlocked es el predicado de la reclamación (RN-CONQ-002, RN-CONQ-003,
// INV-TERR-011), el mismo al ordenar y en cada tick (RN-CONQ-005).
func (s *Simulation) claimBlocked(claimer uuid.UUID, t territory.Territory, now time.Time) (string, string) {
	control, _ := s.state.TerritoryControl(t.ID)
	owner, owned := control.PlayerOwner()
	var centers []world.Tile
	if owned {
		if c, ok := s.state.CityOf(owner); ok {
			centers = append(centers, c.Center())
		}
	}
	if err := territory.CheckClaimable(t, control, claimer, centers); err != nil {
		return protocol.CodeInvalidTarget, err.Error()
	}
	// La protección es simétrica (RN-COMBAT-018), y quien reclama no puede
	// seguir protegido ni en un territorio libre: su guerrero sería
	// invulnerable (ADR-034 §7).
	if c, ok := s.state.CityOf(claimer); ok && c.ProtectedAt(now) {
		return protocol.CodeCityProtected, "estás protegido: no puedes reclamar territorios"
	}
	if !owned {
		return "", ""
	}
	if s.state.AtPeace(claimer, owner, now) {
		return protocol.CodeInvalidTarget, "un tratado de paz impide reclamar ese territorio"
	}
	if c, ok := s.state.CityOf(owner); ok && c.ProtectedAt(now) {
		return protocol.CodeCityProtected, "el dueño de ese territorio está protegido"
	}
	return "", ""
}

// handleClaimResolved confirma la reclamación o la deshace.
func (s *Simulation) handleClaimResolved(cmd ClaimResolved) {
	cs, ok := s.state.conquest.claims[cmd.TerritoryID]
	if !ok || !cs.pending || cs.RequestID != cmd.RequestID || cs.PlayerID != cmd.PlayerID {
		return
	}
	nowMs := s.deps.Clock.NowMs()
	if cmd.Code != "" {
		s.dropClaim(cs)
		if u, ok := s.state.Unit(cs.UnitID); ok && u.Status == unit.StatusClaiming {
			u.Status = unit.StatusIdle
			s.state.MarkDirty(u.ID)
		}
		s.economyError(cmd.PlayerID, cmd.RequestID, cmd.Code, cmd.Message)
		return
	}
	cs.ID = cmd.ClaimID
	cs.pending = false

	// Mientras la transacción estaba en vuelo pudo llegar otra orden o la
	// muerte: la fila existe, así que se cierra como cualquier otra.
	u, ok := s.state.Unit(cs.UnitID)
	if !ok || !u.IsAlive() || u.Status != unit.StatusClaiming {
		s.endClaim(cs, ClaimEndUnit, nowMs)
		return
	}

	// Ahora existe para los demás (ADR-034 §1).
	s.broadcastStatus(u)
	s.broadcastTerritory(cs.TerritoryID)
	if cs.OwnerPlayerID == nil {
		return
	}
	owner := *cs.OwnerPlayerID
	t, _ := s.state.territories.ByID(cs.TerritoryID)
	// RN-CONQ-010: el aviso al dueño, esté donde esté.
	s.sendNotices(eventlog.Event{
		Type: territory.EventClaimStarted, PlayerID: owner, Counterpart: cs.PlayerID, AtMs: cs.StartedAtMs,
		X: cs.X, Y: cs.Y, HasTile: true, TerritoryID: t.ID, TerritoryName: t.Name,
	})
	// RN-CONQ-006: los guardias del dueño responden como a un golpe.
	for _, g := range s.guardsNear(owner, cs.Tile()) {
		s.respond(g, u, nowMs)
	}
}

// broadcastTerritory difunde la vista de un territorio a su huella
// (RN-TERR-012).
func (s *Simulation) broadcastTerritory(id int64) {
	t, ok := s.state.territories.ByID(id)
	if !ok {
		return
	}
	s.deps.Broadcaster.BroadcastChunks(s.state.TerritoryFootprint(id), protocol.TypeTerritoryUpdate,
		protocol.TerritoryUpdatePayload{Territory: s.state.TerritoryView(t)})
}

func (s *Simulation) dropClaim(cs *claimState) {
	delete(s.state.conquest.claims, cs.TerritoryID)
	if s.state.conquest.byUnit[cs.UnitID] == cs.TerritoryID {
		delete(s.state.conquest.byUnit, cs.UnitID)
	}
}

// abandonClaim es la orden de cancelar sobre un guerrero que reclama: lo
// libera, y el barrido termina la reclamación (RN-CONQ-005).
func (s *Simulation) abandonClaim(u *unit.Unit) {
	if u.Status != unit.StatusClaiming {
		return
	}
	u.Status = unit.StatusIdle
	s.state.MarkDirty(u.ID)
	s.broadcastStatus(u)
}

// endClaim termina una reclamación sin captura: la RAM en el acto y la base
// después, con su reencolado (ADR-034 §2).
func (s *Simulation) endClaim(cs *claimState, reason string, nowMs int64) {
	s.dropClaim(cs)
	if u, ok := s.state.Unit(cs.UnitID); ok && u.Status == unit.StatusClaiming {
		u.Status = unit.StatusIdle
		s.state.MarkDirty(u.ID)
		s.broadcastStatus(u)
	}
	if !cs.pending {
		s.broadcastTerritory(cs.TerritoryID)
	}
	s.deps.Log.Info("reclamación terminada", "territory_id", cs.TerritoryID, "unit_id", cs.UnitID, "reason", reason)
	if cs.ID == 0 {
		return
	}
	s.submitClaimEnd(ClaimEnd{
		ClaimID: cs.ID, TerritoryID: cs.TerritoryID, CityID: cs.CityID, Reason: reason, Tick: s.state.Tick(), AtMs: nowMs,
	})
}

func (s *Simulation) submitClaimEnd(e ClaimEnd) {
	if s.deps.Conquest == nil {
		return
	}
	store := s.deps.Conquest
	s.deps.Persister.Submit(Job{
		Name: JobTerritoryClaimEnd,
		Key:  CityKey(e.CityID),
		Run:  func(ctx context.Context) error { return store.EndClaim(ctx, e) },
		OnPermanentFailure: func(err error) {
			s.deps.Log.Error("no se pudo escribir el fin de la reclamación; se reintentará en el siguiente volcado",
				"err", err, "claim_id", e.ClaimID)
			s.reinject(ClaimEndPersistFailed{End: e})
		},
	})
}

func (s *Simulation) handleClaimEndPersistFailed(cmd ClaimEndPersistFailed) {
	s.state.conquest.pendingEnds[cmd.End.ClaimID] = cmd.End
}

// ─────────────────────────────────────────────────────────────
// Fase 5: reclamaciones que se rompen o vencen
// ─────────────────────────────────────────────────────────────

// sweepClaims recorre las reclamaciones en orden de territorio: termina las que
// se rompieron (RN-CONQ-005) y captura las que vencieron (RN-CONQ-007).
func (s *Simulation) sweepClaims(now time.Time) {
	if len(s.state.conquest.claims) == 0 {
		return
	}
	nowMs := now.UnixMilli()
	ids := make([]int64, 0, len(s.state.conquest.claims))
	for id := range s.state.conquest.claims {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		cs, ok := s.state.conquest.claims[id]
		if !ok || cs.pending || cs.capturing {
			continue
		}
		if reason := s.claimBroken(cs, now); reason != "" {
			s.endClaim(cs, reason, nowMs)
			continue
		}
		if cs.Due(nowMs) {
			s.startCapture(cs, now)
		}
	}
}

// claimBroken dice por qué una reclamación ya no puede seguir, o "".
func (s *Simulation) claimBroken(cs *claimState, now time.Time) string {
	u, ok := s.state.Unit(cs.UnitID)
	if !ok || !u.IsAlive() || u.Status != unit.StatusClaiming || u.Tile() != cs.Tile() {
		return ClaimEndUnit
	}
	t, ok := s.state.territories.ByID(cs.TerritoryID)
	if !ok {
		return ClaimEndTarget
	}
	code, _ := s.claimBlocked(cs.PlayerID, t, now)
	switch code {
	case "":
		return ""
	case protocol.CodeInvalidTarget:
		// La paz también es INVALID_TARGET; el motivo exacto no cambia nada
		// para el jugador, pero se distingue en el log y en la fila.
		control, _ := s.state.TerritoryControl(t.ID)
		if owner, owned := control.PlayerOwner(); owned && s.state.AtPeace(cs.PlayerID, owner, now) {
			return ClaimEndBlocked
		}
		return ClaimEndTarget
	default:
		return ClaimEndBlocked
	}
}

// startCapture encola la captura con las dos ciudades como claves (ADR-034 §3).
func (s *Simulation) startCapture(cs *claimState, now time.Time) {
	t, _ := s.state.territories.ByID(cs.TerritoryID)
	newCity, ok := s.state.City(cs.CityID)
	if !ok {
		s.endClaim(cs, ClaimEndTarget, now.UnixMilli())
		return
	}
	control, _ := s.state.TerritoryControl(t.ID)
	c := Capture{
		ClaimID: cs.ID, TerritoryID: t.ID, TerritoryName: t.Name,
		NewOwner: cs.PlayerID, NewCityID: newCity.ID, NewBonus: territory.CaptureBonus(t, newCity.Center()),
		ExpectedVersion: control.Version, CapturedAt: now, Tick: s.state.Tick(), AtMs: now.UnixMilli(),
		X: cs.X, Y: cs.Y,
	}
	if prev, owned := control.PlayerOwner(); owned {
		c.PrevOwner = prev
		if pc, ok := s.state.CityOf(prev); ok {
			c.PrevCityID = pc.ID
			c.PrevBonus = territory.CaptureBonus(t, pc.Center())
		}
	}
	cs.capturing = true
	cs.capture = c
	s.submitCapture(c)
}

func (s *Simulation) submitCapture(c Capture) {
	if s.deps.Conquest == nil {
		return
	}
	store := s.deps.Conquest
	job := Job{
		Name: JobTerritoryCapture,
		Key:  CityKey(c.NewCityID),
		Run: func(ctx context.Context) error {
			ctrl, err := store.CaptureTerritory(ctx, c)
			if err == nil {
				s.reinject(CaptureResolved{TerritoryID: c.TerritoryID, Control: ctrl})
				return nil
			}
			if errors.Is(err, ErrClaimConflict) {
				s.reinject(CaptureResolved{TerritoryID: c.TerritoryID, Conflict: true})
				return nil
			}
			return err
		},
		OnPermanentFailure: func(err error) {
			s.deps.Log.Error("no se pudo escribir la captura; se reintentará en el siguiente volcado",
				"err", err, "territory_id", c.TerritoryID)
			s.reinject(CaptureResolved{TerritoryID: c.TerritoryID, Failed: true})
		},
	}
	if c.PrevCityID != 0 && c.PrevCityID != c.NewCityID {
		job.Key2 = CityKey(c.PrevCityID)
	}
	s.deps.Persister.Submit(job)
}

// handleCaptureResolved aplica la captura confirmada, termina la que la base
// rechazó o la deja para el volcado si la base no respondió.
func (s *Simulation) handleCaptureResolved(cmd CaptureResolved) {
	cs, ok := s.state.conquest.claims[cmd.TerritoryID]
	if !ok || !cs.capturing {
		return
	}
	nowMs := s.deps.Clock.NowMs()
	if cmd.Failed {
		// Sigue activa y en captura: el volcado la reencola.
		s.state.conquest.pendingCaptures[cmd.TerritoryID] = cs.capture
		return
	}
	cs.capturing = false
	if cmd.Conflict {
		s.endClaim(cs, ClaimEndConflict, nowMs)
		return
	}

	c := cs.capture
	s.dropClaim(cs)
	if u, ok := s.state.Unit(cs.UnitID); ok && u.Status == unit.StatusClaiming {
		u.Status = unit.StatusIdle
		s.state.MarkDirty(u.ID)
		s.broadcastStatus(u)
	}
	// El límite de las dos ciudades (§4.2, INV-ECON-004).
	if nc, ok := s.state.City(c.NewCityID); ok && c.NewBonus != 0 {
		nc.PopulationLimit += c.NewBonus
		limit := nc.PopulationLimit
		s.broadcastCity(nc, protocol.CityUpdatePayload{PopulationLimit: &limit})
	}
	if pc, ok := s.state.City(c.PrevCityID); ok && c.PrevBonus != 0 {
		pc.PopulationLimit -= c.PrevBonus
		limit := pc.PopulationLimit
		s.broadcastCity(pc, protocol.CityUpdatePayload{PopulationLimit: &limit})
	}
	// INV-TERR-009: el control nuevo y su territory.update, en una función.
	s.applyTerritoryControl(cmd.Control)
	s.deps.Log.Info("territorio conquistado", "territory_id", c.TerritoryID, "player_id", c.NewOwner)
	if c.PrevOwner == uuid.Nil {
		return
	}
	// RN-CONQ-010: al anterior dueño y al conquistador, estén donde estén.
	s.sendNotices(eventlog.Event{
		Type: territory.EventControlChanged, PlayerID: c.PrevOwner, Counterpart: c.NewOwner, AtMs: c.AtMs,
		X: c.X, Y: c.Y, HasTile: true, TerritoryID: c.TerritoryID, TerritoryName: c.TerritoryName,
	})
}

// flushClaims reencola, en orden, los fines y las capturas que la cola
// descartó.
func (s *Simulation) flushClaims() {
	if n := len(s.state.conquest.pendingEnds); n > 0 {
		ids := make([]int64, 0, n)
		for id := range s.state.conquest.pendingEnds {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		pend := s.state.conquest.pendingEnds
		s.state.conquest.pendingEnds = make(map[int64]ClaimEnd)
		for _, id := range ids {
			s.submitClaimEnd(pend[id])
		}
	}
	if n := len(s.state.conquest.pendingCaptures); n > 0 {
		ids := make([]int64, 0, n)
		for id := range s.state.conquest.pendingCaptures {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		pend := s.state.conquest.pendingCaptures
		s.state.conquest.pendingCaptures = make(map[int64]Capture)
		for _, id := range ids {
			s.submitCapture(pend[id])
		}
	}
}
