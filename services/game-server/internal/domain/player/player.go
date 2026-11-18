// Package player modela al jugador y sus dos ejes de identidad.
//
// Civilization (identidad cultural: unidades, tecnologías, bonos) y Faction
// (bando global: ORDER / CHAOS / NEUTRAL) son ORTOGONALES. Un jugador Roman puede
// militar en Chaos y un Norse en Order. Modelarlos como una sola entidad haría
// imposible esa combinación, así que son dos referencias independientes.
// Ver ../../../../../docs/specs/player.md
package player

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/economy"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
)

// Player es la identidad durable de un jugador.
type Player struct {
	ID             uuid.UUID
	Username       string
	CivilizationID int32
	FactionID      int32
	CreatedAt      time.Time
	LastSeenAt     *time.Time
}

// Civilization es una fila del catálogo de civilizaciones.
//
// Traits es un mapa de rasgos data-driven. Existe precisamente para que NO haya
// condicionales del tipo `if civilization == "ROMAN"` repartidos por el código:
// los sistemas consultan el rasgo que les concierne y aplican el modificador
// (RN-PLAYER-012).
//
// Cada rasgo es un entero por mil: 1000 es «sin efecto», 1100 es +10 % y 950 es
// −5 % (SPEC-CIVILIZATION RN-CIV-003, ADR-035 §1). La civilización cero —sin
// rasgos— es neutra: todo vale 1000.
type Civilization struct {
	ID   int32
	Code string
	Name string
	// Good es el bien que sólo elabora el taller de esta civilización
	// (SPEC-CIVILIZATION-GOODS RN-GOODS-001, ADR-036 §2).
	Good economy.Resource
	// Unit es su unidad exclusiva, la única que sólo ella entrena
	// (SPEC-CIVILIZATION-UNITS §4.1, civilizations.unit).
	Unit   unit.Type
	Traits map[string]int32
}

// Claves de los rasgos que aplica el servidor (SPEC-CIVILIZATION §4.2).
const (
	// TraitMoveSpeed divide lo que tarda cada paso de toda unidad del jugador.
	TraitMoveSpeed = "unit.move_speed"
	// TraitProductionSpeed divide lo que tarda una obra y un entrenamiento.
	TraitProductionSpeed = "production.speed"
	// TraitHouseHP multiplica la vida de una casa frente al asedio.
	TraitHouseHP = "building.house_hp"
	// TraitLootCarry multiplica lo que carga un guerrero al saquear o al
	// recoger botín.
	TraitLootCarry = "combat.loot_carry"
	// TraitGatherSpeed divide lo que tarda el trabajo en el tile de cada viaje
	// de recolección.
	TraitGatherSpeed = "economy.gather_speed"
	// TraitAttackDamage multiplica el daño de cada golpe de una unidad que
	// ataca. Ninguna civilización la declara: la llevan las tecnologías
	// (SPEC-TECHNOLOGIES §4.3, ADR-046 §2).
	TraitAttackDamage = "combat.attack_damage"
	// TraitGatherCarry multiplica lo que carga un aldeano en cada viaje de
	// recolección. Como la anterior, sólo la llevan las tecnologías.
	TraitGatherCarry = "economy.gather_carry"
)

// TraitNeutral es el valor de un rasgo sin efecto, y el de una clave ausente.
const TraitNeutral int32 = 1000

// TraitKeys devuelve las claves conocidas, en orden estable.
func TraitKeys() []string {
	return []string{TraitMoveSpeed, TraitProductionSpeed, TraitHouseHP, TraitLootCarry, TraitGatherSpeed, TraitAttackDamage, TraitGatherCarry}
}

// ErrInvalidTrait marca un catálogo con un rasgo que no se puede aplicar.
var ErrInvalidTrait = errors.New("rasgo de civilización inválido")

// Trait devuelve el rasgo por mil de una clave, o TraitNeutral si la
// civilización no la declara. Ésta es la ÚNICA vía admitida para consultar
// bonos culturales.
func (c Civilization) Trait(key string) int32 {
	if v, ok := c.Traits[key]; ok {
		return v
	}
	return TraitNeutral
}

// ErrInvalidGood marca un catálogo cuyos bienes no son uno por civilización.
var ErrInvalidGood = errors.New("bien de civilización inválido")

// ErrInvalidUnit marca un catálogo cuyas unidades exclusivas no son una por
// civilización (INV-UNIT-013).
var ErrInvalidUnit = errors.New("unidad de civilización inválida")

// ValidateCatalog comprueba cada civilización con Validate y, además, que cada
// una tenga un bien de civilización y que ninguno se repita (RN-GOODS-001), y
// que cada una tenga una unidad exclusiva y entrenable del catálogo de
// unidades, que no comparte, sin que ningún tipo exclusivo quede sin
// civilización (INV-UNIT-013, ADR-043 §2).
func ValidateCatalog(cs []Civilization) error {
	seen := make(map[economy.Resource]string, len(cs))
	for _, c := range cs {
		if err := c.Validate(); err != nil {
			return err
		}
		if !c.Good.IsGood() {
			return fmt.Errorf("%w: %s produce %q, que no es un bien", ErrInvalidGood, c.Code, c.Good)
		}
		if other, dup := seen[c.Good]; dup {
			return fmt.Errorf("%w: %s y %s producen %s", ErrInvalidGood, other, c.Code, c.Good)
		}
		seen[c.Good] = c.Code
	}
	return validateUnits(cs)
}

// validateUnits es la mitad de ValidateCatalog que mira las unidades
// exclusivas (INV-UNIT-013): va después de los bienes para que un catálogo con
// los dos problemas diga primero el más antiguo.
func validateUnits(cs []Civilization) error {
	units := make(map[unit.Type]string, len(cs))
	for _, c := range cs {
		def, err := unit.Lookup(c.Unit)
		if err != nil || !def.Exclusive || !def.CanTrain() {
			return fmt.Errorf("%w: %s tiene %q, que no es una unidad exclusiva entrenable", ErrInvalidUnit, c.Code, c.Unit)
		}
		if other, dup := units[c.Unit]; dup {
			return fmt.Errorf("%w: %s y %s comparten %s", ErrInvalidUnit, other, c.Code, c.Unit)
		}
		units[c.Unit] = c.Code
	}
	for _, t := range unit.Types() {
		if def, err := unit.Lookup(t); err == nil && def.Exclusive {
			if _, ok := units[t]; !ok {
				return fmt.Errorf("%w: %s es exclusiva y ninguna civilización la tiene", ErrInvalidUnit, t)
			}
		}
	}
	return nil
}

// Validate comprueba que todo rasgo sea de una clave conocida y un entero
// positivo (INV-PLAYER-012). Que sea entero lo garantiza el tipo: un `1.1` en el
// jsonb no se puede leer como int32.
func (c Civilization) Validate() error {
	known := make(map[string]bool, len(TraitKeys()))
	for _, k := range TraitKeys() {
		known[k] = true
	}
	for k, v := range c.Traits {
		if !known[k] {
			return fmt.Errorf("%w: %s declara la clave desconocida %q", ErrInvalidTrait, c.Code, k)
		}
		if v <= 0 {
			return fmt.Errorf("%w: %s.%s = %d, debe ser positivo", ErrInvalidTrait, c.Code, k, v)
		}
	}
	return nil
}

// ScaleDuration aplica un rasgo de velocidad a una duración: la divide, con
// redondeo al más cercano y un mínimo de 1 (RN-CIV-004, ADR-035 §2). Con 1000
// devuelve la base tal cual.
func ScaleDuration(base int64, perMille int32) int64 {
	if perMille <= 0 || base <= 0 {
		return base
	}
	r := int64(perMille)
	out := (base*int64(TraitNeutral) + r/2) / r
	if out < 1 {
		return 1
	}
	return out
}

// ScaleAmount aplica un rasgo de cantidad a un valor: lo multiplica, con
// redondeo al más cercano y un mínimo de 1 (RN-CIV-004, ADR-035 §2).
func ScaleAmount(base int32, perMille int32) int32 {
	if perMille <= 0 || base <= 0 {
		return base
	}
	out := (int64(base)*int64(perMille) + int64(TraitNeutral)/2) / int64(TraitNeutral)
	if out < 1 {
		return 1
	}
	return int32(out)
}

// Faction es un bando global del mundo.
type Faction struct {
	ID   int32
	Code string
	Name string
}

// InitialVillagers es cuántos aldeanos recibe un jugador nuevo (canon §10,
// INV-PLAYER-003). Es el único sitio del código donde vive el número: la API de
// alta, la siembra de desarrollo y el Bootstrapper lo leen de aquí.
const InitialVillagers = 3

// Códigos de facción del MVP.
const (
	FactionOrder   = "ORDER"
	FactionChaos   = "CHAOS"
	FactionNeutral = "NEUTRAL"
)

// Errores de dominio.
var (
	ErrNotFound            = errors.New("jugador no encontrado")
	ErrUsernameTaken       = errors.New("el nombre de usuario ya está en uso")
	ErrInvalidUsername     = errors.New("nombre de usuario inválido")
	ErrAlreadyBootstrapped = errors.New("el jugador ya tiene su ciudad inicial")
)

// ValidateUsername aplica las reglas de nombre de usuario en el boundary.
func ValidateUsername(name string) error {
	if len(name) < 3 || len(name) > 24 {
		return ErrInvalidUsername
	}
	for _, r := range name {
		valid := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-'
		if !valid {
			return ErrInvalidUsername
		}
	}
	return nil
}
