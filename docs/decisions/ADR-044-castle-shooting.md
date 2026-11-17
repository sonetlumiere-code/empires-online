# ADR-044: El castillo golpea dentro de la resolución simultánea sin ser una unidad, elige su objetivo por el estado de ataque y firma sus muertes como edificio

Propósito: fijar cómo el slice de la Edad de los Castillos ([SPEC-CASTLE-AGE](../specs/castle-age.md)) hace que un
edificio golpee a las unidades que atacan a su dueño sin convertirlo en una unidad, a quién apunta, en qué orden entran
sus golpes en la fase 4 y cómo se escriben y se cuentan las muertes que causa.

| Campo | Valor |
|---|---|
| **Estado** | **Aceptado** |
| **Fecha** | **2026-11-17** |
| Ámbito | `internal/domain/advance` (el paso Hierro → Castillos), `internal/domain/building` (el castillo en el catálogo, con su perfil de disparo), `internal/game/simulation` (`castle.go`: el estado de los castillos, la elección del objetivo y los disparos en la fase 4; la muerte con un edificio como autor), `internal/domain/eventlog` (los dos tipos de entrada del castillo), `internal/persistence/postgres` (el `UnitKilled` con autor edificio), `internal/protocol`, migración `000025`, `packages/protocol` (`CASTLE`, `targetUnitId`, los dos tipos de entrada), cliente |
| Índice | [README.md](README.md) |
| Relacionados | [ADR-024](ADR-024-combat-simultaneous-resolution.md) (la resolución simultánea en la fase 4), [ADR-028](ADR-028-event-log-from-world-events.md) (el registro leído de `world_events`), [ADR-029](ADR-029-population-over-limit-and-house-destruction.md) (el asedio de un edificio), [ADR-030](ADR-030-stone-layer-and-era-advance.md) (el avance de era), [ADR-033](ADR-033-live-notices-ephemeral.md) (los avisos en vivo), [ADR-036](ADR-036-civilization-goods.md) (los bienes ajenos en cualquier mezcla, el edificio único por ciudad), [ADR-039](ADR-039-faction-peace-and-affinity.md) (la tierra afín en cada golpe), [ADR-043](ADR-043-civilization-units.md) (el combate por capacidad) |

---

## Contexto

Producto aceptó el 2026-11-17 las diez recomendaciones de la spec: el avance de Hierro a Castillos por 1 600 de
madera, 800 de piedra y 120 bienes ajenos en cualquier mezcla, 360 s; y el castillo, un edificio de la zona urbana
(uno por ciudad, 500 de madera y 800 de piedra, 180 s, 1 000 de vida, asediable) que golpea 10 cada 2 000 ms, a 5
tiles, a una sola unidad por disparo: la más cercana de las que están atacando a una unidad de su dueño, saqueando su
almacén o derribando uno de sus edificios.

El avance no pide decisiones nuevas: es un paso más del catálogo de `advance` con `ForeignGoods`
([ADR-030](ADR-030-stone-layer-and-era-advance.md) §4, [ADR-036](ADR-036-civilization-goods.md) §5). El castillo sí:

- **Hasta hoy sólo golpean las unidades.** La fase 4 recorre los enfrentamientos (`combatState.engagements`, uno por
  atacante, en orden de `id`), calcula los golpes que vencen con el estado de fin de la fase 3 y los aplica después
  todos a la vez: primero a unidades, luego los de saqueo, luego los de asedio ([ADR-024](ADR-024-combat-simultaneous-resolution.md)).
  Un golpe tiene un atacante `*unit.Unit`, que es también el autor de la muerte, el que recibe el botín y el que
  despierta la respuesta automática de los demás.
- **La muerte se escribe con su autor.** `Death` lleva `KillerID` y `KillerPlayer`; la transacción escribe un
  `UnitKilled` a nombre de la víctima con el dueño del autor como contraparte, y el registro lo cuenta a los dos lados
  (`UNIT_LOST` y `ENEMY_KILLED`), que también se avisan en vivo.
- **La hostilidad tiene un predicado único** por acción (`attackBlocked`, `cityAttackBlocked`): mismo jugador,
  visibilidad, protección de los dos lados, las cuatro fuentes de paz y las Safe Zones, comprobado al ordenar y en cada
  golpe ([INV-COMBAT-002](../invariants/combat.md#inv-combat-002), [INV-COMBAT-006](../invariants/combat.md#inv-combat-006)).

## Decisión

**1. El castillo es un edificio del catálogo con un perfil de disparo.** `building.Definition` gana tres campos,
`AttackDamage`, `AttackIntervalMs` y `AttackRange`, con un `CanShoot()` que los exige positivos, como
`unit.Definition.CanAttack`. `CASTLE` entra en el catálogo con 500 de madera y 800 de piedra, 180 000 ms,
`MaxHP` 1 000, `RequiresEra = CASTLE_AGE`, `UniquePerCity` y el perfil 10 / 2 000 / 5. `building.CanBuild` ya
comprueba la era y el único por ciudad, como con el taller; `PopulationBonus` 0. Ningún otro edificio dispara. El
rasgo `building.house_hp` sigue aplicándose sólo a `HOUSE` (`buildingMaxHP`).

**2. Su estado vive en la RAM y no se persiste.** `castleState` guarda por castillo el instante de su próximo disparo y
su objetivo actual. Es reconstruible: tras un reinicio, un castillo completo está listo y sin objetivo (no hay
enfrentamientos tras un reinicio, [ADR-024](ADR-024-combat-simultaneous-resolution.md) §2). Un castillo en obra o
derribado no tiene entrada; derribado, la pierde en `removeBuilding`.

**3. El objetivo sale del estado de ataque.** En la fase 4, después de calcular los golpes de las unidades (que es
cuando cada una ha decidido si persigue o se planta en `ATTACKING`) y antes de aplicar ninguno, cada castillo completo,
en orden de `id` de edificio, mira los enfrentamientos y toma como **candidatos** a los atacantes que:

- están vivos, en el mapa (ni guarnecidos ni entrando) y en `ATTACKING`;
- tienen como blanco algo del dueño del castillo: una unidad suya (`targetID`), su ciudad (`cityID`, saqueo) o un
  edificio de su ciudad (`buildingID`, asedio, también el castillo);
- están a distancia de Chebyshev ≤ `AttackRange` del tile del castillo, en su posición autoritativa;
- pasan `castleBlocked`, el predicado de hostilidad del castillo: el atacante no es del dueño, está visible, ni su
  dueño ni el del castillo están `PROTECTED`, no hay paz entre los dos (`OrderPeace`, `ClanPeace`, `AllyPeace`,
  `AtPeace`) y no pisa una Safe Zone.

Entre los candidatos elige el más cercano y, a igual distancia, el de menor `id`. Si el objetivo cambia (también a
ninguno), lo difunde en `building.update` con `targetUnitId`. El disparo vence como el golpe de una unidad: con
objetivo, si su próximo disparo quedó atrás se lleva a ahora; si `now >= nextShotAtMs`, hay disparo y
`nextShotAtMs += AttackIntervalMs`. Sin objetivo no se toca, así que el castillo queda listo en cuanto lo haya.

**4. Sus golpes se aplican con los de las unidades, después de ellos.** El paso 2 de la fase 4 aplica primero los
golpes de unidad, en orden de atacante, y después los del castillo, en orden de edificio, cada uno sobre la vida que
dejó el anterior; un objetivo que ya quedó a 0 no recibe más. Antes de aplicarlo, el golpe del castillo vuelve a pasar
`castleBlocked`; si no, se descarta y se registra `invariant_violation` con
`inv_id=INV-COMBAT-007` ([INV-COMBAT-007](../invariants/combat.md#inv-combat-007)). El daño pasa por `damageTaken`,
así que la tierra afín al dueño del golpeado lo reduce como cualquier otro (`RN-FAC-008`). Un golpe del castillo **no**
genera `alert`: no despierta la respuesta automática (`RN-CASTLE-006`), y no escribe `UnitAttacked`.

**5. La muerte lleva un autor edificio.** El mapa de asesinos del paso 3 pasa a guardar el autor como unidad **o**
edificio. `kill` acepta el autor edificio: sin botín (lo que llevaba la víctima se pierde, `Lost`), `KillerID` 0 y
dos campos nuevos en `Death`, `KillerBuildingID` y `KillerBuildingType`. `KillerPlayer` es el dueño del castillo, así
que el `UnitKilled` lleva su contraparte como siempre ([ADR-028](ADR-028-event-log-from-world-events.md)). El payload
gana `killerBuildingId` y `killerBuildingType`.

**6. El registro cuenta sus muertes con dos tipos propios.** Mismo `event_type`, `UnitKilled`, para no tocar la
consulta ni `EventTypes`; `eventlog.Event` gana `KillerBuildingType`, y `Classify` devuelve, si es `CASTLE`,
`UNIT_LOST_TO_CASTLE` a la víctima y `CASTLE_KILL` al dueño del castillo, en lugar de `UNIT_LOST` y `ENEMY_KILLED`.
Son tipos distintos para que el agrupamiento no mezcle lo que hizo un castillo con lo que hizo un ejército («Tu
castillo mató a 3 guerreros de Fulano»). Los dos van a `Kinds()` y a `NoticeKinds()`, y a los enums estrictos de Zod
(`EventLogEntryKind`, `EventLogNoticeKind`): cliente y servidor se estrenan juntos.

**7. El protocolo.** `BuildingType` gana `CASTLE`. `BuildingView` y `building.update` ganan `targetUnitId`, opcional y
anulable (`null` cuando deja de apuntar), aditivo. No hay mensajes nuevos.

**8. La base.** La migración `000025` añade `CASTLE` a `buildings_type_valid` y el índice único parcial
`buildings_one_castle_per_city` (`WHERE type = 'CASTLE' AND status <> 'DESTROYED'`), hermano del del taller
([INV-ECON-012](../invariants/economy.md#inv-econ-012)). Su `down` borra los castillos y los avances a `CASTLE_AGE` en
curso, y deja en `CASTLE_AGE` las ciudades que ya llegaron.

## Alternativas descartadas

1. **El castillo como unidad inmóvil** (una fila en `units` con un tipo `CASTLE` sin paso). Reutilizaría el combate
   entero, pero mezclaría lo que es un edificio (un hueco de la zona urbana, el asedio, la vida que se olvida, el
   único por ciudad) con lo que es una unidad (estado, movimiento, guarnición, población consumida, volcado de
   posición), y obligaría a excluirlo en cada sistema que recorre unidades.
2. **Disparar a toda unidad armada sin paz a su alcance.** Es la torre clásica, pero convierte el entorno de cada
   ciudad en un peaje y crea conflictos que nadie empezó (Q5 de la spec); y pediría su propio predicado de
   hostilidad en vez de colgarse del estado de ataque.
3. **Calcular los disparos al inicio de la fase, antes que los enfrentamientos.** Con el estado de fin de la fase 3,
   un atacante que llega al contacto en este tick todavía no está en `ATTACKING` y no recibiría el primer disparo
   hasta el siguiente. Calcularlos tras los enfrentamientos y antes de aplicar nada mantiene la simultaneidad: ningún
   golpe del tick ve el efecto de otro.
4. **Aplicar los disparos antes que los golpes de las unidades.** Cambiaría quién da el golpe mortal en los empates de
   todos los combates junto a un castillo; ponerlos detrás deja intactos los golden de E2 a E22.
5. **Un `event_type` propio** (`UnitKilledByCastle`). Obligaría a tocar `EventTypes`, la consulta y cada lector de
   `UnitKilled` (la auditoría, los recuentos), cuando la muerte es la misma con otro autor.
6. **Los mismos tipos de entrada** (`UNIT_LOST` y `ENEMY_KILLED`) con un campo de autor. El registro agrupa por tipo y
   por el otro jugador: un castillo y un ejército del mismo vecino se fundirían en una sola entrada con un texto
   falso.
7. **Que el disparo despierte la respuesta automática.** Las unidades golpeadas responden a una unidad; no hay una a
   la que perseguir, y mandarlas contra el castillo sería un asedio que nadie ordenó.
8. **Persistir el próximo disparo.** Un reinicio ya termina todos los enfrentamientos; sin ellos no hay objetivo, y un
   castillo listo tras el arranque no da ninguna ventaja.

## Consecuencias

### Positivas

- La fase 4 sigue siendo una sola resolución simultánea, ahora con dos clases de atacante, y los golden anteriores no
  cambian: los disparos van detrás.
- El castillo no necesita su propia noción de hostilidad: dispara a quien ya está en un enfrentamiento hostil contra su
  dueño, que el predicado de cada acción ya validó.
- El registro distingue lo que hizo un castillo sin un tipo de suceso nuevo en la base.

### Negativas

- Cada tick con enfrentamientos recorre, por castillo completo, la lista de enfrentamientos: O(castillos ×
  enfrentamientos). Con pocos jugadores es despreciable; si crece, el índice natural es por dueño del blanco.
- `kill` y el mapa de asesinos aceptan dos clases de autor, y cada lector de `Death` tiene que contemplar el autor
  edificio.
- `BuildingType` y los dos enums del registro crecen en enums estrictos: un cliente viejo tiene que actualizarse con
  el servidor.

### Neutras

- El avance a Castillos es una entrada más del catálogo de `advance`, sin esquema nuevo.
- El castillo se asedia, se olvida su daño y se derriba por los caminos de [ADR-029](ADR-029-population-over-limit-and-house-destruction.md),
  como el taller.
