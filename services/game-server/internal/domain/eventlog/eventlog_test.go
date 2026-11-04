package eventlog_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/eventlog"
)

var (
	yo      = uuid.MustParse("00000000-0000-4000-8000-000000000001")
	otro    = uuid.MustParse("00000000-0000-4000-8000-000000000002")
	tercero = uuid.MustParse("00000000-0000-4000-8000-000000000003")
)

const t0 = int64(1_800_000_000_000)

// Cada fila de SPEC-EVENT-LOG §4.1, vista desde los dos lados, y las que no
// entran: lo que el jugador hizo él mismo en la diplomacia y las filas sin los
// datos para saberlo.
func TestClasificaCadaFilaParaQuienLaLee(t *testing.T) {
	tratado := func(tipo string, owner, other, by uuid.UUID, from string) eventlog.Event {
		return eventlog.Event{Type: tipo, PlayerID: owner, Counterpart: other, By: by, From: from, TreatyType: "NON_AGGRESSION"}
	}
	casos := []struct {
		nombre string
		viewer uuid.UUID
		e      eventlog.Event
		kind   eventlog.Kind
		ok     bool
	}{
		{"muere una unidad mía", yo, eventlog.Event{Type: "UnitKilled", PlayerID: yo, Counterpart: otro}, eventlog.KindUnitLost, true},
		{"las mías matan una ajena", yo, eventlog.Event{Type: "UnitKilled", PlayerID: otro, Counterpart: yo}, eventlog.KindEnemyKilled, true},
		{"me saquean", yo, eventlog.Event{Type: "CityPillaged", PlayerID: yo, Counterpart: otro}, eventlog.KindPillaged, true},
		{"saqueo yo", yo, eventlog.Event{Type: "CityPillaged", PlayerID: otro, Counterpart: yo}, eventlog.KindPillageDone, true},
		{"una muerte sin autor", yo, eventlog.Event{Type: "UnitKilled", PlayerID: yo}, "", false},
		{"un ataque sin consecuencia no entra (Q1)", yo, eventlog.Event{Type: "UnitAttacked", PlayerID: yo, Counterpart: otro}, "", false},

		{"me ofrecen", yo, tratado("TreatyProposed", otro, yo, otro, ""), eventlog.KindTreatyOffered, true},
		{"mi propia oferta no", yo, tratado("TreatyProposed", yo, otro, yo, ""), "", false},
		{"me la retiran", yo, tratado("TreatyWithdrawn", otro, yo, otro, "PROPOSED"), eventlog.KindTreatyWithdrawn, true},
		{"aceptan la mía", yo, tratado("TreatyAccepted", yo, otro, otro, "PROPOSED"), eventlog.KindTreatyAccepted, true},
		{"mi propia aceptación no", yo, tratado("TreatyAccepted", otro, yo, yo, "PROPOSED"), "", false},
		{"rechazan la mía", yo, tratado("TreatyRejected", yo, otro, otro, "PROPOSED"), eventlog.KindTreatyRejected, true},
		{"rechazan la mía, fila antigua sin autor", yo, tratado("TreatyRejected", yo, otro, uuid.Nil, ""), eventlog.KindTreatyRejected, true},
		{"rompe el otro", yo, tratado("TreatyBreakAnnounced", yo, otro, otro, "ACTIVE"), eventlog.KindTreatyBreakAnnounced, true},
		{"rompo yo", yo, tratado("TreatyBreakAnnounced", yo, otro, yo, "ACTIVE"), "", false},
		{"ruptura antigua sin autor", yo, tratado("TreatyBreakAnnounced", yo, otro, uuid.Nil, "ACTIVE"), "", false},
		{"caduca una oferta", yo, tratado("TreatyExpired", otro, yo, uuid.Nil, "PROPOSED"), eventlog.KindTreatyOfferExpired, true},
		{"caduca un tratado administrativo", yo, tratado("TreatyExpired", yo, otro, uuid.Nil, "ACTIVE"), eventlog.KindTreatyEnded, true},
		{"caducidad antigua sin estado anterior", yo, tratado("TreatyExpired", yo, otro, uuid.Nil, ""), "", false},
		{"termina el preaviso, también para quien rompió", yo, tratado("TreatyBroken", otro, yo, uuid.Nil, "ACTIVE"), eventlog.KindTreatyEnded, true},
	}
	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			kind, other, ok := eventlog.Classify(c.viewer, c.e)
			require.Equal(t, c.ok, ok)
			assert.Equal(t, c.kind, kind)
			if ok {
				assert.Equal(t, otro, other, "el otro es siempre el que no lee")
			}
		})
	}
}

// INV-SEC-009: un tercero no recibe nada de un suceso entre otros dos, sea cual
// sea su tipo.
func TestUnTerceroNoVeLosSucesosDeOtros(t *testing.T) {
	for _, tipo := range eventlog.EventTypes() {
		e := eventlog.Event{Type: tipo, PlayerID: yo, Counterpart: otro, From: "ACTIVE"}
		_, _, ok := eventlog.Classify(tercero, e)
		assert.False(t, ok, tipo)
	}
	_, _, ok := eventlog.Classify(uuid.Nil, eventlog.Event{Type: "UnitKilled", PlayerID: yo, Counterpart: otro})
	assert.False(t, ok, "sin jugador no hay registro")
}

func golpe(id int64, at int64, atacante uuid.UUID) eventlog.Event {
	return eventlog.Event{ID: id, Type: "CityPillaged", PlayerID: yo, Counterpart: atacante, AtMs: at, Amount: 5, X: 451, Y: 179, HasTile: true}
}

// Q4: los golpes del mismo saqueo son una entrada con la suma; un hueco de más
// de diez minutos, u otro atacante, abren otra.
func TestAgrupaLosGolpesDeUnMismoSaqueo(t *testing.T) {
	b := eventlog.NewBuilder(yo, 0, 10*time.Minute, 50)
	id := int64(100)
	// Del más nuevo al más viejo: 17 golpes cada 30 s de otro, uno de un tercero
	// en medio, y otro golpe de otro 11 minutos antes del primero.
	for i := 0; i < 17; i++ {
		require.True(t, b.Add(golpe(id, t0-int64(i)*30_000, otro)))
		id--
		if i == 8 {
			require.True(t, b.Add(golpe(id, t0-int64(i)*30_000, tercero)))
			id--
		}
	}
	primero := t0 - 16*30_000
	require.True(t, b.Add(golpe(id, primero-11*60_000, otro)))

	entradas := b.Entries()
	require.Len(t, entradas, 3)
	saqueo := entradas[0]
	assert.Equal(t, eventlog.KindPillaged, saqueo.Kind)
	assert.Equal(t, int64(100), saqueo.ID, "el id del grupo es el de su evento más nuevo")
	assert.Equal(t, 17, saqueo.Count)
	assert.Equal(t, int64(85), saqueo.Amount)
	assert.Equal(t, primero, saqueo.FirstAtMs)
	assert.Equal(t, t0, saqueo.LastAtMs)
	assert.Equal(t, tercero, entradas[1].Other)
	assert.Equal(t, 1, entradas[2].Count, "once minutos de hueco parten el grupo")
	assert.Equal(t, id, b.MinID())
}

// Las muertes se agrupan por tipo de unidad: «2 aldeanos» y «1 guerrero» son dos
// entradas, y lo mismo un pacto y una alianza.
func TestLasMuertesSeAgrupanPorTipoDeUnidad(t *testing.T) {
	b := eventlog.NewBuilder(yo, 0, 10*time.Minute, 50)
	muerte := func(id int64, tipo string) eventlog.Event {
		return eventlog.Event{ID: id, Type: "UnitKilled", PlayerID: yo, Counterpart: otro, AtMs: t0 - id, UnitType: tipo}
	}
	require.True(t, b.Add(muerte(30, "VILLAGER")))
	require.True(t, b.Add(muerte(29, "WARRIOR")))
	require.True(t, b.Add(muerte(28, "VILLAGER")))
	entradas := b.Entries()
	require.Len(t, entradas, 2)
	assert.Equal(t, "VILLAGER", entradas[0].UnitType)
	assert.Equal(t, 2, entradas[0].Count)
	assert.Equal(t, "WARRIOR", entradas[1].UnitType)
}

// Un grupo nunca mezcla lo visto con lo nuevo: el cursor lo parte.
func TestElCursorParteUnGrupo(t *testing.T) {
	b := eventlog.NewBuilder(yo, 50, 10*time.Minute, 50)
	for id := int64(53); id >= 48; id-- {
		require.True(t, b.Add(golpe(id, t0-(53-id)*1_000, otro)))
	}
	entradas := b.Entries()
	require.Len(t, entradas, 2)
	assert.Equal(t, int64(53), entradas[0].ID)
	assert.Equal(t, 3, entradas[0].Count, "51, 52 y 53 son nuevos")
	assert.Equal(t, int64(50), entradas[1].ID)
	assert.Equal(t, 3, entradas[1].Count, "48, 49 y 50 ya se vieron")
}

// ADR-028 §5: la página se corta antes de la fila que abriría la entrada de
// más; la que se suma a un grupo abierto entra aunque la página esté llena, y
// MinID deja la siguiente página justo en la fila que no entró.
func TestLaPaginaSeCortaAntesDeLaEntradaDeMas(t *testing.T) {
	b := eventlog.NewBuilder(yo, 0, 10*time.Minute, 2)
	require.True(t, b.Add(golpe(10, t0, otro)))
	require.True(t, b.Add(golpe(9, t0-1_000, tercero)))
	require.True(t, b.Add(golpe(8, t0-2_000, otro)), "se suma al grupo abierto")
	require.True(t, b.Add(eventlog.Event{ID: 7, Type: "UnitAttacked", PlayerID: yo, Counterpart: otro}),
		"una fila que no da entrada no llena la página")
	require.False(t, b.Add(eventlog.Event{ID: 6, Type: "UnitKilled", PlayerID: yo, Counterpart: otro, AtMs: t0 - 3_000}),
		"la tercera entrada no cabe")
	assert.Len(t, b.Entries(), 2)
	assert.Equal(t, int64(7), b.MinID(), "la siguiente página empieza en la 6")
}

// Una oferta caducada dice si era propia, y la propia y la recibida no se juntan.
func TestUnaOfertaCaducadaDiceSiEraPropia(t *testing.T) {
	b := eventlog.NewBuilder(yo, 0, 10*time.Minute, 50)
	require.True(t, b.Add(eventlog.Event{ID: 5, Type: "TreatyExpired", PlayerID: yo, Counterpart: otro, From: "PROPOSED", TreatyType: "ALLIANCE", AtMs: t0}))
	require.True(t, b.Add(eventlog.Event{ID: 4, Type: "TreatyExpired", PlayerID: otro, Counterpart: yo, From: "PROPOSED", TreatyType: "ALLIANCE", AtMs: t0 - 1}))
	entradas := b.Entries()
	require.Len(t, entradas, 2)
	assert.True(t, entradas[0].ProposedByMe)
	assert.False(t, entradas[1].ProposedByMe)
	assert.Equal(t, "ALLIANCE", entradas[0].TreatyType)
}

func TestLosTiposDeEntradaSonUnCatalogoCerrado(t *testing.T) {
	vistos := map[eventlog.Kind]bool{}
	for _, k := range eventlog.Kinds() {
		require.False(t, vistos[k], "repetido: %s", k)
		vistos[k] = true
	}
	assert.Len(t, vistos, 11)
}
