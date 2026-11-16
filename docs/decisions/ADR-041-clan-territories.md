# ADR-041: Un territorio de clan se captura con la clave del clan, se disputa contra todos sus miembros y se libera al disolver en dos fases

Propósito: fijar cómo el slice de territorios de clan ([SPEC-CLAN-TERRITORIES](../specs/clan-territories.md)) escribe
un dueño `CLAN` sin que la base y la RAM discrepen, mide la hostilidad contra un grupo con la paz y la protección de
cada miembro, y deja libres los territorios de un clan que se disuelve sin cambiar un dueño antes del `COMMIT`.

| Campo | Valor |
|---|---|
| **Estado** | **Aceptado** |
| **Fecha** | **2026-11-16** |
| Ámbito | `internal/domain/territory` (el control con dueño de clan, la reclamación con su clan), `internal/domain/player` (la afinidad con el clan), `internal/game/simulation` (la reclamación para el clan, la hostilidad contra un clan, la captura, la disolución con territorios), `internal/persistence/postgres` (la reclamación, la captura y la disolución, con sus eventos por miembro), `internal/domain/eventlog` (el `clanTag` de los avisos), `internal/game/loadcheck`, migración `000021`, `packages/protocol`, cliente |
| Índice | [README.md](README.md) |
| Relacionados | [ADR-016](ADR-016-foreign-territory-actions.md) (qué es una acción hostil), [ADR-022](ADR-022-durable-write-retry-and-loop-inbox.md) (ninguna escritura se pierde en silencio), [ADR-028](ADR-028-event-log-from-world-events.md) (el registro desde `world_events`), [ADR-032](ADR-032-trade-offers-and-two-key-jobs.md) (los trabajos de dos claves), [ADR-034](ADR-034-territory-claims.md) (la reclamación y la captura en dos fases), [ADR-039](ADR-039-faction-peace-and-affinity.md) (la tierra afín en cada golpe), [ADR-040](ADR-040-clans-membership-and-peace.md) (la pertenencia en la simulación, la clave del clan, la paz del clan y la salida optimista) |

---

## Contexto

`territory_control` admite un dueño `CLAN` desde M6 (`owner_type` con su `CHECK` y `owner_id` como texto
polimórfico), pero nada lo escribe. Producto aceptó el 2026-11-16 las once recomendaciones de la spec: un miembro
reclama para su clan con su guerrero, con los plazos de E12; el territorio del clan es tierra afín para sus miembros y
no da población; reclamarlo es hostil contra todos sus miembros (lo impide la paz con cualquiera y la protección sólo
si están todos protegidos); lo defienden los guerreros de cualquier miembro; se avisa a todos; se queda en el clan
cuando alguien sale y queda libre al disolverse; sin cesiones, sin tope y sin tierra afín de compañero.

Piezas que ya existen y que el slice usa:

- **La reclamación y la captura** ([ADR-034](ADR-034-territory-claims.md)): la reclamación es un encargo durable con
  su fila en `territory_claims`, que la fase 5 revisa en cada tick con el mismo predicado que la orden; la captura es
  una orden en dos fases, un trabajo de dos claves (`city:<nueva>` y `city:<anterior>`) que escribe en una
  transacción el control, los límites de las dos ciudades y los eventos, y la RAM cambia el dueño al volver por el
  buzón. Un conflicto (`ErrClaimConflict`) termina la reclamación sin captura.
- **La pertenencia en la simulación** ([ADR-040](ADR-040-clans-membership-and-peace.md) §1): `clanState` con los
  clanes vivos, sus miembros y las salidas recientes; las escrituras de un clan van con la clave `clan:<id>`; salir es
  optimista y el último que sale disuelve el clan en el mismo trabajo, en una transacción tolerante.
- **La paz** (ADR-040 §3): `State.AtPeace(x, y, now)` responde por los tratados, por la Orden y por el clan con su paz
  residual; `OrderPeace` y `ClanPeace` dan el motivo del rechazo.
- **La afinidad** ([ADR-039](ADR-039-faction-peace-and-affinity.md) §5): `player.Affine` decide en cada golpe, con
  el control y la facción de la RAM, si el tile es tierra afín al dueño de la unidad golpeada.

## Decisión

### 1. El dueño de clan en la RAM y en la reclamación

`territory.Control` gana su lectura de clan (`ClanOwner() (int64, bool)`) junto a `PlayerOwner()`; `owner_id` sigue
siendo texto y el clan se escribe con su `id` decimal. `clanState` gana el índice de los territorios de cada clan,
derivado del control al cargar y mantenido en cada captura y liberación: es el que viaja en la vista del clan
(`territories[]`) y el que libera la disolución.

La reclamación gana su clan: `territory_claims.clan_id` (nulo si es para el propio jugador) y el campo
correspondiente en la reclamación de la RAM, que se recargan juntos al arrancar. El predicado de la fase 5 de una
reclamación para el clan añade que su dueño siga siendo miembro abierto de ese clan (`RN-CLANTERR-008`): una salida,
una expulsión o una disolución la rompen en ese tick, sin escribir más que su final, como cualquier otra ruptura.

### 2. La hostilidad contra un clan

Reclamar un territorio de un clan consulta, en la orden y en cada tick de la fase 5:

- **la paz con cada miembro abierto** del clan dueño, con `AtPeace` (como mucho `EO_CLAN_MAX_MEMBERS` consultas): si
  alguna responde que sí, se rechaza con `INVALID_TARGET` y el motivo de la primera fuente que la dé, en el orden de
  hoy (`OrderPeace`, `ClanPeace`, el tratado). Un miembro del propio clan y quien lo acaba de dejar caen aquí por
  `ClanPeace`;
- **la protección de todos**: se rechaza con `CITY_PROTECTED` sólo si la ciudad de cada miembro abierto está
  protegida en ese instante (`City.ProtectedAt(now)`), y siempre si lo está la de quien reclama.

Es el mismo punto único de ADR-034 (`claimBlocked`), con una rama para un dueño de clan; la de un dueño jugador no
cambia. La defensa automática de `RN-CONQ-006` recorre los guerreros parados de cada miembro abierto dentro del radio,
en orden de `id` de unidad, para que el resultado no dependa del orden de un mapa.

### 3. La captura con un clan usa la clave del clan

Cada lado de la captura toma la clave de su dueño: `city:<id>` si es un jugador (con su ciudad y su bono de E12) y
`clan:<id>` si es un clan (sin bono). Así hay cuatro combinaciones —libre a clan (una clave), jugador a clan, clan a
jugador y clan a clan (dos claves)— con el trabajo de dos claves de [ADR-032](ADR-032-trade-offers-and-two-key-jobs.md)
sin cambios. La clave del clan ordena la captura detrás de cualquier entrada o salida pendiente del mismo clan y
delante de las siguientes, y en particular de su disolución.

La transacción de la captura escribe un dueño `CLAN` sólo si el que reclama tiene, en ese momento, una estancia
abierta en ese clan (lo lee de `clan_members` con el candado del clan, `pg_advisory_xact_lock(int4, int4)` en el
espacio «CLAN», el mismo de ADR-040 §5); si no, o si el trigger de §6 la rechaza, vuelve como `ErrClaimConflict` y la
reclamación termina sin captura.

### 4. La última salida de un clan con territorios va en dos fases

En E18 salir es optimista (ADR-040 §2). La última salida de un clan **con territorios** cambia dueños, y el canon pide
que la RAM cambie un dueño después del `COMMIT`. Por eso:

- **Un clan sin territorios** se disuelve como hoy, en el acto y de forma optimista.
- **Un clan con territorios**: el tick valida la salida del último miembro, marca el clan como **en disolución** y
  encola el trabajo `clan.leave` (clave `clan:<id>`), que en una transacción cierra la estancia, libera **todos** los
  territorios que la base tiene a nombre del clan (leídos en la transacción, no de la RAM) con `version + 1`, marca
  `dissolved_at` y escribe sus eventos. Al volver por el buzón, la RAM cierra la pertenencia, libera los territorios,
  difunde `territory.update` y los `city.update` y `clan.update` de E18. Mientras está en disolución, el clan rechaza
  entradas («el clan se está disolviendo») y las reclamaciones para él se dan por rotas (su dueño ya no cuenta como
  miembro). Si la escritura falla del todo, se deshace la marca y se responde el error, como una orden en dos fases.

Un fallo intermedio no deja un clan disuelto con territorios: el trigger de §6 lo impide, y la clave del clan ordena
la disolución con cualquier captura en vuelo para ese clan (si la captura confirma antes, la disolución libera también
ese territorio; si confirma después, el trigger la rechaza y vuelve como conflicto).

### 5. Los eventos de un clan como parte, una fila por miembro

`TerritoryClaimStarted` y `TerritoryControlChanged` con un clan como dueño se escriben, en la misma transacción que el
hecho, **una fila por miembro abierto** leído con el candado del clan (`player_id` el miembro, `counterpart_player_id`
quien reclama, `clanId` y `clanTag` en el payload). El registro de cada jugador sigue saliendo de sus filas
([ADR-028](ADR-028-event-log-from-world-events.md)) y `Classify` no cambia. Ganar para el clan escribe la fila del que
reclamó; la liberación por disolución escribe un `TerritoryControlChanged` por territorio sin `counterpart_player_id`,
que no va al registro. Los avisos en vivo los manda la simulación con `sendNotices` a cada miembro abierto de su
`clanState`, con `clanTag`.

### 6. La migración `000021`

- `territory_claims.clan_id bigint NULL REFERENCES clans(id)`.
- Trigger `territory_control_clan_owner_guard` (antes de `INSERT` o `UPDATE` de `territory_control`): con
  `owner_type = 'CLAN'`, `owner_id` tiene que ser el `id` de un clan sin `dissolved_at`; toma la fila del clan con
  `FOR UPDATE`.
- Trigger `clans_dissolve_territory_guard` (antes de `UPDATE` de `clans` que fija `dissolved_at`): rechaza si queda
  alguna fila de `territory_control` a nombre del clan.
- Los dos rechazan con `23514` y el ID del invariante (`INV-TERR-012`), como los triggers de bandos de `000020`.
- La validación de carga resuelve cada dueño `CLAN` contra los clanes vivos y libera el que no lo es, como
  [INV-TERR-005](../invariants/territory.md#inv-terr-005) con un dueño `PLAYER` inexistente.

### 7. La afinidad con el clan

`player.Affine` gana el clan: el tile es afín al dueño de la unidad también si el dueño del territorio es el clan del
que es miembro abierto en ese tick. Sigue calculándose en cada golpe, sin guardar nada (ADR-039 §5). La tierra del clan
no es afín a los de su bando que no son del clan, ni la de un compañero a los demás.

## Alternativas descartadas

- **El territorio a nombre del líder** (`owner_type = 'PLAYER'` con una marca de clan): no necesita triggers, pero
  cada traspaso de liderazgo cambiaría el dueño de todos los territorios y su población, y el dueño `CLAN` seguiría
  sin usarse.
- **Una columna `owner_clan_id` con clave foránea** en `territory_control`: da la integridad sin trigger, pero duplica
  el dueño polimórfico que el esquema eligió en M6 y obliga a tocar cada lector del control.
- **Medir la paz y la protección contra el líder**: más barato, pero un pacto o una ausencia de una sola persona
  decidiría sobre la tierra de todo el clan (lo descartó producto, Q5).
- **La protección de cualquier miembro protege la tierra**: con veinte miembros siempre hay alguno protegido; la
  tierra del clan sería intocable (Q5).
- **La disolución optimista, con la liberación en un trabajo aparte**: la RAM liberaría los territorios antes del
  `COMMIT`, y un reinicio entre los dos trabajos dejaría territorios a nombre de un clan disuelto.
- **Disolver en dos fases siempre**: retrasaría también la disolución de un clan sin territorios, que no cambia
  ningún dueño.
- **Una sola fila de evento por clan**, repartida por el registro con la historia de pertenencia: obliga a una
  consulta por rangos de fechas en cada lectura del registro.
- **La cesión de territorios**, **el territorio para el último miembro** y **la tierra afín de compañero**: los
  descartó producto (Q4, Q8, Q10).

## Consecuencias

- La captura tiene cuatro combinaciones de claves; los tests de la spec fijan cada una.
- La última salida de un clan con territorios deja de ser instantánea: tarda lo que su transacción, y mientras tanto
  el clan rechaza entradas.
- Una reclamación sobre la tierra de un clan cuesta hasta `EO_CLAN_MAX_MEMBERS` consultas de paz por tick; con el
  tope de 20 es despreciable frente al combate.
- Una reclamación y una captura de un territorio de clan escriben hasta `EO_CLAN_MAX_MEMBERS` filas de eventos.
- La validación de carga gana un caso de reparación (un dueño `CLAN` que no es un clan vivo).
- Si llegan rangos o alianzas entre clanes, la hostilidad de §2 es el punto que habrá que revisar; la clave del clan y
  la disolución en dos fases se mantendrían.
