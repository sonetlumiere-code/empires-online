// Package unit modela las unidades del juego.
//
// Las estadísticas viven en un CATÁLOGO de datos, no repartidas por el código:
// añadir un tipo de unidad no debe obligar a tocar la lógica de movimiento ni la
// de combate. Ver ../../../../../docs/specs/unit.md
package unit

import (
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/economy"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
)

// Status es el estado de una unidad.
type Status string

const (
	StatusIdle       Status = "IDLE"
	StatusMoving     Status = "MOVING"
	StatusGarrisoned Status = "GARRISONED"
	StatusHidden     Status = "HIDDEN"
	// StatusWorking: parada sobre el tile de un recurso hasta que vence el
	// temporizador de su tarea (RN-ECON-003). Sólo existe con una tarea en fase
	// WORKING (INV-ECON-003).
	StatusWorking Status = "WORKING"
	// StatusAttacking: en alcance de su objetivo, golpeándolo en la fase 4
	// (SPEC-COMBAT-RAID §4.2). Sólo existe en la RAM: el enfrentamiento no se
	// persiste y el volcado la escribe IDLE (ADR-024 §2).
	StatusAttacking Status = "ATTACKING"
	// StatusClaiming: plantada con el estandarte de su dueño, reclamando el
	// territorio en que está (SPEC-TERRITORY-CONQUEST §4.1). No se mueve ni
	// responde a ataques. Sólo existe en la RAM, como ATTACKING: el volcado la
	// escribe IDLE y lo que dura es la fila de la reclamación (ADR-034 §5).
	StatusClaiming Status = "CLAIMING"
	StatusDead     Status = "DEAD"
)

// Valid indica si el estado pertenece al conjunto conocido.
func (s Status) Valid() bool {
	switch s {
	case StatusIdle, StatusMoving, StatusGarrisoned, StatusHidden, StatusWorking, StatusAttacking, StatusClaiming, StatusDead:
		return true
	}
	return false
}

// Type identifica un tipo de unidad del catálogo.
type Type string

const (
	// TypeVillager recolecta; no ataca.
	TypeVillager Type = "VILLAGER"
	// TypeWarrior ataca; no recolecta, pero carga el botín (SPEC-COMBAT-RAID §4.1).
	TypeWarrior Type = "WARRIOR"
	// TypeCaravan lleva la mercancía de un trato de una ciudad a otra
	// (SPEC-CARAVANS §4.1). No se entrena ni obedece órdenes: la crea un trato
	// aceptado y camina sola.
	TypeCaravan Type = "CARAVAN"

	// Unidades exclusivas de civilización (SPEC-CIVILIZATION-UNITS §4.1): cada
	// una la entrena sólo la civilización que la tiene en civilizations.unit.
	// El catálogo no dice de quién es cada una (RN-PLAYER-012, ADR-043 §2).

	// TypeLegionary aguanta: conquista y defiende.
	TypeLegionary Type = "LEGIONARY"
	// TypeCataphract es rápido: alcanza y persigue.
	TypeCataphract Type = "CATAPHRACT"
	// TypeHorseArcher golpea a distancia.
	TypeHorseArcher Type = "HORSE_ARCHER"
	// TypeBerserker pega fuerte: saquea y derriba.
	TypeBerserker Type = "BERSERKER"
)

// Definition son las estadísticas inmutables de un tipo de unidad.
type Definition struct {
	Type Type
	// MaxHP son los puntos de vida a pleno.
	MaxHP int32
	// BaseMsPerTile es lo que tarda en cruzar un tile de coste 1.0 sin diagonal.
	BaseMsPerTile int64
	// PopulationCost es cuánta población consume del límite de la ciudad.
	PopulationCost int32

	// TrainCosts y TrainMs son lo que cuesta y tarda entrenarla en una ciudad
	// (RN-ECON-010). Sin coste no se entrena. El coste puede ser de varios
	// recursos, que se debitan juntos (RN-CIVUNIT-003, ADR-043 §1).
	TrainCosts economy.Costs
	TrainMs    int64
	// OwnGoodCost es cuánto del bien de la civilización del dueño cuesta
	// además de TrainCosts. El bien lo pone el catálogo de civilizaciones al
	// ordenar (CostsWith), nunca este catálogo: así no puede ser uno ajeno.
	OwnGoodCost int64
	// RequiresEra es la era mínima de la ciudad para entrenarla; "" si no pide
	// ninguna (RN-CIVUNIT-002).
	RequiresEra city.Era
	// Exclusive: es la unidad de una civilización y sólo la entrena ella
	// (INV-UNIT-012). Cuál es de quién lo dice civilizations.unit.
	Exclusive bool
	// GatherWorkMs es lo que trabaja sobre un recurso antes de cargar, y
	// GatherCarry lo que carga al terminar (RN-ECON-003). Cero: no recolecta.
	GatherWorkMs int64
	GatherCarry  int32

	// AttackDamage es lo que quita cada golpe, AttackIntervalMs lo que tarda
	// entre golpes y AttackRange el alcance en tiles Chebyshev. Cero: no ataca
	// (SPEC-COMBAT-RAID §4.1).
	AttackDamage     int32
	AttackIntervalMs int64
	AttackRange      int32
	// LootCarry es cuánto botín puede cargar (RN-COMBAT-010).
	LootCarry int32
	// Autonomous: nadie le da órdenes, ni su dueño (RN-CARAVAN-003).
	Autonomous bool
}

// CanTrain indica si el tipo se entrena en una ciudad.
func (d Definition) CanTrain() bool {
	if d.TrainMs <= 0 {
		return false
	}
	for _, c := range d.TrainCosts {
		if c.Amount > 0 {
			return true
		}
	}
	return d.OwnGoodCost > 0
}

// CostsWith es el coste completo de entrenarla para una ciudad cuya
// civilización elabora `good`: TrainCosts y, si OwnGoodCost > 0, el bien
// (ADR-043 §1). Devuelve una lista nueva: el catálogo no se toca.
func (d Definition) CostsWith(good economy.Resource) economy.Costs {
	out := make(economy.Costs, 0, len(d.TrainCosts)+1)
	out = append(out, d.TrainCosts...)
	if d.OwnGoodCost > 0 {
		out = append(out, economy.Cost{Resource: good, Amount: d.OwnGoodCost})
	}
	return out
}

// CanGather indica si el tipo recolecta (RN-ECON-001).
func (d Definition) CanGather() bool { return d.GatherWorkMs > 0 && d.GatherCarry > 0 }

// CanAttack indica si el tipo ataca (RN-COMBAT-001).
func (d Definition) CanAttack() bool {
	return d.AttackDamage > 0 && d.AttackIntervalMs > 0 && d.AttackRange > 0
}

// catalog es la tabla de definiciones. Vive en código; cuando haya decenas de
// tipos se moverá a la base de datos sin cambiar la interfaz de consulta. El
// catálogo de presentación de packages/protocol (units.json) lo copia, y un
// test los compara campo a campo (ADR-043 §7).
var catalog = map[Type]Definition{
	// Coste y tiempos de SPEC-ECON-WOOD §4.5: parámetros de equilibrio, no de
	// diseño. Se ajustan aquí sin tocar la spec mientras sigan siendo catálogo.
	TypeVillager: {
		Type: TypeVillager, MaxHP: 40, BaseMsPerTile: 600, PopulationCost: 1,
		TrainCosts: economy.Costs{{Resource: economy.ResourceWood, Amount: 50}}, TrainMs: 20_000,
		GatherWorkMs: 10_000, GatherCarry: 10,
	},
	// SPEC-COMBAT-RAID §4.1: mata a un aldeano en cinco golpes (~7 s) y a otro
	// guerrero en ocho (~12 s). Parámetros de equilibrio, como los de arriba.
	TypeWarrior: {
		Type: TypeWarrior, MaxHP: 60, BaseMsPerTile: 600, PopulationCost: 1,
		TrainCosts: economy.Costs{{Resource: economy.ResourceWood, Amount: 60}}, TrainMs: 25_000,
		AttackDamage: 8, AttackIntervalMs: 1_500, AttackRange: 1, LootCarry: 20,
	},
	// SPEC-CIVILIZATION-UNITS §4.2 (Q3): cada una gana el duelo contra un
	// guerrero sin llegar a valer dos, y destaca en una cosa. Las cuatro cuestan
	// 80 de madera, 40 de piedra y 10 del bien propio, tardan 35 s y piden la
	// Edad de Bronce (Q4, Q5). Parámetros de equilibrio.
	TypeLegionary:   exclusive(TypeLegionary, 110, 600, 8, 1, 20),
	TypeCataphract:  exclusive(TypeCataphract, 80, 450, 10, 1, 20),
	TypeHorseArcher: exclusive(TypeHorseArcher, 60, 540, 8, 3, 20),
	TypeBerserker:   exclusive(TypeBerserker, 60, 600, 14, 1, 30),
	// SPEC-CARAVANS §4.1 (Q4): un guerrero solo la captura en diez golpes (15 s)
	// y, al doble de su velocidad, la alcanza siempre. No ocupa población.
	TypeCaravan: {
		Type: TypeCaravan, MaxHP: 80, BaseMsPerTile: 1_200, PopulationCost: 0,
		Autonomous: true,
	},
}

// exclusive es una unidad de civilización con el coste, el plazo y la edad
// comunes a las cuatro (SPEC-CIVILIZATION-UNITS Q4, Q5).
func exclusive(t Type, hp int32, msPerTile int64, damage, attackRange, loot int32) Definition {
	return Definition{
		Type: t, MaxHP: hp, BaseMsPerTile: msPerTile, PopulationCost: 1,
		TrainCosts: economy.Costs{
			{Resource: economy.ResourceWood, Amount: 80},
			{Resource: economy.ResourceStone, Amount: 40},
		},
		TrainMs: 35_000, OwnGoodCost: 10, RequiresEra: city.EraBronze, Exclusive: true,
		AttackDamage: damage, AttackIntervalMs: 1_500, AttackRange: attackRange, LootCarry: loot,
	}
}

// Types devuelve los tipos del catálogo en orden estable (el de su código).
func Types() []Type {
	return []Type{TypeVillager, TypeWarrior, TypeCaravan, TypeLegionary, TypeCataphract, TypeHorseArcher, TypeBerserker}
}

// ErrUnknownType indica un tipo de unidad ausente del catálogo.
var ErrUnknownType = errors.New("tipo de unidad desconocido")

// Lookup devuelve la definición de un tipo de unidad.
func Lookup(t Type) (Definition, error) {
	d, ok := catalog[t]
	if !ok {
		return Definition{}, fmt.Errorf("%w: %q", ErrUnknownType, t)
	}
	return d, nil
}

// Unit es una unidad del mundo.
//
// X e Y son la posición autoritativa CONSOLIDADA. Mientras la unidad tiene un
// movimiento activo, la posición vigente se deriva de la polilínea: estos campos
// son la última posición confirmada, no necesariamente la actual.
type Unit struct {
	ID       int64
	PlayerID uuid.UUID
	CityID   *int64
	Type     Type
	X        int32
	Y        int32
	HP       int32
	MaxHP    int32
	Status   Status
	// Loot es el botín que carga un WARRIOR hasta depositarlo en su ciudad
	// (RN-COMBAT-010). La carga de un aldeano vive en su tarea, no aquí.
	Loot int32
}

// Tile devuelve la posición consolidada.
func (u *Unit) Tile() world.Tile { return world.Tile{X: u.X, Y: u.Y} }

// SetTile fija la posición consolidada.
func (u *Unit) SetTile(t world.Tile) { u.X, u.Y = t.X, t.Y }

// IsAlive indica si la unidad sigue en juego.
func (u *Unit) IsAlive() bool { return u.Status != StatusDead && u.HP > 0 }

// Errores de dominio. Cada uno se traduce a un código estable del protocolo en
// la capa de aplicación; el dominio no conoce el protocolo.
var (
	ErrNotOwned    = errors.New("la unidad no pertenece al jugador")
	ErrDead        = errors.New("la unidad está muerta")
	ErrGarrisoned  = errors.New("la unidad está guarnecida")
	ErrNotMovable  = errors.New("la unidad no puede moverse en su estado actual")
	ErrNotFound    = errors.New("unidad no encontrada")
	ErrPopulation  = errors.New("se alcanzó el límite de población")
	ErrInvalidType = errors.New("tipo de unidad inválido para esta operación")
	// ErrAutonomous: la unidad no admite órdenes de nadie (RN-CARAVAN-003).
	ErrAutonomous = errors.New("la caravana no admite órdenes")
)

// EnsureOwnedBy comprueba la propiedad de la unidad.
//
// Es la primera validación de TODO comando (INV-PLAYER-001). Se comprueba antes
// que nada para no filtrar, mediante mensajes de error distintos, la existencia
// de unidades ajenas.
func (u *Unit) EnsureOwnedBy(playerID uuid.UUID) error {
	if u.PlayerID != playerID {
		return ErrNotOwned
	}
	return nil
}

// EnsureOrderable comprueba que la unidad admite órdenes: una caravana no
// admite ninguna, ni de su dueño (RN-CARAVAN-003).
func (u *Unit) EnsureOrderable() error {
	if d, err := Lookup(u.Type); err == nil && d.Autonomous {
		return ErrAutonomous
	}
	return nil
}

// EnsureCanMove comprueba que el estado de la unidad admite una orden de movimiento.
func (u *Unit) EnsureCanMove() error {
	if err := u.EnsureOrderable(); err != nil {
		return err
	}
	switch u.Status {
	case StatusIdle, StatusMoving:
		// Moverse mientras se mueve es válido: reemplaza el movimiento anterior.
		return nil
	case StatusWorking:
		// Una orden de movimiento cancela la tarea y pierde lo cargado
		// (RN-ECON-003).
		return nil
	case StatusAttacking:
		// Moverse termina el enfrentamiento (RN-COMBAT-008).
		return nil
	case StatusClaiming:
		// Moverse termina la reclamación (RN-CONQ-005).
		return nil
	case StatusDead:
		return ErrDead
	case StatusGarrisoned:
		return ErrGarrisoned
	case StatusHidden:
		// Salir de una Safe Zone es legítimo: moverse revela la unidad.
		return nil
	default:
		return ErrNotMovable
	}
}
