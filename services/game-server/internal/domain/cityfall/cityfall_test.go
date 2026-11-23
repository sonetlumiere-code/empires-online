package cityfall_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/cityfall"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
)

var (
	dueno     = uuid.MustParse("00000000-0000-4000-8000-000000000001")
	asaltante = uuid.MustParse("00000000-0000-4000-8000-000000000002")
)

const fundada = int64(1_800_000_000_000)

// RN-SIEGE-010 y RN-CITY-029, cada rechazo y su orden.
func TestQueCiudadSePuedeAsaltar(t *testing.T) {
	inmunidad := 72 * time.Hour
	despues := fundada + inmunidad.Milliseconds()
	ciudad := func(era city.Era) *city.City {
		return &city.City{ID: 7, OwnerPlayerID: dueno, Era: era, FoundedAtMs: fundada}
	}
	casos := []struct {
		nombre string
		t      cityfall.Target
		quien  uuid.UUID
		ahora  int64
		err    error
	}{
		{"en brecha, de Bronce y pasada la inmunidad", cityfall.Target{City: ciudad(city.EraBronze)}, asaltante, despues, nil},
		{"la propia", cityfall.Target{City: ciudad(city.EraBronze)}, dueno, despues, cityfall.ErrOwnCity},
		{"amurallada", cityfall.Target{City: ciudad(city.EraIron), Walled: true}, asaltante, despues, cityfall.ErrWalled},
		{"de la Edad de Piedra", cityfall.Target{City: ciudad(city.EraStone)}, asaltante, despues, cityfall.ErrStoneAge},
		{"recién fundada", cityfall.Target{City: ciudad(city.EraBronze)}, asaltante, despues - 1, cityfall.ErrImmune},
		{"con otro asalto", cityfall.Target{City: ciudad(city.EraCastle), Busy: true}, asaltante, despues, cityfall.ErrBusy},
		{"la muralla va antes que la era", cityfall.Target{City: ciudad(city.EraStone), Walled: true}, asaltante, despues, cityfall.ErrWalled},
	}
	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			assert.ErrorIs(t, cityfall.CheckTarget(c.t, c.quien, c.ahora, inmunidad), c.err)
			if c.err == nil {
				assert.NoError(t, cityfall.CheckTarget(c.t, c.quien, c.ahora, inmunidad))
			}
		})
	}
	// Sin inmunidad configurada (los tests del humo), una recién fundada se asalta.
	assert.NoError(t, cityfall.CheckTarget(cityfall.Target{City: ciudad(city.EraBronze)}, asaltante, fundada, 0))
}

func TestElPlazoDelAsaltoYLaDistanciaDeLaRefundacion(t *testing.T) {
	a := cityfall.Assault{StartedAtMs: 1_000, EndsAtMs: 901_000, X: 3, Y: 4}
	assert.False(t, a.Due(900_999))
	assert.True(t, a.Due(901_000))
	assert.Equal(t, world.Tile{X: 3, Y: 4}, a.Tile())

	evitar := []world.Tile{{X: 100, Y: 100}, {X: 300, Y: 100}}
	assert.True(t, cityfall.FarFromAll(world.Tile{X: 200, Y: 100}, evitar, 64))
	assert.False(t, cityfall.FarFromAll(world.Tile{X: 163, Y: 100}, evitar, 64), "a 63 de la primera")
	assert.True(t, cityfall.FarFromAll(world.Tile{X: 164, Y: 40}, evitar, 64), "a 64 justos")
	assert.True(t, cityfall.FarFromAll(world.Tile{X: 0, Y: 0}, nil, 64))
}
