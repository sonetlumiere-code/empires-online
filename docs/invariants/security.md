# Invariantes de Seguridad (INV-SEC-xxx)

Propiedades de autoridad del servidor, autenticación, autorización, límites de abuso, gestión de secretos e idempotencia.

Formato y severidades: [README.md](README.md). Autenticación: canon §14. Protocolo y límites: canon §13.

---

## Contexto

El principio no negociable del canon §1.1 es la frase que gobierna este archivo entero:

> **Client sends intent, server determines truth.**

El cliente **nunca** aporta estado autoritativo: ni posición final, ni HP, ni recursos, ni resultados de combate, ni ownership, ni cooldowns, ni ETA, ni paths. Los invariantes de este documento son las condiciones concretas bajo las cuales esa frase es verdadera en la implementación y no solo en la intención.

### Superficie de ataque del MVP

Solo existe una superficie: la conexión WebSocket. Cinco tipos de mensaje cliente→servidor en v1:

| Tipo | Payload | Requiere sesión |
|---|---|---|
| `session.hello` | `{ ticket }` | No — **es** el que la establece |
| `session.ping` | — | Sí |
| `session.view` | centro de vista | Sí |
| `unit.move` | `{ unitId, target:{x,y} }` | Sí |
| `unit.cancel_move` | `{ unitId }` | Sí |

Límites del transporte (canon §13):

| Parámetro | Valor | Variable |
|---|---|---|
| Tamaño máximo de mensaje | 16 KiB | `EO_WS_MAX_MESSAGE_BYTES = 16384` |
| Tasa | 20 msg/s | `EO_WS_RATE_LIMIT_PER_SECOND = 20` |
| Burst | 40 | `EO_WS_RATE_LIMIT_BURST = 40` |
| Ping | cada 15 s | — (`WSPingInterval`, constante de código) |
| Timeout de lectura | 45 s | — (`WSReadTimeout`, constante de código) |
| Timeout de escritura | 10 s | — (`WSWriteTimeout`, constante de código) |
| Timeout de handshake | 5 s | — (`WSHandshakeTimeout`, constante de código) |
| Cola de salida por conexión | 256 | `EO_WS_OUTBOUND_QUEUE_SIZE` (rango 8–65536) |

> **Un único token bucket.** En MVP existe **un solo** limitador de tasa por conexión: 20 msg/s con burst 40, aplicado por igual a los cinco tipos de mensaje. `session.view` **no** tiene un sub-límite dedicado; el canon §13 dice que `session.view` actualiza el centro «con rate limit», refiriéndose a ese límite global, y no define ninguno adicional. Un sub-límite propio para `session.view` —justificable, porque recalcular conjuntos de interés es más caro que responder un ping— es **TBD (fuera de MVP)**.
>
> Los cuatro timeouts de la tabla son **constantes de código**, no variables de entorno: no se pueden ajustar por despliegue. La única variable `EO_WS_*` añadida sobre el canon §17 es `EO_WS_OUTBOUND_QUEUE_SIZE`.

Códigos de cierre WS (canon §14): `4400` mensaje inválido, `4401` no autenticado, `4403` no autorizado, `4408` timeout de handshake, `4429` rate limited, `4500` error interno.

---

<a id="inv-sec-001"></a>
## INV-SEC-001 — El estado del cliente nunca muta el estado autoritativo

| Campo | Valor |
|---|---|
| Severidad | CRITICO |
| Aplicación | TYPE, BOUNDARY, TEST |
| Milestone | M3 |
| Política ante violación | FAIL_FAST |
| Cobertura | **Cubierto** por los 18 tests de `packages/protocol` (Vitest), por `TestElComandoDeMovimientoNoAdmiteRuta` y `TestEntityUpdateEsUnDeltaDeVerdad` (`internal/protocol`) y por `TestPayloadConCamposExtraNoAportaEstado` (`internal/websocket`) |

**Enunciado.** Ningún valor procedente de un mensaje del cliente se escribe directamente en el estado autoritativo. Los payloads solo pueden expresar **intención** (qué unidad, qué destino, qué centro de vista) y todo valor derivado —posición, path, tiempos, ETA, resultado— lo calcula el servidor.

**Razón.** Es el invariante del que dependen todos los demás. Si el cliente pudiera escribir su propia posición, sobraría el pathfinding y sobraría el modelo de bloqueo: bastaría enviar «estoy en el centro de la ciudad enemiga». Si pudiera escribir el `hp`, sobraría el combate. Si pudiera escribir los tiempos, sobraría la velocidad de las unidades.

La forma en que este invariante se rompe en la práctica no es un descuido grosero, sino una comodidad: añadir al payload de `unit.move` un campo `estimatedArrival` «para que el cliente no espere», o un `path` «que el cliente ya calculó para la previsualización», y usarlo en el servidor. Por eso el invariante se garantiza estructuralmente en el esquema del protocolo y no con disciplina.

**Cómo se garantiza.**

- `TYPE` — los esquemas Zod de `packages/protocol/src/v1/` son la fuente de verdad del protocolo (canon §13). Los payloads cliente→servidor **no contienen** ningún campo de estado derivado: `unit.move` es exactamente `{ unitId, target:{x,y} }` y nada más. El canon lo dice explícitamente en §7: el cliente «NUNCA envía secuencias de posiciones».
- `TYPE` — los esquemas **cliente→servidor** son estrictos (`additionalProperties: false`): un campo desconocido hace fallar la validación en lugar de ignorarse silenciosamente. Un cliente que intenta colar `path` o `hp` recibe `INVALID_MESSAGE`, no un descarte silencioso que podría convertirse en aceptación tras un refactor. Los esquemas **servidor→cliente NO son estrictos**, deliberadamente, para poder añadir campos opcionales sin romper clientes antiguos: estricto donde entra dato ajeno, tolerante donde sale dato propio.
- `BOUNDARY` — el decodificador de Go valida a mano por rendimiento (canon §13) pero se verifica contra el JSON Schema embebido por `go:embed` en `internal/protocol/schema/v1/`, espejo generado por `pnpm run protocol:build`: la implementación rápida no puede divergir del esquema.
- `DOMAIN` — la posición durante un movimiento es **derivada** (`Movement.PositionAt`, [INV-MOVE-006](movement.md#inv-move-006), [INV-MOVE-009](movement.md#inv-move-009)) y la interpolación sub-tile es exclusivamente visual y del cliente (canon §7). No hay ninguna ruta por la que el cliente informe su posición ([INV-MOVE-010](movement.md#inv-move-010)).
- `DOMAIN` — el `presence_state` de la ciudad lo decide el servidor y el cliente solo lo observa por `city.update` (canon §9); no hay mensaje cliente→servidor que lo altere ([INV-CITY-005](city.md#inv-city-005)).

**Cómo se verifica.**

- `packages/protocol` (Vitest, 18 tests en verde) — **existen y pasan**: «rechaza un payload que intente aportar estado autoritativo desconocido», «unit.move no admite que el cliente envíe una ruta» y «no ha derivado respecto de los esquemas Zod (misma comprobación que la CI)».
- `TestElComandoDeMovimientoNoAdmiteRuta` (contract, `internal/protocol`) — **existe y pasa**: el espejo embebido en Go tampoco admite una ruta del cliente.
- `TestEntityUpdateEsUnDeltaDeVerdad` (contract, `internal/protocol`) — **existe y pasa**: el servidor emite deltas parciales calculados por él, no ecos del cliente.
- `TestPayloadConCamposExtraNoAportaEstado` (e2e, `internal/websocket`) — **existe y pasa**: un cliente que envía campos extra no altera el estado; la unidad sigue donde el servidor calculó.
- `TestEjemploNumericoCanonico` (unit, `internal/domain/movement`) — **existe y pasa**: el tiempo de llegada depende únicamente del terreno y del `baseMsPerTile` del catálogo, no de nada enviado por el cliente.

**Violación en runtime.** Detección en los contract tests de CI (preventiva) y en el decodificador ante campos desconocidos. Log `invariant_violation` con `inv_id=INV-SEC-001`, `session_id` y el campo ofensivo. Política `FAIL_FAST` del mensaje: se responde `INVALID_MESSAGE` y no se procesa. Un PR que añada un campo de estado a un payload cliente→servidor debe bloquearse en revisión aunque los tests pasen.

---

<a id="inv-sec-002"></a>
## INV-SEC-002 — Ninguna conexión ejecuta comandos antes de autenticarse

| Campo | Valor |
|---|---|
| Severidad | CRITICO |
| Aplicación | TYPE, BOUNDARY, TEST |
| Milestone | M3 |
| Política ante violación | FAIL_FAST (cierre de la conexión) |
| Cobertura | **Cubierto** por `TestPreAuthSoloAdmiteElHandshake`, `TestNoSeAceptanComandosSinAutenticar`, `TestTicketInvalidoCierraLaConexion` y `TestTicketNoSePuedeReutilizarEnOtraConexion` (`internal/websocket`) |

**Enunciado.** Una conexión WebSocket solo acepta `session.hello` hasta que el ticket ha sido verificado y la sesión creada. Cualquier otro tipo de mensaje recibido antes se rechaza con `UNAUTHORIZED` y cierra la conexión con `4401`; si no llega un `session.hello` válido en 5 segundos, la conexión se cierra con `4408`.

**Razón.** Sin sesión no hay `playerId`, y sin `playerId` no hay ownership que comprobar. Un `unit.move` procesado antes del handshake tendría que compararse contra una identidad vacía: en Go, el valor cero de `uuid`. Según cómo esté escrita la comparación, eso puede resultar verdadero para unidades cuyo owner también estuviera vacío, o simplemente entrar en una rama no prevista. En cualquier caso, es una ejecución de comando sin autorización.

El timeout de 5 segundos cubre un segundo problema, de disponibilidad: conexiones abiertas que nunca se autentican consumen file descriptors, memoria y una entrada en `eo_connected_websockets`. Sin timeout, abrir sockets y no decir nada es un ataque de agotamiento de recursos trivial de montar.

**Cómo se garantiza.**

- `TYPE` — la conexión tiene dos estados y el conjunto de mensajes admitidos depende del estado en el **despachador**, no en cada handler. Antes del handshake, el despachador solo conoce `session.hello`; el resto ni siquiera se enruta.
- `BOUNDARY` — el temporizador de 5 s (`WSHandshakeTimeout`) arranca al establecerse la conexión, antes de leer el primer byte. Al vencer, cierre `4408`.
- `BOUNDARY` — `conn.SetReadLimit(EO_WS_MAX_MESSAGE_BYTES)` se aplica **antes** del handshake, en `Server.serve`, de modo que ni siquiera un `session.hello` puede desbordar memoria.
- `BOUNDARY` — la verificación del ticket es completa antes de crear la sesión (canon §14): firma HS256 con `EO_AUTH_JWT_SECRET`, `exp`, `aud = "game-server"` y consumo del `jti` ([INV-SEC-003](#inv-sec-003)). Un fallo en cualquier paso cierra con `4401` sin crear sesión.
- `DOMAIN` — el `playerId` de la sesión se establece una sola vez y es inmutable ([INV-PLAYER-004](player.md#inv-player-004)).

> **Qué protege realmente la fase previa al handshake.** El límite de **tamaño** sí se aplica antes (`SetReadLimit` en `Server.serve`), pero el **token bucket NO**: `newRateLimiter` se construye en `readPump`, que sólo se ejecuta con la sesión ya establecida. Lo que acota el coste de una conexión no autenticada es la combinación de tres cosas: el límite de lectura, el timeout de 5 s y que el handshake acepta **un solo mensaje** —si no es un `session.hello` válido, la conexión se cierra sin volver a leer—. Una conexión no autenticada no puede, por tanto, enviar una ráfaga: no hay un bucle que la lea. Extender el token bucket a la fase de handshake, para acotar también el coste de conexiones que abren y cierran en serie, es **TBD (fuera de MVP)**.

**Cómo se verifica.**

- `TestNoSeAceptanComandosSinAutenticar` (e2e, `internal/websocket`) — **existe y pasa**: se abre el socket y se envía `unit.move` como primer mensaje; no se ejecuta y la conexión se cierra.
- `TestPreAuthSoloAdmiteElHandshake` (e2e, `internal/websocket`) — **existe y pasa**: antes del handshake el despachador sólo conoce `session.hello`.
- `TestTicketInvalidoCierraLaConexion` (e2e, `internal/websocket`) — **existe y pasa**: los tickets inválidos cierran con `4401`.
- `TestTicketNoSePuedeReutilizarEnOtraConexion` (e2e, `internal/websocket`) — **existe y pasa**: el segundo canje no establece sesión.
- `TestCodigosDeCierreSonDelRangoPrivado` (contract, `internal/protocol`) — **existe y pasa**: `4400`, `4401`, `4403`, `4408`, `4429` y `4500` están en el rango privado.
- Test previsto: `Test_INV_SEC_002_HandshakeTimeoutClosesConnection` (integration) — socket abierto sin enviar nada: cierre con `4408` transcurridos 5 s.

**Violación en runtime.** Detección en el despachador (mensaje enrutado sin sesión) y en una aserción de los handlers de comando, que exigen sesión establecida. Log `invariant_violation` con `inv_id=INV-SEC-002`, `session_id` y el tipo de mensaje. Política `FAIL_FAST`: cierre inmediato con `4401` sin procesar nada más de esa conexión.

---

<a id="inv-sec-003"></a>
## INV-SEC-003 — Un ticket solo puede canjearse una vez

| Campo | Valor |
|---|---|
| Severidad | CRITICO |
| Aplicación | BOUNDARY, TEST |
| Milestone | M3 |
| Política ante violación | FAIL_FAST (cierre `4401`) |
| Cobertura | **Cubierto** por `TestTicketSoloSeConsumeUnaVez`, `TestTicketNoSePuedeReutilizar`, `TestCadaTicketTieneUnJtiDistinto` y `TestFalloDelRegistroDeTicketsFallaCerrado` (`internal/auth`) y por `TestTicketNoSePuedeReutilizarEnOtraConexion` (`internal/websocket`) |

**Enunciado.** El `jti` de todo game ticket verificado se consume atómicamente en Redis bajo la clave `ticket:jti:{jti}` con TTL 120 s; un segundo intento de canjear el mismo `jti` se rechaza y cierra la conexión con `4401`.

**Razón.** Es la defensa contra **replay**. El ticket es un JWT HS256 con TTL de 60 s (`ticketTTL`, constante de `cmd/server/main.go`) que viaja del navegador al Game Server. Sin consumo del `jti`, cualquiera que capture el ticket dentro de esa ventana —un log mal configurado, una extensión del navegador, un proxy corporativo, el propio jugador compartiéndolo— puede abrir sesiones como ese jugador durante 60 segundos. El TTL corto reduce la ventana, pero no la cierra: solo el consumo único la cierra.

El TTL de 120 s de la clave es deliberadamente el doble del TTL del ticket: garantiza que el registro anti-replay sobrevive a cualquier ticket que pudiera seguir siendo válido, con margen para desfase de reloj entre el emisor Next.js y el Game Server. Que el `jti` desaparezca después no importa: para entonces el ticket ya expiró y `exp` lo rechaza por sí solo.

**Cómo se garantiza.**

- `BOUNDARY` — el consumo es `SETNX ticket:jti:{jti} "1" EX 120`, una operación **atómica** de Redis en una sola llamada (`TicketStore.Consume`). No es un `EXISTS` seguido de un `SET`: dos conexiones simultáneas con el mismo ticket entrarían ambas por esa ventana.
- `BOUNDARY` — el orden de verificación es: firma HS256 → `exp` → `aud = "game-server"` (`auth.Audience`) → presencia del `jti` → consumo del `jti`. El consumo va al final, para no gastar entradas de Redis con tickets que ya son inválidos por otra razón; y va **antes** de crear la sesión, para que no exista sesión sin ticket consumido.
- `BOUNDARY` — un ticket **sin `jti`** se rechaza en la verificación, antes del consumo: sin `jti` no se puede impedir el replay, así que el ticket no sirve. Es una decisión explícita del verificador, no un efecto colateral.
- `BOUNDARY` — el fallo del consumo (clave ya existente) cierra con `4401`. No se distingue en la respuesta al cliente entre «ticket ya usado» y «ticket inválido»: ambas son `UNAUTHORIZED`.
- `DOMAIN` — la indisponibilidad de Redis en el momento del consumo se trata como **fallo cerrado**: si no se puede garantizar la unicidad, no se autentica. Se cierra con `4500` y se registra. Autenticar «por si acaso» convertiría una caída de Redis en una ventana de replay abierta.
- Nunca se registra el ticket completo en logs; solo el `jti`, que ya está consumido y no sirve para nada.

**Cómo se verifica.**

- `TestTicketSoloSeConsumeUnaVez` (unit, `internal/auth`) — **existe y pasa**: el segundo consumo del mismo `jti` devuelve falso.
- `TestTicketNoSePuedeReutilizar` (unit, `internal/auth`) — **existe y pasa**: el ticket ya canjeado no verifica.
- `TestCadaTicketTieneUnJtiDistinto` (unit, `internal/auth`) — **existe y pasa**: el emisor no repite `jti`, que es lo que hace útil el registro anti-replay.
- `TestFalloDelRegistroDeTicketsFallaCerrado` (unit, `internal/auth`) — **existe y pasa**: si el registro no responde, **no se autentica**. Es el fail-closed del enunciado.
- `TestTicketNoSePuedeReutilizarEnOtraConexion` (e2e, `internal/websocket`) — **existe y pasa**: el mismo ticket en dos conexiones sólo crea una sesión.
- `TestPresenciaSeRegistraYExpira` y `TestLatidoRenuevaLaPresencia` (integration, `internal/persistence/redis`) — **diseñados, aún no ejecutados**: cubren el comportamiento de TTL del que depende `ticket:jti:{jti}`.
- Test previsto: `Test_INV_SEC_003_JtiKeyHasTtl` (integration) — `ticket:jti:{jti}` existe con TTL 120 s tras el canje.

**Violación en runtime.** Detección en el resultado del consumo atómico. Log `invariant_violation` con `inv_id=INV-SEC-003`, `jti` y `player_id` del claim `sub`. Política `FAIL_FAST`: cierre `4401`. Un segundo canje del mismo `jti` es indistinguible de un intento de replay y se registra como evento de seguridad, no como error ordinario.

---

<a id="inv-sec-004"></a>
## INV-SEC-004 — Todo comando valida ownership antes de ejecutarse

| Campo | Valor |
|---|---|
| Severidad | CRITICO |
| Aplicación | DOMAIN, TEST |
| Milestone | M4 |
| Política ante violación | REJECT |
| Cobertura | **Cubierto** por `TestRechazosDeMovimiento` y `TestCatalogoDeComandosEsCerrado` (`internal/game/simulation`) y por `TestNoSePuedeComandarUnaUnidadAjena` (`internal/websocket`) |

**Enunciado.** Para todo comando que referencia una entidad, la comprobación de ownership contra el `playerId` de la sesión se ejecuta **antes** de cualquier mutación de estado, de cualquier escritura en base de datos y de cualquier trabajo costoso como el pathfinding.

**Razón.** Es la contraparte procedimental de [INV-PLAYER-001](player.md#inv-player-001): aquel invariante dice que el resultado del chequeo determina la ejecución; este dice **cuándo** ocurre el chequeo. Se separan porque se rompen por causas distintas, y este se rompe por reordenación de código: alguien mueve el cálculo del path antes de la validación «para hacerlo en paralelo», o inserta una escritura de telemetría antes de validar.

El orden importa por dos razones concretas:

- **Efectos parciales** — validar después de mutar deja el sistema con un cambio que hay que deshacer. La compensación es código adicional, se ejecuta poco y por tanto es donde viven los bugs.
- **Amplificación** — el A\* tiene un presupuesto de `EO_PATHFINDING_MAX_NODES = 20000` nodos. Ejecutarlo antes de validar ownership permite a un atacante consumir ese presupuesto 20 veces por segundo (`EO_WS_RATE_LIMIT_PER_SECOND`) con unidades ajenas: gasto de CPU dentro del tick del juego, que se traduce en `eo_game_tick_overruns_total` para todos los jugadores.

El canon §7 fija el orden explícitamente: **validar ownership → validar estado de unidad → validar destino → ejecutar A\* → crear el movimiento → simular → notificar**.

**Cómo se garantiza.**

- `DOMAIN` — el pipeline de `Simulation.handleMoveUnit` es una secuencia explícita y única, no una colección de validaciones dispersas: existencia de la unidad → `EnsureOwnedBy` → `EnsureCanMove` → límites del destino → transitabilidad → caso degenerado → `unit.Lookup` → A\* → construir polilínea → registrar. La autorización es la primera etapa que puede fallar por culpa del jugador.
- `DOMAIN` — ownership antes que estado de unidad, de forma deliberada: intentar mover una unidad `DEAD` ajena debe responder `UNIT_NOT_OWNED`, no `UNIT_DEAD`. Un error de estado sobre una unidad ajena filtra información.
- `DOMAIN` — el `playerId` usado en la comparación proviene de la sesión ([INV-PLAYER-004](player.md#inv-player-004)); ningún payload v1 lo contiene.
- `DOMAIN` — las mutaciones ocurren en una única transacción al final del pipeline. Antes de esa transacción, ninguna etapa escribe.
- `DOMAIN` — códigos estables (canon §16): `UNIT_NOT_FOUND`, `UNIT_NOT_OWNED`, `FORBIDDEN`.

**Cómo se verifica.**

- `TestRechazosDeMovimiento` (simulation, `internal/game/simulation`) — **existe y pasa**: los seis rechazos del pipeline, con la comprobación explícita de que «un comando rechazado no puede producir ningún movimiento».
- `TestNoSePuedeComandarUnaUnidadAjena` (e2e, `internal/websocket`) — **existe y pasa**: la unidad ajena se rechaza con `UNIT_NOT_OWNED` de extremo a extremo.
- `TestCatalogoDeComandosEsCerrado` (simulation, `internal/game/simulation`) — **existe y pasa**: no hay comandos fuera del catálogo que pudieran saltarse el pipeline.
- Test previsto: `Test_INV_SEC_004_OwnershipCheckedBeforeExecution` (unit) — con un `Pathfinder` instrumentado, un `unit.move` sobre una unidad ajena no invoca el pathfinder ni una sola vez.
- Test previsto: `Test_INV_SEC_004_OwnershipErrorPrecedesStateError` (unit) — unidad `DEAD` ajena devuelve `UNIT_NOT_OWNED`, no `UNIT_DEAD`.

**Violación en runtime.** Detección en una aserción del pipeline (etapa de mutación alcanzada sin etapa de autorización completada). Log `invariant_violation` con `inv_id=INV-SEC-004`, `player_id`, `request_id` y el comando. Política `REJECT` con `UNIT_NOT_OWNED` o `FORBIDDEN`. Una mutación consumada sobre una entidad ajena es un incidente de seguridad y requiere revisión manual del alcance.

---

<a id="inv-sec-005"></a>
## INV-SEC-005 — Los mensajes fuera de límite se rechazan sin procesarse

| Campo | Valor |
|---|---|
| Severidad | ALTO |
| Aplicación | BOUNDARY, TEST |
| Milestone | M3 |
| Política ante violación | REJECT + cierre |
| Cobertura | **Cubierto** por `TestMensajeMalformadoNoTumbaLaSesion` y `TestTipoDeMensajeDesconocidoSeRechaza` (`internal/websocket`) y por `TestBurstNoPuedeSerMenorQueLaTasa` y `TestValoresPorDefectoDelCanon` (`internal/config`); **no existe todavía** un test que ejercite el rechazo por tamaño ni la ráfaga real |

**Enunciado.** Un mensaje que supera `EO_WS_MAX_MESSAGE_BYTES = 16384` bytes se rechaza **sin deserializarse**; una conexión que supera `EO_WS_RATE_LIMIT_PER_SECOND = 20` mensajes por segundo con burst `EO_WS_RATE_LIMIT_BURST = 40` ve sus mensajes excedentes rechazados **sin enrutarse a ningún handler**.

**Razón.** La palabra clave del enunciado es «sin». No basta con responder un error: el rechazo debe ocurrir **antes** del trabajo que el límite pretende evitar, o el límite no protege de nada.

- **Tamaño** — deserializar un JSON de 50 MiB para después responder `MESSAGE_TOO_LARGE` ya ha asignado 50 MiB. Repetido en paralelo desde varias conexiones, es un agotamiento de memoria. El límite debe aplicarlo el lector del WebSocket, que corta la lectura del frame, no el validador que corre después.
- **Tasa** — enrutar 10 000 `unit.move` por segundo y responder `RATE_LIMITED` a partir del vigésimo ya ha ejecutado 10 000 búsquedas de unidad. El contador debe consultarse antes del enrutado.

Los 16 KiB son holgados para los cinco mensajes de v1: el mayor concebible es `session.hello` con un JWT, muy por debajo del límite. Un mensaje que se acerque siquiera al límite es, por definición, anómalo.

**Cómo se garantiza.**

- `BOUNDARY` — `conn.SetReadLimit(EO_WS_MAX_MESSAGE_BYTES)` se configura en `Server.serve` al establecerse la conexión y **antes** del handshake, de modo que la biblioteca aborta la lectura del frame al superarlo. El buffer nunca crece más allá del límite. El resultado es `MESSAGE_TOO_LARGE` y cierre `4400`.
- `BOUNDARY` — el rate limit es un *token bucket* por conexión (`newRateLimiter(RateLimitPerSec, RateLimitBurst)`) con tasa 20/s y capacidad 40, evaluado en `readPump` **inmediatamente después** de `ReadMessage` y **antes** del `json.Unmarshal`. Ése es el punto exacto en que el enunciado se cumple: sin token, el frame no llega a deserializarse.
- `BOUNDARY` — agotar el bucket **cierra la conexión**: se responde `RATE_LIMITED` y acto seguido se cierra con `4429`. No hay un modo intermedio de «descartar y seguir»; el burst de 40 es el margen que absorbe la ráfaga legítima, y superarlo se trata como abuso.
- `BOUNDARY` — el límite de **tamaño** se aplica también antes del handshake; el **token bucket no**, porque se construye en `readPump` (ver la nota de [INV-SEC-002](#inv-sec-002)). Lo que acota la fase previa es el límite de lectura, el timeout de 5 s y que el handshake lee un solo mensaje.
- `BOUNDARY` — un mensaje malformado o de tipo desconocido se responde con `INVALID_MESSAGE` y la sesión **continúa**: un JSON roto no es motivo de cierre, a diferencia del abuso de tasa.
- `DOMAIN` — la contrapresión completa el cuadro: si la cola de comandos está llena, el comando se descarta y se responde `INTERNAL_ERROR`; si la cola de salida de una sesión (`EO_WS_OUTBOUND_QUEUE_SIZE`, 256) se llena, la sesión se cierra con `4500` y el cliente reconecta con un snapshot limpio.
- Los contadores alimentan `eo_ws_messages_total{direction,type}` y `eo_protocol_errors_total{code}`, que permiten ver el rechazo en agregado.

**Cómo se verifica.**

- `TestValoresPorDefectoDelCanon` (unit, `internal/config`) — **existe y pasa**: los tres valores (`16384`, `20`, `40`) se leen de `internal/config`, sin literales duplicados (canon §17).
- `TestBurstNoPuedeSerMenorQueLaTasa` (unit, `internal/config`) — **existe y pasa**: `EO_WS_RATE_LIMIT_BURST >= EO_WS_RATE_LIMIT_PER_SECOND`, o el bucket no podría absorber ni un segundo de tasa nominal.
- `TestMensajeMalformadoNoTumbaLaSesion` y `TestTipoDeMensajeDesconocidoSeRechaza` (e2e, `internal/websocket`) — **existen y pasan**: el mensaje inválido se responde con `INVALID_MESSAGE` y la sesión sobrevive.
- Los límites de transporte del protocolo se comprueban además en `packages/protocol` (Vitest): «coinciden con los valores por defecto de la configuración `EO_WS_*`» — **existe y pasa**.
- Test previsto: `Test_INV_SEC_005_OversizedAndRateLimitedRejectedUnparsed` (integration) — con un deserializador instrumentado: un mensaje de 16385 bytes no llega a deserializarse; el mensaje 41 de una ráfaga tampoco.
- Test previsto: `Test_INV_SEC_005_BurstAllowedThenLimited` (integration) — 40 mensajes instantáneos pasan; el 41 recibe `RATE_LIMITED` y cierre `4429`.

**Violación en runtime.** Detección en el lector y en el limitador. Log de nivel `warn` para el rechazo ordinario —es comportamiento esperado ante un cliente abusivo, no un fallo del sistema; el código emite exactamente eso: «límite de tasa superado; se cierra la sesión»— y `invariant_violation` de nivel `error` con `inv_id=INV-SEC-005` únicamente si un mensaje fuera de límite **llegó** a deserializarse o a un handler, que es lo que el invariante prohíbe. Política `REJECT`; cierre `4400` o `4429` según el caso.

---

<a id="inv-sec-006"></a>
## INV-SEC-006 — Las credenciales de infraestructura no salen del servidor

| Campo | Valor |
|---|---|
| Severidad | CRITICO |
| Aplicación | BOUNDARY, TEST |
| Milestone | M0 |
| Política ante violación | FAIL_FAST |
| Cobertura | **Parcial**: `TestObligatoriasAusentes`, `TestSecretoDemasiadoCorto` y `TestSecretoDeDesarrolloEnProduccion` (`internal/config`) cubren el arranque; el escaneo de secretos y la comprobación del bundle de cliente **no existen todavía** |

**Enunciado.** `EO_POSTGRES_URL`, `EO_REDIS_URL`, `EO_AUTH_JWT_SECRET` y cualquier otro secreto de infraestructura no aparecen jamás en un mensaje al cliente, en un artefacto servido al navegador, en un log, ni en el repositorio de código en ninguna revisión de su historia.

**Razón.** Cada uno tiene una consecuencia distinta y todas son terminales:

| Secreto | Si se filtra |
|---|---|
| `EO_POSTGRES_URL` | Acceso directo a la *source of truth*: lectura y escritura de todo el estado durable |
| `EO_REDIS_URL` | Manipulación de presencia, sesiones y anti-replay de tickets ([INV-SEC-003](#inv-sec-003)) |
| `EO_AUTH_JWT_SECRET` | **Falsificación de tickets**: cualquier `sub`, cualquier `exp`. Suplantación de cualquier jugador sin límite |

El último es el peor. Hoy el emisor de tickets es el propio Game Server (`internal/httpapi` + `auth.Issuer`), así que el secreto vive en un único proceso y no toca ningún bundle. La arquitectura objetivo de [../decisions/ADR-010-authentication-game-ticket.md](../decisions/ADR-010-authentication-game-ticket.md) traslada la emisión a Next.js, y **ahí aparece una vía de fuga específica**: en Next.js, una variable de entorno con el prefijo público se **incluye en el bundle del navegador**. `EO_AUTH_JWT_SECRET` no lleva ese prefijo y no debe llevarlo nunca; lo usará exclusivamente el código de servidor de la API route de emisión. Se anota aquí por adelantado porque el momento de recordarlo es el del traslado, no el posterior.

El repositorio importa tanto como el runtime: un secreto commiteado sigue en el historial de git aunque se borre en el commit siguiente. Rotarlo es la única mitigación real.

**Cómo se garantiza.**

- `BOUNDARY` — toda la configuración pasa por `internal/config` (canon §17) y se lee de variables de entorno. `EO_POSTGRES_URL`, `EO_REDIS_URL` y `EO_AUTH_JWT_SECRET` tienen valor por defecto vacío y son obligatorias: el arranque falla si faltan. `config.Load` exige además al menos 32 caracteres de secreto y rechaza el valor `dev-only` cuando `EO_ENV=production`. Los errores de configuración se reportan **todos juntos**, no de uno en uno, para que corregirlos no sea un juego de adivinanzas.
- `BOUNDARY` — los secretos se almacenan en un tipo que redacta su valor al formatearse, de modo que un log accidental de la configuración imprime la redacción y no el valor. Esto neutraliza el modo de fuga más común: el `log` de depuración de la estructura de config.
- `BOUNDARY` — ninguno de los mensajes servidor→cliente v1 contiene configuración de infraestructura. `session.welcome` lleva la información de sesión y de mundo que el cliente necesita, no cadenas de conexión.
- `BOUNDARY` — `system.error` devuelve `{ code, message, requestId?, details? }` (canon §16) con mensajes redactados: un error de PostgreSQL nunca se propaga textualmente al cliente, porque los mensajes de error de driver incluyen a menudo el host y el usuario. Al cliente le llega `INTERNAL_ERROR`; el detalle va al log del servidor.
- `BOUNDARY` — el repositorio incluye `.env.example` con todas las variables `EO_*` y valores vacíos o de marcador, y `.gitignore` cubre los `.env` reales. El escaneo de secretos en el paso `lint` del pipeline (canon §19) es **trabajo pendiente**, no una comprobación ya activa.
- Las credenciales de desarrollo viven en `docker-compose.yml` para los servicios locales y no se reutilizan en ningún otro entorno.

**Cómo se verifica.**

- `TestObligatoriasAusentes` (unit, `internal/config`) — **existe y pasa**: sin `EO_POSTGRES_URL`, `EO_REDIS_URL` o `EO_AUTH_JWT_SECRET`, el arranque falla en lugar de usar un valor por defecto.
- `TestSecretoDemasiadoCorto` y `TestSecretoDeDesarrolloEnProduccion` (unit, `internal/config`) — **existen y pasan**: un secreto de menos de 32 caracteres, o el de desarrollo en producción, abortan el arranque.
- `TestErrorDeProtocoloLlevaCodigoEstable` (contract, `internal/protocol`) — **existe y pasa**: `system.error` viaja con un código del catálogo cerrado, no con texto de driver.
- Test previsto: `Test_INV_SEC_006_SecretsRedactedInLogs` (unit) — formatear la configuración produce una redacción; el valor no aparece en la salida.
- Test previsto: `Test_INV_SEC_006_ErrorsDoNotExposeInternals` (integration) — se fuerza un error de base de datos; el `system.error` recibido es `INTERNAL_ERROR` sin cadena de conexión ni texto del driver.
- Test previsto: `Test_INV_SEC_006_NoSecretsInRepository` (CI) — escaneo de secretos sobre el árbol de trabajo y el historial.
- Test previsto: `Test_INV_SEC_006_JwtSecretNotInClientBundle` (Vitest) — el bundle de cliente construido no contiene el secreto ni su nombre de variable con prefijo público. **Pendiente de que exista `apps/web/`**, que hoy no está en el repositorio.

**Violación en runtime.** Detección en el escaneo de CI (preventiva), en los contract tests y en la validación de configuración de arranque. Log `invariant_violation` con `inv_id=INV-SEC-006` y el nombre de la variable, **jamás su valor**. Política `FAIL_FAST`. Una fuga confirmada exige rotación inmediata del secreto afectado; corregir el código no basta, porque el valor ya está fuera.

---

<a id="inv-sec-007"></a>
## INV-SEC-007 — Un `requestId` repetido no produce un segundo efecto

| Campo | Valor |
|---|---|
| Severidad | CRITICO |
| Aplicación | BOUNDARY, TEST |
| Milestone | M3 |
| Política ante violación | REJECT (no reejecutar) |
| Cobertura | **Cubierto** por `TestComandoDuplicadoSeIgnora` (`internal/websocket`) y por `TestIdempotenciaDeduplicaComandos`, `TestIdempotenciaEstaAcotadaPorJugador` y `TestReleaseLiberaLaReserva` (`internal/persistence/redis`, integration **diseñados y no ejecutados**) |

**Enunciado.** Para un mismo `playerId`, un `requestId` ya procesado **no se reejecuta**: no se crea un segundo movimiento, no se crea una segunda unidad, no se aplica un segundo cambio de estado.

> **Alcance real hoy, y la parte que falta.** La deduplicación implementada es la de Redis: `IdempotencyStore.Claim` reserva `idem:{playerId}:{requestId}` con `SETNX` y TTL 300 s antes de ejecutar, y un duplicado **se ignora en silencio** (log `debug`, sin respuesta al cliente). Dos piezas del diseño **no están implementadas todavía** y no deben darse por hechas:
>
> 1. **La respuesta original no se reemite.** `IdempotencyStore.Complete` existe para guardarla, pero el borde no la devuelve: el segundo envío no recibe nada. El cliente que reintenta tras perder la respuesta sigue sin recibirla; lo que sí obtiene es la garantía de que no habrá un segundo efecto.
> 2. **`idempotency_keys` no se escribe.** La tabla existe en `000001_initial_schema.up.sql` con `PRIMARY KEY (player_id, request_id)`, pero **ninguna ruta de código inserta en ella**. El respaldo durable de la idempotencia es diseño pendiente, no garantía vigente. Por eso esta ficha declara `BOUNDARY, TEST` y no `DB, DOMAIN`.

**Razón.** Es lo que hace segura la reconexión. El `requestId` es un UUIDv4 obligatorio en todo comando (canon §13), y el cliente reintenta cuando pierde la conexión sin haber recibido respuesta —que es exactamente el caso en que **no sabe** si el comando se ejecutó. Sin idempotencia, la única opción segura para el cliente sería no reintentar nunca, lo que haría que cada corte de red perdiera comandos.

Los efectos de una reejecución no son simétricos:

- **`unit.move` duplicado** — el segundo cancelaría el primero con `reason = REPLACED` y crearía otro. El resultado final es parecido, pero se emitirían `unit.movement.cancelled` y `unit.movement.started` espurios, y el `start_time_ms` se recalcularía, retrasando la llegada. Molesto y visible.
- **Creación de jugador duplicada** — dos ciudades y seis aldeanos ([INV-PLAYER-003](player.md#inv-player-003)). Ventaja material permanente obtenida por un corte de red.

**Cómo se garantiza.**

- `BOUNDARY` — el `requestId` es obligatorio en todo comando y su formato UUID se valida en el esquema; un `requestId` ausente devuelve `INVALID_MESSAGE` antes de tocar nada. Sin él no hay clave de idempotencia posible.
- `BOUNDARY` — `Server.claimRequest` consulta Redis **antes** de encolar el comando: `SETNX idem:{playerId}:{requestId} "pending" EX 300`. Si la reserva se gana, el comando se ejecuta; si no, se descarta. La reserva ocurre **antes** de ejecutar, no después, que es lo que resuelve la carrera de dos reintentos simultáneos: sólo uno gana el `SETNX`.
- `BOUNDARY` — la clave incluye el `playerId`, así que dos jugadores pueden usar el mismo `requestId` sin interferir; y el `playerId` proviene de la sesión ([INV-PLAYER-004](player.md#inv-player-004)), no del payload, de modo que nadie puede envenenar la caché de idempotencia de otro.
- `BOUNDARY` — `IdempotencyStore.Release` borra la reserva de un comando que falló de forma **transitoria**, para que el cliente pueda reintentarlo con el mismo `requestId`. Sin esa liberación, un fallo transitorio dejaría el `requestId` quemado durante 300 s.
- `BOUNDARY` — si la clave expiró entre el `SETNX` y la lectura, se trata como primera vez. Es la decisión conservadora en la dirección de dejar jugar.

> **Degradación abierta si Redis no responde.** Si `Claim` devuelve error, `claimRequest` registra un `warn` («no se pudo verificar la idempotencia; se ejecuta igualmente») y **ejecuta el comando**. Es una decisión explícita y documentada: se prefiere jugar a bloquear al jugador. La consecuencia honesta es que, con Redis caído, un reintento **sí** puede producir un segundo efecto. Nótese el contraste deliberado con [INV-SEC-003](#inv-sec-003), que falla **cerrado**: allí lo que está en juego es la suplantación de una cuenta; aquí, un movimiento repetido. Las dos decisiones son opuestas porque los costes lo son.

**Cómo se verifica.**

- `TestComandoDuplicadoSeIgnora` (e2e, `internal/websocket`) — **existe y pasa**: el mismo `unit.move` enviado dos veces produce un solo efecto.
- `TestIdempotenciaDeduplicaComandos` (integration, `internal/persistence/redis`) — **diseñado, aún no ejecutado**: la segunda reserva del mismo `requestId` no se concede.
- `TestIdempotenciaEstaAcotadaPorJugador` (integration, `internal/persistence/redis`) — **diseñado, aún no ejecutado**: dos jugadores con el mismo `requestId` no colisionan.
- `TestReleaseLiberaLaReserva` (integration, `internal/persistence/redis`) — **diseñado, aún no ejecutado**: liberar permite reintentar con el mismo `requestId`.
- `packages/protocol` (Vitest) — **existe y pasa**: «exige requestId con formato UUID».
- Test previsto: `Test_INV_SEC_007_IdempotencyAcrossReconnect` (integration) — comando enviado, socket cortado antes de la respuesta, reconexión y reenvío con el mismo `requestId`: un solo efecto.
- Test previsto: `Test_INV_SEC_007_DurableCommandsRecordedInPostgres` (integration) — **para cuando se implemente**: todo comando durable deja su fila en `idempotency_keys`.

**Violación en runtime.** Detección en una aserción posterior que compara el recuento de efectos con el número de `requestId` distintos. Log `invariant_violation` con `inv_id=INV-SEC-007`, `player_id` y `request_id`. Política `REJECT`: el comando duplicado no se ejecuta. Un segundo efecto **consumado** no se revierte automáticamente —revertir un efecto de gameplay es en sí un efecto de gameplay—: se registra en `world_events` y se escala.

---

## Modelo de amenazas del MVP

Qué cubre este catálogo y qué queda explícitamente fuera.

| Amenaza | Invariante que la cubre |
|---|---|
| Suplantación de jugador | [INV-PLAYER-004](player.md#inv-player-004), [INV-SEC-002](#inv-sec-002), [INV-SEC-003](#inv-sec-003) |
| Control de unidades ajenas | [INV-PLAYER-001](player.md#inv-player-001), [INV-SEC-004](#inv-sec-004) |
| Falsificación de posición, velocidad o path | [INV-SEC-001](#inv-sec-001), [INV-MOVE-004](movement.md#inv-move-004), [INV-MOVE-006](movement.md#inv-move-006) |
| Replay de ticket | [INV-SEC-003](#inv-sec-003) |
| Duplicación de efectos por reintento | [INV-SEC-007](#inv-sec-007) |
| Agotamiento de recursos por mensajes | [INV-SEC-005](#inv-sec-005), [INV-SEC-002](#inv-sec-002) (timeout de handshake) |
| Fuga de credenciales | [INV-SEC-006](#inv-sec-006) |
| Evasión de la protección por desconexión | [INV-CITY-005](city.md#inv-city-005), [INV-CITY-006](city.md#inv-city-006) |

**Fuera de MVP**, sin invariante asignado y **TBD**: cifrado en reposo de la base de datos, rotación automatizada de secretos, auditoría de acciones administrativas, detección de bots y automatización de cliente, protección anti-DDoS a nivel de red, moderación y anti-abuso de chat (no hay chat en MVP, canon §21), rate limiting agregado por jugador a través de múltiples conexiones simultáneas, sub-límite de tasa dedicado para `session.view`, token bucket durante el handshake, y respaldo durable de la idempotencia en `idempotency_keys`.

## Trazabilidad

| Invariante | Componente propietario | Relacionado con |
|---|---|---|
| INV-SEC-001 | `packages/protocol`, `internal/websocket` | [INV-MOVE-006](movement.md#inv-move-006), [INV-CITY-005](city.md#inv-city-005) |
| INV-SEC-002 | `internal/websocket`, `internal/auth` | [INV-PLAYER-004](player.md#inv-player-004) |
| INV-SEC-003 | `internal/auth`, `internal/persistence/redis` | [INV-PERSIST-003](persistence.md#inv-persist-003) |
| INV-SEC-004 | `internal/websocket`, `internal/domain` | [INV-PLAYER-001](player.md#inv-player-001) |
| INV-SEC-005 | `internal/websocket` | [../specs/websocket-protocol.md](../specs/websocket-protocol.md) |
| INV-SEC-006 | `internal/config`, `internal/httpapi` | [../operations/monitoring.md](../operations/monitoring.md) |
| INV-SEC-007 | `internal/websocket`, `internal/persistence/redis` | [INV-PERSIST-001](persistence.md#inv-persist-001), [INV-PLAYER-003](player.md#inv-player-003) |
