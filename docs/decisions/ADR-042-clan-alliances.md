# ADR-042: La alianza entre clanes es la cuarta fuente de paz, una relación entre dos clanes que se firma en dos fases con los dos candados

Propósito: fijar cómo el slice de alianzas entre clanes ([SPEC-CLAN-ALLIANCES](../specs/clan-alliances.md)) da paz a
todos los miembros de dos clanes sin crear un tratado por pareja, quién la escribe y con qué candados, cómo se deriva
la paz residual con los aliados y cómo la base nunca queda con una alianza entre la Orden y el Caos sin preaviso.

| Campo | Valor |
|---|---|
| **Estado** | **Aceptado** |
| **Fecha** | **2026-11-16** |
| Ámbito | `internal/domain/clan` (la alianza, su máquina de estados, la mezcla de bandos), `internal/game/simulation` (las alianzas en `clanState`, las órdenes, `AtPeace`, la paz residual con los aliados, los vencimientos y la mezcla de bandos al entrar en un clan o en un bando), `internal/persistence/postgres` (las órdenes con los dos candados, la expiración en la disolución, la carga), `internal/domain/eventlog` (las entradas y los avisos de la alianza), `internal/game/loadcheck`, migración `000023`, `packages/protocol`, cliente |
| Índice | [README.md](README.md) |
| Relacionados | [ADR-016](ADR-016-foreign-territory-actions.md) (qué es una acción hostil), [ADR-022](ADR-022-durable-write-retry-and-loop-inbox.md) (ninguna escritura se pierde en silencio), [ADR-027](ADR-027-hot-treaties.md) (los tratados en caliente: órdenes en dos fases, vencimientos optimistas), [ADR-028](ADR-028-event-log-from-world-events.md) (el registro desde `world_events`), [ADR-032](ADR-032-trade-offers-and-two-key-jobs.md) (los trabajos de dos claves), [ADR-039](ADR-039-faction-peace-and-affinity.md) (la Orden como fuente de paz y el preaviso al entrar en un bando), [ADR-040](ADR-040-clans-membership-and-peace.md) (el clan como tercera fuente de paz, con paz residual derivada, la clave y el candado del clan), [ADR-041](ADR-041-clan-territories.md) (la hostilidad contra un clan y la disolución en dos fases) |

---

## Contexto

Producto aceptó el 2026-11-16 las once recomendaciones de la spec:

- una sola relación entre clanes, la alianza, con el ciclo de vida y los plazos de un tratado de E6;
- da sólo paz, entre todos los miembros de los dos clanes;
- la firman los líderes;
- un clan tiene como mucho dos alianzas activas;
- no se firma una alianza que junte a la Orden y al Caos, y si la mezcla aparece después, empieza su preaviso;
- la paz residual de E18 cubre también a los aliados;
- las alianzas activas son públicas, y el preaviso de ruptura se avisa a todos los miembros de los dos clanes;
- sin guerras declaradas ni canal compartido.

Piezas que ya existen y que el slice usa:

- **Los tratados** ([ADR-027](ADR-027-hot-treaties.md)): la RAM decide cada orden en el tick y reserva, un trabajo de
  la cola la escribe y la RAM la aplica al volver por el buzón (dos fases). Los vencimientos (oferta, preaviso) son
  optimistas con compensación. `treaties` guarda el par en orden canónico con dos índices únicos parciales.
- **La paz** ([ADR-040](ADR-040-clans-membership-and-peace.md) §3): `State.AtPeace(x, y, now)` responde por los
  tratados, por la Orden (`OrderPeace`) y por el clan con su paz residual (`ClanPeace`). Cada fuente da su motivo.
- **La pertenencia** (ADR-040 §1 y §2): `clanState` con los clanes vivos, el clan de cada jugador y las estancias
  cerradas recientes. Entrar va en dos fases con la clave `clan:<id>`; salir es optimista. Las transacciones de un
  clan toman su candado `pg_advisory_xact_lock(int4, int4)` en el espacio «CLAN».
- **La captura de clan a clan** ([ADR-041](ADR-041-clan-territories.md) §3): dos claves de clan en un trabajo de
  [ADR-032](ADR-032-trade-offers-and-two-key-jobs.md) y los dos candados en orden de `id` (`ascendingClans`).
- **La entrada en un bando** ([ADR-039](ADR-039-faction-peace-and-affinity.md) §3): al vencer, abre el preaviso de
  los tratados con el bando contrario en la misma transacción tolerante que escribe la facción.

## Decisión

### 1. La alianza es una relación entre clanes, en `clanState`

`clanState` gana las alianzas `PROPOSED` y `ACTIVE`, indexadas por el par canónico (`clanA < clanB`) y por clan, con
su estado, quién propuso, `acceptedAt` y `breakEffectiveAt`. Guarda también los rechazos recientes (par, clan que
propuso y fecha) de los últimos `EO_TREATY_RETRY_MINUTES`, y las alianzas cerradas en los últimos
`EO_CLAN_PEACE_GRACE_SECONDS` (para la paz residual, §3). Todo se carga con el mundo desde `clan_alliances`. La
validación de carga comprueba tres cosas: que cada alianza abierta une a dos clanes vivos, que ningún par tiene dos
abiertas del mismo estado, y que ninguna alianza `ACTIVE` sin preaviso junta a la Orden y al Caos. Si alguna falla,
la cierra en la RAM y lo registra, como las demás reparaciones de carga.

La alianza **no** crea tratados: `treaties` no cambia, y un tratado entre dos aliados sigue siendo independiente de la
alianza.

### 2. `AtPeace` gana la alianza, con su motivo

`AtPeace` pasa a ser «tratado que da paz, **o** `OrderPeace`, **o** `ClanPeace`, **o** `AllyPeace`».
`AllyPeace(x, y, now)` es cierto en dos casos:

- **Aliados.** `x` e `y` son miembros abiertos de dos clanes con una alianza `ACTIVE` (también en preaviso). Son dos
  lecturas de `memberOf` y una del mapa de pares: coste constante, sin recorrer miembros.
- **Paz residual con aliados.** Uno de los dos, `x`, tiene una estancia cerrada en un clan `A` hace menos de
  `EO_CLAN_PEACE_GRACE_SECONDS`. El otro, `y`, tenía en el instante de esa salida una estancia abierta en un clan `B`
  (la actual o una cerrada después). Y en ese instante había una alianza entre `A` y `B` que estaba `ACTIVE`: aceptada
  antes de la salida y no cerrada antes de ella.

  Es la misma derivación de ADR-040 §3 con un salto más, y como ella no se guarda: sale de las estancias y de las
  fechas de la alianza.

El orden de los motivos del rechazo es `OrderPeace`, `ClanPeace`, `AllyPeace` y el tratado. El de la alianza es
«vuestros clanes están aliados». La hostilidad contra un clan de ADR-041 §2 (`clanClaimBlocked`) ya recorre a cada
miembro con `AtPeace`, así que una reclamación contra un aliado se rechaza sin cambiar ese punto: sólo gana la rama del
motivo. Cuando una alianza pasa a `ACTIVE`, o alguien entra en un clan aliado, los enfrentamientos, saqueos, derribos,
estandartes y reclamaciones entre las dos partes terminan por el mismo camino que cuando nace un tratado.

### 3. Las órdenes van en dos fases, con las dos claves y los dos candados

`clan.alliance.propose`, `clan.alliance.respond` y `clan.alliance.cancel` son comandos de la simulación, no hostiles,
y siguen ADR-027 §2:

- **En el tick.** Se valida contra la RAM:
  - que quien la da es el líder de su clan;
  - el estado de la relación y que no haya otra orden de alianza en vuelo sobre el mismo par;
  - el tope de cada clan, contando las aceptaciones en vuelo;
  - la mezcla de bandos con las facciones efectivas y las entradas pendientes.

  Después se reserva el par y se encola un trabajo con las claves `clan:<min>` y `clan:<max>`.
- **En la transacción.** Toma los candados de los dos clanes en orden de `id` y vuelve a comprobar, contra la base,
  todo lo que el tick comprobó:
  - que quien la dio es `clans.leader_player_id` de su clan;
  - que el clan no está disuelto;
  - el estado de la fila;
  - el tope;
  - para proponer y aceptar, la mezcla de bandos con `players.faction_id` y las entradas pendientes de los miembros
    abiertos.

  Si algo no cuadra, no escribe y vuelve como rechazo con su motivo (`INVALID_TARGET`).
- **Al volver por el buzón.** La RAM aplica el cambio o deshace la reserva. La paz nace, y el preaviso empieza, en ese
  tick. Si la escritura no llega tras sus reintentos, la RAM no ha cambiado y quien la dio recibe `INTERNAL_ERROR`.

Un trabajo de dos claves bloquea a un worker hasta que llega el otro (ADR-032 §3). Aquí es aceptable: las órdenes de
alianza son raras y las dos claves son de clanes, no de ciudades.

Los vencimientos son optimistas (ADR-027 §3). En la fase 5, una oferta que caduca pasa a `EXPIRED`, y un preaviso que
vence pasa a `BROKEN`. La RAM aplica el cambio en ese tick y un trabajo con las dos claves lo escribe, de forma
tolerante: sólo si la fila sigue en ese estado. Si falla del todo, se reencola en el barrido siguiente (ADR-022).

### 4. La mezcla de bandos que aparece después abre el preaviso en la transacción que la crea

Dos caminos pueden mezclar a la Orden y al Caos en una alianza activa. Los dos hacen como ADR-039 §3 con los tratados:
abren el preaviso en la misma transacción que escribe el cambio, de forma tolerante (sólo sobre una fila `ACTIVE` sin
`break_effective_at`, y si no, se omite sin error). Son:

- **La entrada en un bando que vence** (trabajo `faction.joined`, clave `player:<id>`). Además de los tratados, abre
  el preaviso de cada alianza `ACTIVE` del clan del jugador con un clan que tenga un miembro abierto del bando
  contrario, con `ClanAllianceBreakAnnounced` y `reason = FACTIONS`. La RAM lo aplica en el mismo tick que la facción,
  y espera, como con los tratados, si hay una orden de alianza en vuelo sobre ese par.
- **La entrada en un clan** (trabajo de `clan.respond`, clave `clan:<id>`). Al confirmar la estancia, abre el preaviso
  de cada alianza `ACTIVE` del clan con un clan que tenga un miembro abierto del bando contrario al del que entra, o al
  de su entrada pendiente. La RAM lo aplica al volver por el buzón, junto con la entrada.

Ninguno de los dos toma el candado del clan aliado ni lleva su clave: un trabajo de muchas claves bloquearía varios
workers. La escritura tolerante evita que un orden distinto entre colas deje la base incoherente. La base nunca queda
con una alianza `ACTIVE` sin preaviso entre la Orden y el Caos: o la transacción entera está escrita, o el cambio que
mezcla no lo está.

### 5. La disolución expira las alianzas en su transacción

La transacción que disuelve un clan (la última salida, optimista sin territorios y en dos fases con territorios,
ADR-041 §4) pasa a `EXPIRED` sus ofertas y alianzas abiertas, con `ClanAllianceExpired`, sin preaviso. Esa
transacción ya lleva la clave y el candado del clan que se disuelve. La fila es del par, así que escribir sin el
candado del otro clan podría cruzarse con una orden suya. Por eso la escritura es tolerante (sólo si la fila sigue
abierta), y la orden del otro clan comprueba en su transacción que los dos clanes están vivos. La RAM las cierra en el
mismo tick que la disolución.

### 6. Los eventos de una alianza y el registro de dos clanes

Cada transición escribe su evento en `world_events`. `player_id` es el líder que dio la orden (o el líder del clan que
propuso, en los vencimientos y en la expiración); `counterpart_player_id` es el líder del otro clan en ese momento;
el payload lleva `atMs`, `allianceId`, los dos `clanId` y las dos siglas.

El preaviso tiene que llegar al registro de **todos** los miembros de los dos clanes (Q9), y el registro sale de las
filas de cada jugador (ADR-028). Por eso `ClanAllianceBreakAnnounced` se escribe **una fila por miembro abierto** de
cada clan, leídos con los candados tomados, como los eventos de un territorio de clan (ADR-041 §5). Las demás
transiciones escriben una sola fila, del líder que corresponde. Los avisos en vivo los manda la simulación con
`sendNotices` a los miembros de su `clanState`.

### 7. La migración `000023`

- Tabla `clan_alliances`:
  - `id` (identidad);
  - `clan_a` y `clan_b` (FK a `clans`, `CHECK (clan_a < clan_b)`);
  - `proposed_by_clan` (`CHECK` que sea uno de los dos) y `proposed_by_player` (FK a `players`);
  - `status` (`CHECK` de `PROPOSED`, `ACTIVE`, `REJECTED`, `WITHDRAWN`, `EXPIRED` y `BROKEN`);
  - `request_id`;
  - `created_at`, `accepted_at`, `break_effective_at`, `break_reason` (`CANCEL` o `FACTIONS`) y `closed_at`.

  Dos índices únicos parciales por par (`ACTIVE` y `PROPOSED`) y un índice por cada clan para la carga y el tope.
- Trigger `clan_alliances_faction_guard`, antes de `INSERT` o de pasar a `ACTIVE`. Rechaza con `23514` y el ID del
  invariante una alianza cuyos miembros abiertos, con sus entradas pendientes, juntarían a la Orden y al Caos. Toma las
  filas de los dos clanes con `FOR UPDATE`, como los triggers de bandos de `000020`. No vigila la mezcla posterior,
  que abre el preaviso (§4).
- Trigger `clan_alliances_dissolved_guard`: rechaza abrir o aceptar una alianza con un clan disuelto.
- Se suma al `TRUNCATE` de los tests y a [schema.md](../database/schema.md) §1.

El tope por clan (`EO_CLAN_MAX_ALLIES`) es configuración, no esquema: lo comprueban el tick y la transacción con los
dos candados, sin trigger.

## Alternativas descartadas

- **Un tratado `NON_AGGRESSION` implícito por cada pareja de miembros.** Hasta 400 filas por alianza que nadie firmó,
  y cada entrada o salida firmaría o rompería veinte. Lo descartó ADR-040 dentro de un clan por la misma razón.
- **La alianza como un tratado entre los dos líderes** que «se extiende» a sus clanes. Ata la alianza a una persona:
  cada traspaso o sucesión de E20 la rompería o la movería, y la paz dependería de quién lidera en ese segundo.
- **Fundir la alianza en la paz del clan** (un «superclan» con dos clanes dentro). Mezcla el tope de miembros, el canal
  y los territorios de los dos, que producto dejó separados (Q2, Q11).
- **Las órdenes optimistas**, como la salida de un clan. Una alianza da paz, y la paz que nace antes del `COMMIT`
  terminaría enfrentamientos que la base podría no confirmar. Los tratados ya eligieron las dos fases por eso.
- **Una sola clave** (la del clan que da la orden) con un candado del otro clan sólo en la base. La cola no ordenaría
  la alianza con las entradas del otro clan, y una aceptación podría escribirse antes que una entrada que la base ya
  tenía en vuelo para el otro.
- **Bloquear la mezcla posterior** (una entrada o un bando que juntaría a la Orden y al Caos con un aliado). El clan
  aliado vetaría a quién invita el otro. Lo descartó producto (Q6).
- **Guardar la paz residual con aliados** en una tabla: duplica lo que ya dicen las estancias y las fechas de la
  alianza, y habría que limpiarla.
- **Una fila de evento por clan** para el preaviso, repartida por el registro con la historia de pertenencia: lo
  descartó ADR-041 §5 por el coste de leer el registro.

## Consecuencias

- `AtPeace` gana una fuente y la paz residual un salto. Siguen siendo de coste constante, salvo la residual, que
  recorre las estancias cerradas recientes del jugador, como hoy.
- Las órdenes de alianza bloquean a un worker hasta que llega la segunda clave, como el trato y la captura de clan a
  clan; `eo_persistence_queue_depth` las cuenta dos veces.
- El trabajo de la entrada en un bando y el de la entrada en un clan crecen: pueden escribir rupturas de alianzas,
  con una fila de evento por miembro de los dos clanes en cada una.
- La transacción de la disolución escribe además la expiración de hasta `EO_CLAN_MAX_ALLIES` alianzas y sus ofertas.
- Una guerra declarada, un canal de alianza o alianzas de más de dos clanes no caben en esta forma sin otra decisión;
  el par canónico, la clave y los candados se mantendrían.
