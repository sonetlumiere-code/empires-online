# Protocolo WebSocket v1

Especificación normativa y completa del contrato cliente↔servidor de Empires Online: envelopes, límites, códigos de cierre, catálogo de mensajes v1, errores, idempotencia y ordenamiento.

---

## 1. Objetivo

Definir, sin ambigüedad, el único canal de comunicación en tiempo real entre el cliente web y el Game Server autoritativo (`services/game-server`, módulo Go con `go 1.23` como versión mínima). Un ingeniero que llegue nuevo debe poder escribir cliente o servidor únicamente con este documento y los esquemas de `packages/protocol`.

> **Fuente de verdad.** El contrato lo definen los esquemas Zod de `packages/protocol/src/v1/` y su implementación Go en `services/game-server/internal/protocol/`. Este documento describe ese contrato; si difiere del código, **el documento está mal**. Cada ficha de mensaje de las secciones 7 y 8 es un reflejo literal de `internal/protocol/protocol.go` y de `packages/protocol/src/v1/{client,server,common,errors}.ts`.

El cliente web (`apps/web`, Next.js + PixiJS 8) **todavía no existe** en el repositorio: sus obligaciones aquí descritas están redactadas como el contrato que deberá cumplir cuando se implemente (milestone de frontend), no como comportamiento ya observable.

El protocolo materializa el principio no negociable número 1: *client sends intent, server determines truth*. El cliente jamás transmite estado autoritativo (posición final, HP, resultado de pathfinding, ETA, ownership, cooldowns). Envía **intenciones** y recibe **hechos**.

## 2. Scope

Dentro del scope de v1:

- Handshake autenticado por *game ticket* y establecimiento de sesión.
- Keepalive aplicativo (`session.ping` / `session.pong`) además del ping/pong de frames WebSocket.
- Gestión de área de interés por chunk (`session.view`).
- Sincronización del área de interés (`world.snapshot`, terreno incluido) y deltas incrementales (`entity.*`, `city.update`, `territory.update`).
- Ciclo de vida completo del movimiento de unidades (`unit.move`, `unit.cancel_move` y sus cinco mensajes de salida).
- Errores estables, idempotencia por `requestId`, ordenamiento por `seq`.

### 2.1 No-scope (Fuera de MVP)

No forman parte de v1 y **no deben documentarse ni implementarse como si existieran**: combate y resolución de daño, economía y recolección de recursos, construcción de edificios, árbol tecnológico, chat, comercio y caravanas, clanes, ranking, unidades navales, IA de NPC. La fase 4 del tick (`resolve simulation`) está reservada para combate y hoy es un no-op; ver [../architecture/game-loop.md](../architecture/game-loop.md).

Tampoco entran en v1:

- **Compresión** (`permessage-deflate`), binario/CBOR, batching de múltiples envelopes por frame: `TBD (fuera de MVP)`. v1 es JSON UTF-8, un envelope por frame de texto.
- Entidades de tipo edificio: en MVP no existe tabla `buildings`; el `TOWN_CENTER` se describe dentro de `city.update`. Un *kind* de entidad `BUILDING` es `TBD (fuera de MVP)`.
- **Cota máxima de waypoints** de una polilínea: `TBD (fuera de MVP)`. Ver la nota de la ficha 8.12.
- **Reproducción de la respuesta original ante un `requestId` duplicado**: hoy el duplicado se descarta en silencio. Ver sección 10.

## 3. Versionado y política de compatibilidad

La versión viaja **en cada mensaje**, en el campo `v` (entero). En v1 el valor es siempre `1` (`protocol.Version`, `PROTOCOL_VERSION`).

| Regla | Detalle |
|---|---|
| R-VER-1 | Un mensaje con `v` distinto de una versión soportada se rechaza con `UNSUPPORTED_VERSION`. En el handshake implica cierre `4400`; ya dentro de la sesión, el mensaje se descarta y la conexión continúa. |
| R-VER-2 | Añadir un **campo opcional** a un payload servidor→cliente es aditivo y NO rompe v1. El cliente antiguo lo ignora. Esta regla es la razón de que los esquemas servidor→cliente no sean estrictos (ver S-7). |
| R-VER-3 | Añadir un **tipo de mensaje nuevo** servidor→cliente es aditivo. El cliente debe descartar silenciosamente los `type` que no conoce, sin cerrar la conexión y sin loguear como error. |
| R-VER-4 | Renombrar o eliminar un campo, cambiar su tipo, cambiar la semántica de un valor existente, volver obligatorio un campo opcional o eliminar un `type` es **incompatible**: obliga a crear v2. |
| R-VER-5 | v1 y v2 **coexisten**. El servidor acepta ambas durante la ventana de migración; la sesión queda fijada a la versión del `session.hello`. Mezclar versiones dentro de una conexión es `UNSUPPORTED_VERSION`. |
| R-VER-6 | Nunca se rompe v1 en silencio. Todo cambio incompatible requiere ADR, bump de major del paquete `@empires-online/protocol` y directorio nuevo `packages/protocol/src/v2/`. |
| R-VER-7 | Los códigos de error de la sección 9 son **estables**: su string jamás cambia. Se pueden añadir códigos nuevos; el cliente trata un código desconocido como `INTERNAL_ERROR` a efectos de UI. |

## 4. Transporte

| Aspecto | Valor v1 |
|---|---|
| Esquema | `wss://` (TLS obligatorio en cualquier entorno que no sea local) |
| Endpoint | `/ws` sobre `EO_HTTP_ADDR` (por defecto `:8080`) → `wss://<host>/ws` |
| Origen | El upgrade comprueba la cabecera `Origin` contra una lista blanca. Vacía = se acepta cualquier origen (sólo desarrollo); en producción debe estar poblada. |
| Subprotocolo | v1 **no negocia** `Sec-WebSocket-Protocol`. La versión viaja en el campo `v` de cada envelope. Registrar un subprotocolo con nombre es `TBD (fuera de MVP)`. |
| Formato de frame | Frame de **texto**, JSON válido, codificación **UTF-8**. |
| Un frame | Exactamente **un envelope**. No hay *framing* de varios mensajes por frame ni fragmentación aplicativa. |
| Tamaño máximo entrante | **16 KiB** = 16384 bytes (`EO_WS_MAX_MESSAGE_BYTES`), aplicado por `conn.SetReadLimit` sobre el frame entrante. Es un límite del **sentido entrante**: los mensajes salientes no se truncan (ver la nota de tamaño de 8.4). |
| Rate limit entrante | **20 msg/s** (`EO_WS_RATE_LIMIT_PER_SECOND`) con **burst 40** (`EO_WS_RATE_LIMIT_BURST`), token bucket por conexión. |
| Cola de salida | **256** mensajes por conexión (`EO_WS_OUTBOUND_QUEUE_SIZE`, rango 8–65536). Si se llena, la sesión se cierra con `4500`. |
| Ping | El servidor envía un ping de control WebSocket cada **15 s**. El cliente debe responder pong (lo hace el navegador de forma automática). |
| Read timeout | **45 s** sin ningún frame recibido del cliente → cierre. Cada frame recibido, incluido el pong de control, renueva el deadline. |
| Write timeout | **10 s** por escritura. Una escritura fallida cierra la sesión con `4500`. |
| Handshake | El primer mensaje debe ser `session.hello` **antes de 5 s** desde el establecimiento del WebSocket; si no, cierre `4408`. |

`WSHandshakeTimeout = 5s`, `WSPingInterval = 15s`, `WSReadTimeout = 45s` y `WSWriteTimeout = 10s` son **constantes de código**, no variables de entorno: no hay ninguna `EO_` que las controle.

### 4.1 Reglas de serialización

| Regla | Detalle |
|---|---|
| S-1 | Los identificadores `bigint GENERATED ALWAYS AS IDENTITY` (`unitId`/`id` de unidad, `movementId`, `cityId`, `territoryId`) se serializan como **número JSON entero**. El esquema los acota a `[1, Number.MAX_SAFE_INTEGER]` (`EntityId`), de modo que ningún id emitido puede perder precisión en el `number` de JavaScript. |
| S-2 | `playerId` y `sessionId` son **UUID** en formato canónico. `playerId` es `players.id` (`uuid` en PostgreSQL); `sessionId` lo genera el servidor en memoria al aceptar la conexión y **no** es un `bigint` de la tabla `sessions`. |
| S-3 | Todo instante absoluto es epoch **milliseconds** UTC en un campo `*Ms` de tipo `number` entero (`serverTimeMs`, `startTimeMs`, `arrivalTimeMs`, `protectionUntilMs`, `ts`). Nunca strings ISO en el canal de tiempo real. `tMs` es la excepción semántica: es un **offset** relativo a `startTimeMs`, no un instante. |
| S-4 | Coordenadas `x`, `y`: enteros `int32` en espacio de **tiles**. El servidor nunca envía píxeles; la proyección isométrica es responsabilidad exclusiva del cliente. |
| S-5 | Un chunk se identifica por el **par `{cx, cy}`** (`ChunkRef`), nunca por un índice lineal: `cx = x / chunkSize`, `cy = y / chunkSize` con `chunkSize = EO_CHUNK_SIZE` (32 en MVP). No existe ningún campo `chunkId` en el protocolo v1. |
| S-6 | En los deltas servidor→cliente, campo **ausente** y `null` **no son equivalentes**: ausente significa "sin cambios en este delta"; `null` significa "el servidor afirma que no hay valor" (p. ej. `protectionUntilMs: null`, `movement: null`, `cityId: null`). |
| S-7 | Los esquemas **cliente→servidor son estrictos** (`.strict()` en Zod → `additionalProperties: false` en el JSON Schema): una propiedad desconocida se rechaza. Los esquemas **servidor→cliente NO son estrictos**, deliberadamente: es lo que permite añadir campos opcionales (R-VER-2) sin invalidar a un cliente antiguo que valide lo que recibe. La asimetría es intencionada: hacia dentro se cierra la puerta para detectar drift y bloquear estado no contractual (INV-SEC-001); hacia fuera se deja abierta para poder evolucionar. |
| S-8 | El terreno viaja como **base64** de `size*size` bytes en orden fila-mayor, un byte por tile con el valor numérico del tipo de terreno: `GRASSLAND=0`, `FOREST=1`, `HILL=2`, `MOUNTAIN=3`, `WATER=4`, `ROAD=5`. |

### 4.2 Rate limiting

Token bucket por conexión: capacidad `EO_WS_RATE_LIMIT_BURST` (40), recarga `EO_WS_RATE_LIMIT_PER_SECOND` (20) fichas/segundo, un token por frame entrante (los pong de control no consumen token, porque no pasan por el lector de mensajes). Al agotarse, en el mismo paso:

1. El frame se **descarta sin ejecutar**.
2. El servidor emite `system.error` con `code: "RATE_LIMITED"` (sin `requestId`: el frame aún no se había parseado).
3. El servidor **cierra inmediatamente con `4429`**. No hay ronda de gracia.

**No existe ningún sub-límite específico para `session.view`.** Todos los mensajes cliente→servidor comparten el mismo bucket global. Un límite dedicado por tipo de mensaje es `TBD (fuera de MVP)` y, de introducirse, exigiría una variable `EO_` propia en la configuración.

## 5. Envelopes

### 5.1 Cliente → Servidor

```ts
interface ClientEnvelope<T extends ClientMessageType, P> {
  v: 1;
  type: T;
  requestId: string;  // UUID
  payload: P;
}
```

```go
// internal/protocol/protocol.go
type Inbound struct {
    V         int             `json:"v"`
    Type      string          `json:"type"`
    RequestID string          `json:"requestId"`
    Payload   json.RawMessage `json:"payload"`
}
```

| Campo | Tipo | Obligatorio | Significado |
|---|---|---|---|
| `v` | `1` | Sí | Versión del protocolo. Distinto de una versión soportada → `UNSUPPORTED_VERSION`. |
| `type` | `string` | Sí | Uno de los cinco tipos cliente→servidor de v1 (catálogo cerrado `ClientMessageTypes`). Desconocido → `INVALID_MESSAGE`. |
| `requestId` | `string` (UUID) | Sí en los cinco tipos, según el esquema Zod. El servidor lo **exige** hoy en `unit.move` y `unit.cancel_move`, donde es la clave de idempotencia; en los demás lo usa sólo para correlacionar la respuesta | Correlación y clave de idempotencia. Ver sección 10. |
| `payload` | `object` | Sí | Objeto específico del `type`, **estricto**: ninguna propiedad adicional. Nunca `null`. |

El payload se deja sin decodificar (`json.RawMessage`) hasta después de validar versión, tipo y autorización: no se gasta trabajo en mensajes que van a rechazarse.

### 5.2 Servidor → Cliente

```ts
interface ServerEnvelope<T extends ServerMessageType, P> {
  v: 1;
  type: T;
  seq: number;
  ts: number;
  requestId?: string;  // UUID, sólo si responde a un comando
  payload: P;
}
```

```go
type Outbound struct {
    V         int    `json:"v"`
    Type      string `json:"type"`
    Seq       uint64 `json:"seq"`
    TS        int64  `json:"ts"`
    RequestID string `json:"requestId,omitempty"`
    Payload   any    `json:"payload"`
}
```

| Campo | Tipo | Obligatorio | Significado |
|---|---|---|---|
| `v` | `1` | Sí | Versión del protocolo. |
| `type` | `string` | Sí | Uno de los catorce tipos servidor→cliente de v1. |
| `seq` | `number` (uint64) | Sí | Contador **monótono estrictamente creciente por conexión**, empieza en `1`. Se reinicia en cada conexión nueva. Ver sección 11. |
| `ts` | `number` | Sí | Epoch ms del reloj del servidor en el momento de encolar el mensaje. Es la referencia temporal del cliente. |
| `requestId` | `string` | **Se omite** salvo que el mensaje responda a un comando concreto | La etiqueta `json:"requestId,omitempty"` garantiza que el campo **no aparece** cuando no aplica: un broadcast (`entity.*`, `city.update`, `territory.update`, `unit.movement.*` dirigidos al chunk) nunca lleva un `requestId` vacío que el cliente pudiera confundir con una respuesta. Lo verifica el contract test `TestRequestIDSeOmiteCuandoNoAplica`. |
| `payload` | `object` | Sí | Objeto específico del `type`, **no estricto** (S-7). |

## 6. Ciclo de vida de la conexión

```mermaid
sequenceDiagram
    participant C as Cliente
    participant H as HTTP API de auth
    participant G as Game Server WS (Go)
    participant R as Redis
    C->>H: POST /api/auth/login { username, password }
    H-->>C: { playerId, ticket, expiresInSeconds: 60 }
    C->>G: WSS CONNECT /ws
    Note over G: read deadline de handshake (5 s)
    C->>G: session.hello { ticket }
    G->>R: SETNX ticket:jti:{jti} (TTL 120 s)
    alt firma/exp/aud inválidos o jti ya consumido
        G-->>C: close 4401 (sin system.error)
    else válido
        G-->>C: session.welcome (seq 1, requestId del hello)
        G-->>C: world.snapshot (seq 2, sin requestId)
        loop con cada session.ping
            G->>R: EXPIRE presence:player:{playerId} (TTL 30 s)
        end
    end
```

**Emisión del ticket, hoy y mañana.** En el estado actual del repositorio el ticket lo emite el **propio game server** en `POST /api/auth/register` y `POST /api/auth/login` (`internal/httpapi`, bcrypt para las credenciales), que devuelven `{ playerId, ticket, expiresInSeconds: 60 }`. Es explícitamente **provisional**: la arquitectura objetivo de [../decisions/ADR-010-authentication-game-ticket.md](../decisions/ADR-010-authentication-game-ticket.md) traslada la emisión a Next.js. El contrato del WebSocket no cambia con esa mudanza: el `session.hello` sigue llevando el mismo JWT HS256, `aud = "game-server"`, TTL 60 s, `jti` de un solo uso.

Reglas del ciclo de vida:

1. **El primer mensaje de la conexión debe ser `session.hello`.** No hay "estado pre-sesión" en el que quepan otros mensajes: el lector de la sesión autenticada ni siquiera arranca hasta que el handshake termina bien (INV-SEC-002).
2. Durante el handshake el servidor **no envía `system.error`**: cierra con el código correspondiente y punto. Distinguir por mensaje entre "ticket caducado", "firma inválida" y "jti ya consumido" ayudaría a afinar un ataque, así que los tres se traducen al mismo `4401`.
3. Un `session.hello` posterior a un handshake completado no es un tipo pre-auth válido; el lector lo trata como cualquier otro mensaje y no reabre la sesión.
4. Al establecerse la sesión, el servidor la registra en el `Hub`, marca presencia en Redis (`presence:player:{playerId}` con el `sessionId` como valor, TTL `EO_PRESENCE_TTL_SECONDS` = 30 s) y despacha `PlayerConnected` al game loop, que transiciona la ciudad del jugador a `ONLINE`. Un jugador con varias sesiones abiertas sigue `ONLINE` mientras le quede una.
5. Al cerrarse el WebSocket **no** se destruye ningún estado durable. La ciudad pasa a `OFFLINE_PENDING` cuando el game loop, en RAM, comprueba que el jugador lleva sin sesión más que `DisconnectGrace` (= `EO_PRESENCE_TTL_SECONDS`); la clave de Redis es para observadores externos y para el futuro multiproceso, no la fuente de esa decisión. Ver [../architecture/overview.md](../architecture/overview.md). Los movimientos `ACTIVE` continúan simulándose.
6. La reconexión es un handshake nuevo con un ticket nuevo: `seq` se reinicia en `1` y el cliente **debe** descartar el estado local y reconstruirlo desde el `world.snapshot` entrante. La caché de terreno de la sesión anterior también se pierde: `NeedsTerrain` es estado por sesión, así que la reconexión vuelve a recibir el terreno de sus chunks.

### 6.1 Códigos de cierre

Los seis códigos de aplicación viven en el rango privado 4000–4999 (`protocol.CloseXxx`, `WS_CLOSE` en TypeScript):

| Código | Constante | Cuándo lo emite el servidor | ¿El cliente debe reintentar? |
|---|---|---|---|
| `4400` | `CloseInvalidMessage` / `INVALID_MESSAGE` | Durante el handshake: JSON malformado, payload de `session.hello` ilegible o sin `ticket`, o `v` no soportado | Sí, pero solo tras corregir el emisor; es un bug del cliente |
| `4401` | `CloseUnauthenticated` / `UNAUTHENTICATED` | Durante el handshake: el primer mensaje no es `session.hello`, o el ticket no supera la verificación (firma, `exp`, `aud`, `jti` ya consumido, `sub` inválido) | Sí, pidiendo un **ticket nuevo** |
| `4403` | `CloseForbidden` / `FORBIDDEN` | Reservado: sesión válida sin permiso a nivel de conexión (sesión revocada, jugador suspendido). Ninguna ruta del servidor lo emite hoy | No de forma automática |
| `4408` | `CloseHandshakeTimeout` / `HANDSHAKE_TIMEOUT` | No llegó ningún frame legible dentro de los 5 s del deadline de handshake | Sí, con backoff |
| `4429` | `CloseRateLimited` / `RATE_LIMITED` | El token bucket se agotó | Sí, con backoff exponencial ≥ 5 s |
| `4500` | `CloseInternalError` / `INTERNAL_ERROR` | Cola de salida saturada, error de escritura, ping de control fallido | Sí, con backoff exponencial |

Un cierre limpio por fin de sesión o shutdown planificado usa el código estándar `1001`/`1000`. Un frame entrante que supera `EO_WS_MAX_MESSAGE_BYTES` lo corta el `SetReadLimit` del transporte: la lectura falla y la conexión se cierra con el código estándar `1009` (*message too big*), sin llegar a ningún handler. El código de aplicación `MESSAGE_TOO_LARGE` está **reservado** en el catálogo para el día en que el boundary lo emita explícitamente.

El cliente aplica backoff exponencial con jitter: 1 s, 2 s, 4 s, 8 s, máximo 30 s.

## 7. Mensajes cliente → servidor

Los cinco tipos de v1: `session.hello`, `session.ping`, `session.view`, `unit.move`, `unit.cancel_move`. El catálogo es **cerrado** y su cierre está testeado (`TestCatalogoDeComandosEsCerrado`).

### 7.1 `session.hello`

- **Dirección:** cliente → servidor. **Propósito:** autenticar la conexión y establecer la sesión.

```ts
payload: z.object({
  ticket: z.string().min(1).max(4096),
  clientVersion: z.string().max(64).optional(),  // sólo telemetría
}).strict()
```

```json
{
  "v": 1,
  "type": "session.hello",
  "requestId": "0f7a9c1e-6d54-4a1b-9c2e-8b3d5f6a7c10",
  "payload": {
    "ticket": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiJiNjFmM2E4Mi0zZTQ0LTRkOTAtOTIxYi0wY2FhNzc0YmMxMDIiLCJqdGkiOiI5YTQ0YzUxMi1kMWZlLTRjOTctYjBhMS0wZTFmMjM0NTY3ODkiLCJpYXQiOjE3ODg5NTUxNzAsImV4cCI6MTc4ODk1NTIzMCwiYXVkIjoiZ2FtZS1zZXJ2ZXIifQ.9sTn0oGm3xY1r5v8QeJ2bZk7WcA4pLd6HsUvN0iRxYk",
    "clientVersion": "web/0.1.0"
  }
}
```

**Validaciones del servidor:** primer mensaje de la conexión y dentro de 5 s · JSON válido y `v == 1` · `type == "session.hello"` · payload decodificable con `ticket` no vacío · firma HS256 con `EO_AUTH_JWT_SECRET` · `exp` no vencido · `aud == "game-server"` · `sub` es un UUID de jugador · `jti` presente y no consumido (`SET ticket:jti:{jti} NX EX 120`).

**Fallos:** cierre `4400` (mensaje o payload inválido, versión no soportada) o `4401` (tipo distinto de `session.hello`, o autenticación fallida). **Sin `system.error` previo.**

**Efectos:** crea la sesión en el `Hub` · marca presencia en Redis · despacha `PlayerConnected` (que transiciona la ciudad a `ONLINE`) · emite `session.welcome` (`seq: 1`, con el `requestId` del hello) y a continuación el `world.snapshot` inicial centrado en la ciudad del jugador.

### 7.2 `session.ping`

- **Dirección:** cliente → servidor. **Propósito:** keepalive aplicativo, latido de presencia y estimación de RTT / offset de reloj.

```ts
payload: z.object({ clientTimeMs: z.number().int().min(0) }).strict()
```

```json
{ "v": 1, "type": "session.ping", "requestId": "3c1a77d2-91b4-4f0e-8a55-2d9b6e40aa31", "payload": { "clientTimeMs": 1788955199820 } }
```

**Validaciones:** payload decodificable. El servidor **no** valida la coherencia del reloj del cliente: no es autoritativo, y `clientTimeMs` sólo se devuelve como eco.

**Errores:** `INVALID_MESSAGE` (payload ilegible), `RATE_LIMITED`. **Efectos:** renueva el read deadline, renueva el TTL de la clave de presencia en Redis (un fallo de Redis aquí se loguea y no interrumpe nada) y responde `session.pong`. Cadencia recomendada del cliente: la que anuncia `session.welcome.heartbeatIntervalMs`. No sustituye al ping de control del servidor.

### 7.3 `session.view`

- **Dirección:** cliente → servidor. **Propósito:** mover el centro del área de interés cuando el jugador desplaza la cámara.

```ts
payload: z.object({ center: Tile }).strict()   // Tile = { x, y } en tiles, no píxeles
```

```json
{ "v": 1, "type": "session.view", "requestId": "b2d4e6f8-1a3c-4e5d-9f70-112233445566", "payload": { "center": { "x": 100, "y": 100 } } }
```

**Validaciones:** payload decodificable. El centro **no se rechaza por estar fuera del mundo**: se recorta al rango válido (`clamp`) antes de calcular los chunks. `session.view` nunca produce `TARGET_OUT_OF_BOUNDS`. Está sujeto únicamente al token bucket global (§4.2): no hay ningún sub-límite propio.

**Errores:** `INVALID_MESSAGE`, `RATE_LIMITED`, `INTERNAL_ERROR` (cola de comandos saturada, o el game loop no respondió en 2 s).

**Efectos:** el servidor despacha `RequestSnapshot` al game loop con el nuevo centro, recalcula el conjunto de chunks suscritos (radio `EO_INTEREST_RADIUS_CHUNKS` = 2 alrededor del chunk del centro, 5×5 = 25 chunks en el interior del mundo, menos en los bordes) y responde con un **`world.snapshot` completo del área nueva**, llevando el `requestId` de este comando. El terreno incluido en esa respuesta es **sólo el de los chunks que esta sesión aún no tiene** (§8.4).

> El MVP recentra con un snapshot, no con deltas. `Hub.Subscribe` ya devuelve los chunks que entran y los que salen, pero emitir `entity.spawn` / `entity.despawn` a partir de esa diferencia está **pendiente**: hoy esos dos conjuntos sólo se registran en el log. Un cliente correcto reemplaza su escena con lo que trae el snapshot.

### 7.4 `unit.move`

- **Dirección:** cliente → servidor. **Propósito:** expresar la intención de mover una unidad a un tile destino. Es una **intención**, no una orden garantizada.

```ts
payload: z.object({
  unitId: EntityId,   // número entero
  target: Tile,
}).strict()
```

```json
{ "v": 1, "type": "unit.move", "requestId": "7e2b41c0-5a8d-4b63-8c19-0af3d2e19b74", "payload": { "unitId": 48211, "target": { "x": 103, "y": 102 } } }
```

El cliente **nunca** envía una secuencia de posiciones ni un path: la estructura `UnitMovePayload` no tiene dónde guardarlos y el esquema estricto los rechaza en el boundary (`TestElComandoDeMovimientoNoAdmiteRuta`). El path lo calcula el servidor con A\* octile determinista; ver [./movement.md](./movement.md).

**Validaciones del servidor, en el orden exacto en que las aplica `handleMoveUnit`:**

| # | Validación | Error si falla |
|---|---|---|
| 1 | La unidad existe en el estado del mundo | `UNIT_NOT_FOUND` |
| 2 | `unit.playerId == session.playerId` (ownership antes que nada: INV-SEC-004, INV-PLAYER-001) | `UNIT_NOT_OWNED` |
| 3 | `EnsureCanMove()`: `DEAD` → `UNIT_DEAD`; `GARRISONED` → `UNIT_GARRISONED`; cualquier otro estado no admitido → `UNIT_NOT_MOVABLE` | `UNIT_DEAD` / `UNIT_GARRISONED` / `UNIT_NOT_MOVABLE` |
| 4 | `target` dentro de `[0, EO_WORLD_WIDTH) × [0, EO_WORLD_HEIGHT)` | `TARGET_OUT_OF_BOUNDS` |
| 5 | `target` transitable: terreno `walkable` **y** sin marca en la capa de ocupación (`IsWalkable` comprueba las dos cosas) | `TARGET_NOT_WALKABLE` |
| 6 | El tipo de unidad está en el catálogo (`unit.Lookup`) | `INTERNAL_ERROR` |
| 7 | A\* encuentra camino dentro de `EO_PATHFINDING_MAX_NODES` (20000) y `EO_PATHFINDING_MAX_DISTANCE` (256) | `PATH_NOT_FOUND` / `PATH_TOO_LONG` / `TARGET_OUT_OF_BOUNDS` / `TARGET_NOT_WALKABLE`, según el error del pathfinder; cualquier otro → `INTERNAL_ERROR` |
| 8 | La polilínea temporizada se construye sin error | `INTERNAL_ERROR` |

`IDLE`, `MOVING` y `HIDDEN` **pasan** la validación 3. Que `MOVING` la pase es lo que permite reemplazar una orden en curso. Que `HIDDEN` la pase es deliberado: salir de una Safe Zone es legítimo y moverse revela la unidad.

**Caso especial: el destino es la posición actual.** No es un error. El servidor cancela el movimiento activo si lo había (razón `CANCELLED_BY_PLAYER`) y responde `unit.move.accepted` con **`movementId: 0`**, sin crear ningún movimiento: es idempotente y evita polilíneas degeneradas de un solo punto. El origen contra el que se compara es la posición **autoritativa ahora mismo** —el tile que ocupa según su polilínea si se está moviendo—, no la posición consolidada en la base de datos, que puede ir por detrás.

**Errores adicionales:** `RATE_LIMITED`, `INVALID_MESSAGE` (payload ilegible o `requestId` ausente).

**Efectos en caso de éxito**, todos en la fase 2 del tick y **todos en RAM**:

1. Si la unidad tenía un movimiento activo, se cancela **antes** de registrar el nuevo —en ningún instante existen dos movimientos activos de la misma unidad (INV-MOVE-001)— y se emite `unit.movement.cancelled` con `reason: "REPLACED"`.
2. Se registra el movimiento nuevo en el estado del mundo con su polilínea temporizada completa, `startTimeMs` y `arrivalTimeMs`.
3. `unit.status = MOVING` y la unidad se marca sucia.
4. La escritura durable se **encola** en la cola de persistencia (`Job{Name: "movement.start"}`) y la ejecutan los workers fuera del tick: 4 workers, hasta **3 intentos** con backoff lineal y compensación `OnPermanentFailure` si se agotan. **El tick nunca ejecuta I/O de PostgreSQL** (canon §6, [../architecture/persistence.md](../architecture/persistence.md)).
5. Se emite `unit.move.accepted` **al jugador solicitante** (a todas sus sesiones, con el `requestId`) y `unit.movement.started` **a los suscriptores del chunk de origen**, incluido el solicitante.

> **Ventana de riesgo documentada.** La confirmación al cliente ocurre antes del COMMIT. Si el proceso muere entre la aceptación y la escritura —típicamente pocas decenas de milisegundos—, ese movimiento se pierde y la unidad queda en su última posición consolidada. Es un RPO conocido y aceptado, no un descuido: el precio de que el tick no bloquee.

En caso de fallo se emite `unit.move.rejected` al solicitante con el `code` correspondiente; no se emite `system.error` adicional (`unit.move.rejected` es el error tipado de este comando).

### 7.5 `unit.cancel_move`

- **Dirección:** cliente → servidor. **Propósito:** detener el movimiento activo de una unidad en el tile alcanzado.

```ts
payload: z.object({ unitId: EntityId }).strict()
```

```json
{ "v": 1, "type": "unit.cancel_move", "requestId": "c9a01b7f-2e44-4d6a-b8f1-77e0c9d31a55", "payload": { "unitId": 48211 } }
```

**Validaciones:** la unidad existe · ownership. A diferencia de `unit.move`, los rechazos de este comando viajan como **`system.error`** (`UNIT_NOT_FOUND`, `UNIT_NOT_OWNED`) con el `requestId` del comando, no como `unit.move.rejected`.

Si la unidad existe y es del jugador pero **no tiene movimiento activo**, la operación es un **no-op silencioso**: no hay error y tampoco se emite `unit.movement.cancelled`, porque no hay ningún movimiento que cancelar.

**Efectos cuando sí había movimiento activo:** la unidad queda **snapeada al waypoint alcanzado** en ese instante (nunca a medio tile), se limpia el movimiento del estado, `unit.status = IDLE`, se encola la escritura durable (`Job{Name: "movement.cancel"}` → `unit_movements.status = CANCELLED`) y se emite `unit.movement.cancelled` con `reason: "CANCELLED_BY_PLAYER"` a los suscriptores del chunk donde se detuvo. La cancelación se aplica en el tick que consume el comando, no en el instante exacto del frame: el tiempo del juego avanza solo en el loop.

## 8. Mensajes servidor → cliente

Los catorce tipos de v1. Ninguno de estos payloads es estricto (S-7).

### 8.1 `session.welcome`

- **Propósito:** confirmar la sesión y entregar la configuración mínima que el cliente necesita para no hardcodear nada. Lleva el `requestId` del `session.hello`. Siempre `seq: 1`.

```ts
payload: z.object({
  sessionId: Uuid,
  playerId: Uuid,
  serverTimeMs: EpochMs,
  tickDurationMs: z.number().int().min(1),
  heartbeatIntervalMs: z.number().int().min(1000),
  world: z.object({
    width: z.number().int().min(1),
    height: z.number().int().min(1),
    chunkSize: z.number().int().min(1),
  }),
})
```

```json
{
  "v": 1, "type": "session.welcome", "seq": 1, "ts": 1788955199950,
  "requestId": "0f7a9c1e-6d54-4a1b-9c2e-8b3d5f6a7c10",
  "payload": {
    "sessionId": "5f0c2b18-7a3d-4e51-9c88-2b41f0e9d7a4",
    "playerId": "b61f3a82-3e44-4d90-921b-0caa774bc102",
    "serverTimeMs": 1788955199950,
    "tickDurationMs": 100,
    "heartbeatIntervalMs": 10000,
    "world": { "width": 512, "height": 512, "chunkSize": 32 }
  }
}
```

`tickDurationMs = 1000 / EO_TICK_RATE_HZ` (100 ms a 10 Hz; la configuración exige que `EO_TICK_RATE_HZ` divida exactamente a 1000, precisamente para que sea un entero). `heartbeatIntervalMs` deriva de `EO_PRESENCE_HEARTBEAT_SECONDS` (10 s) y siempre es estrictamente menor que `EO_PRESENCE_TTL_SECONDS` (30 s). El cliente usa `serverTimeMs` y `ts` para calcular su offset de reloj y **jamás** usa su reloj local como referencia de simulación.

Este payload **no** transporta el radio de interés, los límites de transporte, el estado del jugador (civilización, facción, era, población) ni la vista inicial. El radio de interés es observable en el número de chunks del snapshot; los límites de transporte son los de la sección 4. Publicar límites y perfil de jugador dentro del welcome es `TBD (fuera de MVP)`.

### 8.2 `session.pong`

- **Propósito:** responder a `session.ping` y permitir estimar RTT y offset. Lleva el `requestId` del ping.

```ts
payload: z.object({ clientTimeMs: z.number().int().min(0), serverTimeMs: EpochMs })
```

```json
{ "v": 1, "type": "session.pong", "seq": 87, "ts": 1788955199860, "requestId": "3c1a77d2-91b4-4f0e-8a55-2d9b6e40aa31", "payload": { "clientTimeMs": 1788955199820, "serverTimeMs": 1788955199860 } }
```

Cálculo del cliente: `rtt = nowMs - clientTimeMs`; `offset ≈ serverTimeMs - (clientTimeMs + rtt/2)`. **Errores:** ninguno propio. **Efectos:** ninguno.

### 8.3 `system.error`

- **Propósito:** error genérico no ligado a un comando con respuesta tipada propia. Lleva `requestId` en el envelope si el error es atribuible a un comando concreto.

```ts
payload: z.object({
  code: ErrorCode,                                  // sección 9, estable
  message: z.string().max(512),                     // texto humano, NUNCA usado por la lógica
  details: z.record(z.string(), z.unknown()).optional(),
})
```

```json
{ "v": 1, "type": "system.error", "seq": 12, "ts": 1788955201110, "requestId": "c9a01b7f-2e44-4d6a-b8f1-77e0c9d31a55", "payload": { "code": "UNIT_NOT_OWNED", "message": "la unidad no te pertenece" } }
```

El `requestId` va **en el envelope**, no dentro del payload: el payload sólo tiene `code`, `message` y opcionalmente `details`.

Regla dura: **la lógica de control del cliente usa `code`, jamás `message`**. `details` es informativo y puede cambiar sin romper compatibilidad. Cada emisión incrementa la métrica `eo_protocol_errors_total{code}`.

### 8.4 `world.snapshot`

- **Propósito:** estado del área de interés. Se envía tras el handshake y como respuesta a cada `session.view`. Es el único mensaje que reconstruye estado desde cero.

```ts
payload: z.object({
  serverTimeMs: EpochMs,
  tick: z.number().int().min(0),
  chunks: z.array(ChunkRef),         // { cx, cy }
  terrain: z.array(ChunkTerrain),    // { cx, cy, size, terrain }  terrain = base64
  units: z.array(UnitView),
  cities: z.array(CityView),
  territories: z.array(TerritoryView),
})
```

```ts
// packages/protocol/src/v1/common.ts
interface UnitView {
  id: number;                 // = unitId
  playerId: string;           // uuid del propietario
  cityId: number | null;
  unitType: 'VILLAGER';       // MVP
  x: number; y: number;       // tiles, planos: no hay objeto position
  hp: number; maxHp: number;
  status: 'IDLE' | 'MOVING' | 'GARRISONED' | 'HIDDEN' | 'DEAD';
  movement: ActiveMovement | null;
}

interface ActiveMovement {
  movementId: number;
  path: Array<{ x: number; y: number; tMs: number }>;
  startTimeMs: number;
  arrivalTimeMs: number;
  target: { x: number; y: number };
}

interface ChunkTerrain {
  cx: number; cy: number;
  size: number;               // = EO_CHUNK_SIZE (32)
  terrain: string;            // base64 de size*size bytes, fila-mayor
}
```

```json
{
  "v": 1, "type": "world.snapshot", "seq": 2, "ts": 1788955199960,
  "payload": {
    "serverTimeMs": 1788955199960, "tick": 4231000,
    "chunks": [ { "cx": 3, "cy": 3 }, { "cx": 4, "cy": 3 } ],
    "terrain": [ { "cx": 3, "cy": 3, "size": 32, "terrain": "AAAAAQEAAAIC...(1368 caracteres)" } ],
    "units": [
      { "id": 48211, "playerId": "b61f3a82-3e44-4d90-921b-0caa774bc102", "cityId": 1204, "unitType": "VILLAGER", "x": 100, "y": 100, "hp": 40, "maxHp": 40, "status": "IDLE", "movement": null },
      { "id": 48212, "playerId": "b61f3a82-3e44-4d90-921b-0caa774bc102", "cityId": 1204, "unitType": "VILLAGER", "x": 101, "y": 99, "hp": 40, "maxHp": 40, "status": "MOVING",
        "movement": { "movementId": 77301, "path": [ { "x": 100, "y": 99, "tMs": 0 }, { "x": 101, "y": 99, "tMs": 600 }, { "x": 102, "y": 99, "tMs": 1200 } ], "startTimeMs": 1788955198800, "arrivalTimeMs": 1788955200000, "target": { "x": 102, "y": 99 } } }
    ],
    "cities": [ { "id": 1204, "ownerPlayerId": "b61f3a82-3e44-4d90-921b-0caa774bc102", "name": "Roma Nova", "centerX": 98, "centerY": 99, "era": "STONE_AGE", "population": 3, "populationLimit": 20, "presenceState": "ONLINE", "protectionUntilMs": null } ],
    "territories": []
  }
}
```

**Terreno: una sola vez por chunk y por sesión.** El terreno es inmutable, así que reenviarlo en cada movimiento de cámara sería malgastar ancho de banda. La sesión recuerda qué chunks ya lo recibieron (`NeedsTerrain` / `MarkTerrainSent`) y el servidor filtra `terrain[]` antes de enviar: el primer snapshot trae los 25 chunks completos y los siguientes traen únicamente los chunks nuevos. `chunks[]` sigue listando **todos** los chunks suscritos, tengan terreno adjunto o no.

**Tamaño.** Un chunk de 32×32 son 1024 bytes crudos = 1368 caracteres en base64. Los 25 chunks del primer snapshot rondan por tanto los **35 KB**, por encima de `EO_WS_MAX_MESSAGE_BYTES`. No es una contradicción: ese límite se aplica hoy **al sentido entrante** (`SetReadLimit`), y el snapshot no se trunca. La amortización real la da el filtro de terreno: a partir del segundo snapshot el payload cae a los pocos KB de entidades más los chunks recién descubiertos. Truncar el snapshot y diferir el resto a `entity.spawn` es `TBD (fuera de MVP)`: no hay campo `truncated` en v1.

**Nunca** se retransmite el mundo completo: `world.snapshot` cubre exclusivamente el área de interés (radio 2 chunks; 25 chunks en el interior del mundo, menos en los bordes).

**`requestId`.** El snapshot inicial del handshake se emite **sin `requestId`** (el `requestId` del hello lo lleva el `session.welcome` que lo precede). El snapshot de respuesta a un `session.view` sí lleva el `requestId` de ese comando.

`territories` está en el contrato y hoy viaja **siempre vacío**: la proyección de territorios al snapshot está pendiente.

### 8.5 `entity.spawn`

- **Propósito:** una entidad entra en escena. Sin `requestId` (es broadcast al chunk).

```ts
payload: z.object({ unit: UnitView })
```

```json
{ "v": 1, "type": "entity.spawn", "seq": 41, "ts": 1788955203100, "payload": { "unit": { "id": 50877, "playerId": "1c9e2f40-7bb1-4a02-98d5-4d61aa03e7f9", "cityId": 1207, "unitType": "VILLAGER", "x": 118, "y": 104, "hp": 40, "maxHp": 40, "status": "IDLE", "movement": null } } }
```

**No hay campo `reason`.** El payload es exactamente `{ unit }`. Hoy el servidor lo emite en un único punto: al incorporar un jugador al mundo (`IntroducePlayer`), una vez por cada uno de sus 3 aldeanos iniciales. El spawn por entrada en el área de interés está pendiente (ver la nota de 7.3).

El cliente debe tratar un `entity.spawn` de una entidad que ya conoce como **reemplazo total** del estado local de esa entidad (upsert), no como error.

### 8.6 `entity.update`

- **Propósito:** delta parcial de una entidad ya visible. Solo viajan los campos que cambiaron. Sin `requestId`.

```ts
payload: z.object({
  id: EntityId,
  x: WorldCoordinate.optional(),
  y: WorldCoordinate.optional(),
  hp: z.number().int().min(0).optional(),
  status: UnitStatus.optional(),
  cityId: EntityId.nullable().optional(),
  movement: ActiveMovement.nullable().optional(),
})
```

```json
{ "v": 1, "type": "entity.update", "seq": 55, "ts": 1788955204000, "payload": { "id": 50877, "x": 119, "y": 104 } }
```

Es un delta de verdad: en Go los campos opcionales son punteros con `omitempty`, y un campo sin cambios **no viaja** (`TestEntityUpdateEsUnDeltaDeVerdad`). Nótese la ausencia de `kind` y de `chunkId`: no existen en v1.

**Sí se emite durante el movimiento.** En la fase 3 del tick, cada vez que una unidad en movimiento **cambia de tile**, el servidor emite un `entity.update` con `x` e `y` al chunk correspondiente. No es un mensaje por tick —a 10 Hz con pasos de 600 ms la mayoría de los ticks no producen ninguno—, sino un mensaje por tile cruzado. Es la corrección autoritativa que ancla la interpolación del cliente, que entretanto reconstruye la posición analíticamente desde la polilínea de `unit.movement.started`.

### 8.7 `entity.despawn`

- **Propósito:** una entidad deja de ser visible o deja de existir. Sin `requestId`.

```ts
payload: z.object({
  id: EntityId,
  reason: z.enum(['OUT_OF_INTEREST', 'DEAD', 'GARRISONED', 'HIDDEN', 'REMOVED']),
})
```

```json
{ "v": 1, "type": "entity.despawn", "seq": 56, "ts": 1788955204010, "payload": { "id": 50877, "reason": "OUT_OF_INTEREST" } }
```

Las cinco razones son las constantes `protocol.DespawnOutOfInterest`, `DespawnDead`, `DespawnGarrisoned`, `DespawnHidden` y `DespawnRemoved`, y cubren cada caso sin ambigüedad, incluidos el ocultamiento en una Safe Zone (`HIDDEN`) y la entrada en guarnición (`GARRISONED`):

| `reason` | Significado para el cliente |
|---|---|
| `OUT_OF_INTEREST` | Sigue existiendo, ya no la ves. Eliminar de la escena **sin** efectos de destrucción. |
| `DEAD` | Murió. Efectos de destrucción. |
| `GARRISONED` | Entró en una guarnición. Sin efectos de destrucción. |
| `HIDDEN` | Se ocultó (Safe Zone). Sin efectos de destrucción. |
| `REMOVED` | Desapareció del mundo por otra causa (corrección administrativa, recovery). Sin efectos de destrucción. |

El tipo está definido en ambos lados del contrato, pero **ninguna ruta del servidor lo emite todavía**: es el correlato pendiente del spawn por interés descrito en 7.3, y de las specs de [./safe-zones.md](./safe-zones.md) y [./garrison.md](./garrison.md).

### 8.8 `city.update`

- **Propósito:** delta parcial de una ciudad visible, incluida la máquina de presencia/protección. Sin `requestId`.

```ts
payload: z.object({
  id: EntityId,
  name: z.string().min(1).max(64).optional(),
  population: z.number().int().min(0).optional(),
  populationLimit: z.number().int().min(0).optional(),
  presenceState: PresenceState.optional(),          // ONLINE | OFFLINE_PENDING | PROTECTED
  protectionUntilMs: EpochMs.nullable().optional(),
})
```

```json
{ "v": 1, "type": "city.update", "seq": 63, "ts": 1788955500000, "payload": { "id": 1204, "presenceState": "PROTECTED" } }
```

Es un **delta**, no el estado completo de la ciudad: el estado completo sólo viaja en `world.snapshot.cities[]` como `CityView`, que además incluye `ownerPlayerId`, `centerX`, `centerY` y `era` —campos que `city.update` no puede modificar—. Hoy el servidor emite dos variantes: al incorporar un jugador (`id`, `name`, `population`, `populationLimit`) y en cada transición de presencia (`id`, `presenceState`).

El estado de presencia lo decide **exclusivamente el servidor** (transición a `PROTECTED` tras `EO_CITY_OFFLINE_PROTECTION_COOLDOWN_SECONDS` = 300 s). El cliente lo observa; nunca lo propone.

### 8.9 `territory.update`

- **Propósito:** geometría y control de un territorio visible. Geometría rectangular en MVP. Sin `requestId`.

```ts
payload: z.object({ territory: TerritoryView })

interface TerritoryView {
  id: number;
  name: string;
  minX: number; minY: number; maxX: number; maxY: number;
  ownerType: 'NONE' | 'PLAYER' | 'CLAN' | 'FACTION';
  ownerId: string | null;      // consistente con ownerType: NONE ⟺ null
  contested: boolean;
}
```

```json
{ "v": 1, "type": "territory.update", "seq": 64, "ts": 1788955500100, "payload": { "territory": { "id": 310, "name": "Latium", "minX": 96, "minY": 96, "maxX": 127, "maxY": 127, "ownerType": "NONE", "ownerId": null, "contested": false } } }
```

El payload envuelve el estado completo bajo la clave `territory`; no es un delta parcial. `ownerType = 'NONE'` ⟺ `ownerId = null` es la misma consistencia que impone el CHECK de `territory_control` en la base de datos. Como `entity.despawn`, el tipo está definido pero **ninguna ruta del servidor lo emite todavía**.

### 8.10 `unit.move.accepted`

- **Propósito:** acuse **al solicitante** de que la intención fue validada y el movimiento existe. Lleva siempre `requestId`.

```ts
payload: z.object({ unitId: EntityId, movementId: EntityId })
```

```json
{ "v": 1, "type": "unit.move.accepted", "seq": 101, "ts": 1788955200000, "requestId": "7e2b41c0-5a8d-4b63-8c19-0af3d2e19b74", "payload": { "unitId": 48211, "movementId": 77345 } }
```

Es deliberadamente mínimo: **no duplica la polilínea** ni repite tiempos. La geometría llega en `unit.movement.started`, que el solicitante también recibe por ser suscriptor del chunk de origen. Así el broadcast tiene una sola forma canónica y el ack no paga el coste de serializar la polilínea dos veces.

`movementId: 0` es el valor reservado para el caso "el destino ya es la posición actual" (§7.4): la orden se acepta, pero no se creó ningún movimiento.

Se envía con `SendToPlayer`, es decir **a todas las sesiones del jugador**, no sólo a la que emitió el comando: si el jugador tiene dos pestañas abiertas, ambas ven el acuse.

### 8.11 `unit.move.rejected`

- **Propósito:** la intención no pasó validación. Error tipado del comando `unit.move`. Lleva siempre `requestId`.

```ts
payload: z.object({
  unitId: EntityId,
  code: ErrorCode,          // UNIT_NOT_FOUND | UNIT_NOT_OWNED | UNIT_DEAD | UNIT_GARRISONED |
                            // UNIT_NOT_MOVABLE | TARGET_OUT_OF_BOUNDS | TARGET_NOT_WALKABLE |
                            // PATH_NOT_FOUND | PATH_TOO_LONG | INTERNAL_ERROR
  message: z.string().max(512),
})
```

```json
{ "v": 1, "type": "unit.move.rejected", "seq": 102, "ts": 1788955200100, "requestId": "7e2b41c0-5a8d-4b63-8c19-0af3d2e19b74", "payload": { "unitId": 48211, "code": "TARGET_NOT_WALKABLE", "message": "el destino no es transitable" } }
```

El payload **no tiene `details`**: sólo `unitId`, `code` y `message`. Como `unit.move.accepted`, se entrega a todas las sesiones del jugador.

Ningún rechazo modifica estado: si `unit.move` es rechazado, el movimiento previo de la unidad (si existía) **sigue intacto**. La cancelación del movimiento anterior solo ocurre cuando la orden nueva ya ha superado todas las validaciones y su polilínea está construida.

### 8.12 `unit.movement.started`

- **Propósito:** hecho consumado `UnitMovementStarted`. Contiene **toda** la información necesaria para que el cliente anime el trayecto completo sin volver a preguntar. Broadcast a los suscriptores del chunk de **origen**; sin `requestId`.

```ts
payload: z.object({
  unitId: EntityId,
  movement: ActiveMovement,   // { movementId, path[], startTimeMs, arrivalTimeMs, target }
})
```

```json
{
  "v": 1, "type": "unit.movement.started", "seq": 103, "ts": 1788955200000,
  "payload": {
    "unitId": 48211,
    "movement": {
      "movementId": 77345,
      "path": [
        { "x": 0, "y": 0, "tMs": 0 },
        { "x": 1, "y": 0, "tMs": 600 },
        { "x": 2, "y": 1, "tMs": 1449 },
        { "x": 3, "y": 1, "tMs": 2409 },
        { "x": 4, "y": 1, "tMs": 2769 }
      ],
      "startTimeMs": 1788955200000,
      "arrivalTimeMs": 1788955202769,
      "target": { "x": 4, "y": 1 }
    }
  }
}
```

El ejemplo es el **caso numérico canónico** del modelo de movimiento: GRASSLAND ortogonal (600) → GRASSLAND diagonal (849) → FOREST ortogonal (960) → ROAD ortogonal (360), acumulados. Nótese que el payload **no** lleva `from` ni `tick`: el origen es `path[0]` y el instante es `ts`.

**Por qué la polilínea completa viaja en un solo mensaje.** Es la decisión de diseño central del protocolo y no es negociable:

1. **Cero chatter.** Sin ella, el servidor tendría que emitir un delta por tile con toda la geometría implícita: con cientos de unidades visibles, el coste de serialización crece con la duración del movimiento. Con ella, un trayecto de 200 tiles cuesta **un** mensaje geométrico más un `entity.update` mínimo por tile cruzado.
2. **Interpolación local exacta.** La posición autoritativa en el instante `T` es el último waypoint con `tMs <= (T - startTimeMs)`; entre waypoints el cliente interpola visualmente. Cliente y servidor calculan lo mismo con la misma fórmula, sin adivinar y sin *client-side prediction* especulativa.
3. **Reconstrucción analítica.** El modelo de movimiento exige que la posición sea derivable sin replay de ticks. El mensaje es el reflejo exacto de lo que se persiste en `unit_movements.path` (`jsonb` con `[{"x":int,"y":int,"tMs":int}, ...]`); tras un reinicio del servidor, el estado que se recarga y el que el cliente ya tiene coinciden.
4. **Tolerancia a jitter.** Un pico de latencia de 800 ms no rompe la animación: el cliente sigue teniendo el trayecto entero y solo corrige el offset de reloj.

**Restricciones de la polilínea:** `path` nunca está vacío (`TimedPath` exige `min(1)`); `path[0]` es siempre el origen con `tMs = 0`; `tMs` es estrictamente creciente; `arrivalTimeMs = startTimeMs + path[last].tMs`; `target` coincide con el último waypoint.

**No hay cota máxima de waypoints.** `EO_PATHFINDING_MAX_DISTANCE` (256) acota la distancia Chebyshev origen→destino y `EO_PATHFINDING_MAX_NODES` (20000) el trabajo del A\*, pero **ninguno acota la longitud del camino**: un rodeo alrededor de un obstáculo entre dos tiles vecinos puede producir decenas de waypoints. Una cota explícita de la polilínea es `TBD (fuera de MVP)`; ver [../decisions/ADR-011-movement-timed-polyline.md](../decisions/ADR-011-movement-timed-polyline.md).

**Duración de cada segmento** (aritmética exacta, `movement.StepDurationMs`), redondeando **cada** segmento al milisegundo más cercano antes de acumular —truncar regalaría casi un segundo de ventaja en mil pasos—:

```
ms = (baseMsPerTile * costUnits + 5) / 10
si diagonal:  ms = (ms * 1414214 + 500000) / 1000000
si ms < 1:    ms = 1
```

Para `VILLAGER` (`baseMsPerTile = 600`): GRASSLAND (costUnits 10) 600 / 849 · FOREST (16) 960 / 1358 · HILL (18) 1080 / 1527 · ROAD (6) 360 / 509, en ortogonal / diagonal.

### 8.13 `unit.movement.completed`

- **Propósito:** hecho consumado `UnitMovementCompleted`. La unidad llegó al destino. Broadcast al chunk de llegada; sin `requestId`.

```ts
payload: z.object({ unitId: EntityId, movementId: EntityId, finalPosition: Tile })
```

```json
{ "v": 1, "type": "unit.movement.completed", "seq": 118, "ts": 1788955202770, "payload": { "unitId": 48211, "movementId": 77345, "finalPosition": { "x": 4, "y": 1 } } }
```

El campo es `finalPosition`, no `position`; y no hay `arrivalTimeMs`, ni `tick`, ni `chunkId` en el payload: el instante de emisión es `ts` y el `arrivalTimeMs` ya lo conocía el cliente desde `unit.movement.started`.

Se emite en el **primer tick cuyo tiempo de simulación alcanza o supera `arrivalTimeMs`**, por lo que `ts` puede ser hasta un período de tick (100 ms) posterior a la llegada teórica. El cliente debe usar `arrivalTimeMs` para la animación y `finalPosition` como verdad.

**Efectos:** se limpia el movimiento del estado en RAM, `unit.status = IDLE`, la unidad se marca sucia, y se encolan **dos** escrituras durables independientes: `Job{Name: "movement.complete"}` cierra la fila de `unit_movements` como `COMPLETED` de forma inmediata, mientras que la posición consolidada de `units.(x, y)` viaja en el volcado periódico de entidades sucias (`EO_PERSISTENCE_FLUSH_INTERVAL_TICKS` = 50, es decir cada 5 s a 10 Hz). Ambas van por la cola de persistencia, nunca dentro del tick.

> **Consecuencia honesta.** Entre el `COMPLETED` y el volcado siguiente hay una ventana en la que la base tiene el movimiento cerrado y una `units.(x, y)` anterior. La recuperación la cubre: `simulation.Hydrate` reconstruye la posición desde la polilínea del movimiento vencido y la unidad aparece en el destino. No es RPO 0 sobre `units.(x, y)`, y así debe documentarse en [../database/persistence-strategy.md](../database/persistence-strategy.md).

Tras un reinicio, un movimiento cuyo `arrival_time_ms <= now` se completa al arrancar (snap al tile final) y su fila queda como `COMPLETED`; un movimiento aún en curso se reanuda desde la polilínea; una polilínea inválida cierra el movimiento como `FAILED` y la unidad se queda donde estaba —nunca se teletransporta a nadie por un dato dudoso—.

### 8.14 `unit.movement.cancelled`

- **Propósito:** hecho consumado de cancelación. Broadcast al chunk donde la unidad se detuvo; **sin `requestId`**, incluso cuando la causa es un `unit.cancel_move` del propio jugador (es un broadcast al chunk, no una respuesta dirigida).

```ts
payload: z.object({
  unitId: EntityId,
  movementId: EntityId,
  stoppedAt: Tile,
  reason: z.enum(['REPLACED', 'CANCELLED_BY_PLAYER', 'PATH_BLOCKED', 'UNIT_DEAD', 'SERVER']),
})
```

```json
{ "v": 1, "type": "unit.movement.cancelled", "seq": 130, "ts": 1788955201500, "payload": { "unitId": 48211, "movementId": 77345, "stoppedAt": { "x": 2, "y": 1 }, "reason": "CANCELLED_BY_PLAYER" } }
```

`movementId` **nunca es `null`**: el mensaje solo se emite si existía un movimiento activo que cancelar. La cancelación de una unidad sin movimiento activo es un no-op silencioso (§7.5).

Las cinco razones son las constantes de `movement.CancelReason`:

| `reason` | Cuándo |
|---|---|
| `REPLACED` | Una orden `unit.move` aceptada reemplaza al movimiento anterior. Llega inmediatamente **antes** del `unit.movement.started` de la orden nueva. |
| `CANCELLED_BY_PLAYER` | `unit.cancel_move` explícito, o `unit.move` cuyo destino ya era la posición actual. |
| `PATH_BLOCKED` | La ruta dejó de ser válida durante la simulación. |
| `UNIT_DEAD` | La unidad murió con un movimiento en curso. |
| `SERVER` | Cancelación de oficio del servidor (recovery, compensación de una escritura fallida). |

`stoppedAt` es siempre un tile completo, el waypoint alcanzado en el instante de la cancelación, nunca una fracción. El estado `FAILED` de `unit_movements` no tiene mensaje propio en v1: un fallo interno durante la simulación se reporta como `system.error INTERNAL_ERROR` más el `entity.update` correctivo.

## 9. Códigos de error

El catálogo es de **22 códigos** y es cerrado. Vive en `errors.ts` (`ErrorCode`) y en `codes.go` (`AllErrorCodes`); un contract test en Go (`TestCatalogoDeErroresCoincideConElExportado`) compara ambos contra `error-codes.json` y rompe la CI si divergen.

El formato es siempre `{ code, message, details? }` dentro de `system.error`, o `{ unitId, code, message }` dentro de `unit.move.rejected`. La columna "análogo" es orientativa para logging y métricas: **el protocolo no usa códigos HTTP**.

| `code` | Análogo HTTP / WS | Cierra la conexión | Cuándo se emite |
|---|---|---|---|
| `UNAUTHORIZED` | 401 / WS `4401` | Sí (`4401`) | Fallo de autenticación. Hoy el handshake cierra con `4401` **sin** enviar el mensaje; el código está reservado para el día en que el boundary lo emita |
| `FORBIDDEN` | 403 / WS `4403` | Solo si es a nivel de sesión | Reservado: sesión válida, operación no permitida para ese jugador |
| `INVALID_MESSAGE` | 400 / WS `4400` | Solo en el handshake | JSON malformado, `type` desconocido, payload que no decodifica, `requestId` ausente en un comando durable, propiedad desconocida (esquema estricto). Dentro de la sesión se emite como `system.error` y la conexión **continúa** |
| `UNSUPPORTED_VERSION` | 426 / WS `4400` | Solo en el handshake | `v` distinto de las versiones soportadas |
| `RATE_LIMITED` | 429 / WS `4429` | Sí (`4429`) | Token bucket agotado (20/s, burst 40). El `system.error` y el cierre son simultáneos |
| `MESSAGE_TOO_LARGE` | 413 / WS `1009` | Sí | Reservado. Hoy el exceso lo corta `SetReadLimit` y el cierre lleva el código estándar `1009` |
| `UNIT_NOT_FOUND` | 404 | No | `unitId` inexistente en `unit.move` (→ `unit.move.rejected`) o en `unit.cancel_move` (→ `system.error`) |
| `UNIT_NOT_OWNED` | 403 | No | La unidad existe pero no pertenece al jugador de la sesión |
| `UNIT_NOT_MOVABLE` | 409 | No | El estado actual de la unidad no admite órdenes de movimiento |
| `UNIT_DEAD` | 409 | No | `unit.status == DEAD` |
| `UNIT_GARRISONED` | 409 | No | `unit.status == GARRISONED`; requiere salir de la guarnición primero |
| `INVALID_TARGET` | 400 | No | Reservado: destino mal formado. El caso "destino igual a la posición actual" **no** lo usa: se acepta con `movementId: 0` (§7.4) |
| `TARGET_OUT_OF_BOUNDS` | 400 | No | Tile fuera de `[0, EO_WORLD_WIDTH) × [0, EO_WORLD_HEIGHT)`. **No** aplica a `session.view`, cuyo centro se recorta |
| `TARGET_NOT_WALKABLE` | 409 | No | Terreno `MOUNTAIN`/`WATER` o tile marcado en la capa de ocupación. El MVP **rechaza**, no busca tile cercano |
| `PATH_NOT_FOUND` | 409 | No | A\* agotó el frontier sin alcanzar el destino (destino encerrado) |
| `PATH_TOO_LONG` | 409 | No | Se excedió `EO_PATHFINDING_MAX_NODES` (20000) o `EO_PATHFINDING_MAX_DISTANCE` (256) |
| `CITY_NOT_FOUND` | 404 | No | Reservado: referencia a una ciudad inexistente |
| `CITY_PROTECTED` | 409 | No | Reservado: operación bloqueada por `presence_state = PROTECTED` |
| `TREATY_REQUIRED` | 409 | No | Reservado: requiere un `treaty` `ACTIVE` con `allows_garrison` |
| `POPULATION_LIMIT_REACHED` | 409 | No | Reservado: `population_limit` alcanzado (`eras.population_cap`) |
| `INTERNAL_ERROR` | 500 / WS `4500` | Solo si es irrecuperable | Cola de comandos saturada, el game loop no responde en 2 s, tipo de unidad no catalogado, polilínea no construible. El `message` nunca expone detalles internos ni credenciales |
| `NOT_IMPLEMENTED` | 501 | No | Reservado: ruta de código para funcionalidad fuera de MVP |

Los códigos marcados **Reservado** están definidos y estables en v1, pero ninguna ruta del servidor los emite todavía: sus comandos naturales (asedio, garrison, entrenamiento, revocación de sesión) están fuera de MVP.

## 10. Idempotencia

La idempotencia se apoya **hoy exclusivamente en Redis**: `IdempotencyStore` reserva la clave `idem:{playerId}:{requestId}` con `SETNX` y TTL de **300 s** antes de ejecutar el comando (INV-SEC-007).

| Regla | Detalle |
|---|---|
| I-1 | Los esquemas exigen `requestId` con formato UUID en los cinco comandos. El servidor lo **hace cumplir** hoy en `unit.move` y `unit.cancel_move`: `requestId` vacío → `system.error INVALID_MESSAGE` y el comando no se ejecuta. La validación del **formato** UUID en Go es `TBD (fuera de MVP)`: hoy la garantiza el esquema estricto del cliente. |
| I-2 | La deduplicación se aplica **sólo a los comandos durables** (`unit.move`, `unit.cancel_move`). `session.hello`, `session.ping` y `session.view` no reservan clave: no crean efectos durables. |
| I-3 | El alcance de la deduplicación es `(playerId, requestId)`. Dos jugadores distintos pueden usar el mismo `requestId` sin interferir. |
| I-4 | El marcador `"pending"` reserva la clave **antes** de ejecutar. Si dos copias del mismo comando llegan a la vez, sólo una gana el `SETNX`. |
| I-5 | Un `requestId` repetido dentro de la ventana **no re-ejecuta** el comando: hoy se **descarta en silencio** y el cliente no recibe ninguna respuesta. Guardar la respuesta original y reproducirla (`IdempotencyStore.Complete` ya existe, sin usar) es `TBD (fuera de MVP)`. |
| I-6 | Un `requestId` reutilizado con un **payload distinto** dentro de la ventana se descarta igual: es un bug del cliente, no una vía para reordenar intención. |
| I-7 | Pasados los 300 s, el mismo `requestId` se trata como comando nuevo. El cliente **debe** generar un UUID por intención, no reutilizarlos. |
| I-8 | Los reintentos tras reconexión son seguros: reenviar `unit.move` con el mismo `requestId` no crea un segundo movimiento. Es el mecanismo previsto para redes inestables. Como el duplicado no responde (I-5), el cliente debe converger con el `world.snapshot` de la reconexión, no esperando un ack. |
| I-9 | **Si Redis no responde, el comando se ejecuta igualmente.** Se prefiere dejar jugar a bloquear al jugador por una caída de la caché. El fallo se registra con nivel `warn`. Es una decisión consciente: durante una indisponibilidad de Redis la garantía de I-5 queda suspendida y un reintento puede duplicar el efecto. |

La tabla `idempotency_keys` existe en la migración `000001` (con su índice `idempotency_keys_expiry_idx` sobre `expires_at`) y es la sede prevista de la variante durable, pero **el servidor no escribe en ella todavía**: la deduplicación es hoy Redis-only, y por tanto se pierde si Redis se vacía. Lo mismo vale para la tabla `sessions`: existe en el esquema, pero la sesión vive en RAM y en la clave `presence:player:{playerId}` de Redis.

## 11. Ordenamiento y entrega

- `seq` es **uint64 monótono estrictamente creciente por conexión**, empieza en `1`, sin huecos, y se reinicia en cada conexión. `session.welcome` es siempre `seq: 1` y el snapshot inicial `seq: 2`.
- WebSocket sobre TCP garantiza orden y entrega dentro de la conexión. Un hueco en `seq` indica un bug del servidor: el cliente lo loguea y **reconecta** para forzar un `world.snapshot` limpio.
- **Regla del cliente:** mantener `lastSeenSeq`; descartar todo mensaje con `seq <= lastSeenSeq`. Esto absorbe duplicados y hace inocuo cualquier reintento del writer.
- El orden dentro de un tick es determinista y sigue el orden de fases del loop: comandos (fases 1–2) → avance del movimiento (fase 3) → simulación, hoy no-op (fase 4) → temporizadores y presencia (fase 5) → encolado de persistencia (fase 8). No existe una fase separada de "emisión de deltas": cada hecho se emite dentro de la fase que lo produce. Concretamente, para una orden aceptada que reemplaza otra: `unit.movement.cancelled(REPLACED)` → `unit.move.accepted` → `unit.movement.started`.
- El cliente **no** debe reordenar por `ts`: dos mensajes del mismo tick pueden compartir `ts`. `seq` es el único criterio de orden.
- No hay ACK aplicativo del cliente hacia el servidor ni reenvío selectivo. La única recuperación ante desincronización es reconectar.
- **Contrapresión.** Si la cola de comandos del loop está llena, el comando se descarta y el cliente recibe `INTERNAL_ERROR`. Si la cola de salida de una sesión (256 mensajes) se llena, la sesión se cierra con `4500` y el cliente reconecta con un snapshot limpio: bloquear el envío propagaría la lentitud de un solo cliente al game loop entero.

## 12. Invariantes y persistencia

Este documento no define invariantes propios: materializa los de la familia `INV-SEC` del registro de [../invariants/security.md](../invariants/security.md), que es su única sede.

| Invariante | Cómo lo materializa este protocolo |
|---|---|
| [INV-SEC-001](../invariants/security.md#inv-sec-001) — el estado del cliente nunca muta el estado autoritativo | Los cinco payloads cliente→servidor son **estrictos** (S-7) y ninguno tiene campo para posición, HP, ruta, ETA ni ownership (§7.4) |
| [INV-SEC-002](../invariants/security.md#inv-sec-002) — ninguna conexión ejecuta comandos antes de autenticarse | El lector de la sesión no arranca hasta que el handshake termina; `session.hello` es el único tipo pre-auth (§6, §7.1) |
| [INV-SEC-003](../invariants/security.md#inv-sec-003) — un ticket solo puede canjearse una vez | `SET ticket:jti:{jti} NX EX 120` en el handshake (§7.1) |
| [INV-SEC-004](../invariants/security.md#inv-sec-004) — todo comando valida ownership antes de ejecutarse | Validación 2 de `unit.move` y primera de `unit.cancel_move`, antes que cualquier lectura de estado (§7.4, §7.5) |
| [INV-SEC-005](../invariants/security.md#inv-sec-005) — los mensajes fuera de límite se rechazan sin procesarse | `SetReadLimit(EO_WS_MAX_MESSAGE_BYTES)` y token bucket, ambos antes de deserializar (§4, §4.2) |
| [INV-SEC-006](../invariants/security.md#inv-sec-006) — las credenciales de infraestructura no salen del servidor | Ningún payload servidor→cliente transporta configuración de infraestructura; `INTERNAL_ERROR` nunca expone detalles internos (§9) |
| [INV-SEC-007](../invariants/security.md#inv-sec-007) — un `requestId` repetido no produce un segundo efecto | Reserva `SETNX` de `idem:{playerId}:{requestId}` antes de despachar el comando (§10) |

**Persistencia asociada al protocolo.** Qué se escribe, cuándo, y qué es reconstruible:

| Dato | Sede | Cuándo se escribe | ¿Reconstruible? |
|---|---|---|---|
| Anti-replay del ticket | Redis `ticket:jti:{jti}`, TTL 120 s | En el handshake, antes de crear la sesión | No, y no hace falta: expira solo |
| Presencia del jugador | Redis `presence:player:{playerId}`, TTL 30 s | Al establecer la sesión y con cada `session.ping` | Sí: la verdad de presencia la lleva el game loop en RAM |
| Deduplicación de comandos | Redis `idem:{playerId}:{requestId}`, TTL 300 s | Antes de despachar `unit.move` / `unit.cancel_move` | No; su pérdida sólo reabre la ventana de duplicado |
| Movimiento aceptado | PostgreSQL `unit_movements` (`path` en `jsonb`, `start_time_ms`, `arrival_time_ms` en `bigint`) | Encolado en la fase 2, escrito por los workers fuera del tick | Sí, desde la polilínea, en `simulation.Hydrate` |
| Posición consolidada | PostgreSQL `units.(x, y)` | Volcado periódico de sucios (`EO_PERSISTENCE_FLUSH_INTERVAL_TICKS` = 50) | Sí, desde el movimiento vigente o su último waypoint |
| Sesión | Tabla `sessions` **sin uso todavía**; la sesión vive en RAM y en Redis | — | Sí: una sesión perdida se rehace reconectando |

## 13. Fuente de verdad del schema

```
packages/protocol/
├── src/v1/
│   ├── common.ts            # Tile, Waypoint, TimedPath, UnitView, CityView, TerritoryView, ChunkRef, ChunkTerrain
│   ├── errors.ts            # ErrorCode como z.enum, exactamente los 22 códigos, y WS_CLOSE
│   ├── client.ts            # los 5 mensajes cliente→servidor, TODOS .strict()
│   ├── server.ts            # los 14 mensajes servidor→cliente, ninguno .strict()
│   └── index.ts             # export público de @empires-online/protocol
├── src/scripts/export-schema.ts
└── schema/v1/               # JSON Schema GENERADO — artefacto de build, nunca editado a mano
    ├── client-message.schema.json
    ├── server-message.schema.json
    └── error-codes.json     # { version: 1, codes: [...] }
```

Cadena de propagación:

```
Zod (TS)  --pnpm run protocol:build-->  JSON Schema  --go:embed-->  internal/protocol (contract tests)
   |                                          |
   v                                          v
apps/web (tipos + validación, pendiente)   validación manual en Go (runtime, por rendimiento)
```

`export-schema.ts` escribe en **dos** destinos y ambos se versionan en git: `packages/protocol/schema/v1/` y `services/game-server/internal/protocol/schema/v1/`. El espejo existe porque `go:embed` no puede salir del módulo Go.

| Regla | Detalle |
|---|---|
| SC-1 | Los esquemas Zod de `packages/protocol/src/v1/` son la única fuente de verdad. Ningún tipo del protocolo se declara a mano en el cliente: se importan de `@empires-online/protocol`. |
| SC-2 | `pnpm run protocol:build` regenera ambos destinos; `pnpm run protocol:check` (`export-schema.ts --check`) falla si hay deriva. CI lo ejecuta: un schema desactualizado en el repo es un check rojo. |
| SC-3 | El Game Server embebe los JSON Schema con `go:embed` en `internal/protocol` (`schema.go`) y los usa **solo en contract tests**. La validación en runtime está escrita a mano en Go por rendimiento; los contract tests garantizan que ambas coinciden. |
| SC-4 | `error-codes.json` declara su `version`; `ExportedErrorCodes()` rechaza un artefacto cuya versión no sea la que habla el servidor, antes incluso de comparar el catálogo. |
| SC-5 | Guardar los ejemplos JSON de este documento como *golden files* validados en CI es `TBD (fuera de MVP)`: hoy no existe `services/game-server/testdata/`. La cobertura equivalente la dan los contract tests de `internal/protocol` y los tests de `packages/protocol`. |
| SC-6 | `v2` no modifica `src/v1/`: crea `src/v2/` y `schema/v2/`. Los dos árboles coexisten mientras haya clientes v1 en producción. |

## 14. Tests esperados del protocolo

Los marcados **EN VERDE** ya existen y pasan. Los demás están diseñados y pendientes; ninguno de los de nivel `integration` puede ejecutarse hasta que el daemon de Docker arranque (PostgreSQL y Redis reales).

| Nivel | Test | Criterio de aceptación | Estado |
|---|---|---|---|
| contract | El catálogo de 22 códigos de Go coincide con el exportado por `packages/protocol` | `ElementsMatch` exacto | **EN VERDE** (`TestCatalogoDeErroresCoincideConElExportado`) |
| contract | Los 5 + 14 tipos declarados en Go aparecen en los JSON Schema embebidos | Los 19 presentes | **EN VERDE** |
| contract | El envelope saliente serializa `v`, `type`, `seq`, `ts`, `requestId`, `payload` | Campos exactos | **EN VERDE** |
| contract | `requestId` se **omite** cuando no aplica | La cadena `requestId` no aparece en el JSON | **EN VERDE** |
| contract | `entity.update` es un delta: los campos sin cambios no viajan | Sólo `id` y lo modificado | **EN VERDE** |
| contract | `unit.movement.started` lleva la polilínea completa y sobrevive al round-trip | `path` íntegro, `tMs` preservados | **EN VERDE** |
| contract | `unit.move` no admite que el cliente aporte una ruta | La estructura no tiene dónde guardarla | **EN VERDE** |
| contract | Sólo `session.hello` es pre-auth; el catálogo de comandos es cerrado (5) | Los otros cuatro rechazados como pre-auth | **EN VERDE** |
| contract | Los seis códigos de cierre están en el rango privado 4000–4999 | Verificado | **EN VERDE** |
| unit (TS) | Envelope cliente→servidor: tipo desconocido, versión no soportada, `requestId` no UUID, coordenadas no enteras, campo extra rechazado | Cada caso con su fallo esperado | **EN VERDE** |
| unit (TS) | `system.error` acepta cada uno de los 22 códigos y rechaza uno fuera del catálogo | Verificado | **EN VERDE** |
| unit (TS) | El JSON Schema exportado no ha derivado de los esquemas Zod | Mismo resultado que `--check` de CI | **EN VERDE** |
| unit (TS) | Los límites de transporte coinciden con los valores por defecto `EO_WS_*` | 16384 / 20 / 40 | **EN VERDE** |
| unit | Rechazo de `unit.move` / `unit.cancel_move` sin `requestId` | `INVALID_MESSAGE`, sin ejecución | Pendiente |
| unit | Tabla completa de las validaciones de `unit.move`, una por código | El `code` exacto esperado, y **sin** mutación de estado | **EN VERDE** (6 rechazos en `internal/game/simulation`) |
| unit | `seq` estrictamente creciente sin huecos en 10 000 mensajes | Monotonía verificada | Pendiente |
| integration | Handshake sin `session.hello` durante 5 s | Cierre `4408` | Pendiente (Docker) |
| integration | Ticket con `jti` reutilizado | Cierre `4401`, sin `system.error` previo | Pendiente (Docker) |
| integration | Ticket expirado (TTL 60 s) | Cierre `4401` | Pendiente (Docker) |
| integration | 60 mensajes en 1 s | `system.error RATE_LIMITED` y cierre `4429` inmediato | Pendiente (Docker) |
| integration | `unit.move` duplicado con el mismo `requestId` | Un solo movimiento creado; el duplicado sin respuesta | Pendiente (Docker) |
| integration | Redis caído durante un `unit.move` | El comando se ejecuta igualmente y se registra `warn` | Pendiente (Docker) |
| integration | `session.view` que desplaza el área 2 chunks | Nuevo `world.snapshot` con el `requestId` del comando y terreno **sólo** de los chunks nuevos | Pendiente (Docker) |
| simulation | `unit.move` con `FakeClock` sobre el trayecto canónico | `path` exacto `[0, 600, 1449, 2409, 2769]`, `arrivalTimeMs` exacto | **EN VERDE** |
| simulation | Reconstrucción de la posición desde la polilínea (`PositionAt`) | Coincide con la posición autoritativa del servidor | **EN VERDE** |
| simulation | Segunda orden sobre una unidad en movimiento | `unit.movement.cancelled(REPLACED)` antes de `unit.move.accepted` y `unit.movement.started` | **EN VERDE** |
| simulation | `unit.move` cuyo destino es la posición actual | `unit.move.accepted` con `movementId: 0`, sin movimiento creado | **EN VERDE** |
| recovery | Restart con movimiento `ACTIVE` y `arrival_time_ms` futuro | El movimiento se reanuda con la misma polilínea | **EN VERDE** (3 tests de recuperación) |
| recovery | Restart con `arrival_time_ms <= now` | Snap al tile final y movimiento `COMPLETED` | **EN VERDE** |
| recovery | Restart con polilínea inválida | Movimiento `FAILED`, unidad donde estaba | **EN VERDE** |

## 15. Documentos relacionados

- [../architecture/overview.md](../architecture/overview.md) — capas de estado, presencia y protección.
- [../architecture/game-loop.md](../architecture/game-loop.md) — orden de fases del tick y momento exacto de emisión de cada delta.
- [../architecture/networking.md](../architecture/networking.md) — interest management, tamaños y contrapresión.
- [../architecture/persistence.md](../architecture/persistence.md) — cola de persistencia y por qué el tick no hace I/O.
- [./movement.md](./movement.md) — A\*, coste por terreno, polilínea temporizada y persistencia en `unit_movements`.
- [./presence.md](./presence.md) — autómata de presencia y protección de ciudad.
- [../decisions/ADR-006-websocket-protocol.md](../decisions/ADR-006-websocket-protocol.md) — por qué JSON versionado sobre WebSocket.
- [../decisions/ADR-009-shared-protocol-package.md](../decisions/ADR-009-shared-protocol-package.md) — Zod como fuente única y su exportación a JSON Schema.
- [../decisions/ADR-010-authentication-game-ticket.md](../decisions/ADR-010-authentication-game-ticket.md) — emisión y consumo del game ticket.
- [../decisions/ADR-011-movement-timed-polyline.md](../decisions/ADR-011-movement-timed-polyline.md) — por qué la polilínea completa viaja en un mensaje.
- [../invariants/security.md](../invariants/security.md) — registro de `INV-SEC-001..007`.
