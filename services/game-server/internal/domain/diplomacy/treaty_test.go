package diplomacy

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// parOrdenado devuelve dos identificadores ya en orden canónico, para que los
// tests no dependan de qué UUID salió mayor.
func parOrdenado() (uuid.UUID, uuid.UUID) {
	return CanonicalPair(uuid.New(), uuid.New())
}

func tratado(status Status, allowsGarrison bool) Treaty {
	a, b := parOrdenado()
	ahora := time.Now().UTC()
	t := Treaty{
		ID: 1, PlayerA: a, PlayerB: b,
		Type: TypeAlliance, Status: status,
		AllowsGarrison: allowsGarrison,
		ProposedAt:     ahora,
	}
	if status == StatusActive || status == StatusExpired || status == StatusBroken {
		t.AcceptedAt = &ahora
	}
	if status == StatusBroken {
		t.BrokenAt = &ahora
	}
	return t
}

// ─────────────────────────────────────────────────────────────
// Máquina de estados
// ─────────────────────────────────────────────────────────────

// SPEC-DIPLOMACY §4.2: una oferta se acepta, se rechaza, se retira o caduca; un
// tratado vigente caduca o se rompe.
func TestLaMaquinaDeEstadosAceptaExactamenteSeisTransiciones(t *testing.T) {
	permitidas := []struct {
		desde, hasta Status
	}{
		{StatusProposed, StatusActive},
		{StatusProposed, StatusRejected},
		{StatusProposed, StatusWithdrawn},
		{StatusProposed, StatusExpired},
		{StatusActive, StatusExpired},
		{StatusActive, StatusBroken},
	}
	for _, c := range permitidas {
		t.Run(string(c.desde)+"→"+string(c.hasta), func(t *testing.T) {
			antes := tratado(c.desde, false)
			despues, err := antes.TransitionTo(c.hasta, time.Now())
			require.NoError(t, err)
			assert.Equal(t, c.hasta, despues.Status)
		})
	}
}

func TestLaMaquinaDeEstadosRechazaTodoLoDemas(t *testing.T) {
	// Se enumeran TODOS los pares posibles y se rechaza cualquiera que no sea uno
	// de los seis legales. Escribir la lista negra a mano dejaría fuera los casos
	// que a nadie se le ocurren, que son justo los que rompen.
	todos := []Status{StatusProposed, StatusActive, StatusExpired, StatusBroken, StatusRejected, StatusWithdrawn}
	legales := map[string]bool{
		string(StatusProposed) + ">" + string(StatusActive):    true,
		string(StatusProposed) + ">" + string(StatusRejected):  true,
		string(StatusProposed) + ">" + string(StatusWithdrawn): true,
		string(StatusProposed) + ">" + string(StatusExpired):   true,
		string(StatusActive) + ">" + string(StatusExpired):     true,
		string(StatusActive) + ">" + string(StatusBroken):      true,
	}

	for _, desde := range todos {
		for _, hasta := range todos {
			if legales[string(desde)+">"+string(hasta)] {
				continue
			}
			t.Run(string(desde)+"→"+string(hasta), func(t *testing.T) {
				antes := tratado(desde, false)
				_, err := antes.TransitionTo(hasta, time.Now())
				assert.ErrorIs(t, err, ErrTransicionInvalida)
			})
		}
	}
}

func TestUnTratadoTerminadoNoRevive(t *testing.T) {
	// El caso concreto que más tienta implementar mal: "reactivar" un tratado
	// roto en vez de firmar uno nuevo. Firmar otro deja rastro de que hubo dos;
	// revivirlo borra esa historia.
	roto := tratado(StatusBroken, true)
	_, err := roto.TransitionTo(StatusActive, time.Now())
	assert.ErrorIs(t, err, ErrTransicionInvalida)

	caducado := tratado(StatusExpired, true)
	_, err = caducado.TransitionTo(StatusActive, time.Now())
	assert.ErrorIs(t, err, ErrTransicionInvalida)
}

func TestLaTransicionNoMutaElOriginal(t *testing.T) {
	antes := tratado(StatusProposed, false)
	despues, err := antes.TransitionTo(StatusActive, time.Now())
	require.NoError(t, err)

	assert.Equal(t, StatusProposed, antes.Status,
		"si la transacción falla después, el objeto en memoria no debe contar una historia que la base no confirmó")
	assert.Equal(t, StatusActive, despues.Status)
}

func TestAceptarSellaAcceptedAtYRomperSellaBrokenAt(t *testing.T) {
	instante := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)

	activo, err := tratado(StatusProposed, false).TransitionTo(StatusActive, instante)
	require.NoError(t, err)
	require.NotNil(t, activo.AcceptedAt)
	assert.Equal(t, instante, *activo.AcceptedAt)
	assert.Nil(t, activo.BrokenAt)

	roto, err := activo.TransitionTo(StatusBroken, instante.Add(time.Hour))
	require.NoError(t, err)
	require.NotNil(t, roto.BrokenAt)
	assert.Equal(t, instante.Add(time.Hour), *roto.BrokenAt)
}

func TestCaducarNoSobrescribeExpiresAt(t *testing.T) {
	// `expires_at` dice cuándo DEBÍA caducar. Sobrescribirlo con el instante en
	// que el tick se dio cuenta borraría la única prueba de si el servidor llegó
	// tarde.
	vencimiento := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	activo := tratado(StatusActive, false)
	activo.ExpiresAt = &vencimiento

	caducado, err := activo.TransitionTo(StatusExpired, vencimiento.Add(30*time.Second))
	require.NoError(t, err)
	require.NotNil(t, caducado.ExpiresAt)
	assert.Equal(t, vencimiento, *caducado.ExpiresAt)
}

func TestUnEstadoDesconocidoSeRechaza(t *testing.T) {
	_, err := tratado(StatusActive, false).TransitionTo(Status("SUSPENDIDO"), time.Now())
	assert.ErrorIs(t, err, ErrTratadoInvalido)
}

// ─────────────────────────────────────────────────────────────
// Autorización de guarnición
// ─────────────────────────────────────────────────────────────

func TestSoloUnTratadoActivoConElFlagAutorizaGuarnicion(t *testing.T) {
	// RN-GARR-006, que es el corazón de M7: lo que autoriza es el PAR
	// (estado, flag). Cualquiera de los dos por separado no autoriza nada.
	casos := []struct {
		status   Status
		flag     bool
		autoriza bool
	}{
		{StatusActive, true, true},
		{StatusActive, false, false},
		{StatusProposed, true, false},
		{StatusExpired, true, false},
		{StatusBroken, true, false},
		{StatusProposed, false, false},
	}
	for _, c := range casos {
		t.Run(string(c.status)+"/flag="+map[bool]string{true: "sí", false: "no"}[c.flag], func(t *testing.T) {
			assert.Equal(t, c.autoriza, tratado(c.status, c.flag).AuthorizesGarrison())
		})
	}
}

func TestLosTresTiposAutorizanPorIgualSiLlevanElFlag(t *testing.T) {
	// RN-GARR-006: no es el tipo lo que autoriza. Un TRADE con el flag autoriza
	// exactamente igual que una ALLIANCE.
	for _, tipo := range []Type{TypeNonAggression, TypeAlliance, TypeTrade} {
		t.Run(string(tipo), func(t *testing.T) {
			tr := tratado(StatusActive, true)
			tr.Type = tipo
			assert.True(t, tr.AuthorizesGarrison())
		})
	}
}

func TestFindAuthorizingEncuentraElTratadoEnCualquierOrdenDelPar(t *testing.T) {
	a, b := parOrdenado()
	tr := tratado(StatusActive, true)
	tr.PlayerA, tr.PlayerB = a, b

	_, ok := FindAuthorizing([]Treaty{tr}, a, b)
	assert.True(t, ok, "en orden canónico")

	_, ok = FindAuthorizing([]Treaty{tr}, b, a)
	assert.True(t, ok, "y en el orden inverso: una sola fila cubre ambas direcciones")
}

func TestFindAuthorizingIgnoraTratadosDeOtrosJugadores(t *testing.T) {
	a, b := parOrdenado()
	ajeno := tratado(StatusActive, true)

	_, ok := FindAuthorizing([]Treaty{ajeno}, a, b)
	assert.False(t, ok)
}

func TestFindAuthorizingIgnoraLosQueNoAutorizan(t *testing.T) {
	a, b := parOrdenado()
	propuesto := tratado(StatusProposed, true)
	propuesto.PlayerA, propuesto.PlayerB = a, b

	_, ok := FindAuthorizing([]Treaty{propuesto}, a, b)
	assert.False(t, ok, "un tratado propuesto no es un tratado: es una oferta")
}

// ─────────────────────────────────────────────────────────────
// Par canónico y caducidad
// ─────────────────────────────────────────────────────────────

func TestElParCanonicoEsEstableEnAmbosSentidos(t *testing.T) {
	x, y := uuid.New(), uuid.New()
	a1, b1 := CanonicalPair(x, y)
	a2, b2 := CanonicalPair(y, x)

	assert.Equal(t, a1, a2)
	assert.Equal(t, b1, b2)
	assert.Less(t, a1.String(), b1.String(), "el menor va primero")
}

func TestUnParFueraDeOrdenNoValida(t *testing.T) {
	a, b := parOrdenado()
	tr := tratado(StatusProposed, false)
	tr.PlayerA, tr.PlayerB = b, a // invertido a propósito

	assert.ErrorIs(t, tr.Validate(), ErrTratadoInvalido)
}

func TestUnTratadoConsigoMismoNoValida(t *testing.T) {
	mismo := uuid.New()
	tr := tratado(StatusProposed, false)
	tr.PlayerA, tr.PlayerB = mismo, mismo

	assert.ErrorIs(t, tr.Validate(), ErrTratadoInvalido)
}

func TestUnActivoSinAcceptedAtNoValida(t *testing.T) {
	tr := tratado(StatusActive, false)
	tr.AcceptedAt = nil
	assert.ErrorIs(t, tr.Validate(), ErrTratadoInvalido)
}

func TestLaCaducidadEsInclusivaEnElInstanteExacto(t *testing.T) {
	vencimiento := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	tr := tratado(StatusActive, true)
	tr.ExpiresAt = &vencimiento

	assert.False(t, tr.HasExpired(vencimiento.Add(-time.Nanosecond)), "un instante antes, sigue vivo")
	assert.True(t, tr.HasExpired(vencimiento), "en el instante exacto, ya caducó")
	assert.True(t, tr.HasExpired(vencimiento.Add(time.Hour)))
}

func TestUnTratadoSinVencimientoNoCaducaNunca(t *testing.T) {
	tr := tratado(StatusActive, true)
	require.Nil(t, tr.ExpiresAt)
	assert.False(t, tr.HasExpired(time.Now().Add(100*365*24*time.Hour)))
}

func TestSoloCaducanLosActivos(t *testing.T) {
	pasado := time.Now().Add(-time.Hour)
	for _, s := range []Status{StatusProposed, StatusExpired, StatusBroken} {
		t.Run(string(s), func(t *testing.T) {
			tr := tratado(s, true)
			tr.ExpiresAt = &pasado
			assert.False(t, tr.HasExpired(time.Now()),
				"caducar algo que no está activo produciría una transición ilegal")
		})
	}
}

// ─────────────────────────────────────────────────────────────
// Órdenes de los jugadores (SPEC-DIPLOMACY §4.3)
// ─────────────────────────────────────────────────────────────

// oferta es un tratado PROPOSED propuesto por PlayerA.
func oferta() Treaty {
	t := tratado(StatusProposed, true)
	a := t.PlayerA
	t.ProposedBy = &a
	return t
}

// INV-DIPLO-001: sólo responde el firmante que no propuso, y sólo retira quien
// propuso; un tercero no puede nada.
func TestSoloRespondeQuienNoPropusoYSoloRetiraQuienPropuso(t *testing.T) {
	o := oferta()
	tercero := uuid.New()

	assert.NoError(t, o.CanRespond(o.PlayerB))
	assert.ErrorIs(t, o.CanRespond(o.PlayerA), ErrNoPuede, "la oferta propia no se responde")
	assert.ErrorIs(t, o.CanRespond(tercero), ErrNoEsParte)

	assert.NoError(t, o.CanWithdraw(o.PlayerA))
	assert.ErrorIs(t, o.CanWithdraw(o.PlayerB), ErrNoPuede, "la oferta ajena no se retira")
	assert.ErrorIs(t, o.CanWithdraw(tercero), ErrNoEsParte)

	vigente := tratado(StatusActive, true)
	assert.ErrorIs(t, vigente.CanRespond(vigente.PlayerB), ErrNoPuede, "sólo se responde a una oferta")
	admin := tratado(StatusProposed, true) // sin proposed_by: nadie la responde
	assert.ErrorIs(t, admin.CanRespond(admin.PlayerB), ErrNoPuede)
}

// RN-DIPLO-003 y RN-DIPLO-004: cualquiera de los dos rompe un tratado vigente,
// una sola vez; durante el preaviso la paz sigue y al vencer termina.
func TestRomperAbreUnPreavisoDuranteElQueLaPazSigue(t *testing.T) {
	vigente := tratado(StatusActive, true)
	ahora := time.Now().UTC()
	assert.NoError(t, vigente.CanBreak(vigente.PlayerA))
	assert.NoError(t, vigente.CanBreak(vigente.PlayerB))
	assert.ErrorIs(t, vigente.CanBreak(uuid.New()), ErrNoEsParte)
	assert.ErrorIs(t, oferta().CanBreak(vigente.PlayerA), ErrNoEsParte)

	fin := ahora.Add(15 * time.Minute)
	vigente.BreakEffectiveAt = &fin
	assert.ErrorIs(t, vigente.CanBreak(vigente.PlayerA), ErrNoPuede, "no se rompe dos veces")
	assert.True(t, vigente.GivesPeaceAt(ahora.Add(14*time.Minute)), "en el preaviso, la paz sigue")
	assert.False(t, vigente.GivesPeaceAt(fin), "al vencer, termina")
	cuando, estado, ok := vigente.EndsAt()
	require.True(t, ok)
	assert.Equal(t, fin, cuando)
	assert.Equal(t, StatusBroken, estado)
}

// EndsAt elige lo que llegue antes entre la caducidad y el fin del preaviso.
func TestElFinDeUnTratadoEsLoQueLlegueAntes(t *testing.T) {
	vigente := tratado(StatusActive, false)
	_, _, ok := vigente.EndsAt()
	assert.False(t, ok, "sin plazo ni ruptura no termina solo")

	ahora := time.Now().UTC()
	caduca, rompe := ahora.Add(time.Minute), ahora.Add(time.Hour)
	vigente.ExpiresAt, vigente.BreakEffectiveAt = &caduca, &rompe
	cuando, estado, _ := vigente.EndsAt()
	assert.Equal(t, caduca, cuando)
	assert.Equal(t, StatusExpired, estado)
}

// Q1, precisada por SPEC-TRADE: se proponen los tres tipos; la guarnición la
// fija el tipo. Una oferta rechazada, retirada o caducada sella su cierre.
func TestTiposProponiblesYCierreDeLasOfertas(t *testing.T) {
	assert.True(t, TypeNonAggression.Proposable())
	assert.True(t, TypeAlliance.Proposable())
	assert.True(t, TypeTrade.Proposable())
	assert.False(t, Type("VASALLAJE").Proposable())
	assert.False(t, TypeTrade.GrantsGarrison())
	assert.True(t, TypeAlliance.GrantsGarrison())
	assert.False(t, TypeNonAggression.GrantsGarrison())

	for _, fin := range []Status{StatusRejected, StatusWithdrawn, StatusExpired} {
		cerrada, err := oferta().TransitionTo(fin, time.Now())
		require.NoError(t, err)
		assert.NotNil(t, cerrada.ClosedAt, "%s sella closed_at", fin)
		assert.False(t, cerrada.GivesPeaceAt(time.Now()))
	}
	caducado, err := tratado(StatusActive, false).TransitionTo(StatusExpired, time.Now())
	require.NoError(t, err)
	assert.Nil(t, caducado.ClosedAt, "un tratado vigente que caduca no es una oferta cerrada")
}

// SPEC-TRADE §4.1: la escalera TRADE < pacto < alianza. Sólo se sube, y un tipo
// desconocido no sustituye a nada ni es sustituido.
func TestLaEscaleraDeTratadosSoloSube(t *testing.T) {
	assert.True(t, TypeNonAggression.Supersedes(TypeTrade))
	assert.True(t, TypeAlliance.Supersedes(TypeTrade))
	assert.True(t, TypeAlliance.Supersedes(TypeNonAggression))

	assert.False(t, TypeTrade.Supersedes(TypeTrade), "repetir no es subir")
	assert.False(t, TypeTrade.Supersedes(TypeNonAggression), "bajar no existe")
	assert.False(t, TypeNonAggression.Supersedes(TypeAlliance))
	assert.False(t, Type("VASALLAJE").Supersedes(TypeTrade))
	assert.False(t, TypeAlliance.Supersedes(Type("VASALLAJE")))
}

// SPEC-TRADE §4.1 e INV-TRADE-002: los tres tipos vigentes dejan comerciar,
// también durante el preaviso, y dejan de hacerlo al llegar su fin. Comerciar no
// da paz.
func TestQueTratadoDejaComerciar(t *testing.T) {
	ahora := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	for _, tipo := range []Type{TypeTrade, TypeNonAggression, TypeAlliance} {
		vigente := oferta()
		vigente.Type, vigente.Status = tipo, StatusActive
		assert.True(t, vigente.AllowsTradeAt(ahora), "%s deja comerciar", tipo)

		fin := ahora.Add(15 * time.Minute)
		vigente.BreakEffectiveAt = &fin
		assert.True(t, vigente.AllowsTradeAt(ahora), "%s en preaviso sigue dejando comerciar", tipo)
		assert.False(t, vigente.AllowsTradeAt(fin), "%s deja de valer al vencer el preaviso", tipo)
	}

	comercio := oferta()
	comercio.Type, comercio.Status = TypeTrade, StatusActive
	assert.False(t, comercio.GivesPeaceAt(ahora), "TRADE no da paz")

	propuesto := oferta()
	propuesto.Type = TypeTrade
	assert.False(t, propuesto.AllowsTradeAt(ahora), "una oferta no es un tratado")
}
