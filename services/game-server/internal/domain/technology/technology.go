// Package technology modela el árbol tecnológico: el catálogo de tecnologías,
// su validación, la composición de sus efectos con los rasgos de la
// civilización y la orden de investigar.
//
// Es dominio puro. El catálogo vive en la tabla `technologies` (ADR-046 §1): la
// carga lo convierte en un Catalog y lo valida, y un catálogo inválido impide
// arrancar (INV-TECH-002). La reserva, la escritura y el fin de una
// investigación son de la simulación, como los del avance de era (ADR-030).
//
// Spec: ../../../../../docs/specs/technologies.md.
package technology

import (
	"errors"
	"fmt"
	"sort"

	"github.com/google/uuid"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/economy"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/player"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/production"
)

// Technology es una fila del catálogo.
type Technology struct {
	ID   int32
	Code string
	Name string
	// Era es la era que pide: la de la ciudad o una posterior.
	Era city.Era
	// Civilization es el código de la civilización de una exclusiva; vacío en
	// una común (SPEC-TECHNOLOGIES Q3).
	Civilization string
	// Costs son los recursos fijos que cobra (madera y piedra).
	Costs economy.Costs
	// ForeignGoods son los bienes de otras civilizaciones que pide además, en
	// cualquier mezcla (ADR-036 §5); 0 si ninguno.
	ForeignGoods int64
	// OwnGood es lo que pide del bien de la civilización del jugador; sólo una
	// exclusiva lo pide (SPEC-TECHNOLOGIES Q5).
	OwnGood    int64
	DurationMs int64
	// Requires son los códigos de las tecnologías que hay que tener antes.
	Requires []string
	// Effects son sus efectos: una clave del catálogo de rasgos y un entero
	// por mil (RN-TECH-001).
	Effects map[string]int32
}

// Exclusive dice si la tecnología es de una sola civilización.
func (t Technology) Exclusive() bool { return t.Civilization != "" }

// AvailableTo dice si una civilización puede investigarla.
func (t Technology) AvailableTo(civ string) bool { return !t.Exclusive() || t.Civilization == civ }

// ErrInvalidCatalog marca un catálogo que no se puede aplicar (INV-TECH-002).
var ErrInvalidCatalog = errors.New("catálogo de tecnologías inválido")

// Catalog es el catálogo validado. Es inmutable mientras el proceso vive.
type Catalog struct {
	byCode map[string]Technology
	codes  []string
}

// NewCatalog valida un catálogo (INV-TECH-002, ADR-046 §1): códigos únicos y no
// vacíos; una era del catálogo de eras; efectos con claves de rasgo y valores
// positivos; costes válidos y un plazo positivo; requisitos que existen, que no
// piden una era posterior, que no son exclusivos de otra civilización y sin
// ciclos; una exclusiva pide su bien propio y ningún ajeno, y una común no pide
// bien propio. `civs` son los códigos de civilización del catálogo.
func NewCatalog(ts []Technology, civs []string) (Catalog, error) {
	known := make(map[string]bool, len(player.TraitKeys()))
	for _, k := range player.TraitKeys() {
		known[k] = true
	}
	civOK := make(map[string]bool, len(civs))
	for _, c := range civs {
		civOK[c] = true
	}
	c := Catalog{byCode: make(map[string]Technology, len(ts))}
	for _, t := range ts {
		if t.Code == "" {
			return Catalog{}, fmt.Errorf("%w: una tecnología sin código", ErrInvalidCatalog)
		}
		if _, dup := c.byCode[t.Code]; dup {
			return Catalog{}, fmt.Errorf("%w: %s repetida", ErrInvalidCatalog, t.Code)
		}
		if !t.Era.AtLeast(city.EraStone) {
			return Catalog{}, fmt.Errorf("%w: %s pide la era desconocida %q", ErrInvalidCatalog, t.Code, t.Era)
		}
		if t.DurationMs <= 0 {
			return Catalog{}, fmt.Errorf("%w: %s dura %d ms", ErrInvalidCatalog, t.Code, t.DurationMs)
		}
		if err := t.Costs.Validate(); err != nil {
			return Catalog{}, fmt.Errorf("%w: %s: %w", ErrInvalidCatalog, t.Code, err)
		}
		for _, cost := range t.Costs {
			if cost.Resource.IsGood() {
				return Catalog{}, fmt.Errorf("%w: %s cobra %s como coste fijo", ErrInvalidCatalog, t.Code, cost.Resource)
			}
		}
		if t.ForeignGoods < 0 || t.OwnGood < 0 {
			return Catalog{}, fmt.Errorf("%w: %s pide bienes negativos", ErrInvalidCatalog, t.Code)
		}
		if t.Exclusive() {
			if !civOK[t.Civilization] {
				return Catalog{}, fmt.Errorf("%w: %s es de la civilización desconocida %q", ErrInvalidCatalog, t.Code, t.Civilization)
			}
			if t.OwnGood == 0 || t.ForeignGoods != 0 {
				return Catalog{}, fmt.Errorf("%w: %s es exclusiva: pide su bien propio y ningún ajeno", ErrInvalidCatalog, t.Code)
			}
		} else if t.OwnGood != 0 {
			return Catalog{}, fmt.Errorf("%w: %s es común y pide el bien propio", ErrInvalidCatalog, t.Code)
		}
		if len(t.Effects) == 0 {
			return Catalog{}, fmt.Errorf("%w: %s no tiene efectos", ErrInvalidCatalog, t.Code)
		}
		for k, v := range t.Effects {
			if !known[k] {
				return Catalog{}, fmt.Errorf("%w: %s declara la clave desconocida %q", ErrInvalidCatalog, t.Code, k)
			}
			if v <= 0 {
				return Catalog{}, fmt.Errorf("%w: %s.%s = %d, debe ser positivo", ErrInvalidCatalog, t.Code, k, v)
			}
		}
		t.Costs = append(economy.Costs(nil), t.Costs...)
		t.Requires = append([]string(nil), t.Requires...)
		effects := make(map[string]int32, len(t.Effects))
		for k, v := range t.Effects {
			effects[k] = v
		}
		t.Effects = effects
		c.byCode[t.Code] = t
		c.codes = append(c.codes, t.Code)
	}
	sort.Strings(c.codes)
	for _, code := range c.codes {
		t := c.byCode[code]
		for _, r := range t.Requires {
			req, ok := c.byCode[r]
			if !ok {
				return Catalog{}, fmt.Errorf("%w: %s pide %s, que no existe", ErrInvalidCatalog, code, r)
			}
			if !t.Era.AtLeast(req.Era) {
				return Catalog{}, fmt.Errorf("%w: %s pide %s, de una era posterior", ErrInvalidCatalog, code, r)
			}
			if req.Exclusive() && req.Civilization != t.Civilization {
				return Catalog{}, fmt.Errorf("%w: %s pide %s, exclusiva de otra civilización", ErrInvalidCatalog, code, r)
			}
		}
	}
	if cycle := c.findCycle(); cycle != "" {
		return Catalog{}, fmt.Errorf("%w: los requisitos de %s forman un ciclo", ErrInvalidCatalog, cycle)
	}
	return c, nil
}

// findCycle devuelve una tecnología de un ciclo de requisitos, o "".
func (c Catalog) findCycle() string {
	const (
		unseen = iota
		open
		done
	)
	state := make(map[string]int, len(c.codes))
	var visit func(code string) bool
	visit = func(code string) bool {
		switch state[code] {
		case open:
			return true
		case done:
			return false
		}
		state[code] = open
		for _, r := range c.byCode[code].Requires {
			if visit(r) {
				return true
			}
		}
		state[code] = done
		return false
	}
	for _, code := range c.codes {
		if visit(code) {
			return code
		}
	}
	return ""
}

// Lookup devuelve una tecnología del catálogo.
func (c Catalog) Lookup(code string) (Technology, bool) {
	t, ok := c.byCode[code]
	if !ok {
		return Technology{}, false
	}
	t.Costs = append(economy.Costs(nil), t.Costs...)
	return t, true
}

// Codes son los códigos del catálogo, en orden.
func (c Catalog) Codes() []string { return append([]string(nil), c.codes...) }

// Len es cuántas tecnologías tiene el catálogo.
func (c Catalog) Len() int { return len(c.codes) }

// Compose compone un rasgo por mil con un efecto por mil, con la fórmula de
// cantidad de los rasgos: redondeo al más cercano y un mínimo de 1
// (SPEC-TECHNOLOGIES §4.4, INV-TECH-002).
func Compose(r, e int32) int32 {
	if r <= 0 {
		r = player.TraitNeutral
	}
	if e <= 0 {
		return r
	}
	out := (int64(r)*int64(e) + int64(player.TraitNeutral)/2) / int64(player.TraitNeutral)
	if out < 1 {
		return 1
	}
	return int32(out)
}

// Traits son los rasgos efectivos de un jugador: para cada clave del catálogo
// de rasgos, el de su civilización compuesto con el efecto de cada tecnología
// que tiene y que lleva esa clave, en orden de código (INV-TECH-002). Una
// tecnología que el catálogo no conoce no cuenta.
func (c Catalog) Traits(civ player.Civilization, owned map[string]bool) map[string]int32 {
	out := make(map[string]int32, len(player.TraitKeys()))
	for _, k := range player.TraitKeys() {
		out[k] = civ.Trait(k)
	}
	for _, code := range c.codes {
		if !owned[code] {
			continue
		}
		for k, e := range c.byCode[code].Effects {
			out[k] = Compose(out[k], e)
		}
	}
	return out
}

// Research es la investigación en curso de una ciudad.
type Research struct {
	CityID        int64
	Technology    string
	CompletesAtMs int64
	RequestID     string
}

// Due indica si la investigación venció en `nowMs`.
func (r Research) Due(nowMs int64) bool { return r.CompletesAtMs <= nowMs }

// Errores de dominio. CITY_NOT_FOUND y CITY_BUSY son los del entrenamiento y del
// avance de era; los demás son INVALID_TARGET con su motivo (SPEC-TECHNOLOGIES §5).
var (
	// ErrCityNotFound: la ciudad no existe o no es del jugador.
	ErrCityNotFound = production.ErrCityNotFound
	// ErrCityBusy: la ciudad ya investiga o avanza de era (RN-TECH-003).
	ErrCityBusy = production.ErrCityBusy
	// ErrUnavailable: la tecnología no existe o es exclusiva de otra
	// civilización.
	ErrUnavailable = errors.New("esa tecnología no está a tu alcance")
	// ErrOwned: el jugador ya la tiene.
	ErrOwned = errors.New("ya tienes esa tecnología")
	// ErrEraRequired: la ciudad no ha llegado a su era.
	ErrEraRequired = errors.New("la tecnología pide una era posterior")
	// ErrRequirement: le falta una tecnología que se pide antes.
	ErrRequirement = errors.New("la tecnología pide otra antes")
)

// Order es lo que la orden de investigar decide: la tecnología y su coste ya
// resuelto (los bienes ajenos o el propio convertidos en costes concretos).
type Order struct {
	Technology Technology
	Costs      economy.Costs
}

// CanResearch aplica RN-TECH-002 a RN-TECH-004 en el orden de la spec: ciudad
// propia, tecnología al alcance de la civilización, no la tiene, la era, sus
// requisitos, ciudad libre y almacén suficiente. `busy` dice si la ciudad ya
// investiga o avanza de era; `owned`, las tecnologías del jugador; `own` y
// `order`, el bien propio y el orden de los bienes del catálogo, como en el
// avance de era (RN-GOODS-006); `stock`, el almacén.
func CanResearch(
	c *city.City, requester uuid.UUID, code string, cat Catalog, civ player.Civilization, owned map[string]bool,
	busy bool, order []economy.Resource, stock func(economy.Resource) *economy.Stock,
) (Order, error) {
	if c == nil || c.OwnerPlayerID != requester {
		return Order{}, ErrCityNotFound
	}
	t, ok := cat.Lookup(code)
	if !ok || !t.AvailableTo(civ.Code) {
		return Order{}, ErrUnavailable
	}
	if owned[code] {
		return Order{}, fmt.Errorf("%w: %s", ErrOwned, t.Name)
	}
	if !c.Era.AtLeast(t.Era) {
		return Order{}, fmt.Errorf("%w: %s pide la %s", ErrEraRequired, t.Name, EraName(t.Era))
	}
	for _, r := range t.Requires {
		if !owned[r] {
			name := r
			if req, ok := cat.Lookup(r); ok {
				name = req.Name
			}
			return Order{}, fmt.Errorf("%w: %s pide %s", ErrRequirement, t.Name, name)
		}
	}
	if busy {
		return Order{}, ErrCityBusy
	}
	costs := append(economy.Costs(nil), t.Costs...)
	if t.OwnGood > 0 {
		if !civ.Good.IsGood() {
			return Order{}, ErrUnavailable
		}
		costs = append(costs, economy.Cost{Resource: civ.Good, Amount: t.OwnGood})
	}
	if !costs.CanAfford(stock) {
		return Order{}, economy.ErrInsufficientResources
	}
	if t.ForeignGoods > 0 {
		left := t.ForeignGoods
		for _, g := range order {
			if left <= 0 {
				break
			}
			if g == civ.Good || !g.IsGood() {
				continue
			}
			var have int64
			if st := stock(g); st != nil {
				have = st.Amount
			}
			take := min(have, left)
			if take <= 0 {
				continue
			}
			costs = append(costs, economy.Cost{Resource: g, Amount: take})
			left -= take
		}
		if left > 0 {
			return Order{}, fmt.Errorf("%w: faltan %d bienes de otras civilizaciones", economy.ErrInsufficientResources, left)
		}
	}
	return Order{Technology: t, Costs: costs}, nil
}

// EraName es el nombre de una era para un motivo («Edad de Bronce»).
func EraName(e city.Era) string {
	switch e {
	case city.EraStone:
		return "Edad de Piedra"
	case city.EraBronze:
		return "Edad de Bronce"
	case city.EraIron:
		return "Edad de Hierro"
	case city.EraCastle:
		return "Edad de los Castillos"
	}
	return string(e)
}
