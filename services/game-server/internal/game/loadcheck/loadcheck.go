// Package loadcheck valida, al arrancar y antes de construir la RAM, que el
// estado durable cumple los invariantes que el esquema no puede garantizar
// (EO-119).
//
// Ninguna ruta del código produce estas violaciones: sólo una escritura manual,
// una migración o un cambio del generador. Por eso la validación no está en las
// rutas de escritura sino aquí, una vez por arranque, sobre lo que se acaba de
// leer. Cada hallazgo se trata como dice la ficha de su invariante:
//
//   - REPAIR: el valor se corrige en los objetos en memoria, y quien llama
//     escribe la corrección en PostgreSQL con un world_events de auditoría.
//   - EXCLUDE: la entidad no se incorpora a la simulación y su fila no se toca.
//   - FAIL_FAST: el arranque debe abortar sin escribir nada.
//
// Es dominio puro: no lee el reloj (el instante llega en Input.Now), no hace
// I/O y recorre todo en orden de id, de modo que la misma entrada produce
// siempre los mismos hallazgos en el mismo orden.
package loadcheck

import (
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/building"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/production"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/task"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/territory"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
)

// Políticas de un hallazgo.
const (
	PolicyRepair   = "REPAIR"
	PolicyExclude  = "EXCLUDE"
	PolicyFailFast = "FAIL_FAST"
)

// Input es el estado durable recién leído.
type Input struct {
	World *world.World
	// EraCaps es el population_cap de cada era del catálogo.
	EraCaps map[city.Era]int32
	// Players es el conjunto de players.id existentes.
	Players map[uuid.UUID]struct{}
	Cities  []*city.City
	// Units son las unidades vivas (status <> 'DEAD'), tal como las carga
	// UnitRepo.ListAlive.
	Units []*unit.Unit
	// Garrisons son las filas de garrisons: unidad → ciudad.
	Garrisons map[int64]int64
	Controls  []territory.Control
	// Economía (SPEC-ECON-WOOD): las tareas, los edificios y las producciones
	// leídos de la base.
	Tasks       []task.Task
	Buildings   []building.Building
	Productions []production.Production
	// Now es el instante de la detección: el que se escribe en los campos que
	// una reparación tiene que fechar.
	Now time.Time
}

// Finding es un hallazgo: la señal invariant_violation que hay que emitir.
type Finding struct {
	InvID    string
	Severity string
	Policy   string
	Entity   string
	Detail   string
}

// Result es lo que queda tras validar.
type Result struct {
	Findings []Finding

	// Units son las unidades que entran en la simulación: las de Input menos
	// las excluidas.
	Units []*unit.Unit

	// Lo que hay que escribir en PostgreSQL, en orden de id. Los objetos de
	// Input ya llevan el valor corregido; los controles corregidos están en
	// Controls.
	CityRepairs     []int64
	UnitRepairs     []int64
	ControlRepairs  []int64
	GarrisonDeletes []int64
	Controls        []territory.Control
	// Garrisons son las filas de garrisons que siguen vigentes: las de Input
	// menos las borradas. Es el índice unidad → anfitriona de la simulación.
	Garrisons map[int64]int64
	// TaskDeletes son las tareas que hay que borrar (INV-ECON-003), por unidad;
	// Tasks, las que siguen vigentes y entran en la simulación.
	TaskDeletes []int64
	Tasks       []task.Task
}

// Fatal indica si algún hallazgo exige abortar el arranque.
func (r Result) Fatal() bool {
	for _, f := range r.Findings {
		if f.Policy == PolicyFailFast {
			return true
		}
	}
	return false
}

// Check valida la entrada. Modifica en el sitio las ciudades y las unidades
// que repara.
func Check(in Input) Result {
	controls := append([]territory.Control(nil), in.Controls...)
	sort.Slice(controls, func(i, j int) bool { return controls[i].TerritoryID < controls[j].TerritoryID })
	c := checker{in: in, res: Result{Controls: controls}}
	c.units()
	c.tasks()
	c.cities()
	c.controls()
	return c.res
}

type checker struct {
	in  Input
	res Result
	// citiesTouched y unitsTouched evitan anotar dos veces la misma fila.
	citiesTouched map[int64]bool
	unitsTouched  map[int64]bool
}

func (c *checker) find(inv, sev, policy, entity, detail string) {
	c.res.Findings = append(c.res.Findings, Finding{InvID: inv, Severity: sev, Policy: policy, Entity: entity, Detail: detail})
}

func (c *checker) touchCity(id int64) {
	if c.citiesTouched == nil {
		c.citiesTouched = make(map[int64]bool)
	}
	if !c.citiesTouched[id] {
		c.citiesTouched[id] = true
		c.res.CityRepairs = append(c.res.CityRepairs, id)
	}
}

func (c *checker) touchUnit(id int64) {
	if c.unitsTouched == nil {
		c.unitsTouched = make(map[int64]bool)
	}
	if !c.unitsTouched[id] {
		c.unitsTouched[id] = true
		c.res.UnitRepairs = append(c.res.UnitRepairs, id)
	}
}

// ─────────────────────────────────────────────────────────────
// Unidades
// ─────────────────────────────────────────────────────────────

func (c *checker) units() {
	units := append([]*unit.Unit(nil), c.in.Units...)
	sort.Slice(units, func(i, j int) bool { return units[i].ID < units[j].ID })
	alive := make(map[int64]bool, len(units))

	for _, u := range units {
		alive[u.ID] = true
		entity := fmt.Sprintf("unit:%d", u.ID)

		// INV-UNIT-009: sin definición no hay velocidad ni vida máxima. La unidad
		// no entra; no se le asignan estadísticas ajenas.
		def, err := unit.Lookup(u.Type)
		if err != nil {
			c.find("INV-UNIT-009", "ALTO", PolicyExclude, entity,
				fmt.Sprintf("unit_type %q fuera del catálogo: no se incorpora a la simulación", u.Type))
			continue
		}

		// INV-UNIT-007: garrisons es la tabla del hecho; el estado se deriva de ella.
		_, guarnecida := c.in.Garrisons[u.ID]
		switch {
		case u.Status == unit.StatusGarrisoned && !guarnecida:
			u.Status = unit.StatusIdle
			c.touchUnit(u.ID)
			c.find("INV-UNIT-007", "ALTO", PolicyRepair, entity,
				"GARRISONED sin fila en garrisons: vuelve a IDLE en su última posición")
		case u.Status != unit.StatusGarrisoned && guarnecida:
			c.find("INV-UNIT-007", "ALTO", PolicyRepair, entity,
				fmt.Sprintf("%s con fila en garrisons: pasa a GARRISONED", u.Status))
			u.Status = unit.StatusGarrisoned
			c.touchUnit(u.ID)
		}

		// INV-UNIT-004: max_hp es el del catálogo y hp cabe en [0, max_hp].
		if u.MaxHP != def.MaxHP || u.HP < 0 || u.HP > def.MaxHP {
			before := fmt.Sprintf("hp %d/%d", u.HP, u.MaxHP)
			u.MaxHP = def.MaxHP
			u.HP = min(max(u.HP, 0), def.MaxHP)
			c.touchUnit(u.ID)
			c.find("INV-UNIT-004", "ALTO", PolicyRepair, entity,
				fmt.Sprintf("%s fuera del catálogo de %s: queda hp %d/%d", before, u.Type, u.HP, u.MaxHP))
		}

		// INV-UNIT-001: una unidad en el mapa está dentro del mundo y sobre
		// terreno transitable. Las murallas no cuentan aquí: lo que quede dentro
		// de una lo aparta ClearFootprints (RN-CITY-022). Una guarnecida no
		// ocupa tile y su posición es histórica.
		if u.Status != unit.StatusGarrisoned && !terrainWalkable(c.in.World, u.X, u.Y) {
			c.find("INV-UNIT-001", "CRITICO", PolicyFailFast, entity,
				fmt.Sprintf("posición (%d,%d) fuera del mundo o sobre terreno intransitable", u.X, u.Y))
		}

		c.res.Units = append(c.res.Units, u)
	}

	// Una fila de garrisons de una unidad que no está viva no guarnece nada.
	ids := make([]int64, 0, len(c.in.Garrisons))
	for id := range c.in.Garrisons {
		if !alive[id] {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		c.res.GarrisonDeletes = append(c.res.GarrisonDeletes, id)
		c.find("INV-UNIT-007", "ALTO", PolicyRepair, fmt.Sprintf("unit:%d", id),
			"fila de garrisons de una unidad que no está viva: se borra")
	}

	c.res.Garrisons = make(map[int64]int64, len(c.in.Garrisons))
	for unitID, cityID := range c.in.Garrisons {
		if alive[unitID] {
			c.res.Garrisons[unitID] = cityID
		}
	}
}

func terrainWalkable(w *world.World, x, y int32) bool {
	return w.InBounds(x, y) && w.TerrainAt(x, y).Walkable()
}

// ─────────────────────────────────────────────────────────────
// Ciudades
// ─────────────────────────────────────────────────────────────

func (c *checker) cities() {
	// INV-CITY-017 se cuenta sobre todas las unidades vivas de la base, también
	// las excluidas —existen, y consumen población—, más las producciones en
	// curso, cuya plaza se reservó al ordenar (RN-ECON-010).
	population := make(map[int64]int32)
	for _, u := range c.in.Units {
		if u.CityID != nil {
			population[*u.CityID]++
		}
	}
	for _, p := range c.in.Productions {
		if p.InProgress() {
			population[p.CityID]++
		}
	}
	// INV-ECON-004: los edificios de cada ciudad, para el bono de sus casas.
	edificios := make(map[int64][]building.Building)
	for _, b := range c.in.Buildings {
		edificios[b.CityID] = append(edificios[b.CityID], b)
	}

	cities := append([]*city.City(nil), c.in.Cities...)
	sort.Slice(cities, func(i, j int) bool { return cities[i].ID < cities[j].ID })

	for _, ci := range cities {
		entity := fmt.Sprintf("city:%d", ci.ID)

		// INV-CITY-007: reubicar una ciudad existente está prohibido; se aborta.
		if !terrainWalkable(c.in.World, ci.CenterX, ci.CenterY) {
			c.find("INV-CITY-007", "ALTO", PolicyFailFast, entity,
				fmt.Sprintf("centro (%d,%d) fuera del mundo o sobre terreno intransitable", ci.CenterX, ci.CenterY))
		}

		// INV-CITY-017 e INV-CITY-003, juntos porque el CHECK de la base
		// (population <= population_limit) los ata en la misma fila.
		real := population[ci.ID]
		if ci.Population != real {
			c.find("INV-CITY-017", "ALTO", PolicyRepair, entity,
				fmt.Sprintf("population %d; las unidades vivas y las producciones en curso de la ciudad son %d", ci.Population, real))
			ci.Population = real
			c.touchCity(ci.ID)
		}
		limit, ok := c.in.EraCaps[ci.Era]
		bonus := building.PopulationBonus(edificios[ci.ID])
		if ok {
			// INV-ECON-004: la era más cinco por casa completa.
			limit = building.PopulationLimit(limit, edificios[ci.ID])
		} else {
			// La FK de cities.era lo impide; si faltara el catálogo, no hay límite
			// que recomputar.
			limit = ci.PopulationLimit
		}
		if ci.Population > limit {
			// INV-CITY-002: no se destruyen unidades. El límite sube hasta el
			// recuento, el CHECK se cumple y ninguna creación cabe hasta volver
			// bajo el límite de la era.
			c.find("INV-CITY-002", "ALTO", PolicyRepair, entity,
				fmt.Sprintf("%d unidades vivas sobre un límite de era de %d: el límite queda en el recuento", ci.Population, limit))
			limit = ci.Population
		}
		if ci.PopulationLimit != limit {
			// Sin casas es la regla de la era (INV-CITY-003); con casas, su
			// fórmula concreta (INV-ECON-004).
			inv := "INV-CITY-003"
			if bonus > 0 {
				inv = "INV-ECON-004"
			}
			c.find(inv, "ALTO", PolicyRepair, entity,
				fmt.Sprintf("population_limit %d; el de su era y sus casas es %d", ci.PopulationLimit, limit))
			ci.PopulationLimit = limit
			c.touchCity(ci.ID)
		}

		// INV-CITY-011: fuera de ONLINE hay marca de desconexión. Se fecha ahora:
		// retrasa la protección, nunca la adelanta.
		if ci.PresenceState != city.PresenceOnline && ci.LastOfflineAt == nil {
			now := c.in.Now
			ci.LastOfflineAt = &now
			c.touchCity(ci.ID)
			c.find("INV-CITY-011", "ALTO", PolicyRepair, entity,
				fmt.Sprintf("%s sin last_offline_at: se fecha en el arranque", ci.PresenceState))
		}
		// INV-CITY-012: ONLINE no tiene protección pendiente.
		if ci.PresenceState == city.PresenceOnline && ci.ProtectionUntil != nil {
			ci.ProtectionUntil = nil
			c.touchCity(ci.ID)
			c.find("INV-CITY-012", "ALTO", PolicyRepair, entity,
				"ONLINE con protection_until: se borra")
		}
	}
}

// ─────────────────────────────────────────────────────────────
// Tareas
// ─────────────────────────────────────────────────────────────

// tasks aplica INV-ECON-003: una tarea pertenece a una unidad viva y no
// guarnecida, es válida, y está en WORKING si y sólo si su unidad lo está. Se
// repara siempre hacia el lado sin tarea, que no regala madera.
func (c *checker) tasks() {
	unidades := make(map[int64]*unit.Unit, len(c.res.Units))
	for _, u := range c.res.Units {
		unidades[u.ID] = u
	}
	tareas := append([]task.Task(nil), c.in.Tasks...)
	sort.Slice(tareas, func(i, j int) bool { return tareas[i].UnitID < tareas[j].UnitID })

	trabajando := make(map[int64]bool)
	for _, t := range tareas {
		entity := fmt.Sprintf("unit:%d", t.UnitID)
		u, viva := unidades[t.UnitID]
		var motivo string
		switch {
		case !viva:
			motivo = "tarea de una unidad que no está viva o no entra en la simulación"
		case u.Status == unit.StatusGarrisoned:
			motivo = "tarea de una unidad guarnecida"
		case t.Validate() != nil:
			motivo = fmt.Sprintf("tarea inválida: %v", t.Validate())
		case t.Phase == task.PhaseWorking && u.Status != unit.StatusWorking:
			motivo = fmt.Sprintf("tarea en WORKING sobre una unidad %s", u.Status)
		}
		if motivo != "" {
			c.res.TaskDeletes = append(c.res.TaskDeletes, t.UnitID)
			c.find("INV-ECON-003", "ALTO", PolicyRepair, entity, motivo+": se borra")
			continue
		}
		if t.Phase == task.PhaseWorking {
			trabajando[t.UnitID] = true
		}
		c.res.Tasks = append(c.res.Tasks, t)
	}

	for _, u := range c.res.Units {
		if u.Status == unit.StatusWorking && !trabajando[u.ID] {
			u.Status = unit.StatusIdle
			c.touchUnit(u.ID)
			c.find("INV-ECON-003", "ALTO", PolicyRepair, fmt.Sprintf("unit:%d", u.ID),
				"WORKING sin tarea en WORKING: vuelve a IDLE en su última posición")
		}
	}
}

// ─────────────────────────────────────────────────────────────
// Control de territorios
// ─────────────────────────────────────────────────────────────

func (c *checker) controls() {
	for i := range c.res.Controls {
		ct := &c.res.Controls[i]
		entity := fmt.Sprintf("territory:%d", ct.TerritoryID)
		touched := false

		// INV-TERR-005: un dueño PLAYER que no existe se libera. Conservarlo
		// sacaría el territorio del juego para siempre.
		if ct.OwnerType == territory.OwnerPlayer && !c.playerExists(ct.OwnerID) {
			owner := "nulo"
			if ct.OwnerID != nil {
				owner = *ct.OwnerID
			}
			c.find("INV-TERR-005", "ALTO", PolicyRepair, entity,
				fmt.Sprintf("dueño PLAYER %s inexistente: el territorio se libera", owner))
			ct.OwnerType, ct.OwnerID, ct.CapturedAt = territory.OwnerNone, nil, nil
			touched = true
		}
		// INV-TERR-007: con dueño hay fecha de captura. Se fecha ahora: hace la
		// posesión más reciente de lo que fue, nunca más antigua.
		if ct.OwnerType != territory.OwnerNone && ct.CapturedAt == nil {
			now := c.in.Now
			ct.CapturedAt = &now
			touched = true
			c.find("INV-TERR-007", "MEDIO", PolicyRepair, entity,
				fmt.Sprintf("dueño %s sin captured_at: se fecha en el arranque", ct.OwnerType))
		}
		if touched {
			ct.Version++
			c.res.ControlRepairs = append(c.res.ControlRepairs, ct.TerritoryID)
		}
	}
}

func (c *checker) playerExists(id *string) bool {
	if id == nil {
		return false
	}
	parsed, err := uuid.Parse(*id)
	if err != nil {
		return false
	}
	_, ok := c.in.Players[parsed]
	return ok
}
