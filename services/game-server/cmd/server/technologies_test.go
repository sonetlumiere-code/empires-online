package main

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/economy"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/player"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/technology"
)

// INV-TECH-001, REPAIR al arrancar: researchProblem dice por qué una
// investigación leída no se puede aplicar, o "" si se puede.
func TestLaCargaDescartaLasInvestigacionesMalFormadas(t *testing.T) {
	costes := economy.Costs{{Resource: economy.ResourceWood, Amount: 1}}
	cat, err := technology.NewCatalog([]technology.Technology{
		{Code: "STONE_TOOLS", Name: "Herramientas de piedra", Era: city.EraStone, Costs: costes, DurationMs: 1,
			Effects: map[string]int32{player.TraitGatherSpeed: 1100}},
		{Code: "BRONZE_WEAPONS", Name: "Armas de bronce", Era: city.EraBronze, Costs: costes, DurationMs: 1,
			Effects: map[string]int32{player.TraitAttackDamage: 1100}},
		{Code: "IRON_WEAPONS", Name: "Armas de hierro", Era: city.EraCastle, Costs: costes, DurationMs: 1,
			Requires: []string{"BRONZE_WEAPONS"}, Effects: map[string]int32{player.TraitAttackDamage: 1100}},
		{Code: "RAIDING", Name: "Incursiones", Era: city.EraIron, Civilization: "NORSE", Costs: costes, OwnGood: 1,
			DurationMs: 1, Effects: map[string]int32{player.TraitLootCarry: 1200}},
	}, []string{"ROMAN", "NORSE"})
	require.NoError(t, err)
	dueno := uuid.New()
	romana := func(era city.Era) *city.City {
		return &city.City{ID: 1, OwnerPlayerID: dueno, Era: era, Civilization: "ROMAN"}
	}
	inv := func(code string) technology.Research { return technology.Research{CityID: 1, Technology: code} }
	tenidas := func(codes ...string) map[uuid.UUID][]string { return map[uuid.UUID][]string{dueno: codes} }

	assert.Empty(t, researchProblem(inv("STONE_TOOLS"), romana(city.EraStone), false, cat, nil))
	assert.Equal(t, "la ciudad no existe", researchProblem(inv("STONE_TOOLS"), nil, false, cat, nil))
	assert.Equal(t, "la ciudad avanza de era", researchProblem(inv("STONE_TOOLS"), romana(city.EraStone), true, cat, nil))
	assert.Equal(t, "la tecnología no está en el catálogo", researchProblem(inv("POLVORA"), romana(city.EraCastle), false, cat, nil))
	assert.Equal(t, "es exclusiva de otra civilización", researchProblem(inv("RAIDING"), romana(city.EraCastle), false, cat, nil))
	assert.Equal(t, "pide una era posterior a la de la ciudad", researchProblem(inv("BRONZE_WEAPONS"), romana(city.EraStone), false, cat, nil))
	assert.Equal(t, "el dueño ya la tiene", researchProblem(inv("STONE_TOOLS"), romana(city.EraStone), false, cat, tenidas("STONE_TOOLS")))
	assert.Equal(t, "al dueño le falta BRONZE_WEAPONS", researchProblem(inv("IRON_WEAPONS"), romana(city.EraCastle), false, cat, nil))
	assert.Empty(t, researchProblem(inv("IRON_WEAPONS"), romana(city.EraCastle), false, cat, tenidas("BRONZE_WEAPONS")))
}
