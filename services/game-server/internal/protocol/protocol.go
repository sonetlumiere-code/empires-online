// Package protocol define el contrato WebSocket del lado servidor.
//
// La FUENTE DE VERDAD del contrato es packages/protocol (Zod). Este paquete lo
// implementa en Go y embebe el JSON Schema exportado desde allí para que los
// contract tests detecten cualquier deriva antes de que llegue a producción.
// Ver ../../../../docs/decisions/ADR-009-shared-protocol-package.md
package protocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

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
	TypeUnitGarrison   = "unit.garrison"
	TypeUnitUngarrison = "unit.ungarrison"
	// Economía (SPEC-ECON-WOOD).
	TypeUnitGather = "unit.gather"
	TypeCityBuild  = "city.build"
	TypeCityTrain  = "city.train"
	// Avance de era (SPEC-ECON-STONE).
	TypeCityAdvance = "city.advance"
	// Elaboración del taller (SPEC-CIVILIZATION-GOODS).
	TypeCityCraft = "city.craft"
	// Combate (SPEC-COMBAT-RAID).
	TypeUnitAttack = "unit.attack"
	// Saqueo (SPEC-COMBAT-PILLAGE).
	TypeUnitPillage = "unit.pillage"
	// Asedio (SPEC-COMBAT-SIEGE).
	TypeUnitDemolish = "unit.demolish"
	// Conquista (SPEC-TERRITORY-CONQUEST).
	TypeUnitClaim = "unit.claim"
	// Diplomacia (SPEC-DIPLOMACY).
	TypeTreatyPropose = "treaty.propose"
	TypeTreatyRespond = "treaty.respond"
	TypeTreatyCancel  = "treaty.cancel"
	// Comercio (SPEC-TRADE).
	TypeTradePropose = "trade.propose"
	TypeTradeRespond = "trade.respond"
	TypeTradeCancel  = "trade.cancel"
	// Registro (SPEC-EVENT-LOG): los atiende la conexión, no la simulación.
	TypeEventLogFetch = "event_log.fetch"
	TypeEventLogSeen  = "event_log.seen"
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
	TypeBuildingSpawn         = "building.spawn"
	TypeBuildingUpdate        = "building.update"
	TypeBuildingDestroyed     = "building.destroyed"
	TypeForestUpdate          = "forest.update"
	TypeHillUpdate            = "hill.update"
	TypeTreatyUpdate          = "treaty.update"
	TypeEventLogPage          = "event_log.page"
	TypeTradeUpdate           = "trade.update"
	TypeEventLogNotice        = "event_log.notice"
)

// ClientMessageTypes es el catálogo cerrado de comandos admitidos.
var ClientMessageTypes = map[string]bool{
	TypeSessionHello:   true,
	TypeSessionPing:    true,
	TypeSessionView:    true,
	TypeUnitMove:       true,
	TypeUnitCancelMove: true,
	TypeUnitGarrison:   true,
	TypeUnitUngarrison: true,
	TypeUnitGather:     true,
	TypeCityBuild:      true,
	TypeCityTrain:      true,
	TypeCityAdvance:    true,
	TypeCityCraft:      true,
	TypeUnitAttack:     true,
	TypeUnitPillage:    true,
	TypeUnitDemolish:   true,
	TypeUnitClaim:      true,
	TypeTreatyPropose:  true,
	TypeTreatyRespond:  true,
	TypeTreatyCancel:   true,
	TypeTradePropose:   true,
	TypeTradeRespond:   true,
	TypeTradeCancel:    true,
	TypeEventLogFetch:  true,
	TypeEventLogSeen:   true,
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

// DecodeStrict decodifica un mensaje cliente → servidor —el envelope o su
// payload— rechazando toda propiedad que el contrato no declare, a cualquier
// profundidad, y cualquier dato tras el valor JSON.
//
// Es la mitad Go de S-7 (docs/specs/websocket-protocol.md): los esquemas
// cliente → servidor son estrictos (`additionalProperties: false`). Un
// `json.Unmarshal` corriente descartaría en silencio un `path` o un `hp`
// colados en `unit.move`; descartarlos no rompe hoy nada, porque ningún struct
// tiene dónde guardarlos, pero un refactor que añadiera el campo los aceptaría
// sin que nadie lo decidiera (INV-SEC-001). Los mensajes servidor → cliente no
// pasan por aquí: hacia fuera el contrato es tolerante a propósito.
func DecodeStrict(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		return errors.New("datos tras el valor JSON")
	}
	return nil
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
	// GarrisonCityID es la ciudad que aloja a la unidad, o nil fuera de una
	// guarnición. CityID es siempre la de origen (ADR-017 §2.4).
	GarrisonCityID *int64 `json:"garrisonCityId"`
	// Carrying es lo que lleva encima una unidad que recolecta; ausente si nada.
	Carrying int32 `json:"carrying,omitempty"`
	// CarryingResource es el recurso de Carrying cuando lo lleva una tarea de
	// recolección (WOOD, STONE); ausente si no (SPEC-ECON-STONE §6).
	CarryingResource string `json:"carryingResource,omitempty"`
}

// Stocks es el stock de una ciudad, por recurso (SPEC-ECON-WOOD §6).
type Stocks struct {
	Wood int64 `json:"WOOD"`
	// Stone es la piedra (SPEC-ECON-STONE). Opcional en el esquema para que
	// un cliente nuevo lea un servidor anterior; este servidor siempre la manda.
	Stone int64 `json:"STONE"`
	// Los bienes de civilización (SPEC-CIVILIZATION-GOODS). Opcionales en el
	// esquema, como la piedra; este servidor siempre los manda.
	Wine   int64 `json:"WINE"`
	Silk   int64 `json:"SILK"`
	Spices int64 `json:"SPICES"`
	Furs   int64 `json:"FURS"`
}

// CraftView es la elaboración en curso del taller de una ciudad: qué bien,
// cuánto y cuándo se entrega (SPEC-CIVILIZATION-GOODS §4.3).
type CraftView struct {
	Good          string `json:"good"`
	Amount        int32  `json:"amount"`
	CompletesAtMs int64  `json:"completesAtMs"`
}

// ProductionView es el entrenamiento en curso de una ciudad.
type ProductionView struct {
	UnitType      string `json:"unitType"`
	CompletesAtMs int64  `json:"completesAtMs"`
}

// AdvanceView es el avance de era en curso de una ciudad: a qué era va y
// cuándo llega (SPEC-ECON-STONE §6).
type AdvanceView struct {
	Era           string `json:"era"`
	CompletesAtMs int64  `json:"completesAtMs"`
}

// BuildingView es el estado observable de un edificio.
type BuildingView struct {
	ID            int64  `json:"id"`
	CityID        int64  `json:"cityId"`
	Type          string `json:"type"`
	X             int32  `json:"x"`
	Y             int32  `json:"y"`
	Status        string `json:"status"`
	CompletesAtMs int64  `json:"completesAtMs"`
	// HP y MaxHP son su vida frente a un asedio (SPEC-COMBAT-SIEGE §4.2).
	HP    int32 `json:"hp"`
	MaxHP int32 `json:"maxHp"`
}

// CityView es el estado observable de una ciudad.
type CityView struct {
	ID            int64  `json:"id"`
	OwnerPlayerID string `json:"ownerPlayerId"`
	Name          string `json:"name"`
	CenterX       int32  `json:"centerX"`
	CenterY       int32  `json:"centerY"`
	Era           string `json:"era"`
	// Civilization es el código de la civilización de su dueño, pública para
	// todo el que ve la ciudad (SPEC-CIVILIZATION RN-CIV-009).
	Civilization      string `json:"civilization"`
	Population        int32  `json:"population"`
	PopulationLimit   int32  `json:"populationLimit"`
	PresenceState     string `json:"presenceState"`
	ProtectionUntilMs *int64 `json:"protectionUntilMs"`
	// Stocks y Production viajan siempre: el stock de la ciudad y su
	// entrenamiento en curso, o null.
	Stocks     Stocks          `json:"stocks"`
	Production *ProductionView `json:"production"`
	// Advance es el avance de era en curso, o null (SPEC-ECON-STONE §6).
	Advance *AdvanceView `json:"advance"`
	// Craft es la elaboración en curso del taller, o null
	// (SPEC-CIVILIZATION-GOODS §6).
	Craft *CraftView `json:"craft"`
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
	// Contested es true exactamente mientras hay una reclamación activa
	// (SPEC-TERRITORY-CONQUEST §6, INV-TERR-006).
	Contested bool `json:"contested"`
	// Claim es la reclamación activa, si la hay.
	Claim *TerritoryClaimView `json:"claim,omitempty"`
}

// TerritoryClaimView es una reclamación activa: quién reclama y cuándo termina.
type TerritoryClaimView struct {
	PlayerID    string  `json:"playerId"`
	CityID      *int64  `json:"cityId"`
	CityName    *string `json:"cityName"`
	StartedAtMs int64   `json:"startedAtMs"`
	EndsAtMs    int64   `json:"endsAtMs"`
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

// UnitGarrisonPayload pide guarnecer una unidad en una ciudad (ADR-017).
type UnitGarrisonPayload struct {
	UnitID int64 `json:"unitId"`
	CityID int64 `json:"cityId"`
}

// UnitUngarrisonPayload pide sacar una unidad de su guarnición. El tile de
// salida lo elige el servidor.
type UnitUngarrisonPayload struct {
	UnitID int64 `json:"unitId"`
}

// UnitGatherPayload pone a un aldeano a recolectar en un tile de bosque. Sólo
// el destino: la ruta, los tiempos y las cantidades son del servidor.
type UnitGatherPayload struct {
	UnitID int64 `json:"unitId"`
	Target Tile  `json:"target"`
}

// UnitAttackPayload ordena a una unidad militar propia atacar a una unidad
// ajena. Sólo quién y a quién: la ruta, el daño y el botín son del servidor.
type UnitAttackPayload struct {
	UnitID       int64 `json:"unitId"`
	TargetUnitID int64 `json:"targetUnitId"`
}

// UnitPillagePayload ordena a un guerrero propio saquear una ciudad ajena.
// Sólo quién y qué ciudad: el tile de llegada, lo robado y el suelo son del
// servidor (SPEC-COMBAT-PILLAGE).
type UnitPillagePayload struct {
	UnitID int64 `json:"unitId"`
	CityID int64 `json:"cityId"`
}

// UnitDemolishPayload ordena a un guerrero propio derribar una casa ajena.
// Sólo quién y qué casa: el tile de llegada, la vida y el daño son del
// servidor (SPEC-COMBAT-SIEGE).
type UnitDemolishPayload struct {
	UnitID     int64 `json:"unitId"`
	BuildingID int64 `json:"buildingId"`
}

// UnitClaimPayload ordena a un guerrero propio reclamar el territorio en que
// está. Sólo qué guerrero: el territorio, el plazo y si se puede son del
// servidor (SPEC-TERRITORY-CONQUEST §6).
type UnitClaimPayload struct {
	UnitID int64 `json:"unitId"`
}

// TreatyProposePayload propone un tratado a otro jugador (SPEC-DIPLOMACY).
type TreatyProposePayload struct {
	PlayerID string `json:"playerId"`
	Type     string `json:"type"`
}

// TreatyRespondPayload acepta o rechaza una oferta recibida.
type TreatyRespondPayload struct {
	TreatyID int64 `json:"treatyId"`
	Accept   bool  `json:"accept"`
}

// TreatyCancelPayload retira la oferta propia o rompe un tratado vigente.
type TreatyCancelPayload struct {
	TreatyID int64 `json:"treatyId"`
}

// TreatyView es un tratado visto por uno de sus firmantes: «otro» es relativo
// a quien lo recibe (SPEC-DIPLOMACY §6).
type TreatyView struct {
	ID            int64   `json:"id"`
	OtherPlayerID string  `json:"otherPlayerId"`
	OtherCityID   *int64  `json:"otherCityId"`
	OtherCityName *string `json:"otherCityName"`
	Type          string  `json:"type"`
	Status        string  `json:"status"`
	ProposedByMe  bool    `json:"proposedByMe"`
	BreaksAtMs    *int64  `json:"breaksAtMs"`
}

// TreatyUpdatePayload lleva un tratado que cambió a uno de sus firmantes.
type TreatyUpdatePayload struct {
	Treaty TreatyView `json:"treaty"`
}

// ResourceAmount es una línea de una oferta de comercio: un recurso y una
// cantidad (SPEC-TRADE §4.2).
type ResourceAmount struct {
	Resource string `json:"resource"`
	Amount   int64  `json:"amount"`
}

// TradeProposePayload ofrece un trato a otro jugador (SPEC-TRADE §6).
type TradeProposePayload struct {
	PlayerID string           `json:"playerId"`
	Give     []ResourceAmount `json:"give"`
	Want     []ResourceAmount `json:"want"`
}

// TradeRespondPayload acepta o rechaza una oferta recibida.
type TradeRespondPayload struct {
	OfferID int64 `json:"offerId"`
	Accept  bool  `json:"accept"`
}

// TradeCancelPayload retira la oferta propia.
type TradeCancelPayload struct {
	OfferID int64 `json:"offerId"`
}

// TradeOfferView es una oferta vista por uno de sus dos jugadores: Give es lo
// que saldría de su almacén y Get lo que entraría (SPEC-TRADE §6).
type TradeOfferView struct {
	ID            int64            `json:"id"`
	OtherPlayerID string           `json:"otherPlayerId"`
	OtherCityID   *int64           `json:"otherCityId"`
	OtherCityName *string          `json:"otherCityName"`
	Give          []ResourceAmount `json:"give"`
	Get           []ResourceAmount `json:"get"`
	Status        string           `json:"status"`
	ProposedByMe  bool             `json:"proposedByMe"`
	ExpiresAtMs   int64            `json:"expiresAtMs"`
}

// TradeUpdatePayload lleva una oferta que cambió a uno de sus dos jugadores.
type TradeUpdatePayload struct {
	Offer TradeOfferView `json:"offer"`
}

// EventLogFetchPayload pide una página del registro (SPEC-EVENT-LOG): la más
// reciente, o la de los eventos con id menor que BeforeID.
type EventLogFetchPayload struct {
	BeforeID *int64 `json:"beforeId,omitempty"`
}

// EventLogSeenPayload da por visto el registro hasta UpToID (RN-LOG-004).
type EventLogSeenPayload struct {
	UpToID int64 `json:"upToId"`
}

// EventLogEntry es una entrada del registro vista por quien la recibe
// (SPEC-EVENT-LOG §6). Los campos opcionales sólo van en los tipos que los usan.
type EventLogEntry struct {
	ID            int64   `json:"id"`
	Kind          string  `json:"kind"`
	FirstAtMs     int64   `json:"firstAtMs"`
	LastAtMs      int64   `json:"lastAtMs"`
	Count         int     `json:"count"`
	OtherPlayerID string  `json:"otherPlayerId"`
	OtherCityID   *int64  `json:"otherCityId"`
	OtherCityName *string `json:"otherCityName"`
	UnitType      string  `json:"unitType,omitempty"`
	Amount        *int64  `json:"amount,omitempty"`
	TreatyType    string  `json:"treatyType,omitempty"`
	ProposedByMe  *bool   `json:"proposedByMe,omitempty"`
	X             *int32  `json:"x,omitempty"`
	Y             *int32  `json:"y,omitempty"`
	BreaksAtMs    *int64  `json:"breaksAtMs,omitempty"`
	// TradeGive y TradeGet, en los tratos: lo que la oferta sacaba del almacén
	// de quien lee y lo que metía en él (SPEC-TRADE §6).
	TradeGive []ResourceAmount `json:"tradeGive,omitempty"`
	TradeGet  []ResourceAmount `json:"tradeGet,omitempty"`
	// TerritoryID y TerritoryName, en la conquista (SPEC-TERRITORY-CONQUEST §6).
	TerritoryID   *int64 `json:"territoryId,omitempty"`
	TerritoryName string `json:"territoryName,omitempty"`
}

// EventLogNoticePayload es un aviso en vivo (SPEC-LIVE-NOTICES §6): un suceso
// que les pasó a las unidades del jugador, en el tick en que pasó. No es una
// entrada del registro: no lleva id ni recuento (RN-NOTICE-004, ADR-033 §1).
// Los campos opcionales sólo van en los tipos que los usan.
type EventLogNoticePayload struct {
	Kind          string  `json:"kind"`
	AtMs          int64   `json:"atMs"`
	OtherPlayerID string  `json:"otherPlayerId"`
	OtherCityID   *int64  `json:"otherCityId"`
	OtherCityName *string `json:"otherCityName"`
	UnitType      string  `json:"unitType,omitempty"`
	Amount        *int64  `json:"amount,omitempty"`
	X             *int32  `json:"x,omitempty"`
	Y             *int32  `json:"y,omitempty"`
	// TerritoryID y TerritoryName, en la conquista (SPEC-TERRITORY-CONQUEST §6).
	TerritoryID   *int64 `json:"territoryId,omitempty"`
	TerritoryName string `json:"territoryName,omitempty"`
}

// EventLogPagePayload es una página del registro. NextBeforeID es nil en la
// última.
type EventLogPagePayload struct {
	Entries      []EventLogEntry `json:"entries"`
	SeenUpToID   int64           `json:"seenUpToId"`
	NextBeforeID *int64          `json:"nextBeforeId"`
}

// CityBuildPayload encarga un edificio en un tile de la zona urbana.
type CityBuildPayload struct {
	CityID int64  `json:"cityId"`
	Type   string `json:"type"`
	X      int32  `json:"x"`
	Y      int32  `json:"y"`
}

// CityTrainPayload encarga el entrenamiento de una unidad.
type CityTrainPayload struct {
	CityID int64  `json:"cityId"`
	Type   string `json:"type"`
}

// CityAdvancePayload encarga el avance de una ciudad a la era siguiente. Sólo
// nombra la ciudad: la era, el coste y el tiempo son del servidor
// (SPEC-ECON-STONE RN-ECON-024, INV-SEC-001).
type CityAdvancePayload struct {
	CityID int64 `json:"cityId"`
}

// CityCraftPayload encarga al taller de una ciudad una elaboración. Sólo
// nombra la ciudad: el bien, la cantidad, el coste y el tiempo son del
// servidor (SPEC-CIVILIZATION-GOODS RN-GOODS-004, INV-SEC-001).
type CityCraftPayload struct {
	CityID int64 `json:"cityId"`
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
	Buildings    []BuildingView  `json:"buildings"`
	// Forests son los tiles de bosque del área que no están llenos
	// (SPEC-ECON-FOREST). Opcional en el esquema: se omite si no hay ninguno.
	Forests []ForestView `json:"forests,omitempty"`
	// Hills son los tiles de colina del área que no están llenos
	// (SPEC-ECON-STONE). Opcional en el esquema, como Forests.
	Hills []HillView `json:"hills,omitempty"`
	// Treaties son los tratados PROPOSED y ACTIVE de quien recibe el snapshot
	// (SPEC-DIPLOMACY, ADR-027 §5). Se omite si no tiene ninguno.
	Treaties []TreatyView `json:"treaties,omitempty"`
	// TradeOffers son las ofertas de comercio PROPOSED de quien recibe el
	// snapshot (SPEC-TRADE §6). Se omite si no tiene ninguna.
	TradeOffers []TradeOfferView `json:"tradeOffers,omitempty"`
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
	// GarrisonCityID: nil si no cambia; apuntando a nil, la unidad sale de la
	// guarnición; apuntando a un id, entra en esa ciudad.
	GarrisonCityID **int64 `json:"garrisonCityId,omitempty"`
	// Carrying: nil si no cambia; 0 al depositar o al perder la carga.
	Carrying *int32 `json:"carrying,omitempty"`
	// CarryingResource acompaña a una carga de recolección (SPEC-ECON-STONE §6).
	CarryingResource string `json:"carryingResource,omitempty"`
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
	// Stocks: nil si no cambia.
	Stocks *Stocks `json:"stocks,omitempty"`
	// Production: nil si no cambia; apuntando a nil, la ciudad deja de
	// entrenar; apuntando a una vista, empieza.
	Production **ProductionView `json:"production,omitempty"`
	// Era es la era nueva de la ciudad al terminar un avance (SPEC-ECON-STONE).
	Era *string `json:"era,omitempty"`
	// Advance: nil si no cambia; apuntando a nil, el avance terminó o se
	// deshizo; apuntando a una vista, empieza.
	Advance **AdvanceView `json:"advance,omitempty"`
	// Craft: nil si no cambia; apuntando a nil, la elaboración terminó o se
	// perdió; apuntando a una vista, empieza (SPEC-CIVILIZATION-GOODS §6).
	Craft **CraftView `json:"craft,omitempty"`
	// PillagedAtMs es el instante del último golpe de saqueo. Sólo lo recibe
	// el dueño de la ciudad (SPEC-COMBAT-PILLAGE RN-COMBAT-028).
	PillagedAtMs *int64 `json:"pillagedAtMs,omitempty"`
	// BesiegedAtMs es el instante del último golpe de asedio a una casa de la
	// ciudad. Sólo lo recibe su dueño (SPEC-COMBAT-SIEGE RN-COMBAT-038).
	BesiegedAtMs *int64 `json:"besiegedAtMs,omitempty"`
}

// BuildingSpawnPayload anuncia un edificio.
type BuildingSpawnPayload struct {
	Building BuildingView `json:"building"`
}

// ForestView es la madera de un tile de bosque que no está lleno, y el
// payload de forest.update. RegrowsAtMs nil viaja como null: el tile está
// lleno (SPEC-ECON-FOREST §6).
type ForestView struct {
	X           int32  `json:"x"`
	Y           int32  `json:"y"`
	Remaining   int32  `json:"remaining"`
	RegrowsAtMs *int64 `json:"regrowsAtMs"`
}

// HillView es la piedra de un tile de colina que no está lleno, y el payload de
// hill.update: la misma forma que ForestView (SPEC-ECON-STONE §6).
type HillView = ForestView

// BuildingUpdatePayload es el cambio de estado de un edificio conocido.
type BuildingUpdatePayload struct {
	ID     int64  `json:"id"`
	Status string `json:"status"`
	// HP es su vida tras un golpe de asedio, o al olvidarse el daño.
	HP *int32 `json:"hp,omitempty"`
}

// BuildingDestroyedPayload anuncia un edificio derribado: el cliente lo quita
// (SPEC-COMBAT-SIEGE §6).
type BuildingDestroyedPayload struct {
	ID     int64 `json:"id"`
	CityID int64 `json:"cityId"`
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
