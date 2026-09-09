package protocol

// Códigos de error estables.
//
// La lógica de control (cliente y servidor) se programa SIEMPRE sobre estos
// códigos, nunca sobre el texto del mensaje. Añadir un código es un cambio
// aditivo; renombrar o eliminar uno rompe el contrato y exige una nueva versión.
const (
	// Autenticación, autorización y transporte.
	CodeUnauthorized       = "UNAUTHORIZED"
	CodeForbidden          = "FORBIDDEN"
	CodeInvalidMessage     = "INVALID_MESSAGE"
	CodeUnsupportedVersion = "UNSUPPORTED_VERSION"
	CodeRateLimited        = "RATE_LIMITED"
	CodeMessageTooLarge    = "MESSAGE_TOO_LARGE"

	// Unidades.
	CodeUnitNotFound   = "UNIT_NOT_FOUND"
	CodeUnitNotOwned   = "UNIT_NOT_OWNED"
	CodeUnitNotMovable = "UNIT_NOT_MOVABLE"
	CodeUnitDead       = "UNIT_DEAD"
	CodeUnitGarrisoned = "UNIT_GARRISONED"

	// Movimiento y pathfinding.
	CodeInvalidTarget     = "INVALID_TARGET"
	CodeTargetOutOfBounds = "TARGET_OUT_OF_BOUNDS"
	CodeTargetNotWalkable = "TARGET_NOT_WALKABLE"
	CodePathNotFound      = "PATH_NOT_FOUND"
	CodePathTooLong       = "PATH_TOO_LONG"

	// Ciudad, diplomacia y economía.
	CodeCityNotFound           = "CITY_NOT_FOUND"
	CodeCityProtected          = "CITY_PROTECTED"
	CodeTreatyRequired         = "TREATY_REQUIRED"
	CodePopulationLimitReached = "POPULATION_LIMIT_REACHED"

	// Genéricos.
	CodeInternalError  = "INTERNAL_ERROR"
	CodeNotImplemented = "NOT_IMPLEMENTED"
)

// AllErrorCodes es el catálogo cerrado. Un contract test comprueba que coincide
// exactamente con el exportado por packages/protocol.
var AllErrorCodes = []string{
	CodeUnauthorized, CodeForbidden, CodeInvalidMessage, CodeUnsupportedVersion,
	CodeRateLimited, CodeMessageTooLarge,
	CodeUnitNotFound, CodeUnitNotOwned, CodeUnitNotMovable, CodeUnitDead, CodeUnitGarrisoned,
	CodeInvalidTarget, CodeTargetOutOfBounds, CodeTargetNotWalkable, CodePathNotFound, CodePathTooLong,
	CodeCityNotFound, CodeCityProtected, CodeTreatyRequired, CodePopulationLimitReached,
	CodeInternalError, CodeNotImplemented,
}

// Códigos de cierre de WebSocket específicos de la aplicación (rango privado 4000-4999).
const (
	CloseInvalidMessage   = 4400
	CloseUnauthenticated  = 4401
	CloseForbidden        = 4403
	CloseHandshakeTimeout = 4408
	CloseRateLimited      = 4429
	CloseInternalError    = 4500
)

// Error es un error de aplicación con código estable, apto para enviarse al cliente.
type Error struct {
	Code    string
	Message string
	Details map[string]any
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// NewError construye un error de protocolo.
func NewError(code, message string) *Error {
	return &Error{Code: code, Message: message}
}

// WithDetails añade contexto estructurado al error.
func (e *Error) WithDetails(details map[string]any) *Error {
	e.Details = details
	return e
}
