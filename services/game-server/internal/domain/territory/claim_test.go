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
// capital de su dueño; no el propio ni la capital. Desde E19, uno de un clan
// ajeno sí, y uno del propio clan no (RN-CLANTERR-001); un owner_id de clan que
// no es un id no se puede reclamar.
func TestQueTerritorioSePuedeReclamar(t *testing.T) {
	libre := territory.Control{TerritoryID: llanura.ID, OwnerType: territory.OwnerNone}
	fuera := world.Tile{X: 10, Y: 10}
	dentro := world.Tile{X: 70, Y: 5}
	clan := "clan-1"
	deClan := func(id string) territory.Control {
		return territory.Control{TerritoryID: llanura.ID, OwnerType: territory.OwnerClan, OwnerID: &id}
	}
	const miClan = int64(3)

	casos := []struct {
		nombre  string
		control territory.Control
		centros []world.Tile
		clan    int64
		err     error
	}{
		{"libre", libre, nil, 0, nil},
		{"ajeno, con la ciudad del dueño fuera", controlDe(dueño), []world.Tile{fuera}, 0, nil},
		{"ajeno y capital de su dueño", controlDe(dueño), []world.Tile{dentro}, 0, territory.ErrCapital},
		{"ajeno, la capital en el borde inclusivo", controlDe(dueño), []world.Tile{{X: 127, Y: 63}}, 0, territory.ErrCapital},
		{"propio", controlDe(reclamante), []world.Tile{fuera}, 0, territory.ErrOwnTerritory},
		{"propio, reclamando para el clan", controlDe(reclamante), []world.Tile{fuera}, miClan, territory.ErrOwnTerritory},
		{"de un clan ajeno", deClan("7"), []world.Tile{dentro}, miClan, nil},
		{"de un clan, sin clan", deClan("7"), nil, 0, nil},
		{"del propio clan", deClan("3"), nil, miClan, territory.ErrClanTerritory},
		{"de un clan con un owner_id que no es un id", territory.Control{TerritoryID: llanura.ID, OwnerType: territory.OwnerClan, OwnerID: &clan}, nil, 0, territory.ErrNotClaimable},
		{"de una facción", territory.Control{TerritoryID: llanura.ID, OwnerType: territory.OwnerFaction, OwnerID: &clan}, nil, 0, territory.ErrNotClaimable},
	}
	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			err := territory.CheckClaimable(llanura, c.control, reclamante, c.clan, c.centros)
			if c.err == nil {
				assert.NoError(t, err)
				return
			}
			assert.ErrorIs(t, err, c.err)
		})
	}
}

// ADR-041 §1: el owner_id de un dueño CLAN es el id del clan en decimal.
func TestElDueñoDeClan(t *testing.T) {
	id := territory.ClanOwnerID(42)
	c := territory.Control{TerritoryID: llanura.ID, OwnerType: territory.OwnerClan, OwnerID: &id}
	got, ok := c.ClanOwner()
	assert.True(t, ok)
	assert.Equal(t, int64(42), got)
	_, ok = c.PlayerOwner()
	assert.False(t, ok, "un clan no es un jugador")
	_, ok = controlDe(dueño).ClanOwner()
	assert.False(t, ok, "un jugador no es un clan")
	malo := "0"
	_, ok = territory.Control{OwnerType: territory.OwnerClan, OwnerID: &malo}.ClanOwner()
	assert.False(t, ok)
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
