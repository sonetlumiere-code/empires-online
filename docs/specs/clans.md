# Clanes: grupos de jugadores con nombre, líder, paz y canal

Propuesta para que los jugadores puedan organizarse en grupos persistentes: un clan con nombre y sigla, un líder que
invita y expulsa, paz entre sus miembros y un canal de mensajes propio.

| Campo | Valor |
|---|---|
| Spec ID | `SPEC-CLANS` |
| Estado | Implemented (2026-11-15). Propuesta en `Draft` y aceptada ese mismo día, con la recomendación de cada pregunta de §10; en `main` desde la fusión `b228fdf`, con la CI en verde. |
| Milestone | E18 Clanes (decimoctavo slice después de M7) |
| Canon | §1.5 (determinismo), §12 (persistencia), §13 (protocolo), §14 (seguridad), §16 (errores), §21 (fuera de MVP: clanes) |
| Depende de | [player.md](player.md), [city.md](city.md), [diplomacy.md](diplomacy.md), [factions.md](factions.md), [messages.md](messages.md), [combat-raid.md](combat-raid.md), [combat-response.md](combat-response.md), [combat-pillage.md](combat-pillage.md), [combat-siege.md](combat-siege.md), [territory-conquest.md](territory-conquest.md), [caravans.md](caravans.md), [event-log.md](event-log.md), [live-notices.md](live-notices.md), [websocket-protocol.md](websocket-protocol.md) |
| Decisiones | [ADR-027](../decisions/ADR-027-hot-treaties.md) (los tratados en caliente y su preaviso), [ADR-028](../decisions/ADR-028-event-log-from-world-events.md) (el registro desde `world_events`), [ADR-038](../decisions/ADR-038-player-messages-session-subsystem.md) (los mensajes como subsistema de sesión), [ADR-039](../decisions/ADR-039-faction-peace-and-affinity.md) (la facción como segunda fuente de paz y la entrada en dos fases). [ADR-040](../decisions/ADR-040-clans-membership-and-peace.md) (la pertenencia a un clan como estado de la simulación y tercera fuente de paz, con su paz residual derivada al salir, la regla de bandos en el tick y en la base, y el canal del clan como subsistema de sesión autorizado contra la base): ver §7. |
| Reemplaza a | En [mvp-scope.md](../product/mvp-scope.md), `OUT-13` («Clanes»), en lo que entra en §2. En [messages.md](messages.md) §2, «clanes» y los canales de clan de su no-scope. En [../database/schema.md](../database/schema.md) §17.3, el diseño previsto de `clans` y `clan_members` (la sigla sustituye a `code`, el clan no tiene `faction_id` y la historia de pertenencia vive en `clan_members`: ver §7). Precisa `RN-COMBAT-006` de [combat-raid.md](combat-raid.md) (la paz entre dos jugadores sale también del clan) y `RN-FAC-005` de [factions.md](factions.md) (la Orden y el Caos tampoco comparten clan). Responde en parte a la pregunta `Q-16` del [backlog](../roadmap/backlog.md) (los canales de clan) y abre la fase Clans de [roadmap.md](../roadmap/roadmap.md) §5, sin territorios de clan. |
| Invariantes | Cita [INV-SEC-001](../invariants/security.md#inv-sec-001), [INV-SEC-004](../invariants/security.md#inv-sec-004), [INV-SEC-005](../invariants/security.md#inv-sec-005), [INV-SEC-007](../invariants/security.md#inv-sec-007), [INV-SEC-010](../invariants/security.md#inv-sec-010), [INV-COMBAT-002](../invariants/combat.md#inv-combat-002), [INV-COMBAT-006](../invariants/combat.md#inv-combat-006), [INV-DIPLO-003](../invariants/diplomacy.md#inv-diplo-003), [INV-PLAYER-013](../invariants/player.md#inv-player-013), [INV-PERSIST-003](../invariants/persistence.md#inv-persist-003) e [INV-PERSIST-005](../invariants/persistence.md#inv-persist-005); asigna [INV-CLAN-001](../invariants/clans.md#inv-clan-001), [INV-CLAN-002](../invariants/clans.md#inv-clan-002), [INV-CLAN-003](../invariants/clans.md#inv-clan-003) e [INV-SEC-012](../invariants/security.md#inv-sec-012) (§8) |

## 1. Objetivo

Que exista un nivel social entre el jugador solo y el bando. Hoy un jugador se relaciona con otro de tres maneras:
los tratados, que se firman de dos en dos ([diplomacy.md](diplomacy.md)); los mensajes, también de dos en dos
([messages.md](messages.md)); y el bando, que agrupa a cientos sin que nadie elija a quién
([factions.md](factions.md)). Lo que falta es lo que hace que un MMO tenga comunidad
([game-pillars.md](../product/game-pillars.md), pilar 4): un **grupo pequeño y elegido** que se protege, se coordina
y tiene un nombre que los demás reconocen en el mapa. Cinco jugadores que hoy quieran actuar juntos necesitan diez
pactos y diez conversaciones; con un clan les basta una invitación por cabeza y un canal.

## 2. Scope

**Entra.** Fundar un clan con nombre y sigla (Q2); invitar, aceptar o rechazar una invitación, retirarla, salir y
expulsar (Q3); un líder y miembros, con el liderazgo que pasa solo al miembro más antiguo y el clan que se disuelve
cuando sale el último (Q4); un tope de miembros (Q5); la **paz del clan**: dos miembros no pueden agredirse (Q1), con
una **paz residual** de 15 minutos al salir o ser expulsado (Q6); la regla de que un clan no mezcla la Orden y el Caos
(Q7); la sigla del clan en la vista de la ciudad y la lista pública de sus miembros (Q8); el **canal del clan**, con
las reglas de los mensajes de E16 (Q9); la expulsión en el registro y en los avisos del expulsado (Q10); la migración
`000020`; en el cliente, el botón «Clan» con su panel, «Invitar al clan» en la barra de una ciudad ajena, la sigla en
el mapa y los rechazos con su motivo; `scripts/smoke.mjs --clan` con cuentas propias.

**No entra** (`Fuera de MVP` de este slice): territorios cuyo dueño es un clan (`owner_type = 'CLAN'`, que
[territory.md](territory.md) ya prevé) y que la tierra de un compañero cuente como afín (Q11); guarnición, comercio o
almacén compartidos entre miembros sin tratado (Q12); oficiales u otros rangos, traspaso explícito del liderazgo y
votaciones (Q4); solicitudes de entrada del aspirante y clanes abiertos (Q3); renombrar un clan o cambiar su sigla;
un bando propio del clan (Q7); guerras de clan, alianzas entre clanes y ranking de clanes; un directorio o buscador de
clanes (se conocen por la sigla en el mapa); moderación del canal más allá de expulsar (Q9); el borrado definitivo de
los mensajes viejos del canal, que va con la operación 24/7 como el de E16; repartir la entrega en vivo entre varios
procesos del Game Server (`OUT-17`).

## 3. Actores

El líder (funda, invita, retira invitaciones y expulsa), los miembros (salen, escriben y leen el canal), el jugador
invitado (acepta o rechaza), los demás jugadores (ven la sigla y los miembros, y no pueden contar con agredir a dos
miembros entre sí), cliente web, Game Server (la simulación decide la pertenencia y la paz en el tick,
[INV-SEC-001](../invariants/security.md#inv-sec-001); la conexión de cada sesión atiende el canal, como en E16),
PostgreSQL (los clanes, la pertenencia con su historia, las invitaciones y el canal). Redis no participa.

## 4. Modelo

### 4.1 El clan

Un clan es `{ id, nombre, sigla, líder, fundado }`. El **nombre** tiene de 3 a 24 caracteres (letras, dígitos,
espacios y guiones, sin espacios en los extremos ni dobles); la **sigla**, de 2 a 4 caracteres de `A`–`Z` y `0`–`9`.
Los dos son únicos entre los clanes vivos sin distinguir mayúsculas, y no cambian (Q2). El `id` lo asigna la base.

Un clan tiene entre 1 y `EO_CLAN_MAX_MEMBERS` miembros (20, Q5); exactamente uno es el **líder**. Un jugador es miembro
de como mucho un clan a la vez. Cuando sale el último miembro, el clan se **disuelve**: deja de existir para el juego
y su nombre y su sigla quedan libres; su fila y su historia se conservan.

A cada miembro se le nombra por su ciudad, como en los mensajes (`RN-MSG-010`): el nombre de usuario no sale nunca.

### 4.2 Fundar, invitar, entrar

Todas las órdenes que cambian quién está en un clan son **comandos de la simulación**, no hostiles: la pertenencia
decide la paz, y la paz la decide el tick (§7). Van en dos fases, como `faction.join`: el tick valida y reserva, un
trabajo de la cola escribe, y al volver por el buzón se confirma y se difunde (un rechazo de la base deshace la reserva
y responde el código). Nada cuenta hasta la confirmación.

- `clan.create { name, tag }`: el jugador no está en ningún clan ni en una reserva pendiente; el nombre y la sigla
  tienen el formato de §4.1. La unicidad la decide la base: un nombre o una sigla ocupados vuelven como rechazo. El
  fundador es el líder.
- `clan.invite { playerId, invited }`: sólo el líder; con `invited: true` invita a un jugador existente, distinto, sin
  clan y sin otra invitación de este clan, si el clan no está lleno contando las invitaciones vivas y si no rompe la
  regla de bandos (§4.4). Con `invited: false` retira la invitación. Una invitación caduca a las
  `EO_CLAN_INVITE_HOURS` (24, Q3).
- `clan.respond { clanId, accept }`: el invitado acepta o rechaza una invitación viva. Al aceptar se comprueban otra vez
  el tope, la regla de bandos y que no esté ya en otro clan; las demás invitaciones que tuviera se borran. Desde la
  confirmación es miembro y está en paz con todos (§4.3).

### 4.3 La paz del clan

`State.AtPeace` (hoy: un tratado vigente o los dos de `ORDER`, [ADR-039](../decisions/ADR-039-faction-peace-and-affinity.md))
pasa a responder «sí» también cuando **los dos son miembros del mismo clan** o cuando entre ellos queda **paz
residual** (§4.5). Así las cuatro acciones hostiles (`unit.attack` —también contra una caravana—, `unit.pillage`,
`unit.demolish` y `unit.claim` sobre un territorio con dueño) y la respuesta automática quedan cubiertas por la regla
que ya existe, con su propio motivo en el rechazo («sois del mismo clan»). Al confirmarse la entrada de un miembro, los
enfrentamientos, saqueos, derribos y estandartes entre él y sus nuevos compañeros terminan como cuando nace un tratado
(`RN-COMBAT-008`, `RN-COMBAT-025` y la conquista).

La paz del clan no da guarnición, comercio ni almacén compartidos (Q12): eso sigue en los tratados, que los miembros
pueden firmar entre sí como hasta ahora.

### 4.4 Clanes y bandos

Un clan no puede tener a la vez un miembro de `ORDER` y uno de `CHAOS`, **contando las entradas pendientes** en un
bando (Q7). La paz del clan entre la Orden y el Caos contradiría `RN-FAC-005`, que no les deja firmar la paz. En
concreto:

- no se invita ni se acepta si con ello el clan quedaría con los dos bandos;
- un miembro no puede pedir `faction.join` al bando contrario del de otro miembro, ni del de una entrada pendiente de
  otro miembro (`INVALID_TARGET`, «tu clan tiene miembros del Caos»);
- `NEUTRAL` cabe en cualquier clan.

El clan no tiene bando propio: su bando, si se quiere hablar de uno, es el de sus miembros que lo tienen.

### 4.5 Salir, expulsar, liderazgo

- `clan.leave {}`: cualquier miembro sale. Si era el líder, el liderazgo pasa en el mismo tick al miembro más antiguo
  (por fecha de entrada y, a igualdad, por el `id` del jugador). Si era el último, el clan se disuelve (Q4).
- `clan.expel { playerId }`: sólo el líder, sobre otro miembro.

**Precisión (2026-11-16, [clan-governance.md](clan-governance.md)).** Desde E20, invitar, retirar y expulsar a un
miembro raso los dan también los oficiales; expulsar a un oficial, sólo el líder. Al salir el líder, hereda el oficial
más antiguo antes que el miembro más antiguo, y un líder que lleva 7 días sin jugar pierde el liderazgo.

Al salir o ser expulsado, el jugador deja de ser miembro **en el acto**: pierde el canal, la sigla y el panel, y puede
entrar en otro clan. La paz, en cambio, no termina en el acto: durante `EO_CLAN_PEACE_GRACE_SECONDS` (900, Q6) sigue en
paz con quienes eran sus compañeros en ese momento, igual que un tratado roto sigue en vigor durante su preaviso
(`RN-DIPLO-004`). Sin esa paz residual, salir sería una manera de atacar por sorpresa a un compañero que tienes al
lado, y expulsar, la de atacar por sorpresa al expulsado. Un reinicio no la pierde: se deriva de la hora de salida
guardada.

### 4.6 El canal del clan

El canal es una conversación del clan entero, con las reglas de los mensajes de E16 (Q9): texto plano de 1 a
`EO_MESSAGE_MAX_CHARS` caracteres, guardado antes de entregarse, entregado en vivo a las sesiones conectadas de los
miembros, visible los últimos `EO_MESSAGE_MAX_DAYS` días y paginado de `EO_MESSAGE_PAGE_SIZE` en
`EO_MESSAGE_PAGE_SIZE`, con lo leído por miembro. El ritmo es el mismo cubo que los mensajes directos: un mensaje del
canal cuenta para los `EO_MESSAGE_RATE_PER_MINUTE` del remitente.

Un miembro lee **lo escrito desde que entró**, no lo anterior; al salir pierde el acceso a todo. El canal lo atiende la
conexión, como los mensajes directos, y autoriza contra la base (§7): sólo escribe y lee quien es miembro confirmado
en el momento de la transacción.

### 4.7 Reglas

Dominio: `RN-CLAN`.

| ID | Regla |
|---|---|
| `RN-CLAN-001` | Un jugador es miembro de como mucho un clan. Todo clan vivo tiene entre 1 y `EO_CLAN_MAX_MEMBERS` miembros y exactamente un líder entre ellos (Q4, Q5). |
| `RN-CLAN-002` | El nombre y la sigla de §4.1 son únicos entre los clanes vivos sin distinguir mayúsculas y no cambian (Q2). |
| `RN-CLAN-003` | Se entra sólo aceptando una invitación viva del líder; una invitación caduca a las `EO_CLAN_INVITE_HOURS` y el líder puede retirarla (Q3). |
| `RN-CLAN-004` | **La paz del clan.** Entre dos miembros del mismo clan ninguna acción hostil se acepta ni sigue en curso: `INVALID_TARGET` al darla, y fin del enfrentamiento, saqueo, derribo o estandarte en el tick en que se confirma la entrada (Q1). |
| `RN-CLAN-005` | **Paz residual.** Quien sale o es expulsado sigue en paz con quienes eran sus compañeros durante `EO_CLAN_PEACE_GRACE_SECONDS` desde la salida; el resto de la pertenencia termina en el acto (Q6). **Precisión (2026-11-16, [clan-alliances.md](clan-alliances.md)):** con E21, también con los miembros de los clanes aliados del suyo en el instante de la salida (`RN-CLANALLY-006`). |
| `RN-CLAN-006` | Un clan nunca tiene a la vez un miembro de `ORDER` y uno de `CHAOS`, contando las entradas pendientes en un bando (Q7). |
| `RN-CLAN-007` | Sólo el líder invita, retira invitaciones y expulsa. Si el líder sale, el liderazgo pasa al miembro más antiguo; si sale el último, el clan se disuelve (Q4). **Precisión (2026-11-16):** con E20, también los oficiales invitan, retiran y expulsan a miembros rasos, y el liderazgo pasa antes al oficial más antiguo ([clan-governance.md](clan-governance.md) `RN-CLANGOV-002` y `RN-CLANGOV-004`). |
| `RN-CLAN-008` | La sigla del clan de un jugador y la lista de miembros de un clan son públicas; el canal y las invitaciones, sólo de sus implicados (Q8). **Precisión (2026-11-16, [clan-alliances.md](clan-alliances.md)):** con E21, también son públicas las alianzas activas de un clan, con su preaviso; las ofertas, sólo para el líder y los oficiales de los dos clanes (`RN-CLANALLY-007`). **Precisión (2026-11-17, [clan-wars.md](clan-wars.md)):** con E24, también son públicas las guerras abiertas de un clan, con su marcador y sus plazos, y su historial de victorias, derrotas y empates; las ofertas de paz, sólo para el líder y los oficiales de los dos clanes (`RN-CLANWAR-009`). |
| `RN-CLAN-009` | **Canal.** Escribe y lee el canal sólo un miembro confirmado, y lee sólo lo escrito desde su entrada; los límites, la forma del texto y la entrega son los de `RN-MSG-003`, `RN-MSG-005` y `RN-MSG-006` (Q9). |
| `RN-CLAN-010` | El clan no da guarnición, comercio ni almacén: los tratados siguen siendo la única vía (Q12). |

### 4.8 Qué ve el jugador

- **HUD**: un botón «Clan» con la sigla del propio clan, o «Clan» a secas, y un contador de invitaciones y mensajes
  del canal sin leer.
- **Panel del clan**, sin clan: «Fundar un clan» (nombre y sigla) y las invitaciones recibidas con «Aceptar» y
  «Rechazar». Con clan: la lista de miembros (ciudad, bando y quién es el líder), el canal con su historial y su
  cuadro de texto, «Salir del clan» con la advertencia de la paz residual y, para el líder, las invitaciones
  enviadas con «Retirar» y «Expulsar» junto a cada miembro.
- **Ciudad ajena**: su sigla en la etiqueta del mapa y en la barra, con «Ver clan» (la lista pública de miembros) y,
  para un líder, «Invitar al clan» si se puede (el servidor rechaza igual si llega).
- **Ataque**: el clic de ataque sobre una unidad de un compañero no sale y pone en el pie el motivo («Sois del mismo
  clan»), como entre dos de la Orden.
- Una invitación que llega se ve en vivo (el contador y un aviso breve); una expulsión, como un aviso de E11 y en el
  registro (Q10).
- Un rechazo del servidor sale con su motivo («Ese nombre ya existe», «El clan está lleno», «Tu clan tiene miembros
  del Caos»).

## 5. Errores

Sólo códigos que ya existen en el catálogo (canon §16).

| Código | Condición |
|---|---|
| `INVALID_MESSAGE` | Nombre o sigla fuera del formato de §4.1; un `playerId` o `clanId` mal formado; el texto del canal como en `message.send`; `beforeId` o `upToId` que no son enteros positivos. |
| `INVALID_TARGET` | Fundar o aceptar estando ya en un clan; nombre o sigla ocupados; invitar, retirar o expulsar sin ser el líder; invitar a un jugador inexistente, a uno mismo, a quien ya está en un clan o ya invitado; clan lleno; romper la regla de bandos (`RN-CLAN-006`), también desde `faction.join`; responder a una invitación que no existe o caducó; expulsar a quien no es miembro o a uno mismo; salir sin clan; escribir o leer el canal sin ser miembro; una acción hostil entre dos jugadores en paz de clan (`RN-CLAN-004`, `RN-CLAN-005`). El mensaje dice cuál. |
| `RATE_LIMITED` | El ritmo de `RN-MSG-005` en el canal, como en E16: `system.error` con el `requestId` y sin cerrar la conexión. |
| `INTERNAL_ERROR` | PostgreSQL no responde a tiempo al escribir o leer el canal o la lista de miembros. |

## 6. Eventos y contratos de red

**Cliente → servidor.** Nueve comandos nuevos y estrictos (R-VER-3). El catálogo pasa de 30 a 39.

- De la **simulación** (entran en `CommandTypes` y en `actions.go` como **no hostiles**; cada uno con su trabajo de la
  cola, con la clave del jugador o, si toca a dos, con la de los dos, que entran en `simulation.JobNames`):
  `clan.create { name, tag }`, `clan.invite { playerId, invited }`, `clan.respond { clanId, accept }`,
  `clan.leave {}` y `clan.expel { playerId }`.
- De **sesión** (se suman a los mensajes de sesión de `actions.go`, no crean trabajos): `clan.lookup { clanId }`,
  `clan.chat.send { body }`, `clan.chat.fetch { beforeId? }` y `clan.chat.read { upToId }`.

**Servidor → cliente.** Cuatro tipos nuevos, cada uno con su e2e en el mismo commit (el `TestMain` de
`schema_e2e_test.go`). El catálogo pasa de 27 a 31.

- `clan.update { clan, invitations[] }`, a todas las sesiones de un jugador cuando cambia su clan o sus invitaciones
  (y con el `requestId` a la que dio la orden): `clan` es la vista del propio clan (`{ id, name, tag, leaderId,
  members[], sentInvitations[] }`, con las invitaciones enviadas sólo para el líder —desde E20 también para los oficiales,
  [clan-governance.md](clan-governance.md)—) o `null`; `invitations[]`, las
  recibidas vivas (`{ clanId, name, tag, expiresAtMs }`). Llega también al conectar, tras el snapshot.
- `clan.info { clanId, name, tag, members[] }`, sólo a la sesión que hizo `clan.lookup`: la vista pública.
- `clan.chat.new { message }`, a las sesiones de todos los miembros; la del remitente que lo envió, con su
  `requestId`.
- `clan.chat.page { messages[], readUpToId, nextBeforeId }`, sólo a la sesión que lo pidió.

Cada miembro de una vista: `{ playerId, cityId, cityName, faction, role, joinedAtMs }`. `CityView` gana `clan`
(`{ id, tag }` o `null`), obligatorio, que llega por la vista y los deltas de ciudad que ya existen: una entrada, una
salida o una disolución vuelven a difundir la ciudad de los afectados. El esquema Zod lo fija la implementación.

**Eventos de dominio** en `world_events`: `ClanFounded`, `ClanJoined`, `ClanLeft` y `ClanMemberExpelled`. Sólo
`ClanMemberExpelled` implica a otro jugador: lleva `counterpart_player_id` (el líder) y `atMs`
([ADR-028](../decisions/ADR-028-event-log-from-world-events.md)), entra en `eventlog.EventTypes` y en `Classify` para
el expulsado y para el líder (`RN-LOG-001`), y en `eventlog.NoticeKinds` para avisarse en vivo (Q10). Los mensajes del
canal no son sucesos del mundo y no van a `world_events`, como los de E16.

**Métricas.** `eo_clan_changes_total{kind}` (con `kind` en `founded`, `joined`, `left`, `expelled` y `dissolved`,
series a cero) y `eo_clan_messages_sent_total`, a [../operations/monitoring.md](../operations/monitoring.md) §2.
Sin el texto, los nombres ni los jugadores en las etiquetas.

**Configuración.** `EO_CLAN_MAX_MEMBERS` (20), `EO_CLAN_INVITE_HOURS` (24) y `EO_CLAN_PEACE_GRACE_SECONDS` (900). El
canal usa las `EO_MESSAGE_*` de E16.

## 7. Persistencia

| Pregunta (canon §12) | Respuesta |
|---|---|
| Autoritativo en RAM | La pertenencia confirmada de cada jugador (clan y fecha de entrada), el líder de cada clan, las invitaciones vivas y las salidas de los últimos `EO_CLAN_PEACE_GRACE_SECONDS`; se cargan con el mundo. La paz del clan y la residual, que se derivan en cada consulta |
| Write-through | Cada orden de §4.2 y §4.5, en su trabajo de la cola; los mensajes del canal y lo leído, desde la conexión, como en E16 |
| Dirty-flag | Nada |
| Reconstruible / no persistido | Quién está en paz por su clan; las invitaciones caducadas, que se descartan al leerlas |

**Por qué hace falta un ADR ([ADR-040](../decisions/ADR-040-clans-membership-and-peace.md)).** Tres decisiones estructurales, más la regla de bandos en los dos sentidos (ADR-040 §4):

1. **La pertenencia es estado de la simulación.** Decide la paz, y la paz se consulta dentro del tick en cada golpe:
   las órdenes que la cambian pasan por el tick, en dos fases como `faction.join`. Alternativa descartada: llevar los
   clanes como los mensajes, fuera del tick, y que la simulación los lea de un caché; tendría dos escritores de la
   misma verdad y una paz que podría llegar al tick después del golpe.
2. **El clan es la tercera fuente de paz, con paz residual derivada.** `AtPeace` pasa a ser «tratado, o los dos de la
   Orden, o mismo clan, o paz residual». Alternativa descartada: crear un pacto de no agresión implícito por cada
   pareja de miembros, que crece con el cuadrado del clan, llenaría la diplomacia de filas que nadie firmó y obligaría
   a romper diecinueve tratados cada vez que alguien sale. La paz residual no se guarda: se deriva de la hora de
   salida guardada en la historia de pertenencia y de la de entrada del otro.
3. **El canal es un subsistema de sesión autorizado contra la base.** Lo atiende la conexión, como los mensajes de
   [ADR-038](../decisions/ADR-038-player-messages-session-subsystem.md), y en la transacción del envío comprueba la
   pertenencia en `clan_members` con el candado del clan, el mismo que toman la entrada y la salida (ADR-040 §5), no
   en la RAM de la simulación, que la conexión no puede leer sin cruzar el escritor único. Quien acaba de salir no
   escribe ni un mensaje más.

**Migración `000020`.** Cinco tablas nuevas, que se suman al `TRUNCATE` de `testenv_integration_test.go` y a la
clasificación de [../database/schema.md](../database/schema.md) §1, y sustituyen el diseño previsto de §17.3:

- `clans` (`id` identidad, `name`, `tag`, `leader_player_id` con FK a `players`, `founded_at`, `dissolved_at`), con
  índices únicos sobre `lower(name)` y sobre `tag` sólo para los clanes vivos (`dissolved_at IS NULL`) y `CHECK` del
  formato.
- `clan_members` (`clan_id`, `player_id`, `joined_at`, `first_message_id` —el mayor `id` del canal al entrar, del
  que sale «lo escrito desde que entró»—, `left_at`, `left_reason` en `LEFT`, `EXPELLED` o `DISSOLVED`), una fila por
  estancia: la historia de pertenencia que pedía §17.3 y de la que sale la paz residual. Un índice único
  parcial impide dos estancias abiertas (`left_at IS NULL`) de un jugador, y un `CHECK` el par `left_at`/`left_reason`.
- `clan_invites` (`clan_id`, `player_id`, `invited_by`, `created_at`, `expires_at`), con PK de los dos primeros.
- `clan_messages` (`id` identidad, `clan_id`, `sender_player_id`, `body` con el `CHECK` de longitud de E16,
  `request_id` con `UNIQUE (sender_player_id, request_id)`, `sent_at`), con un índice `(clan_id, id DESC)`.
- `clan_message_reads` (`player_id`, `clan_id`, `read_up_to_id`), con PK de los dos.

Un trigger en los dos sentidos —al abrir una estancia en `clan_members` y al apuntar una entrada o cambiar la facción en
`players`— rechaza que un clan quede con un miembro (o una entrada) de `ORDER` y uno de `CHAOS` (`RN-CLAN-006`), como
red de seguridad bajo la comprobación del tick (ADR-040 §4)
([INV-PERSIST-005](../invariants/persistence.md#inv-persist-005)). Que el líder sea un miembro abierto lo garantiza la
transacción del trabajo que cambia el líder y lo comprueba un test, no la base. `territory_control` no cambia: el
dueño `CLAN` sigue sin usarse (Q11). La siguiente migración libre pasará a ser `000021`. **Precisión (2026-11-16):**
E19 usa el dueño `CLAN` con la migración `000021` ([clan-territories.md](clan-territories.md) §7).

## 8. Invariantes

**Cita** [INV-SEC-001](../invariants/security.md#inv-sec-001) y [INV-SEC-004](../invariants/security.md#inv-sec-004)
(la pertenencia la cambia el jugador de la sesión y la decide el servidor; el líder lo es por la base, no por un campo
del comando), [INV-SEC-005](../invariants/security.md#inv-sec-005) (los límites del mensaje entrante),
[INV-SEC-007](../invariants/security.md#inv-sec-007) (un `requestId` repetido no funda dos clanes ni guarda dos
mensajes), [INV-SEC-010](../invariants/security.md#inv-sec-010) (los mensajes directos siguen siendo sólo de sus dos
jugadores), [INV-COMBAT-002](../invariants/combat.md#inv-combat-002) e
[INV-COMBAT-006](../invariants/combat.md#inv-combat-006) (los demás motivos que impiden un golpe, la paz de la Orden
incluida, siguen igual), [INV-DIPLO-003](../invariants/diplomacy.md#inv-diplo-003) e
[INV-PLAYER-013](../invariants/player.md#inv-player-013) (la regla de bandos del clan se suma a las de E17),
[INV-PERSIST-003](../invariants/persistence.md#inv-persist-003) (nada del clan vive sólo en Redis) e
[INV-PERSIST-005](../invariants/persistence.md#inv-persist-005) (la migración `000020`).

**Asigna** cuatro invariantes nuevos, registrados en [../invariants/clans.md](../invariants/clans.md), una familia
nueva, y en [../invariants/security.md](../invariants/security.md):

- [INV-CLAN-001](../invariants/clans.md#inv-clan-001): un jugador tiene como mucho una estancia abierta en un clan, y
  todo clan vivo tiene exactamente un líder, que es uno de sus miembros abiertos (el índice parcial de `000020` y un
  test del cambio de líder).
- [INV-CLAN-002](../invariants/clans.md#inv-clan-002): ningún clan tiene a la vez un miembro de `ORDER` y uno de
  `CHAOS`, contando las entradas pendientes en un bando (los triggers de `000020` y un test por cada camino: invitar,
  aceptar y `faction.join`).
- [INV-CLAN-003](../invariants/clans.md#inv-clan-003): ningún golpe, robo, derribo ni avance de estandarte se aplica en
  un tick entre dos jugadores del mismo clan, ni entre un jugador y quien fue su compañero hasta hace menos de
  `EO_CLAN_PEACE_GRACE_SECONDS`.
- [INV-SEC-012](../invariants/security.md#inv-sec-012): una sesión sólo recibe mensajes del canal del clan del que su
  jugador es miembro abierto, y sólo los posteriores a su entrada; todo mensaje del canal lleva como remitente al
  jugador de la sesión.

## 9. Tests esperados

| Nivel | Qué |
|---|---|
| unit | El formato del nombre y la sigla; cada fila de §5 para cada comando; el tope contando invitaciones; la regla de bandos con entradas pendientes; `AtPeace` con clan, con paz residual (dentro y fuera del plazo, con quien entró después de la salida) y sin ella; el paso del liderazgo al más antiguo con el desempate; la disolución |
| simulation | Dos miembros que se enfrentaban, saqueaban, derribaban o plantaban estandarte y paran en el tick en que se confirma la entrada; un ex miembro que no puede atacar hasta que vence su paz residual y sí en el tick siguiente; un `faction.join` rechazado por el bando de un compañero; el catálogo de comandos (39) y de trabajos |
| integration | La migración: los índices únicos de nombre y sigla (también tras disolver), la estancia abierta única, el trigger de bandos; cada trabajo de la cola, con un fallo inyectado que no deja nada a medias; el canal: guardar y leer por el índice, la unicidad de `(remitente, request_id)`, que un no miembro no escribe ni lee y que un miembro no lee lo anterior a su entrada; dos envíos que confirman en orden inverso y lo leído que no se salta el primero |
| recovery | La pertenencia, el líder, las invitaciones vivas y la paz residual sobreviven a un reinicio; una paz residual que venció con el servidor parado ya no cuenta en el primer tick |
| contract / e2e | Los nueve comandos y los cuatro tipos contra el JSON Schema; `CityView` con `clan`; de punta a punta por WebSocket: fundar, invitar, aceptar (con `clan.update` a los dos), escribir en el canal con dos miembros conectados, expulsar (con su aviso) y un `unit.attack` entre compañeros rechazado con `INVALID_TARGET` |
| cliente | El botón y el panel del clan con y sin clan, el canal con paginación y contador, «Invitar al clan» y «Ver clan» en la barra de una ciudad ajena, la sigla en el mapa, el clic de ataque que no sale entre compañeros; `scripts/smoke.mjs --clan` con **cuentas de humo propias** (nunca `smoke_runner` ni `smoke_victim`, que siguen sin clan para el saqueo, el asedio y la diplomacia): una funda, invita a otra, la otra acepta, escriben en el canal, la invitación de `smoke_caos` a un clan con `smoke_orden` se rechaza, y la expulsión se avisa; en una pasada siguiente, con el clan ya fundado, sólo comprueba lo que no cambia el estado |

**Precisiones (2026-11-15), al implementar.** Ninguna cambia una decisión de §10; las que concretan un detalle de
[ADR-040](../decisions/ADR-040-clans-membership-and-peace.md) se anotan aquí, porque un ADR aceptado no se edita.

- **`left_reason`.** Sólo `LEFT` y `EXPELLED`: el último que sale lo hace con `LEFT` y la disolución la marca
  `clans.dissolved_at`. No hay evento `ClanDissolved`: es el `ClanLeft` del último, con `dissolved: true` en su
  payload; la métrica sí la cuenta aparte (`dissolved`).
- **El nombre en el esquema.** El JSON Schema acota la longitud (3–24) y la sigla con su patrón; qué caracteres admite
  el nombre lo comprueba el dominio (`clan.ValidateName`), que responde `INVALID_MESSAGE`. El cliente aplica las mismas
  reglas antes de enviar (`nameProblem`, `tagProblem`).
- **Qué responde cada orden.** La respuesta es `clan.update` con el `requestId` a quien dio la orden; el `city.update`
  con el `clan` de la ciudad sale sin él. El `clan.update` de la conexión sale tras el primer `world.snapshot`, no
  tras un `session.view`. Una invitación que caduca no se avisa: el cliente la oculta por `expiresAtMs` y la fase 5 la
  olvida.
- **Retirar y rechazar son optimistas**, como salir: la RAM borra la invitación en el acto y un trabajo la borra de la
  base. Si esa escritura se pierde del todo, la invitación vuelve tras un reinicio hasta caducar; no da paz ni acceso a
  nada, porque la entrada vuelve a comprobarla en la base.
- **La última salida espera a las entradas en vuelo.** Si el último miembro pide salir mientras una entrada al clan
  espera su transacción (milisegundos), se rechaza con `INVALID_TARGET` («alguien está entrando en el clan; inténtalo
  en un momento»): disolver primero dejaría esa entrada sobre un clan disuelto.
- **Fundar borra las invitaciones recibidas** del fundador, como entrar.
- **Los e2e** corren con almacenes en memoria, como los de los mensajes: prueban la conexión (quién recibe qué, el
  orden de las respuestas, los rechazos y el aviso). La base, el candado del clan, los triggers y la idempotencia son
  de la integración, y el recorrido entero contra el servidor vivo es `scripts/smoke.mjs --clan`.

## 10. Decisiones de producto (2026-11-15)

Preguntas que la propuesta dejó abiertas; producto aceptó la recomendación de cada una. Son decisiones de producto,
no de ingeniería: cambiarlas es cambiar la spec.

| # | Pregunta | Decisión |
|---|---|---|
| Q1 | ¿Qué da un clan en este slice? | **Paz entre sus miembros, un canal propio y una sigla que se ve en el mapa.** La paz es lo que hace que el clan cambie el juego y no sea sólo un chat: cinco jugadores en paz entre sí sin diez pactos. Se apoya en `AtPeace`, el punto que ya usan los tratados y la Orden, así que no abre una regla nueva en cada acción hostil. Alternativas: **sólo social** (canal y sigla; la paz, con tratados de dos en dos), más pequeño pero un clan que no protege; **paz y guarnición**, que adelanta Q12. |
| Q2 | ¿Quién funda, cómo se llama y cuánto cuesta? | **Cualquier jugador sin clan, gratis**, con un nombre de 3 a 24 caracteres y una sigla de 2 a 4 (`A`–`Z`, `0`–`9`), únicos y para siempre. Sin jugadores no hay ocupación de nombres que frenar, y un coste añade balance sin datos. Que no cambien evita que un clan se haga pasar por otro de un día para otro. Alternativas: **con coste** (p. ej. 200 de madera) como freno a los clanes de un día; **renombrar** con un plazo de espera. |
| Q3 | ¿Cómo se entra? | **Sólo por invitación del líder, que el invitado acepta**, y la invitación caduca a las 24 horas (como las ofertas de comercio). Entrar da paz en el acto, como aceptar un pacto: tienen que querer las dos partes. Alternativas: **también por solicitud** del aspirante, que el líder acepta (más cómodo, un comando y una lista más); **clanes abiertos**, que dejarían entrar en paz a quien va perdiendo contra un miembro. |
| Q4 | ¿Qué rangos hay y qué pasa si el líder se va? | **Un líder y miembros. Sólo el líder invita y expulsa; si sale, el liderazgo pasa al miembro más antiguo; si sale el último, el clan se disuelve.** Es lo mínimo que funciona y no deja clanes sin líder. Alternativas: **oficiales** que también invitan; **traspaso explícito** (`clan.transfer`) y que el líder no pueda salir sin traspasar; **disolver** con una orden propia. Ninguna resuelve un líder que deja de jugar: eso espera a que haya datos de inactividad. **Precisión (2026-11-16):** E20 trae los oficiales, el traspaso y la sucesión por ausencia ([clan-governance.md](clan-governance.md)): los oficiales también invitan y expulsan a los miembros rasos, el líder cede el liderazgo con `clan.transfer`, al salir hereda el oficial más antiguo y, si el líder lleva 7 días sin jugar, el liderazgo pasa a quien siga jugando. |
| Q5 | ¿Cuántos miembros? | **Hasta 20** (`EO_CLAN_MAX_MEMBERS`). Da para un grupo que se conoce y no para un bando paralelo: los bloques grandes ya son la Orden y el Caos. Es un número de partida, sin datos. Alternativas: **10**, más cerrado; **50**, que con la paz del clan haría de un clan un bando sin reglas de bando. |
| Q6 | ¿Qué pasa con la paz al salir o ser expulsado? | **15 minutos de paz residual con quienes eran tus compañeros** (`EO_CLAN_PEACE_GRACE_SECONDS` = 900, los mismos que el preaviso de ruptura de un tratado y la entrada en un bando); el canal, la sigla y el panel se pierden en el acto. Sin ella, salir sería la manera de atacar por sorpresa a quien tienes al lado, y expulsar, la de atacar al expulsado. Alternativas: **sin paz residual** (salir y atacar en el mismo tick); **toda la salida con preaviso** (el que se va sigue leyendo el canal 15 minutos más, y el expulsado también). |
| Q7 | ¿Cómo encajan los clanes con los bandos? | **Un clan no mezcla la Orden y el Caos**, contando las entradas pendientes: no se invita ni se acepta, y un miembro no puede entrar en el bando contrario al de un compañero. `NEUTRAL` cabe en cualquiera. Si se mezclaran, la paz del clan daría a la Orden y al Caos la paz que E17 les prohíbe. Alternativas: **el clan elige un bando** (el `faction_id` que preveía el esquema) y todos tienen que ser de él o `NEUTRAL`, que añade una elección y otra regla; **permitir la mezcla** y que la paz del clan gane, que deshace la Q5 de E17. |
| Q8 | ¿Qué es público? | **La sigla del clan en la ciudad y la lista de miembros, por su ciudad, para cualquiera; el canal y las invitaciones, sólo para los implicados.** Las reglas de agresión tienen que ser predecibles, como el bando en E17: si no sabes quién está en paz con quién, no sabes a quién atacar. Alternativa: **miembros ocultos** (sólo la sigla), que esconde quién protege a quién. |
| Q9 | ¿Cómo es el canal? | **Las reglas de los mensajes de E16**: 500 caracteres, 30 días, el mismo ritmo de 10 por minuto compartido con los mensajes directos, y lo leído por miembro. **Un miembro lee lo escrito desde que entró**, y al salir lo pierde todo. Expulsar es la única moderación. Alternativas: **ver los últimos 30 días al entrar**, más útil para ponerse al día, pero cualquiera que entre se lleva la estrategia del clan; **un límite propio** del canal, que sólo complica sin datos. |
| Q10 | ¿Qué entra en el registro y en los avisos? | **La expulsión, para el expulsado (aviso en vivo y registro) y para el líder (registro). Las invitaciones llegan en vivo al panel con su contador, sin registro. Entrar o salir por voluntad propia no entra en el registro de nadie**: se ve en el panel del clan. El registro cuenta lo que otros te hicieron y lo que hiciste tú a otros (`RN-LOG-001`), y una expulsión es justo eso. Alternativa: **avisar a todo el clan** de cada entrada y salida, que llena los avisos de la vida social de un grupo de 20. |
| Q11 | ¿Territorio del clan o tierra afín del clan? | **No en este slice.** El dueño `CLAN` del territorio sigue sin usarse y la tierra afín sigue siendo la del propio jugador o la de su bando. Un territorio de clan pide decidir quién lo conquista, quién lo pierde al salir y qué pasa al disolver: es otro slice. Alternativa: **la tierra de un compañero cuenta como afín**, que es barato de hacer, pero mezcla el bono de E17 con el clan sin datos de ninguno de los dos. **Precisión (2026-11-16):** el territorio de clan lo trae E19 ([clan-territories.md](clan-territories.md), [ADR-041](../decisions/ADR-041-clan-territories.md)): un miembro lo reclama para el clan, es tierra afín para sus miembros y queda libre al disolverse; la tierra de un compañero sigue sin ser afín. |
| Q12 | ¿Da el clan guarnición, comercio o almacén compartidos? | **No: los tratados siguen siendo la única vía**, y los miembros los firman entre sí como hasta ahora. El comercio pide un `TRADE` para que un trato no salga de la nada, y la guarnición una alianza. Alternativa: **comercio sin tratado entre miembros**, que ahorra un paso pero crea dos maneras de habilitar lo mismo. |

## 11. Documentos relacionados

- [diplomacy.md](diplomacy.md) (los tratados, el preaviso de ruptura y la otra fuente de paz) y
  [factions.md](factions.md) (la paz de la Orden, la entrada en dos fases y la regla de bandos).
- [messages.md](messages.md) (las reglas del texto, el ritmo, la entrega y lo leído que el canal reutiliza).
- [combat-raid.md](combat-raid.md), [combat-response.md](combat-response.md), [combat-pillage.md](combat-pillage.md),
  [combat-siege.md](combat-siege.md), [territory-conquest.md](territory-conquest.md) y [caravans.md](caravans.md)
  (las acciones hostiles que la paz del clan prohíbe).
- [event-log.md](event-log.md) y [live-notices.md](live-notices.md) (la expulsión en el registro y en los avisos).
- [../database/schema.md](../database/schema.md) §17.3 (el diseño previsto que esta spec sustituye) y
  [territory.md](territory.md) (el dueño `CLAN`, que sigue fuera).
- [../product/mvp-scope.md](../product/mvp-scope.md) (`OUT-09`, `OUT-13`), [../roadmap/roadmap.md](../roadmap/roadmap.md)
  (la fase Clans) y [../roadmap/backlog.md](../roadmap/backlog.md) (`Q-16`).
