# ADR-045: La guerra entre clanes es una relación entre dos clanes que no toca la paz, y su marcador viaja en los eventos que puntúan

Propósito: fijar cómo el slice de guerras entre clanes ([SPEC-CLAN-WARS](../specs/clan-wars.md)) compromete a dos
clanes sin cambiar `AtPeace`, quién escribe la guerra y con qué candados, cómo se rompen los pactos entre sus miembros
sin que ninguno quede vigente, y cómo se lleva un marcador que sobrevive a un reinicio sin escribir en cada punto.

| Campo | Valor |
|---|---|
| **Estado** | **Aceptado** |
| **Fecha** | **2026-11-17** |
| Ámbito | `internal/domain/clan` (la guerra, su máquina de estados, los puntos y el resultado), `internal/game/simulation` (las guerras en `clanState`, las órdenes, el compromiso sobre los tratados, el marcador, los plazos y el final por disolución), `internal/persistence/postgres` (las órdenes con los dos candados, el final en la disolución, la carga, la suma del marcador y la vista pública), `internal/domain/eventlog` (las entradas y los avisos de la guerra), `internal/game/loadcheck`, migración `000026`, `packages/protocol`, cliente |
| Índice | [README.md](README.md) |
| Relacionados | [ADR-022](ADR-022-durable-write-retry-and-loop-inbox.md) (ninguna escritura se pierde en silencio), [ADR-027](ADR-027-hot-treaties.md) (los tratados en caliente: órdenes en dos fases, vencimientos optimistas), [ADR-028](ADR-028-event-log-from-world-events.md) (el registro desde `world_events`), [ADR-032](ADR-032-trade-offers-and-two-key-jobs.md) (los trabajos de dos claves), [ADR-039](ADR-039-faction-peace-and-affinity.md) (la ruptura de los pactos al entrar en un bando), [ADR-040](ADR-040-clans-membership-and-peace.md) (la pertenencia, la clave y el candado del clan), [ADR-041](ADR-041-clan-territories.md) (la disolución en dos fases y una fila de evento por miembro), [ADR-042](ADR-042-clan-alliances.md) (la alianza entre clanes, sus dos claves y sus dos candados) |

---

## Contexto

Producto aceptó el 2026-11-17 las once recomendaciones de la spec:

- la guerra la declara un clan sin que el otro acepte; rompe los pactos y alianzas entre sus miembros y prohíbe firmar
  otros mientras dure, sin bonos ni otra paz que se levante;
- la declaran, ofrecen la paz y se rinden sólo los líderes;
- el marcador suma 1 por unidad enemiga muerta, 3 por edificio destruido y 10 por territorio ganado, con la pertenencia
  del instante;
- termina en empate por paz pactada, gana el otro por rendición o disolución, y a puntos al cumplirse 72 horas;
- el vencedor no recibe nada material: el resultado queda en un historial público;
- preparación de 15 minutos, guerra de 72 horas como mucho y tregua de 24 horas sin paz;
- una guerra declarada abierta por clan, las recibidas sin tope;
- no levanta la paz de la Orden ni la protección offline;
- los aliados no entran, no se declara a un aliado y dos clanes en guerra no se alían;
- las guerras abiertas, su marcador y el historial son públicos; las ofertas de paz, del líder y los oficiales;
- la declaración y el final se avisan a todos los miembros de los dos clanes; la oferta de paz, al líder del otro.

Piezas que ya existen y que el slice usa:

- **Las alianzas** ([ADR-042](ADR-042-clan-alliances.md)): una relación entre dos clanes en `clanState`, órdenes en
  dos fases con las claves `clan:<min>` y `clan:<max>` y los candados en orden de `id`, vencimientos optimistas y
  tolerantes, expiración en la transacción de la disolución y una fila de evento por miembro para lo que afecta a
  todos.
- **La ruptura por bando** ([ADR-039](ADR-039-faction-peace-and-affinity.md) §3 y §4): la RAM abre el preaviso de los
  pactos con el bando contrario y una escritura tolerante lo deja en la base; si un pacto tiene una orden en vuelo, se
  espera al tick siguiente.
- **Los eventos que puntúan** ya se escriben en la transacción del hecho: `UnitKilled` en la muerte de la unidad,
  `BuildingDestroyed` en el derribo y `TerritoryControlChanged` en la captura, con `player_id`, `counterpart_player_id`
  y `atMs` (ADR-028).
- **La vista pública de un clan** (`clan.lookup`) la atiende la base, fuera del loop.

## Decisión

### 1. La guerra es una relación entre clanes en `clanState`, y `AtPeace` no la lee

`clanState` gana las guerras abiertas (`DECLARED` y `ACTIVE`), indexadas por el par sin orden y por clan, con quién la
declaró, sus plazos, las ofertas de paz de cada lado y el marcador de cada lado. Guarda también los finales recientes
(par y fecha) de los últimos `EO_CLAN_WAR_COOLDOWN_HOURS`, para la tregua, y los recuentos del historial de cada clan.
Todo se carga con el mundo desde `clan_wars`. La validación de carga comprueba que cada guerra abierta une a dos clanes
vivos, que ningún par tiene dos y que ninguna convive con una alianza `ACTIVE` del mismo par; si alguna falla, la
cierra en la RAM y lo registra, como las demás reparaciones de carga.

`AtPeace` **no cambia**: la guerra ni da paz ni la quita. Sus efectos sobre la diplomacia se expresan con las piezas
que ya existen (§3) y dos comprobaciones más en el tick: `treaty.propose` y `treaty.respond` con `accept` de un
`NON_AGGRESSION` o una `ALLIANCE`, y `clan.alliance.propose` y `clan.alliance.respond` con `accept`, se rechazan entre
los dos lados de una guerra abierta o de una declaración en vuelo («vuestros clanes están en guerra»).

### 2. Las órdenes van en dos fases, con las dos claves y los dos candados; los plazos son optimistas

`clan.war.declare`, `clan.war.peace` y `clan.war.surrender` son comandos de la simulación, no hostiles, y siguen
ADR-042 §3:

- **En el tick.** Se valida contra la RAM: que quien la da es el líder de un clan que no se disuelve; el estado de la
  guerra y que no haya otra orden de guerra en vuelo sobre el par; para declarar, que no haya alianza `ACTIVE` ni guerra
  abierta entre los dos, el tope de declaradas (contando las declaraciones en vuelo) y la tregua. Después se reserva el
  par y se encola un trabajo con las claves de los dos clanes.
- **En la transacción.** Toma los candados de los dos clanes en orden de `id` y vuelve a comprobar el líder, que los dos
  viven, el estado de la fila, la alianza, el tope y la tregua. Si algo no cuadra, no escribe y vuelve como rechazo
  (`INVALID_TARGET`). La declaración expira en la misma transacción la oferta de alianza pendiente entre los dos.
- **Al volver por el buzón.** La RAM aplica el cambio o deshace la reserva. Si la escritura no llega tras sus
  reintentos, la RAM no ha cambiado y quien la dio recibe `INTERNAL_ERROR`.

Una oferta de paz (`offer: true`) cuando el otro lado ya la ofreció es una aceptación: la misma transacción cierra la
guerra en `PEACE`. Las ofertas son dos columnas de la fila, no filas aparte.

El paso de `DECLARED` a `ACTIVE` y el fin por plazo son optimistas (ADR-027 §3): la fase 5 los aplica en la RAM, una vez
por segundo, y un trabajo con las dos claves los escribe de forma tolerante (sólo si la fila sigue en el estado
esperado). Si falla del todo, se reencola en el barrido siguiente (ADR-022). El fin por plazo calcula el vencedor con el
marcador de la RAM en ese tick y lo congela en la fila.

### 3. El compromiso lo aplica la RAM sobre los tratados, con escrituras tolerantes en la clave de cada tratado

La guerra no escribe tratados en su transacción. La RAM aplica el compromiso con `enforceWarTreaties`, que recorre cada
guerra abierta y, para cada `NON_AGGRESSION` o `ALLIANCE` entre un miembro de un lado y uno del otro:

- si está `ACTIVE` sin preaviso, le abre el preaviso de `EO_TREATY_BREAK_NOTICE_MINUTES` en la RAM, como si lo hubiera
  roto el miembro del clan que declaró, con `TreatyBreakAnnounced` y `reason = WAR` en el payload;
- si está `PROPOSED`, la pasa a `EXPIRED`.

Cada cambio se escribe con el trabajo de fin de tratado de siempre (clave del par de jugadores, escritura tolerante: si
la fila ya no está como se esperaba, no escribe). Un tratado con una orden en vuelo se salta y se mira en la pasada
siguiente, como en ADR-039 §4.

`enforceWarTreaties` corre en el tick en que vuelve una declaración, en el que vuelve una entrada en un clan y en el que
vuelve la aceptación de un tratado, y además en el barrido de cada segundo. Así cubre sin más piezas los tres caminos
que dejarían un pacto vivo dentro de una guerra: los pactos que ya existían, el que entra en un clan en guerra y la
aceptación que estaba en vuelo cuando se declaró. Se descarta romper los pactos en la transacción de la guerra: la cola
de un tratado lleva la clave de su par de jugadores, no la de los clanes, y una aceptación en vuelo podría escribirse
después.

### 4. El marcador lo decide la RAM en el instante del hecho y viaja en el evento que puntúa

Cuando la simulación emite una muerte, un derribo o una captura, mira si el autor y la víctima (o el clan dueño del
territorio) son, en ese tick, miembros de los dos lados de una guerra `ACTIVE`. Si lo son, suma los puntos al lado del
autor y añade al evento un campo `war` con `{ warId, clanId, points }`. Ese evento se escribe en la transacción del
hecho, como hoy; en la captura, que es en dos fases, los puntos se suman cuando vuelve la transacción.

- **Al arrancar**, el marcador de cada guerra abierta es la suma de `points` por `clanId` de los eventos con su
  `warId`, con un índice de expresión parcial sobre `world_events`. Coincide por construcción con lo que la base sabe
  del mundo: un hecho que no llegó a la base tampoco puntúa.
- **La vista pública** (`clan.lookup`, en la base) usa la misma suma, así que puede ir un instante por detrás de la
  RAM mientras la cola escribe.
- **Al terminar**, la RAM congela su marcador en la fila de la guerra, que es lo que leen después el historial y la
  vista pública.

Se descartan un contador en la fila con dirty-flag, que tras una caída podría no cuadrar con los hechos escritos; un
write-through por punto, que metería una escritura de la guerra en cada muerte; y derivar la pertenencia de las
estancias de `clan_members` al recalcular, que no coincidiría con la RAM en la ventana de una entrada en dos fases.

### 5. La disolución termina las guerras en su transacción

La transacción que disuelve un clan (ADR-041 §4) cierra sus guerras abiertas en `DISSOLVED` con el otro clan de
vencedor, de forma tolerante (sólo si la fila sigue abierta). El marcador y el vencedor los pone la RAM, que decide la
disolución en el tick, en la orden de salida. La orden del otro clan comprueba en su transacción que los dos clanes
están vivos. La RAM cierra las guerras en el mismo tick que la disolución.

### 6. Los eventos de una guerra y el registro de dos clanes

Cada transición escribe su evento en `world_events`. `player_id` es el líder que dio la orden (o el líder del clan que
declaró, en los optimistas); `counterpart_player_id`, el líder del otro clan en ese momento; el payload lleva `atMs`,
`warId`, los dos `clanId` y las siglas.

- `ClanWarDeclared` y `ClanWarEnded` se escriben **una fila por miembro abierto** de cada clan (con `only = PLAYER`),
  leídos con los candados tomados, como el preaviso de una alianza (ADR-042 §6). `ClanWarEnded` lleva el motivo, el
  vencedor (o ninguno) y los dos marcadores. En la disolución, los miembros del clan que se disuelve ya no están: sólo
  reciben fila los del otro clan.
- `ClanWarPeaceOffered` se escribe una fila por líder y oficial del clan que la recibe.
- `ClanWarStarted` y `ClanWarPeaceWithdrawn` se escriben una sola fila y no van al registro.

Los avisos en vivo los manda la simulación con `sendNotices` a los miembros de su `clanState`.

### 7. La migración `000026`

- Tabla `clan_wars`:
  - `id` (identidad);
  - `attacker_clan` y `defender_clan` (FK a `clans`, `CHECK (attacker_clan <> defender_clan)`);
  - `declared_by_player` (FK a `players`) y `request_id`, únicos juntos;
  - `status` (`CHECK` de `DECLARED`, `ACTIVE` y `ENDED`);
  - `declared_at`, `starts_at` y `ends_at`;
  - `peace_offered_by_attacker` y `peace_offered_by_defender`;
  - `ended_at`, `end_reason` (`SURRENDER`, `PEACE`, `TIMEOUT` o `DISSOLVED`), `winner_clan` (FK, `NULL` en empate),
    `attacker_score` y `defender_score`, con un `CHECK` que exige fin, motivo y marcadores sólo en `ENDED`.

  Un índice único parcial sobre `(LEAST(attacker_clan, defender_clan), GREATEST(attacker_clan, defender_clan))` para
  las guerras abiertas, índices por clan para la carga, el tope y el historial, y el índice de expresión de §4 sobre
  `world_events`.
- Trigger `clan_wars_alliance_guard`: rechaza con `23514` que nazca una guerra entre dos clanes con una alianza
  `ACTIVE`. Trigger `clan_alliances_war_guard`: rechaza que nazca o se acepte una alianza entre dos clanes con una
  guerra abierta. Trigger `clan_wars_dissolved_guard`: rechaza que nazca una guerra con un clan disuelto.
- Se suma al `TRUNCATE` de los tests y a [schema.md](../database/schema.md) §1.

El tope de declaradas y la tregua son configuración, no esquema: los comprueban el tick y la transacción con los dos
candados.

## Alternativas descartadas

- **La guerra como quinta fuente de `AtPeace` que levanta la paz** entre los dos clanes. Dejaría a dos miembros de la
  Orden pegarse, que producto descartó (Q8), y convertiría `AtPeace` en una consulta con prioridades.
- **Romper los pactos entre miembros en la transacción de la declaración.** La transacción tendría que tocar filas de
  `treaties` que se escriben con otra clave de la cola, y una aceptación en vuelo podría escribirse después.
- **Un tratado de guerra entre los dos líderes.** Ataría la guerra a una persona, como la alianza (ADR-042).
- **Un marcador en una tabla aparte, con un write-through por punto o con dirty-flag** (§4).
- **Bloquear las entradas en un clan en guerra** o la salida durante la guerra: un clan decidiría sobre sus miembros
  según lo que hace otro, y la salida es un derecho del jugador desde E18.

## Consecuencias

- `AtPeace` no cambia y el combate no lee la guerra: el único coste en el camino caliente es mirar, al emitir una
  muerte, un derribo o una captura, el clan de dos jugadores y un mapa de guerras por par.
- Las órdenes de guerra bloquean a un worker hasta que llega la segunda clave, como las de alianza.
- La transacción de la disolución escribe además el final de las guerras del clan, con una fila de evento por miembro
  del otro clan.
- `world_events` gana un índice de expresión parcial, que sólo cubre las filas con `war`.
- Una guerra entre coaliciones, una llamada a las armas de los aliados o un tributo no caben en esta forma sin otra
  decisión; el par, las dos claves y el marcador en los eventos se mantendrían.
