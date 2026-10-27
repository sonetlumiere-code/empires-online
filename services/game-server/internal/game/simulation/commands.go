package simulation

import (
	"context"
	"hash/fnv"
	"strconv"

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

// MovementPersistFailed lo devuelve la cola de persistencia cuando el inicio de
// un movimiento no se pudo escribir tras agotar los reintentos (ADR-013 §2.4).
// No es una orden de un jugador: es la compensación, que tiene que aplicarse en
// la goroutine del loop como cualquier otro cambio del mundo.
type MovementPersistFailed struct {
	UnitID     int64
	MovementID int64
}

func (MovementPersistFailed) commandType() string { return "movement.persist_failed" }

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
	// BroadcastChunkExcept entrega el mensaje a toda sesión suscrita al chunk
	// cuyo jugador NO sea `except`. Es el canal de los terceros en el
	// ocultamiento: el propietario recibe otro mensaje por SendToPlayer
	// (docs/specs/safe-zones.md §12, INV-SAFE-004).
	BroadcastChunkExcept(cx, cy int32, except uuid.UUID, msgType string, payload any)
	// BroadcastChunkTransition anuncia que una entidad pasó del chunk `from` al
	// chunk `to`: entrega el spawn a las sesiones suscritas a `to` y NO a
	// `from`, y el despawn a las suscritas a `from` y NO a `to`. Quien observa
	// los dos no recibe nada: sigue viendo la entidad por sus deltas de siempre
	// (docs/architecture/networking.md §4.3). Con `onlyPlayer` distinto de
	// uuid.Nil sólo cuentan las sesiones de ese jugador: es como una unidad
	// que sólo ve su dueño cruza de chunk sin revelarse (INV-SAFE-004).
	BroadcastChunkTransition(from, to world.ChunkCoord, onlyPlayer uuid.UUID,
		spawnType string, spawnPayload any, despawnType string, despawnPayload any)
	// SendToPlayer entrega el mensaje a todas las sesiones de un jugador.
	SendToPlayer(playerID uuid.UUID, msgType, requestID string, payload any)
}

// Job es una escritura durable diferida.
type Job struct {
	// Name identifica el trabajo en logs y métricas.
	Name string
	// Key es la entidad que escribe, por ejemplo UnitKey(42). Los trabajos con
	// la misma clave se ejecutan en el orden en que se encolaron, reintentos
	// incluidos (ADR-013 §2.2). Vacía, el trabajo no tiene orden respecto a
	// ningún otro.
	Key string
	// Run ejecuta la escritura. Debe ser idempotente: puede reintentarse.
	Run func(ctx context.Context) error
	// OnPermanentFailure se llama tras agotar los reintentos. Corre en la
	// goroutine del worker: NO puede tocar el estado del mundo. Para compensar,
	// devuelve un comando al loop por Deps.Inbox.
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
	// Partitions es cuántas particiones ordenadas tiene. La simulación la usa
	// para partir un lote —el volcado periódico— en un trabajo por partición,
	// de modo que cada unidad siga en orden con sus demás escrituras.
	Partitions() int
}

// PartitionOf es la partición de una clave entre n. La cola y la simulación
// usan esta misma función: por eso un lote agrupado aquí cae entero en la
// partición de cualquiera de sus claves.
func PartitionOf(key string, n int) int {
	if n <= 1 {
		return 0
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	return int(h.Sum32() % uint32(n))
}

// UnitKey, CityKey y TreatyKey son las claves de partición de cada entidad.
func UnitKey(id int64) string   { return "unit:" + strconv.FormatInt(id, 10) }
func CityKey(id int64) string   { return "city:" + strconv.FormatInt(id, 10) }
func TreatyKey(id int64) string { return "treaty:" + strconv.FormatInt(id, 10) }

// NoopBroadcaster descarta todo. Útil en tests de simulación pura.
type NoopBroadcaster struct{}

func (NoopBroadcaster) BroadcastChunk(int32, int32, string, any)                  {}
func (NoopBroadcaster) BroadcastChunks([]world.ChunkCoord, string, any)           {}
func (NoopBroadcaster) BroadcastChunkExcept(int32, int32, uuid.UUID, string, any) {}
func (NoopBroadcaster) BroadcastChunkTransition(_, _ world.ChunkCoord, _ uuid.UUID, _ string, _ any, _ string, _ any) {
}
func (NoopBroadcaster) SendToPlayer(uuid.UUID, string, string, any) {}

// NoopPersister descarta las escrituras. Útil en tests de simulación pura.
type NoopPersister struct{}

func (NoopPersister) Submit(Job)      {}
func (NoopPersister) Depth() int      { return 0 }
func (NoopPersister) Partitions() int { return 1 }
