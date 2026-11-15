# ADR-040: La pertenencia a un clan es estado de la simulación, es la tercera fuente de paz y su canal se autoriza contra la base

Propósito: fijar cómo el slice de clanes ([SPEC-CLANS](../specs/clans.md)) da paz a los miembros de un clan sin una
fila por pareja, sin que salir o expulsar sirvan para atacar por sorpresa, sin que un clan junte a la Orden y al Caos,
y con un canal de grupo que reutiliza los mensajes de E16 sin que la conexión lea la RAM de la simulación.

| Campo | Valor |
|---|---|
| **Estado** | **Aceptado** |
| **Fecha** | **2026-11-15** |
| Ámbito | `internal/domain/clan` (el nombre, la sigla, las reglas de pertenencia y de liderazgo), `internal/domain/city` (la sigla en la ciudad), `internal/game/simulation` (los cinco comandos, `AtPeace`, la paz residual, la regla de bandos también en `faction.join`), `internal/persistence/postgres` (la carga, los trabajos de pertenencia, el canal), `internal/websocket` (los cuatro comandos de sesión), `internal/domain/eventlog`, `internal/game/loadcheck`, migración `000020`, `internal/observability`, `packages/protocol`, cliente |
| Índice | [README.md](README.md) |
| Relacionados | [ADR-016](ADR-016-foreign-territory-actions.md) (qué es una acción hostil), [ADR-022](ADR-022-durable-write-retry-and-loop-inbox.md) (ninguna escritura se pierde en silencio), [ADR-027](ADR-027-hot-treaties.md) (la paz evaluada por fecha y el preaviso), [ADR-028](ADR-028-event-log-from-world-events.md) (el registro desde `world_events`), [ADR-032](ADR-032-trade-offers-and-two-key-jobs.md) (las claves de la cola), [ADR-038](ADR-038-player-messages-session-subsystem.md) (los mensajes como subsistema de sesión, el candado y lo leído), [ADR-039](ADR-039-faction-peace-and-affinity.md) (la facción como segunda fuente de paz y la entrada en dos fases) |

---

## Contexto

Los clanes existen desde M2 sólo como diseño: [schema.md](../database/schema.md) §17.3 preveía `clans` y una tabla
puente `clan_members`, y `territory_control` admite un dueño `CLAN` que nadie usa. Producto aceptó el 2026-11-15 las
doce recomendaciones de la spec: el clan da paz entre sus miembros, un canal y una sigla pública; se entra por
invitación; un líder y miembros, hasta 20; 15 minutos de paz residual al salir o ser expulsado; un clan no mezcla la
Orden y el Caos; sin territorio, tierra afín, guarnición ni comercio de clan.

Piezas que ya existen y que el slice usa:

- **Un solo punto de paz.** `State.AtPeace(x, y, now)` responde por los tratados (también en su preaviso) y por la
  Orden ([ADR-039](ADR-039-faction-peace-and-affinity.md) §2). Lo consultan las cuatro acciones hostiles al dar la
  orden y el combate, el saqueo, el asedio y la conquista en cada tick, que terminan lo que esté en curso cuando la paz
  aparece (`RN-COMBAT-008`, `RN-COMBAT-025`). Un predicado aparte da el motivo del rechazo (`OrderPeace`).
- **El encargo en dos fases.** `faction.join` reserva en la RAM, encola su escritura con la clave `player:<id>` y se
  confirma o se deshace al volver por el buzón (ADR-039 §3).
- **Los mensajes de sesión.** `MessageRepo` guarda antes de entregar, serializa una conversación con un candado de
  transacción de dos enteros y entrega con `Hub.SendToPlayer` tras el `COMMIT`; los límites de ritmo viven en la RAM
  del `Server` ([ADR-038](ADR-038-player-messages-session-subsystem.md)).
- **El bando en la ciudad.** `city.City.Faction` y `FactionJoin`, cargados de `players` (ADR-039 §1).

## Decisión

### 1. La pertenencia vive en la simulación

`State` gana un `clanState`: los clanes vivos (`id`, nombre, sigla, líder y sus miembros con la fecha de entrada), el
clan de cada jugador, las invitaciones vivas con su caducidad y las **estancias cerradas recientes** (clan, jugador,
entrada y salida) de los últimos `EO_CLAN_PEACE_GRACE_SECONDS`. Se carga con el mundo desde `clan_members`,
`clans` y `clan_invites`, y la validación de carga exige una estancia abierta como mucho por jugador, un líder que sea
miembro abierto en cada clan vivo y ningún clan con los dos bandos. `city.City` gana `Clan` (`id` y sigla, o nada)
para la vista, como la facción.

Las órdenes que cambian la pertenencia (`clan.create`, `clan.invite`, `clan.respond`, `clan.leave` y `clan.expel`) son
comandos de la simulación, no hostiles: la pertenencia decide la paz, y sólo el tick puede decidir en qué tick empieza
y terminan los enfrentamientos.

### 2. Entrar va en dos fases; salir es optimista

- **Fundar, invitar y entrar** van en dos fases, como `faction.join`: el tick valida y **reserva** (un clan en
  fundación por jugador, una invitación o una entrada en vuelo, que cuentan para el tope y para la regla de bandos),
  encola el trabajo y, al volver por el buzón, confirma —el clan existe, la invitación se ve, el jugador es miembro y
  está en paz— o deshace y responde el código. Fundar va con la clave `player:<id>` (el clan aún no tiene `id`); la
  unicidad del nombre y de la sigla la decide la base y vuelve como `INVALID_TARGET`. Invitar, retirar y entrar van
  con la clave `clan:<id>`, de modo que las escrituras de un clan se ordenan entre sí; la transacción de la entrada
  vuelve a comprobar el tope, la estancia abierta y la invitación viva.
- **Salir y expulsar** son optimistas, como el vencimiento de una entrada en un bando (ADR-039 §4): en el mismo tick
  la RAM cierra la estancia, pasa el liderazgo al miembro más antiguo (por fecha de entrada y, a igualdad, por `id` de
  jugador) o disuelve el clan si era el último, difunde la ciudad y el `clan.update` de los afectados, y encola un
  único trabajo (clave `clan:<id>`) que escribe en una transacción tolerante el cierre, el líder nuevo o la
  disolución y sus eventos. Si falla del todo, se reencola en el barrido siguiente (ADR-022). No hace falta esperar a
  la base: la paz no termina en el acto (§3), y el canal deja de admitir al que sale en cuanto confirma la
  transacción, milisegundos después.

### 3. El clan es la tercera fuente de paz, con paz residual derivada

`AtPeace` pasa a ser «tratado que da paz, **o** los dos de la Orden, **o** `clanPeace`». `clanPeace(x, y, now)` es
cierto si los dos son miembros abiertos del mismo clan, o si hay un clan en el que uno tiene una estancia cerrada
hace menos de `EO_CLAN_PEACE_GRACE_SECONDS` y el otro tenía, en el instante de esa salida, una estancia abierta (la
actual o una cerrada después). Así la paz residual alcanza a quienes eran compañeros al salir y a nadie más: no a
quien entró después.

Nada de esto se guarda aparte: se deriva de las fechas de entrada y salida de `clan_members`, que ya hacen falta para
la historia. Al vencer el plazo, la estancia cerrada sale de la RAM en la fase 5 sin escribir nada. Un reinicio la
recupera de la base; si venció con el servidor parado, ya no se carga.

`clanPeace` es también el predicado del motivo del rechazo («sois del mismo clan»), como `OrderPeace`. El clan no da
guarnición: `allows_garrison` sigue siendo de la alianza.

### 4. Un clan no junta a la Orden y al Caos

La regla (`RN-CLAN-006`) se comprueba en el tick en los tres caminos, con la facción efectiva **y** la entrada
pendiente (confirmada o en vuelo) de cada miembro y de cada reserva: al invitar, al aceptar y en `faction.join` de un
miembro. Como red de seguridad, la migración pone un trigger en los dos sentidos: al abrir una estancia en
`clan_members` y al apuntar una entrada o cambiar la facción en `players`, la base rechaza con `23514` y el ID del
invariante si el clan quedaría con un miembro (o una entrada) de cada bando. Los dos triggers toman la fila del clan
(`FOR UPDATE`) para que dos escrituras concurrentes no se crucen.

### 5. El canal es un subsistema de sesión autorizado contra la base

`clan.chat.send`, `clan.chat.fetch`, `clan.chat.read` y `clan.lookup` los atiende la conexión, como los mensajes de
E16: no pasan por el tick ni por la cola. La conexión no lee la RAM de la simulación (eso cruzaría el escritor único),
así que la pertenencia se comprueba **en la base**, en la misma transacción que el envío o la lectura:

- **Candado por clan.** El envío toma `pg_advisory_xact_lock(int4, int4)` con un espacio propio («CLAN») y el `id` del
  clan; las transacciones de entrada y salida de `clan_members` toman el mismo. Así los `id` de un canal se confirman
  en orden (lo leído no se salta ninguno, como en ADR-038 §3) y ningún envío se cuela entre una salida y su `COMMIT`.
- **Desde la entrada.** La estancia guarda `first_message_id`: el mayor `id` del canal en el instante de entrar,
  leído con el candado tomado. Un miembro lee los mensajes con `id` mayor que ése; uno anterior no le llega nunca.
- **Entrega.** Tras el `COMMIT`, `clan.chat.new` a las sesiones de los miembros abiertos que la transacción leyó, con
  `Hub.SendToPlayer`. Es de un solo proceso, como la de E16 (ADR-038 §6).
- **Ritmo.** El mismo contador por jugador de los mensajes directos: un mensaje del canal cuenta como uno más.

La lista pública de miembros (`clan.lookup`) también se lee de la base: no es estado de juego y no hace falta que sea
la del tick en ese milisegundo.

### 6. La migración `000020`

Cinco tablas: `clans`, `clan_members` (una fila por estancia, con `first_message_id`, `left_at` y `left_reason`),
`clan_invites`, `clan_messages` y `clan_message_reads`, descritas en la spec §7. Índices únicos parciales: el nombre
(`lower(name)`) y la sigla de los clanes vivos, y una estancia abierta por jugador. Los triggers de §4. Sustituye el
diseño de [schema.md](../database/schema.md) §17.3: la sigla en lugar de `code`, el líder actual en lugar del
fundador (el fundador sale del evento `ClanFounded` y de la primera estancia) y sin `faction_id`.

## Alternativas descartadas

- **Los clanes como subsistema de sesión, fuera del tick**, como los mensajes: la simulación tendría que leer la paz
  de un caché que escribe otro, y un golpe podría llegar antes que la paz que ya se confirmó en la base.
- **Un pacto implícito por pareja de miembros**: crece con el cuadrado del clan, llena la diplomacia de tratados que
  nadie firmó, y cada salida rompería hasta diecinueve tratados con su preaviso y su evento.
- **La paz residual guardada** (una columna `peace_until`, o tratados temporales): se deriva de fechas que ya existen;
  guardarla obliga a mantenerla en cada entrada, salida y disolución.
- **Salir en dos fases**: retrasaría el fin de la pertenencia hasta el `COMMIT` sin aportar nada, porque la paz dura
  15 minutos más de todos modos.
- **Salir con todo el preaviso** (el que se va sigue en el canal 15 minutos): un expulsado leería la estrategia del
  clan después de ser expulsado.
- **La pertenencia como columna `clan_id` en `players`**: pierde la historia, y la paz residual y el «desde que entró»
  del canal salen precisamente de ella.
- **Autorizar el canal contra la RAM de la simulación**: la conexión tendría que pedírsela al loop por el buzón y
  esperar un tick por cada mensaje, o leerla sin sincronizar.
- **Un canal con historial completo al entrar**: lo descartó producto (Q9).
- **Territorios de clan** (`owner_type = 'CLAN'`): lo descartó producto para este slice (Q11).

## Consecuencias

- `AtPeace` responde ya por tres fuentes; quien lo lea debe saber que la paz del clan puede durar 15 minutos después
  de que el clan cambie. Los tests de la spec la fijan caso a caso.
- `faction.join` gana un rechazo nuevo (el bando de un compañero): [factions.md](../specs/factions.md) lleva su
  precisión.
- Los trabajos de entrada y salida van con la clave del clan, no con la de los jugadores: un jugador que entra en un
  clan y, a la vez, pide `faction.join` escribe por dos colas; los triggers de §4 son los que impiden que la base
  acabe con los dos bandos si el tick se equivocara.
- La entrega en vivo del canal es de un solo proceso, como la de los mensajes directos.
- Los mensajes del canal no se borran nunca, como los de E16, hasta que el runbook 24/7 añada el borrado.
- Si algún día existen territorios de clan, rangos o alianzas entre clanes, este ADR se sustituye en lo que toque:
  la pertenencia en la simulación y la paz derivada son los puntos que se mantendrían.
