// Package protocol define el contrato WebSocket del lado servidor.
//
// La FUENTE DE VERDAD del contrato es packages/protocol (Zod). Este paquete lo
// implementa en Go y embebe el JSON Schema exportado desde allí para que los
// contract tests detecten cualquier deriva antes de que llegue a producción.
// Ver ../../../../docs/decisions/ADR-009-shared-protocol-package.md
package protocol

import "encoding/json"

// Version es la versión de protocolo que habla este servidor.
const Version = 1

// ─────────────────────────────────────────────────────────────
// Tipos de mensaje
// ─────────────────────────────────────────────────────────────

// Mensajes cliente → servidor.
const (
	TypeSessionHello   = "session.hello"
	TypeSessionPing    = "session.ping"
	TypeSessionView    = "session.view"
	TypeUnitMove       = "unit.move"
	TypeUnitCancelMove = "unit.cancel_move"
)

// Mensajes servidor → cliente.
const (
	TypeSessionWelcome        = "session.welcome"
	TypeSessionPong           = "session.pong"
	TypeSystemError           = "system.error"
	TypeWorldSnapshot         = "world.snapshot"
	TypeEntitySpawn           = "entity.spawn"
	TypeEntityUpdate          = "entity.update"
	TypeEntityDespawn         = "entity.despawn"
	TypeCityUpdate            = "city.update"
	TypeTerritoryUpdate       = "territory.update"
	TypeUnitMoveAccepted      = "unit.move.accepted"
	TypeUnitMoveRejected      = "unit.move.rejected"
	TypeUnitMovementStarted   = "unit.movement.started"
	TypeUnitMovementCompleted = "unit.movement.completed"
	TypeUnitMovementCancelled = "unit.movement.cancelled"
)

// ClientMessageTypes es el catálogo cerrado de comandos admitidos.
var ClientMessageTypes = map[string]bool{
	TypeSessionHello:   true,
	TypeSessionPing:    true,
	TypeSessionView:    true,
	TypeUnitMove:       true,
	TypeUnitCancelMove: true,
}

// IsPreAuth indica los mensajes admitidos antes de completar el handshake.
func IsPreAuth(t string) bool { return t == TypeSessionHello }

// ─────────────────────────────────────────────────────────────
// Envelopes
// ─────────────────────────────────────────────────────────────

// Inbound es el envelope cliente → servidor.
//
// El payload se deja como RawMessage para decodificarlo sólo después de validar
// versión, tipo y autorización: no se gasta trabajo en mensajes que van a rechazarse.
type Inbound struct {
	V         int             `json:"v"`
	Type      string          `json:"type"`
	RequestID string          `json:"requestId"`
	Payload   json.RawMessage `json:"payload"`
}

// Outbound es el envelope servidor → cliente.
type Outbound struct {
	V         int    `json:"v"`
	Type      string `json:"type"`
	Seq       uint64 `json:"seq"`
	TS        int64  `json:"ts"`
	RequestID string `json:"requestId,omitempty"`
	Payload   any    `json:"payload"`
}

// NewOutbound construye un mensaje saliente. `seq` y `ts` los asigna la sesión.
func NewOutbound(msgType string, seq uint64, tsMs int64, requestID string, payload any) Outbound {
	return Outbound{V: Version, Type: msgType, Seq: seq, TS: tsMs, RequestID: requestID, Payload: payload}
}

// ─────────────────────────────────────────────────────────────
// Estructuras compartidas
// ─────────────────────────────────────────────────────────────

// Tile es una coordenada lógica de mundo. Nunca de pantalla.
type Tile struct {
	X int32 `json:"x"`
	Y int32 `json:"y"`
}

// Waypoint es un punto de la polilínea temporizada.
type Waypoint struct {
	X   int32 `json:"x"`
	Y   int32 `json:"y"`
	TMs int64 `json:"tMs"`
}

// ChunkRef identifica un chunk suscrito.
type ChunkRef struct {
	CX int32 `json:"cx"`
	CY int32 `json:"cy"`
}

// ChunkTerrain transporta el terreno de un chunk como base64 de size*size bytes.
type ChunkTerrain struct {
	CX      int32  `json:"cx"`
	CY      int32  `json:"cy"`
	Size    int32  `json:"size"`
	Terrain string `json:"terrain"`
}

// ActiveMovement es el movimiento vigente de una unidad, tal y como lo ve el cliente.
//
// Se envía la polilínea COMPLETA: con ella el cliente interpola su posición visual
// en cada frame sin sondear al servidor, y sin poder alterar la verdad autoritativa.
type ActiveMovement struct {
	MovementID    int64      `json:"movementId"`
	Path          []Waypoint `json:"path"`
	StartTimeMs   int64      `json:"startTimeMs"`
	ArrivalTimeMs int64      `json:"arrivalTimeMs"`
	Target        Tile       `json:"target"`
}

// UnitView es el estado observable de una unidad.
type UnitView struct {
	ID       int64           `json:"id"`
	PlayerID string          `json:"playerId"`
	CityID   *int64          `json:"cityId"`
	UnitType string          `json:"unitType"`
	X        int32           `json:"x"`
	Y        int32           `json:"y"`
	HP       int32           `json:"hp"`
	MaxHP    int32           `json:"maxHp"`
	Status   string          `json:"status"`
	Movement *ActiveMovement `json:"movement"`
}

// CityView es el estado observable de una ciudad.
type CityView struct {
	ID                int64  `json:"id"`
	OwnerPlayerID     string `json:"ownerPlayerId"`
	Name              string `json:"name"`
	CenterX           int32  `json:"centerX"`
	CenterY           int32  `json:"centerY"`
	Era               string `json:"era"`
	Population        int32  `json:"population"`
	PopulationLimit   int32  `json:"populationLimit"`
	PresenceState     string `json:"presenceState"`
	ProtectionUntilMs *int64 `json:"protectionUntilMs"`
}

// TerritoryView es el estado observable de un territorio.
type TerritoryView struct {
	ID        int64   `json:"id"`
	Name      string  `json:"name"`
	MinX      int32   `json:"minX"`
	MinY      int32   `json:"minY"`
	MaxX      int32   `json:"maxX"`
	MaxY      int32   `json:"maxY"`
	OwnerType string  `json:"ownerType"`
	OwnerID   *string `json:"ownerId"`
	Contested bool    `json:"contested"`
}

// ─────────────────────────────────────────────────────────────
// Payloads cliente → servidor
// ─────────────────────────────────────────────────────────────

type SessionHelloPayload struct {
	Ticket        string `json:"ticket"`
	ClientVersion string `json:"clientVersion,omitempty"`
}

type SessionPingPayload struct {
	ClientTimeMs int64 `json:"clientTimeMs"`
}

type SessionViewPayload struct {
	Center Tile `json:"center"`
}

// UnitMovePayload sólo transporta el DESTINO. Que aquí no exista un campo para la
// ruta no es un descuido: es el contrato. El pathfinding es del servidor.
type UnitMovePayload struct {
	UnitID int64 `json:"unitId"`
	Target Tile  `json:"target"`
}

type UnitCancelMovePayload struct {
	UnitID int64 `json:"unitId"`
}

// ─────────────────────────────────────────────────────────────
// Payloads servidor → cliente
// ─────────────────────────────────────────────────────────────

type WorldInfo struct {
	Width     int32 `json:"width"`
	Height    int32 `json:"height"`
	ChunkSize int32 `json:"chunkSize"`
}

type SessionWelcomePayload struct {
	SessionID           string    `json:"sessionId"`
	PlayerID            string    `json:"playerId"`
	ServerTimeMs        int64     `json:"serverTimeMs"`
	TickDurationMs      int64     `json:"tickDurationMs"`
	HeartbeatIntervalMs int64     `json:"heartbeatIntervalMs"`
	World               WorldInfo `json:"world"`
}

type SessionPongPayload struct {
	ClientTimeMs int64 `json:"clientTimeMs"`
	ServerTimeMs int64 `json:"serverTimeMs"`
}

type SystemErrorPayload struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

type WorldSnapshotPayload struct {
	ServerTimeMs int64           `json:"serverTimeMs"`
	Tick         uint64          `json:"tick"`
	Chunks       []ChunkRef      `json:"chunks"`
	Terrain      []ChunkTerrain  `json:"terrain"`
	Units        []UnitView      `json:"units"`
	Cities       []CityView      `json:"cities"`
	Territories  []TerritoryView `json:"territories"`
}

type EntitySpawnPayload struct {
	Unit UnitView `json:"unit"`
}

// EntityUpdatePayload transporta sólo lo que cambió. Los punteros distinguen
// "sin cambios" de "cambió a cero", que no son lo mismo.
type EntityUpdatePayload struct {
	ID       int64           `json:"id"`
	X        *int32          `json:"x,omitempty"`
	Y        *int32          `json:"y,omitempty"`
	HP       *int32          `json:"hp,omitempty"`
	Status   *string         `json:"status,omitempty"`
	CityID   **int64         `json:"cityId,omitempty"`
	Movement *ActiveMovement `json:"movement,omitempty"`
}

type EntityDespawnPayload struct {
	ID     int64  `json:"id"`
	Reason string `json:"reason"`
}

type CityUpdatePayload struct {
	ID                int64   `json:"id"`
	Name              *string `json:"name,omitempty"`
	Population        *int32  `json:"population,omitempty"`
	PopulationLimit   *int32  `json:"populationLimit,omitempty"`
	PresenceState     *string `json:"presenceState,omitempty"`
	ProtectionUntilMs *int64  `json:"protectionUntilMs,omitempty"`
}

type TerritoryUpdatePayload struct {
	Territory TerritoryView `json:"territory"`
}

type UnitMoveAcceptedPayload struct {
	UnitID     int64 `json:"unitId"`
	MovementID int64 `json:"movementId"`
}

type UnitMoveRejectedPayload struct {
	UnitID  int64  `json:"unitId"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type UnitMovementStartedPayload struct {
	UnitID   int64          `json:"unitId"`
	Movement ActiveMovement `json:"movement"`
}

type UnitMovementCompletedPayload struct {
	UnitID        int64 `json:"unitId"`
	MovementID    int64 `json:"movementId"`
	FinalPosition Tile  `json:"finalPosition"`
}

type UnitMovementCancelledPayload struct {
	UnitID     int64  `json:"unitId"`
	MovementID int64  `json:"movementId"`
	StoppedAt  Tile   `json:"stoppedAt"`
	Reason     string `json:"reason"`
}

// Razones de despawn.
const (
	DespawnOutOfInterest = "OUT_OF_INTEREST"
	DespawnDead          = "DEAD"
	DespawnGarrisoned    = "GARRISONED"
	DespawnHidden        = "HIDDEN"
	DespawnRemoved       = "REMOVED"
)
