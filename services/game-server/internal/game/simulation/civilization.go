package simulation

import (
	"github.com/google/uuid"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/economy"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/player"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
)

// Rasgos de civilización (SPEC-CIVILIZATION, ADR-035).
//
// La civilización de un jugador es la de su ciudad (city.City.Civilization, que
// la carga lee de players y la fundación trae con IntroducePlayer), y sus rasgos
// salen del catálogo cargado al arrancar. Los siete puntos de consumo piden el
// rasgo con State.trait, que lo compone con las tecnologías del jugador
// (technologies.go, ADR-046 §2), y lo aplican con player.ScaleDuration o
// player.ScaleAmount al dar la orden (RN-CIV-005); ningún valor efectivo se
// guarda en una fila.
//
// Un jugador sin ciudad, o una ciudad con un código fuera del catálogo, tiene
// rasgos neutros. En producción no ocurre: la validación de carga exige que toda
// ciudad tenga una civilización del catálogo (ADR-035 §4). Ocurre en los tests
// de simulación que no la fijan, y por eso sus golden no cambian.

// SetCivilizations instala el catálogo de civilizaciones. Es inmutable mientras
// el proceso vive.
func (s *State) SetCivilizations(cs []player.Civilization) {
	s.civilizations = make(map[string]player.Civilization, len(cs))
	s.goodsOrder = make([]economy.Resource, 0, len(cs))
	for _, c := range cs {
		s.civilizations[c.Code] = c
		// El orden del catálogo, en que se cobran los bienes ajenos
		// (SPEC-CIVILIZATION-GOODS RN-GOODS-006).
		s.goodsOrder = append(s.goodsOrder, c.Good)
	}
}

// civilizationOf es la civilización de un jugador: la de su ciudad.
func (s *State) civilizationOf(playerID uuid.UUID) player.Civilization {
	c, ok := s.CityOf(playerID)
	if !ok {
		return player.Civilization{}
	}
	return s.civilizations[c.Civilization]
}

// msPerTile es lo que tarda un paso de terreno base para una unidad, con la
// velocidad de la civilización de su dueño (unit.move_speed).
func (s *State) msPerTile(u *unit.Unit, def unit.Definition) int64 {
	return player.ScaleDuration(def.BaseMsPerTile, s.trait(u.PlayerID, player.TraitMoveSpeed))
}

// productionMs es lo que tarda una obra o un entrenamiento de un jugador
// (production.speed).
func (s *State) productionMs(playerID uuid.UUID, baseMs int64) int64 {
	return player.ScaleDuration(baseMs, s.trait(playerID, player.TraitProductionSpeed))
}

// gatherWorkMs es lo que tarda el trabajo en el tile de un viaje de recolección
// (economy.gather_speed).
func (s *State) gatherWorkMs(u *unit.Unit, def unit.Definition) int64 {
	return player.ScaleDuration(def.GatherWorkMs, s.trait(u.PlayerID, player.TraitGatherSpeed))
}

// lootCarry es lo que carga como mucho un guerrero al saquear o al recoger
// botín (combat.loot_carry), medido al tomar cada golpe (RN-CIV-007).
func (s *State) lootCarry(u *unit.Unit, def unit.Definition) int32 {
	return player.ScaleAmount(def.LootCarry, s.trait(u.PlayerID, player.TraitLootCarry))
}

// attackDamage es lo que quita un golpe de una unidad que ataca, contra una
// unidad o contra un edificio (combat.attack_damage), medido al calcular el
// golpe (SPEC-TECHNOLOGIES §4.3). El disparo del castillo no lo lee.
func (s *State) attackDamage(u *unit.Unit, def unit.Definition) int32 {
	return player.ScaleAmount(def.AttackDamage, s.trait(u.PlayerID, player.TraitAttackDamage))
}

// gatherCarry es lo que carga un aldeano en un viaje de recolección
// (economy.gather_carry), medido al terminar el trabajo en el tile, cuando toma
// su carga (SPEC-TECHNOLOGIES §4.3).
func (s *State) gatherCarry(u *unit.Unit, def unit.Definition) int32 {
	return player.ScaleAmount(def.GatherCarry, s.trait(u.PlayerID, player.TraitGatherCarry))
}

// houseMaxHP es la vida entera de una casa de una ciudad: la de su tipo por el
// rasgo de la civilización del dueño (building.house_hp, RN-CIV-006).
func (s *State) houseMaxHP(ownerID uuid.UUID, baseHP int32) int32 {
	return player.ScaleAmount(baseHP, s.trait(ownerID, player.TraitHouseHP))
}
