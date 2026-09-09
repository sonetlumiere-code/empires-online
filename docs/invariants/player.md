# Invariantes del Jugador (INV-PLAYER-xxx)

Propiedades de identidad, pertenencia y estado inicial del jugador: quién es, qué le pertenece y con qué nace.

Formato y severidades: [README.md](README.md). Modelo de jugador: canon §0 y §10. Autenticación: canon §14.

---

## Contexto

Un jugador es un **humano** (canon §0): no hay razas. La variedad proviene de dos ejes **ortogonales e independientes**:

| Eje | Tabla | Naturaleza | Ejemplos |
|---|---|---|---|
| **Civilization** | `civilizations` | Identidad cultural: unidades, tecnologías, bonos | `Roman`, `Byzantine`, `Persian`, `Norse` |
| **Global Faction** | `factions` | Alineamiento global | `ORDER`, `CHAOS`, `NEUTRAL` |

Que sean ortogonales significa que cualquier combinación es válida: un `Norse` de `ORDER` y un `Norse` de `CHAOS` son ambos legales, y ninguna regla puede derivar una de la otra. Los invariantes de este archivo lo tratan como dos pertenencias independientes, no como una jerarquía.

`players.id` es `uuid` —la única excepción a la convención de claves `bigint GENERATED ALWAYS AS IDENTITY` (canon §11)—, porque el identificador de jugador se emite en el claim `sub` del game ticket y viaja fuera del servidor de juego.

> **Nota sobre nombres de constraint.** La migración `000001_initial_schema.up.sql` declara las claves foráneas **en línea** (`REFERENCES players (id) ON DELETE CASCADE`), sin nombrarlas: PostgreSQL les asigna el nombre generado `<tabla>_<columna>_fkey`. Las únicas constraints con nombre explícito relacionadas con `players` son `players_username_format` (`CHECK (username ~ '^[A-Za-z0-9_-]{3,24}$')`) y la unicidad inline de `username`. Este documento cita los mecanismos reales, no nombres de convención que la DDL no usa.

---

<a id="inv-player-001"></a>
## INV-PLAYER-001 — Un jugador solo comanda unidades propias

| Campo | Valor |
|---|---|
| Severidad | CRITICO |
| Aplicación | DB, BOUNDARY, DOMAIN, TEST |
| Milestone | M4 |
| Política ante violación | REJECT |
| Cobertura | **Cubierto** por `TestNoSePuedeComandarUnaUnidadAjena` (`internal/websocket`) y `TestRechazosDeMovimiento` (`internal/game/simulation`) |

**Enunciado.** Para todo comando que referencia una unidad (`unit.move`, `unit.cancel_move`), el comando se ejecuta si y solo si `units.player_id` es igual al `playerId` de la sesión autenticada que lo envió.

**Razón.** Es la propiedad fundacional de la propiedad en un MMO. Si se rompe, cualquier jugador puede mover, y en el futuro sacrificar, las unidades de otro con solo adivinar o enumerar un `unitId`. Como los ids de unidad son `bigint` secuenciales (canon §11), enumerarlos es trivial: la única defensa es la comprobación de ownership, no la opacidad del identificador.

Nótese la relación con [INV-SEC-004](security.md#inv-sec-004): aquel invariante es la regla **procedimental** («el chequeo se ejecuta antes que el efecto»); este es la regla **semántica** («el resultado del chequeo determina la ejecución»). Se documentan por separado porque se rompen por causas distintas: uno por un orden de operaciones mal escrito, el otro por una comparación mal hecha.

**Cómo se garantiza.**

- `BOUNDARY` — el `playerId` usado en la comparación proviene de la sesión establecida en `session.hello`, nunca del payload ([INV-PLAYER-004](#inv-player-004)). El protocolo v1 de `unit.move` es `{ unitId, target:{x,y} }`: **no existe** un campo `playerId` en el payload que pudiera usarse por error.
- `DOMAIN` — `Simulation.handleMoveUnit` carga la unidad y llama a `unit.EnsureOwnedBy(playerID)` como **primera** validación, antes de `EnsureCanMove`, del chequeo de destino y del A\*. Una unidad ajena se responde con `UNIT_NOT_OWNED` (canon §16). La unidad inexistente se responde antes aún, con `UNIT_NOT_FOUND`.
- `DOMAIN` — la respuesta a una unidad inexistente (`UNIT_NOT_FOUND`) y a una unidad ajena (`UNIT_NOT_OWNED`) son códigos distintos por decisión explícita: el canon los define ambos y el MVP no oculta la existencia. Si en el futuro se quisiera evitar la enumeración, se unificarían ambos en `UNIT_NOT_FOUND`; eso requeriría ADR y sería **TBD (fuera de MVP)**.
- `DB` — la clave foránea inline de `units.player_id` hacia `players (id)` (`ON DELETE CASCADE`) garantiza que referencia un jugador existente; el `NOT NULL` garantiza que toda unidad tiene dueño ([INV-UNIT-006](units.md#inv-unit-006)).

**Cómo se verifica.**

- `TestNoSePuedeComandarUnaUnidadAjena` (e2e, `internal/websocket`) — **existe y pasa**: una sesión autenticada como otro jugador envía `unit.move` sobre una unidad ajena y recibe `unit.move.rejected` con `UNIT_NOT_OWNED`.
- `TestRechazosDeMovimiento` (simulation, `internal/game/simulation`) — **existe y pasa**: cubre los seis rechazos del pipeline, `UNIT_NOT_OWNED` entre ellos, y comprueba que la unidad no cambia de estado.
- Test previsto: `Test_INV_PLAYER_001_OwnershipCheckedBeforeUnitState` — jugador A intenta mover una unidad `DEAD` de B: el error debe ser `UNIT_NOT_OWNED`, no `UNIT_DEAD`. Un error de estado revelaría información sobre una unidad ajena.
- Test previsto: `Test_INV_PLAYER_001_ConcurrentOrdersDoNotCrossOwnership` (integration) — dos sesiones enviando órdenes simultáneas sobre unidades propias; ninguna afecta a las del otro.

**Violación en runtime.** Detección en `unit.EnsureOwnedBy`. Log `invariant_violation` con `inv_id=INV-PLAYER-001`, `player_id` de la sesión, `unitId` y el `player_id` real de la unidad. Política `REJECT`: se responde `unit.move.rejected` con `UNIT_NOT_OWNED` y no se muta nada. Una violación **detectada tras** el efecto (es decir, que se aplicó un cambio a una unidad ajena) escala a incidente de seguridad, no a bug ordinario.

---

<a id="inv-player-002"></a>
## INV-PLAYER-002 — Exactamente una civilization y exactamente una faction

| Campo | Valor |
|---|---|
| Severidad | ALTO |
| Aplicación | DB, TEST |
| Milestone | M2 |
| Política ante violación | FAIL_FAST en escritura (constraint) |
| Cobertura | **Sin cobertura ejecutada**: `TestBootstrapCreaMundoCompletoDelJugador` (integration, `internal/persistence/postgres`) lo ejercita pero está **diseñado y no ejecutado** porque el daemon de Docker no arrancó |

**Enunciado.** Toda fila de `players` tiene `civilization_id` no nulo referenciando una fila existente de `civilizations`, y `faction_id` no nulo referenciando una fila existente de `factions`; ninguna de las dos columnas admite valores múltiples ni nulos.

**Razón.** Los bonos de civilización y el alineamiento de facción son entradas de cálculo de gameplay. Un jugador sin civilización obliga a cada punto de cálculo a decidir qué hacer con el caso nulo, y en la práctica esa decisión se toma distinta en cada sitio: uno aplica bonos cero, otro aplica los de la primera civilización, otro entra en pánico. Un jugador con dos civilizaciones es directamente irrepresentable en el modelo de datos, y el invariante existe para que siga siéndolo si alguien propone una tabla de unión «por flexibilidad».

Que sean ejes ortogonales (canon §0) implica una consecuencia concreta: **no existe** ni debe existir una constraint que restrinja qué facciones puede tomar una civilización. Cualquier tabla o `CHECK` que acople ambos ejes viola el modelo.

**Cómo se garantiza.**

- `DB` — `players.civilization_id integer NOT NULL REFERENCES civilizations (id)` y `players.faction_id integer NOT NULL REFERENCES factions (id)`, ambas claves foráneas declaradas en línea. Al ser columnas escalares con `NOT NULL`, la cardinalidad «exactamente una» es estructural: no hay forma de representar cero ni dos.
- `DB` — la migración `000002_seed_catalogs` puebla `factions` con exactamente tres filas (`ORDER`, `CHAOS`, `NEUTRAL`) y `civilizations` con `ROMAN`, `BYZANTINE`, `PERSIAN` y `NORSE`, cada una con su `traits jsonb`.
- `DB` — la selección de civilización y facción se fija en la creación del jugador, dentro de la misma transacción de [INV-PLAYER-003](#inv-player-003).

**Cómo se verifica.**

- `TestBootstrapCreaMundoCompletoDelJugador` (integration, `internal/persistence/postgres`) — **diseñado, aún no ejecutado**: el jugador creado tiene ambas referencias resueltas.
- Test previsto: `Test_INV_PLAYER_002_UnknownReferenceRejected` (integration) — `INSERT` con un `faction_id` inexistente falla por clave foránea.
- Test previsto: `Test_INV_PLAYER_002_FactionCatalogIsExactlyThree` (integration) — `SELECT count(*) FROM factions` devuelve 3 y el conjunto de códigos es exactamente `{ORDER, CHAOS, NEUTRAL}`.
- Test previsto: `Test_INV_PLAYER_002_AxesAreOrthogonal` (integration) — para cada civilización, es posible crear jugadores con las tres facciones. El test falla si alguien introduce un acoplamiento entre ejes.

**Violación en runtime.** Imposible por construcción en escritura: PostgreSQL rechaza la fila. La detección relevante es en **lectura**: si el cargador de jugador encuentra una referencia irresoluble (por un borrado manual en `civilizations`), log `invariant_violation` con `inv_id=INV-PLAYER-002` y política `REJECT` de la sesión con `INTERNAL_ERROR`. No se asigna una civilización por defecto: eso convertiría un problema de datos visible en un cambio silencioso de gameplay.

> **Cambio de civilización o facción.** El MVP no ofrece ninguna vía para cambiarlas después de la creación. Las reglas, costes y cooldowns de un eventual cambio son **TBD (fuera de MVP)**.

---

<a id="inv-player-003"></a>
## INV-PLAYER-003 — Un jugador nuevo nace con una ciudad y tres aldeanos

| Campo | Valor |
|---|---|
| Severidad | ALTO |
| Aplicación | DOMAIN, TEST |
| Milestone | M2 |
| Política ante violación | FAIL_FAST (rollback de la transacción) |
| Cobertura | **Sin cobertura ejecutada**: `TestBootstrapCreaMundoCompletoDelJugador` y `TestBootstrapFallidoNoDejaNadaAMedias` (integration, `internal/persistence/postgres`) están **diseñados y no ejecutados** (Docker) |

**Enunciado.** Al completarse la creación de un jugador existen, atómicamente y de forma indivisible: exactamente una fila en `cities` con `owner` igual a ese jugador (con su `TOWN_CENTER` y su zona urbana amurallada inicial), y exactamente tres filas en `units` con `unit_type = VILLAGER` pertenecientes a ese jugador.

**Razón.** El estado inicial es la única configuración que el jugador no eligió; si es incorrecto, el jugador empieza roto. Los dos modos de fallo son asimétricos y ambos graves:

- **Creación parcial** (jugador sin ciudad, o con uno o dos aldeanos): el jugador no puede jugar, y al conectar el servidor no tiene dónde centrar la vista, porque `session.view` se inicializa en la ciudad del jugador (canon §13).
- **Creación duplicada** (dos ciudades o seis aldeanos por un reintento): ventaja material sobre el resto de jugadores, obtenida sin acción de gameplay.

El segundo modo es el que hace que este invariante necesite idempotencia real y no solo una transacción: un reintento del cliente sobre una creación que sí se completó pero cuya respuesta se perdió debe devolver el resultado original, nunca crear un segundo lote. Ver [INV-SEC-007](security.md#inv-sec-007).

**Cómo se garantiza.**

- `DOMAIN` — la creación de jugador es un caso de **write-through inmediato y transaccional** (canon §12): una única transacción de PostgreSQL inserta `players`, `cities` y las tres filas de `units`. No hay creación por pasos ni compensaciones.
- `DOMAIN` — el límite de **una ciudad por jugador** en MVP es una regla del dominio, no una constraint: la migración `000001` crea `cities_owner_idx` como índice **no único** sobre `owner_player_id`, y la única unicidad declarada sobre `cities` es `cities_unique_center UNIQUE (center_x, center_y)`. Un `INSERT` directo de una segunda ciudad para el mismo jugador **no lo impide la base de datos**; lo impide que el único punto de alta sea la transacción de bootstrap. Convertirlo en `UNIQUE` requeriría migración y es **TBD (fuera de MVP)**.
- `DOMAIN` — la cantidad `3` y el tipo `VILLAGER` provienen del emplazamiento determinista de `internal/game/founding` (tres aldeanos a `spawnRadius = 2` alrededor del centro), no de literales dispersos (canon §17: ningún valor de gameplay se hardcodea en más de un lugar).
- `DOMAIN` — el `TOWN_CENTER` es un *building*, no una unidad (canon §10): no cuenta como cuarta unidad y no aparece en `units`.
- `DOMAIN` — la colocación respeta [INV-CITY-007](city.md#inv-city-007) y [INV-UNIT-001](units.md#inv-unit-001): centro y aldeanos sobre tiles válidos y transitables. `founding.FindSite` busca en espiral desde una semilla derivada del nombre de usuario, exige un entorno despejado de radio 3 y una separación mínima de 24 tiles respecto de cualquier otro centro de ciudad. Si no encuentra sitio válido, la transacción hace rollback y la creación falla con `INTERNAL_ERROR`; no se coloca «donde sea».
- `DOMAIN` — el orden es estricto: **primero** la transacción de PostgreSQL (jugador + ciudad + 3 aldeanos, atómica) y **sólo después** el comando `IntroducePlayer` que incorpora al jugador al mundo en RAM ([INV-PLAYER-008](#inv-player-008)).

**Cómo se verifica.**

- `TestBootstrapCreaMundoCompletoDelJugador` (integration, `internal/persistence/postgres`) — **diseñado, aún no ejecutado**: tras crear un jugador hay exactamente una ciudad y tres `VILLAGER`, todos con el owner correcto.
- `TestBootstrapFallidoNoDejaNadaAMedias` (integration, `internal/persistence/postgres`) — **diseñado, aún no ejecutado**: un fallo a mitad del alta no deja ni jugador, ni ciudad, ni aldeanos.
- `TestDosCiudadesNoPuedenCompartirCentro` (integration, `internal/persistence/postgres`) — **diseñado, aún no ejecutado**: `cities_unique_center` rechaza dos ciudades en el mismo centro.
- Test previsto: `Test_INV_PLAYER_003_RetryDoesNotDuplicate` (integration) — dos ejecuciones con el mismo `requestId` producen un solo jugador con una ciudad y tres aldeanos.
- Test previsto: `Test_INV_PLAYER_003_InitialUnitsOnWalkableTiles` (integration) — los tres aldeanos están sobre tiles transitables y dentro de límites.

**Violación en runtime.** Detección en un chequeo posterior a la transacción (recuento inmediato dentro de la misma unidad de trabajo) y en el arranque de sesión, que verifica que el jugador tiene ciudad antes de emitir `session.welcome`. Log `invariant_violation` con `inv_id=INV-PLAYER-003`, `player_id` y los recuentos observados. Política `FAIL_FAST` durante la creación (rollback). Un jugador ya persistido con estado inicial incompleto no se repara automáticamente: se marca en `world_events` y se escala, porque cualquier reparación automática es indistinguible de una duplicación.

---

<a id="inv-player-004"></a>
## INV-PLAYER-004 — La identidad de la sesión proviene del ticket verificado

| Campo | Valor |
|---|---|
| Severidad | CRITICO |
| Aplicación | TYPE, BOUNDARY, TEST |
| Milestone | M3 |
| Política ante violación | FAIL_FAST |
| Cobertura | **Cubierto** por `TestTicketValidoSeVerifica`, `TestTicketCaducado`, `TestTicketFirmadoConOtroSecreto`, `TestAlgoritmoNoneSeRechaza`, `TestAudienciaIncorrecta`, `TestClaimsIncompletos`, `TestTicketSoloSeConsumeUnaVez` y `TestFalloDelRegistroDeTicketsFallaCerrado` (`internal/auth`), más `TestTicketInvalidoCierraLaConexion` y `TestPayloadConCamposExtraNoAportaEstado` (`internal/websocket`) |

**Enunciado.** El `playerId` asociado a una conexión WebSocket se obtiene exclusivamente del claim `sub` de un game ticket cuya firma HS256, `exp` y `aud = "game-server"` fueron verificados y cuyo `jti` fue consumido; ningún otro campo de ningún mensaje del cliente puede establecerlo ni modificarlo durante la vida de la conexión.

**Razón.** Es la suplantación total. Si el `playerId` pudiera venir del payload, cualquiera enviaría el `playerId` ajeno y obtendría control absoluto sobre esa cuenta: mover sus unidades, ver su área de interés, actuar sobre su ciudad. Todos los demás controles de ownership del sistema —[INV-PLAYER-001](#inv-player-001), [INV-SEC-004](security.md#inv-sec-004)— se apoyan en que el `playerId` de la sesión sea auténtico; si esa base cede, comprueban ownership correctamente contra la identidad equivocada.

**Cómo se garantiza.**

- `BOUNDARY` — el flujo del canon §14 es estricto: se emite un ticket JWT HS256 firmado con `EO_AUTH_JWT_SECRET` y TTL 60 s (`auth.Issuer`, con `sub`, `jti`, `aud` y `exp`); el cliente envía `session.hello { ticket }` como **primer** mensaje antes de 5 s (`WSHandshakeTimeout`, constante de código, no configurable) o se cierra con `4408`; el servidor verifica firma, `exp` y `aud = "game-server"` (`auth.Audience`), consume el `jti` en `ticket:jti:{jti}` (Redis, `SETNX` con TTL 120 s) y sólo entonces crea la sesión.
- `BOUNDARY` — **quién emite el ticket hoy**: lo emite el propio Game Server desde `internal/httpapi` (`POST /api/auth/register` y `POST /api/auth/login`, con bcrypt), cableado en `cmd/server/main.go` con `auth.NewIssuer(cfg.AuthJWTSecret, 60s, clock)`. Es explícitamente provisional: la arquitectura objetivo de [../decisions/ADR-010-authentication-game-ticket.md](../decisions/ADR-010-authentication-game-ticket.md) traslada la emisión a Next.js. El invariante no depende de quién emita: depende de que el `playerId` salga del claim `sub` verificado y de ningún otro sitio.
- `TYPE` — el `playerId` vive en la estructura de sesión del servidor, escrita **una sola vez** en el handshake. La estructura no expone un setter; el resto del código lo recibe por lectura. El estado inválido «sesión con playerId mutable» no es representable.
- `TYPE` — el esquema Zod de `session.hello` en `packages/protocol/src/v1/` declara el payload como `{ ticket }` y nada más. Los esquemas cliente→servidor son **estrictos** (`additionalProperties: false`), de modo que un campo extra se rechaza en lugar de ignorarse. Ningún mensaje cliente→servidor de v1 (`session.hello`, `session.ping`, `session.view`, `unit.move`, `unit.cancel_move`) contiene un campo de identidad de jugador, y los tests de `packages/protocol` verifican que un payload con un campo extra tipo `playerId` no valida.
- `BOUNDARY` — el `playerId` se propaga como campo `player_id` de todo log estructurado (canon §18), lo que hace auditable qué identidad ejecutó qué.

**Cómo se verifica.**

- `TestTicketValidoSeVerifica`, `TestTicketCaducado`, `TestTicketFirmadoConOtroSecreto`, `TestAlgoritmoNoneSeRechaza`, `TestAudienciaIncorrecta`, `TestClaimsIncompletos`, `TestTicketBasuraSeRechaza` (unit, `internal/auth`) — **existen y pasan**: la tabla completa de tickets inválidos no produce claims.
- `TestTicketSoloSeConsumeUnaVez` y `TestFalloDelRegistroDeTicketsFallaCerrado` (unit, `internal/auth`) — **existen y pasan**: consumo único del `jti` y fail-closed si el registro no responde.
- `TestTicketInvalidoCierraLaConexion` y `TestTicketNoSePuedeReutilizarEnOtraConexion` (e2e, `internal/websocket`) — **existen y pasan**: no se crea sesión y se cierra con `4401`.
- `TestPayloadConCamposExtraNoAportaEstado` (e2e, `internal/websocket`) — **existe y pasa**: un payload con campos extra no altera la identidad ni el estado.
- Test previsto: `Test_INV_PLAYER_004_SessionPlayerIdIsImmutable` (unit) — ningún camino de código muta el `playerId` de una sesión ya establecida.

**Violación en runtime.** Detección en el verificador de tickets y en una aserción del creador de sesión (`playerId` ya establecido al intentar establecerlo de nuevo). Log `invariant_violation` con `inv_id=INV-PLAYER-004`, `session_id` y el motivo del rechazo, **sin registrar el ticket completo**. Política `FAIL_FAST` sobre la conexión: cierre inmediato con `4401` (no autenticado) o `4403` (no autorizado) según el caso, sin procesar nada más de esa conexión. Este es el punto donde una violación implica ataque activo más probablemente que bug.

---

<a id="inv-player-005"></a>
## INV-PLAYER-005 — `players.id` es un `uuid` único

| Campo | Valor |
|---|---|
| Severidad | ALTO |
| Aplicación | DB, TYPE, TEST |
| Milestone | M2 |
| Política ante violación | FAIL_FAST (constraint) |
| Cobertura | **Sin cobertura ejecutada**: cubierto por la PK de la migración y ejercitado por `TestBootstrapCreaMundoCompletoDelJugador` (integration), **diseñado y no ejecutado** |

**Enunciado.** `players.id` es `uuid` y único: no existen dos filas de `players` con el mismo id, y ningún otro mecanismo (correlativo, índice posicional) identifica a un jugador.

**Razón.** El identificador de jugador es el único dato de identidad que **sale** del proceso: viaja en el claim `sub` del game ticket, aparece como `player_id` en todo log estructurado (canon §18) y es la clave de las claves de Redis `presence:player:{playerId}` e `idem:{playerId}:{requestId}`. Un identificador que se pudiera repetir haría que dos jugadores compartieran presencia, caché de idempotencia y trazas. Un identificador **correlativo** sería además enumerable desde fuera: quien viera un `sub` podría deducir los vecinos.

Que sea `uuid` y no `bigint GENERATED ALWAYS AS IDENTITY` es la única excepción deliberada a la convención de claves del canon §11, y existe exactamente por eso.

**Cómo se garantiza.**

- `DB` — `players.id uuid PRIMARY KEY` en `000001_initial_schema.up.sql`. La unicidad es estructural.
- `TYPE` — en Go el identificador es `uuid.UUID`, no `string` ni `int64`: un identificador mal formado no es representable, y no puede confundirse con el `bigint` de `units.id` o `cities.id` en una firma.
- `DOMAIN` — el `uuid` lo genera el servidor en el alta, nunca el cliente. Ningún payload cliente→servidor de v1 transporta un identificador de jugador ([INV-PLAYER-004](#inv-player-004)).
- `DB` — toda referencia a un jugador (`cities.owner_player_id`, `units.player_id`, `sessions.player_id`, `idempotency_keys.player_id`, `treaties.player_a_id`/`player_b_id`, `world_events.player_id`) es `uuid` con clave foránea hacia `players (id)`: no hay ninguna vía alternativa de identificación.

**Cómo se verifica.**

- `TestBootstrapCreaMundoCompletoDelJugador` (integration, `internal/persistence/postgres`) — **diseñado, aún no ejecutado**: el jugador insertado se recupera por su `uuid`.
- Test previsto: `Test_INV_PLAYER_005_PlayerIdIsUniqueUuid` (integration) — un segundo `INSERT` con el mismo `uuid` falla por clave primaria, y ninguna tabla identifica al jugador por otro medio.

**Violación en runtime.** Imposible en escritura: PostgreSQL rechaza el duplicado. En lectura, un `player_id` irresoluble produce log `invariant_violation` con `inv_id=INV-PLAYER-005`. Política `FAIL_FAST` de la carga de esa entidad.

---

<a id="inv-player-006"></a>
## INV-PLAYER-006 — `username` único y con formato validado en dos capas

| Campo | Valor |
|---|---|
| Severidad | ALTO |
| Aplicación | DB, BOUNDARY, TEST |
| Milestone | M2 |
| Política ante violación | REJECT en el borde · FAIL_FAST en escritura |
| Cobertura | **Sin cobertura ejecutada**: `player.ValidateUsername` está implementada y usada en `internal/httpapi`, pero **no existe todavía** un test dedicado; el `CHECK` sólo se ejercita en integración, aún no ejecutada |

**Enunciado.** `players.username` es único y cumple `^[A-Za-z0-9_-]{3,24}$`; la misma regla se aplica en el boundary (`player.ValidateUsername`) y en el `CHECK players_username_format` de PostgreSQL.

**Razón.** El nombre de usuario es a la vez credencial de login y semilla del emplazamiento inicial de la ciudad (`founding.FindSite` deriva su semilla del nombre). Eso le da dos exigencias que un simple campo de texto no tiene:

- **Unicidad** — si dos jugadores pudieran llamarse igual, `PlayerRepo.GetByUsername` devolvería una fila arbitraria y el login autenticaría contra el hash equivocado.
- **Formato cerrado** — un conjunto de caracteres restringido evita nombres homógrafos (dos usuarios visualmente idénticos con distintos puntos de código), espacios de anchura cero y cadenas que rompan el logging estructurado.

La regla se declara **dos veces a propósito**: el boundary la aplica para devolver un error legible antes de tocar la base de datos, y el `CHECK` la aplica para que ninguna otra vía de escritura pueda saltársela. Esa duplicación es defensa en profundidad, no redundancia: la regla en un solo sitio se pierde en el primer refactor.

**Cómo se garantiza.**

- `DB` — `players.username text NOT NULL UNIQUE` y `CONSTRAINT players_username_format CHECK (username ~ '^[A-Za-z0-9_-]{3,24}$')` en `000001_initial_schema.up.sql`. Es el nombre real de la constraint, sin prefijo de convención.
- `BOUNDARY` — `player.ValidateUsername(name string) error` en `internal/domain/player`, invocada por `internal/httpapi` antes de intentar el alta. Devuelve un error de dominio, no un error de driver.
- `DOMAIN` — la comparación de la regla es la misma expresión en ambos lados; un cambio de la clase de caracteres exige tocar los dos puntos y una migración, lo que lo convierte en una decisión visible.

**Cómo se verifica.**

- Test previsto: `Test_INV_PLAYER_006_UsernameFormatAndUniqueness` (integration) — la tabla de nombres inválidos (menos de 3, más de 24, caracteres fuera de la clase) es rechazada por `ValidateUsername` **y** por el `CHECK`; un segundo `INSERT` con el mismo `username` falla por unicidad.

**Violación en runtime.** Detección en `ValidateUsername` y en el error de constraint. Log `invariant_violation` con `inv_id=INV-PLAYER-006` y el motivo, **sin registrar credenciales**. Política `REJECT` del alta.

---

<a id="inv-player-007"></a>
## INV-PLAYER-007 — Ninguna contraseña ni secreto sale de su punto de uso

| Campo | Valor |
|---|---|
| Severidad | CRITICO |
| Aplicación | DB, BOUNDARY, TEST |
| Milestone | M2 |
| Política ante violación | FAIL_FAST |
| Cobertura | **Sin cobertura ejecutada**: `TestSecretoDemasiadoCorto` y `TestSecretoDeDesarrolloEnProduccion` (`internal/config`) cubren el arranque; **no existe todavía** un test que recorra el esquema y los logs |

**Enunciado.** Ninguna tabla guarda una contraseña en claro y ningún log estructurado contiene contraseña, `password_hash`, ticket completo ni `EO_AUTH_JWT_SECRET`; `password_hash` sólo sale de `PlayerRepo.GetByUsername` hacia el verificador de credenciales.

**Razón.** Es la extensión de [INV-SEC-006](security.md#inv-sec-006) al dato personal del jugador. Una contraseña en claro en una columna, o un `password_hash` volcado en un log de depuración, convierte una filtración de logs —mucho más frecuente que una filtración de base de datos— en una filtración de credenciales. Y el ticket completo en un log es directamente una sesión regalada durante 60 s ([INV-SEC-003](security.md#inv-sec-003)).

El punto de fuga concreto en este proyecto es el volcado de estructuras: `slog` serializa lo que se le pasa, y una `struct` de credenciales pasada entera a un `log.Debug` imprime el campo de contraseña sin preguntar.

**Cómo se garantiza.**

- `DB` — `players.password_hash text NOT NULL` es la **única** columna de credencial del esquema; no existe ninguna columna de contraseña en claro en ninguna tabla de `000001_initial_schema.up.sql`.
- `BOUNDARY` — el alta usa bcrypt en `internal/httpapi`: la contraseña recibida se hashea y el valor en claro no se propaga ni se conserva.
- `BOUNDARY` — `password_hash` se lee exclusivamente en `PlayerRepo.GetByUsername` y se consume en el verificador de credenciales; no viaja a ningún DTO, mensaje o log.
- `BOUNDARY` — los logs de autenticación registran el `username` y el motivo del fallo, nunca la contraseña, el hash ni el ticket. Del ticket sólo puede registrarse el `jti`, que ya está consumido.
- `BOUNDARY` — `config.Load` exige `EO_AUTH_JWT_SECRET` de al menos 32 caracteres y rechaza el valor de desarrollo (`dev-only`) cuando `EO_ENV=production`, de modo que el proceso nunca arranca con un secreto de ejemplo.

**Cómo se verifica.**

- `TestSecretoDemasiadoCorto` y `TestSecretoDeDesarrolloEnProduccion` (unit, `internal/config`) — **existen y pasan**: el arranque falla en ambos casos.
- `TestObligatoriasAusentes` (unit, `internal/config`) — **existe y pasa**: sin `EO_AUTH_JWT_SECRET` el arranque no continúa.
- Test previsto: `Test_INV_PLAYER_007_NoSecretsInSchemaOrLogs` (contract + integration) — se recorre la DDL buscando columnas de contraseña en claro y se ejercita el flujo de login capturando la salida de `slog` para comprobar que no contiene ni contraseña, ni hash, ni ticket, ni secreto.

**Violación en runtime.** Detección en el escaneo de CI y en el test de captura de logs. Log `invariant_violation` con `inv_id=INV-PLAYER-007` y el nombre del campo ofensivo, **jamás su valor**. Política `FAIL_FAST`. Una fuga confirmada de `EO_AUTH_JWT_SECRET` exige rotación inmediata: corregir el código no basta.

---

<a id="inv-player-008"></a>
## INV-PLAYER-008 — El mundo en RAM sólo conoce jugadores ya confirmados

| Campo | Valor |
|---|---|
| Severidad | ALTO |
| Aplicación | DOMAIN, TEST |
| Milestone | M2 |
| Política ante violación | FAIL_FAST (el jugador no se introduce) |
| Cobertura | **Sin cobertura ejecutada**: `TestBootstrapFallidoNoDejaNadaAMedias` (integration) cubre el rollback pero está **diseñado y no ejecutado**; el orden lo fija `internal/httpapi` |

**Enunciado.** El comando `IntroducePlayer` que incorpora al jugador al mundo en RAM se emite después del `COMMIT` del bootstrap; no existe ningún instante en que el mundo en RAM conozca a un jugador que PostgreSQL no conoce.

**Razón.** El orden inverso —introducir en RAM y persistir después— crea una ventana en la que la simulación tiene una ciudad y tres unidades que la base de datos no tiene. Si la transacción falla en esa ventana, el mundo se queda con entidades fantasma: aparecen en los snapshots de los vecinos, ocupan tiles en la capa de bloqueo y desaparecen en el siguiente reinicio sin explicación. Peor: sus identificadores en RAM no coincidirían con ningún id generado por la base de datos, así que ninguna escritura posterior sobre ellas encontraría fila que actualizar.

Con el orden correcto, el modo de fallo es benigno y observable: si el `COMMIT` tiene éxito pero el `IntroducePlayer` no llega a aplicarse, el jugador existe en disco y entra al mundo en el siguiente arranque por la vía normal de rehidratación. Es preferible un jugador que tarda en aparecer a un jugador que existe sólo en memoria.

**Cómo se garantiza.**

- `DOMAIN` — `internal/httpapi` ejecuta primero la transacción de bootstrap (jugador + ciudad + 3 aldeanos, atómica) y **sólo después** envía `IntroducePlayer` por el canal de comandos de la simulación. No hay ninguna ruta que invierta el orden.
- `DOMAIN` — `IntroducePlayer` recibe las entidades **con los identificadores ya asignados por PostgreSQL**, no unos provisionales: el mundo en RAM y el disco comparten identidad desde el primer instante.
- `DOMAIN` — la simulación no lee la base de datos durante el tick; recibe el alta como un comando más y la aplica en la fase de drenaje, con el aislamiento de pánicos de `applyCommand`.
- `DOMAIN` — si el bootstrap falla, la transacción hace rollback completo ([INV-PLAYER-003](#inv-player-003)) y el comando no llega a emitirse.

**Cómo se verifica.**

- `TestBootstrapFallidoNoDejaNadaAMedias` (integration, `internal/persistence/postgres`) — **diseñado, aún no ejecutado**: un fallo del bootstrap no deja rastro en disco.
- Test previsto: `Test_INV_PLAYER_008_WorldLearnsPlayerOnlyAfterCommit` (integration) — con un `Persister` instrumentado, se comprueba que `IntroducePlayer` no se emite si la transacción no confirmó, y que cuando se emite lleva los ids definitivos.

**Violación en runtime.** Detección en una aserción de `IntroducePlayer` que exige identificadores no nulos y una ciudad resoluble. Log `invariant_violation` con `inv_id=INV-PLAYER-008` y `player_id`. Política `FAIL_FAST` del comando: el jugador no se introduce en el mundo y se escala; entrará por rehidratación en el siguiente arranque.

---

## Trazabilidad

| Invariante | Componente propietario | Relacionado con |
|---|---|---|
| INV-PLAYER-001 | `internal/domain/unit`, `internal/game/simulation` | [INV-SEC-004](security.md#inv-sec-004), [INV-UNIT-006](units.md#inv-unit-006) |
| INV-PLAYER-002 | `internal/persistence/postgres` | [../database/schema.md](../database/schema.md) |
| INV-PLAYER-003 | `internal/game/founding`, `internal/persistence/postgres` | [INV-CITY-001](city.md#inv-city-001), [INV-SEC-007](security.md#inv-sec-007) |
| INV-PLAYER-004 | `internal/auth`, `internal/websocket` | [INV-SEC-002](security.md#inv-sec-002), [INV-SEC-003](security.md#inv-sec-003) |
| INV-PLAYER-005 | `internal/domain/player`, `internal/persistence/postgres` | [INV-UNIT-006](units.md#inv-unit-006) |
| INV-PLAYER-006 | `internal/domain/player`, `internal/httpapi` | [../database/schema.md](../database/schema.md) |
| INV-PLAYER-007 | `internal/httpapi`, `internal/observability` | [INV-SEC-006](security.md#inv-sec-006) |
| INV-PLAYER-008 | `internal/game/simulation`, `internal/persistence/postgres` | [INV-PERSIST-001](persistence.md#inv-persist-001) |
