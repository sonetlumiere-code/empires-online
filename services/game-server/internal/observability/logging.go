// Package observability agrupa logging estructurado, métricas y health checks.
//
// Un servidor de juego persistente 24/7 falla de madrugada. Lo que decide si eso
// es un incidente de diez minutos o de tres horas es lo que se instrumentó antes.
package observability

import (
	"log/slog"
	"os"
	"strings"
)

// NewLogger crea el logger estructurado del proceso.
//
// JSON siempre, también en desarrollo: que los logs locales y los de producción
// tengan el mismo formato evita descubrir en plena incidencia que el campo que
// necesitas sólo existe en un entorno.
func NewLogger(level string, env string) *slog.Logger {
	var lvl slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn", "warning":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}

	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: lvl,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			// Nombre de campo estable para el timestamp.
			if a.Key == slog.TimeKey {
				a.Key = "ts"
			}
			return a
		},
	})

	return slog.New(handler).With(
		slog.String("service", "game-server"),
		slog.String("env", env),
	)
}

// Campos de log estándar. Usar constantes evita que la mitad del código escriba
// "player_id" y la otra mitad "playerId", que es como se pierden las búsquedas.
const (
	FieldPlayerID  = "player_id"
	FieldSessionID = "session_id"
	FieldRequestID = "request_id"
	FieldUnitID    = "unit_id"
	FieldCityID    = "city_id"
	FieldTick      = "tick"
	FieldMsgType   = "msg_type"
	FieldErrorCode = "error_code"
)
