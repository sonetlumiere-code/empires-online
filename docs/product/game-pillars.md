# Pilares de diseño

Traduce la visión de producto en restricciones técnicas verificables: cada pilar declara qué sistema lo sostiene y qué alternativa rechaza.

---

## Cómo usar este documento

Un pilar no es un eslogan: es un criterio de aceptación. Toda propuesta de funcionalidad debe poder
señalar el pilar que refuerza. Toda decisión técnica debe poder señalar el pilar que respeta.

Cada pilar se describe con cinco campos:

- **Enunciado** — la regla, en una frase.
- **Por qué** — qué experiencia de jugador protege.
- **Sistema técnico que lo sostiene** — el mecanismo concreto que lo hace cierto, con nombres canónicos.
- **Anti-pilar** — la alternativa que rechazamos explícitamente, para no volver a discutirla.
- **Cómo se verifica** — la prueba o invariante que detecta una violación.

Resumen:

| # | Pilar | Sostenido por | Anti-pilar rechazado |
|---|---|---|---|
| 1 | El mundo nunca se detiene | Game loop a 10 Hz + persistencia durable | Simulación ligada a jugadores conectados |
| 2 | El servidor es la única verdad | Comandos como intención + validación autoritativa | Cliente que reporta su propio estado |
| 3 | Ausencia con consecuencias acotadas | Máquina de presencia `ONLINE`/`OFFLINE_PENDING`/`PROTECTED` | Invulnerabilidad total o abandono total |
| 4 | Interdependencia entre jugadores | Civilizaciones con capacidades exclusivas + tratados | Jugador autosuficiente |
| 5 | Territorio como recurso disputado | `territories` + `territory_control` sobre mundo fijo | Mapa infinito o regenerable |
| 6 | Legibilidad isométrica clásica | Grid de tiles, proyección solo en cliente | Render 3D libre / servidor con píxeles |
| 7 | Determinismo y reproducibilidad | `Clock` y `RandomSource` inyectados, orden estable | `time.Now()` y `rand` dentro del dominio |

---

## Pilar 1 — El mundo nunca se detiene

**Enunciado.** La simulación avanza de forma continua e independiente de que haya jugadores conectados.
Ningún estado durable depende de que exista un WebSocket vivo.

**Por qué.** Es la propiedad que convierte al juego en un mundo en lugar de en una partida. Si la
simulación se pausara con el último jugador, el tiempo dejaría de ser un recurso, las distancias dejarían
de costar y las decisiones dejarían de tener consecuencias más allá de la sesión. Todo lo demás en este
documento depende de que esto sea literalmente cierto.

**Sistema técnico que lo sostiene.**

- **Game loop de tick fijo** a 10 Hz (`EO_TICK_RATE_HZ=10`, período 100 ms) ejecutándose en el proceso Go
  del Game Server, con `tickNumber` uint64 monótono derivado de `world_state.epoch_ms`
  (`tickTime = epoch_ms + tickNumber * tickDurationMs`). Ver [../architecture/game-loop.md](../architecture/game-loop.md).
- **Orden fijo de fases** dentro del tick, sin excepciones ni atajos: `drain commands` →
  `validate & apply commands` → `advance movement` → `resolve simulation` → `process timers/scheduled events`
  → `update world state / interest sets` → `emit deltas` → `enqueue persistence`.
- **Persistencia desacoplada del tick**: el tick nunca ejecuta I/O bloqueante contra PostgreSQL; encola
  trabajo hacia workers asíncronos (hasta 3 intentos con backoff y compensación `OnPermanentFailure`). Un
  tick lento por la base de datos sería un mundo que se detiene. El precio honesto de esa decisión es una
  ventana de riesgo entre la aceptación en RAM y el `COMMIT`, registrada como `DEBT-17`.
- **Catch-up sin ráfagas**: el calendario del loop es de tiempo absoluto; los ticks perdidos se
  **descartan**, jamás se ejecutan en ráfaga para "ponerse al día".
- **Aislamiento de pánicos**: `applyCommand` recupera el pánico de un comando concreto, de modo que un bug
  en una regla no tumba la simulación del mundo entero.
- **El movimiento sobrevive al proceso**: la polilínea temporizada persistida en `unit_movements` permite
  reconstruir la posición autoritativa analíticamente. Al arrancar, `simulation.Hydrate` carga los
  movimientos `ACTIVE`: si el movimiento venció durante la caída, la unidad aparece en el destino y el
  movimiento se cierra `COMPLETED`; si sigue en curso, se reanuda desde la polilínea; si la polilínea es
  inválida, queda `FAILED` y la unidad se queda donde estaba, porque nunca se teletransporta a nadie por
  un dato dudoso.

**Anti-pilar rechazado.** *"Simular solo lo que alguien está mirando."* Es tentador (ahorra CPU) y es
incompatible con el juego: convierte la desconexión en una pausa y la observación en un factor causal.
También rechazamos la variante blanda —"recalcular el mundo al reconectar"— porque produce estados que
dependen del orden de reconexión de los jugadores y son imposibles de auditar.

**Cómo se verifica.** Tests de **simulation** con `FakeClock` sin ninguna conexión abierta ("avanza 10 s,
asserta estado exacto"), incluido el movimiento con el jugador desconectado, y tres tests de **recovery**
sobre `simulation.Hydrate` que cubren el movimiento vencido, el movimiento en curso y la polilínea
inválida. Todos existen en `internal/game/simulation` y están en verde. La métrica
`eo_game_tick_overruns_total` vigila que el tick no se desborde.

---

## Pilar 2 — El servidor es la única verdad

**Enunciado.** *Client sends intent, server determines truth.* El cliente nunca aporta estado
autoritativo: ni posición final, ni HP, ni recursos, ni resultados de combate, ni ownership, ni
cooldowns, ni ETA, ni paths.

**Por qué.** En un mundo persistente y compartido, un exploit no arruina una partida: arruina el mundo,
de forma permanente y para todos. Además, la confianza entre jugadores —base de los tratados y del
comercio— exige que nadie pueda afirmar unilateralmente un hecho del mundo.

**Sistema técnico que lo sostiene.**

- **Comandos como intención.** El cliente envía `unit.move { unitId, target:{x,y} }` y nunca una secuencia
  de posiciones. El servidor valida ownership → estado de la unidad → destino → ejecuta A\* → crea el
  movimiento → simula → notifica. Ver [../specs/movement.md](../specs/movement.md).
- **Superficie de comandos mínima.** Solo cinco mensajes cliente→servidor en v1: `session.hello`,
  `session.ping`, `session.view`, `unit.move`, `unit.cancel_move`. Lo que no existe no se puede falsear.
- **Autenticación por game ticket**: JWT HS256 de TTL 60 s, `aud: "game-server"`, con `jti` consumido en
  Redis (`ticket:jti:{jti}`, TTL 120 s) para impedir replay. Códigos de cierre estables `4400`, `4401`,
  `4403`, `4408`, `4429`, `4500`.
- **Idempotencia obligatoria**: `requestId` (UUIDv4) en todo comando, registrado en Redis
  (`idem:{playerId}:{requestId}`, TTL 300 s) y en `idempotency_keys` para comandos durables. Un
  `requestId` repetido devuelve la respuesta original, no re-ejecuta.
- **Límites duros por conexión**: mensaje ≤ 16 KiB (`EO_WS_MAX_MESSAGE_BYTES=16384`), 20 msg/s con burst
  40 (`EO_WS_RATE_LIMIT_PER_SECOND`, `EO_WS_RATE_LIMIT_BURST`) y cola de salida de
  `EO_WS_OUTBOUND_QUEUE_SIZE` (256). Si esa cola se llena, la conexión se cierra con `4500` y el cliente
  reconecta con un snapshot limpio; si se llena la cola de comandos, el comando se descarta y se responde
  `INTERNAL_ERROR`. La contrapresión es explícita, no un cuelgue.
- **Esquemas asimétricos a propósito**: los mensajes cliente→servidor se validan con
  `additionalProperties: false` —un campo extra se rechaza—; los servidor→cliente **no** son estrictos,
  para poder añadir campos opcionales sin romper clientes antiguos.
- **Errores por código, nunca por texto**: los **22 códigos** del catálogo cerrado (`UNIT_NOT_OWNED`,
  `TARGET_NOT_WALKABLE`, `PATH_TOO_LONG`…). La lógica de control usa `code`; el mensaje humano es
  decorado.

**Anti-pilar rechazado.** *"Predicción del cliente con reconciliación autoritativa diferida."* Es la
técnica estándar en shooters y es adecuada allí; aquí no. Nuestro tick es de 100 ms sobre tiles enteros,
la latencia no es el cuello de botella de la experiencia, y aceptar posiciones del cliente —aunque sea
provisionalmente— crea una ventana de estado no verificado en un mundo que no se reinicia. También se
rechaza la variante "el cliente calcula el path y lo envía": el path es estado autoritativo.

**Cómo se verifica.** Invariantes `INV-SEC-*` y `INV-UNIT-*`, tests de **contract** contra el JSON Schema
exportado por `@empires-online/protocol`, y un test por cada código de error alcanzable (parte de la
Definition of Done). La interpolación sub-tile del cliente es exclusivamente visual y no debe existir
ninguna ruta de código que la reenvíe al servidor.

---

## Pilar 3 — Ausencia con consecuencias acotadas

**Enunciado.** Desconectarse tiene consecuencias, pero acotadas y conocidas. Estar offline solo altera
las reglas ligadas a la presencia del jugador; no detiene el mundo ni entrega la ciudad.

**Por qué.** Es el equilibrio que hace habitable un mundo persistente. Sin ninguna protección, el juego
castiga a quien tiene trabajo, horarios o zona horaria distinta, y se degrada a una carrera de
disponibilidad. Con protección total, la persistencia se vuelve decorativa: nadie arriesga nada porque
basta con cerrar la pestaña. El pilar es explícitamente **acotada**, no ausente y no infinita.

**Sistema técnico que lo sostiene.**

- **Máquina de estados de presencia de la ciudad** (`cities.presence_state`):

  ```
  ONLINE --disconnect(+grace)--> OFFLINE_PENDING --cooldown--> PROTECTED
  PROTECTED --connect--> ONLINE ;  OFFLINE_PENDING --connect--> ONLINE
  ```

- **Margen de reconexión decidido en RAM**: la transición a `OFFLINE_PENDING` la evalúa el **game loop**
  en la fase 5 del tick, comparando el instante de la última desconexión contra `DisconnectGrace`, que
  vale `EO_PRESENCE_TTL_SECONDS` (30 s). Un jugador con varias sesiones abiertas sigue `ONLINE` mientras
  le quede una. Eso es la gracia de reconexión, y evita que un corte de red de cinco segundos cambie el
  estado del mundo.
- **Presencia en Redis**: clave `presence:player:{playerId}` con TTL 30 s (`EO_PRESENCE_TTL_SECONDS`) y
  heartbeat cada 10 s (`EO_PRESENCE_HEARTBEAT_SECONDS`). Redis **publica** la presencia para observadores
  externos y para el futuro multiproceso; no es quien arbitra la máquina de estados. Confundir ambas cosas
  haría que un fallo de Redis moviera el estado del mundo.
- **Cooldown configurable**: `EO_CITY_OFFLINE_PROTECTION_COOLDOWN_SECONDS` = 300 por defecto. Ningún valor
  de gameplay se hardcodea; todo pasa por `internal/config`.
- **`protection_until`**: NULL mientras el jugador siga offline (protección indefinida en el MVP) y se
  limpia al reconectar. El campo existe desde el principio para poder acotar la protección en el futuro
  sin migración de esquema.
- **El servidor decide**, el cliente solo observa el estado. `CITY_PROTECTED` es el código de error que
  rechaza acciones hostiles contra una ciudad protegida.

Ver [../specs/presence.md](../specs/presence.md) e [../invariants/city.md](../invariants/city.md).

**Anti-pilar rechazado.** Dos, simétricos. *"Full loot siempre, estés o no"*: expulsa a todo jugador que
no pueda vigilar su ciudad de forma continua. *"Invulnerabilidad total al desconectar"*: convierte la
desconexión en la mejor jugada defensiva del juego y vacía de sentido el territorio. También se rechaza
que el cliente pueda declararse protegido, o que la protección se active instantáneamente al cerrar la
conexión —de ahí el paso intermedio `OFFLINE_PENDING`, que impide el "alt-F4 defensivo".

**Cómo se verifica.** Tests de **unit** sobre el autómata completo de `internal/domain/city`
(`CanTransition`, `ShouldEngageProtection` con sus bordes) y de **simulation** con `FakeClock` (avanzar el
grace, avanzar el cooldown, reconectar en cada punto intermedio, varias sesiones simultáneas) —todos ellos
en verde—, más **integration** contra Redis real para la publicación de `presence:player:{playerId}`, aún
por ejecutar.

---

## Pilar 4 — Interdependencia entre jugadores

**Enunciado.** Ningún jugador puede ser autosuficiente. Las civilizaciones tienen unidades y tecnologías
exclusivas, y eso genera comercio real en lugar de comercio decorativo.

**Por qué.** La comunidad es el contenido principal de un mundo persistente, y la comunidad no aparece
por poner un chat: aparece cuando los jugadores se necesitan. Si cualquiera puede producir todo, la
diplomacia es cosmética y el mundo se vuelve una suma de partidas individuales que comparten mapa.

**Sistema técnico que lo sostiene.**

- **Dos ejes ortogonales desde el modelo de datos**: `civilizations` (identidad cultural: unidades,
  tecnologías y bonos) y `factions` (`ORDER`, `CHAOS`, `NEUTRAL`). Independientes: la civilización define
  *qué puedes producir*, la facción *con quién estás alineado*. Un jugador nunca "sube de raza": elige, y
  esa elección lo hace dependiente de otros.
- **Tratados como estado persistido y validado**: `treaties` con tipos `NON_AGGRESSION`, `ALLIANCE`,
  `TRADE`, estados `PROPOSED`, `ACTIVE`, `EXPIRED`, `BROKEN`, y flag `allows_garrison`. **Solo un tratado
  `ACTIVE` habilita garrison.** La cooperación tiene representación mecánica, no solo social.
- **Garrison** como primera manifestación concreta de la confianza: alojar unidades propias en una ciudad
  ajena exige un tratado activo que lo permita, y el servidor lo rechaza con `TREATY_REQUIRED` en caso
  contrario. Ver [../specs/garrison.md](../specs/garrison.md).

**Alcance actual.** En el MVP las tablas `treaties` y `garrisons` se crean con lógica mínima o diferida.
Los sistemas de economía, tecnología, mercados, rutas comerciales y caravanas
(`technologies`, `civilization_technologies`, `trade_routes`, `caravans`, `markets`,
`trade_transactions`) están **fuera de MVP**: existen como diseño, sin migración. Lo que sí está dentro
desde el día uno es la **estructura** —civilización y facción como ejes separados— porque introducirla
después obligaría a reescribir la identidad de todos los jugadores existentes.

**Anti-pilar rechazado.** *"Cada jugador puede desbloquear todo el árbol si invierte suficiente tiempo."*
Es el diseño que convierte el comercio en un atajo opcional y termina eliminando la razón para negociar.
También se rechaza la interdependencia forzada por escasez artificial de recursos aleatoria: la
dependencia debe venir de **capacidades** de civilización, que son legibles, estables y elegidas.

**Cómo se verifica.** Tests de **integration** sobre las transiciones de `treaties` y sobre el rechazo
`TREATY_REQUIRED` en garrison. A nivel de producto, la métrica de diseño es que ninguna civilización
pueda acceder al conjunto completo de capacidades por sí sola.

---

## Pilar 5 — Territorio como recurso disputado

**Enunciado.** El mapa es finito, fijo y compartido. La tierra tiene dueño, el dominio se puede perder y
la posición geográfica es una ventaja permanente o un problema permanente.

**Por qué.** Sin escasez espacial no hay conflicto estructural, solo escaramuzas. Un mundo finito
convierte la expansión en un juego de suma cero contra vecinos reales y da sentido a los tratados: se
firma con quien comparte frontera.

**Sistema técnico que lo sostiene.**

- **Mundo finito y determinista**: grid 2D de 512 × 512 tiles (`EO_WORLD_WIDTH`, `EO_WORLD_HEIGHT`,
  múltiplos exactos de `EO_CHUNK_SIZE`), coordenadas `x`, `y` int32 con origen (0,0) arriba-izquierda, X al
  este e Y al sur. El mapa se **regenera determinísticamente desde `EO_WORLD_SEED` en cada arranque**, y
  `world_chunks` (1024 bytes por chunk) guarda una copia para auditoría y edición futura, no como fuente
  primaria. El mundo no cambia entre arranques porque la semilla no cambia, no porque se lea de disco.
  Fuera de los límites, `TerrainAt` devuelve `WATER` y la transitabilidad es falsa: el borde del mundo es
  un muro, no un pánico.
- **Chunks de 32 × 32 tiles** (`EO_CHUNK_SIZE=32`), 16 × 16 = 256 chunks en el MVP, con
  `chunkX = x >> 5`, `chunkY = y >> 5` e ID `chunkY * chunksPerRow + chunkX` (uint32). El chunk es la
  unidad de suscripción de red y también la unidad natural de agregación territorial.
- **Terreno con coste y bloqueo**: `GRASSLAND` (`costUnits` 10), `FOREST` (16), `HILL` (18) y `ROAD` (6)
  transitables; `MOUNTAIN` y `WATER` bloqueados. El coste va en décimas del base (`world.CostBase = 10`),
  en enteros, para que la aritmética sea exacta en cualquier plataforma. La geografía no es decorado:
  define rutas, cuellos de botella y fronteras naturales. El bloqueo dinámico por edificios vive en una
  capa de ocupación (*blocked overlay*) separada, sin mutar el terreno base: fundar una ciudad no cambia el
  terreno de debajo.
- **Territorio modelado explícitamente**: `territories` con geometría rectangular
  (`min_x, min_y, max_x, max_y`) en el MVP y `territory_control` como tabla **separada**, porque la
  geometría es estable y el control cambia. Ver [../specs/territory.md](../specs/territory.md).
- **Safe Zones** ancladas al terreno: `DENSE_FOREST` sobre `FOREST` y `CAVERN` adyacente a `MOUNTAIN`.
  El refugio es un accidente geográfico, no un botón.

**Anti-pilar rechazado.** *"Mapa infinito o generado por jugador."* Elimina la escasez y con ella todo el
conflicto territorial: si siempre hay tierra libre más allá, nunca hay razón para disputar la de nadie.
También se rechaza el reset periódico del mapa ("temporadas"), que devolvería el juego al modelo de
partida largo.

**Cómo se verifica.** Invariantes `INV-WORLD-*` (los límites del grid y la generación determinista desde
la semilla) y tests que comprueban que dos generaciones con el mismo `EO_WORLD_SEED` producen
`world_chunks` byte a byte idénticos.

---

## Pilar 6 — Legibilidad isométrica clásica

**Enunciado.** El jugador debe entender el estado del mundo de un vistazo, con la gramática visual del
RTS isométrico clásico. La proyección es un asunto exclusivo del cliente.

**Por qué.** La legibilidad es lo que permite tomar decisiones estratégicas rápidas sin interfaz
sobrecargada, y es la herencia estética que conecta con las dos referencias del proyecto. Además, mantener
al servidor fuera del espacio de pantalla es lo que hace que el dominio sea testeable sin render.

**Sistema técnico que lo sostiene.**

- **El servidor no maneja píxeles nunca.** Razona en tiles enteros. La proyección es responsabilidad
  exclusiva del cliente: `screenX = (x - y) * (TILE_W/2)`, `screenY = (x + y) * (TILE_H/2)`, con
  `TILE_W = 64` y `TILE_H = 32`.
- **Vecindad de 8 direcciones** con prohibición de *corner cutting*: la diagonal solo es legal si ambos
  tiles ortogonales adyacentes son transitables. Lo que se ve como posible es posible; lo que se ve como
  bloqueado lo está.
- **Interpolación sub-tile puramente visual** en el cliente (Next.js 15 + React 19 + PixiJS 8). La
  *Render Position* suaviza; la *Authoritative Position* manda. `apps/web/` **ya existe y lo implementa**: este
  punto describe el diseño objetivo, no código entregado. Ver
  [../architecture/frontend.md](../architecture/frontend.md).
- **Sincronización por deltas sobre el área de interés**: `world.snapshot` al conectar y después
  `entity.spawn` / `entity.update` / `entity.despawn`, con suscripción por chunk y radio por defecto de 2
  chunks (`EO_INTEREST_RADIUS_CHUNKS=2`). El terreno de un chunk se envía **una sola vez por sesión**
  (`NeedsTerrain` / `MarkTerrainSent`), porque es inmutable, y viaja en base64. El cliente solo dibuja lo
  que legítimamente ve.

**Anti-pilar rechazado.** *"Cámara 3D libre con posiciones en coma flotante."* Rompe la legibilidad, exige
que el servidor razone en un espacio continuo y hace el pathfinding y los tests mucho más frágiles.
También se rechaza que el servidor envíe coordenadas de pantalla o cualquier concepto de píxel: sería
acoplar el dominio a una decisión de presentación.

**Cómo se verifica.** Test estructural: ningún paquete de `internal/domain` ni de `internal/game` puede
referenciar `TILE_W`, `TILE_H` ni ningún concepto de pantalla. Los tests de dominio corren sin cliente.

---

## Pilar 7 — Determinismo y reproducibilidad

**Enunciado.** Dado el mismo estado inicial y la misma secuencia de comandos, la simulación produce
exactamente el mismo resultado, siempre.

**Por qué.** En un mundo que no se reinicia, un bug no reproducible es un bug eterno. El determinismo es
lo que hace que la simulación sea auditable, que un incidente pueda investigarse y que los tests puedan
afirmar estados exactos en lugar de aproximaciones. Es el pilar que sostiene técnicamente a los otros
seis.

**Sistema técnico que lo sostiene.**

- **Nada de `time.Now()` ni `rand` dentro del dominio.** Interfaces `Clock` (`Now()`, `NowMs()`) y
  `RandomSource` inyectadas; `FakeClock` en tests.
- **Orden de fases fijo** en el tick y **desempate determinista** en A\*: ante coste igual, orden estable
  por `(f, h, y, x)`. Prohibido iterar mapas de Go sin ordenar previamente las claves.
- **Heurística octile en enteros escalados** (`costScaleOrtho = 1000`, `costScaleDiag = 1414`) y
  **ponderada por `MinTerrainCostUnits`, que vale 6 (`ROAD`)**: usar el coste de la hierba la volvería
  inadmisible en un mundo con caminos más baratos y A\* dejaría de garantizar la ruta óptima. Sin
  aritmética de coma flotante en el corazón del algoritmo. Límites duros
  `EO_PATHFINDING_MAX_NODES=20000` y `EO_PATHFINDING_MAX_DISTANCE=256`, con `PATH_TOO_LONG` al excederse.
- **Tiempo del mundo en milisegundos enteros**: los instantes de simulación se guardan como `bigint`
  epoch ms (`*_time_ms`) además del `timestamptz`, para aritmética exacta. La duración de un segmento se
  calcula íntegramente en enteros, con √2 en punto fijo (`1414214/1000000`):

  ```go
  ms := (baseMsPerTile*costUnits + 5) / 10           // redondeo al ms más cercano
  if diagonal { ms = (ms*1414214 + 500000) / 1000000 }
  if ms < 1 { ms = 1 }
  ```

  Se **redondea cada segmento y sólo después se acumula**; truncar regalaría casi un segundo de ventaja
  cada mil pasos.
- **Mapa reproducible** desde `EO_WORLD_SEED`.

**Anti-pilar rechazado.** *"Simulación best-effort con tolerancias."* Un `assert` con margen es un
invariante que no se puede escribir. Igualmente se rechaza el uso de relojes de pared dentro del dominio
"solo para logs": una vez que existe la dependencia, alguien la usará para una decisión.

**Cómo se verifica.** Tests de **simulation** con `FakeClock` que asertan estado exacto tras avanzar N
segundos, y ejecución repetida de los mismos escenarios comprobando igualdad byte a byte de los estados
resultantes.

---

## Tensiones conocidas entre pilares

Los pilares no son independientes; algunos empujan en direcciones opuestas. Se documentan aquí las
tensiones reales para que las decisiones futuras las resuelvan de forma consciente.

| Tensión | Descripción | Resolución adoptada |
|---|---|---|
| 1 vs. 3 | Si el mundo nunca se detiene, la ausencia debería doler; si la ausencia se protege, el mundo se congela por partes. | La protección afecta solo a reglas ligadas a la presencia; el resto del mundo sigue. `OFFLINE_PENDING` impide la protección instantánea. |
| 2 vs. 6 | La autoridad total del servidor añade latencia percibida; la legibilidad quiere respuesta inmediata. | El cliente interpola visualmente pero nunca decide. Tiles enteros y tick de 100 ms hacen la latencia tolerable. |
| 4 vs. 5 | La interdependencia empuja a cooperar; el territorio escaso empuja a competir. | Es la tensión buscada: los tratados existen precisamente para negociar fronteras. |
| 5 vs. 1 | Un mundo finito con simulación continua tiene un techo de carga. | Se acota el MVP a 512 × 512 y un solo proceso; los límites y vías de crecimiento se documentan en [../architecture/scalability.md](../architecture/scalability.md). |
| 7 vs. cualquiera | El determinismo encarece toda implementación. | Es innegociable: se paga el coste. |

---

## Documentos relacionados

- [vision.md](vision.md) — la experiencia de la que derivan estos pilares.
- [mvp-scope.md](mvp-scope.md) — qué parte de cada pilar se demuestra en el primer vertical slice.
- [../architecture/overview.md](../architecture/overview.md) — realización técnica.
- [../invariants/README.md](../invariants/README.md) — invariantes que blindan estos pilares.
