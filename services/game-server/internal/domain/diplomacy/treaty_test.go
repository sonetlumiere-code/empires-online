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

func TestLaMaquinaDeEstadosAceptaExactamenteTresTransiciones(t *testing.T) {
	permitidas := []struct {
		desde, hasta Status
	}{
		{StatusProposed, StatusActive},
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
	// de los tres legales. Escribir la lista negra a mano dejaría fuera los casos
	// que a nadie se le ocurren, que son justo los que rompen.
	todos := []Status{StatusProposed, StatusActive, StatusExpired, StatusBroken}
	legales := map[string]bool{
		string(StatusProposed) + ">" + string(StatusActive): true,
		string(StatusActive) + ">" + string(StatusExpired):  true,
		string(StatusActive) + ">" + string(StatusBroken):   true,
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
