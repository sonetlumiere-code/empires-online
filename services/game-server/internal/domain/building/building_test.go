package building

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/economy"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
)

var dueno = uuid.MustParse("11111111-1111-4111-8111-111111111111")

func ciudad() *city.City {
	return &city.City{ID: 7, OwnerPlayerID: dueno, CenterX: 10, CenterY: 10, Era: city.EraStone, PopulationLimit: 20}
}

func madera(n int64) *economy.Stock { return &economy.Stock{Resource: economy.ResourceWood, Amount: n} }

// SPEC-ECON-WOOD §4.5: la casa cuesta 30, tarda 15 s y suma 5.
func TestLaCasaTieneLosValoresDelCatalogo(t *testing.T) {
	def, err := Lookup(TypeHouse)
	require.NoError(t, err)
	assert.Equal(t, economy.Cost{Resource: economy.ResourceWood, Amount: 30}, def.Cost)
	assert.Equal(t, int64(15_000), def.BuildMs)
	assert.Equal(t, int32(5), def.PopulationBonus)

	_, err = Lookup("BARRACKS")
	assert.True(t, errors.Is(err, ErrUnknownType))
}

// RN-ECON-007: los ocho tiles del 3×3 que no son el centro, y ninguno más.
func TestSoloSeConstruyeEnLosOchoHuecosDeLaZonaUrbana(t *testing.T) {
	c := ciudad()
	huecos := 0
	for y := int32(7); y <= 13; y++ {
		for x := int32(7); x <= 13; x++ {
			err := CanPlace(c, world.Tile{X: x, Y: y}, nil)
			dentro := x >= 9 && x <= 11 && y >= 9 && y <= 11
			centro := x == 10 && y == 10
			if dentro && !centro {
				assert.NoError(t, err, "(%d,%d)", x, y)
				huecos++
				continue
			}
			assert.True(t, errors.Is(err, ErrInvalidSite), "(%d,%d): %v", x, y, err)
		}
	}
	assert.Equal(t, 8, huecos, "ocho huecos por ciudad bastan para el tope de STONE_AGE")
}

func TestUnTileConEdificioNoAdmiteOtro(t *testing.T) {
	ocupado := world.Tile{X: 9, Y: 9}
	err := CanPlace(ciudad(), ocupado, func(t world.Tile) bool { return t == ocupado })
	assert.True(t, errors.Is(err, ErrInvalidSite))
	assert.NoError(t, CanPlace(ciudad(), world.Tile{X: 11, Y: 9}, func(t world.Tile) bool { return t == ocupado }))
}

// RN-ECON-007: el orden de las respuestas. La propiedad va primero y una
// ciudad ajena se responde igual que una que no existe.
func TestCanBuildRespondeEnOrden(t *testing.T) {
	hueco := world.Tile{X: 9, Y: 10}

	_, err := CanBuild(nil, dueno, TypeHouse, hueco, nil, madera(100))
	assert.True(t, errors.Is(err, ErrCityNotFound))
	_, err = CanBuild(ciudad(), uuid.New(), TypeHouse, world.Tile{X: 0, Y: 0}, nil, madera(0))
	assert.True(t, errors.Is(err, ErrCityNotFound), "ciudad ajena: ni el sitio ni la madera se miran")

	_, err = CanBuild(ciudad(), dueno, "TOWER", hueco, nil, madera(100))
	assert.True(t, errors.Is(err, ErrUnknownType))

	_, err = CanBuild(ciudad(), dueno, TypeHouse, world.Tile{X: 10, Y: 10}, nil, madera(0))
	assert.True(t, errors.Is(err, ErrInvalidSite), "el sitio antes que la madera")

	_, err = CanBuild(ciudad(), dueno, TypeHouse, hueco, nil, madera(29))
	assert.True(t, errors.Is(err, economy.ErrInsufficientResources))

	def, err := CanBuild(ciudad(), dueno, TypeHouse, hueco, nil, madera(30))
	require.NoError(t, err)
	assert.Equal(t, TypeHouse, def.Type)
}

// RN-ECON-009 e INV-ECON-004: el límite es la era más cinco por casa completa;
// las que están en obras no cuentan.
func TestElLimiteDePoblacionSumaCincoPorCasaCompleta(t *testing.T) {
	assert.Equal(t, int32(20), PopulationLimit(20, nil))
	casas := []Building{
		{Type: TypeHouse, Status: StatusComplete},
		{Type: TypeHouse, Status: StatusUnderConstruction},
		{Type: TypeHouse, Status: StatusComplete},
	}
	assert.Equal(t, int32(30), PopulationLimit(20, casas))
	assert.Equal(t, int32(10), PopulationBonus(casas))
}

// Completar dos veces no suma dos veces: el segundo intento es un error.
func TestUnaCasaSoloSeCompletaUnaVez(t *testing.T) {
	b := Building{ID: 1, Type: TypeHouse, Status: StatusUnderConstruction, CompletesAtMs: 1000}
	assert.False(t, b.Due(999))
	assert.True(t, b.Due(1000))
	require.NoError(t, b.Complete())
	assert.True(t, b.IsComplete())
	assert.False(t, b.Due(5000), "una casa completa no vuelve a vencer")
	assert.True(t, errors.Is(b.Complete(), ErrAlreadyDone))
}

func TestValidateRechazaFilasFueraDelCatalogo(t *testing.T) {
	assert.NoError(t, (&Building{Type: TypeHouse, Status: StatusComplete}).Validate())
	assert.Error(t, (&Building{Type: "CASTLE", Status: StatusComplete}).Validate())
	assert.Error(t, (&Building{Type: TypeHouse, Status: "RUINED"}).Validate())
}
