# Player

Especificación de la entidad `Player`: identidad, autenticación por ticket, alta con bootstrap atómico, y su relación ortogonal con `Civilization` y `Global Faction`.

| Campo | Valor |
|---|---|
| Spec ID | `SPEC-PLAYER` |
| Estado | Draft |
| Milestone | M2 Player & City |
| Canon | §0, §1, §10, §11, §12, §14, §16, §17 |
| Depende de | [city.md](city.md), [unit.md](unit.md), [websocket-protocol.md](websocket-protocol.md) |
| Reemplaza a | — |
| Invariantes | cita `INV-PLAYER-001..004` del registro; asigna `INV-PLAYER-005..008` |

## 1. Objetivo

Definir la identidad persistente de una persona dentro del mundo: cómo se crea, cómo se autentica frente al
game server, qué la caracteriza (`Civilization`, `Global Faction`) y cómo el alta produce, en una sola
transacción, un jugador jugable con ciudad y unidades.

## 2. Scope (y no-scope)

**Dentro del MVP**

- Identidad `uuid` estable y única del jugador.
- Verificación del *game ticket* JWT en el handshake `session.hello` y creación de la sesión.
- Alta del jugador con **bootstrap atómico**: `players` + `cities` + 3 `units` de tipo `VILLAGER` en una única
  transacción de PostgreSQL (`internal/persistence/postgres/bootstrap.go`).
- Alta, inicio de sesión y sesión web en `apps/web` (Next.js), con su propio almacén de identidad, y la
  **fundación** del jugador en el mundo por el endpoint interno del game server
  ([ADR-018](../decisions/ADR-018-identity-store-and-ticket-issuer.md)). Ver §4.1.
- Asignación de `Civilization` y de `Global Faction` como dos ejes independientes.
- Modelo **data-driven** de rasgos de civilización: `civilizations.traits` como `jsonb` consultado por clave,
  sin condicionales por civilización repartidos por el código.
- Relación 1:1 jugador↔ciudad en MVP y su consecuencia sobre el centrado de la vista al conectar.

**Fuera de MVP (no se implementa; se documenta el punto de extensión)**

- Recuperación de cuenta, cambio de contraseña, OAuth, *magic link* y verificación de correo:
  `TBD (fuera de MVP)`. El almacén de identidad de ADR-018 admite añadirlos sin tocar el juego.
- Efecto de gameplay de los traits de civilización sobre economía, tecnología, combate o coste de unidades:
  esos subsistemas no existen (canon §21). Los valores sembrados en `civilizations.traits` existen pero
  **ningún sistema los lee todavía**.
- Elección de civilización y de facción por parte del jugador en el alta: hoy son parámetros de arranque del
  servidor, no campos del formulario (§4.1).
- Estados de moderación de cuenta (suspensión, baneo, borrado), renombrado, cambio de civilización o de
  facción, múltiples ciudades por jugador, clanes, ranking: `TBD (fuera de MVP)`.
- Mecánica de facciones (bonos, guerra ORDER/CHAOS, reputación): `Fuera de MVP`. En MVP la facción es un
  atributo persistido y visible, sin efecto sobre las reglas.

## 3. Actores

| Actor | Rol |
|---|---|
| Jugador humano | Origen de toda intención. Todos los jugadores son humanos; la variedad viene de `Civilization`, no de razas (canon §0). |
| `apps/web` (Next.js) | Dueño de la identidad ([ADR-018](../decisions/ADR-018-identity-store-and-ticket-issuer.md)): verifica la contraseña, mantiene la sesión web y firma el *game ticket* JWT HS256 con `EO_AUTH_JWT_SECRET`, `aud = "game-server"` y TTL 60 s (`src/server/tokens.ts`). Elige el `playerId` en el alta y pide al game server que funde el jugador. Nunca escribe estado de simulación ni toca el esquema del juego. |
| `services/game-server` (Go) | Único autoritativo. Funda el jugador cuando `apps/web` se lo pide con un token de fundación, verifica el ticket, consume el `jti`, crea la sesión y responde. No ve contraseñas. |
| PostgreSQL, base del juego | *Durable source of truth*: `players`, `civilizations`, `factions`, `eras`, `cities`, `units`, `sessions`, `world_events`. Sin ninguna credencial. |
| PostgreSQL, almacén de identidad | Base propia de `apps/web` (`empires_identity`): `accounts` y `web_sessions`. Otro rol, otro ciclo de migración. |
| Redis | *Hot state*: `ticket:jti:{jti}`, `presence:player:{playerId}`, idempotencia de comandos del protocolo. Nunca sustituye a PostgreSQL. |

## 4. Inputs

### 4.1 Alta, inicio de sesión y fundación (fuera del canal WebSocket)

El canon no define un mensaje WebSocket de registro (§13 no lo lista), de modo que el alta **no** es un
comando del protocolo v1. Desde [ADR-018](../decisions/ADR-018-identity-store-and-ticket-issuer.md) la sirve
`apps/web` (route handlers de Next.js en `app/api/auth/*`, lógica en `src/server/`), y el game server sólo
**funda** al jugador en el mundo cuando `apps/web` se lo pide.

`POST /api/auth/register` (Next.js) — cuerpo JSON `application/json`, máximo 4096 bytes:

| Input | Tipo | Origen | Notas |
|---|---|---|---|
| `username` | `string` | jugador | Misma regla que `RN-PLAYER-003`, comprobada en `apps/web` y otra vez en el game server. |
| `password` | `string` | jugador | De 8 a 256 caracteres. Se deriva con `scrypt` (`N = 2^15, r = 8, p = 3`) y **nunca** se almacena en claro ni sale de `apps/web`. |

`POST /api/auth/login` — mismo cuerpo `{ username, password }`.

`POST /api/auth/ticket` — sin cuerpo: un ticket nuevo para la sesión de la cookie `eo_session`. Es lo que pide
el cliente en cada (re)conexión, porque el ticket es de un solo uso.

`POST /api/auth/logout` — sin cuerpo: borra la sesión y caduca la cookie (204).

El alta y el login dejan la cookie de sesión `eo_session` (`HttpOnly`, `SameSite=Strict`, `Path=/api/auth`,
`Secure` en producción, 7 días) y responden:

```json
{
  "playerId": "6f0f7f1a-3d21-4a5b-8c9d-0e1f2a3b4c5d",
  "ticket": "<JWT HS256>",
  "expiresInSeconds": 60,
  "city": { "id": 1042, "name": "alicepolis", "centerX": 231, "centerY": 178 }
}
```

`city` sólo viene cuando la llamada fundó (o terminó de fundar) al jugador; el login de una cuenta ya activa no
la trae, y el cliente la encuentra en el `world.snapshot`.

**Fundación.** El alta crea la cuenta en el almacén de identidad como `PENDING`, con un `playerId` nuevo
(`crypto.randomUUID()`), y llama a `POST /internal/players` del game server con
`{ playerId, username }` y un token de fundación (JWT HS256 firmado con `EO_AUTH_JWT_SECRET`,
`aud = "game-server-internal"`, `sub = playerId`, TTL 60 s, `jti` de un solo uso). El game server ejecuta el
bootstrap de §6 y responde `201` con la ciudad, o `200` si ese jugador ya existía. Sólo entonces la cuenta pasa a
`ACTIVE` y se abre la sesión (`INV-PLAYER-009`). Si el game server rechaza de forma definitiva, la cuenta
`PENDING` se borra y el nombre queda libre; si no responde, la cuenta se queda `PENDING`, el alta responde 503
y el siguiente login con la contraseña correcta repite la fundación. Antes de repetirla, el login renueva el
intento de fundación de la cuenta; si entretanto la cuenta se activó, entra sin fundar, y si se borró, responde
`UNAUTHORIZED` sin llamar al game server (`RN-PLAYER-016`).

**Cuentas abandonadas** ([ADR-020](../decisions/ADR-020-stale-pending-accounts.md)). Una cuenta `PENDING` cuyo último intento de fundación
(`accounts.founding_attempted_at`, en el reloj de la base) tiene más de una hora es *abandonada*. Se resuelve
preguntando al game server con `GET /internal/players/{playerId}`: si el jugador existe, la cuenta se activa;
si responde `404 PLAYER_NOT_FOUND`, se borra, con la guarda en la propia sentencia; con cualquier otra respuesta,
o sin ella, no se toca (`RN-PLAYER-015`, `INV-PLAYER-010`). Lo hace el alta que choca con su nombre —si la
borra, reintenta la creación una vez— y el comando `pnpm run identity:sweep`, que resuelve hasta 500 de la
más antigua a la más reciente.

Lo que el cuerpo del alta **no** lleva, y por qué:

- **`playerId`.** Lo genera `apps/web` en el alta y es el mismo `uuid` para la cuenta y para el jugador
  (`RN-PLAYER-001`). El jugador no lo elige.
- **`civilizationCode` / `factionCode`.** En MVP son parámetros de arranque del game server
  (`httpapi.Options.CivilizationID` = 1 → `ROMAN`, `FactionID` = 3 → `NEUTRAL`), no elección del jugador.
  Exponerlos en el formulario es `Fuera de MVP`; el modelo de datos ya soporta cualquier combinación
  (`RN-PLAYER-005`).
- **`requestId`.** El alta HTTP **no es idempotente por clave de petición**: ver `RN-PLAYER-006`.

Errores del alta (Next.js), con su código HTTP: `INVALID_MESSAGE` (400: cuerpo que no es JSON, sin
`Content-Type: application/json`, o contraseña de más de 256 caracteres), `INVALID_USERNAME` (400),
`WEAK_PASSWORD` (400), `FORBIDDEN` (403, petición con `Origin` de otro sitio), `USERNAME_TAKEN` (409),
`NO_SITE_AVAILABLE` (409), `RATE_LIMITED` (429), `SITE_CONTENDED` (503: otras altas simultáneas ocuparon cada
sitio elegido y basta con reintentar), `GAME_SERVER_UNAVAILABLE` (503: la cuenta existe pero el mundo no
respondió; o el nombre lo retiene una cuenta abandonada y el mundo no contestó para resolverla) e `INTERNAL_ERROR` (500). El login responde `UNAUTHORIZED` (401) tanto si el usuario no existe como
si la contraseña no coincide, tras comparar igualmente contra un hash de relleno para no revelar por tiempo qué
nombres existen. **Estos códigos son del transporte HTTP de la identidad y no pertenecen al catálogo de 22
códigos del protocolo v1** (canon §16).

**Límites de tasa** (`src/server/rateLimit.ts`, por proceso de Next.js). El alta y el login pasan por tres
cubos de fichas antes de calcular `scrypt`, y basta con que uno se agote para responder `RATE_LIMITED` (429):
por IP (`EO_AUTH_RATE_LIMIT_PER_MINUTE` / `_BURST`, 10 / 5), sólo cuando se conoce; por nombre de usuario sin
distinguir mayúsculas (`EO_AUTH_RATE_LIMIT_USER_*`, 20 / 10), venga de donde venga, que es lo que frena la
fuerza bruta repartida entre direcciones; y uno global (`EO_AUTH_RATE_LIMIT_GLOBAL_*`, 120 / 30) que acota el
coste total de `scrypt` del proceso y que, sin proxy, es lo único que acota a un solo cliente. La IP sólo se
conoce con `EO_TRUST_PROXY_HEADERS=true`, y entonces es el `EO_TRUSTED_PROXY_COUNT`-ésimo elemento **por la
derecha** de `X-Forwarded-For` —el que añadió el último proxy de confianza; el primero lo escribe el cliente—,
o `X-Real-IP` si no hay lista. Los cubos por IP y global se evalúan antes de leer el cuerpo; el de usuario, con
el cuerpo leído y antes de `scrypt`. `POST /api/auth/ticket` y `POST /api/auth/logout` no calculan `scrypt`
pero consultan la base: con cookie pasan por un cubo por sesión (60 / 30, constante: un cliente correcto pide
como mucho siete tickets en su primer minuto de reconexión), otro por IP si se conoce (240 / 120, constante) y
un tope global (`EO_AUTH_TICKET_RATE_LIMIT_*`, 1200 / 600), y responden `RATE_LIMITED` (429) al agotar
cualquiera; sin cookie responden `UNAUTHORIZED` sin gastar ningún cupo ni tocar la base. La clave de sesión
que llega al log del 429 es un hash truncado del token, nunca el token (`INV-PLAYER-007`). El tope de 4096
bytes del cuerpo del alta y del login se aplica **antes** de leerlo: con `Content-Length` mayor se rechaza sin
leer un byte y, sin esa cabecera, se deja de leer al pasarlo; en los dos casos `INVALID_MESSAGE` (400).

Respuestas de `POST /internal/players` (game server): `201`/`200` con `{ playerId, city }`; `400`
`INVALID_MESSAGE` o `INVALID_USERNAME`; `401` `UNAUTHORIZED` (sin token, token de otra audiencia, caducado o ya
consumido); `403` `FORBIDDEN` (el cuerpo nombra otro `playerId` que el `sub`); `409` `USERNAME_TAKEN` (el
nombre es de otro jugador), `PLAYER_CONFLICT` (el id ya existe con otro nombre) o `NO_SITE_AVAILABLE`; `503`
`SITE_CONTENDED` o `INTERNAL_ERROR` (no se pudo comprobar el `jti`); `500` `INTERNAL_ERROR`.

Respuestas de `GET /internal/players/{playerId}` (game server), con el mismo token de fundación: `200`
`{ playerId, username, city }` si el jugador existe; `404` `PLAYER_NOT_FOUND` si no; `400` `INVALID_MESSAGE`
(el id no es un `uuid`); `401` `UNAUTHORIZED` y `503` `INTERNAL_ERROR` como en la fundación; `403`
`FORBIDDEN` (la ruta nombra otro id que el `sub`); `500` `INTERNAL_ERROR`. Sólo lee: nunca funda.

### 4.2 Handshake de sesión (WebSocket v1)

| Mensaje | Dirección | Payload | Notas |
|---|---|---|---|
| `session.hello` | cliente→servidor | `{ ticket, clientVersion? }` | Primer mensaje de la conexión, antes de 5 s o cierre `4408`. Esquema estricto: cualquier campo adicional se rechaza. |
| `session.ping` | cliente→servidor | `{ clientTimeMs }` | Mantiene viva la conexión y refresca la presencia (ver [presence.md](presence.md)). El servidor responde `session.pong` con eco de `clientTimeMs`. |

Claims del ticket (canon §14): `{ sub: playerId, jti, iat, exp, aud: "game-server" }`, HS256, TTL **60 s**.
`clientVersion` es solo diagnóstico y telemetría: no altera ninguna decisión de gameplay.

### 4.3 Configuración consumida

| Variable | Valor por defecto | Uso en esta spec |
|---|---|---|
| `EO_AUTH_JWT_SECRET` | — (obligatoria en los dos procesos) | `apps/web` firma con él el ticket y el token de fundación; el game server los verifica. Mínimo 32 caracteres; no puede contener `dev-only` si `EO_ENV=production`, en ninguno de los dos. |
| `EO_IDENTITY_POSTGRES_URL` | — (obligatoria en `apps/web`) | Almacén de identidad. Sólo la lee `apps/web`. |
| `EO_GAME_SERVER_INTERNAL_URL` | `http://localhost:8081` | Dónde llama `apps/web` para fundar: el listener interno del Game Server (`EO_INTERNAL_ADDR`), no el público. |
| `EO_AUTH_RATE_LIMIT_PER_MINUTE` / `EO_AUTH_RATE_LIMIT_BURST` | 10 / 5 | Alta y login por IP, en `apps/web` y por proceso; sólo actúa con `EO_TRUST_PROXY_HEADERS=true`. |
| `EO_AUTH_RATE_LIMIT_USER_PER_MINUTE` / `EO_AUTH_RATE_LIMIT_USER_BURST` | 20 / 10 | Alta y login por nombre de usuario, con o sin proxy. |
| `EO_AUTH_RATE_LIMIT_GLOBAL_PER_MINUTE` / `EO_AUTH_RATE_LIMIT_GLOBAL_BURST` | 120 / 30 | Tope de alta y login de todo el proceso: acota el coste de `scrypt`. Al menos tan amplio como los dos anteriores. |
| `EO_AUTH_TICKET_RATE_LIMIT_PER_MINUTE` / `EO_AUTH_TICKET_RATE_LIMIT_BURST` | 1200 / 600 | Tope de ticket y cierre de sesión de todo el proceso. |
| `EO_TRUST_PROXY_HEADERS` / `EO_TRUSTED_PROXY_COUNT` | `false` / 1 | Si se cree a `X-Forwarded-For` (y a `X-Real-IP` a falta de ella) y cuántos proxies de confianza añaden su salto. |
| `EO_WORLD_WIDTH` / `EO_WORLD_HEIGHT` | 512 / 512 | Límites para el emplazamiento de la ciudad inicial. |
| `EO_WORLD_SEED` | 20260909 | Semilla del **generador del mapa**, no del emplazamiento. |
| `EO_INTEREST_RADIUS_CHUNKS` | 2 | Área de interés inicial centrada en la ciudad del jugador. |
| `EO_CHUNK_SIZE` | 32 | Deriva `units.chunk_x`/`chunk_y` de los aldeanos iniciales. |

Constante no configurable por entorno, fijada en código: el TTL del ticket y del token de fundación, **60 s**
(`TOKEN_TTL_SECONDS` en `apps/web/src/server/tokens.ts`). Tampoco lo son la vida de la sesión web (7 días,
`SESSION_TTL_MS`), los cupos de ticket y cierre de sesión por sesión (60 por minuto, ráfaga 30,
`TICKET_SESSION_POLICY`) y por IP (240 / 120, `TICKET_IP_POLICY`), el tope de 4096 bytes del cuerpo del alta y
del login, el plazo de handshake (5 s), el intervalo de ping (15 s) ni los timeouts de lectura (45 s) y
escritura (10 s) del WebSocket.

La semilla del emplazamiento **no** es `EO_WORLD_SEED`: es un FNV-1a de 64 bits del propio `username`
(`hashSeed`). Así es raro que dos altas simultáneas busquen en la misma región, y el mismo nombre obtiene siempre el
mismo punto de partida, lo que hace el alta reproducible en tests y depurable en producción.

## 5. Outputs

| Output | Destino | Cuándo |
|---|---|---|
| Fila en `players` | PostgreSQL | Alta, dentro de la transacción de bootstrap. |
| Fila en `cities` | PostgreSQL | Alta, misma transacción. Ver [city.md](city.md). |
| 3 filas en `units` (`VILLAGER`, `IDLE`) | PostgreSQL | Alta, misma transacción. |
| Fila en `world_events` con `event_type = 'PlayerBootstrapped'` | PostgreSQL | Alta, misma transacción. Payload: `{ cityId, villagers, center:{x,y} }`. |
| Comando `IntroducePlayer` | cola de comandos de la simulación | **Después** del COMMIT, nunca antes: el mundo en RAM solo conoce al jugador cuando la transacción confirmó. Incorpora ciudad, aldeanos y el rectángulo bloqueado de la muralla. |
| Fila en `accounts` (`PENDING`, luego `ACTIVE`) y en `web_sessions` | almacén de identidad de `apps/web` | Alta y login. Ver §4.1. |
| Ticket JWT, cookie `eo_session` y `city` | respuesta HTTP del alta o del login | Ver §4.1. |
| Fila en `sessions` | PostgreSQL | Handshake aceptado. |
| Clave `presence:player:{playerId}` | Redis, TTL `EO_PRESENCE_TTL_SECONDS` (30 s) | Handshake aceptado. Ver [presence.md](presence.md). |
| `session.welcome` | conexión que hizo el handshake | Tras verificar el ticket y crear la sesión. |
| `world.snapshot` | misma conexión | Inmediatamente después de `session.welcome`, con el área de interés centrada en la ciudad. |
| `system.error` | misma conexión | Ante cualquier fallo, con un `code` del canon §16. |
| Métrica `eo_connected_players` | Prometheus | Gauge, +1 al primer WS del jugador, −1 al cerrar el último. |
| Log estructurado con `player_id` | stdout JSON (`log/slog` en el game server, `jsonLogger` en `apps/web`) | Cuenta creada, activada o borrada; fundación hecha o rechazada; login rechazado; cierre de sesión. Nunca registra la contraseña, su hash, el token de la sesión ni un ticket o token de fundación, completo o en parte. |

## 6. Reglas de negocio

Dominio de reglas: `RN-PLAYER`.

| ID | Regla |
|---|---|
| `RN-PLAYER-001` | `players.id` es un `uuid` (canon §11: la única PK que no es `bigint GENERATED ALWAYS AS IDENTITY`). Desde [ADR-018](../decisions/ADR-018-identity-store-and-ticket-issuer.md) lo genera `apps/web` en el alta (`crypto.randomUUID()`, UUIDv4) y es a la vez el id de la cuenta y el `sub` del ticket; el game server lo recibe en la fundación y lo usa tal cual. Los jugadores de la siembra de desarrollo llevan un UUIDv5 de su nombre (`devseed.PlayerID`). Nada de eso ocurre en el tick: no forma parte del dominio determinista y no usa `RandomSource`. |
| `RN-PLAYER-002` | Un jugador se identifica siempre por su `uuid`. Ningún mensaje del protocolo transporta claves internas alternativas ni índices posicionales. |
| `RN-PLAYER-003` | El nombre de cuenta es la columna **`players.username`**, no `display_name`: la tabla no tiene una columna de nombre visible separada. Es único (`UNIQUE` en la columna) y su formato se valida en **dos** niveles con la misma regla: `player.ValidateUsername` en el boundary y el `CHECK` `players_username_format` (`username ~ '^[A-Za-z0-9_-]{3,24}$'`) en PostgreSQL. Longitud 3..24 y únicamente `A-Z`, `a-z`, `0-9`, `_` y `-`. **No hay normalización NFKC ni recorte de espacios**, precisamente porque el alfabeto permitido es ASCII cerrado: normalizar sería resolver un problema que el `CHECK` ya impide. La lista de nombres reservados es `TBD (fuera de MVP)`. |
| `RN-PLAYER-004` | Todo jugador tiene exactamente una `Civilization` y exactamente una `Global Faction`, ambas `NOT NULL` con FK. No hay valor nulo ni "sin asignar". |
| `RN-PLAYER-005` | `Civilization` y `Global Faction` son **ejes ortogonales**: cualquier combinación del producto cartesiano es válida. No existe ninguna restricción, tabla de compatibilidad ni valor por defecto derivado del otro eje. |
| `RN-PLAYER-006` | El alta HTTP **no** es idempotente por `requestId`. La defensa contra el alta duplicada es el `UNIQUE` de `accounts.username` en el almacén de identidad, que traduce a `USERNAME_TAKEN` (409), y detrás el de `players.username`. La **fundación** sí es idempotente, por `playerId`: repetirla devuelve el jugador que ya existe, y dos fundaciones simultáneas del mismo id acaban en un solo jugador (`INV-PLAYER-009`). La deduplicación por `requestId` sirve a los **comandos del protocolo v1**, no a estos endpoints. |
| `RN-PLAYER-007` | El alta es **atómica**: `players` + `cities` + 3 `units` + el `world_events` de auditoría se escriben en una sola transacción (`Bootstrapper.Create` sobre `Store.InTx`). Un fallo en cualquier paso deja la base de datos sin rastro del jugador. No existe estado intermedio "jugador sin ciudad" ni "ciudad sin aldeanos". |
| `RN-PLAYER-008` | El game server no ve contraseñas: su esquema no tiene ninguna columna de credencial (migración `000005`) y sus dos entradas de autenticación son el ticket del handshake y el token de fundación, ambos firmados. La contraseña sólo atraviesa `apps/web`, el tiempo justo de derivar o comparar su hash `scrypt`, que vive en `accounts.password_hash` del almacén de identidad y no viaja a ninguna otra capa ni a ningún log. |
| `RN-PLAYER-009` | El ticket se verifica en firma HS256, `exp` y `aud == "game-server"`, y su `jti` se consume una sola vez en Redis (`ticket:jti:{jti}`). Un ticket sin `jti` se rechaza: sin él no se puede impedir el replay. Un `jti` ya consumido se rechaza con el cierre `4401`, sin mensaje previo ([ADR-019](../decisions/ADR-019-handshake-failure-close-code.md)). |
| `RN-PLAYER-010` | Si `session.hello` no llega en los primeros 5 s de la conexión, el servidor cierra con código `4408`. Ningún otro tipo de mensaje se procesa antes del handshake. |
| `RN-PLAYER-011` | En MVP un jugador posee **exactamente una** ciudad. El área de interés inicial se centra en `cities.center_x` / `cities.center_y` de esa ciudad, con radio `EO_INTEREST_RADIUS_CHUNKS` (canon §13). Multi-ciudad es `Fuera de MVP`. |
| `RN-PLAYER-012` | Los rasgos de civilización no se consultan con condicionales por código de civilización. Toda lectura pasa por `Civilization.Trait(key, def)` (§6.3). |
| `RN-PLAYER-013` | La facción no restringe ninguna acción del MVP. Es un atributo persistido; su efecto sobre reglas es `Fuera de MVP`. |
| `RN-PLAYER-014` | El emplazamiento de la ciudad inicial se elige **antes** de abrir la transacción, con `founding.FindSite`: espiral determinista desde `hashSeed(username)`, entorno despejado de radio 3, separación mínima de 24 tiles con cualquier centro de ciudad existente, y 3 aldeanos a radio 2 sobre tiles transitables. Si no hay sitio, el alta responde `NO_SITE_AVAILABLE` (409) y **no** se coloca al jugador «donde sea». Dentro de la transacción, bajo un `pg_advisory_xact_lock` que serializa las altas, el centro elegido se vuelve a comprobar con `founding.FarEnough` contra los centros ya confirmados; si otra alta se adelantó, la búsqueda se repite hasta tres veces y, agotadas, el alta responde `SITE_CONTENDED` (503, `Retry-After: 1`) sin escribir nada (`INV-CITY-008`). |
| `RN-PLAYER-015` | Una cuenta `PENDING` cuyo último intento de fundación tiene más de una hora se resuelve preguntando al game server: si su jugador existe, se activa; si responde `404 PLAYER_NOT_FOUND`, se borra sólo si en el instante del borrado sigue `PENDING` y abandonada; con cualquier otra respuesta, o sin ella, no se toca. Ningún proceso borra una cuenta `PENDING` sin esa respuesta, salvo el rechazo definitivo de su propia fundación (`INV-PLAYER-010`, [ADR-020](../decisions/ADR-020-stale-pending-accounts.md)). |
| `RN-PLAYER-016` | El login de una cuenta `PENDING` renueva su intento de fundación **antes** de llamar al game server, y sólo lo llama si la cuenta seguía `PENDING`: una cuenta activada entretanto entra sin fundar, y una borrada responde `UNAUTHORIZED`. |

### 6.1 Ortogonalidad Civilization × Global Faction

`Civilization` es identidad **cultural** (unidades, tecnologías y bonos futuros). `Global Faction` es
alineamiento **político** del servidor: `ORDER`, `CHAOS`, `NEUTRAL`. Son dos preguntas distintas sobre el mismo
jugador y ninguna condiciona a la otra.

```
        Civilization (cultura)  ─────────────►
        │  Roman     Byzantine   Persian    Norse
   ORDER│   ✔           ✔           ✔         ✔
   CHAOS│   ✔           ✔           ✔         ✔
 NEUTRAL│   ✔           ✔           ✔         ✔
        ▼
   Global Faction (política)
```

Ejemplos que demuestran la ortogonalidad (los cuatro son estados válidos y simultáneos del mismo servidor):

| Jugador | `civilization` | `faction` | Lectura | ¿Válido? |
|---|---|---|---|---|
| `alice` | `ROMAN` | `ORDER` | Cultura romana, alineada con el Orden. | Sí |
| `bob` | `ROMAN` | `CHAOS` | Misma cultura que `alice`, política opuesta. Comparten árbol cultural, no bando. | Sí |
| `carol` | `NORSE` | `ORDER` | Cultura distinta a `alice`, mismo bando. Aliadas políticas con unidades distintas. | Sí |
| `dave` | `BYZANTINE` | `NEUTRAL` | No participa del eje político. Su cultura sigue siendo plena. | Sí |
| `erin` | `PERSIAN` | `CHAOS` | Cuarta combinación independiente. | Sí |

Consecuencias de diseño que se derivan de la tabla:

- La cardinalidad del espacio de jugadores es `|civilizations| × |factions|`; añadir una civilización no toca
  el eje de facciones y viceversa.
- Ninguna consulta SQL ni ninguna regla puede inferir la facción a partir de la civilización.
- Dos jugadores con la misma civilización pueden ser enemigos políticos; dos jugadores de la misma facción
  pueden tener bonos culturales completamente distintos.

**Estado real en MVP:** el modelo de datos admite las doce combinaciones, pero el alta provisional de §4.1
no las expone: todos los jugadores nacen `ROMAN` / `NEUTRAL` porque la civilización y la facción son
parámetros de arranque del proceso. La ortogonalidad es una propiedad del esquema verificable hoy con un
`INSERT` por cada par; abrir la elección al jugador es un cambio de formulario, no de modelo.

### 6.2 Catálogo de traits data-driven

El objetivo es no escribir jamás `if civilization == "ROMAN" { ... }` disperso por el dominio. Ese patrón
crece en O(civilizaciones × subsistemas), es imposible de testear exhaustivamente y obliga a recompilar para
un ajuste de balance.

El canon §11 fija la lista de tablas del MVP y `civilization_traits` **no** está en ella, así que no se crea
una tabla nueva: los traits viajan como `jsonb` dentro de `civilizations`.

```sql
-- civilizations (migración 000001). DDL canónico completo en ../database/schema.md
CREATE TABLE civilizations (
    id          integer     PRIMARY KEY,
    code        text        NOT NULL UNIQUE,   -- ROMAN, BYZANTINE, PERSIAN, NORSE
    name        text        NOT NULL,
    traits      jsonb       NOT NULL DEFAULT '{}'::jsonb,
    created_at  timestamptz NOT NULL DEFAULT now()
);
```

`civilizations`, `factions` y `eras` son catálogos sembrados por migración con `id` explícito y estable:
son la excepción a `bigint GENERATED ALWAYS AS IDENTITY`, porque su clave es un dato de juego versionado
en `000002_seed_catalogs.up.sql`, no un correlativo. Tampoco llevan `version` ni `updated_at`: se cambian
por migración, no por escritura de la aplicación.

**Forma de un trait: un objeto plano `clave → multiplicador`**, no una lista de operaciones. `1.0`
significa «sin efecto», que es lo que permite consultar cualquier clave sin comprobar antes si existe:

```json
{ "unit.move_speed": 0.95, "build.speed": 1.00, "defense.wall_hp": 1.20 }
```

Semillas reales de la migración 000002:

| `code` | `traits` |
|---|---|
| `ROMAN` | `{"unit.move_speed": 1.00, "build.speed": 1.10, "economy.gather_rate": 1.00}` |
| `BYZANTINE` | `{"unit.move_speed": 0.95, "build.speed": 1.00, "defense.wall_hp": 1.20}` |
| `PERSIAN` | `{"unit.move_speed": 1.05, "build.speed": 0.95, "economy.trade_yield": 1.15}` |
| `NORSE` | `{"unit.move_speed": 1.10, "build.speed": 0.90, "combat.raid_bonus": 1.15}` |

Las claves son cadenas estables en minúsculas con punto (`dominio.propiedad`). Un `op`, un `scope` y un
`source` por trait —el modelo de pipeline de modificadores con `ADD`/`MUL`/`SET_MIN`/`SET_MAX`— son
`Fuera de MVP`: hoy el único operador es la multiplicación implícita sobre un valor base y el `scope` es
siempre global a la civilización.

**Estado real en MVP: los traits están sembrados pero ningún sistema los lee.** `unit.move_speed` no se
aplica al construir la polilínea: el coste por segmento sale de `unit.Lookup(unitType).BaseMsPerTile`
(600 ms para `VILLAGER`) sin ningún modificador de por medio. Lo que existe es el **mecanismo de
consulta** (`Civilization.Trait`), de modo que enchufar el primer bono real sea un cambio de datos más una
línea en el punto de consumo, y no una refactorización.

### 6.3 Punto único de consulta de traits

El catálogo se carga al arrancar (`PlayerRepo.ListCivilizations`) y se cachea inmutable durante la vida del
proceso. La única vía admitida para leer un bono cultural es un método del dominio:

```go
// internal/domain/player/player.go
type Civilization struct {
    ID     int32
    Code   string
    Name   string
    Traits map[string]float64
}

// Trait devuelve el valor de un rasgo, o el valor por defecto si la civilización
// no lo define. Ésta es la ÚNICA vía admitida para consultar bonos culturales.
func (c Civilization) Trait(key string, def float64) float64
```

Propiedades que hacen que esto baste hoy:

- **Determinismo.** `Trait` es una lectura por clave de un mapa inmutable: no itera el mapa, así que no
  hereda el orden aleatorio de iteración de Go (canon §1.5).
- **Ausencia segura.** Una civilización que no declara la clave devuelve `def`. No hay caso nulo que cada
  llamador tenga que resolver a su manera, que es exactamente el modo de fallo que
  [`INV-PLAYER-002`](../invariants/player.md#inv-player-002) describe para las referencias irresolubles.
- **Sin condicionales por civilización.** El llamador nombra la propiedad que le concierne
  (`"unit.move_speed"`), nunca el código de la civilización.

Puntos de consumo previstos y su estado honesto:

| `key` | Valor base | Punto de aplicación | Estado |
|---|---|---|---|
| `unit.move_speed` | `600` ms/tile de `VILLAGER`, del catálogo de unidades | Coste temporal por segmento al construir la polilínea, ver [movement.md](movement.md) | **Sembrado pero sin consumir.** Hoy `BuildTimedPath` recibe `def.BaseMsPerTile` sin modificar. |
| `build.speed` | — | Construcción | `Fuera de MVP` (no hay construcción) |
| `economy.gather_rate` / `economy.trade_yield` | — | Economía | `Fuera de MVP` |
| `defense.wall_hp` / `combat.raid_bonus` | — | Combate | `Fuera de MVP` |
| Límite de población | `eras.population_cap` (20/50/100/150) | `cities.population_limit`, ver [city.md](city.md) | Implementado **sin** trait: se copia de la era en el alta. Ninguna civilización lo modifica. |

Cuando llegue el primer bono real habrá que decidir explícitamente **dónde** se aplica, porque la decisión
determina qué dato es autoritativo. Para `unit.move_speed` la respuesta ya está fijada por el esquema: la
tabla `units` **no** tiene columna `base_ms_per_tile` (ver [unit.md](unit.md) §4), así que el modificador
solo puede aplicarse al construir cada polilínea, y no queda congelado en ninguna fila. Un ajuste de
balance afecta al siguiente movimiento de todas las unidades, no solo a las creadas después.

Coste de mantenimiento: añadir una civilización con bonos es insertar una fila en `civilizations` con su
`traits` y, a lo sumo, una llamada a `Trait` en el punto que la consume. Cero ramas condicionales nuevas.

## 7. Estados y transiciones

El jugador tiene tres ejes de estado independientes que conviene no mezclar:

**a) Ciclo de vida de la cuenta.** En MVP existe un único estado vivo: el jugador está dado de alta y es
jugable. Suspensión, baneo y borrado son `TBD (fuera de MVP)` y no se modela una columna de estado para ellos
hasta que el canon los defina.

**b) Presencia del jugador** (volátil, Redis): derivada de la existencia de `presence:player:{playerId}` y de
la existencia de al menos una conexión WebSocket viva. No es una columna de `players`. Especificada por
completo en [presence.md](presence.md).

**c) Ciclo de vida de la sesión WebSocket:**

```mermaid
stateDiagram-v2
    [*] --> CONNECTING: TCP/TLS establecido
    CONNECTING --> AUTHENTICATING: session.hello recibido
    CONNECTING --> [*]: 5 s sin mensaje → cierre 4408
    AUTHENTICATING --> ACTIVE: ticket válido + jti consumido → session.welcome
    AUTHENTICATING --> [*]: firma/exp/aud inválidos o jti repetido → cierre 4401 (sin mensaje)
    ACTIVE --> ACTIVE: session.ping / session.view / unit.*
    ACTIVE --> [*]: cierre del cliente, timeout de lectura 45 s, 4429 o 4500
```

`CONNECTING`, `AUTHENTICATING` y `ACTIVE` son estados **en RAM** de la goroutine de la conexión, y no se
persisten: la tabla `sessions` existe desde `000001` (`id`, `player_id`, `connected_at`, `disconnected_at`,
`remote_addr`, `client_version`) pero **ningún código la escribe**. Las sesiones vivas las cuenta la
simulación por jugador, y la presencia caliente está en Redis (`presence:player:{playerId}`).

La transición `ACTIVE → cerrada` **no** degrada por sí sola el estado de la ciudad: eso lo decide la fase de
timers del tick comparando contra `DisconnectGrace` (= `EO_PRESENCE_TTL_SECONDS`, 30 s) en RAM, no leyendo
la expiración de la clave de Redis. Un jugador con varias pestañas abiertas sigue `ONLINE` mientras le
quede una. Ver [presence.md](presence.md) §7.

## 8. Errores

Por el WebSocket sólo se usan códigos del canon §16, emitidos como `system.error` con
`{ code, message, requestId?, details? }`, y **sólo dentro de una sesión**: mientras la conexión no tiene
sesión, un fallo se comunica únicamente con el código de cierre
([ADR-019](../decisions/ADR-019-handshake-failure-close-code.md)). La lógica de control se basa en `code`, o en
el código de cierre, jamás en el texto.

| Código | Condición exacta | Efecto sobre la conexión |
|---|---|---|
| Cierre `4401` | Durante el handshake: ticket con firma inválida, `exp` vencido, `aud != "game-server"`, sin `jti` o con `jti` ya consumido, `sub` que no es un UUID, Redis sin responder al consumir el `jti`, o un primer mensaje que no es `session.hello`. El handshake **no** comprueba que el `sub` exista en `players`: el ticket lo firma quien acaba de fundarlo. | Cierre `4401` sin mensaje. `UNAUTHORIZED` está reservado en el catálogo y el WebSocket no lo emite. |
| Cierre `4400` | Durante el handshake: JSON malformado, `session.hello` sin `ticket` o con propiedades que el contrato no declara, o `v != 1`. | Cierre `4400` sin mensaje. |
| `INVALID_MESSAGE` | Dentro de la sesión: JSON malformado, `type` desconocido, payload que no valida contra el esquema estricto, o comando sin `requestId`. | `system.error`; la conexión **continúa**. |
| `UNSUPPORTED_VERSION` | Dentro de la sesión: `v != 1`. | `system.error`; la conexión continúa. |
| `MESSAGE_TOO_LARGE` | Reservado. Un frame de más de `EO_WS_MAX_MESSAGE_BYTES` (16384) lo corta el transporte antes de leerlo. | Cierre `1009`, sin mensaje. |
| `RATE_LIMITED` | Más de `EO_WS_RATE_LIMIT_PER_SECOND` (20) msg/s con burst 40. | `system.error` y cierre `4429` en cuanto el bucket se agota. |
| `UNIT_NOT_OWNED` | La sesión ordena algo sobre una unidad de otro jugador (`INV-PLAYER-001`). | `unit.move.rejected` o `system.error`; la conexión continúa. |
| `FORBIDDEN` | Reservado: ninguna ruta del WebSocket lo emite, y el cierre `4403` tampoco. | — |
| `INTERNAL_ERROR` | Dentro de la sesión: la cola de comandos del loop está llena o el mundo no responde a tiempo. | `system.error` con el `requestId`, que se puede reintentar; la conexión continúa. Una cola de **salida** llena cierra con `4500`. |

`NOT_IMPLEMENTED` no lo emite ninguna ruta del MVP. El alta no pasa por el WebSocket: sus errores son los del
transporte HTTP de la identidad (§4.1), incluido `USERNAME_TAKEN` (409), que no está en el catálogo de 22
códigos del protocolo. El MVP no deja elegir civilización (§4.1), así que no hay error de civilización
inexistente.

## 9. Invariantes (con ID)

El registro **único** de invariantes es [../invariants/player.md](../invariants/player.md). Esta spec cita
`INV-PLAYER-001..004` con el significado que allí tienen —no los redefine— y asigna los nuevos a partir de
`INV-PLAYER-005`, que es el primer número libre de la familia.

### 9.1 Invariantes ya registrados que esta spec ejerce

| ID | Enunciado (según el registro) | Dónde aparece en esta spec |
|---|---|---|
| [`INV-PLAYER-001`](../invariants/player.md#inv-player-001) | Un jugador solo comanda unidades propias: un comando sobre una unidad se ejecuta si y solo si `units.player_id` es el `playerId` de la sesión autenticada. | §3, [unit.md](unit.md) §8 |
| [`INV-PLAYER-002`](../invariants/player.md#inv-player-002) | Exactamente una `civilization` y exactamente una `faction`, ambas `NOT NULL` y con FK resoluble. | `RN-PLAYER-004`, `RN-PLAYER-005` |
| [`INV-PLAYER-003`](../invariants/player.md#inv-player-003) | Un jugador nuevo nace, atómicamente, con una ciudad y tres aldeanos `VILLAGER`. | `RN-PLAYER-007`, §10.1 |
| [`INV-PLAYER-004`](../invariants/player.md#inv-player-004) | La identidad de la sesión proviene exclusivamente del claim `sub` de un ticket verificado y con `jti` consumido. | `RN-PLAYER-009`, `RN-PLAYER-010`, §7 |

Nótese que `INV-PLAYER-001` designa el **ownership de comandos**, no la unicidad del `uuid`, y que
`INV-PLAYER-003` cubre de una vez la ciudad y los tres aldeanos. El test de integración que lo cubre se
llama `TestBootstrapCreaMundoCompletoDelJugador`; el nombre `Test_INV_PLAYER_003_…` que citaba esta spec nunca existió.

### 9.2 Invariantes que esta spec asigna

Rango asignado: `INV-PLAYER-005..008`. Antes de citarse desde tests o código deben tener ficha en
[../invariants/player.md](../invariants/player.md) con el formato obligatorio del catálogo. La presencia
del jugador se documenta en [presence.md](presence.md) §9 y toma sus números por encima de éstos.

| ID | Invariante | Verificación |
|---|---|---|
| `INV-PLAYER-005` | `players.id` es `uuid` y único: no existen dos filas con el mismo id, y ningún otro mecanismo (correlativo, índice posicional) identifica a un jugador. | PK `uuid` en PostgreSQL + tipo `uuid.UUID` no puntero en el agregado de Go. |
| `INV-PLAYER-006` | `players.username` es único y cumple `^[A-Za-z0-9_-]{3,24}$`. La misma regla se aplica en el boundary (`player.ValidateUsername`) y en la base (`CHECK players_username_format`), de modo que ninguna ruta de escritura pueda saltársela. | `UNIQUE` + `CHECK` + unit test de tabla sobre `ValidateUsername`. |
| `INV-PLAYER-007` | Ninguna tabla guarda una contraseña en claro y ningún log estructurado contiene contraseña, hash, token de sesión, ticket ni `EO_AUTH_JWT_SECRET`. El esquema del juego no tiene ninguna columna de credencial; el almacén de identidad, sólo dos hashes. | Tests de esquema de las dos bases + tests que inspeccionan los logs del alta, el login, la fundación y el handshake. |
| `INV-PLAYER-009` | Toda cuenta `ACTIVE` del almacén de identidad tiene en el juego un jugador con el mismo id y nombre; una cuenta `PENDING` no obtiene sesión ni ticket. | Orquestación del alta en `apps/web` con un game server falso + idempotencia y carrera de la fundación contra PostgreSQL. |
| `INV-PLAYER-010` | Una cuenta `PENDING` sólo se borra si el game server confirma que su jugador no existe y, al borrarla, sigue `PENDING` y abandonada; ninguna limpieza deja un jugador sin cuenta. | Clasificación de las respuestas de la consulta + orquestación con un game server falso + la guarda del borrado contra PostgreSQL + el endpoint contra PostgreSQL. |
| `INV-PLAYER-008` | El comando `IntroducePlayer` que incorpora al jugador al mundo en RAM se emite **después** del COMMIT del bootstrap, nunca antes. No existe ningún instante en que el mundo en RAM conozca a un jugador que PostgreSQL no conoce. | Test de simulación con fallo inyectado en la transacción: el mundo no recibe la ciudad ni los aldeanos. |

No se asigna ningún invariante para «cada jugador tiene exactamente una ciudad»: esa propiedad es
[`INV-CITY-001`](../invariants/city.md#inv-city-001) del lado de la ciudad, y duplicarla desde la familia
`INV-PLAYER` crearía dos IDs para un solo hecho. Tampoco se asigna uno de idempotencia del alta: el alta
HTTP no es idempotente por `requestId` (`RN-PLAYER-006`), y un invariante que el código no garantiza es
una mentira documentada.

## 10. Persistencia

Respuesta a las cuatro preguntas del canon §12.

**1. Autoritativo en RAM del game server**

- Registro de conexiones WebSocket vivas por `playerId` (para el conteo de pestañas, ver [presence.md](presence.md)).
- Sesión en curso: `seq` saliente, límites de rate, centro de vista y conjunto de chunks suscritos.
- Catálogo de `civilizations`, `factions` y `eras` cargado al arranque y cacheado inmutable durante el proceso.

**2. Write-through inmediato y transaccional**

- Creación de `players`, `cities` y `units` (el bootstrap completo, en una sola transacción), más la fila
  de auditoría en `world_events`.
- Fila de `sessions` al aceptar el handshake y su cierre al desconectar.

**3. Dirty-flag + flush periódico (`EO_PERSISTENCE_FLUSH_INTERVAL_TICKS = 50`, 5 s)**

- Nada específico del jugador. `players` es una entidad de baja frecuencia de escritura; todos sus cambios son
  write-through.

**4. Reconstruible (no se persiste)**

- Presencia del jugador: se deriva de Redis + conexiones vivas.
- Conjunto de interés y suscripciones a chunks: se recalculan al conectar y con cada `session.view`.
- Valores efectivos derivados de `civilizations.traits`: se recalculan a partir del trait y del valor base.
  Nunca se persiste un valor ya modificado.

### 10.1 La transacción de bootstrap

Implementada en `internal/persistence/postgres/bootstrap.go` (`Bootstrapper.Create`). Las columnas son las
del DDL real; la secuencia, la que ejecuta el código:

```sql
BEGIN;

-- 1) jugador. El uuid lo eligió apps/web (ADR-018); el esquema no guarda credenciales.
INSERT INTO players (id, username, civilization_id, faction_id)
VALUES ($player_id, $username, $civilization_id, $faction_id);
-- una violación de UNIQUE(id) o UNIQUE(username) aborta con USERNAME_TAKEN, y la
-- fundación comprueba entonces si el jugador ya existía con ese id y ese nombre

-- 2) ciudad inicial: emplazamiento ya validado en memoria (ver city.md §6)
INSERT INTO cities (owner_player_id, name, center_x, center_y, era,
                    population, population_limit, presence_state, last_online_at)
VALUES ($player_id, $city_name, $cx, $cy, 'STONE_AGE',
        0, $era_population_cap, 'ONLINE', now())
RETURNING id;   -- → $city_id

-- 3) tres aldeanos alrededor del centro urbano, uno por INSERT.
--    chunk_x/chunk_y se derivan de x/y con EO_CHUNK_SIZE en el mismo statement.
INSERT INTO units (player_id, city_id, unit_type, x, y, hp, max_hp, status, chunk_x, chunk_y)
VALUES ($player_id, $city_id, 'VILLAGER', $x, $y, 40, 40, 'IDLE', $x / 32, $y / 32)
RETURNING id;

-- 4) población DERIVADA de las unidades vivas, no escrita a mano
UPDATE cities c
   SET population = COALESCE((
           SELECT count(*) FROM units u WHERE u.city_id = c.id AND u.status <> 'DEAD'
       ), 0),
       version = version + 1
 WHERE c.id = $city_id
RETURNING c.population;

-- 5) auditoría
INSERT INTO world_events (event_type, tick, player_id, payload)
VALUES ('PlayerBootstrapped', $tick, $player_id,
        '{"cityId": ..., "villagers": 3, "center": {"x": ..., "y": ...}}');

COMMIT;
```

Notas de diseño:

- Las columnas son `cities.owner_player_id`, `cities.center_x`, `cities.center_y` y `cities.id`; y
  `units.player_id`. Ni `cities.city_id` ni `units.owner_player_id` existen.
- `cities.era` referencia `eras(code)` en **texto** (`'STONE_AGE'`), no un `era_id` numérico.
- La **población se recalcula, no se asigna**: `UPDATE ... SET population = (SELECT count(*) ...)`. Escribir
  el literal `3` dejaría la ciudad desincronizada del mundo real en cuanto cambiara el número de aldeanos
  iniciales; derivarla hace imposible esa clase de bug.
- El **TOWN_CENTER** de la ciudad inicial (canon §10) no es una unidad y **no** tiene tabla propia: el canon §11
  fija las tablas del MVP y no incluye `buildings`. En MVP el Centro Urbano se representa por el propio tile
  `(center_x, center_y)` de la ciudad más el rectángulo 3×3 marcado en el overlay de ocupación —que se
  aplica al mundo en RAM con `IntroducePlayer`, no muta el terreno—. Una tabla general de edificios es
  `Fuera de MVP`. Ver [city.md](city.md) §6.
- La elección del emplazamiento y de los tres tiles de los aldeanos ocurre **en memoria y antes** de abrir la
  transacción (`founding.FindSite`, `RN-PLAYER-014`): la transacción no ejecuta búsquedas costosas con locks
  abiertos. Lo único que hace dentro es la comprobación barata de la distancia contra los centros ya
  confirmados.
- `population_limit` se toma de `eras.population_cap` de la era inicial, `STONE_AGE` (20). Nunca se escribe el
  literal 20 en el código: el `Bootstrapper` lo lee de `eras` dentro de la misma transacción que crea la
  ciudad, y la petición de alta lleva la era y no el límite (`INV-CITY-003`).
- `hp` y `max_hp` de los aldeanos salen de `unit.Lookup(VILLAGER).MaxHP` (40), no de un literal.
- La transacción **no** corre dentro del tick (canon §6: el tick jamás hace I/O bloqueante contra Postgres).
  Corre en la goroutine de la petición HTTP; el loop recibe el jugador ya creado a través del comando
  `IntroducePlayer`, encolado tras el COMMIT (`INV-PLAYER-008`).

## 11. Eventos

Eventos de dominio, nombrados en pasado (canon §15). Son la base de los deltas de red y de `world_events`.

| Evento | Payload | Emitido cuando | Consumidores |
|---|---|---|---|
| `PlayerBootstrapped` | `{ cityId, villagers, center: { x, y } }` | Dentro de la transacción de bootstrap. **Es el único que hoy se materializa como fila de `world_events`**, con `event_type = 'PlayerBootstrapped'`. | `world_events`, auditoría. |
| `CityCreated` | `{ cityId, ownerPlayerId, centerX, centerY, era, occurredAtMs }` | Commit del bootstrap. Ver [city.md](city.md). | Overlay de ocupación, difusión `city.update`. |
| `UnitSpawned` | `{ unitId, playerId, cityId, unitType, x, y, occurredAtMs }` (×3) | Commit del bootstrap, al aplicar `IntroducePlayer`. | `entity.spawn` a los suscriptores del chunk. |
| `PlayerSessionOpened` | `{ playerId, sessionId, occurredAtMs }` | `session.welcome` enviado. | Presencia, métricas. |
| `PlayerSessionClosed` | `{ playerId, sessionId, closeCode, occurredAtMs }` | Cierre de la conexión. | Presencia, métricas. |

Salvo `PlayerBootstrapped`, los demás son eventos **de dominio en RAM**: nombran el hecho y alimentan los
deltas de red y las métricas, pero no tienen hoy una fila propia en `world_events`. Añadirlos es aditivo y
no cambia el esquema.

`CityProtectionEngaged` es el evento canónico de protección y pertenece a [presence.md](presence.md).

## 12. Contratos de red

Los esquemas **Zod** de `packages/protocol/src/v1/` son la fuente de verdad; el JSON Schema exportado a
`packages/protocol/schema/v1/*.json` se embebe en el game server con `go:embed` y se usa en los *contract
tests*. Los payloads de abajo describen la **forma prevista**, no sustituyen al esquema.

`session.hello` (cliente→servidor):

```json
{
  "v": 1,
  "type": "session.hello",
  "requestId": "8b7c1f2e-0a4d-4f0b-9a1e-2c3d4e5f6a7b",
  "payload": { "ticket": "<JWT HS256>", "clientVersion": "web-0.1.0" }
}
```

`session.welcome` (servidor→cliente):

```json
{
  "v": 1,
  "type": "session.welcome",
  "seq": 1,
  "ts": 1767830400123,
  "requestId": "8b7c1f2e-0a4d-4f0b-9a1e-2c3d4e5f6a7b",
  "payload": {
    "sessionId": "1f2e3d4c-5b6a-4798-8a9b-0c1d2e3f4a5b",
    "playerId": "6f0f7f1a-3d21-4a5b-8c9d-0e1f2a3b4c5d",
    "serverTimeMs": 1767830400123,
    "tickDurationMs": 100,
    "heartbeatIntervalMs": 15000,
    "world": { "width": 512, "height": 512, "chunkSize": 32 }
  }
}
```

Reglas del contrato:

- El payload de `session.welcome` es exactamente `{ sessionId, playerId, serverTimeMs, tickDurationMs,
  heartbeatIntervalMs, world }`. **No lleva `displayName`, `civilization`, `faction`, `city` ni
  `tickRateHz`.** La cadencia se comunica como `tickDurationMs` (100 ms a 10 Hz), no como frecuencia: el
  cliente dimensiona su interpolación con una duración, no con un hercio que tendría que invertir.
- La ciudad del jugador no viaja en el `welcome`: llega en el `world.snapshot` inmediatamente posterior,
  dentro de `cities[]`, junto con el resto del área de interés. El alta y el login **sí** devuelven la
  ciudad en su respuesta HTTP (§4.1), que es lo que permite al cliente centrar la cámara antes incluso de
  abrir el WebSocket.
- `session.welcome` va siempre seguido de `world.snapshot` con el área de interés centrada en la ciudad del
  jugador y radio `EO_INTEREST_RADIUS_CHUNKS` (2).
- El cliente **no** aporta estado autoritativo en el handshake: ni posición, ni recursos, ni ownership. Solo el
  ticket.
- Donde la civilización y la facción sí aparecen, viajan como `code` en texto (`"ROMAN"`, `"NEUTRAL"`),
  nunca como id numérico interno.
- Ningún payload incluye traits resueltos: los valores efectivos son responsabilidad del servidor y el
  cliente no los necesita para simular (no simula).
- Los mensajes cliente→servidor validan contra esquemas **estrictos** (`additionalProperties: false`): un
  campo extra en `session.hello` se rechaza con `INVALID_MESSAGE`. Los servidor→cliente **no** son
  estrictos, para poder añadir campos opcionales sin romper clientes antiguos.

## 13. Tests esperados

| ID | Nivel | Descripción | Cubre |
|---|---|---|---|
| `T-PLAYER-U-001` | unit | `Civilization.Trait` sobre una clave ausente devuelve el valor por defecto; sobre `Traits == nil` también. Escrito: `TestTraitDevuelveElValorODelPorDefecto`. | `RN-PLAYER-012` |
| `T-PLAYER-U-002` | unit | `Civilization.Trait` sobre una clave presente devuelve su multiplicador exacto, sin depender del orden de iteración del mapa. Escrito: `TestTraitDevuelveElValorODelPorDefecto`. | `RN-PLAYER-012` |
| `T-PLAYER-U-003` | unit | `player.ValidateUsername`: tabla de casos con 2, 3, 24 y 25 caracteres, y con espacios, acentos, punto y `@`. Solo `[A-Za-z0-9_-]{3,24}` pasa. Escritos: `TestValidateUsernameAplicaLaReglaDelEsquema`, `TestUnNombreConCaracteresMultibyteNoPasaPorLongitud`. | `RN-PLAYER-003`, `INV-PLAYER-006` |
| `T-PLAYER-U-004` | unit | Verificación del ticket con `FakeClock`: válido, `exp` vencido, `aud` incorrecto, firma alterada, `alg=none`, `jti` ausente, claims incompletos. Escritos en `internal/auth`: `TestTicketValidoSeVerifica`, `TestTicketCaducado`, `TestAudienciaIncorrecta`, `TestTicketFirmadoConOtroSecreto`, `TestAlgoritmoNoneSeRechaza`, `TestClaimsIncompletos`. | `RN-PLAYER-009`, `INV-PLAYER-004` |
| `T-PLAYER-U-005` | unit | `founding.FindSite` con la misma semilla devuelve el mismo emplazamiento; con ciudades existentes respeta la separación de 24 tiles; sin sitio devuelve `ErrNoSite`. Escritos: `TestLaBusquedaEsDeterminista`, `TestVariasFundacionesGuardanLaDistanciaYNoSeSolapan`, `TestSinTerrenoTransitableNoHaySitio`. | `RN-PLAYER-014` |
| `T-PLAYER-I-001` | integration | Bootstrap completo: 1 fila en `players`, 1 en `cities`, 3 en `units` con `hp = max_hp = 40` y `status='IDLE'`, `cities.population = 3`. Escrito: `TestBootstrapCreaMundoCompletoDelJugador`. | `RN-PLAYER-007`, `INV-PLAYER-003` |
| `T-PLAYER-I-002` | integration | Un fallo a mitad del alta no deja ninguna fila en `players`, `cities` ni `units`. Escritos: `TestBootstrapFallidoNoDejaNadaAMedias`, `TestUnAltaConOtroNumeroDeAldeanosSeRechazaSinEscribir`. | `RN-PLAYER-007`, `INV-PLAYER-003` |
| `T-PLAYER-I-003` | integration | Alta repetida con el mismo `username`: la segunda falla con `USERNAME_TAKEN` (409) y no crea ninguna fila adicional. Escritos: `TestUnNombreDeOtroJugadorNoSeFundaNiSeReasigna` (por la fundación), `TestElAltaRegistraUnEventoYNoSeDuplica`; en `apps/web`, «un nombre ya tomado no llega al Game Server» (`authService.test.ts`). | `RN-PLAYER-006`, `INV-PLAYER-006` |
| `T-PLAYER-I-004` | integration | Alta de un jugador por cada combinación (civilización × facción): las doce tienen éxito. Falla si alguien acopla los ejes. Escrito: `TestLosDosEjesSonOrtogonales`. | `RN-PLAYER-005`, `INV-PLAYER-002` |
| `T-PLAYER-I-005` | e2e | `jti` reutilizado: el segundo handshake se rechaza con el cierre `4401`, sin `system.error` previo (el handshake se comunica sólo con códigos de cierre). Escrito: `TestTicketNoSePuedeReutilizarEnOtraConexion`. | `RN-PLAYER-009`, `INV-PLAYER-004` |
| `T-PLAYER-I-006` | integration | `INSERT` directo con `username` de 2 caracteres o con `@`: rechazado por `players_username_format`. Escritos: `TestLasRestriccionesDeJugadoresRechazanFilasInvalidas`, `TestValidateUsernameYElCheckDicenLoMismo`. | `INV-PLAYER-006` |
| `T-PLAYER-I-007` | integration | `INSERT` con `civilization_id` o `faction_id` nulo o inexistente: rechazado por `NOT NULL` / FK. Escrito: `TestLasRestriccionesDeJugadoresRechazanFilasInvalidas`. | `INV-PLAYER-002` |
| `T-PLAYER-C-001` | contract | `session.hello` y `session.welcome` existen en el JSON Schema exportado por `@empires-online/protocol`. Escritos: `TestTiposDeMensajePresentesEnElEsquema`; y todo `session.welcome` y `world.snapshot` que emite el servidor real en los e2e se valida contra el esquema (`schema_e2e_test.go`). | §12 |
| `T-PLAYER-C-002` | contract | `session.hello` con un campo extra (`playerId`, por ejemplo) **no** valida: los mensajes cliente→servidor son estrictos, también en el decodificador Go. Escrito: `TestElDecodificadorEsTanEstrictoComoElEsquema`. | `INV-PLAYER-004` |
| `T-PLAYER-C-003` | contract | El handshake no emite `system.error`: sus rechazos son códigos de cierre del rango privado ([ADR-019](../decisions/ADR-019-handshake-failure-close-code.md)). Escritos: `TestCodigosDeCierreSonDelRangoPrivado` (el rango), y `TestTicketInvalidoCierraLaConexion`, `TestNoSeAceptanComandosSinAutenticar` y `TestUnHandshakeIlegibleCierraCon4400SinMensaje` (e2e: la primera lectura es el cierre). | §8 |
| `T-PLAYER-E-001` | e2e | Conexión sin `session.hello` dentro del plazo → cierre `4408`. Escrito: `TestSinHandshakeEnPlazoSeCierraCon4408`. | `RN-PLAYER-010` |
| `T-PLAYER-E-002` | e2e | Handshake correcto → `session.welcome` seguido de `world.snapshot` centrado en la ciudad. Escrito: `TestVerticalSliceEndToEnd`. | `RN-PLAYER-011` |
| `T-PLAYER-E-003` | integration | Alta → el mundo en RAM no conoce al jugador hasta después del COMMIT; con la transacción abortada, no se introduce nada. Escritos: `TestLaFundacionIntroduceAlJugadorDespuesDelCommit`, `TestUnNombreDeOtroJugadorNoSeFundaNiSeReasigna`, `TestSinSitioLaFundacionResponde409YNoEscribe`. | `INV-PLAYER-008` |
| `T-PLAYER-I-008` | integration | La fundación es idempotente por `playerId`, también con el mundo lleno, y dos fundaciones simultáneas del mismo id acaban en un jugador. Escritos: `TestLaFundacionEsIdempotentePorPlayerId`, `TestLaRepeticionNoBuscaSitioAunqueElMundoEsteLleno`, `TestDosFundacionesSimultaneasDelMismoJugadorAcabanEnUno`. | `INV-PLAYER-009` |
| `T-PLAYER-I-009` | integration | Sólo un token de fundación funda, una vez y sólo su `sub`: `TestUnTicketDeJuegoNoFundaJugadores`, `TestUnTokenDeFundacionSirveUnaVezYSoloParaSuSub`; y las audiencias son disjuntas (`TestLasAudienciasSonDisjuntas`, unit). | `INV-SEC-008` |
| `T-PLAYER-W-001` | unit (`apps/web`) | Orquestación del alta y del login: activación sólo tras la fundación, borrado ante rechazo definitivo, `PENDING` sin sesión ante indisponibilidad y su cierre en el login, respuestas idénticas ante contraseña errónea y usuario inexistente, ticket por cookie, logs sin secretos. `src/server/authService.test.ts`. | `INV-PLAYER-007`, `INV-PLAYER-009` |
| `T-PLAYER-W-002` | integration (`apps/web`) | Almacén de identidad contra PostgreSQL real: migraciones idempotentes, restricciones, sesiones sólo de cuentas `ACTIVE`, y el rol de identidad sin acceso a la base del juego. `src/server/store.integration.test.ts`. | `INV-PLAYER-009`, ADR-018 |
| `T-PLAYER-W-004` | unit (`apps/web`) | Cuentas abandonadas: sólo `404 PLAYER_NOT_FOUND` es «no existe» (`identity/pending.test.ts`); el alta libera un nombre abandonado sin jugador y reintenta una vez, y no lo libera si es reciente, si el jugador existe o si el mundo no contesta; el login renueva el intento antes de fundar y no funda una cuenta borrada; el barrido cuenta lo que hizo (`authService.test.ts`). | `RN-PLAYER-015`, `RN-PLAYER-016`, `INV-PLAYER-010` |
| `T-PLAYER-W-005` | integration (`apps/web`) | La migración 2 y la guarda del borrado contra PostgreSQL real: el intento se fija y se renueva sólo en `PENDING`, el corte de una hora, el orden del barrido y el borrado que no toca recientes, `ACTIVE` ni renovadas. `store.integration.test.ts`. | `INV-PLAYER-010` |
| `T-PLAYER-W-006` | unit (`apps/web`) | Perímetro HTTP de la identidad: la IP es el salto que añadió el último proxy y un `X-Forwarded-For` falsificado no evita el cupo por IP; sin proxy la cabecera se ignora y manda el tope global; un usuario no agota el cupo de otro y las mayúsculas no abren otro; el tope global corta aunque cambien IP y usuario; el ticket y el cierre de sesión se limitan por sesión, por IP y en global, con la clave de sesión en el log como hash, y sin cookie no gastan cupo; el cuerpo se rechaza sin leerlo si `Content-Length` pasa de 4096 bytes y, sin cabecera, se deja de leer al pasar el tope. `src/server/edges.test.ts`. | §4.1, `INV-PLAYER-007` |
| `T-PLAYER-I-010` | integration | `GET /internal/players/{id}`: 200 con el jugador fundado, 404 `PLAYER_NOT_FOUND` con uno inexistente, y sólo con un token interno de ese `sub`. `TestLaConsultaDistingueUnJugadorFundadoDeUnoQueNoExiste`, `TestLaConsultaExigeUnTokenInternoDeSuSub`. | `INV-PLAYER-010`, `INV-SEC-008` |
| `T-PLAYER-W-003` | contract | El emisor de TypeScript produce los bytes exactos de `internal/auth/testdata/web-tokens.json` y el verificador de Go los acepta. `tokens.test.ts`, `TestLosTokensDeAppsWebSeVerificanEnGo`. | ADR-018 §2.4 |

Los tests de integración necesitan PostgreSQL y Redis reales y están gated por `EO_INTEGRATION=1` (canon §3,
§19). La CI los ejecuta en cada push; en la máquina de desarrollo se ejecutan contra el cluster propio de
PostgreSQL en el puerto 5433 y Redis en WSL, porque Docker está descartado
([../operations/local-development.md](../operations/local-development.md) §3-bis).
