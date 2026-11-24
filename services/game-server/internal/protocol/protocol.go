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
	// Investigación (SPEC-TECHNOLOGIES).
	TypeCityResearch = "city.research"
	// Combate (SPEC-COMBAT-RAID).
	TypeUnitAttack = "unit.attack"
	// Saqueo (SPEC-COMBAT-PILLAGE).
	TypeUnitPillage = "unit.pillage"
	// Asedio (SPEC-COMBAT-SIEGE).
	TypeUnitDemolish = "unit.demolish"
	// Conquista (SPEC-TERRITORY-CONQUEST).
	TypeUnitClaim = "unit.claim"
	// La caída de una ciudad (SPEC-CITY-FALL).
	TypeUnitAssault = "unit.assault"
	// TypeUnitFoundCity funda una segunda ciudad con un aldeano (SPEC-MULTI-CITY).
	TypeUnitFoundCity = "unit.found_city"
	// Diplomacia (SPEC-DIPLOMACY).
	TypeTreatyPropose = "treaty.propose"
	TypeTreatyRespond = "treaty.respond"
	TypeTreatyCancel  = "treaty.cancel"
	// Comercio (SPEC-TRADE).
	TypeTradePropose = "trade.propose"
	TypeTradeRespond = "trade.respond"
	TypeTradeCancel  = "trade.cancel"
	// Mercado (SPEC-MARKETS): las tres órdenes pasan por la simulación; el
	// tablón lo lee la conexión de la base (ADR-047 §6).
	TypeMarketPost   = "market.post"
	TypeMarketBuy    = "market.buy"
	TypeMarketCancel = "market.cancel"
	TypeMarketList   = "market.list"
	// Facciones (SPEC-FACTIONS).
	TypeFactionJoin = "faction.join"
	// Registro (SPEC-EVENT-LOG): los atiende la conexión, no la simulación.
	TypeEventLogFetch = "event_log.fetch"
	TypeEventLogSeen  = "event_log.seen"
	// Mensajes entre jugadores (SPEC-MESSAGES): los atiende la conexión, no la
	// simulación (ADR-038 §1).
	TypeMessageSend  = "message.send"
	TypeMessageList  = "message.list"
	TypeMessageFetch = "message.fetch"
	TypeMessageRead  = "message.read"
	TypeMessageBlock = "message.block"
	// Clanes (SPEC-CLANS): los cinco primeros cambian quién está en un clan y
	// pasan por la simulación; los cuatro últimos los atiende la conexión
	// (ADR-040 §1 y §5).
	TypeClanCreate    = "clan.create"
	TypeClanInvite    = "clan.invite"
	TypeClanRespond   = "clan.respond"
	TypeClanLeave     = "clan.leave"
	TypeClanExpel     = "clan.expel"
	TypeClanLookup    = "clan.lookup"
	TypeClanChatSend  = "clan.chat.send"
	TypeClanChatFetch = "clan.chat.fetch"
	TypeClanChatRead  = "clan.chat.read"
	// Gobierno del clan (SPEC-CLAN-GOVERNANCE §6): pasan por la simulación,
	// como los rangos y el líder que cambian.
	TypeClanPromote  = "clan.promote"
	TypeClanTransfer = "clan.transfer"
	// Alianzas entre clanes (SPEC-CLAN-ALLIANCES §6): pasan por la simulación,
	// como los tratados.
	TypeClanAlliancePropose = "clan.alliance.propose"
	TypeClanAllianceRespond = "clan.alliance.respond"
	TypeClanAllianceCancel  = "clan.alliance.cancel"
	// Guerras entre clanes (SPEC-CLAN-WARS §6): pasan por la simulación, como
	// las alianzas.
	TypeClanWarDeclare   = "clan.war.declare"
	TypeClanWarPeace     = "clan.war.peace"
	TypeClanWarSurrender = "clan.war.surrender"
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
	TypeCaravanUpdate         = "caravan.update"
	TypeMessageNew            = "message.new"
	TypeMessageInbox          = "message.inbox"
	TypeMessagePage           = "message.page"
	TypeClanUpdate            = "clan.update"
	TypeClanInfo              = "clan.info"
	TypeClanChatNew           = "clan.chat.new"
	TypeClanChatPage          = "clan.chat.page"
	TypeMarketUpdate          = "market.update"
	TypeMarketListing         = "market.listing"
	TypeCityFallen            = "city.fallen"
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
	TypeCityResearch:   true,
	TypeUnitAttack:     true,
	TypeUnitPillage:    true,
	TypeUnitDemolish:   true,
	TypeUnitClaim:      true,
	TypeUnitAssault:    true,
	TypeUnitFoundCity:  true,
	TypeTreatyPropose:  true,
	TypeTreatyRespond:  true,
	TypeTreatyCancel:   true,
	TypeTradePropose:   true,
	TypeTradeRespond:   true,
	TypeTradeCancel:    true,
	TypeFactionJoin:    true,
	TypeEventLogFetch:  true,
	TypeEventLogSeen:   true,
	TypeMessageSend:    true,
	TypeMessageList:    true,
	TypeMessageFetch:   true,
	TypeMessageRead:    true,
	TypeMessageBlock:   true,
	TypeClanCreate:     true,
	TypeClanInvite:     true,
	TypeClanRespond:    true,
	TypeClanLeave:      true,
	TypeClanExpel:      true,
	TypeClanLookup:     true,
	TypeClanChatSend:   true,
	TypeClanChatFetch:  true,
	TypeClanChatRead:   true,
	TypeClanPromote:    true,
	TypeClanTransfer:   true,

	TypeClanAlliancePropose: true,
	TypeClanAllianceRespond: true,
	TypeClanAllianceCancel:  true,

	TypeClanWarDeclare:   true,
	TypeClanWarPeace:     true,
	TypeClanWarSurrender: true,

	TypeMarketPost:   true,
	TypeMarketBuy:    true,
	TypeMarketCancel: true,
	TypeMarketList:   true,
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

// FactionJoinView es la entrada de un jugador en un bando, que cuenta en AtMs
// (SPEC-FACTIONS §6).
type FactionJoinView struct {
	Faction string `json:"faction"`
	AtMs    int64  `json:"atMs"`
}

// AdvanceView es el avance de era en curso de una ciudad: a qué era va y
// cuándo llega (SPEC-ECON-STONE §6).
type AdvanceView struct {
	Era           string `json:"era"`
	CompletesAtMs int64  `json:"completesAtMs"`
}

// ResearchView es la investigación en curso de una ciudad: qué tecnología y
// cuándo termina (SPEC-TECHNOLOGIES §6).
type ResearchView struct {
	Technology    string `json:"technology"`
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
	// TargetUnitID es la unidad a la que apunta un castillo, si apunta a alguna
	// (SPEC-CASTLE-AGE §6).
	TargetUnitID *int64 `json:"targetUnitId,omitempty"`
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
	Civilization string `json:"civilization"`
	// Faction es el código de la facción de su dueño y FactionJoin su entrada
	// pendiente en un bando, o null; públicas para todo el que ve la ciudad
	// (SPEC-FACTIONS RN-FAC-009).
	Faction     string           `json:"faction"`
	FactionJoin *FactionJoinView `json:"factionJoin"`
	// Clan es el clan de su dueño (su id y su sigla), o null; público
	// (SPEC-CLANS RN-CLAN-008).
	Clan              *CityClanView `json:"clan"`
	Population        int32         `json:"population"`
	PopulationLimit   int32         `json:"populationLimit"`
	PresenceState     string        `json:"presenceState"`
	ProtectionUntilMs *int64        `json:"protectionUntilMs"`
	// Stocks y Production viajan siempre: el stock de la ciudad y su
	// entrenamiento en curso, o null.
	Stocks     Stocks          `json:"stocks"`
	Production *ProductionView `json:"production"`
	// Advance es el avance de era en curso, o null (SPEC-ECON-STONE §6).
	Advance *AdvanceView `json:"advance"`
	// Craft es la elaboración en curso del taller, o null
	// (SPEC-CIVILIZATION-GOODS §6).
	Craft *CraftView `json:"craft"`
	// Research y Technologies sólo viajan en la vista del dueño: la
	// investigación en curso (apuntando a nil, ninguna) y las tecnologías del
	// jugador; nil, no viajan (SPEC-TECHNOLOGIES RN-TECH-008).
	Research     **ResearchView `json:"research,omitempty"`
	Technologies *[]string      `json:"technologies,omitempty"`
	// Assault es el asalto activo sobre la ciudad, si lo hay; público
	// (SPEC-CITY-FALL §6).
	Assault *CityAssaultView `json:"assault,omitempty"`
	// FallImmuneUntilMs es hasta cuándo no se la puede asaltar, mientras dure
	// su inmunidad (RN-CITY-029).
	FallImmuneUntilMs *int64 `json:"fallImmuneUntilMs,omitempty"`
}

// CityAssaultView es un asalto activo sobre una ciudad: quién lo sostiene, con
// qué unidad, y cuándo empezó y cae la ciudad (SPEC-CITY-FALL §6).
type CityAssaultView struct {
	PlayerID    string `json:"playerId"`
	UnitID      int64  `json:"unitId"`
	StartedAtMs int64  `json:"startedAtMs"`
	EndsAtMs    int64  `json:"endsAtMs"`
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
	// OwnerFaction es la facción del jugador dueño, o null si no tiene un
	// jugador por dueño (SPEC-FACTIONS §6).
	OwnerFaction *string `json:"ownerFaction"`
	// OwnerClan es el clan dueño cuando OwnerType es CLAN, o null
	// (SPEC-CLAN-TERRITORIES §6).
	OwnerClan *TerritoryOwnerClanView `json:"ownerClan"`
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
	// Clan es el clan para el que se reclama, si es para un clan
	// (SPEC-CLAN-TERRITORIES §6).
	Clan *CityClanView `json:"clan,omitempty"`
}

// TerritoryOwnerClanView es el clan dueño de un territorio: público
// (SPEC-CLAN-TERRITORIES Q11).
type TerritoryOwnerClanView struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Tag  string `json:"tag"`
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
	// ForClan reclama en nombre del propio clan (SPEC-CLAN-TERRITORIES §4.2).
	ForClan *bool `json:"forClan,omitempty"`
}

// UnitAssaultPayload ordena a una unidad propia que derriba asaltar una ciudad
// en brecha (SPEC-CITY-FALL §6). El plazo y si se puede son del servidor.
type UnitAssaultPayload struct {
	UnitID int64 `json:"unitId"`
	CityID int64 `json:"cityId"`
}

// UnitFoundCityPayload ordena a un aldeano propio fundar una segunda ciudad en
// su tile (SPEC-MULTI-CITY §6). El sitio, el coste y si se puede son del
// servidor.
type UnitFoundCityPayload struct {
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

// MarketPostPayload publica una oferta en el mercado (SPEC-MARKETS §6): lo que
// da y pide cada lote y cuántos lotes.
type MarketPostPayload struct {
	Give ResourceAmount `json:"give"`
	Want ResourceAmount `json:"want"`
	Lots int32          `json:"lots"`
}

// MarketBuyPayload compra lotes de una oferta ajena.
type MarketBuyPayload struct {
	OfferID int64 `json:"offerId"`
	Lots    int32 `json:"lots"`
}

// MarketCancelPayload retira la oferta propia.
type MarketCancelPayload struct {
	OfferID int64 `json:"offerId"`
}

// MarketListPayload pide el tablón, con un filtro opcional por el recurso que
// se quiere recibir o el que se quiere pagar.
type MarketListPayload struct {
	Receive string `json:"receive,omitempty"`
	Pay     string `json:"pay,omitempty"`
}

// MarketOfferView es una oferta del mercado (SPEC-MARKETS §6). La ciudad lleva
// su posición, no la de su depósito (ADR-047 §6); en una oferta retirada o
// caducada, LotsLeft son los lotes que volvieron al almacén.
type MarketOfferView struct {
	ID           int64          `json:"id"`
	PlayerID     string         `json:"playerId"`
	CityID       int64          `json:"cityId"`
	CityName     *string        `json:"cityName"`
	Civilization *string        `json:"civilization"`
	CityX        *int32         `json:"cityX"`
	CityY        *int32         `json:"cityY"`
	Give         ResourceAmount `json:"give"`
	Want         ResourceAmount `json:"want"`
	LotsTotal    int32          `json:"lotsTotal"`
	LotsLeft     int32          `json:"lotsLeft"`
	LotsSold     int32          `json:"lotsSold"`
	Status       string         `json:"status"`
	ExpiresAtMs  int64          `json:"expiresAtMs"`
}

// MarketUpdatePayload lleva una oferta del mercado que cambió a su vendedor, y a
// quien dio la orden.
type MarketUpdatePayload struct {
	Offer MarketOfferView `json:"offer"`
}

// MarketListingPayload es el tablón, en respuesta a market.list.
type MarketListingPayload struct {
	Offers []MarketOfferView `json:"offers"`
}

// CaravanView es una caravana vista por uno de sus implicados (SPEC-CARAVANS
// §6): su dueño, quien la mandó, el destinatario original y el captor. ToCityID
// es adonde va ahora y OriginalToCityID adonde iba al salir. ArrivesAtMs es nil
// mientras no tiene movimiento.
type CaravanView struct {
	ID               int64            `json:"id"`
	UnitID           int64            `json:"unitId"`
	OwnerPlayerID    string           `json:"ownerPlayerId"`
	SenderPlayerID   string           `json:"senderPlayerId"`
	FromCityID       int64            `json:"fromCityId"`
	FromCityName     *string          `json:"fromCityName"`
	ToCityID         int64            `json:"toCityId"`
	ToCityName       *string          `json:"toCityName"`
	OriginalToCityID int64            `json:"originalToCityId"`
	Cargo            []ResourceAmount `json:"cargo"`
	Status           string           `json:"status"`
	ArrivesAtMs      *int64           `json:"arrivesAtMs"`
}

// CaravanUpdatePayload lleva una caravana que cambió a uno de sus implicados.
type CaravanUpdatePayload struct {
	Caravan CaravanView `json:"caravan"`
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
	ID        int64  `json:"id"`
	Kind      string `json:"kind"`
	FirstAtMs int64  `json:"firstAtMs"`
	LastAtMs  int64  `json:"lastAtMs"`
	Count     int    `json:"count"`
	// OtherPlayerID es nil sólo en MARKET_EXPIRED, que no tiene otro jugador
	// (SPEC-MARKETS §6).
	OtherPlayerID *string `json:"otherPlayerId"`
	OtherCityID   *int64  `json:"otherCityId"`
	OtherCityName *string `json:"otherCityName"`
	UnitType      string  `json:"unitType,omitempty"`
	// BuildingType, en los derribos: qué edificio cayó (SPEC-SIEGE-ENGINES §6).
	BuildingType string `json:"buildingType,omitempty"`
	Amount       *int64 `json:"amount,omitempty"`
	TreatyType   string `json:"treatyType,omitempty"`
	ProposedByMe *bool  `json:"proposedByMe,omitempty"`
	X            *int32 `json:"x,omitempty"`
	Y            *int32 `json:"y,omitempty"`
	BreaksAtMs   *int64 `json:"breaksAtMs,omitempty"`
	// TradeGive y TradeGet, en los tratos: lo que la oferta sacaba del almacén
	// de quien lee y lo que metía en él (SPEC-TRADE §6).
	TradeGive []ResourceAmount `json:"tradeGive,omitempty"`
	TradeGet  []ResourceAmount `json:"tradeGet,omitempty"`
	// TerritoryID y TerritoryName, en la conquista (SPEC-TERRITORY-CONQUEST §6).
	TerritoryID   *int64 `json:"territoryId,omitempty"`
	TerritoryName string `json:"territoryName,omitempty"`
	// ClanTag, en la conquista con un clan de por medio: el clan dueño en
	// TERRITORY_CLAIMED y TERRITORY_LOST, y el clan para el que se ganó en
	// TERRITORY_TAKEN (SPEC-CLAN-TERRITORIES §6).
	ClanTag string `json:"clanTag,omitempty"`
	// Cargo, en las caravanas: lo que llevaba (SPEC-CARAVANS §6).
	Cargo []ResourceAmount `json:"cargo,omitempty"`
	// Lots, en el mercado: los lotes vendidos en MARKET_SOLD o devueltos en
	// MARKET_EXPIRED (SPEC-MARKETS §6).
	Lots *int32 `json:"lots,omitempty"`
	// BreakReason, en una ruptura que abrió una guerra entre clanes: WAR
	// (SPEC-CLAN-WARS §6).
	BreakReason string `json:"breakReason,omitempty"`
	// La guerra entre clanes (SPEC-CLAN-WARS §6): cuándo empieza, en la
	// declaración y la oferta de paz; el resultado para quien lee, el motivo y
	// el marcador desde su clan, en el final.
	StartsAtMs *int64 `json:"startsAtMs,omitempty"`
	WarResult  string `json:"warResult,omitempty"`
	WarReason  string `json:"warReason,omitempty"`
	OurScore   *int64 `json:"ourScore,omitempty"`
	TheirScore *int64 `json:"theirScore,omitempty"`
	// FallsAtMs, en el asalto de una ciudad: cuándo cae si nadie lo rompe
	// (SPEC-CITY-FALL §6).
	FallsAtMs *int64 `json:"fallsAtMs,omitempty"`
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
	// BuildingType, en un derribo, como en EventLogEntry (SPEC-SIEGE-ENGINES §6).
	BuildingType string `json:"buildingType,omitempty"`
	Amount       *int64 `json:"amount,omitempty"`
	X            *int32 `json:"x,omitempty"`
	Y            *int32 `json:"y,omitempty"`
	// TerritoryID y TerritoryName, en la conquista (SPEC-TERRITORY-CONQUEST §6).
	TerritoryID   *int64 `json:"territoryId,omitempty"`
	TerritoryName string `json:"territoryName,omitempty"`
	// ClanTag, como en EventLogEntry (SPEC-CLAN-TERRITORIES §6).
	ClanTag string `json:"clanTag,omitempty"`
	// Cargo, en las caravanas: lo que llevaba (SPEC-CARAVANS §6).
	Cargo []ResourceAmount `json:"cargo,omitempty"`
	// La guerra entre clanes, como en EventLogEntry (SPEC-CLAN-WARS §6).
	StartsAtMs *int64 `json:"startsAtMs,omitempty"`
	WarResult  string `json:"warResult,omitempty"`
	WarReason  string `json:"warReason,omitempty"`
	OurScore   *int64 `json:"ourScore,omitempty"`
	TheirScore *int64 `json:"theirScore,omitempty"`
	// FallsAtMs, en el asalto de una ciudad: cuándo cae si nadie lo rompe
	// (SPEC-CITY-FALL §6).
	FallsAtMs *int64 `json:"fallsAtMs,omitempty"`
}

// EventLogPagePayload es una página del registro. NextBeforeID es nil en la
// última.
type EventLogPagePayload struct {
	Entries      []EventLogEntry `json:"entries"`
	SeenUpToID   int64           `json:"seenUpToId"`
	NextBeforeID *int64          `json:"nextBeforeId"`
}

// MessageSendPayload escribe a otro jugador (SPEC-MESSAGES §4.2). El remitente
// es siempre el jugador de la sesión (RN-MSG-001).
type MessageSendPayload struct {
	ToPlayerID string `json:"toPlayerId"`
	Body       string `json:"body"`
}

// MessageListPayload pide la bandeja; no lleva nada.
type MessageListPayload struct{}

// MessageFetchPayload pide una página de la conversación con OtherPlayerID: la
// más reciente, o la de los mensajes con id menor que BeforeID.
type MessageFetchPayload struct {
	OtherPlayerID string `json:"otherPlayerId"`
	BeforeID      *int64 `json:"beforeId,omitempty"`
}

// MessageReadPayload marca leída la conversación hasta UpToID (RN-MSG-008).
type MessageReadPayload struct {
	OtherPlayerID string `json:"otherPlayerId"`
	UpToID        int64  `json:"upToId"`
}

// MessageBlockPayload bloquea o desbloquea los mensajes con PlayerID.
type MessageBlockPayload struct {
	PlayerID string `json:"playerId"`
	Blocked  bool   `json:"blocked"`
}

// MessageView es un mensaje visto por uno de sus dos jugadores (SPEC-MESSAGES
// §6): «el otro» es el que no es el de la sesión, nombrado por su ciudad.
type MessageView struct {
	ID            int64   `json:"id"`
	FromPlayerID  string  `json:"fromPlayerId"`
	ToPlayerID    string  `json:"toPlayerId"`
	OtherPlayerID string  `json:"otherPlayerId"`
	OtherCityID   *int64  `json:"otherCityId"`
	OtherCityName *string `json:"otherCityName"`
	Body          string  `json:"body"`
	SentAtMs      int64   `json:"sentAtMs"`
}

// MessageNewPayload lleva un mensaje recién guardado a las sesiones de los dos.
type MessageNewPayload struct {
	Message MessageView `json:"message"`
}

// ConversationView es una conversación de la bandeja de quien mira.
type ConversationView struct {
	OtherPlayerID string  `json:"otherPlayerId"`
	OtherCityID   *int64  `json:"otherCityId"`
	OtherCityName *string `json:"otherCityName"`
	LastBody      string  `json:"lastBody"`
	LastFromMe    bool    `json:"lastFromMe"`
	LastAtMs      int64   `json:"lastAtMs"`
	Unread        int     `json:"unread"`
	BlockedByMe   bool    `json:"blockedByMe"`
}

// BlockedPlayerView es un jugador bloqueado por quien mira.
type BlockedPlayerView struct {
	PlayerID string  `json:"playerId"`
	CityID   *int64  `json:"cityId"`
	CityName *string `json:"cityName"`
}

// MessageInboxPayload es la bandeja: las conversaciones, de la más reciente a la
// más antigua, y los bloqueados.
type MessageInboxPayload struct {
	Conversations []ConversationView  `json:"conversations"`
	Blocked       []BlockedPlayerView `json:"blocked"`
}

// MessagePagePayload es una página de una conversación, de más antiguo a más
// reciente. NextBeforeID es nil cuando no hay mensajes anteriores.
type MessagePagePayload struct {
	OtherPlayerID string        `json:"otherPlayerId"`
	OtherCityID   *int64        `json:"otherCityId"`
	OtherCityName *string       `json:"otherCityName"`
	Messages      []MessageView `json:"messages"`
	ReadUpToID    int64         `json:"readUpToId"`
	NextBeforeID  *int64        `json:"nextBeforeId"`
	BlockedByMe   bool          `json:"blockedByMe"`
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

// CityResearchPayload encarga una investigación en una ciudad. Sólo nombra la
// ciudad y la tecnología: el coste, el plazo y si está a su alcance son del
// servidor (SPEC-TECHNOLOGIES RN-TECH-002, INV-SEC-001).
type CityResearchPayload struct {
	CityID     int64  `json:"cityId"`
	Technology string `json:"technology"`
}

// CityCraftPayload encarga al taller de una ciudad una elaboración. Sólo
// nombra la ciudad: el bien, la cantidad, el coste y el tiempo son del
// servidor (SPEC-CIVILIZATION-GOODS RN-GOODS-004, INV-SEC-001).
type CityCraftPayload struct {
	CityID int64 `json:"cityId"`
}

// ─────────────────────────────────────────────────────────────
// Clanes (SPEC-CLANS)
// ─────────────────────────────────────────────────────────────

// CityClanView es el clan de una ciudad: su id y su sigla.
type CityClanView struct {
	ID  int64  `json:"id"`
	Tag string `json:"tag"`
}

// ClanCreatePayload funda un clan; quien funda es su líder (SPEC-CLANS §4.2).
type ClanCreatePayload struct {
	Name string `json:"name"`
	Tag  string `json:"tag"`
}

// ClanInvitePayload invita a PlayerID al clan del líder, o retira la
// invitación con Invited false.
type ClanInvitePayload struct {
	PlayerID string `json:"playerId"`
	Invited  bool   `json:"invited"`
}

// ClanRespondPayload acepta o rechaza una invitación viva.
type ClanRespondPayload struct {
	ClanID int64 `json:"clanId"`
	Accept bool  `json:"accept"`
}

// ClanLeavePayload sale del clan propio; no lleva nada.
type ClanLeavePayload struct{}

// ClanExpelPayload expulsa a PlayerID del clan del líder.
type ClanExpelPayload struct {
	PlayerID string `json:"playerId"`
}

// ClanPromotePayload nombra oficial a un miembro o le quita el rango
// (SPEC-CLAN-GOVERNANCE §4.3).
type ClanPromotePayload struct {
	PlayerID string `json:"playerId"`
	Officer  bool   `json:"officer"`
}

// ClanTransferPayload cede el liderazgo a otro miembro (SPEC-CLAN-GOVERNANCE
// §4.3).
type ClanTransferPayload struct {
	PlayerID string `json:"playerId"`
}

// ClanAlliancePayload propone una alianza al clan ClanID
// (SPEC-CLAN-ALLIANCES §4.3).
type ClanAlliancePayload struct {
	ClanID int64 `json:"clanId"`
}

// ClanAllianceRespondPayload acepta o rechaza una oferta de alianza.
type ClanAllianceRespondPayload struct {
	AllianceID int64 `json:"allianceId"`
	Accept     bool  `json:"accept"`
}

// ClanAllianceCancelPayload retira una oferta o rompe una alianza.
type ClanAllianceCancelPayload struct {
	AllianceID int64 `json:"allianceId"`
}

// ClanWarDeclarePayload declara la guerra al clan ClanID (SPEC-CLAN-WARS
// §4.3).
type ClanWarDeclarePayload struct {
	ClanID int64 `json:"clanId"`
}

// ClanWarPeacePayload ofrece la paz (o la acepta, si el otro clan ya la
// ofreció) con Offer, o retira la propia o rechaza la ajena sin él.
type ClanWarPeacePayload struct {
	WarID int64 `json:"warId"`
	Offer bool  `json:"offer"`
}

// ClanWarSurrenderPayload rinde al clan propio en una guerra.
type ClanWarSurrenderPayload struct {
	WarID int64 `json:"warId"`
}

// ClanLookupPayload pide la vista pública de un clan.
type ClanLookupPayload struct {
	ClanID int64 `json:"clanId"`
}

// ClanChatSendPayload escribe en el canal del clan propio.
type ClanChatSendPayload struct {
	Body string `json:"body"`
}

// ClanChatFetchPayload pide una página del canal: la más reciente o la de los
// mensajes con id menor que BeforeID.
type ClanChatFetchPayload struct {
	BeforeID *int64 `json:"beforeId,omitempty"`
}

// ClanChatReadPayload marca leído el canal hasta UpToID.
type ClanChatReadPayload struct {
	UpToID int64 `json:"upToId"`
}

// ClanMemberView es un miembro de un clan, nombrado por su ciudad.
type ClanMemberView struct {
	PlayerID   string  `json:"playerId"`
	CityID     *int64  `json:"cityId"`
	CityName   *string `json:"cityName"`
	Faction    string  `json:"faction"`
	Role       string  `json:"role"`
	JoinedAtMs int64   `json:"joinedAtMs"`
}

// ClanSentInvitationView es una invitación viva que envió el clan.
type ClanSentInvitationView struct {
	PlayerID    string  `json:"playerId"`
	CityID      *int64  `json:"cityId"`
	CityName    *string `json:"cityName"`
	ExpiresAtMs int64   `json:"expiresAtMs"`
}

// ClanView es el propio clan, visto por uno de sus miembros.
type ClanView struct {
	ID              int64                    `json:"id"`
	Name            string                   `json:"name"`
	Tag             string                   `json:"tag"`
	LeaderID        string                   `json:"leaderId"`
	Members         []ClanMemberView         `json:"members"`
	SentInvitations []ClanSentInvitationView `json:"sentInvitations"`
	// Territories son los territorios del clan, por id
	// (SPEC-CLAN-TERRITORIES Q11).
	Territories []ClanTerritoryView `json:"territories"`
	// Alliances son las alianzas activas del clan y, para el líder y los
	// oficiales, también sus ofertas, por id (SPEC-CLAN-ALLIANCES §6).
	Alliances []ClanAllianceView `json:"alliances"`
	// Wars son las guerras abiertas del clan, por id; WarRecord, su historial
	// (SPEC-CLAN-WARS §6).
	Wars      []ClanWarView     `json:"wars"`
	WarRecord ClanWarRecordView `json:"warRecord"`
}

// ClanWarView es una guerra abierta del propio clan con otro.
type ClanWarView struct {
	ID int64 `json:"id"`
	// ClanID, Name y Tag son los del otro clan.
	ClanID int64  `json:"clanId"`
	Name   string `json:"name"`
	Tag    string `json:"tag"`
	// DeclaredByUs: la declaró el clan de quien mira.
	DeclaredByUs bool `json:"declaredByUs"`
	// Status es DECLARED o ACTIVE.
	Status     string `json:"status"`
	StartsAtMs int64  `json:"startsAtMs"`
	// EndsAtMs es el plazo, o null en la preparación.
	EndsAtMs   *int64 `json:"endsAtMs"`
	OurScore   int64  `json:"ourScore"`
	TheirScore int64  `json:"theirScore"`
	// Las ofertas de paz; siempre false salvo para el líder y los oficiales.
	PeaceOfferedByUs   bool `json:"peaceOfferedByUs"`
	PeaceOfferedByThem bool `json:"peaceOfferedByThem"`
}

// ClanPublicWarView es una guerra abierta de un clan en su vista pública, vista
// desde ese clan.
type ClanPublicWarView struct {
	ClanID       int64  `json:"clanId"`
	Name         string `json:"name"`
	Tag          string `json:"tag"`
	DeclaredByUs bool   `json:"declaredByUs"`
	Status       string `json:"status"`
	StartsAtMs   int64  `json:"startsAtMs"`
	EndsAtMs     *int64 `json:"endsAtMs"`
	OurScore     int64  `json:"ourScore"`
	TheirScore   int64  `json:"theirScore"`
}

// ClanWarRecordView es el historial de guerras terminadas de un clan.
type ClanWarRecordView struct {
	Won   int `json:"won"`
	Lost  int `json:"lost"`
	Drawn int `json:"drawn"`
}

// ClanAllianceView es una alianza (o una oferta) del propio clan con otro.
type ClanAllianceView struct {
	ID int64 `json:"id"`
	// ClanID, Name y Tag son los del otro clan.
	ClanID int64  `json:"clanId"`
	Name   string `json:"name"`
	Tag    string `json:"tag"`
	// Status es PROPOSED o ACTIVE.
	Status string `json:"status"`
	// ProposedByUs: la propuso el clan de quien mira.
	ProposedByUs bool `json:"proposedByUs"`
	// BreaksAtMs es el fin del preaviso de ruptura, o null.
	BreaksAtMs *int64 `json:"breaksAtMs"`
}

// ClanPublicAllianceView es una alianza activa de un clan en su vista pública.
type ClanPublicAllianceView struct {
	ClanID     int64  `json:"clanId"`
	Name       string `json:"name"`
	Tag        string `json:"tag"`
	BreaksAtMs *int64 `json:"breaksAtMs"`
}

// ClanTerritoryView es un territorio de un clan, con su tile central para
// llevar la cámara.
type ClanTerritoryView struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	X    int32  `json:"x"`
	Y    int32  `json:"y"`
}

// ClanInvitationView es una invitación recibida y viva.
type ClanInvitationView struct {
	ClanID      int64  `json:"clanId"`
	Name        string `json:"name"`
	Tag         string `json:"tag"`
	ExpiresAtMs int64  `json:"expiresAtMs"`
}

// ClanUpdatePayload es el clan de un jugador, o null, y sus invitaciones
// recibidas.
type ClanUpdatePayload struct {
	Clan        *ClanView            `json:"clan"`
	Invitations []ClanInvitationView `json:"invitations"`
}

// ClanInfoPayload es la vista pública de un clan.
type ClanInfoPayload struct {
	ClanID  int64            `json:"clanId"`
	Name    string           `json:"name"`
	Tag     string           `json:"tag"`
	Members []ClanMemberView `json:"members"`
	// Territories son los territorios del clan, por id.
	Territories []ClanTerritoryView `json:"territories"`
	// Alliances son sus alianzas activas, por id de clan
	// (SPEC-CLAN-ALLIANCES Q8).
	Alliances []ClanPublicAllianceView `json:"alliances"`
	// Wars son sus guerras abiertas, por id de clan, y WarRecord su historial
	// (SPEC-CLAN-WARS Q10).
	Wars      []ClanPublicWarView `json:"wars"`
	WarRecord ClanWarRecordView   `json:"warRecord"`
}

// ClanMessageView es un mensaje del canal de un clan.
type ClanMessageView struct {
	ID           int64   `json:"id"`
	ClanID       int64   `json:"clanId"`
	FromPlayerID string  `json:"fromPlayerId"`
	FromCityID   *int64  `json:"fromCityId"`
	FromCityName *string `json:"fromCityName"`
	Body         string  `json:"body"`
	SentAtMs     int64   `json:"sentAtMs"`
}

// ClanChatNewPayload lleva un mensaje recién guardado a los miembros.
type ClanChatNewPayload struct {
	Message ClanMessageView `json:"message"`
}

// ClanChatPagePayload es una página del canal, de más antiguo a más reciente.
type ClanChatPagePayload struct {
	ClanID       int64             `json:"clanId"`
	Messages     []ClanMessageView `json:"messages"`
	ReadUpToID   int64             `json:"readUpToId"`
	NextBeforeID *int64            `json:"nextBeforeId"`
}

// FactionJoinPayload pide unirse a un bando (SPEC-FACTIONS §4.2). Sólo nombra
// el bando: el instante en que cuenta lo decide el servidor (INV-SEC-001).
type FactionJoinPayload struct {
	Faction string `json:"faction"`
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
	// Caravans son las caravanas en camino que salen de la ciudad de quien
	// recibe el snapshot, van a ella o son suyas (SPEC-CARAVANS §6). Se omite si
	// no hay ninguna.
	Caravans []CaravanView `json:"caravans,omitempty"`
	// MarketOffers son las ofertas OPEN del mercado de quien recibe el
	// snapshot (SPEC-MARKETS §6). Se omite si no tiene ninguna.
	MarketOffers []MarketOfferView `json:"marketOffers,omitempty"`
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
	// Research y Technologies: sólo los recibe el dueño (SPEC-TECHNOLOGIES §6).
	// Research: nil si no cambia; apuntando a nil, la investigación terminó o se
	// deshizo; apuntando a una vista, empieza. Technologies: las del jugador
	// tras terminar una.
	Research     **ResearchView `json:"research,omitempty"`
	Technologies *[]string      `json:"technologies,omitempty"`
	// Faction es la facción nueva al contar una entrada; FactionJoin: nil si no
	// cambia, apuntando a nil si la entrada contó, apuntando a una vista si
	// empieza (SPEC-FACTIONS §6).
	Faction     *string           `json:"faction,omitempty"`
	FactionJoin **FactionJoinView `json:"factionJoin,omitempty"`
	// Clan: nil si no cambia; apuntando a nil, el dueño sale de su clan;
	// apuntando a una vista, entra (SPEC-CLANS §6).
	Clan **CityClanView `json:"clan,omitempty"`
	// PillagedAtMs es el instante del último golpe de saqueo. Sólo lo recibe
	// el dueño de la ciudad (SPEC-COMBAT-PILLAGE RN-COMBAT-028).
	PillagedAtMs *int64 `json:"pillagedAtMs,omitempty"`
	// BesiegedAtMs es el instante del último golpe de asedio a una casa de la
	// ciudad. Sólo lo recibe su dueño (SPEC-COMBAT-SIEGE RN-COMBAT-038).
	BesiegedAtMs *int64 `json:"besiegedAtMs,omitempty"`
	// Assault es el asalto sobre la ciudad: apuntando a una vista, empieza;
	// apuntando a nil, termina sin caída (SPEC-CITY-FALL §6).
	Assault **CityAssaultView `json:"assault,omitempty"`
}

// CityFallenPayload anuncia una ciudad caída: el cliente la quita
// (SPEC-CITY-FALL §6).
type CityFallenPayload struct {
	CityID        int64  `json:"cityId"`
	OwnerPlayerID string `json:"ownerPlayerId"`
	ByPlayerID    string `json:"byPlayerId"`
	AtMs          int64  `json:"atMs"`
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
	// TargetUnitID es el nuevo objetivo de un castillo, o null cuando deja de
	// apuntar (SPEC-CASTLE-AGE §6). Ausente si no cambia.
	TargetUnitID **int64 `json:"targetUnitId,omitempty"`
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
