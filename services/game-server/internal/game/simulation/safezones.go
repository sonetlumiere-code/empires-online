package simulation

import (
	"fmt"
	"sort"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/safezone"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// Safe Zones en la simulación: evaluación del ocultamiento en la fase 5 y su
// difusión asimétrica — el propietario sigue viendo su unidad, los terceros la
// pierden de vista.
//
// Spec: ../../../../docs/specs/safe-zones.md §6.4, §7 y §12.

// SetSafeZones instala el índice de zonas seguras.
//
// Se llama una sola vez, al arrancar, DESPUÉS de marcar las murallas de las
// ciudades existentes: el índice excluye los tiles ocupados, así que construirlo
// antes dejaría dentro tiles que ninguna unidad puede pisar (INV-SAFE-002).
func (s *State) SetSafeZones(idx *safezone.Index) { s.safeZones = idx }

// SafeZoneAt resuelve la zona segura que contiene el tile, si hay alguna.
func (s *State) SafeZoneAt(x, y int32) (safezone.Zone, bool) {
	return s.safeZones.ZoneAt(x, y)
}

// drainConcealCandidates devuelve los candidatos en orden ascendente de id y
// vacía el conjunto. El orden fijo es lo que hace reproducible la secuencia de
// deltas de la fase 5 (canon §8).
func (s *State) drainConcealCandidates() []int64 {
	if len(s.concealCandidates) == 0 {
		return nil
	}
	ids := make([]int64, 0, len(s.concealCandidates))
	for id := range s.concealCandidates {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	s.concealCandidates = make(map[int64]struct{}, len(ids))
	return ids
}

// evaluateConcealment es la parte de la fase 5 que decide quién se oculta y
// quién se revela.
//
// Sólo mira las unidades que cambiaron de tile o de estado desde el tick
// anterior (RN-SAFE-011): el resultado es el mismo que un barrido completo, a
// una fracción del coste. Es aritmética sobre RAM; no hay I/O. El cambio de
// estado se persiste por la vía normal del dirty-flag.
func (s *Simulation) evaluateConcealment() {
	ids := s.state.drainConcealCandidates()
	if len(ids) == 0 {
		return
	}
	nowMs := s.deps.Clock.NowMs()

	for _, id := range ids {
		u, ok := s.state.Unit(id)
		if !ok {
			continue
		}
		_, moving := s.state.Movement(id)
		_, inZone := s.state.SafeZoneAt(u.X, u.Y)

		prev := u.Status
		next := safezone.NextStatus(prev, u.HP, moving, inZone)
		if next == prev {
			continue
		}
		if prev == unit.StatusHidden && next == unit.StatusMoving {
			s.invariantViolation("INV-SAFE-003", "CRITICO", "REPAIR", u.ID,
				"unidad HIDDEN con un movimiento ACTIVE; se repara a MOVING")
		}

		u.Status = next
		s.state.MarkDirty(u.ID)
		s.emitConcealment(u, prev, nowMs)
	}
}

// emitConcealment difunde un cambio de ocultamiento (spec §12).
//
// La asimetría es el punto entero: el filtro se aplica POR DESTINATARIO. Un
// único delta por chunk enviado a todos sus suscriptores sería correcto para el
// propietario y una fuga para los demás (INV-SAFE-004).
//
//	IDLE → HIDDEN     propietario: entity.update{status}   terceros: entity.despawn{HIDDEN}
//	HIDDEN → IDLE     propietario: entity.update{status}   terceros: entity.spawn{unit}
func (s *Simulation) emitConcealment(u *unit.Unit, prev unit.Status, nowMs int64) {
	status := string(u.Status)
	s.deps.Broadcaster.SendToPlayer(u.PlayerID, protocol.TypeEntityUpdate, "",
		protocol.EntityUpdatePayload{ID: u.ID, Status: &status})

	cx, cy := s.state.World().ChunkOf(u.X, u.Y)
	switch {
	case u.Status == unit.StatusHidden:
		s.deps.Broadcaster.BroadcastChunkExcept(cx, cy, u.PlayerID, protocol.TypeEntityDespawn,
			protocol.EntityDespawnPayload{ID: u.ID, Reason: protocol.DespawnHidden})
	case prev == unit.StatusHidden:
		s.deps.Broadcaster.BroadcastChunkExcept(cx, cy, u.PlayerID, protocol.TypeEntitySpawn,
			protocol.EntitySpawnPayload{Unit: s.UnitView(u, nowMs)})
	}
}

// excludeFromSafeZones retira de las zonas los tiles que acaba de ocupar una
// construcción y apunta a revisión las unidades de ese rectángulo.
//
// Una ciudad fundada sobre una zona la deja recortada: un tile amurallado no
// puede seguir siendo refugio (INV-SAFE-002). Se informa como violación de
// INV-SAFE-006, que es lo que su ficha prescribe, aunque aquí la causa no sea
// un error de datos sino una fundación —ver la nota de esa ficha—.
func (s *Simulation) excludeFromSafeZones(minX, minY, maxX, maxY int32, cityID int64) {
	for _, lost := range s.state.safeZones.Exclude(minX, minY, maxX, maxY) {
		s.deps.Log.Error("invariant_violation",
			"inv_id", "INV-SAFE-006", "severity", "MEDIO", "policy", "REPAIR",
			"entity", fmt.Sprintf("safe_zone:%d", lost.ZoneID),
			"city_id", cityID, "tiles", lost.Tiles,
			"detail", fmt.Sprintf("la muralla de la ciudad %d retira %d tiles de la zona desde (%d,%d)",
				cityID, lost.Tiles, lost.X, lost.Y))
	}

	w := s.state.World()
	minCX, minCY := w.ChunkOf(max(minX, 0), max(minY, 0))
	maxCX, maxCY := w.ChunkOf(min(maxX, w.Width()-1), min(maxY, w.Height()-1))
	for cy := minCY; cy <= maxCY; cy++ {
		for cx := minCX; cx <= maxCX; cx++ {
			for _, u := range s.state.UnitsInChunk(cx, cy) {
				if u.X >= minX && u.X <= maxX && u.Y >= minY && u.Y <= maxY {
					s.state.concealCandidates[u.ID] = struct{}{}
				}
			}
		}
	}
}

// invariantViolation emite la señal obligatoria de docs/invariants/README.md
// §5.1: un log de nivel error con msg "invariant_violation" e inv_id.
func (s *Simulation) invariantViolation(invID, severity, policy string, unitID int64, detail string) {
	s.deps.Log.Error("invariant_violation",
		"inv_id", invID, "severity", severity, "policy", policy,
		"entity", fmt.Sprintf("unit:%d", unitID), "tick", s.state.Tick(),
		"detail", detail)
}
