package simulation

import (
	"context"

	"github.com/google/uuid"

	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
)

// Command / Event / State
//
// Los tres conceptos son distintos y el código los mantiene separados a propósito:
//
//	Command  — una INTENCIÓN que puede rechazarse.   MoveUnit, CancelMovement
//	Event    — un HECHO consumado, en pasado.        UnitMovementStarted
//	State    — la consecuencia sobre el mundo.       Unit.Status = MOVING
//
// Confundirlos es lo que lleva a clientes que "aplican" comandos localmente y a
// servidores que aceptan estado del cliente.
// Ver ../../../../docs/architecture/overview.md

// Command es cualquier orden que el game loop aplica sobre el mundo.
type Command interface{ commandType() string }

// MoveUnit ordena a una unidad desplazarse a un tile.
//
// Nótese lo que NO lleva: ni ruta, ni waypoints, ni tiempo estimado. El cliente
// dice adónde; el servidor decide si se puede, por dónde y cuánto tarda.
type MoveUnit struct {
	PlayerID  uuid.UUID
	SessionID uuid.UUID
	RequestID string
	UnitID    int64
	Target    world.Tile
}

func (MoveUnit) commandType() string { return "unit.move" }

// CancelMovement detiene el movimiento activo de una unidad.
type CancelMovement struct {
	PlayerID  uuid.UUID
	SessionID uuid.UUID
	RequestID string
	UnitID    int64
}

func (CancelMovement) commandType() string { return "unit.cancel_move" }

// PlayerConnected notifica que un jugador ha completado el handshake.
type PlayerConnected struct {
	PlayerID  uuid.UUID
	SessionID uuid.UUID
}

func (PlayerConnected) commandType() string { return "player.connected" }

// PlayerDisconnected notifica que un jugador ha perdido su sesión.
type PlayerDisconnected struct {
	PlayerID  uuid.UUID
	SessionID uuid.UUID
}

func (PlayerDisconnected) commandType() string { return "player.disconnected" }

// Broadcaster entrega mensajes a las sesiones conectadas.
//
// La simulación no sabe qué es un WebSocket: sólo sabe emitir hechos hacia un
// área de interés o hacia un jugador concreto.
type Broadcaster interface {
	// BroadcastChunk entrega el mensaje a toda sesión suscrita a ese chunk.
	BroadcastChunk(cx, cy int32, msgType string, payload any)
	// BroadcastChunks entrega el mensaje UNA vez a cada sesión suscrita a alguno
	// de esos chunks. Para entidades que abarcan varios, como los territorios:
	// llamar a BroadcastChunk en bucle duplicaría el mensaje.
	BroadcastChunks(chunks []world.ChunkCoord, msgType string, payload any)
	// SendToPlayer entrega el mensaje a todas las sesiones de un jugador.
	SendToPlayer(playerID uuid.UUID, msgType, requestID string, payload any)
}

// Job es una escritura durable diferida.
type Job struct {
	// Name identifica el trabajo en logs y métricas.
	Name string
	// Run ejecuta la escritura. Debe ser idempotente: puede reintentarse.
	Run func(ctx context.Context) error
	// OnPermanentFailure compensa el fallo definitivo tras agotar los reintentos.
	// Es lo que permite deshacer en el mundo lo que no se pudo escribir en disco.
	OnPermanentFailure func(err error)
}

// Persister acepta escrituras durables sin bloquear al game loop.
//
// Ésta es la frontera que hace cumplible la regla "el tick nunca hace I/O de
// PostgreSQL": la simulación encola y sigue; los workers escriben.
type Persister interface {
	Submit(job Job)
	// Depth es la profundidad actual de la cola (métrica y contrapresión).
	Depth() int
}

// NoopBroadcaster descarta todo. Útil en tests de simulación pura.
type NoopBroadcaster struct{}

func (NoopBroadcaster) BroadcastChunk(int32, int32, string, any)        {}
func (NoopBroadcaster) BroadcastChunks([]world.ChunkCoord, string, any) {}
func (NoopBroadcaster) SendToPlayer(uuid.UUID, string, string, any)     {}

// NoopPersister descarta las escrituras. Útil en tests de simulación pura.
type NoopPersister struct{}

func (NoopPersister) Submit(Job) {}
func (NoopPersister) Depth() int { return 0 }
