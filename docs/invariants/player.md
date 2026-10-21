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

> **Nota sobre nombres de constraint.** La migración `000001_initial_schema.up.sql` declara las claves foráneas **en línea** (`REFERENCES players (id) ON DELETE CASCADE`), sin nombrarlas: PostgreSQL les asigna el nombre generado `<tabla>_<columna>_fkey`. Las únicas constraints con nombre explícito relacionadas con `players` son `players_username_format` (`CHECK (username ~ '^[A-Za-z0-9_-]{3,24}$')`) y la unicidad inline de `username`, que PostgreSQL llama `players_username_key`. Este documento cita los mecanismos reales, no nombres de convención que la DDL no usa.

> **Estado de estas fichas.** Reconciliadas con la suite por `EO-118`: cada fila `Cobertura` nombra tests que existen y pasan. Los tests de la fundación viven en `internal/persistence/postgres/fundacion_integration_test.go` y no en `internal/httpapi`, porque reutilizan el arnés de integración protegido. Desde [ADR-018](../decisions/ADR-018-identity-store-and-ticket-issuer.md) el alta la sirve `apps/web` y el game server sólo funda: las fichas que hablaban del «alta HTTP» del game server se reescribieron contra la fundación.

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
- El caso «unidad ajena y muerta» de `TestRechazosDeMovimiento` comprueba el orden: el jugador A que intenta mover una unidad `DEAD` de B recibe `UNIT_NOT_OWNED`, no `UNIT_DEAD`. Un error de estado revelaría información sobre una unidad ajena.
- No hay test de órdenes concurrentes que crucen la propiedad: todas las órdenes pasan por la cola de comandos de un único game loop, que las aplica de una en una.

**Violación en runtime.** Una orden sobre una unidad ajena no es una violación: es el invariante aplicándose. `unit.EnsureOwnedBy` la rechaza y se responde `unit.move.rejected` (o `system.error` en `unit.cancel_move`) con `UNIT_NOT_OWNED`, sin mutar nada. Una violación de verdad sería un cambio **aplicado** a una unidad ajena, y no hay aserción posterior que lo detecte; si apareciera, escalaría a incidente de seguridad, no a bug ordinario.

---

<a id="inv-player-002"></a>
## INV-PLAYER-002 — Exactamente una civilization y exactamente una faction

| Campo | Valor |
|---|---|
| Severidad | ALTO |
| Aplicación | DB, TEST |
| Milestone | M2 |
| Política ante violación | FAIL_FAST en escritura (constraint) |
| Cobertura | **Cubierto** por `TestLasRestriccionesDeJugadoresRechazanFilasInvalidas`, `TestLosCatalogosDeCivilizacionYFaccionSonLosDelCanon` y `TestLosDosEjesSonOrtogonales` (integration, `internal/persistence/postgres`) |

**Enunciado.** Toda fila de `players` tiene `civilization_id` no nulo referenciando una fila existente de `civilizations`, y `faction_id` no nulo referenciando una fila existente de `factions`; ninguna de las dos columnas admite valores múltiples ni nulos.

**Razón.** Los bonos de civilización y el alineamiento de facción son entradas de cálculo de gameplay. Un jugador sin civilización obliga a cada punto de cálculo a decidir qué hacer con el caso nulo, y en la práctica esa decisión se toma distinta en cada sitio: uno aplica bonos cero, otro aplica los de la primera civilización, otro entra en pánico. Un jugador con dos civilizaciones es directamente irrepresentable en el modelo de datos, y el invariante existe para que siga siéndolo si alguien propone una tabla de unión «por flexibilidad».

Que sean ejes ortogonales (canon §0) implica una consecuencia concreta: **no existe** ni debe existir una constraint que restrinja qué facciones puede tomar una civilización. Cualquier tabla o `CHECK` que acople ambos ejes viola el modelo.

**Cómo se garantiza.**

- `DB` — `players.civilization_id integer NOT NULL REFERENCES civilizations (id)` y `players.faction_id integer NOT NULL REFERENCES factions (id)`, ambas claves foráneas declaradas en línea. Al ser columnas escalares con `NOT NULL`, la cardinalidad «exactamente una» es estructural: no hay forma de representar cero ni dos.
- `DB` — la migración `000002_seed_catalogs` puebla `factions` con exactamente tres filas (`ORDER`, `CHAOS`, `NEUTRAL`) y `civilizations` con `ROMAN`, `BYZANTINE`, `PERSIAN` y `NORSE`, cada una con su `traits jsonb`.
- `DB` — la selección de civilización y facción se fija en la creación del jugador, dentro de la misma transacción de [INV-PLAYER-003](#inv-player-003).

**Cómo se verifica.**

- `TestLasRestriccionesDeJugadoresRechazanFilasInvalidas` (integration) — una civilización o una facción inexistente falla por `players_civilization_id_fkey` o `players_faction_id_fkey`, y una nula por `NOT NULL` (SQLSTATE `23502`).
- `TestLosCatalogosDeCivilizacionYFaccionSonLosDelCanon` (integration) — las civilizaciones son exactamente `{BYZANTINE, NORSE, PERSIAN, ROMAN}` y las facciones `{CHAOS, NEUTRAL, ORDER}`.
- `TestLosDosEjesSonOrtogonales` (integration) — las doce combinaciones civilización × facción dan de alta un jugador. Falla si alguien acopla los ejes.

**Violación en runtime.** Imposible en disco: las claves foráneas impiden incluso borrar una civilización o una facción que algún jugador use. Por eso no hay validación al cargar ni al hacer login. No se asigna nunca una civilización por defecto: eso convertiría un problema de datos visible en un cambio silencioso de gameplay.

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
| Cobertura | **Cubierto** por `TestBootstrapCreaMundoCompletoDelJugador`, `TestBootstrapFallidoNoDejaNadaAMedias`, `TestUnAltaConOtroNumeroDeAldeanosSeRechazaSinEscribir` y `TestSinSitioElAltaResponde409YNoEscribe` (integration, `internal/persistence/postgres`) |

**Enunciado.** Al completarse la creación de un jugador existen, atómicamente y de forma indivisible: exactamente una fila en `cities` con `owner` igual a ese jugador (con su `TOWN_CENTER` y su zona urbana amurallada inicial), y exactamente tres filas en `units` con `unit_type = VILLAGER` pertenecientes a ese jugador.

**Razón.** El estado inicial es la única configuración que el jugador no eligió; si es incorrecto, el jugador empieza roto. Los dos modos de fallo son asimétricos y ambos graves:

- **Creación parcial** (jugador sin ciudad, o con uno o dos aldeanos): el jugador no puede jugar, y al conectar el servidor no tiene dónde centrar la vista, porque `session.view` se inicializa en la ciudad del jugador (canon §13).
- **Creación duplicada** (dos ciudades o seis aldeanos por un reintento): ventaja material sobre el resto de jugadores, obtenida sin acción de gameplay.

El segundo modo lo cierra hoy la unicidad del nombre de usuario: un reintento del cliente sobre un alta que sí se completó, pero cuya respuesta se perdió, recibe `USERNAME_TAKEN` (409) y nunca crea un segundo lote. Lo que **no** hace es devolver el resultado original: el cliente tiene que iniciar sesión con las credenciales que ya envió. Devolver el resultado original exigiría idempotencia por `requestId` en la API HTTP, que no existe. Ver [INV-SEC-007](security.md#inv-sec-007).

**Cómo se garantiza.**

- `DOMAIN` — la creación de jugador es un caso de **write-through inmediato y transaccional** (canon §12): una única transacción de PostgreSQL inserta `players`, `cities` y las tres filas de `units`. No hay creación por pasos ni compensaciones.
- `DOMAIN` — el límite de **una ciudad por jugador** en MVP es una regla del dominio, no una constraint: la migración `000001` crea `cities_owner_idx` como índice **no único** sobre `owner_player_id`, y la única unicidad declarada sobre `cities` es `cities_unique_center UNIQUE (center_x, center_y)`. Un `INSERT` directo de una segunda ciudad para el mismo jugador **no lo impide la base de datos**; lo impide que el único punto de alta sea la transacción de bootstrap. Convertirlo en `UNIQUE` requeriría migración y es **TBD (fuera de MVP)**.
- `DOMAIN` — la cantidad es `player.InitialVillagers`, el único sitio del código donde vive el `3` (canon §17: ningún valor de gameplay se hardcodea en más de un lugar). La API de alta y la siembra de desarrollo se la pasan a `founding.FindSite`, que coloca los aldeanos a `spawnRadius = 2` del centro, y el `Bootstrapper` rechaza cualquier alta que no traiga exactamente esa cantidad. Hasta `EO-118` el `3` estaba escrito en tres sitios. El tipo `VILLAGER` lo fija el `Bootstrapper`.
- `DOMAIN` — el `TOWN_CENTER` es un *building*, no una unidad (canon §10): no cuenta como cuarta unidad y no aparece en `units`.
- `DOMAIN` — la colocación respeta [INV-CITY-007](city.md#inv-city-007) y [INV-UNIT-001](units.md#inv-unit-001): centro y aldeanos sobre tiles válidos y transitables. `founding.FindSite` busca en espiral desde una semilla derivada del nombre de usuario, exige un entorno despejado de radio 3 y una separación mínima de 24 tiles respecto de cualquier otro centro de ciudad. Si no encuentra sitio válido, el alta responde `NO_SITE_AVAILABLE` (409) antes de abrir la transacción, sin escribir nada; no se coloca «donde sea».
- `DOMAIN` — el orden es estricto: **primero** la transacción de PostgreSQL (jugador + ciudad + 3 aldeanos, atómica) y **sólo después** el comando `IntroducePlayer` que incorpora al jugador al mundo en RAM ([INV-PLAYER-008](#inv-player-008)).

**Cómo se verifica.**

- `TestBootstrapCreaMundoCompletoDelJugador` (integration) — tras crear un jugador hay exactamente una ciudad y tres `VILLAGER`, todos con el owner correcto.
- `TestBootstrapFallidoNoDejaNadaAMedias` (integration) — un alta que falla a mitad no deja ni jugador, ni ciudad, ni aldeanos.
- `TestUnAltaConOtroNumeroDeAldeanosSeRechazaSinEscribir` (integration) — un alta con 0, 2 o 4 aldeanos se rechaza sin dejar jugador.
- `TestSinSitioLaFundacionResponde409YNoEscribe` (integration) — en un mundo sin sitio, `NO_SITE_AVAILABLE` y nada escrito.
- `TestUnNombreDeOtroJugadorNoSeFundaNiSeReasigna` (integration) — fundar con el nombre de otro jugador da `USERNAME_TAKEN` y no crea un segundo lote; y `TestDosFundacionesSimultaneasDelMismoJugadorAcabanEnUno` — dos fundaciones del mismo jugador a la vez dejan una sola ciudad.
- Que los aldeanos nazcan sobre tiles transitables lo cubre `TestElSitioCaeDentroDelMundoYSobreTerrenoTransitable` (`internal/game/founding`), bajo [INV-CITY-007](city.md#inv-city-007).

**Violación en runtime.** Durante el alta, cualquier fallo deshace la transacción entera (`FAIL_FAST`). No hay más detección: ni un recuento posterior al commit ni una comprobación en el handshake de que el jugador tenga ciudad antes de `session.welcome`. Con el único escritor exigiendo el estado inicial completo, un jugador incompleto sólo podría venir de una escritura manual. Si apareciera, no se repararía automáticamente, porque cualquier reparación automática es indistinguible de una duplicación.

---

<a id="inv-player-004"></a>
## INV-PLAYER-004 — La identidad de la sesión proviene del ticket verificado

| Campo | Valor |
|---|---|
| Severidad | CRITICO |
| Aplicación | TYPE, BOUNDARY, TEST |
| Milestone | M3 |
| Política ante violación | FAIL_FAST |
| Cobertura | **Cubierto** por `TestTicketValidoSeVerifica`, `TestTicketCaducado`, `TestTicketFirmadoConOtroSecreto`, `TestAlgoritmoNoneSeRechaza`, `TestAudienciaIncorrecta`, `TestClaimsIncompletos`, `TestTicketNoSePuedeReutilizar` y `TestFalloDelRegistroDeTicketsFallaCerrado` (`internal/auth`), `TestTicketSoloSeConsumeUnaVez` (`internal/persistence/memory` y `redis`), y `TestTicketInvalidoCierraLaConexion` y `TestPayloadConCamposExtraNoAportaEstado` (`internal/websocket`) |

**Enunciado.** El `playerId` asociado a una conexión WebSocket se obtiene exclusivamente del claim `sub` de un game ticket cuya firma HS256, `exp` y `aud = "game-server"` fueron verificados y cuyo `jti` fue consumido; ningún otro campo de ningún mensaje del cliente puede establecerlo ni modificarlo durante la vida de la conexión.

**Razón.** Es la suplantación total. Si el `playerId` pudiera venir del payload, cualquiera enviaría el `playerId` ajeno y obtendría control absoluto sobre esa cuenta: mover sus unidades, ver su área de interés, actuar sobre su ciudad. Todos los demás controles de ownership del sistema —[INV-PLAYER-001](#inv-player-001), [INV-SEC-004](security.md#inv-sec-004)— se apoyan en que el `playerId` de la sesión sea auténtico; si esa base cede, comprueban ownership correctamente contra la identidad equivocada.

**Cómo se garantiza.**

- `BOUNDARY` — el flujo del canon §14 es estricto: se emite un ticket JWT HS256 firmado con `EO_AUTH_JWT_SECRET` y TTL 60 s (`auth.Issuer`, con `sub`, `jti`, `aud` y `exp`); el cliente envía `session.hello { ticket }` como **primer** mensaje antes de 5 s (`WSHandshakeTimeout`, constante de código, no configurable) o se cierra con `4408`; el servidor verifica firma, `exp` y `aud = "game-server"` (`auth.Audience`), consume el `jti` en `ticket:jti:{jti}` (Redis, `SETNX` con TTL 120 s) y sólo entonces crea la sesión.
- `BOUNDARY` — **quién emite el ticket**: `apps/web` (`src/server/tokens.ts`), en el alta, el login y `POST /api/auth/ticket`, como fija [ADR-010](../decisions/ADR-010-authentication-game-ticket.md) y concreta [ADR-018](../decisions/ADR-018-identity-store-and-ticket-issuer.md). El Game Server ya no firma tickets en producción; `auth.Issuer` queda para los tests. Un fixture compartido (`internal/auth/testdata/web-tokens.json`) ata los dos lados: el emisor de TypeScript tiene que seguir produciendo esos bytes y el verificador de Go tiene que aceptarlos (`TestLosTokensDeAppsWebSeVerificanEnGo`). El invariante no depende de quién emita: depende de que el `playerId` salga del claim `sub` verificado y de ningún otro sitio.
- `BOUNDARY` — el `playerId` vive en `websocket.Session.PlayerID`, que sólo escribe `newSession` con el `sub` de los claims verificados. **No es inmutable por tipo**: es un campo del paquete `websocket`, y nada fuera de él tiene acceso a la sesión. La garantía es que ningún código del paquete lo reasigna después, algo que cualquier revisión comprueba buscando asignaciones al campo.
- `TYPE` — el esquema Zod de `session.hello` en `packages/protocol/src/v1/` declara el payload como `{ ticket }` y nada más. Los esquemas cliente→servidor son **estrictos** (`additionalProperties: false`), de modo que un campo extra se rechaza en lugar de ignorarse. Ningún mensaje cliente→servidor de v1 (`session.hello`, `session.ping`, `session.view`, `unit.move`, `unit.cancel_move`) contiene un campo de identidad de jugador, y los tests de `packages/protocol` verifican que un payload con un campo extra tipo `playerId` no valida.
- `BOUNDARY` — el `playerId` se propaga como campo `player_id` de todo log estructurado (canon §18), lo que hace auditable qué identidad ejecutó qué.

**Cómo se verifica.**

- `TestTicketValidoSeVerifica`, `TestTicketCaducado`, `TestTicketFirmadoConOtroSecreto`, `TestAlgoritmoNoneSeRechaza`, `TestAudienciaIncorrecta`, `TestClaimsIncompletos`, `TestTicketBasuraSeRechaza` (unit, `internal/auth`) — **existen y pasan**: la tabla completa de tickets inválidos no produce claims.
- `TestTicketNoSePuedeReutilizar` y `TestFalloDelRegistroDeTicketsFallaCerrado` (unit, `internal/auth`) y `TestTicketSoloSeConsumeUnaVez` (`internal/persistence/memory`, y contra Redis real en `internal/persistence/redis`) — **existen y pasan**: consumo único del `jti` y fail-closed si el registro no responde.
- `TestTicketInvalidoCierraLaConexion` y `TestTicketNoSePuedeReutilizarEnOtraConexion` (e2e, `internal/websocket`) — **existen y pasan**: no se crea sesión y se cierra con `4401`.
- `TestPayloadConCamposExtraNoAportaEstado` (e2e, `internal/websocket`) — **existe y pasa**: un payload con campos extra no altera la identidad ni el estado.
- No hay test de que el `playerId` de una sesión no cambie: no existe ninguna ruta que lo intente.

**Violación en runtime.** Un ticket inválido no es una violación sino el borde haciendo su trabajo: el verificador lo rechaza, el servidor registra `handshake rechazado` con el motivo y el código de cierre, **sin el ticket**, y cierra la conexión con el código de `handshakeCloseCode` (`4401` para un ticket inválido o ya usado, `4408` si no llega a tiempo, `4400` si el primer mensaje no es un `session.hello` válido) sin crear sesión ni procesar nada más. Una violación de verdad sería una sesión cuya identidad no viene del ticket, y no hay ninguna aserción que la detecte; si apareciera, implicaría un ataque activo más probablemente que un bug.

---

<a id="inv-player-005"></a>
## INV-PLAYER-005 — `players.id` es un `uuid` único

| Campo | Valor |
|---|---|
| Severidad | ALTO |
| Aplicación | DB, TYPE, TEST |
| Milestone | M2 |
| Política ante violación | FAIL_FAST (constraint) |
| Cobertura | **Cubierto** por `TestLasRestriccionesDeJugadoresRechazanFilasInvalidas` y `TestBootstrapCreaMundoCompletoDelJugador` (integration, `internal/persistence/postgres`) |

**Enunciado.** `players.id` es `uuid` y único: no existen dos filas de `players` con el mismo id, y ningún otro mecanismo (correlativo, índice posicional) identifica a un jugador.

**Razón.** El identificador de jugador es el único dato de identidad que **sale** del proceso: viaja en el claim `sub` del game ticket, aparece como `player_id` en todo log estructurado (canon §18) y es la clave de las claves de Redis `presence:player:{playerId}` e `idem:{playerId}:{requestId}`. Un identificador que se pudiera repetir haría que dos jugadores compartieran presencia, caché de idempotencia y trazas. Un identificador **correlativo** sería además enumerable desde fuera: quien viera un `sub` podría deducir los vecinos.

Que sea `uuid` y no `bigint GENERATED ALWAYS AS IDENTITY` es la única excepción deliberada a la convención de claves del canon §11, y existe exactamente por eso.

**Cómo se garantiza.**

- `DB` — `players.id uuid PRIMARY KEY` en `000001_initial_schema.up.sql`. La unicidad es estructural.
- `TYPE` — en Go el identificador es `uuid.UUID`, no `string` ni `int64`: un identificador mal formado no es representable, y no puede confundirse con el `bigint` de `units.id` o `cities.id` en una firma.
- `DOMAIN` — el `uuid` lo genera el servidor en el alta, nunca el cliente. Ningún payload cliente→servidor de v1 transporta un identificador de jugador ([INV-PLAYER-004](#inv-player-004)).
- `DB` — toda referencia a un jugador (`cities.owner_player_id`, `units.player_id`, `sessions.player_id`, `treaties.player_a_id`/`player_b_id`, `world_events.player_id`) es `uuid` con clave foránea hacia `players (id)`: no hay ninguna vía alternativa de identificación.

**Cómo se verifica.**

- `TestLasRestriccionesDeJugadoresRechazanFilasInvalidas` (integration) — un segundo `INSERT` con el mismo `uuid` falla por `players_pkey`.
- `TestBootstrapCreaMundoCompletoDelJugador` (integration) — el jugador creado tiene un `uuid` no nulo y sus entidades lo referencian.

**Violación en runtime.** Imposible en disco: la clave primaria rechaza el duplicado y las claves foráneas impiden referencias a un jugador inexistente. Por eso no hay validación al cargar.

---

<a id="inv-player-006"></a>
## INV-PLAYER-006 — `username` único y con formato validado en dos capas

| Campo | Valor |
|---|---|
| Severidad | ALTO |
| Aplicación | DB, BOUNDARY, TEST |
| Milestone | M2 |
| Política ante violación | REJECT en el borde · FAIL_FAST en escritura |
| Cobertura | **Cubierto** por `TestValidateUsernameAplicaLaReglaDelEsquema` (`internal/domain/player`), `TestValidateUsernameYElCheckDicenLoMismo` y `TestLasRestriccionesDeJugadoresRechazanFilasInvalidas` (integration) |

**Enunciado.** `players.username` es único y cumple `^[A-Za-z0-9_-]{3,24}$`; la misma regla se aplica en el boundary (`player.ValidateUsername`) y en el `CHECK players_username_format` de PostgreSQL.

**Razón.** El nombre de usuario es a la vez credencial de login y semilla del emplazamiento inicial de la ciudad (`founding.FindSite` deriva su semilla del nombre). Eso le da dos exigencias que un simple campo de texto no tiene:

- **Unicidad** — si dos jugadores pudieran llamarse igual, `PlayerRepo.GetByUsername` devolvería una fila arbitraria y el login autenticaría contra el hash equivocado.
- **Formato cerrado** — un conjunto de caracteres restringido evita nombres homógrafos (dos usuarios visualmente idénticos con distintos puntos de código), espacios de anchura cero y cadenas que rompan el logging estructurado.

La regla se declara **dos veces a propósito**: el boundary la aplica para devolver un error legible antes de tocar la base de datos, y el `CHECK` la aplica para que ninguna otra vía de escritura pueda saltársela. Esa duplicación es defensa en profundidad, no redundancia: la regla en un solo sitio se pierde en el primer refactor.

**Cómo se garantiza.**

- `DB` — `players.username text NOT NULL UNIQUE` y `CONSTRAINT players_username_format CHECK (username ~ '^[A-Za-z0-9_-]{3,24}$')` en `000001_initial_schema.up.sql`. Es el nombre real de la constraint, sin prefijo de convención.
- `BOUNDARY` — `player.ValidateUsername(name string) error` en `internal/domain/player`, invocada por la fundación (`internal/httpapi`) antes de intentar el bootstrap; `apps/web` aplica la misma expresión antes de crear la cuenta, y `accounts_username_format` la repite en el almacén de identidad. Devuelve un error de dominio, no un error de driver.
- `DOMAIN` — la comparación de la regla es la misma expresión en ambos lados; un cambio de la clase de caracteres exige tocar los dos puntos y una migración, lo que lo convierte en una decisión visible.

**Cómo se verifica.**

- `TestValidateUsernameAplicaLaReglaDelEsquema` (unit) — longitudes límite, los caracteres permitidos y una tabla de rechazos: espacios, puntuación, letras acentuadas, emoji, tabulador y espacio de anchura cero.
- `TestValidateUsernameYElCheckDicenLoMismo` (integration) — para cada nombre de una tabla de válidos e inválidos, `ValidateUsername` lo acepta **si y sólo si** `players_username_format` lo acepta. Si alguien cambia una de las dos reglas sin la otra, falla.
- `TestLasRestriccionesDeJugadoresRechazanFilasInvalidas` (integration) — un segundo jugador con el mismo nombre falla por `players_username_key`.

**Violación en runtime.** Un nombre inválido no es una violación sino el borde aplicando la regla: la API responde `INVALID_USERNAME` (400) sin tocar la base de datos, y un nombre repetido, `USERNAME_TAKEN` (409). Ninguna de las dos rutas registra credenciales.

---

<a id="inv-player-007"></a>
## INV-PLAYER-007 — Ninguna contraseña ni secreto sale de su punto de uso

| Campo | Valor |
|---|---|
| Severidad | CRITICO |
| Aplicación | DB, BOUNDARY, TEST |
| Milestone | M2 (identidad movida a `apps/web` en M3, [ADR-018](../decisions/ADR-018-identity-store-and-ticket-issuer.md)) |
| Política ante violación | FAIL_FAST |
| Cobertura | **Cubierto**: el esquema del juego por `TestElEsquemaDelJuegoNoTieneCredenciales` y el del almacén de identidad por «las únicas columnas de credencial son dos hashes» (integration); los logs de la fundación por `TestLosLogsDeLaFundacionNoContienenSecretos` (integration), los del handshake por `TestLosLogsDelHandshakeNoContienenTicketsNiSecreto` (e2e, `internal/websocket`) y los del alta, el login y el ticket por «los logs de la identidad no contienen secretos» (`apps/web/src/server/authService.test.ts`); el arranque por `TestSecretoDemasiadoCorto`, `TestSecretoDeDesarrolloEnProduccion` (`internal/config`) y su gemelo de `apps/web` (`edges.test.ts`) |

**Enunciado.** Ninguna tabla guarda una contraseña en claro; el esquema del juego no tiene ninguna columna de
credencial y el almacén de identidad sólo tiene dos, ambas hashes (`accounts.password_hash` con `scrypt` y
`web_sessions.token_hash` con SHA-256); y ningún log estructurado, de ninguno de los dos procesos, contiene una
contraseña, un hash de contraseña, un token de sesión, un ticket o token de fundación —completo o en parte— ni
`EO_AUTH_JWT_SECRET`.

**Razón.** Es la extensión de [INV-SEC-006](security.md#inv-sec-006) al dato personal del jugador. Una contraseña en claro en una columna, o un hash volcado en un log de depuración, convierte una filtración de logs —mucho más frecuente que una filtración de base de datos— en una filtración de credenciales. Y un ticket en un log es directamente una sesión regalada durante 60 s ([INV-SEC-003](security.md#inv-sec-003)); un token de sesión, una sesión regalada durante 7 días.

El punto de fuga concreto en este proyecto es el volcado de estructuras: `slog` serializa lo que se le pasa, y una `struct` de credenciales pasada entera a un `log.Debug` imprime el campo de contraseña sin preguntar. En `apps/web`, lo mismo con un objeto pasado a `JSON.stringify`.

**Cómo se garantiza.**

- `DB` — la migración `000005` borró `players.password_hash`: el esquema del juego no tiene dónde guardar una credencial. En el almacén de identidad, `accounts_password_hash_format` exige el prefijo `scrypt$` y `web_sessions_token_hash_length` 32 bytes: un texto en claro no cabe en ninguna de las dos.
- `BOUNDARY` — la contraseña sólo atraviesa `apps/web`, el tiempo justo de derivar (`hashPassword`) o comparar (`verifyPassword`) su hash; no se propaga ni se conserva. El cliente la borra del formulario tras entrar y no la guarda: cada ticket nuevo se pide con la cookie.
- `BOUNDARY` — el token de sesión sólo existe en claro en la cookie `HttpOnly` del navegador; en la base se guarda su SHA-256.
- `BOUNDARY` — los logs registran el `username`, el `player_id`, el código y el motivo; nunca el valor de una credencial. Del ticket sólo puede registrarse el `jti`, que ya está consumido.
- `BOUNDARY` — los dos procesos exigen `EO_AUTH_JWT_SECRET` de al menos 32 caracteres y rechazan el valor de desarrollo (`dev-only`) con `EO_ENV=production`, y el mensaje de error no incluye el valor.
- `BOUNDARY` — el login con un usuario inexistente compara igualmente contra un hash de relleno y responde el mismo `UNAUTHORIZED` que una contraseña equivocada: no revela qué nombres existen.

**Cómo se verifica.**

- `TestSecretoDemasiadoCorto`, `TestSecretoDeDesarrolloEnProduccion` y `TestObligatoriasAusentes` (unit, `internal/config`) — el game server no arranca con un secreto ausente, corto o de desarrollo en producción. Sus gemelos en `apps/web/src/server/edges.test.ts` (`loadAuthConfig`), que además comprueban que el mensaje no contiene el secreto.
- `TestElEsquemaDelJuegoNoTieneCredenciales` (integration) — en todo el esquema `public` del juego, ninguna columna se llama como una contraseña, un secreto, un ticket, un token o una credencial.
- «las únicas columnas de credencial son dos hashes» (integration, `apps/web/src/server/store.integration.test.ts`) — en el almacén de identidad, sólo `accounts.password_hash` y `web_sessions.token_hash`.
- «los logs de la identidad no contienen secretos» (unit, `apps/web`) — recorre alta, login correcto y erróneo, usuario inexistente, alta rechazada, alta sin respuesta del mundo y petición de ticket: ninguna línea contiene la contraseña, su hash, el prefijo `scrypt$`, el secreto, un token de sesión ni un ticket. Verificado por mutación: registrar la contraseña lo hace fallar.
- `TestLosLogsDeLaFundacionNoContienenSecretos` (integration) — fundación correcta, rechazada por audiencia y fallida dentro de la transacción: ningún token ni el secreto. Verificado por mutación.
- `TestLosLogsDelHandshakeNoContienenTicketsNiSecreto` (e2e, `internal/websocket`) — handshake válido y rechazos por ticket basura, ajeno, caducado y reutilizado: ni los tickets, ni su firma por separado, ni el secreto. Verificado por mutación: registrar el ticket, o sólo su segunda mitad, lo hace fallar.

**Violación en runtime.** No hay detección en runtime: la detecta el test. Si una fuga apareciera, el tratamiento es `FAIL_FAST` sobre el cambio que la introdujo, y nunca se registra el valor filtrado, sólo el nombre del campo. Una fuga confirmada de `EO_AUTH_JWT_SECRET` exige rotación inmediata en los dos procesos: corregir el código no basta.

---

<a id="inv-player-008"></a>
## INV-PLAYER-008 — El mundo en RAM sólo conoce jugadores ya confirmados

| Campo | Valor |
|---|---|
| Severidad | ALTO |
| Aplicación | DOMAIN, TEST |
| Milestone | M2 |
| Política ante violación | FAIL_FAST (el jugador no se introduce) |
| Cobertura | **Cubierto** por `TestLaFundacionIntroduceAlJugadorDespuesDelCommit`, `TestUnNombreDeOtroJugadorNoSeFundaNiSeReasigna`, `TestLaFundacionEsIdempotentePorPlayerId` y `TestBootstrapFallidoNoDejaNadaAMedias` (integration, `internal/persistence/postgres`) |

**Enunciado.** El comando `IntroducePlayer` que incorpora al jugador al mundo en RAM se emite después del `COMMIT` del bootstrap; no existe ningún instante en que el mundo en RAM conozca a un jugador que PostgreSQL no conoce.

**Razón.** El orden inverso —introducir en RAM y persistir después— crea una ventana en la que la simulación tiene una ciudad y tres unidades que la base de datos no tiene. Si la transacción falla en esa ventana, el mundo se queda con entidades fantasma: aparecen en los snapshots de los vecinos, ocupan tiles en la capa de bloqueo y desaparecen en el siguiente reinicio sin explicación. Peor: sus identificadores en RAM no coincidirían con ningún id generado por la base de datos, así que ninguna escritura posterior sobre ellas encontraría fila que actualizar.

Con el orden correcto, el modo de fallo es benigno y observable: si el `COMMIT` tiene éxito pero el `IntroducePlayer` no llega a aplicarse, el jugador existe en disco y entra al mundo en el siguiente arranque por la vía normal de rehidratación. Es preferible un jugador que tarda en aparecer a un jugador que existe sólo en memoria.

**Cómo se garantiza.**

- `DOMAIN` — la fundación (`internal/httpapi`) ejecuta primero la transacción de bootstrap (jugador + ciudad + 3 aldeanos, atómica) y **sólo después** envía `IntroducePlayer` por el canal de comandos de la simulación. No hay ninguna ruta que invierta el orden.
- `DOMAIN` — `IntroducePlayer` recibe las entidades **con los identificadores ya asignados por PostgreSQL**, no unos provisionales: el mundo en RAM y el disco comparten identidad desde el primer instante.
- `DOMAIN` — la simulación no lee la base de datos durante el tick; recibe el alta como un comando más y la aplica en la fase de drenaje, con el aislamiento de pánicos de `applyCommand`.
- `DOMAIN` — si el bootstrap falla, la transacción hace rollback completo ([INV-PLAYER-003](#inv-player-003)) y el comando no llega a emitirse.

**Cómo se verifica.**

- `TestLaFundacionIntroduceAlJugadorDespuesDelCommit` (integration) — el handler de la fundación escribe en un canal de comandos sin buffer, así que queda bloqueado hasta que el test recibe `IntroducePlayer`. En ese instante, desde otra conexión, el jugador, su ciudad y sus aldeanos ya están confirmados, y los ids del comando son los de disco. Verificado por mutación: emitir un `IntroducePlayer` antes de la transacción lo hace fallar.
- `TestUnNombreDeOtroJugadorNoSeFundaNiSeReasigna` (integration) — una fundación rechazada no envía ningún comando; y `TestLaFundacionEsIdempotentePorPlayerId` — repetir la fundación de un jugador ya introducido no lo vuelve a introducir.
- `TestBootstrapFallidoNoDejaNadaAMedias` (integration) — un fallo del bootstrap no deja rastro en disco.

**Violación en runtime.** No hay detección en runtime: `handleIntroducePlayer` no valida los ids que recibe, porque su único emisor ya los recibió de PostgreSQL. La garantía es el orden del handler. Si el `COMMIT` tiene éxito y el comando no llega —por ejemplo, porque la cola está saturada más de 2 s—, el jugador existe en disco y entra en el mundo en el siguiente arranque por rehidratación.

---

<a id="inv-player-009"></a>
## INV-PLAYER-009 — Una cuenta sólo entra si su jugador existe en el mundo

| Campo | Valor |
|---|---|
| Severidad | ALTO |
| Aplicación | DB, DOMAIN, TEST |
| Milestone | M3 |
| Política ante violación | REJECT |
| Cobertura | **Cubierto** por los tests de la orquestación de `apps/web` (`src/server/authService.test.ts`), los del almacén contra PostgreSQL (`src/server/store.integration.test.ts`) y `TestLaFundacionEsIdempotentePorPlayerId`, `TestLaRepeticionNoBuscaSitioAunqueElMundoEsteLleno` y `TestDosFundacionesSimultaneasDelMismoJugadorAcabanEnUno` (integration, `internal/persistence/postgres`) |

**Enunciado.** Toda cuenta `ACTIVE` de `accounts` (almacén de identidad) tiene en el esquema del juego un
jugador con el mismo `id` y el mismo `username`; una cuenta `PENDING` no obtiene sesión web ni ticket.

**Razón.** Desde [ADR-018](../decisions/ADR-018-identity-store-and-ticket-issuer.md) la cuenta y el jugador viven
en dos bases que no comparten transacción. Una cuenta que pudiera entrar sin jugador recibiría un ticket cuyo
`sub` no existe en el mundo: el handshake lo aceptaría y el snapshot llegaría vacío, un jugador fantasma.

**Cómo se garantiza.**

- `DB` — `accounts.status` sólo admite `PENDING` y `ACTIVE` (`accounts_status_valid`), y nace `PENDING`.
- `DOMAIN` — el alta y el login de `apps/web` sólo pasan una cuenta a `ACTIVE` **después** de que el Game Server
  confirme el jugador; sólo crean sesión y ticket para cuentas `ACTIVE`.
- `DOMAIN` — la fundación es idempotente por `playerId`: repetirla tras un fallo intermedio devuelve el mismo
  jugador, y un nombre ocupado por otro id se rechaza (409), nunca se reasigna.

**Cómo se verifica.**

- «funda el jugador, activa la cuenta DESPUÉS y entrega sesión y ticket» (unit, `apps/web`) — el Game Server falso anota el estado de la cuenta en el instante de la llamada: era `PENDING`. Verificado por mutación: activar antes de fundar lo hace fallar.
- «sin respuesta del mundo la cuenta queda PENDING, sin sesión, y el login la termina» y «un rechazo definitivo del mundo borra la cuenta pendiente y libera el nombre» (unit, `apps/web`) — las dos salidas de una fundación que no confirma. Verificados por mutación: abrir sesión sin fundación, o no borrar la pendiente, los hacen fallar.
- «una sesión sólo resuelve si está vigente y su cuenta está ACTIVE» (integration, `apps/web`) — la guarda vive también en la consulta SQL. Verificado por mutación.
- `TestLaFundacionEsIdempotentePorPlayerId` (integration) — repetir la fundación devuelve el mismo jugador y no lo introduce dos veces.
- `TestLaRepeticionNoBuscaSitioAunqueElMundoEsteLleno` (integration) — el jugador existente se reconoce antes de buscar sitio: con el mundo lleno, la repetición responde 200 y no `NO_SITE_AVAILABLE`, que haría a `apps/web` borrar una cuenta cuyo jugador existe. Verificado por mutación.
- `TestDosFundacionesSimultaneasDelMismoJugadorAcabanEnUno` (integration) — el test retiene el cerrojo de fundación hasta que las dos esperan en él; una responde 201, la otra 200, y queda una ciudad. Verificado por mutación: sin el respaldo tras la violación de unicidad, la perdedora responde `USERNAME_TAKEN`.

**Violación en runtime.** No hay barrido que compare las dos bases. Una cuenta `ACTIVE` sin jugador sólo
podría aparecer por una escritura manual; el síntoma sería un snapshot sin ciudad propia.

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
| INV-PLAYER-007 | `apps/web` (identidad), `internal/httpapi`, `internal/websocket` | [INV-SEC-006](security.md#inv-sec-006) |
| INV-PLAYER-008 | `internal/game/simulation`, `internal/persistence/postgres` | [INV-PERSIST-001](persistence.md#inv-persist-001) |
| INV-PLAYER-009 | `apps/web` (identidad), `internal/httpapi` | [INV-PLAYER-008](#inv-player-008), [INV-SEC-008](security.md#inv-sec-008) |
