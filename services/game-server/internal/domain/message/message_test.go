package message

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// RN-MSG-003: el texto se recorta, se cuenta en caracteres y no admite más
// control que el salto de línea.
func TestElTextoSeRecortaYSeCuentaEnCaracteres(t *testing.T) {
	body, err := NormalizeBody("  ¿60 de vino\r\npor 200 de piedra?  \n")
	require.NoError(t, err)
	assert.Equal(t, "¿60 de vino\npor 200 de piedra?", body)

	_, err = NormalizeBody(strings.Repeat("ñ", MaxChars))
	assert.NoError(t, err, "500 caracteres de dos bytes caben: se cuenta en caracteres, no en bytes")
	_, err = NormalizeBody(strings.Repeat("𝔸", MaxChars))
	assert.NoError(t, err, "y de cuatro bytes también")
	_, err = NormalizeBody(strings.Repeat("a", MaxChars+1))
	assert.ErrorIs(t, err, ErrTooLong)
	_, err = NormalizeBody("  " + strings.Repeat("a", MaxChars) + "\n\n")
	assert.NoError(t, err, "los extremos no cuentan")

	for _, vacio := range []string{"", "   ", "\n\t \r\n"} {
		_, err = NormalizeBody(vacio)
		assert.ErrorIs(t, err, ErrEmpty, "%q", vacio)
	}
	for _, malo := range []string{"a\x00b", "a\tb", "a\x1bb", "a\rb", "a‮b", "a⁦b", "a\x7fb", "a\u0085b", string([]byte{'a', 0xff})} {
		_, err = NormalizeBody(malo)
		assert.ErrorIs(t, err, ErrControl, "%q", malo)
	}
	_, err = NormalizeBody("<b>hola</b> & 😀")
	assert.NoError(t, err, "el marcado no se filtra: el cliente lo pinta como texto")
}

func TestLaVistaPreviaCortaEnOchentaCaracteres(t *testing.T) {
	assert.Equal(t, "hola mundo", Preview("hola\nmundo"))
	corto := strings.Repeat("ñ", PreviewChars)
	assert.Equal(t, corto, Preview(corto))
	largo := Preview(strings.Repeat("ñ", PreviewChars+10))
	assert.Equal(t, PreviewChars, len([]rune(largo)))
	assert.True(t, strings.HasSuffix(largo, "…"))
}

func TestLaParejaNoDependeDelOrden(t *testing.T) {
	a := uuid.MustParse("00000000-0000-4000-8000-000000000001")
	b := uuid.MustParse("ffffffff-0000-4000-8000-000000000000")
	lo, hi := Pair(a, b)
	lo2, hi2 := Pair(b, a)
	assert.Equal(t, [2]uuid.UUID{a, b}, [2]uuid.UUID{lo, hi})
	assert.Equal(t, [2]uuid.UUID{lo, hi}, [2]uuid.UUID{lo2, hi2})
}

// RN-MSG-005: diez envíos por minuto y diez conversaciones nuevas por hora,
// por jugador; lo rechazado o deshecho no cuenta.
func TestLosLimitesDeRitmoPorJugador(t *testing.T) {
	l := NewLimiter(10, 10)
	yo, otro := uuid.New(), uuid.New()
	t0 := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)

	for i := range 10 {
		_, ok := l.ReserveSend(yo, t0.Add(time.Duration(i)*time.Second))
		require.True(t, ok, "envío %d", i)
	}
	_, ok := l.ReserveSend(yo, t0.Add(10*time.Second))
	assert.False(t, ok, "el undécimo en el mismo minuto")
	_, ok = l.ReserveSend(otro, t0.Add(10*time.Second))
	assert.True(t, ok, "el límite es de cada jugador")
	_, ok = l.ReserveSend(yo, t0.Add(time.Minute-time.Millisecond))
	assert.False(t, ok, "el primero sigue dentro de la ventana hasta cumplir el minuto")
	_, ok = l.ReserveSend(yo, t0.Add(time.Minute))
	assert.True(t, ok, "cumplido el minuto del primero, vuelve a haber sitio")

	release, ok := l.ReserveNewConversation(otro, t0)
	require.True(t, ok)
	release()
	release()
	for i := range 10 {
		_, ok := l.ReserveNewConversation(otro, t0.Add(time.Duration(i)*time.Minute))
		require.True(t, ok, "conversación %d: la deshecha no contó", i)
	}
	_, ok = l.ReserveNewConversation(otro, t0.Add(59*time.Minute))
	assert.False(t, ok, "la undécima en la hora")
	_, ok = l.ReserveNewConversation(otro, t0.Add(time.Hour+time.Second))
	assert.True(t, ok)
}

func TestUnLimiteDeCeroNoLimitaYElMapaNoCrece(t *testing.T) {
	sin := NewLimiter(0, 0)
	for range 100 {
		_, ok := sin.ReserveSend(uuid.New(), time.Unix(0, 0))
		require.True(t, ok)
	}
	assert.Zero(t, sin.players())

	l := NewLimiter(1, 1)
	yo := uuid.New()
	release, ok := l.ReserveSend(yo, time.Unix(100, 0))
	require.True(t, ok)
	release()
	assert.Zero(t, l.players(), "deshecho lo único que tenía, el jugador sale del mapa")
	_, ok = l.ReserveSend(yo, time.Unix(100, 0))
	require.True(t, ok)
	_, ok = l.ReserveSend(yo, time.Unix(200, 0))
	require.True(t, ok)
	assert.Equal(t, 1, l.players())
}
