package postgres_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/persistence/postgres"
)

// ADR-028 §6: una fila sólo está asentada cuando ya no puede quedar abierta
// ninguna transacción con un id menor, y eso exige más de dos intentos de la
// cola. Si alguien alarga el plazo de un intento, este test le recuerda el tope.
func TestElAsentadoCubreDosIntentosDeLaCola(t *testing.T) {
	require.Greater(t, postgres.EventLogSettle, 2*postgres.WriteAttemptTimeout)
}
