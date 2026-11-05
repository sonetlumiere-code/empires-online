package territory_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/territory"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
)

var (
	reclamante = uuid.MustParse("00000000-0000-4000-8000-000000000001")
	dueño      = uuid.MustParse("00000000-0000-4000-8000-000000000002")
	llanura    = territory.Territory{ID: 7, Name: "Llanura", MinX: 64, MinY: 0, MaxX: 127, MaxY: 63}
)

func controlDe(p uuid.UUID) territory.Control {
	id := p.String()
	at := time.Unix(1_800_000_000, 0).UTC()
	return territory.Control{TerritoryID: llanura.ID, OwnerType: territory.OwnerPlayer, OwnerID: &id, CapturedAt: &at}
}

// RN-CONQ-002 e INV-TERR-011: se reclama uno libre o uno ajeno que no es la
// capital de su dueño; no el propio, ni la capital, ni uno de un clan.
func TestQueTerritorioSePuedeReclamar(t *testing.T) {
	libre := territory.Control{TerritoryID: llanura.ID, OwnerType: territory.OwnerNone}
	fuera := world.Tile{X: 10, Y: 10}
	dentro := world.Tile{X: 70, Y: 5}
	clan := "clan-1"

	casos := []struct {
		nombre  string
		control territory.Control
		centros []world.Tile
		err     error
	}{
		{"libre", libre, nil, nil},
		{"ajeno, con la ciudad del dueño fuera", controlDe(dueño), []world.Tile{fuera}, nil},
		{"ajeno y capital de su dueño", controlDe(dueño), []world.Tile{dentro}, territory.ErrCapital},
		{"ajeno, la capital en el borde inclusivo", controlDe(dueño), []world.Tile{{X: 127, Y: 63}}, territory.ErrCapital},
		{"propio", controlDe(reclamante), []world.Tile{fuera}, territory.ErrOwnTerritory},
		{"de un clan", territory.Control{TerritoryID: llanura.ID, OwnerType: territory.OwnerClan, OwnerID: &clan}, nil, territory.ErrNotClaimable},
	}
	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			assert.ErrorIs(t, territory.CheckClaimable(llanura, c.control, reclamante, c.centros), c.err)
			if c.err == nil {
				assert.NoError(t, territory.CheckClaimable(llanura, c.control, reclamante, c.centros))
			}
		})
	}
}

// SPEC-TERRITORY-CONQUEST §4.2 e INV-ECON-004: cuenta lo conquistado, nunca la
// capital, y cada territorio suma 5.
func TestElBonoCuentaLoConquistadoSinLaCapital(t *testing.T) {
	capital := territory.Territory{ID: 1, MinX: 0, MinY: 0, MaxX: 63, MaxY: 63}
	otro := territory.Territory{ID: 2, MinX: 64, MinY: 0, MaxX: 127, MaxY: 63}
	ajeno := territory.Territory{ID: 3, MinX: 128, MinY: 0, MaxX: 191, MaxY: 63}
	libre := territory.Territory{ID: 4, MinX: 192, MinY: 0, MaxX: 255, MaxY: 63}
	mio := func(id int64) territory.Control {
		c := controlDe(reclamante)
		c.TerritoryID = id
		return c
	}
	suyo := controlDe(dueño)
	suyo.TerritoryID = 3
	controls := map[int64]territory.Control{
		1: mio(1), 2: mio(2), 3: suyo, 4: {TerritoryID: 4, OwnerType: territory.OwnerNone},
	}
	centro := world.Tile{X: 30, Y: 30}
	ts := []territory.Territory{capital, otro, ajeno, libre}

	assert.Equal(t, 1, territory.CountConquered(ts, controls, reclamante, centro))
	assert.Equal(t, int32(5), territory.ConqueredBonus(1))
	assert.Equal(t, int32(0), territory.ConqueredBonus(0))
	assert.Equal(t, int32(5), territory.CaptureBonus(otro, centro), "conquistar fuera de casa suma")
	assert.Equal(t, int32(0), territory.CaptureBonus(capital, centro), "la propia capital no suma")
}

// ADR-034 §1: el plazo vence en su instante exacto.
func TestUnaReclamacionVenceEnSuInstante(t *testing.T) {
	c := territory.Claim{StartedAtMs: 1_000, CompletesAtMs: 301_000}
	assert.False(t, c.Due(300_999))
	assert.True(t, c.Due(301_000))
}
