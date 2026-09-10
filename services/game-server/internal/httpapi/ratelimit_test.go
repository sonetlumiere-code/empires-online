package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func peticionDesde(remoteAddr string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
	r.RemoteAddr = remoteAddr
	return r
}

func TestElCuboSeAgotaTrasElBurst(t *testing.T) {
	l := NewIPLimiter(60, 3, false)
	ahora := time.Now()

	for i := 0; i < 3; i++ {
		assert.True(t, l.Allow(peticionDesde("10.0.0.1:1234"), ahora), "petición %d", i+1)
	}
	assert.False(t, l.Allow(peticionDesde("10.0.0.1:1234"), ahora), "la cuarta debe rechazarse")
}

func TestElPuertoDeOrigenNoCuentaComoClienteDistinto(t *testing.T) {
	// Cada petición TCP llega desde un puerto efímero distinto. Si el puerto
	// formara parte de la clave, cada una estrenaría cubo y el límite no
	// limitaría nada en absoluto.
	l := NewIPLimiter(60, 2, false)
	ahora := time.Now()

	assert.True(t, l.Allow(peticionDesde("10.0.0.1:1111"), ahora))
	assert.True(t, l.Allow(peticionDesde("10.0.0.1:2222"), ahora))
	assert.False(t, l.Allow(peticionDesde("10.0.0.1:3333"), ahora),
		"el mismo host con otro puerto sigue siendo el mismo cliente")
	assert.Equal(t, 1, l.Tracked())
}

func TestCadaDireccionTieneSuPropioCubo(t *testing.T) {
	l := NewIPLimiter(60, 1, false)
	ahora := time.Now()

	assert.True(t, l.Allow(peticionDesde("10.0.0.1:1111"), ahora))
	assert.False(t, l.Allow(peticionDesde("10.0.0.1:1111"), ahora))
	assert.True(t, l.Allow(peticionDesde("10.0.0.2:1111"), ahora),
		"agotar un cliente no debe afectar a otro")
}

func TestElCuboSeRellenaConElTiempo(t *testing.T) {
	// 60 por minuto = 1 por segundo.
	l := NewIPLimiter(60, 1, false)
	ahora := time.Now()

	require.True(t, l.Allow(peticionDesde("10.0.0.1:1"), ahora))
	require.False(t, l.Allow(peticionDesde("10.0.0.1:1"), ahora))

	assert.False(t, l.Allow(peticionDesde("10.0.0.1:1"), ahora.Add(500*time.Millisecond)),
		"media ficha no basta")
	assert.True(t, l.Allow(peticionDesde("10.0.0.1:1"), ahora.Add(1100*time.Millisecond)),
		"pasado un segundo hay ficha otra vez")
}

func TestElCuboNoAcumulaMasAlláDelBurst(t *testing.T) {
	// Estar una hora sin pedir nada no da derecho a una hora de peticiones de
	// golpe: eso convertiría el límite en un ahorro acumulable.
	l := NewIPLimiter(60, 2, false)
	ahora := time.Now()

	require.True(t, l.Allow(peticionDesde("10.0.0.1:1"), ahora))
	tarde := ahora.Add(time.Hour)

	assert.True(t, l.Allow(peticionDesde("10.0.0.1:1"), tarde))
	assert.True(t, l.Allow(peticionDesde("10.0.0.1:1"), tarde))
	assert.False(t, l.Allow(peticionDesde("10.0.0.1:1"), tarde), "sólo el burst, ni una más")
}

// ─────────────────────────────────────────────────────────────
// Cabeceras de proxy
// ─────────────────────────────────────────────────────────────

func TestSinConfiarEnElProxyNoSePuedeFalsificarLaIP(t *testing.T) {
	// Es el comportamiento por DEFECTO y el importante: si se creyera a
	// X-Forwarded-For sin un proxy delante, cualquiera se saltaría el límite
	// cambiando una cabecera en cada petición.
	l := NewIPLimiter(60, 1, false)
	ahora := time.Now()

	r1 := peticionDesde("10.0.0.1:1111")
	r1.Header.Set("X-Forwarded-For", "1.2.3.4")
	r2 := peticionDesde("10.0.0.1:2222")
	r2.Header.Set("X-Forwarded-For", "5.6.7.8")

	assert.True(t, l.Allow(r1, ahora))
	assert.False(t, l.Allow(r2, ahora), "cambiar la cabecera no debe estrenar cubo")
}

func TestConfiandoEnElProxySeUsaElPrimerValorDeXForwardedFor(t *testing.T) {
	// El primer valor es el cliente original; el resto son los proxies que
	// atravesó.
	l := NewIPLimiter(60, 1, true)
	ahora := time.Now()

	r1 := peticionDesde("10.0.0.1:1111")
	r1.Header.Set("X-Forwarded-For", "1.2.3.4, 10.0.0.9")
	r2 := peticionDesde("10.0.0.1:2222")
	r2.Header.Set("X-Forwarded-For", "5.6.7.8, 10.0.0.9")

	assert.True(t, l.Allow(r1, ahora))
	assert.True(t, l.Allow(r2, ahora), "son dos clientes distintos detrás del mismo proxy")
	assert.False(t, l.Allow(r1, ahora), "y el primero ya gastó el suyo")
}

func TestConfiandoEnElProxySeAceptaXRealIPSiNoHayXForwardedFor(t *testing.T) {
	l := NewIPLimiter(60, 1, true)
	ahora := time.Now()

	r := peticionDesde("10.0.0.1:1111")
	r.Header.Set("X-Real-IP", "1.2.3.4")
	assert.True(t, l.Allow(r, ahora))

	otro := peticionDesde("10.0.0.1:2222")
	otro.Header.Set("X-Real-IP", "1.2.3.4")
	assert.False(t, l.Allow(otro, ahora))
}

func TestSinCabecerasDeProxySeCaeAlRemoteAddr(t *testing.T) {
	l := NewIPLimiter(60, 1, true)
	ahora := time.Now()

	assert.True(t, l.Allow(peticionDesde("10.0.0.1:1111"), ahora))
	assert.False(t, l.Allow(peticionDesde("10.0.0.1:2222"), ahora))
}

// ─────────────────────────────────────────────────────────────
// Memoria acotada
// ─────────────────────────────────────────────────────────────

func TestLosClientesInactivosSeDesalojan(t *testing.T) {
	// El limitador no puede convertirse en el agotamiento de memoria que venía
	// a evitar. Se comprueba que el desalojo ocurre de verdad forzándolo con el
	// reloj, en lugar de esperar quince minutos.
	l := NewIPLimiter(60, 1, false)
	ahora := time.Now()

	l.Allow(peticionDesde("10.0.0.1:1"), ahora)
	l.Allow(peticionDesde("10.0.0.2:1"), ahora)
	require.Equal(t, 2, l.Tracked())

	l.mu.Lock()
	l.evict(ahora.Add(idleEviction + time.Minute))
	l.mu.Unlock()

	assert.Zero(t, l.Tracked(), "los cubos inactivos deben desaparecer")
}

func TestUnClienteActivoNoSeDesaloja(t *testing.T) {
	l := NewIPLimiter(60, 1, false)
	ahora := time.Now()

	l.Allow(peticionDesde("10.0.0.1:1"), ahora)

	l.mu.Lock()
	l.evict(ahora.Add(time.Minute))
	l.mu.Unlock()

	assert.Equal(t, 1, l.Tracked(), "un minuto de inactividad no es motivo de desalojo")
}

func TestElLimitadorEsSeguroDesdeVariasGoroutines(t *testing.T) {
	// Los handlers de HTTP corren en paralelo. Sin el mutex esto sería una
	// carrera, y el detector la señalaría.
	l := NewIPLimiter(6000, 1000, false)
	ahora := time.Now()

	hecho := make(chan struct{})
	for i := 0; i < 16; i++ {
		go func() {
			for j := 0; j < 50; j++ {
				l.Allow(peticionDesde("10.0.0.1:1"), ahora)
			}
			hecho <- struct{}{}
		}()
	}
	for i := 0; i < 16; i++ {
		<-hecho
	}
	assert.Equal(t, 1, l.Tracked())
}
