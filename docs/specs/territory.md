# Territory y Territory Control

Especificación de los territorios del mundo: la separación deliberada entre la geometría estática
(`territories`) y el control dinámico (`territory_control`), la resolución de un tile a su territorio
y la mecánica mínima de ownership.

| Campo | Valor |
|---|---|
| Spec ID | `SPEC-TERRITORY` |
| Estado | Draft |
| Milestone | M6 Territory, Safe Zones & Diplomacy |
| Canon | §4, §10, §11, §12, §13 |
| Depende de | [city.md](city.md), [player.md](player.md) |
| Reemplaza a | — |

## 1. Objetivo

Un `Territory` es una región nombrada y estable del mapa. `TerritoryControl` es quién la domina en
este momento. Son dos cosas distintas con ciclos de vida distintos y esta spec insiste en no
mezclarlas: la geometría es del mundo, el control es de la partida.

**Estado: implementado.** El mundo se siembra con una rejilla de territorios al primer arranque, la
fundación de la ciudad inicial cambia el dueño del territorio que la contiene, el cambio se persiste
transaccionalmente y viaja a las sesiones interesadas, y el cliente lo pinta.

Lo que sigue fuera de MVP es la **mecánica de conquista**: no hay forma de ganar ni perder un
territorio salvo fundando en uno sin dueño. La lista completa está en §2.

## 2. Scope

**Dentro de MVP**

- Tablas `territories` y `territory_control`, creadas por la migración `000001_initial_schema`
  (canon §11: entidades creadas en el MVP con la lógica diferida).
- Geometría rectangular `min_x`, `min_y`, `max_x`, `max_y` (canon §10), inclusiva, con
  `CHECK territories_bounds_ordered`.
- El mensaje `territory.update` y el campo `world.snapshot.payload.territories[]`, ambos ya definidos
  en `packages/protocol` con la forma `TerritoryView`, y **emitidos con contenido real**.
- **Siembra de la geometría**: una rejilla que cubre el mundo entero, generada al primer arranque por
  `territory.SeedGrid` y persistida en una transacción. No la hace una migración: la geometría tiene
  que caber en el mundo y las dimensiones del mundo son configuración (`EO_WORLD_WIDTH`,
  `EO_WORLD_HEIGHT`), que ninguna migración conoce.
- **Ownership al fundar la ciudad inicial** (§6.4), dentro de la misma transacción que la fundación y
  con concurrencia optimista sobre `territory_control.version`.
- **Overlay del territorio en el cliente**, derivado exclusivamente de lo que emite el servidor.

**Fuera de MVP**

- Captura por combate, asedio o presencia militar: no hay combate (canon §21).
- Mecánica de `control_points`: la columna existe y se persiste, pero **ningún proceso la modifica**.
  Su valor es siempre `0`. Nótese que **no viaja en `territory.update`**: `TerritoryView` no la
  transporta (§12).
- Estado `contested`: la columna existe y viaja en `TerritoryView`, pero es siempre `false`.
- Ownership de clan (`owner_type = 'CLAN'`): los clanes están fuera de MVP (canon §21). El valor está
  admitido por el `CHECK` y por el enum del protocolo, pero **ningún proceso lo escribe**.
- Influencia de facción (`owner_type = 'FACTION'`): existe la tabla `factions`, pero no hay mecánica
  de influencia. Mismo estatus que `CLAN`.
- `resource_modifiers` aplicados: la columna existe con `DEFAULT '{}'`, pero no modifica nada porque
  no hay economía, y **no viaja en `territory.update`**.
- Catálogo de valores de `territories.type`: **TBD (fuera de MVP)**. La columna es `text NOT NULL
  DEFAULT 'PLAINS'` **sin `CHECK`**: se trata como opaca, sin comportamiento asociado. Cerrar el
  dominio con un `CHECK` será una migración aditiva cuando el catálogo se decida. Tampoco viaja en
  `territory.update`.
- Geometría no rectangular, territorios anidados, territorios superpuestos.
- Comandos cliente→servidor sobre territorios: no existen en el protocolo v1 y no se inventan aquí.

## 3. Actores

| Actor | Rol |
|---|---|
| Generador de mundo | Define la geometría de forma determinista (`territory.SeedGrid`) y escribe `territories` y sus filas de control en el primer arranque. |
| Game Server | Único autor de `territory_control`. Resuelve tiles y emite `territory.update`. |
| Player | Observa. En MVP no ejecuta ninguna acción dirigida a un territorio. |

## 4. Inputs

Ningún mensaje cliente→servidor de v1 se dirige a un territorio. Los inputs son internos del
servidor.

| Input | Origen | Tipo | Nota |
|---|---|---|---|
| Filas de `territories` | PostgreSQL, al arrancar | `(id, name, min_x, min_y, max_x, max_y, type, resource_modifiers)` | Sembradas al primer arranque; 64 con el mundo por defecto. |
| Filas de `territory_control` | PostgreSQL, al arrancar | `(territory_id, owner_type, owner_id, control_points, contested, captured_at, version)` | Una por territorio, garantizada por la PK. |
| `EO_WORLD_WIDTH` / `EO_WORLD_HEIGHT` | `internal/config` | `int32`, por defecto `512` cada uno | Dimensionan el índice denso. |
| `EO_CHUNK_SIZE` | `internal/config` | `int32`, por defecto `32` | Divide la geometría en la huella de chunks. |
| `EO_INTEREST_RADIUS_CHUNKS` | `internal/config` | `int32`, por defecto `2` | Decide qué sesiones reciben `territory.update`. |
| Fundación de una ciudad | `internal/httpapi` -> `postgres.Bootstrapper` | transacción | **Único productor de cambio de dueño** (§6.4, RN-TERR-007). |
| `tickTime` | `Clock` inyectado | `int64` epoch ms | Sella `captured_at` y los envelopes de salida. |

## 5. Outputs

| Output | Destino | Cuándo |
|---|---|---|
| Fila de `territory_control` (`owner_type`, `owner_id`, `captured_at`) | PostgreSQL | Cambio de dueño. |
| `territory.update { territory }` | Sesiones cuya área de interés intersecta la huella del territorio | Cambio de dueño, entrada en el área de interés, `session.view` |
| `world.snapshot.payload.territories[]` | Sesión que acaba de conectar o de recentrar la vista | Snapshot: los territorios que intersectan el área de interés. |
| Índice `territoryOfTile` y huella de chunks por territorio | RAM | Al arrancar. |
| Evento de dominio `TerritoryControlChanged` | Bus interno | Cambio de dueño. |
| Log `error` + contador de violación de invariantes | Logs y métricas | Solape entre rectángulos (`INV-TERR-002`). |

## 6. Reglas de negocio

### 6.1 Modelo de datos

DDL real de la migración `000001_initial_schema`; el DDL canónico completo vive en
[../database/schema.md](../database/schema.md).

**`territories` — geometría estática**

| Campo | Tipo | Semántica |
|---|---|---|
| `id` | `bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY` | Identidad estable del territorio. |
| `name` | `text NOT NULL` | Nombre legible ("Northern Plains"). Identidad de mundo, no de UI. |
| `min_x`, `min_y`, `max_x`, `max_y` | `integer NOT NULL` | Rectángulo delimitador, **inclusivo** en ambos extremos, con `CHECK territories_bounds_ordered (min_x <= max_x AND min_y <= max_y)`. |
| `type` | `text NOT NULL DEFAULT 'PLAINS'` | Clasificación del territorio. **Sin `CHECK`**: valores `TBD (fuera de MVP)`, opaco en MVP. |
| `resource_modifiers` | `jsonb NOT NULL DEFAULT '{}'::jsonb` | Modificadores de recolección. Vacío y sin efecto en MVP. |
| `created_at` | `timestamptz NOT NULL DEFAULT now()` | Auditoría. |

No hay `updated_at` ni trigger: la tabla cambia con migraciones y ediciones de mundo, no con el
juego. En MVP es de facto inmutable tras el arranque.

**`territory_control` — control dinámico**

| Campo | Tipo | Semántica |
|---|---|---|
| `territory_id` | `bigint PRIMARY KEY REFERENCES territories (id) ON DELETE CASCADE` | Es a la vez la FK y la clave primaria: **una fila de control por territorio, garantizada por la base de datos**. No hay columna `id` propia. |
| `owner_type` | `text NOT NULL DEFAULT 'NONE'` + `CHECK territory_control_owner_type_valid (owner_type IN ('NONE','PLAYER','CLAN','FACTION'))` | Naturaleza del dueño. Valores en **mayúsculas**, igual que el enum `TerritoryOwnerType` del protocolo. |
| `owner_id` | `text` NULL | Identidad del dueño, interpretada según `owner_type`. NULL si `NONE`. |
| `control_points` | `integer NOT NULL DEFAULT 0` + `CHECK (control_points >= 0)` | Progreso de captura. Siempre `0` en MVP. |
| `contested` | `boolean NOT NULL DEFAULT false` | Disputa activa. Siempre `false` en MVP. |
| `captured_at` | `timestamptz` NULL | Momento del último cambio de dueño. |
| `version` | `integer NOT NULL DEFAULT 0` | Concurrencia optimista. La añade la migración `000003`, no la `000001`: la spec la exigía y el esquema inicial la omitió. |
| `updated_at` | `timestamptz NOT NULL DEFAULT now()` | Mantenido por el trigger `territory_control_set_updated_at`. |

`CHECK territory_control_owner_consistency`:
`(owner_type = 'NONE' AND owner_id IS NULL) OR (owner_type <> 'NONE' AND owner_id IS NOT NULL)`.

No hay columna `version`: la concurrencia optimista de `cities` no se replica aquí porque el único
escritor es el servicio de territorio dentro del comando que la provoca. Tampoco hay
`captured_time_ms`: el instante de captura se guarda solo como `timestamptz`, y la aritmética del
juego no depende de él en MVP.

`owner_id` es **polimórfico** y por eso se almacena como `text`, no como FK: `players.id` es `uuid` y
los identificadores de clan y facción no lo son. Se acepta conscientemente la pérdida de integridad
referencial declarativa a cambio de que añadir un nuevo tipo de dueño no requiera ni una columna
nueva ni una migración de datos. La integridad se sostiene en tres puntos: el `CHECK` de
consistencia, la validación en el servicio de dominio y el test de integridad de `INV-TERR-005`. Las
alternativas descartadas fueron una columna por tipo de dueño (`owner_player_id`, `owner_clan_id`,
…), que multiplica columnas nulas y `CHECK` cruzados, y una tabla de control por tipo de dueño, que
multiplica el código de lectura.

El `CHECK` admite `'CLAN'` y `'FACTION'` desde el primer día, pero **ninguna mecánica los escribe en
MVP** (§2, Fuera de MVP): están admitidos, no soportados. Admitirlos ahora es más barato que ampliar
después el `CHECK` con un `ALTER TABLE`, y no compromete nada porque el escritor es único.

### 6.2 Por qué geometría y control están separados

| Razón | Detalle |
|---|---|
| **Frecuencias de escritura opuestas** | `territories` se escribe en migraciones; `territory_control` se escribirá en cada captura. Mezclarlas obligaría a tocar filas grandes (con `jsonb`) por cada cambio de dueño e invalidaría cachés de geometría que no cambiaron. |
| **Cacheabilidad y determinismo** | El índice tile → territorio depende **solo** de `territories`. Al estar separado, ese índice es inmutable en runtime y puede construirse una vez, sin invalidación por eventos de juego. Si el dueño viviera en la misma fila, cualquier captura tocaría la fuente del índice. |
| **Ciclos de vida distintos** | Un territorio existe siempre; su control puede no existir todavía. Una fila `NONE` es un hecho del juego, no un hueco en la geometría. |
| **Historia y auditoría** | Con el control aislado, convertirlo en una tabla con historial (una fila por periodo de dominio) es un cambio contenido. Con todo junto, exigiría duplicar la geometría en cada registro histórico. |
| **Superficie de escritura mínima** | El único proceso que escribe control es el servicio de territorio; el único que escribe geometría son las migraciones. La separación hace esa regla verificable en revisión, no una convención de buena voluntad. |

Cada característica futura entra como **cambio aditivo** sobre `territory_control`, sin reescribir
`territories`, sin mover tiles y sin reasignar ids:

| Capacidad futura | Qué cambia | Migración destructiva |
|---|---|---|
| Ownership de clan | `owner_type = 'CLAN'`, `owner_id` = id del clan. La columna, el `CHECK` y el consumidor ya existen. | No |
| Influencia de facción | `owner_type = 'FACTION'`, `owner_id` ∈ `ORDER`/`CHAOS`/`NEUTRAL`. | No |
| Estado disputado | `contested` pasa a ser escrito por el sistema de combate. La columna ya se persiste y ya viaja en `TerritoryView`. | No |
| Progreso de captura | `control_points` acumula por tick; `captured_at` marca la transición. Publicarlo al cliente exige ampliar `TerritoryView`, que es un cambio aditivo del protocolo. | No |
| Historial de dominio | Nueva tabla `territory_control_history` con FK a `territories`. | No |
| Geometría no rectangular | Nuevas columnas de geometría en `territories` y una nueva estrategia de índice. `territory_control` **no se toca**: no conoce la geometría. | No |

Ese es el argumento central de la separación: la parte que más va a cambiar (el control) y la parte
más costosa de cambiar (la geometría) no comparten fila, ni transacción, ni caché.

### 6.3 Resolución tile → territorio

Un tile `(x, y)` pertenece al territorio `t` si y solo si:

```
t.min_x <= x <= t.max_x  AND  t.min_y <= y <= t.max_y
```

| ID | Regla |
|---|---|
| `RN-TERR-001` | Los límites son **inclusivos** en los cuatro lados. |
| `RN-TERR-002` | Un tile pertenece a **como máximo un** territorio (`INV-TERR-002`); puede no pertenecer a ninguno. "Tierra de nadie" es un estado legítimo, distinto de "territorio con `owner_type = 'NONE'`". |
| `RN-TERR-003` | La construcción del índice recorre los territorios en **orden ascendente de `id`** y pinta sus rectángulos. El orden es explícito y estable; iterar un mapa de Go sin ordenar está prohibido (canon §8). |
| `RN-TERR-004` | Si dos rectángulos se solapan —violación de `INV-TERR-002`— gana el `id` menor, se registra un log de nivel `error` y se incrementa el contador de violación de invariantes. El servidor arranca igualmente, pero el estado queda marcado como inconsistente. |
| `RN-TERR-005` | Una consulta fuera de los límites del mundo devuelve "ningún territorio" y nunca entra en pánico. |

```
Tres territorios sobre una porción del mundo. Cada celda muestra el id resuelto; '.' = ninguno.

        x=0  1  2  3  4  5  6  7
 y=0     1  1  1  .  .  2  2  2
 y=1     1  1  1  .  .  2  2  2
 y=2     1  1  1  .  .  .  .  .
 y=3     .  .  .  .  3  3  3  3
 y=4     .  .  .  .  3  3  3  3

 T1: (0,0)-(2,2)   T2: (5,0)-(7,1)   T3: (4,3)-(7,4)
 La columna x=3 y la franja y=2..3 quedan sin territorio: es válido.
```

Índice en RAM:

```go
// territoryOfTile[y*worldWidth + x] = territories.id, o 0 si el tile no pertenece a ninguno.
// Mundo MVP 512 x 512 (EO_WORLD_WIDTH/HEIGHT por defecto) -> 262144 entradas uint16 = 512 KiB.
type TerritoryIndex struct {
    width, height int32
    tiles         []uint16
}

func (i *TerritoryIndex) TerritoryAt(x, y int32) (uint16, bool) {
    if x < 0 || y < 0 || x >= i.width || y >= i.height {
        return 0, false
    }
    id := i.tiles[y*i.width+x]
    return id, id != 0
}
```

Se elige una tabla densa frente a un recorrido lineal sobre los rectángulos porque la resolución se
consulta dentro del tick y debe tener coste constante y predecible, sin depender del número de
territorios. `uint16` limita el índice a 65535 territorios, holgadamente por encima de cualquier
escenario previsible; como `territories.id` es `bigint`, la restricción se **verifica al construir el
índice** y queda documentada aquí.

El índice **nunca se persiste**: es un derivado puro de `territories` y se reconstruye en cada
arranque (canon §12, categoría "reconstruible").

**Relación con chunks.** Territorio y chunk son particiones **independientes** del mismo mapa: un
territorio puede abarcar varios chunks y un chunk puede contener varios territorios. No se fuerza
ninguna alineación. Para el broadcast, el servidor precalcula la **huella de chunks** de cada
territorio, `[min_x / chunkSize .. max_x / chunkSize] × [min_y / chunkSize .. max_y / chunkSize]`
donde `chunkSize` es `EO_CHUNK_SIZE` (32 por defecto, configurable entre 1 y 256), y la usa para
decidir qué sesiones reciben `territory.update`. La división se escribe como división entera y no
como desplazamiento de bits, precisamente porque el tamaño de chunk es configuración y no una
constante.

### 6.4 Mecánica mínima de ownership

Ownership simple y nada más. **Todo este apartado está implementado.**

| ID | Regla |
|---|---|
| `RN-TERR-006` | La siembra de mundo crea una fila de `territory_control` por territorio con `owner_type = 'NONE'`, `owner_id = NULL`, `control_points = 0`, `contested = false` (`INV-TERR-003`). Los `DEFAULT` del DDL producen exactamente ese estado. |
| `RN-TERR-007` | **Único productor de cambio de dueño:** la fundación de la ciudad inicial de un jugador. Si el tile central de la ciudad pertenece a un territorio cuyo `owner_type` es `'NONE'`, ese territorio pasa a `owner_type = 'PLAYER'` con `owner_id` = `players.id` del fundador, y se sella `captured_at`. |
| `RN-TERR-008` | Si el territorio ya tiene dueño, **no** cambia de manos: no existe la pérdida de control. Fundar dentro del territorio de otro jugador es válido y no altera el ownership. |
| `RN-TERR-009` | No hay disputa, ni progreso, ni decaimiento, ni caducidad por inactividad. Un jugador que no se conecta nunca pierde territorio: el mundo persiste y la protección offline (canon §9) hace de contrapeso. |
| `RN-TERR-010` | Todo cambio de `territory_control` emite `territory.update` a las sesiones cuya área de interés intersecta la huella de chunks del territorio (`INV-TERR-009`). |

Esta mecánica es deliberadamente pobre. Su función es cerrar el circuito completo —dato persistido,
índice en RAM, evento de dominio, delta de red, cliente que lo pinta— sobre el que después se apoyará
la conquista real, en lugar de dejar el sistema como un esquema muerto.

## 7. Estados y transiciones

El sujeto de la máquina de estados es la fila de `territory_control`, no una entidad del mundo
simulado. El estado es el par (`owner_type`, `contested`).

| Origen | Destino | Disparador | Fase del tick |
|---|---|---|---|
| — | `owner_type = 'NONE'` | Siembra de mundo: una fila por territorio (`RN-TERR-006`) | Arranque / migración |
| `'NONE'` | `'PLAYER'` | Fundación de la ciudad inicial sobre ese territorio (`RN-TERR-007`) | Fase 2 (comandos), misma transacción que la ciudad |
| `'PLAYER'` | `'PLAYER'` (mismo dueño) | Fundación de otro jugador dentro: **no hay transición** (`RN-TERR-008`) | — |
| `'PLAYER'` | `'NONE'` | Pérdida de control: **no existe** en MVP (`RN-TERR-009`) | — |
| `contested = false` | `contested = true` | Sistema de combate: **sin productor**, `TBD (fuera de MVP)` | — |

```
   siembra                fundación de ciudad
     │                    sobre territorio libre
     ▼                             │
  ┌───────┐                        ▼                  ┌──────────┐
  │ NONE  │ ─────────────────────────────────────────▶│  PLAYER  │
  └───────┘                                           └──────────┘
                                                        │      ▲
                            (sin transición de vuelta en MVP)  │
                                                        └──────┘
      CLAN y FACTION: admitidos por el CHECK, sin ningún productor.
```

## 8. Errores

El catálogo estable de códigos (canon §16, `packages/protocol/src/v1/errors.ts`) **no define ningún
código específico de territorio**, y esta spec no inventa ninguno: en MVP no existe comando de
cliente dirigido a un territorio, luego **no hay superficie de error de usuario**.

| Situación | Código | Nota |
|---|---|---|
| El jugador intenta reclamar o atacar un territorio | — | Imposible: no existe el comando. |
| Índice inconsistente (solape de rectángulos) | ninguno | Log `error` + métrica, sin abortar (`RN-TERR-004`). |
| Fila de control ausente o transacción de captura fallida | `INTERNAL_ERROR` | Registrado en logs y métricas. |

Los códigos para una futura mecánica de captura son **TBD (fuera de MVP)**.

## 9. Invariantes

Familia `INV-TERR-xxx`, **nueva**. Rango que ocupa esta spec: `INV-TERR-001..009`. El registro
consolidado de la familia es [../invariants/territory.md](../invariants/territory.md), compartido con
`INV-SAFE-*` (fichero en redacción junto con esta spec; hasta que exista, esta tabla es la
referencia).

| ID | Invariante | Dónde se verifica |
|---|---|---|
| `INV-TERR-001` | Rectángulo válido: `min_x <= max_x` y `min_y <= max_y` (garantizado por `CHECK territories_bounds_ordered`) y contenido en `[0, EO_WORLD_WIDTH) × [0, EO_WORLD_HEIGHT)`. | `CHECK` para el orden; validación al cargar para la contención, porque las dimensiones del mundo son configuración y no pueden expresarse en un `CHECK` |
| `INV-TERR-002` | Dos territorios no comparten ningún tile. | Construcción del índice (`RN-TERR-004`) + test de integración sobre los datos cargados |
| `INV-TERR-003` | Todo territorio tiene como máximo una fila en `territory_control`, y toda fila de control referencia un territorio existente. | `territory_control.territory_id` es PRIMARY KEY y FK con `ON DELETE CASCADE` + test de integración |
| `INV-TERR-004` | `(owner_type = 'NONE') = (owner_id IS NULL)`. | `CHECK territory_control_owner_consistency` + unit test |
| `INV-TERR-005` | Si `owner_type = 'PLAYER'`, `owner_id` es un `players.id` existente. | Validación en el servicio + test de integridad. **No** es un `CHECK`: `owner_id` es `text` polimórfico y no tiene FK (§6.1) |
| `INV-TERR-006` | En MVP, `control_points = 0` y `contested = false` para toda fila. | `CHECK (control_points >= 0)` acota por abajo; la igualdad a 0 la comprueba un test de integración, que se retirará cuando exista captura |
| `INV-TERR-007` | `owner_type <> 'NONE'` ⟹ `captured_at IS NOT NULL`. | Validación en el servicio + unit test. **No** es un `CHECK` en la migración `000001` |
| `INV-TERR-008` | El índice tile → territorio es función pura de `territories`: reconstruirlo produce el mismo resultado byte a byte. | Test de determinismo |
| `INV-TERR-009` | Ningún cambio de `territory_control` ocurre sin emitir `territory.update` a la huella de chunks del territorio. | Test de simulación |

## 10. Persistencia

Respuesta a las cuatro preguntas del canon §12.

| Dato | Autoritativo en | Estrategia |
|---|---|---|
| `territories` (geometría, `type`, `resource_modifiers`) | PostgreSQL | Sembrada por el Game Server en el primer arranque, en una transacción. Inmutable en runtime. |
| `territory_control` (`owner_type`, `owner_id`, `captured_at`, `version`) | PostgreSQL | **Write-through transaccional**: el canon §12 clasifica los cambios de ownership como escritura inmediata. Va en la misma transacción que el hecho que lo provoca —la fundación de la ciudad—, que se ejecuta en la goroutine del alta HTTP, **no** en la cola de persistencia ni en el tick. La cola existe para escrituras diferidas de la simulación; esto no lo es: si la transacción falla, el alta entera falla, y no hay nada que reintentar en segundo plano. El tick sigue sin hacer I/O contra PostgreSQL. El mundo en RAM se entera **después** del commit, por el mismo comando que incorpora al jugador. |
| `control_points`, `contested` | PostgreSQL | Sin productor en MVP. Cuando exista captura serán candidatos naturales a **dirty-flag + flush**, por ser magnitudes que cambian cada tick y toleran perder unos segundos tras un crash. |
| Índice `territoryOfTile` | RAM | **Reconstruible** al arrancar. Nunca se persiste. |
| Huella de chunks por territorio | RAM | **Reconstruible**, derivada de la geometría. Nunca se persiste. |

Recuperación tras reinicio: se recargan `territories` y `territory_control`, se reconstruyen índice y
huellas, y el estado de control de los territorios del área de interés viaja en el primer
`world.snapshot` de cada sesión. No hay estado de territorio que pueda perderse entre flushes, porque
todo lo mutable se escribe de inmediato.

## 11. Eventos

Evento de dominio: `TerritoryControlChanged` (hecho consumado, en pasado). Es el origen del delta y
**se escribe** como fila de `world_events` (`event_type`, `tick`, `player_id`, `payload jsonb`), en la
**misma transacción** que el cambio de control: un evento que puede faltar cuando el hecho ocurrió no
sirve para reconstruir nada.

El `payload` lleva `territoryId`, `ownerType`, `ownerId` y `version`. El `tick` es el del game loop en
el instante del alta; el contador del loop es atómico precisamente para poder leerlo desde la goroutine
de HTTP que atiende el alta sin provocar una carrera.

## 12. Contratos de red

Mensaje servidor→cliente `territory.update`. Envelope canónico
`{ v, type, seq, ts, requestId?, payload }`; los payloads los fija Zod en
`packages/protocol/src/v1/` y son la autoridad. `payload` es `{ territory }`, y `territory` es un
`TerritoryView`:

| Campo de `TerritoryView` | Tipo | Nota |
|---|---|---|
| `id` | `EntityId` (número entero ≥ 1) | Los ids durables viajan como **número** JSON, no como string. |
| `name` | `string` (1..64) | |
| `minX`, `minY`, `maxX`, `maxY` | `WorldCoordinate` (entero) | La geometría se incluye en cada mensaje. |
| `ownerType` | `"NONE" \| "PLAYER" \| "CLAN" \| "FACTION"` | Mayúsculas, idénticas a los valores del `CHECK`. |
| `ownerId` | `string \| null` | `null` cuando `ownerType` es `"NONE"`. |
| `contested` | `boolean` | Siempre `false` en MVP. |

`TerritoryView` **no transporta** `controlPoints`, `capturedAt`, `type` ni `resourceModifiers`:
ampliarlo cuando exista la mecánica es un cambio aditivo del protocolo (campos opcionales nuevos), y
los mensajes servidor→cliente no son estrictos precisamente para permitirlo.

```json
{
  "v": 1,
  "type": "territory.update",
  "seq": 87,
  "ts": 1757404800456,
  "payload": {
    "territory": {
      "id": 12,
      "name": "Northern Plains",
      "minX": 128,
      "minY": 64,
      "maxX": 191,
      "maxY": 127,
      "ownerType": "PLAYER",
      "ownerId": "6d2a1f8e-1c7b-4d1e-9a2f-0b6a5c3d4e5f",
      "contested": false
    }
  }
}
```

Reglas de emisión:

| ID | Regla |
|---|---|
| `RN-TERR-011` | **Al conectar**: los territorios cuya huella de chunks intersecta el área de interés inicial (radio `EO_INTEREST_RADIUS_CHUNKS = 2`) viajan dentro de `world.snapshot.payload.territories[]`, que es un array de `TerritoryView`. Con la rejilla sembrada, un área de interés de radio 2 sobre territorios de 64 tiles de lado intersecta típicamente entre 1 y 4 territorios. |
| `RN-TERR-012` | **Al cambiar el control**: se emite un `territory.update` a todas las sesiones cuya área de interés intersecta la huella del territorio. |
| `RN-TERR-013` | **Al mover la vista** (`session.view`): el servidor responde con un `world.snapshot` completo del área nueva, y los territorios viajan dentro de él por RN-TERR-011. **No se emiten además `territory.update` sueltos**: sería repetir en N mensajes lo que ya va en uno, y el cliente tendría que reconciliar dos fuentes para el mismo hecho. La regla se cumple —el cliente conoce todo territorio de su área nueva— por una vía distinta de la que se escribió aquí originalmente. |
| `RN-TERR-014` | La geometría se incluye en cada mensaje. Es redundante pero pequeña y constante, y evita al cliente mantener un catálogo aparte con su propio problema de invalidación. |
| `RN-TERR-015` | `contested` viaja desde el primer día aunque sea constante: así el cliente no necesita un cambio de esquema cuando la mecánica exista. |

Detalle del protocolo en [websocket-protocol.md](websocket-protocol.md).

## 13. Tests esperados

Los tests **unit** de esta sección existen y están en verde:
[`internal/domain/territory/territory_test.go`](../../services/game-server/internal/domain/territory/territory_test.go)
y su `seed_test.go`, 30 tests que cubren los cuatro bordes y las cuatro esquinas por separado, el
territorio que cruza fronteras de chunk, el solapamiento resuelto de forma determinista y el ejemplo
dibujado en §6.3 comprobado tile a tile.

Los tests de **integración** exigen PostgreSQL real (`EO_INTEGRATION=1`); el procedimiento sin Docker
está en [../operations/local-development.md](../operations/local-development.md) §3-bis.

**Unit (dominio puro)**

- Contención inclusiva: los cuatro vértices y los cuatro bordes del rectángulo pertenecen al
  territorio; los tiles inmediatamente exteriores, no (`RN-TERR-001`).
- Territorio de un solo tile (`min == max`) se resuelve correctamente.
- Consulta fuera de los límites del mundo devuelve "ningún territorio" y no entra en pánico
  (`RN-TERR-005`).
- Tile en tierra de nadie devuelve "ningún territorio", que es distinto de "territorio con
  `owner_type = 'NONE'`" (`RN-TERR-002`).
- Solape: dos rectángulos que comparten tiles ⟹ gana el `id` menor y se contabiliza la violación
  (`RN-TERR-004`).
- Huella de chunks: un territorio que cruza un borde de chunk produce la huella completa, con
  `EO_CHUNK_SIZE` distinto de 32 incluido.
- Determinismo: dos construcciones del índice sobre la misma entrada son idénticas (`INV-TERR-008`).
- Consistencia de dueño: `owner_type = 'NONE'` con `owner_id` no nulo es rechazado, y viceversa
  (`INV-TERR-004`).

**Integration (PostgreSQL + Redis reales, `EO_INTEGRATION=1`)**

- Datos cargados: ningún par de territorios se solapa (`INV-TERR-002`); cada fila de control
  referencia un territorio existente y no hay dos para el mismo (`INV-TERR-003`).
- `CHECK territories_bounds_ordered`: insertar `max_x < min_x` es rechazado (`INV-TERR-001`).
- `CHECK territory_control_owner_type_valid`: insertar `owner_type = 'player'` en minúsculas es
  rechazado.
- `CHECK territory_control_owner_consistency`: `('NONE', 'algo')` y `('PLAYER', NULL)` son rechazados
  (`INV-TERR-004`).
- Fundar la ciudad inicial dentro de un territorio `'NONE'` ⟹ `owner_type = 'PLAYER'`, `owner_id` = el
  fundador, `captured_at` sellado (`INV-TERR-007`), todo en la misma transacción que la ciudad
  (`RN-TERR-007`).
- Fundar dentro de un territorio ya poseído por otro jugador ⟹ el control **no** cambia
  (`RN-TERR-008`).
- `control_points = 0` y `contested = false` en todas las filas tras una partida simulada
  (`INV-TERR-006`).
- `ON DELETE CASCADE`: borrar el territorio elimina su fila de control.

**Contract**

- `territory.update` valida contra el JSON Schema exportado por `packages/protocol`, incluidos
  `ownerType = "NONE"` con `ownerId: null`.
- `world.snapshot` con `territories: []` valida (caso del MVP).

**Simulation (loop determinista con `FakeClock`)**

- Una sesión suscrita a los chunks del territorio recibe exactamente un `territory.update` por
  cambio de control, y ninguno cuando no hay cambio (`INV-TERR-009`).
- Una sesión cuya área de interés no intersecta la huella no recibe nada.
- `session.view` que desplaza el centro hacia el territorio produce el `territory.update`
  correspondiente (`RN-TERR-013`).

**Recovery**

- Reinicio: el índice reconstruido es idéntico al previo y el ownership persistido se conserva.

## 14. Documentos relacionados

- [safe-zones.md](safe-zones.md) — misma primitiva geométrica rectangular y mismo patrón de índice.
- [city.md](city.md) — fundación de la ciudad inicial, único productor de ownership del diseño
  objetivo.
- [unit.md](unit.md) — coordenadas lógicas de mapa.
- [websocket-protocol.md](websocket-protocol.md) — envelopes, límites y forma de los payloads.
- [../architecture/game-loop.md](../architecture/game-loop.md) — fases del tick y emisión de deltas.
- [../database/schema.md](../database/schema.md) — DDL de `territories` y `territory_control`.
- [../invariants/territory.md](../invariants/territory.md) — registro de las familias `INV-TERR-*` e
  `INV-SAFE-*`.
