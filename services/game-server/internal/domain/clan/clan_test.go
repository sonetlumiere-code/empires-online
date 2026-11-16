package clan_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/clan"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/player"
)

// SPEC-CLANS §4.1: el nombre, tal como llega.
func TestElNombreDelClan(t *testing.T) {
	for _, ok := range []string{"Los Lobos", "Ñandú-7", "abc", "xyz", "Clan de los Veinticuatro", "A-B C"} {
		assert.NoError(t, clan.ValidateName(ok), ok)
	}
	for _, mal := range []string{
		"", "ab", " Los Lobos", "Los Lobos ", "Los  Lobos", "Los_Lobos", "Lobos!", "Clan de los Veinticinco_", "Clan de los Veinticincoxx",
		"Lobos\n", "Lobos\t1", string([]byte{0xff, 0xfe, 0x41}),
	} {
		assert.ErrorIs(t, clan.ValidateName(mal), clan.ErrInvalidName, "%q", mal)
	}
	// 24 caracteres de dos bytes caben: se cuentan caracteres, no bytes.
	assert.NoError(t, clan.ValidateName("ññññññññññññññññññññññññ"))
	assert.ErrorIs(t, clan.ValidateName("ñññññññññññññññññññññññññ"), clan.ErrInvalidName)
}

// SPEC-CLANS §4.1: la sigla, de 2 a 4 caracteres de A–Z y 0–9.
func TestLaSiglaDelClan(t *testing.T) {
	for _, ok := range []string{"LB", "LOB", "LOB7", "99"} {
		assert.NoError(t, clan.ValidateTag(ok), ok)
	}
	for _, mal := range []string{"", "L", "LOBOS", "lob", "LÓB", "LO-B", "LO B"} {
		assert.ErrorIs(t, clan.ValidateTag(mal), clan.ErrInvalidTag, "%q", mal)
	}
	assert.Equal(t, clan.NameKey("los lobos"), clan.NameKey("LOS Lobos"))
}

// RN-CLAN-007: el liderazgo pasa al más antiguo y, a igualdad, al de menor id.
func TestElLiderazgoPasaAlMasAntiguo(t *testing.T) {
	a := uuid.MustParse("00000000-0000-4000-8000-00000000000a")
	b := uuid.MustParse("00000000-0000-4000-8000-00000000000b")
	c := uuid.MustParse("00000000-0000-4000-8000-00000000000c")
	got, ok := clan.NextLeader([]clan.Member{{PlayerID: c, JoinedAtMs: 30}, {PlayerID: b, JoinedAtMs: 20}, {PlayerID: a, JoinedAtMs: 40}})
	assert.True(t, ok)
	assert.Equal(t, b, got)
	got, _ = clan.NextLeader([]clan.Member{{PlayerID: c, JoinedAtMs: 20}, {PlayerID: b, JoinedAtMs: 20}})
	assert.Equal(t, b, got, "a igualdad, el menor id")
	_, ok = clan.NextLeader(nil)
	assert.False(t, ok, "sin nadie, el clan se disuelve")
}

// RN-CLANGOV-004: con oficiales, hereda el oficial más antiguo aunque haya
// miembros rasos más antiguos; la antigüedad es la de entrada en el clan.
func TestElLiderazgoPasaAlOficialMasAntiguo(t *testing.T) {
	a := uuid.MustParse("00000000-0000-4000-8000-00000000000a")
	b := uuid.MustParse("00000000-0000-4000-8000-00000000000b")
	c := uuid.MustParse("00000000-0000-4000-8000-00000000000c")
	got, ok := clan.NextLeader([]clan.Member{
		{PlayerID: a, JoinedAtMs: 10}, {PlayerID: b, JoinedAtMs: 30, Officer: true}, {PlayerID: c, JoinedAtMs: 20, Officer: true},
	})
	require.True(t, ok)
	assert.Equal(t, c, got, "el oficial que entró antes, no el miembro más antiguo")
	got, _ = clan.NextLeader([]clan.Member{{PlayerID: c, JoinedAtMs: 20, Officer: true}, {PlayerID: b, JoinedAtMs: 20, Officer: true}})
	assert.Equal(t, b, got, "a igualdad, el menor id")
}

// RN-CLANGOV-005: el sucesor de un líder ausente es el oficial activo más
// antiguo, si no el miembro activo más antiguo; sin nadie activo, nadie.
func TestElSucesorDeUnLiderAusente(t *testing.T) {
	a := uuid.MustParse("00000000-0000-4000-8000-00000000000a")
	b := uuid.MustParse("00000000-0000-4000-8000-00000000000b")
	c := uuid.MustParse("00000000-0000-4000-8000-00000000000c")
	d := uuid.MustParse("00000000-0000-4000-8000-00000000000d")
	otros := []clan.Member{
		{PlayerID: a, JoinedAtMs: 10, Officer: true}, {PlayerID: b, JoinedAtMs: 20, Officer: true},
		{PlayerID: c, JoinedAtMs: 5}, {PlayerID: d, JoinedAtMs: 30},
	}
	activos := func(ids ...uuid.UUID) func(uuid.UUID) bool {
		return func(p uuid.UUID) bool {
			for _, id := range ids {
				if id == p {
					return true
				}
			}
			return false
		}
	}
	got, ok := clan.Successor(otros, activos(a, b, c, d))
	require.True(t, ok)
	assert.Equal(t, a, got)
	got, _ = clan.Successor(otros, activos(b, c, d))
	assert.Equal(t, b, got, "salta al oficial ausente")
	got, _ = clan.Successor(otros, activos(c, d))
	assert.Equal(t, c, got, "sin oficiales activos, el miembro activo más antiguo")
	_, ok = clan.Successor(otros, activos())
	assert.False(t, ok, "sin nadie activo, no cambia")
}

// RN-CLANGOV-005: ausente es no estar ONLINE con la marca de desconexión de
// hace el plazo o más; sin marca no se está ausente.
func TestLaAusenciaDelLider(t *testing.T) {
	const dia = int64(24 * 60 * 60 * 1000)
	plazo := 7 * 24 * time.Hour
	ahora := 100 * dia
	assert.False(t, clan.Absent(true, ahora-30*dia, ahora, plazo), "ONLINE nunca está ausente")
	assert.False(t, clan.Absent(false, ahora-7*dia+1, ahora, plazo), "justo antes del plazo")
	assert.True(t, clan.Absent(false, ahora-7*dia, ahora, plazo), "justo en el plazo")
	assert.False(t, clan.Absent(false, 0, ahora, plazo), "sin marca")
	assert.Equal(t, plazo, clan.DefaultLeaderInactive)
}

// RN-CLANGOV-002 y RN-CLANGOV-003: la tabla de permisos de §4.2.
func TestLosPermisosDeCadaRango(t *testing.T) {
	L, O, M := clan.RoleLeader, clan.RoleOfficer, clan.RoleMember
	assert.True(t, clan.CanInvite(L))
	assert.True(t, clan.CanInvite(O))
	assert.False(t, clan.CanInvite(M))
	casos := []struct {
		by, target string
		puede      bool
	}{
		{L, O, true}, {L, M, true}, {L, L, false},
		{O, M, true}, {O, O, false}, {O, L, false},
		{M, M, false}, {M, O, false}, {M, L, false},
	}
	for _, c := range casos {
		assert.Equal(t, c.puede, clan.CanExpel(c.by, c.target), "%s expulsa a %s", c.by, c.target)
	}
	assert.True(t, clan.CanGovern(L))
	assert.False(t, clan.CanGovern(O))
	assert.False(t, clan.CanGovern(M))
}

// RN-CLAN-006: un clan no junta a la Orden y al Caos, contando las entradas.
func TestUnClanNoJuntaALaOrdenYAlCaos(t *testing.T) {
	neutral := clan.Stance{Faction: player.FactionNeutral}
	orden := clan.Stance{Faction: player.FactionOrder}
	caos := clan.Stance{Faction: player.FactionChaos}
	haciaCaos := clan.Stance{Faction: player.FactionNeutral, Joining: player.FactionChaos}
	haciaOrden := clan.Stance{Faction: "", Joining: player.FactionOrder}

	assert.False(t, clan.Mixes(neutral, orden, neutral))
	assert.False(t, clan.Mixes(caos, caos, haciaCaos))
	assert.True(t, clan.Mixes(orden, caos))
	assert.True(t, clan.Mixes(orden, haciaCaos), "la entrada pendiente cuenta")
	assert.True(t, clan.Mixes(haciaOrden, haciaCaos))
	assert.False(t, clan.Mixes())

	assert.Equal(t, player.FactionChaos, clan.OpposedSide(neutral, orden))
	assert.Equal(t, player.FactionOrder, clan.OpposedSide(haciaCaos))
	assert.Empty(t, clan.OpposedSide(neutral))
}

// RN-CLAN-005: la paz residual alcanza a quien era compañero al salir, durante
// el plazo, y a nadie más.
func TestLaPazResidual(t *testing.T) {
	x := uuid.MustParse("00000000-0000-4000-8000-000000000001")
	y := uuid.MustParse("00000000-0000-4000-8000-000000000002")
	z := uuid.MustParse("00000000-0000-4000-8000-000000000003")
	grace := 15 * time.Minute
	g := grace.Milliseconds()

	// x salió del clan 1 en t=1000; y sigue desde t=500; z entró en t=2000.
	stays := []clan.Stay{
		{ClanID: 1, PlayerID: x, JoinedAtMs: 100, LeftAtMs: 1000},
		{ClanID: 1, PlayerID: y, JoinedAtMs: 500},
		{ClanID: 1, PlayerID: z, JoinedAtMs: 2000},
	}
	assert.True(t, clan.ResidualPeace(x, y, stays, 1000, grace))
	assert.True(t, clan.ResidualPeace(y, x, stays, 1000+g-1, grace), "simétrica, hasta el último ms")
	assert.False(t, clan.ResidualPeace(x, y, stays, 1000+g, grace), "vence al cumplirse el plazo")
	assert.False(t, clan.ResidualPeace(x, z, stays, 2500, grace), "no alcanza a quien entró después")

	// Los dos salieron: x en 1000 e y en 1200. Cuenta la salida de x, con y
	// todavía dentro; la de y no, porque x ya no estaba: la paz dura hasta la
	// salida de x más el plazo.
	ambos := []clan.Stay{
		{ClanID: 1, PlayerID: x, JoinedAtMs: 100, LeftAtMs: 1000},
		{ClanID: 1, PlayerID: y, JoinedAtMs: 500, LeftAtMs: 1200},
	}
	assert.True(t, clan.ResidualPeace(x, y, ambos, 1000+g-1, grace))
	assert.False(t, clan.ResidualPeace(x, y, ambos, 1000+g, grace))

	// Otro clan no cuenta.
	otro := []clan.Stay{
		{ClanID: 1, PlayerID: x, JoinedAtMs: 100, LeftAtMs: 1000},
		{ClanID: 2, PlayerID: y, JoinedAtMs: 500},
	}
	assert.False(t, clan.ResidualPeace(x, y, otro, 1000, grace))
}
