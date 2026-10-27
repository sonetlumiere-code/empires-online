# Invariantes de Seguridad (INV-SEC-xxx)

Propiedades de autoridad del servidor, autenticación, autorización, límites de abuso, gestión de secretos e idempotencia.

Formato y severidades: [README.md](README.md). Autenticación: canon §14. Protocolo y límites: canon §13.

---

## Contexto

El principio no negociable del canon §1.1 es la frase que gobierna este archivo entero:

> **Client sends intent, server determines truth.**

El cliente **nunca** aporta estado autoritativo: ni posición final, ni HP, ni recursos, ni resultados de combate, ni ownership, ni cooldowns, ni ETA, ni paths. Los invariantes de este documento son las condiciones concretas bajo las cuales esa frase es verdadera en la implementación y no solo en la intención.

### Superficie de ataque del MVP

Solo existe una superficie: la conexión WebSocket. Siete tipos de mensaje cliente→servidor en v1 (los dos de guarnición, desde [ADR-017](../decisions/ADR-017-garrison-commands.md)):

| Tipo | Payload | Requiere sesión |
|---|---|---|
| `session.hello` | `{ ticket }` | No — **es** el que la establece |
| `session.ping` | — | Sí |
| `session.view` | centro de vista | Sí |
| `unit.move` | `{ unitId, target:{x,y} }` | Sí |
| `unit.cancel_move` | `{ unitId }` | Sí |
| `unit.garrison` | `{ unitId, cityId }` | Sí |
| `unit.ungarrison` | `{ unitId }` | Sí |

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

> **Un único token bucket.** En MVP existe **un solo** limitador de tasa por conexión: 20 msg/s con burst 40, aplicado por igual a los siete tipos de mensaje. `session.view` **no** tiene un sub-límite dedicado; el canon §13 dice que `session.view` actualiza el centro «con rate limit», refiriéndose a ese límite global, y no define ninguno adicional. Un sub-límite propio para `session.view` —justificable, porque recalcular conjuntos de interés es más caro que responder un ping— es **TBD (fuera de MVP)**.
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
| Cobertura | **Cubierto** por los tests de `packages/protocol` (Vitest), por `TestElDecodificadorEsTanEstrictoComoElEsquema`, `TestElComandoDeMovimientoNoAdmiteRuta` y `TestEntityUpdateEsUnDeltaDeVerdad` (`internal/protocol`) y por `TestPayloadConCamposExtraNoAportaEstado` (`internal/websocket`) |

**Enunciado.** Ningún valor procedente de un mensaje del cliente se escribe directamente en el estado autoritativo. Los payloads solo pueden expresar **intención** (qué unidad, qué destino, qué centro de vista) y todo valor derivado —posición, path, tiempos, ETA, resultado— lo calcula el servidor.

**Razón.** Es el invariante del que dependen todos los demás. Si el cliente pudiera escribir su propia posición, sobraría el pathfinding y sobraría el modelo de bloqueo: bastaría enviar «estoy en el centro de la ciudad enemiga». Si pudiera escribir el `hp`, sobraría el combate. Si pudiera escribir los tiempos, sobraría la velocidad de las unidades.

La forma en que este invariante se rompe en la práctica no es un descuido grosero, sino una comodidad: añadir al payload de `unit.move` un campo `estimatedArrival` «para que el cliente no espere», o un `path` «que el cliente ya calculó para la previsualización», y usarlo en el servidor. Por eso el invariante se garantiza estructuralmente en el esquema del protocolo y no con disciplina.

**Cómo se garantiza.**

- `TYPE` — los esquemas Zod de `packages/protocol/src/v1/` son la fuente de verdad del protocolo (canon §13). Los payloads cliente→servidor **no contienen** ningún campo de estado derivado: `unit.move` es exactamente `{ unitId, target:{x,y} }` y nada más. El canon lo dice explícitamente en §7: el cliente «NUNCA envía secuencias de posiciones».
- `TYPE` — los esquemas **cliente→servidor** son estrictos (`additionalProperties: false`, S-7 de [../specs/websocket-protocol.md](../specs/websocket-protocol.md)): un campo desconocido hace fallar la validación en lugar de ignorarse silenciosamente. Los esquemas **servidor→cliente NO son estrictos**, deliberadamente, para poder añadir campos opcionales sin romper clientes antiguos: estricto donde entra dato ajeno, tolerante donde sale dato propio.
- `BOUNDARY` — el servidor Go decodifica todo mensaje entrante, envelope y payload, con `protocol.DecodeStrict`: `DisallowUnknownFields` a cualquier profundidad y nada tras el valor JSON. Un cliente que cuela `path` o `hp` en `unit.move` recibe `INVALID_MESSAGE` con su `requestId` y el comando no se ejecuta; la sesión sigue. Hasta `EO-118` el servidor usaba `json.Unmarshal`, que **descartaba en silencio** los campos de más y ejecutaba el comando: el invariante se cumplía porque ningún struct tenía dónde guardarlos, pero un refactor que añadiera el campo los habría aceptado sin que nadie lo decidiera. El cliente web envía sólo `{x, y}` aunque reciba un tile con más propiedades.
- `TEST` — la equivalencia con el JSON Schema embebido (`internal/protocol/schema/v1/`, espejo de `pnpm run protocol:build`) la comprueba un contract test: el esquema declara `additionalProperties: false` en todo objeto cliente→servidor, y el decodificador rechaza lo que el esquema rechaza.
- `DOMAIN` — la posición durante un movimiento es **derivada** (`Movement.PositionAt`, [INV-MOVE-006](movement.md#inv-move-006), [INV-MOVE-009](movement.md#inv-move-009)) y la interpolación sub-tile es exclusivamente visual y del cliente (canon §7). No hay ninguna ruta por la que el cliente informe su posición ([INV-MOVE-010](movement.md#inv-move-010)).
- `DOMAIN` — el `presence_state` de la ciudad lo decide el servidor y el cliente solo lo observa por `city.update` (canon §9); no hay mensaje cliente→servidor que lo altere ([INV-CITY-005](city.md#inv-city-005)).

**Cómo se verifica.**

- `packages/protocol` (Vitest) — «rechaza un payload que intente aportar estado autoritativo desconocido», «unit.move no admite que el cliente envíe una ruta» y «no ha derivado respecto de los esquemas Zod (misma comprobación que la CI)».
- `TestElDecodificadorEsTanEstrictoComoElEsquema` (contract, `internal/protocol`) — las dos mitades de S-7: todo objeto cliente→servidor del esquema es estricto, y en los siete mensajes una propiedad de más en el envelope, en el payload o en un objeto anidado se rechaza.
- `TestElComandoDeMovimientoNoAdmiteRuta` (contract) — `DecodeStrict` rechaza una ruta en `unit.move`.
- `TestEntityUpdateEsUnDeltaDeVerdad` (contract) — el servidor emite deltas parciales calculados por él, no ecos del cliente.
- `TestPayloadConCamposExtraNoAportaEstado` (e2e, `internal/websocket`) — una ruta y un HP colados, una coordenada de más y un campo de más en el envelope reciben `INVALID_MESSAGE`; el comando limpio posterior es el primero que se acepta, y su ruta la calcula el servidor. Verificado por mutación: sin `DisallowUnknownFields` falla.
- `TestEjemploNumericoCanonico` (unit, `internal/domain/movement`) — el tiempo de llegada depende únicamente del terreno y del `baseMsPerTile` del catálogo, no de nada enviado por el cliente.

**Violación en runtime.** El rechazo de un campo desconocido es la regla de S-7, no una violación: `INVALID_MESSAGE`, contado en `eo_protocol_errors_total{code}`, sin log de invariante. La defensa que queda fuera del código es la revisión: un PR que añada un campo de estado a un payload cliente→servidor debe bloquearse aunque los tests pasen, porque el esquema y el decodificador lo aceptarían juntos.

---

<a id="inv-sec-002"></a>
## INV-SEC-002 — Ninguna conexión ejecuta comandos antes de autenticarse

| Campo | Valor |
|---|---|
| Severidad | CRITICO |
| Aplicación | BOUNDARY, TEST |
| Milestone | M3 |
| Política ante violación | FAIL_FAST (cierre de la conexión) |
| Cobertura | **Cubierto** por `TestNoSeAceptanComandosSinAutenticar`, `TestSinHandshakeEnPlazoSeCierraCon4408`, `TestTicketInvalidoCierraLaConexion`, `TestTicketNoSePuedeReutilizarEnOtraConexion` y `TestUnOrigenNoListadoSeRechazaCon403AntesDelHandshake` (`internal/websocket`), por `TestOrigenesPermitidosDelWebSocket` (`internal/config`) y por `TestPreAuthSoloAdmiteElHandshake` (`internal/protocol`) |

**Enunciado.** Una conexión WebSocket sólo acepta `session.hello` hasta que el ticket ha sido verificado y la sesión creada. Cualquier otro tipo de mensaje recibido antes cierra la conexión con `4401` sin procesarse; si no llega un frame legible dentro del plazo de handshake (5 s), la conexión se cierra con `4408`.

**Razón.** Sin sesión no hay `playerId`, y sin `playerId` no hay ownership que comprobar. Un `unit.move` procesado antes del handshake tendría que compararse contra una identidad vacía: en Go, el valor cero de `uuid`. Según cómo esté escrita la comparación, eso puede resultar verdadero para unidades cuyo owner también estuviera vacío, o simplemente entrar en una rama no prevista. En cualquier caso, es una ejecución de comando sin autorización.

El timeout de 5 segundos cubre un segundo problema, de disponibilidad: conexiones abiertas que nunca se autentican consumen file descriptors, memoria y una entrada en `eo_connected_websockets`. Sin timeout, abrir sockets y no decir nada es un ataque de agotamiento de recursos trivial de montar.

**Cómo se garantiza.**

- `BOUNDARY` — la sesión no existe hasta que el handshake termina. `Server.handshake` lee **un único** mensaje: si no es un `session.hello` con un ticket que verifica, la conexión se cierra sin volver a leer, y `readPump` —el único bucle que enruta comandos— sólo arranca con la sesión ya creada. No hay un estado «conectado sin autenticar» en el que un comando pudiera enrutarse.
- `BOUNDARY` — el plazo de handshake (`HandshakeTimeout`, 5 s en producción) es el `SetReadDeadline` de esa primera lectura. Al vencer, cierre `4408`.
- `BOUNDARY` — `conn.SetReadLimit(EO_WS_MAX_MESSAGE_BYTES)` se aplica **antes** del handshake, en `Server.serve`, de modo que ni siquiera un `session.hello` puede desbordar memoria: un frame mayor lo corta el transporte con `1009` ([INV-SEC-005](#inv-sec-005)).
- `BOUNDARY` — la verificación del ticket es completa antes de crear la sesión (canon §14): firma HS256 con `EO_AUTH_JWT_SECRET`, `exp`, `aud = "game-server"` y consumo del `jti` ([INV-SEC-003](#inv-sec-003)). Un fallo en cualquier paso cierra con `4401` sin crear sesión, y el mismo código para todos: distinguirlos ayudaría a afinar un ataque.
- `BOUNDARY` — **antes incluso del socket**, el upgrade comprueba la cabecera `Origin` contra `EO_WS_ALLOWED_ORIGINS` ([ADR-021](../decisions/ADR-021-game-server-perimeter.md)): un origen no listado recibe `403` y no hay handshake que proteger. Un navegador envía `Origin` siempre y no deja al script cambiarla, así que es lo que impide que otra página abierta en el mismo navegador abra una sesión con el ticket que la web del juego acaba de pedir (*cross-site WebSocket hijacking*). Una petición sin `Origin` no es un navegador y se admite. En producción la lista es obligatoria; hasta la auditoría del 2026-10-26 nunca se rellenaba y la comprobación estaba apagada.
- `DOMAIN` — el `playerId` de la sesión se establece una sola vez y es inmutable ([INV-PLAYER-004](player.md#inv-player-004)).

> **Qué protege realmente la fase previa al handshake.** El límite de **tamaño** sí se aplica antes (`SetReadLimit` en `Server.serve`), pero el **token bucket NO**: `newRateLimiter` se construye en `readPump`, que sólo se ejecuta con la sesión ya establecida. Lo que acota el coste de una conexión no autenticada es la combinación de tres cosas: el límite de lectura, el plazo de handshake y que el handshake acepta **un solo mensaje**. Una conexión no autenticada no puede, por tanto, enviar una ráfaga: no hay un bucle que la lea. Extender el token bucket a la fase de handshake, para acotar también el coste de conexiones que abren y cierran en serie, es **TBD (fuera de MVP)**.

**Cómo se verifica.**

- `TestNoSeAceptanComandosSinAutenticar` (e2e, `internal/websocket`) — se abre el socket y se envía `unit.move` como primer mensaje; no se ejecuta y la conexión se cierra con `4401`.
- `TestSinHandshakeEnPlazoSeCierraCon4408` (e2e) — un socket que no envía nada se cierra con `4408` al vencer el plazo.
- `TestTicketInvalidoCierraLaConexion` (e2e) — los tickets inválidos cierran con `4401`.
- `TestTicketNoSePuedeReutilizarEnOtraConexion` (e2e) — el segundo canje no establece sesión.
- `TestPreAuthSoloAdmiteElHandshake` (contract, `internal/protocol`) — de los siete tipos cliente→servidor, sólo `session.hello` es pre-auth.
- `TestCodigosDeCierreSonDelRangoPrivado` (contract) — `4400`, `4401`, `4403`, `4408`, `4429` y `4500` están en el rango privado.
- `TestUnOrigenNoListadoSeRechazaCon403AntesDelHandshake`, `TestUnOrigenListadoAbreSesion`, `TestSinOrigenSeAdmiteAunqueHayaLista` y `TestConLaListaVaciaSeAdmiteCualquierOrigen` (e2e, `internal/websocket`) — con lista, un `Origin` ajeno recibe `403` sin socket y uno listado abre sesión; sin `Origin`, o sin lista, se admite. Verificado por mutación: la comprobación que siempre devuelve `true` —la que sobrevivía a la suite antes— lo hace fallar.
- `TestOrigenesPermitidosDelWebSocket` (unit, `internal/config`) — la lista se parsea y normaliza, una entrada que no es `scheme://host[:port]` aborta el arranque y en producción la variable es obligatoria.

**Violación en runtime.** El rechazo es la regla, no una violación: `Server.serve` registra «handshake rechazado» a nivel `info`, con la causa y el código de cierre, y cierra. No hay un camino de código en el que un comando llegue a enrutarse sin sesión, y por tanto no hay una detección que escribir.

---

<a id="inv-sec-003"></a>
## INV-SEC-003 — Un ticket solo puede canjearse una vez

| Campo | Valor |
|---|---|
| Severidad | CRITICO |
| Aplicación | BOUNDARY, TEST |
| Milestone | M3 |
| Política ante violación | FAIL_FAST (cierre `4401`) |
| Cobertura | **Cubierto** por `TestTicketNoSePuedeReutilizar`, `TestCadaTicketTieneUnJtiDistinto`, `TestFalloDelRegistroDeTicketsFallaCerrado`, `TestTicketCaducado` y `TestElVerificadorToleraElDesfaseDeReloj` (`internal/auth`), `TestTicketSoloSeConsumeUnaVez` (`internal/persistence/redis`, integration, y `internal/persistence/memory`), `TestConsumoConcurrenteDeTicketSoloUnaVez` (`internal/persistence/memory`), `TestNingunaClaveDelServidorQuedaSinCaducidad` (integration) y `TestTicketNoSePuedeReutilizarEnOtraConexion` (`internal/websocket`) |

**Enunciado.** El `jti` de todo game ticket verificado se consume atómicamente en Redis bajo la clave `ticket:jti:{jti}` con TTL 120 s; un segundo intento de canjear el mismo `jti` se rechaza y cierra la conexión con `4401`.

**Razón.** Es la defensa contra **replay**. El ticket es un JWT HS256 con TTL de 60 s (`TOKEN_TTL_SECONDS`, constante de `apps/web/identity/tokens.mjs`: desde [ADR-018](../decisions/ADR-018-identity-store-and-ticket-issuer.md) lo emite `apps/web`, no el Game Server) que viaja del navegador al Game Server. Sin consumo del `jti`, cualquiera que capture el ticket dentro de esa ventana —un log mal configurado, una extensión del navegador, un proxy corporativo, el propio jugador compartiéndolo— puede abrir sesiones como ese jugador durante 60 segundos. El TTL corto reduce la ventana, pero no la cierra: solo el consumo único la cierra.

El TTL de 120 s de la clave es deliberadamente el doble del TTL del ticket: garantiza que el registro anti-replay sobrevive a cualquier ticket que pudiera seguir siendo válido. La vida efectiva máxima de un ticket es el TTL de 60 s **más los 30 s de margen de desfase de reloj** que el verificador concede a `exp` (`auth.ClockSkew`, [ADR-021](../decisions/ADR-021-game-server-perimeter.md)): 90 s, por debajo de los 120 s de la clave. Que el `jti` desaparezca después no importa: para entonces el ticket ya expiró, margen incluido, y `exp` lo rechaza por sí solo.

**Cómo se garantiza.**

- `BOUNDARY` — el consumo es `SETNX ticket:jti:{jti} "1" EX 120`, una operación **atómica** de Redis en una sola llamada (`TicketStore.Consume`). No es un `EXISTS` seguido de un `SET`: dos conexiones simultáneas con el mismo ticket entrarían ambas por esa ventana.
- `BOUNDARY` — el orden de verificación es: firma HS256 → `exp` → `aud = "game-server"` (`auth.Audience`) → presencia del `jti` → consumo del `jti`. El consumo va al final, para no gastar entradas de Redis con tickets que ya son inválidos por otra razón; y va **antes** de crear la sesión, para que no exista sesión sin ticket consumido.
- `BOUNDARY` — `iat` y `exp` se evalúan con **30 s de margen** en ambos sentidos (`auth.ClockSkew`, `jwt.WithLeeway`, [ADR-021](../decisions/ADR-021-game-server-perimeter.md)): el emisor corre en otra máquina y trunca `iat` a segundos, así que sin margen bastaba con que su reloj fuese un segundo por delante para que todo ticket llegase «emitido en el futuro» y se rechazase con `4401`, indistinguible de un ticket falso. Es una constante y no una variable `EO_*`: forma parte del contrato del ticket, como su TTL, y acota la vida efectiva (90 s) por debajo de la clave anti-replay. El mismo margen vale para el token de fundación ([INV-SEC-008](#inv-sec-008)).
- `BOUNDARY` — un ticket **sin `jti`** se rechaza en la verificación, antes del consumo: sin `jti` no se puede impedir el replay, así que el ticket no sirve. Es una decisión explícita del verificador, no un efecto colateral.
- `BOUNDARY` — el fallo del consumo (clave ya existente) cierra con `4401`. No se distingue en la respuesta al cliente entre «ticket ya usado» y «ticket inválido»: ambos son el mismo cierre `4401`, sin mensaje previo ([ADR-019](../decisions/ADR-019-handshake-failure-close-code.md)).
- `DOMAIN` — la indisponibilidad de Redis en el momento del consumo se trata como **fallo cerrado**: si no se puede garantizar la unicidad, no se autentica. Se cierra con `4401`, el mismo código que cualquier otro fallo de verificación ([../architecture/networking.md](../architecture/networking.md) §3.2), y el cliente reintenta con un ticket nuevo y backoff. Autenticar «por si acaso» convertiría una caída de Redis en una ventana de replay abierta.
- Nunca se registra el ticket completo en logs: el rechazo registra la causa, que como mucho nombra el `jti`, ya consumido e inútil.

**Cómo se verifica.**

- `TestTicketSoloSeConsumeUnaVez` (integration, `internal/persistence/redis`; y su gemelo en `internal/persistence/memory`) — el segundo consumo del mismo `jti` devuelve falso. `TestConsumoConcurrenteDeTicketSoloUnaVez` (unit, `internal/persistence/memory`) — con consumos simultáneos, sólo uno gana.
- `TestTicketNoSePuedeReutilizar` (unit, `internal/auth`) — el ticket ya canjeado no autentica.
- `TestTicketCaducado` y `TestElVerificadorToleraElDesfaseDeReloj` (unit, `internal/auth`) — un ticket vale hasta 90 s después de emitido y no a los 91; `iat` 20 s en el futuro se acepta y 60 s se rechaza; `exp` vencido hace 20 s se acepta y hace 60 s se rechaza; el token de fundación tiene el mismo margen. `TestLosTokensDeAppsWebSeVerificanEnGo` lo fija sobre los tokens reales de TypeScript.
- `TestCadaTicketTieneUnJtiDistinto` (unit, `internal/auth`) — el emisor no repite `jti`, que es lo que hace útil el registro anti-replay.
- `TestFalloDelRegistroDeTicketsFallaCerrado` (unit, `internal/auth`) — si el registro no responde, **no se autentica**. Es el fail-closed del enunciado.
- `TestTicketNoSePuedeReutilizarEnOtraConexion` (e2e, `internal/websocket`) — el mismo ticket en dos conexiones sólo crea una sesión.
- `TestNingunaClaveDelServidorQuedaSinCaducidad` (integration, `internal/persistence/redis`) — tras un canje, `ticket:jti:{jti}` existe y caduca; `TestUnAlmacenSinTTLNoSeConstruye` impide construir el almacén con TTL 0, que en go-redis sería «para siempre».

**Violación en runtime.** Un segundo canje es la regla funcionando, no una violación: el handshake registra «handshake rechazado» a nivel `info` con la causa (`auth.ErrReplayedTicket`) y cierra con `4401`. Es indistinguible de un intento de replay, y separarlo como evento de seguridad propio —con su métrica— es `TBD (fuera de MVP)`.

---

<a id="inv-sec-004"></a>
## INV-SEC-004 — Todo comando valida ownership antes de ejecutarse

| Campo | Valor |
|---|---|
| Severidad | CRITICO |
| Aplicación | DOMAIN, TEST |
| Milestone | M4 |
| Política ante violación | REJECT |
| Cobertura | **Cubierto** por `TestRechazosDeMovimiento` (`internal/game/simulation`), `TestCatalogoDeComandosEsCerrado` (`internal/protocol`) y `TestNoSePuedeComandarUnaUnidadAjena` (`internal/websocket`) |

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
- `DOMAIN` — las mutaciones ocurren todas al final del pipeline, en el mismo paso del loop: registrar el movimiento en RAM y encolar su escritura durable, que es una transacción. Ninguna etapa anterior escribe ni encola nada. `unit.cancel_move` sigue el mismo orden: existencia → `EnsureOwnedBy` → cancelación.
- `DOMAIN` — códigos estables (canon §16): `UNIT_NOT_FOUND` y `UNIT_NOT_OWNED`. `FORBIDDEN` está reservado y ninguna ruta lo emite hoy.

**Cómo se verifica.**

- `TestRechazosDeMovimiento` (simulation, `internal/game/simulation`) — los siete rechazos previos al pathfinding, cada uno con su código y sin ningún movimiento producido; el caso «unidad ajena y muerta» responde `UNIT_NOT_OWNED`, no `UNIT_DEAD`. Con un `Pathfinder` instrumentado comprueba además que **ninguno** llega a invocarlo. Verificado por mutación: una búsqueda de ruta al principio del pipeline lo hace fallar.
- `TestNoSePuedeComandarUnaUnidadAjena` (e2e, `internal/websocket`) — la unidad ajena se rechaza con `UNIT_NOT_OWNED` de extremo a extremo.
- `TestCatalogoDeComandosEsCerrado` (contract, `internal/protocol`) — no hay comandos fuera del catálogo que pudieran saltarse el pipeline.

**Violación en runtime.** El orden es estructural —una secuencia de `return` tempranos en una sola función— y no hay una aserción en runtime que lo vigile: lo vigilan el test y la revisión. El rechazo es `REJECT` con `UNIT_NOT_OWNED`. Una mutación consumada sobre una entidad ajena sería un incidente de seguridad y requeriría revisión manual del alcance.

---

<a id="inv-sec-005"></a>
## INV-SEC-005 — Los mensajes fuera de límite se rechazan sin procesarse

| Campo | Valor |
|---|---|
| Severidad | ALTO |
| Aplicación | BOUNDARY, TEST |
| Milestone | M3 |
| Política ante violación | REJECT + cierre (`1009` o `4429`) |
| Cobertura | **Cubierto** por `TestUnMensajeDemasiadoGrandeCierraCon1009`, `TestUnaRafagaPorEncimaDelBurstCierraCon4429`, `TestConLaColaLlenaElComandoRespondeYSePuedeReintentar`, `TestMensajeMalformadoNoTumbaLaSesion` y `TestTipoDeMensajeDesconocidoSeRechaza` (`internal/websocket`), y por `TestBurstNoPuedeSerMenorQueLaTasa` y `TestValoresPorDefectoDelCanon` (`internal/config`) |

**Enunciado.** Un mensaje que supera `EO_WS_MAX_MESSAGE_BYTES = 16384` bytes se rechaza **sin deserializarse**; una conexión que supera `EO_WS_RATE_LIMIT_PER_SECOND = 20` mensajes por segundo con burst `EO_WS_RATE_LIMIT_BURST = 40` ve sus mensajes excedentes rechazados **sin enrutarse a ningún handler**.

**Razón.** La palabra clave del enunciado es «sin». No basta con responder un error: el rechazo debe ocurrir **antes** del trabajo que el límite pretende evitar, o el límite no protege de nada.

- **Tamaño** — deserializar un JSON de 50 MiB para después responder `MESSAGE_TOO_LARGE` ya ha asignado 50 MiB. Repetido en paralelo desde varias conexiones, es un agotamiento de memoria. El límite debe aplicarlo el lector del WebSocket, que corta la lectura del frame, no el validador que corre después.
- **Tasa** — enrutar 10 000 `unit.move` por segundo y responder `RATE_LIMITED` a partir del vigésimo ya ha ejecutado 10 000 búsquedas de unidad. El contador debe consultarse antes del enrutado.

Los 16 KiB son holgados para los siete mensajes de v1: el mayor concebible es `session.hello` con un JWT, muy por debajo del límite. Un mensaje que se acerque siquiera al límite es, por definición, anómalo.

**Cómo se garantiza.**

- `BOUNDARY` — `conn.SetReadLimit(EO_WS_MAX_MESSAGE_BYTES)` se configura en `Server.serve` al establecerse la conexión y **antes** del handshake. gorilla compara la longitud que declara la cabecera del frame con el límite **antes de leer su contenido**: un frame mayor no se lee, el transporte envía el cierre estándar `1009` (*message too big*) y la lectura devuelve `ErrReadLimit`. No hay `system.error`: el código de aplicación `MESSAGE_TOO_LARGE` está reservado ([../specs/websocket-protocol.md](../specs/websocket-protocol.md) §6.1). El servidor registra la causa verdadera; hasta `EO-118` el handshake la confundía con un plazo vencido y la sesión no la registraba.
- `BOUNDARY` — el rate limit es un *token bucket* por conexión (`newRateLimiter(RateLimitPerSec, RateLimitBurst)`) con tasa 20/s y capacidad 40, evaluado en `readPump` **inmediatamente después** de `ReadMessage` y **antes** de decodificar. Ése es el punto exacto en que el enunciado se cumple: sin ficha, el frame no llega a deserializarse.
- `BOUNDARY` — agotar el bucket **cierra la conexión**: se envía `system.error RATE_LIMITED` y, después, el cierre `4429`. No hay un modo intermedio de «descartar y seguir»; el burst de 40 es el margen que absorbe la ráfaga legítima, y superarlo se trata como abuso. Que el `RATE_LIMITED` llegue antes del cierre exige dos cosas que hasta `EO-118` no se hacían: cerrar **después** de vaciar la cola de salida (`Session.CloseAfterFlush`), porque el cierre directo adelantaba al mensaje encolado; y no soltar el socket con datos del cliente sin leer (`drainAndClose`), porque el sistema responde entonces con un RST y el cliente pierde hasta el frame de cierre. Antes, el cliente que inundaba veía «conexión reiniciada» y ningún código.
- `BOUNDARY` — el límite de **tamaño** se aplica también antes del handshake; el **token bucket no**, porque se construye en `readPump` (ver la nota de [INV-SEC-002](#inv-sec-002)). Lo que acota la fase previa es el límite de lectura, el plazo de handshake y que el handshake lee un solo mensaje.
- `BOUNDARY` — un mensaje malformado, de tipo desconocido o con propiedades fuera del contrato se responde con `INVALID_MESSAGE` y la sesión **continúa**: un JSON roto no es motivo de cierre, a diferencia del abuso de tasa.
- `DOMAIN` — la contrapresión completa el cuadro: si la cola de comandos del loop está llena, el comando no se ejecuta, se responde `INTERNAL_ERROR` con su `requestId` y se libera su reserva de idempotencia (I-10). Hasta `EO-118` el comando se descartaba sin responder y el reintento con el mismo `requestId` se ignoraba como duplicado. Si la cola de salida de una sesión (`EO_WS_OUTBOUND_QUEUE_SIZE`, 256) se llena, la sesión se cierra con `4500` y el cliente reconecta con un snapshot limpio.
- Los contadores alimentan `eo_ws_messages_total{direction,type}` y `eo_protocol_errors_total{code}`, que permiten ver el rechazo en agregado.

**Cómo se verifica.**

- `TestUnMensajeDemasiadoGrandeCierraCon1009` (e2e, `internal/websocket`) — un frame por encima del límite cierra con `1009`, antes y después del handshake.
- `TestUnaRafagaPorEncimaDelBurstCierraCon4429` (e2e) — 60 mensajes en ráfaga: llega `RATE_LIMITED` y después el cierre `4429`. Estable en diez repeticiones; sin `CloseAfterFlush` el `RATE_LIMITED` no llega, y sin drenar el cierre llega como RST.
- `TestConLaColaLlenaElComandoRespondeYSePuedeReintentar` (e2e) — con la cola del loop llena, `INTERNAL_ERROR` con el `requestId`, y el reintento con el mismo `requestId` se ejecuta. Verificado por mutación.
- `TestMensajeMalformadoNoTumbaLaSesion` y `TestTipoDeMensajeDesconocidoSeRechaza` (e2e) — el mensaje inválido se responde con `INVALID_MESSAGE` y la sesión sobrevive.
- `TestValoresPorDefectoDelCanon` y `TestBurstNoPuedeSerMenorQueLaTasa` (unit, `internal/config`) — los tres valores (`16384`, `20`, `40`) salen de `internal/config`, y el burst no puede ser menor que la tasa.
- `packages/protocol` (Vitest): «coinciden con los valores por defecto de la configuración `EO_WS_*`».

**Violación en runtime.** El rechazo es comportamiento esperado ante un cliente abusivo, no un fallo del sistema: `warn` para la tasa («límite de tasa superado; se cierra la sesión») e `info` para el tamaño. El invariante sólo se rompería si un mensaje fuera de límite **llegara** a deserializarse o a un handler, y ningún camino del código lo permite: el límite de tamaño lo aplica el transporte y el de tasa va antes de decodificar.

---

<a id="inv-sec-006"></a>
## INV-SEC-006 — Las credenciales de infraestructura no salen del servidor

| Campo | Valor |
|---|---|
| Severidad | CRITICO |
| Aplicación | BOUNDARY, TEST |
| Milestone | M0 |
| Política ante violación | FAIL_FAST |
| Cobertura | **Cubierto**: la configuración por `TestLosSecretosNoSeImprimenPorNingunaVia`, `TestObligatoriasAusentes`, `TestSecretoDemasiadoCorto` y `TestSecretoDeDesarrolloEnProduccion` (`internal/config`) y su gemelo de `apps/web`; los logs por los tests de [INV-PLAYER-007](player.md#inv-player-007); los mensajes al cliente por `TestUnErrorDeInfraestructuraNoLlegaAlCliente` (`internal/game/simulation`) y «un error interno responde 500 con un mensaje fijo» (`apps/web`); el repositorio, en todas sus revisiones, y el bundle del cliente por `scripts/scan-secrets.mjs`, que corre en la CI y tiene sus tests en `scripts/scan-secrets.test.mjs` |

**Enunciado.** `EO_POSTGRES_URL`, `EO_REDIS_URL`, `EO_AUTH_JWT_SECRET`, `EO_IDENTITY_POSTGRES_URL` y cualquier otro secreto de infraestructura no aparecen jamás en un mensaje al cliente, en un artefacto servido al navegador, en un log, ni en el repositorio de código en ninguna revisión de su historia.

**Razón.** Cada uno tiene una consecuencia distinta y todas son terminales:

| Secreto | Si se filtra |
|---|---|
| `EO_POSTGRES_URL` | Acceso directo a la *source of truth*: lectura y escritura de todo el estado durable |
| `EO_REDIS_URL` | Manipulación de presencia, sesiones y anti-replay de tickets ([INV-SEC-003](#inv-sec-003)) |
| `EO_AUTH_JWT_SECRET` | **Falsificación de tickets y de tokens de fundación**: cualquier `sub`, cualquier `exp`. Suplantación de cualquier jugador sin límite |
| `EO_IDENTITY_POSTGRES_URL` | Acceso a las cuentas: hashes de contraseña y sesiones web ([ADR-018](../decisions/ADR-018-identity-store-and-ticket-issuer.md)) |

El secreto JWT es el peor. Desde [ADR-018](../decisions/ADR-018-identity-store-and-ticket-issuer.md) lo usan dos procesos —`apps/web` firma, el Game Server verifica— y en Next.js **hay una vía de fuga específica**: una variable de entorno con el prefijo `NEXT_PUBLIC_` se **incluye en el bundle del navegador**. `EO_AUTH_JWT_SECRET` y `EO_IDENTITY_POSTGRES_URL` no llevan ese prefijo y no deben llevarlo nunca; las lee en tiempo de ejecución el código de servidor (`apps/web/src/server/config.ts`), y todo módulo que las toca importa `server-only`, de modo que el build falla si un componente de cliente lo importa.

El repositorio importa tanto como el runtime: un secreto commiteado sigue en el historial de git aunque se borre en el commit siguiente. Rotarlo es la única mitigación real.

**Cómo se garantiza.**

- `BOUNDARY` — toda la configuración pasa por `internal/config` (canon §17) y se lee de variables de entorno. `EO_POSTGRES_URL`, `EO_REDIS_URL` y `EO_AUTH_JWT_SECRET` tienen valor por defecto vacío y son obligatorias: el arranque falla si faltan. `config.Load` exige además al menos 32 caracteres de secreto y rechaza el valor `dev-only` cuando `EO_ENV=production`. Los errores de configuración se reportan **todos juntos**, no de uno en uno, para que corregirlos no sea un juego de adivinanzas.
- `BOUNDARY` — `EO_POSTGRES_URL`, `EO_REDIS_URL` y `EO_AUTH_JWT_SECRET` se guardan en `config.Secret`, que imprime `[REDACTED]` por cualquier vía —`fmt` con cualquier verbo, `%#v` incluido, `slog` y JSON— y sólo entrega el valor con `Reveal()`, que se llama en el punto exacto de uso (`cmd/server`, `cmd/devseed`). Neutraliza el modo de fuga más común: loguear la estructura de configuración para depurar. Hasta `EO-118` eran `string`: la ficha prometía este tipo y no existía.
- `BOUNDARY` — ninguno de los mensajes servidor→cliente v1 contiene configuración de infraestructura. `session.welcome` lleva la información de sesión y de mundo que el cliente necesita, no cadenas de conexión.
- `BOUNDARY` — `system.error` devuelve `{ code, message, requestId?, details? }` (canon §16) con mensajes redactados: un error de PostgreSQL nunca se propaga textualmente al cliente, porque los mensajes de error de driver incluyen a menudo el host y el usuario. Al cliente le llega `INTERNAL_ERROR`; el detalle va al log del servidor.
- `BOUNDARY` — el repositorio incluye `.env.example` con las variables `EO_*` y valores de marcador, y `.gitignore` cubre los `.env` reales. La contraseña del almacén de identidad local la genera `pnpm run pg:identity` y sólo la escribe en el `.env`.
- `TEST` — **escaneo de secretos** (`scripts/scan-secrets.mjs`, Node sin dependencias), en la CI:
  - *árbol e historia* (job de documentación, con `fetch-depth: 0`): cada fichero versionado y cada línea añadida en cualquier revisión, contra reglas de alta señal —claves privadas, tokens con formato conocido (AWS, GitHub, Slack, Stripe, Google, npm, claves de API de LLM), JWT firmados, URL con contraseña (también sin usuario, como las de Redis) y asignaciones a variables con nombre de secreto en ficheros de configuración y documentación—;
  - *bundle del cliente* (job de TypeScript, tras `next build`): el build se hace con valores **canario** en `EO_AUTH_JWT_SECRET`, `EO_IDENTITY_POSTGRES_URL`, `EO_POSTGRES_URL` y `EO_REDIS_URL`, y `.next/static` no puede contener ninguno, ni el nombre de esas variables, ni las marcas de los módulos de servidor de `apps/web`, ni nada que case con las reglas anteriores. `--require-canaries` falla si falta alguno, para que el escaneo no pase por no tener nada que buscar.
  - Lo que parece credencial y no lo es —contraseñas del cluster de desarrollo y de los servicios efímeros de la CI, secretos de los tests, el ticket de ejemplo caducado del protocolo, los canarios— está en `scripts/secret-scan-allowlist.json`, **cada entrada con su motivo**; una entrada sin motivo hace fallar el escaneo. El informe nunca imprime un valor entero.
- `BOUNDARY` — el código de navegador de `apps/web` sólo lee `NEXT_PUBLIC_GAME_SERVER_WS_URL`. Las credenciales las lee el servidor de Next.js en `src/server/`, bajo `server-only`; `next.config.mjs` no copia ninguna a `env`.
- Las credenciales de desarrollo viven en `docker-compose.yml` y `scripts/pg-local.mjs` para los servicios locales, sólo escuchan en loopback y no se reutilizan en ningún otro entorno; por eso están en la lista de permitidos.

**Cómo se verifica.**

- `TestLosSecretosNoSeImprimenPorNingunaVia` (unit, `internal/config`) — la configuración cargada con tres credenciales, impresa con seis verbos de `fmt`, con `slog` en texto y en JSON y con `json.Marshal`, no contiene ninguna; `Reveal` sigue dando el valor. Verificado por mutación: sin `Format`, `%#v` la filtra.
- `TestObligatoriasAusentes` (unit, `internal/config`) — sin `EO_POSTGRES_URL` o `EO_AUTH_JWT_SECRET`, el arranque falla en lugar de usar un valor por defecto; sin `EO_REDIS_URL`, sólo fuera de producción se admite el estado caliente en proceso.
- `TestSecretoDemasiadoCorto` y `TestSecretoDeDesarrolloEnProduccion` (unit, `internal/config`) — un secreto de menos de 32 caracteres, o el de desarrollo en producción, abortan el arranque.
- Los tests de logs de [INV-PLAYER-007](player.md#inv-player-007) —`TestLosLogsDeLaFundacionNoContienenSecretos`, `TestLosLogsDelHandshakeNoContienenTicketsNiSecreto` y «los logs de la identidad no contienen secretos» en `apps/web`— comprueban que el secreto no aparece en ningún log.
- `TestErrorDeProtocoloLlevaCodigoEstable` (contract, `internal/protocol`) — `system.error` viaja con un código del catálogo cerrado, no con texto de driver.
- `TestUnErrorDeInfraestructuraNoLlegaAlCliente` (simulation, `internal/game/simulation`) — una entrada en guarnición cuya transacción falla para siempre con un error que lleva la cadena de conexión: el `system.error` es `INTERNAL_ERROR` con un mensaje fijo, sin la cadena, el host ni el texto del driver. Es una garantía estructural —el resultado que vuelve al loop no tiene campo para el texto del error— y el test la fija. En `apps/web`, «un error interno responde 500 con un mensaje fijo, sin el texto del error» hace lo mismo con la capa HTTP. Verificado por mutación: devolver el mensaje del error lo hace fallar.
- `node scripts/scan-secrets.mjs --tree --history` (CI, job de documentación; y `pnpm run secrets:scan`, dentro de `pnpm run verify`) — sin hallazgos en el árbol ni en ninguna de las revisiones de la historia. La primera ejecución encontró sólo valores de desarrollo, de CI y de tests, que son las entradas de la lista de permitidos.
- `node scripts/scan-secrets.mjs --bundle apps/web/.next/static --require-canaries` (CI, job de TypeScript) — sin hallazgos. Verificado por mutación contra un build real: copiar `EO_AUTH_JWT_SECRET` a una variable `NEXT_PUBLIC_` y leerla en la página hace aparecer el canario en un chunk y el paso falla; importar un módulo de `src/server/` desde la página rompe el build por `server-only` antes de llegar al escaneo.
- `scripts/scan-secrets.test.mjs` (Node) — cada regla detecta su muestra, los marcadores no cuentan, la lista admite por valor, ruta y regla y nada más, el informe enmascara, y el escaneo del bundle encuentra canarios, marcas de servidor y credenciales también en los `.map`. Quince mutaciones de sus guardas, todas muertas.

**Violación en runtime.** Lo que se detecta en runtime es la configuración inválida: el arranque aborta con todos los errores juntos, que nombran la variable y **jamás su valor** (`FAIL_FAST`). Una fuga no se detecta en runtime. Una fuga confirmada exige rotación inmediata del secreto afectado; corregir el código no basta, porque el valor ya está fuera.

---

<a id="inv-sec-007"></a>
## INV-SEC-007 — Un `requestId` repetido no produce un segundo efecto

| Campo | Valor |
|---|---|
| Severidad | CRITICO |
| Aplicación | BOUNDARY, TEST |
| Milestone | M3 |
| Política ante violación | REJECT (no reejecutar) |
| Cobertura | **Cubierto** por `TestComandoDuplicadoSeIgnora`, `TestUnDuplicadoTrasReconectarRecibeLaRespuestaOriginal` y `TestConLaColaLlenaElComandoRespondeYSePuedeReintentar` (`internal/websocket`) y por `TestIdempotenciaDeduplicaComandos`, `TestIdempotenciaEstaAcotadaPorJugador` y `TestReleaseLiberaLaReserva` (integration, `internal/persistence/redis`) |

**Enunciado.** Para un mismo `playerId`, un `requestId` ya procesado **no se reejecuta**: no se crea un segundo movimiento, no se crea una segunda unidad, no se aplica un segundo cambio de estado.

> **Alcance real.** La deduplicación es la de Redis: `IdempotencyStore.Claim` reserva `idem:{playerId}:{requestId}` con `SETNX` y TTL 300 s antes de ejecutar. Un duplicado **nunca se reejecuta**; si el original ya tuvo respuesta directa, el duplicado la recibe otra vez (I-5 de [../specs/websocket-protocol.md](../specs/websocket-protocol.md)), y si sigue en curso no recibe nada. Hasta `EO-118` el duplicado se descartaba siempre en silencio: `IdempotencyStore.Complete` existía y nadie lo llamaba, así que el cliente que reintentaba tras perder la respuesta no la recibía nunca.
>
> **No hay respaldo durable, por diseño.** [ADR-014](../decisions/ADR-014-command-idempotency.md) decidió que la deduplicación es de corto plazo y vive sólo en Redis; la tabla `idempotency_keys`, que nunca se escribió, se eliminó en `000004`. Un comando futuro con efectos no repetibles guardará su `requestId` en la fila de su efecto, con `UNIQUE`. Por eso esta ficha declara `BOUNDARY, TEST` y no `DB, DOMAIN`. Reproducir la respuesta no cambia esa decisión: la respuesta vive en la misma clave y con el mismo TTL que la reserva.

**Razón.** Es lo que hace segura la reconexión. El `requestId` es un UUIDv4 obligatorio en todo comando (canon §13), y el cliente reintenta cuando pierde la conexión sin haber recibido respuesta —que es exactamente el caso en que **no sabe** si el comando se ejecutó. Sin idempotencia, la única opción segura para el cliente sería no reintentar nunca, lo que haría que cada corte de red perdiera comandos.

Los efectos de una reejecución no son simétricos:

- **`unit.move` duplicado** — el segundo cancelaría el primero con `reason = REPLACED` y crearía otro. El resultado final es parecido, pero se emitirían `unit.movement.cancelled` y `unit.movement.started` espurios, y el `start_time_ms` se recalcularía, retrasando la llegada. Molesto y visible.
- **Creación de jugador duplicada** — dos ciudades y seis aldeanos ([INV-PLAYER-003](player.md#inv-player-003)). Ventaja material permanente obtenida por un corte de red. El alta no viaja por WebSocket ni lleva `requestId`: la protege el `UNIQUE` de `players.username`, que es exactamente el patrón de ADR-014 para efectos no repetibles.

**Cómo se garantiza.**

- `BOUNDARY` — el `requestId` es obligatorio en `unit.move` y `unit.cancel_move`: vacío, el servidor responde `INVALID_MESSAGE` antes de tocar nada. Su formato UUID lo exige el esquema del cliente; en Go sólo se exige que exista (I-1).
- `BOUNDARY` — `Server.claimRequest` consulta Redis **antes** de encolar el comando: `SETNX idem:{playerId}:{requestId} "pending" EX 300`. Si la reserva se gana, el comando se ejecuta; si no, no. La reserva ocurre **antes** de ejecutar, que es lo que resuelve la carrera de dos reintentos simultáneos: sólo uno gana el `SETNX`.
- `BOUNDARY` — la respuesta directa al comando (`unit.move.accepted`, `unit.move.rejected` o un `system.error` con su `requestId`) la ve pasar el `Hub` al enviarla, y el servidor la guarda en la misma clave con `IdempotencyStore.Complete`, en una goroutine propia: el game loop, que es quien envía, no espera nunca a Redis. Un duplicado posterior la recibe tal cual. Un duplicado que llegue antes de que termine esa escritura ve todavía `"pending"` y no recibe nada, igual que si el original siguiera en curso.
- `BOUNDARY` — la clave incluye el `playerId`, así que dos jugadores pueden usar el mismo `requestId` sin interferir; y el `playerId` proviene de la sesión ([INV-PLAYER-004](player.md#inv-player-004)), no del payload, de modo que nadie puede envenenar la caché de idempotencia de otro ni leer sus respuestas.
- `BOUNDARY` — `IdempotencyStore.Release` borra la reserva de un comando que **no llegó a ejecutarse** porque la cola del loop estaba llena, para que el reintento con el mismo `requestId` sí se ejecute (I-10). Hasta `EO-118` no lo llamaba nadie: el comando se perdía sin respuesta y su `requestId` quedaba quemado 300 s.
- `BOUNDARY` — si la clave expiró entre el `SETNX` y la lectura, se trata como primera vez. Es la decisión conservadora en la dirección de dejar jugar.

> **Degradación abierta si Redis no responde.** Si `Claim` devuelve error, `claimRequest` registra un `warn` («no se pudo verificar la idempotencia; se ejecuta igualmente») y **ejecuta el comando**. Es una decisión explícita y documentada: se prefiere jugar a bloquear al jugador. La consecuencia honesta es que, con Redis caído, un reintento **sí** puede producir un segundo efecto. Nótese el contraste deliberado con [INV-SEC-003](#inv-sec-003), que falla **cerrado**: allí lo que está en juego es la suplantación de una cuenta; aquí, un movimiento repetido. Las dos decisiones son opuestas porque los costes lo son.

**Cómo se verifica.**

- `TestComandoDuplicadoSeIgnora` (e2e, `internal/websocket`) — el mismo `unit.move` enviado dos veces seguidas inicia un solo movimiento; si el duplicado recibe respuesta, es la original.
- `TestUnDuplicadoTrasReconectarRecibeLaRespuestaOriginal` (e2e) — el cliente pierde la conexión tras la respuesta, reconecta y reenvía con el mismo `requestId`: recibe el mismo `unit.move.accepted`, con el mismo `movementId`, y no hay segundo movimiento. Verificado por mutación.
- `TestConLaColaLlenaElComandoRespondeYSePuedeReintentar` (e2e) — un comando que la cola no admite no quema su `requestId`. Verificado por mutación.
- `TestIdempotenciaDeduplicaComandos`, `TestIdempotenciaEstaAcotadaPorJugador` y `TestReleaseLiberaLaReserva` (integration, `internal/persistence/redis`, con gemelos en `internal/persistence/memory`) — la segunda reserva no se concede y devuelve la respuesta guardada, dos jugadores no colisionan, y liberar permite reintentar.
- `packages/protocol` (Vitest) — «exige requestId con formato UUID».

**Violación en runtime.** No hay detección de un segundo efecto en runtime. Lo que se registra es la decisión en cada duplicado (`debug`: «se reproduce la respuesta original» o «comando duplicado ignorado») y la degradación con Redis caído (`warn`), que es el único camino por el que el invariante puede romperse.

---

<a id="inv-sec-008"></a>
## INV-SEC-008 — Un token sólo sirve a su audiencia

| Campo | Valor |
|---|---|
| Severidad | CRITICO |
| Aplicación | BOUNDARY, TEST |
| Milestone | M3 |
| Política ante violación | REJECT |
| Cobertura | **Cubierto** por `TestLasAudienciasSonDisjuntas` y `TestLosTokensDeAppsWebSeVerificanEnGo` (unit, `internal/auth`) y por `TestUnTicketDeJuegoNoFundaJugadores` y `TestUnTokenDeFundacionSirveUnaVezYSoloParaSuSub` (integration, `internal/persistence/postgres`) |

**Enunciado.** Un ticket de juego (`aud: "game-server"`) no funda jugadores; un token de fundación
(`aud: "game-server-internal"`) no abre una sesión de juego; y la fundación sólo crea el jugador cuyo
`playerId` es el `sub` del token que la autoriza.

**Razón.** [ADR-018](../decisions/ADR-018-identity-store-and-ticket-issuer.md) firma los dos tokens con el mismo
`EO_AUTH_JWT_SECRET`. Sin audiencias disjuntas, cualquier jugador con un ticket de juego válido —que recibe en
cada conexión— podría llamar al endpoint interno y fundar jugadores a su gusto; y un token de fundación
interceptado abriría una sesión de juego para la cuenta recién creada. Atar el cuerpo al `sub` impide que un
token legítimo funde otro id que el que Next.js acaba de autenticar.

**Cómo se garantiza.**

- `BOUNDARY` — `auth.Verifier` recibe la audiencia al construirse y la exige con `jwt.WithAudience`. El
  WebSocket usa `auth.Audience` y el endpoint interno `auth.InternalAudience`, a través de
  `httpapi.NewFoundingAuthenticator`, que es lo que usan tanto `cmd/server` como los tests; ningún verificador
  acepta las dos.
- `BOUNDARY` — `POST /internal/players` rechaza con 401 un token de otra audiencia, caducado o ya consumido, y
  con 403 un cuerpo cuyo `playerId` no es el `sub`.
- `BOUNDARY` — el `jti` del token de fundación se consume en el mismo almacén anti-replay que el del ticket
  ([INV-SEC-003](#inv-sec-003)).

**Cómo se verifica.**

- `TestLasAudienciasSonDisjuntas` (unit, `internal/auth`) — cada verificador acepta su token y rechaza el del otro. Verificado por mutación: un verificador sin `WithAudience` lo hace fallar.
- `TestLosTokensDeAppsWebSeVerificanEnGo` (unit, `internal/auth`) — lo mismo con los tokens que produce el emisor de TypeScript.
- `TestUnTicketDeJuegoNoFundaJugadores` (integration) — un ticket de juego, o ningún token, no funda nada. Verificado por mutación: construir la fundación con la audiencia de juego lo hace fallar.
- `TestUnTokenDeFundacionSirveUnaVezYSoloParaSuSub` (integration) — un cuerpo con otro `playerId` recibe 403, y el token ya consumido no sirve después ni para su propio `sub`. Verificado por mutación.
- «el token de fundación sólo cambia la audiencia» (unit, `apps/web/src/server/tokens.test.ts`) — y la audiencia interna no es la de juego. Verificado por mutación.

**Violación en runtime.** Cada rechazo se registra en `warn` con el motivo y el `jti`, nunca con el token.

---


## Modelo de amenazas del MVP

Qué cubre este catálogo y qué queda explícitamente fuera.

| Amenaza | Invariante que la cubre |
|---|---|
| Suplantación de jugador | [INV-PLAYER-004](player.md#inv-player-004), [INV-SEC-002](#inv-sec-002), [INV-SEC-003](#inv-sec-003) |
| Sesión abierta desde otra página en el navegador del jugador (*cross-site WebSocket hijacking*) | [INV-SEC-002](#inv-sec-002) (`Origin` en el upgrade, [ADR-021](../decisions/ADR-021-game-server-perimeter.md)) |
| Control de unidades ajenas | [INV-PLAYER-001](player.md#inv-player-001), [INV-SEC-004](#inv-sec-004) |
| Falsificación de posición, velocidad o path | [INV-SEC-001](#inv-sec-001), [INV-MOVE-004](movement.md#inv-move-004), [INV-MOVE-006](movement.md#inv-move-006) |
| Replay de ticket | [INV-SEC-003](#inv-sec-003) |
| Duplicación de efectos por reintento | [INV-SEC-007](#inv-sec-007) |
| Agotamiento de recursos por mensajes | [INV-SEC-005](#inv-sec-005), [INV-SEC-002](#inv-sec-002) (timeout de handshake) |
| Fuga de credenciales | [INV-SEC-006](#inv-sec-006) |
| Evasión de la protección por desconexión | [INV-CITY-005](city.md#inv-city-005), [INV-CITY-006](city.md#inv-city-006) |

**Fuera de MVP**, sin invariante asignado y **TBD**: cifrado en reposo de la base de datos, rotación automatizada de secretos, auditoría de acciones administrativas, detección de bots y automatización de cliente, protección anti-DDoS a nivel de red, moderación y anti-abuso de chat (no hay chat en MVP, canon §21), rate limiting agregado por jugador a través de múltiples conexiones simultáneas, sub-límite de tasa dedicado para `session.view`, y token bucket durante el handshake. El respaldo durable de la idempotencia no es una carencia sino una decisión: [ADR-014](../decisions/ADR-014-command-idempotency.md).

## Trazabilidad

| Invariante | Componente propietario | Relacionado con |
|---|---|---|
| INV-SEC-001 | `packages/protocol`, `internal/websocket` | [INV-MOVE-006](movement.md#inv-move-006), [INV-CITY-005](city.md#inv-city-005) |
| INV-SEC-002 | `internal/websocket`, `internal/auth` | [INV-PLAYER-004](player.md#inv-player-004) |
| INV-SEC-003 | `internal/auth`, `internal/persistence/redis` | [INV-PERSIST-003](persistence.md#inv-persist-003) |
| INV-SEC-004 | `internal/websocket`, `internal/domain` | [INV-PLAYER-001](player.md#inv-player-001) |
| INV-SEC-005 | `internal/websocket` | [../specs/websocket-protocol.md](../specs/websocket-protocol.md) |
| INV-SEC-006 | `internal/config`, `apps/web/src/server` | [../operations/monitoring.md](../operations/monitoring.md) |
| INV-SEC-007 | `internal/websocket`, `internal/persistence/redis` | [INV-PERSIST-001](persistence.md#inv-persist-001), [INV-PLAYER-003](player.md#inv-player-003) |
| INV-SEC-008 | `internal/auth`, `internal/httpapi`, `apps/web` | [INV-SEC-003](#inv-sec-003), [INV-PLAYER-009](player.md#inv-player-009) |
