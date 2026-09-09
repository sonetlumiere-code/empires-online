# Safe Zones

Especificación de las zonas seguras `DENSE_FOREST` y `CAVERN`: geometría, cálculo de pertenencia,
estado `HIDDEN`, autoridad del servidor e interacción con la presencia y la protección offline.

| Campo | Valor |
|---|---|
| Spec ID | `SPEC-SAFE-ZONES` |
| Estado | Draft |
| Milestone | M5 Offline protection & Safe Zones |
| Canon | §5, §10, §11, §12, §13 |
| Depende de | [unit.md](unit.md), [movement.md](movement.md), [presence.md](presence.md) |
| Reemplaza a | — |

## 1. Objetivo

Una Safe Zone es una región del mapa donde una unidad en reposo queda **oculta**: deja de ser
visible para terceros. Es el equivalente en campo abierto de la protección offline de ciudades: da al
jugador un lugar donde dejar unidades sin que sean triviales de localizar.

En el MVP la Safe Zone es **estructura, geometría y estado**, no mecánica de combate. La tabla
`safe_zones` existe (migración `000001_initial_schema`) y el estado `HIDDEN` existe en el dominio y
en el protocolo, pero **la lógica está diferida**: la migración de semillas `000002_seed_catalogs`
solo siembra `civilizations`, `factions` y `eras`, de modo que en el MVP **`safe_zones` está vacía**
y ninguna unidad llega a `HIDDEN`. El valor de escribirlo ahora es que el modelo de visibilidad, la
persistencia y el protocolo ya contemplan unidades ocultas, de modo que cuando llegue el combate no
haya que rediseñar la emisión de deltas.

## 2. Scope

**Dentro de MVP**

- Tabla `safe_zones` creada por la migración `000001_initial_schema` (canon §11: entidad creada en el
  MVP con la lógica diferida).
- Tipos `DENSE_FOREST` y `CAVERN`, fijados por el `CHECK safe_zones_type_valid` (canon §10).
- Geometría rectangular `min_x`, `min_y`, `max_x`, `max_y`, inclusiva, con
  `CHECK safe_zones_bounds_ordered`.
- El valor `HIDDEN` del dominio de `units.status` (`CHECK units_status_valid`) y del enum
  `UnitStatus` de `packages/protocol`.
- El valor `HIDDEN` de `entity.despawn.payload.reason`, ya presente en el protocolo v1.

**Fuera de MVP**

- **Siembra de la geometría de las zonas: `TBD (fuera de MVP)`.** Ninguna migración inserta filas en
  `safe_zones`, y el generador de mundo no las produce. Con la tabla vacía, el índice de §6.3 es todo
  ceros y el predicado de §6.2 nunca se satisface.
- **Evaluación de `HIDDEN` en la fase 5 del tick: `TBD (fuera de MVP)`.** Las reglas de §6 describen
  el diseño objetivo; no hay código que las ejecute todavía.
- **Desactivación de una zona sin borrarla: `TBD (fuera de MVP)`.** `safe_zones` **no tiene** columna
  `active` en la migración `000001`; añadirla exige una migración aditiva. En el MVP el conjunto de
  zonas es inmutable tras el arranque y una zona solo deja de existir borrando su fila.
- Efectos de combate: inmunidad, reducción de daño, ruptura del ocultamiento por ataque.
- Mecánicas de detección: exploradores, radio de detección, contadores de sigilo, revelado temporal.
- Zonas seguras dinámicas: creadas, destruidas o capturadas por jugadores.
- Geometría no rectangular (polígonos, radios).
- Capacidad máxima de unidades por zona.
- Bonificaciones de recolección o regeneración dentro de la zona.

Nada de lo anterior se implementa ni se insinúa en el código del MVP. El estado `HIDDEN` en MVP
significa **exactamente** "no visible para terceros", ni más ni menos.

## 3. Actores

| Actor | Rol |
|---|---|
| Game Server (fase 5 del tick) | Único evaluador de pertenencia y único autor de `HIDDEN`. |
| Player propietario | Observa `HIDDEN` en sus propias unidades. No puede solicitarlo ni renunciar a él. |
| Terceros | No reciben ninguna información sobre unidades ocultas. |
| Semillas / generador de mundo | Definirían la geometría de forma determinista desde `EO_WORLD_SEED`. `TBD (fuera de MVP)`. |

## 4. Inputs

Ningún mensaje cliente→servidor de v1 menciona Safe Zones ni ocultamiento; el jugador **no tiene
ningún input** en este subsistema (`RN-SAFE-008`). Los inputs son todos internos del servidor.

| Input | Origen | Tipo | Nota |
|---|---|---|---|
| Filas de `safe_zones` | PostgreSQL, al arrancar | `(id, name, zone_type, min_x, min_y, max_x, max_y)` | Vacío en MVP. |
| Terreno del mundo | RAM, regenerado desde `EO_WORLD_SEED` en cada arranque | `world.Grid` | `world_chunks` es copia de auditoría, no fuente primaria. |
| `EO_WORLD_WIDTH` / `EO_WORLD_HEIGHT` | `internal/config` | `int32`, por defecto `512` cada uno | Dimensionan el índice denso. |
| Posición y estado de las unidades | RAM (simulación) | `units.x`, `units.y`, `units.status` | Conjunto candidato de la fase 5. |
| `tickTime` | `Clock` inyectado | `int64` epoch ms | El ocultamiento no depende del tiempo, pero los deltas se sellan con él. |

## 5. Outputs

| Output | Destino | Cuándo |
|---|---|---|
| `units.status = 'HIDDEN'` / `'IDLE'` | PostgreSQL (dirty-flag + flush) | Al ocultarse o revelarse. |
| `entity.update { id, status }` | Propietario | Al ocultarse y al revelarse. |
| `entity.despawn { id, reason: "HIDDEN" }` | Terceros del chunk | Al ocultarse. |
| `entity.spawn { unit }` | Terceros del chunk | Al revelarse. |
| Índice `safeZoneOfTile` | RAM | Al arrancar y ante cualquier cambio de `safe_zones` o de terreno. |
| Log `error` + contador de violación de invariantes | Logs y métricas | Solape entre zonas (`INV-SAFE-001`). |

El servidor **no** produce ningún output dirigido a terceros que mencione una unidad oculta, ni
siquiera anonimizado (`RN-SAFE-009`).

## 6. Reglas de negocio

### 6.1 Modelo de datos

DDL real de la migración `000001_initial_schema`; el DDL canónico completo vive en
[../database/schema.md](../database/schema.md).

| Campo | Tipo | Semántica |
|---|---|---|
| `id` | `bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY` | Identidad de la zona. |
| `name` | `text NOT NULL` | Nombre legible de la zona. |
| `zone_type` | `text NOT NULL` + `CHECK safe_zones_type_valid (zone_type IN ('DENSE_FOREST','CAVERN'))` | Tipo canónico. |
| `min_x`, `min_y`, `max_x`, `max_y` | `integer NOT NULL` | Rectángulo delimitador, **inclusivo** en ambos extremos, con `CHECK safe_zones_bounds_ordered (min_x <= max_x AND min_y <= max_y)`. |
| `created_at` | `timestamptz NOT NULL DEFAULT now()` | Auditoría. |

La tabla **no tiene** `active` ni `updated_at` ni trigger `set_updated_at`: en el MVP las zonas son
inmutables tras el arranque. Desactivar una zona sin borrarla es `TBD (fuera de MVP)` y exige una
migración aditiva.

Se adopta el rectángulo `min_x/min_y/max_x/max_y` por coherencia deliberada con `territories`
(canon §10): la misma primitiva geométrica, el mismo índice y los mismos tests de contención sirven
para ambos sistemas. La geometría poligonal queda fuera de MVP.

### 6.2 Geometría y cálculo de pertenencia

El rectángulo por sí solo **no** define la zona: es una caja delimitadora que se intersecta con un
predicado de terreno. Esta composición evita tener que almacenar máscaras por tile y mantiene la
zona coherente con el mapa, que se regenera desde `EO_WORLD_SEED` en cada arranque.

| ID | Regla |
|---|---|
| `RN-SAFE-001` | Predicado `DENSE_FOREST`: `(x,y)` dentro del rectángulo **y** `terrain(x,y) == FOREST`. |
| `RN-SAFE-002` | Predicado `CAVERN`: `(x,y)` dentro del rectángulo **y** `terrain(x,y)` es `walkable` **y** existe **al menos un** vecino, en las 8 direcciones, con `terrain == MOUNTAIN`. |
| `RN-SAFE-003` | Los cuatro bordes del rectángulo son **inclusivos**: `min_x <= x <= max_x` y `min_y <= y <= max_y`. |
| `RN-SAFE-004` | Un tile `MOUNTAIN` o `WATER` nunca pertenece a una Safe Zone: no es transitable (`INV-SAFE-002`). |
| `RN-SAFE-005` | Fuera de los límites del mundo la consulta devuelve "sin zona" y nunca entra en pánico, igual que `TerrainAt` devuelve `WATER` fuera de límites. |

Ambos predicados derivan directamente del canon §5: `FOREST` "soporta SafeZone `DENSE_FOREST`" y
`MOUNTAIN` "soporta SafeZone `CAVERN` adyacente". La vecindad es de 8 direcciones, igual que el
movimiento (canon §4), para no tener dos nociones de adyacencia en el mismo servidor.

```
Rectángulo (min_x=10, min_y=10, max_x=14, max_y=13), zone_type = DENSE_FOREST
Leyenda: F = FOREST, G = GRASSLAND, # = tile perteneciente a la Safe Zone

      x=10 11 12 13 14
y=10   F  F  G  F  F        ->  # #  .  # #
y=11   F  F  F  F  G        ->  # # #  # .
y=12   G  F  F  F  F        ->  . # #  # #
y=13   F  G  G  F  F        ->  # .  .  # #

La caja tiene 20 tiles; la zona tiene 15. El hueco de GRASSLAND en (12,10) NO oculta.
```

```
Rectángulo (min_x=40, min_y=20, max_x=43, max_y=22), zone_type = CAVERN
Leyenda: M = MOUNTAIN (no walkable), G = GRASSLAND, # = tile perteneciente a la Safe Zone

      x=40 41 42 43
y=20   M  M  G  G      ->  .  .  #  .    (42,20) es adyacente a M(41,20)
y=21   M  G  G  G      ->  .  #  #  .    (41,21) y (42,21) adyacentes a montaña
y=22   G  G  G  G      ->  #  .  .  .    (40,22) adyacente a M(40,21)

Los tiles MOUNTAIN nunca pertenecen a la zona: no son transitables (INV-SAFE-002).
```

### 6.3 Índice en RAM

Evaluar el predicado en cada consulta sería estable pero innecesariamente caro dentro del tick. Al
arrancar, tras generar el terreno desde la semilla, se materializa un índice denso:

```go
// safeZoneOfTile[y*worldWidth + x] = id de la zona, o 0 si el tile no pertenece a ninguna.
// Mundo MVP 512 x 512 (EO_WORLD_WIDTH/HEIGHT por defecto) -> 262144 entradas uint32 = 1 MiB.
// Coste aceptable y O(1) por consulta.
type SafeZoneIndex struct {
    width, height int32
    tiles         []uint32
}

func (i *SafeZoneIndex) ZoneAt(x, y int32) (uint32, bool) {
    if x < 0 || y < 0 || x >= i.width || y >= i.height {
        return 0, false
    }
    id := i.tiles[y*i.width+x]
    return id, id != 0
}
```

| ID | Regla |
|---|---|
| `RN-SAFE-006` | Construcción determinista: las zonas se recorren en **orden ascendente de `id`** y solo se escriben los tiles que satisfacen el predicado. Iterar un mapa de Go sin ordenar está prohibido (canon §8). |
| `RN-SAFE-007` | Si dos zonas se solapan (violación de `INV-SAFE-001`), gana la de `id` menor, se registra un log de nivel `error` y se incrementa el contador de violación de invariantes. El servidor no aborta, pero el estado queda marcado como inconsistente. |

El índice se reconstruye ante cualquier cambio en `safe_zones` o en el terreno. En MVP ambos son
inmutables tras el arranque, de modo que se construye una sola vez —y sobre una tabla vacía.

### 6.4 Condiciones de ocultamiento

En la fase 5 del tick (`process timers/scheduled events`, canon §6), para cada unidad marcada como
candidata se evalúa:

```
entra_en_HIDDEN(u) :=
      u.status == IDLE
  AND u.hp > 0
  AND no existe unit_movements ACTIVE para u
  AND SafeZoneIndex.ZoneAt(u.x, u.y) devuelve una zona
```

| ID | Regla |
|---|---|
| `RN-SAFE-010` | El ocultamiento se evalúa **solo sobre unidades `IDLE`**, nunca sobre `MOVING`: es un estado de reposo. Esto es lo que hace coherente la transición prohibida `MOVING → HIDDEN` de [unit.md](unit.md) y evita recalcular pertenencia en cada waypoint de una polilínea. |
| `RN-SAFE-011` | Para no barrer todas las unidades del mundo cada tick, la fase 5 solo evalúa las unidades que cambiaron de tile o de estado en el tick actual. El resultado es idéntico a un barrido completo; la diferencia es únicamente de coste. |
| `RN-SAFE-012` | El ocultamiento **no expira por tiempo** y no tiene coste: no hay temporizador, ni cooldown de reentrada, ni penalización por entrar y salir repetidamente. Cualquiera de esas mecánicas pertenece al diseño de combate y está fuera de MVP. |
| `RN-SAFE-013` | Iniciar un movimiento revela la unidad de forma **inmediata y completa**: en el mismo tick en que el movimiento pasa a `ACTIVE`, los observadores de los chunks afectados reciben `entity.spawn` con la unidad ya en `MOVING`. No existe revelado progresivo ni ventana en la que la unidad se mueva todavía oculta; eso sería un privilegio de información imposible de auditar. |
| `RN-SAFE-014` | La unidad oculta **sigue ocupando su tile** en la capa de ocupación. El pathfinder la trata igual que a cualquier otra unidad. Ocultar no es desmaterializar: filtrar la información por el canal de visibilidad y no por el de simulación evita divergencias entre lo que el servidor simula y lo que cree cada cliente. |

### 6.5 Autoridad del servidor

Principio canónico §1.1: *"Client sends intent, server determines truth."* Aplicado aquí: el cliente
nunca envía "estoy oculto", nunca envía "estoy dentro de la zona 7" y nunca envía sus coordenadas
como hecho. El servidor conoce el terreno, conoce las zonas, conoce la posición autoritativa y
deduce el estado.

Ataques que habilitaría confiar en el cliente:

| Vector | Si el cliente declarase el estado | Consecuencia |
|---|---|---|
| **Ocultamiento arbitrario** | Un cliente modificado envía `hidden = true` desde cualquier tile. | Invisibilidad permanente en campo abierto. Cuando exista combate, invulnerabilidad efectiva: el atacante no puede seleccionar lo que no ve. |
| **Falsificación de posición** | El cliente envía la posición que el servidor usa para calcular pertenencia. | Teletransporte y ocultamiento simultáneos: la unidad "está" en el bosque y actúa en otro sitio. |
| **Revelado de ocultos (wallhack)** | El servidor envía las unidades ocultas marcadas con un flag y el cliente las esconde. | El flag se ignora en un cliente parcheado. Todo el sistema se vuelve decorativo: los datos ya están en el navegador. |
| **Desincronización de terreno** | El cliente decide si un tile es `FOREST`. | Con un mapa local editado, cualquier tile se declara Safe Zone. |
| **Salida negada** | El cliente decide cuándo deja de estar oculto. | La unidad se mueve, ataca y regresa sin haberse revelado nunca. |

Reglas de implementación que se derivan y son verificables en revisión de código:

| ID | Regla |
|---|---|
| `RN-SAFE-008` | Ningún mensaje **cliente→servidor** de v1 menciona Safe Zones ni ocultamiento. El protocolo fija cinco comandos (`session.hello`, `session.ping`, `session.view`, `unit.move`, `unit.cancel_move`) y ninguno lleva información de zona. |
| `RN-SAFE-009` | El servidor **no** emite unidades ocultas a terceros ni siquiera anonimizadas o con posición redondeada. La ausencia total es el único filtro seguro. |
| `RN-SAFE-015` | La geometría de las Safe Zones se envía al cliente solo como decorado de mapa dentro del área de interés; que el jugador vea dónde hay bosque denso no le da ninguna ventaja, porque el estado de las unidades ajenas nunca sale del servidor. El mensaje que la transportaría es `TBD (fuera de MVP)`: el protocolo v1 no tiene ninguno. |
| `RN-SAFE-016` | La pertenencia se recalcula desde el terreno autoritativo en cada arranque; no se confía siquiera en el índice persistido, porque es un derivado. |

Esto conecta con `INV-SEC-001` (*el estado del cliente nunca muta el estado autoritativo*); ver
[../invariants/security.md](../invariants/security.md).

### 6.6 Interacción con presencia y protección offline

Son dos sistemas **independientes**, con sujetos distintos, y no se componen:

| | Protección offline | Safe Zone |
|---|---|---|
| Sujeto | `cities` (`presence_state`) | `units` (`status`) |
| Estados | `ONLINE`, `OFFLINE_PENDING`, `PROTECTED` | `HIDDEN` |
| Disparador | Ausencia de conexión WS durante `EO_PRESENCE_TTL_SECONDS` (30 s) más el cooldown `EO_CITY_OFFLINE_PROTECTION_COOLDOWN_SECONDS` (300 s) | Geometría: unidad en reposo dentro de la zona |
| Depende de la presencia | Sí, por definición | **No** |
| Error asociado | `CITY_PROTECTED` | ninguno |

Consecuencias explícitas:

- **Desconectarse no oculta unidades.** Un jugador que cierra el cliente deja sus unidades en campo
  abierto exactamente donde estaban, visibles. Si quiere ocultarlas, debe llevarlas a una Safe Zone
  antes de desconectarse. Esta es una decisión de diseño: el mundo persiste y el riesgo también
  (canon §1.2 y §1.3).
- **Reconectarse no revela unidades.** El ocultamiento no se recalcula por presencia, solo por
  geometría; una unidad `HIDDEN` sigue `HIDDEN` tras el reconnect de su dueño.
- **El ocultamiento sobrevive al offline indefinidamente**, igual que la protección de ciudad en MVP
  (`cities.protection_until` NULL, canon §9). No hay caducidad.
- Una unidad `GARRISONED` no participa de este sistema: ya es invisible por otra vía y el interior de
  una ciudad no es una Safe Zone. Ver [garrison.md](garrison.md).

Detalle del ciclo de presencia en [presence.md](presence.md) y
[../architecture/game-loop.md](../architecture/game-loop.md).

## 7. Estados y transiciones

Las Safe Zones no introducen un estado nuevo: reutilizan `HIDDEN` de `units.status`, cuyo dominio
cerrado es `IDLE | MOVING | GARRISONED | HIDDEN | DEAD` (`CHECK units_status_valid`).

| Origen | Destino | Disparador | Fase del tick |
|---|---|---|---|
| `IDLE` | `HIDDEN` | La unidad está en reposo sobre un tile que satisface el predicado de su zona (`RN-SAFE-010`) | Fase 5 (timers) |
| `HIDDEN` | `MOVING` | `unit.move` aceptado sobre la unidad; revelado inmediato (`RN-SAFE-013`) | Fase 2 (comandos) |
| `HIDDEN` | `IDLE` | El tile deja de satisfacer el predicado: solo puede ocurrir si cambian `safe_zones` o el terreno, ambos inmutables tras el arranque en MVP | Fase 5 (timers) |
| `HIDDEN` | `DEAD` | Muerte. **Sin productor en MVP** | — |

Transiciones prohibidas: `MOVING → HIDDEN` (`RN-SAFE-010`), `HIDDEN → GARRISONED` y
`GARRISONED → HIDDEN` (`INV-SAFE-006`). La máquina completa vive en [unit.md](unit.md).

## 8. Errores

El catálogo estable de códigos (canon §16, `packages/protocol/src/v1/errors.ts`) **no define ningún
código específico de Safe Zone**, y esta spec no inventa ninguno: en MVP no existe comando de cliente
dirigido a una zona ni al ocultamiento, luego **no hay superficie de error de usuario**.

| Situación | Código | Nota |
|---|---|---|
| El jugador intenta ocultar o revelar una unidad | — | Imposible: no existe el comando (`RN-SAFE-008`). |
| `unit.move` sobre una unidad `HIDDEN` | ninguno | Es una operación **válida**: revela la unidad y la pone en `MOVING` (`RN-SAFE-013`). |
| Índice inconsistente o solape de zonas | `INTERNAL_ERROR` | Solo si impide servir la petición; el caso normal es log `error` + métrica, sin abortar (`RN-SAFE-007`). |

Los códigos para una futura mecánica de detección o de ocultamiento activo son
**TBD (fuera de MVP)**.

## 9. Invariantes

Familia `INV-SAFE-xxx`, **nueva**. Rango que ocupa esta spec: `INV-SAFE-001..007`. El registro
consolidado de la familia es [../invariants/territory.md](../invariants/territory.md), compartido con
`INV-TERR-*` (fichero en redacción junto con esta spec; hasta que exista, esta tabla es la
referencia).

| ID | Invariante | Dónde se verifica |
|---|---|---|
| `INV-SAFE-001` | Dos Safe Zones no comparten ningún tile. | Construcción del índice (`RN-SAFE-007`) + test de integración sobre los datos cargados |
| `INV-SAFE-002` | Ningún tile perteneciente a una Safe Zone es intransitable. | Construcción del índice + unit test del predicado |
| `INV-SAFE-003` | `units.status = 'HIDDEN'` ⟹ `SafeZoneIndex.ZoneAt(x,y)` devuelve una zona y la unidad no tiene movimiento `ACTIVE`. | Test de simulación tras cada tick |
| `INV-SAFE-004` | Ninguna unidad `HIDDEN` aparece en un delta dirigido a un jugador distinto de su propietario. | Test de integración de visibilidad |
| `INV-SAFE-005` | El rectángulo es válido: `min_x <= max_x` y `min_y <= max_y` (garantizado por `CHECK safe_zones_bounds_ordered`) y está contenido en `[0, EO_WORLD_WIDTH) × [0, EO_WORLD_HEIGHT)`. | `CHECK` para el orden; validación al cargar para la contención, porque las dimensiones del mundo son configuración y no pueden expresarse en un `CHECK` |
| `INV-SAFE-006` | Ninguna Safe Zone se solapa con la zona urbana de una ciudad. | Test de integración sobre los datos cargados |
| `INV-SAFE-007` | El índice en RAM es función pura de (`safe_zones`, terreno): reconstruirlo produce el mismo resultado byte a byte. | Test de determinismo |

`INV-SAFE-006` es lo que hace innecesaria la transición `HIDDEN → GARRISONED`: una unidad oculta
nunca está dentro de una zona urbana, y llegar a ella exige moverse, lo que la revela.

## 10. Persistencia

Respuesta a las cuatro preguntas del canon §12.

| Dato | Autoritativo en | Estrategia |
|---|---|---|
| Filas de `safe_zones` | PostgreSQL | **Write-through**, pero sin escritor en MVP: la tabla está vacía y su siembra es `TBD (fuera de MVP)`. Inmutable en runtime. |
| Índice `safeZoneOfTile` | RAM | **Reconstruible**: se calcula al arrancar desde el terreno regenerado y `safe_zones`. Nunca se persiste. |
| `units.status = 'HIDDEN'` | RAM (loop) | **Dirty-flag + flush** cada `EO_PERSISTENCE_FLUSH_INTERVAL_TICKS` (50 ticks). Ver [unit.md](unit.md). |
| Pertenencia tile → zona | — | **No se persiste**: es un derivado puro de geometría y terreno (`RN-SAFE-016`). |

`HIDDEN` no necesita write-through inmediato porque es enteramente derivable: si el proceso cae entre
dos flushes, la primera fase 5 tras el arranque restablece el estado correcto a partir de la posición
persistida. Persistirlo sirve solo para que un volcado de la base de datos sea legible sin ejecutar
el servidor.

## 11. Eventos

Evento de dominio: `UnitConcealmentChanged` (hecho consumado, en pasado). Es útil como registro
interno y como origen del delta.

Su escritura en `world_events` es **TBD (fuera de MVP)**: publicar cada ocultamiento en una tabla de
eventos consultable sería, en sí mismo, una fuga de información si esa tabla llegara a exponerse.

## 12. Contratos de red

No se introduce ningún tipo de mensaje nuevo: el protocolo v1 fija catorce mensajes
servidor→cliente y las Safe Zones no añaden ninguno. Los payloads los fija Zod en
`packages/protocol/src/v1/` y son la autoridad.

| Situación | Propietario | Terceros del chunk |
|---|---|---|
| `IDLE → HIDDEN` | `entity.update { id, status: "HIDDEN" }` | `entity.despawn { id, reason: "HIDDEN" }` |
| `HIDDEN → IDLE` | `entity.update { id, status: "IDLE" }` | `entity.spawn { unit }` |
| `HIDDEN → MOVING` | `unit.move.accepted` + `unit.movement.started` | `entity.spawn { unit }` + `unit.movement.started` |

Precisiones que el protocolo v1 impone y esta spec acata:

- `entity.despawn.payload.reason` tiene el valor `HIDDEN` en el enum
  (`OUT_OF_INTEREST | DEAD | GARRISONED | HIDDEN | REMOVED`). No se usa `OUT_OF_INTEREST`, que sería
  semánticamente falso porque la unidad sigue en el chunk, ni `DEAD`, que haría al cliente reproducir
  efectos de destrucción.
- `entity.spawn.payload` es `{ unit }` y **no lleva campo `reason`**: el revelado es indistinguible,
  para el cliente, de una entrada en el área de interés. Añadir un motivo de spawn sería un cambio de
  protocolo, `TBD (fuera de MVP)`.
- Ni `world.snapshot` ni ningún otro mensaje v1 transportan la geometría de las Safe Zones
  (`RN-SAFE-015`).
- El propietario ve `x`, `y` y `hp` reales de su unidad oculta a través de `entity.update`, cuyos
  campos son todos opcionales salvo `id`: solo se transmite lo que cambió.

## 13. Tests esperados

Todos los tests de esta sección están **pendientes**: describen el diseño objetivo, no una suite
existente. Los tests de integración exigen PostgreSQL y Redis reales (`EO_INTEGRATION=1`) y hoy no se
ejecutan porque el daemon de Docker no está disponible en la máquina de desarrollo.

**Unit (dominio puro)**

- Predicado `DENSE_FOREST`: tile `FOREST` dentro del rectángulo ⟹ pertenece; tile `GRASSLAND` dentro
  del rectángulo ⟹ no pertenece; tile `FOREST` fuera del rectángulo ⟹ no pertenece (`RN-SAFE-001`).
- Predicado `CAVERN`: tile walkable con un vecino `MOUNTAIN`, un caso por cada una de las 8
  direcciones posibles de ese vecino (8 casos) ⟹ pertenece en los 8; sin ningún vecino `MOUNTAIN` ⟹
  no pertenece; tile `MOUNTAIN` ⟹ nunca pertenece (`RN-SAFE-002`, `RN-SAFE-004`).
- Bordes inclusivos: los cuatro vértices del rectángulo se evalúan; `max_x`/`max_y` están dentro
  (`RN-SAFE-003`).
- Índice: consulta fuera de los límites del mundo devuelve "sin zona" y no entra en pánico
  (`RN-SAFE-005`).
- Solape: dos zonas que comparten tiles ⟹ gana el `id` menor y se contabiliza la violación
  (`RN-SAFE-007`).
- Determinismo: dos construcciones del índice sobre la misma entrada son idénticas (`INV-SAFE-007`).

**Simulation (loop determinista con `FakeClock`)**

- Unidad `IDLE` colocada en un tile de la zona: tras un tick, `HIDDEN` (`INV-SAFE-003`).
- Unidad que atraviesa la zona sin detenerse: en ningún tick intermedio su estado es `HIDDEN`
  (`RN-SAFE-010`).
- Unidad que termina su movimiento dentro de la zona: `MOVING → IDLE` y en el tick siguiente
  `IDLE → HIDDEN`.
- Unidad `HIDDEN` que recibe `unit.move`: pasa a `MOVING` en el mismo tick, sin paso por `IDLE`
  observable en los deltas (`RN-SAFE-013`).
- La unidad oculta sigue bloqueando su tile para el pathfinder (`RN-SAFE-014`).

**Integration (PostgreSQL + Redis reales, `EO_INTEGRATION=1`)**

- Visibilidad: dos sesiones, jugadores distintos, mismo chunk. Al ocultarse la unidad de A, la sesión
  de B recibe `entity.despawn { reason: "HIDDEN" }` y ningún mensaje posterior menciona esa unidad
  (`INV-SAFE-004`).
- La sesión de A sigue recibiendo su propia unidad con `status = "HIDDEN"`.
- Datos cargados: no hay solape entre zonas (`INV-SAFE-001`) ni con zonas urbanas (`INV-SAFE-006`).
- `CHECK safe_zones_type_valid`: insertar un `zone_type` fuera de `{DENSE_FOREST, CAVERN}` es
  rechazado por la base de datos.
- `CHECK safe_zones_bounds_ordered`: insertar `max_x < min_x` es rechazado (`INV-SAFE-005`).

**Contract**

- `entity.despawn` con `reason = "HIDDEN"` valida contra el JSON Schema exportado por
  `packages/protocol`.

**Recovery**

- Reinicio con una unidad `HIDDEN` cuyo estado no llegó a persistirse: tras el arranque, el primer
  tick la devuelve a `HIDDEN` a partir de la geometría.

## 14. Documentos relacionados

- [unit.md](unit.md) — máquina de estados y visibilidad de unidades.
- [garrison.md](garrison.md) — la otra vía de invisibilidad, con reglas distintas.
- [territory.md](territory.md) — misma primitiva geométrica rectangular.
- [movement.md](movement.md) — por qué el ocultamiento no se evalúa en tránsito.
- [presence.md](presence.md) — presencia del jugador y protección offline de la ciudad.
- [../architecture/game-loop.md](../architecture/game-loop.md) — fase 5 del tick.
- [../database/schema.md](../database/schema.md) — DDL de `safe_zones`.
- [../invariants/territory.md](../invariants/territory.md) — registro de la familia `INV-SAFE-*`.
