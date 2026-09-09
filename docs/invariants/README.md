# Catálogo de Invariantes

Especificación formal de corrección de Empires Online: el conjunto de propiedades que el sistema debe cumplir en todo momento, cómo se garantiza cada una y qué test la verifica.

---

## 1. Qué es un invariante en este proyecto

Un **invariante** es una propiedad del estado del sistema que es verdadera **en todo instante observable**, para **cualquier** secuencia de entradas legales o ilegales, y cuya violación significa que el sistema está **roto**, no que un jugador hizo algo no permitido.

Contraste con una **regla de negocio**:

| | Invariante | Regla de negocio |
|---|---|---|
| Naturaleza | Propiedad estructural del estado | Política de gameplay |
| Puede cambiar por diseño | No sin cambiar el modelo | Sí, es esperable que cambie |
| Violación significa | Bug o corrupción | Intento no permitido del jugador |
| Respuesta del sistema | Log `error`, fail-fast o reparación | Rechazo con código de error estable |
| Ejemplo | «una unidad tiene como máximo un movimiento `ACTIVE`» | «el destino intransitable se rechaza con `TARGET_NOT_WALKABLE`» |
| Dónde vive | Este catálogo | Spec funcional del feature |

Una regla de negocio bien implementada **protege** un invariante. `TARGET_NOT_WALKABLE` es la regla; [INV-WORLD-003](world.md#inv-world-003) e [INV-MOVE-004](movement.md#inv-move-004) son los invariantes que esa regla defiende. Que la regla se rechace correctamente es comportamiento esperado; que un path atraviese agua es un fallo del sistema.

Corolario operativo: **un invariante nunca se comunica al jugador**. Si el servidor detecta una violación de invariante en runtime, el cliente recibe `INTERNAL_ERROR` (canon §16), nunca un mensaje que describa la condición interna.

## 2. Convención de identificadores

```
INV-<AREA>-<NNN>
```

- `<AREA>` ∈ `WORLD`, `PLAYER`, `CITY`, `UNIT`, `MOVE`, `PERSIST`, `SEC`, `TERR`, `SAFE`, `GARR`.
- `<NNN>` es un correlativo de tres dígitos con ceros a la izquierda, **asignado una sola vez y jamás reutilizado**.
- Un invariante que deja de aplicar no se borra ni se renumera: se marca `DEROGADO` con la fecha y el ADR que lo deroga, y su número queda quemado.
- Un invariante nuevo toma el siguiente número libre del área, aunque el catálogo quede desordenado temáticamente.

Las siete primeras familias son las del canon §22. Las tres últimas —`TERR`, `SAFE`, `GARR`— se añadieron al aparecer las specs de territorio, safe zones y guarnición, que necesitaban propiedades que ninguna familia existente expresaba: la geometría de una región no es la del mundo, y el ocultamiento de una unidad no es su posición. Se registran en dos archivos nuevos, [territory.md](territory.md) (`TERR` y `SAFE`) y [diplomacy.md](diplomacy.md) (`GARR`), y quedan enlazadas desde este índice.

> **Regla de resolución de colisiones.** Este catálogo es el **único registro** de invariantes del proyecto. Un ID estable designa un solo invariante, aquí y en cualquier documento que lo cite. Las specs de `docs/specs/` **citan IDs, no los redefinen** (§8): si una spec necesita una propiedad nueva, se añade aquí primero y se numera continuando la familia por encima del máximo ya asignado. Un ID reutilizado con otro significado es un defecto de documentación tan grave como un invariante roto, porque hace que dos equipos lean lo mismo y entiendan cosas distintas.

Los IDs son **referenciables desde el código**. Un chequeo defensivo en Go debe citar el ID en el comentario y en el log:

```go
// INV-MOVE-001: una unidad tiene como máximo un movimiento ACTIVE.
if existing != nil {
    return fmt.Errorf("INV-MOVE-001 violated: unit %d has active movement %d", unitID, existing.ID)
}
```

## 3. Formato obligatorio de cada ficha

Cada invariante se documenta con exactamente esta estructura. No se admiten fichas parciales: una ficha sin test previsto es una ficha incompleta.

```markdown
### INV-AREA-NNN — Título corto

| Campo | Valor |
|---|---|
| Severidad | CRITICO \| ALTO \| MEDIO |
| Aplicación | DB, TYPE, BOUNDARY, DOMAIN, TEST |
| Milestone | M1 |
| Política ante violación | FAIL_FAST \| REJECT \| REPAIR |
| Cobertura | **Cubierto** por `TestX` (paquete) \| **Parcial**: … \| **Sin cobertura**: … |

**Enunciado.** Una frase precisa y verificable, sin adverbios ni condicionales.

**Razón.** Qué se rompe concretamente si se viola.

**Cómo se garantiza.** Mecanismo concreto por cada nivel de aplicación declarado.

**Cómo se verifica.** Los tests, distinguiendo los que **existen y pasan** de los **previstos**.

**Violación en runtime.** Detección, log, métrica y política.
```

### 3.1 La fila `Cobertura` y las tres etiquetas de test

La fila `Cobertura` responde a una sola pregunta —**¿hay hoy un test real que verifique esto?**— y sólo admite tres respuestas, con el nombre exacto del test cuando lo hay:

| Etiqueta | Significado |
|---|---|
| **Cubierto** | Existe al menos un test que **está escrito y pasa** en la suite actual. Se cita su nombre real y su paquete. |
| **Parcial** | Hay tests reales que cubren una parte del enunciado, y se dice explícitamente qué parte queda fuera. |
| **Sin cobertura** | No hay ningún test en verde. O el test está **diseñado pero no ejecutado** (los de integración, que requieren Docker Desktop arrancado), o directamente no existe todavía. |

La misma disciplina se aplica dentro de **Cómo se verifica**, donde cada línea lleva su marca:

- `TestX` (unit, `internal/...`) — **existe y pasa**.
- `TestY` (integration, `internal/...`) — **diseñado, aún no ejecutado**: el daemon de Docker no arrancó en la máquina de desarrollo, así que la suite de integración está escrita pero no verificada.
- Test previsto: `Test_INV_...` — no existe todavía; el nombre fija la convención de §6 para cuando se escriba.

Esta distinción no es burocracia. Un catálogo que enumera nombres de test sin decir cuáles existen da una falsa sensación de cobertura, y esa falsa sensación es exactamente lo que un catálogo de invariantes no puede permitirse. Los nombres reales de los tests del proyecto están en español (`TestEjemploNumericoCanonico`, `TestAutomataDePresencia`); los `Test_INV_*` que aparecen en las fichas son, salvo indicación en contra, nombres **previstos**.

Reglas de redacción del **enunciado**: debe poder traducirse mecánicamente a una aserción. «La posición es coherente» no es un enunciado válido; «la posición de una unidad referencia un tile dentro de `[0, EO_WORLD_WIDTH) × [0, EO_WORLD_HEIGHT)` cuyo terreno es `walkable` y que no está en la capa de ocupación» sí lo es.

## 4. Niveles de aplicación

«Dónde se garantiza» un invariante. Un invariante fuerte se garantiza en **varios** niveles simultáneamente: la defensa en profundidad no es redundancia, es supervivencia ante un bug en cualquier capa individual.

| Token | Nivel | Mecanismo típico | Coste de violarlo |
|---|---|---|---|
| `DB` | Constraint de PostgreSQL | `CHECK`, `UNIQUE`, `FOREIGN KEY`, `NOT NULL`, índice parcial único, transacción | El dato incorrecto **no puede existir** en disco |
| `TYPE` | Sistema de tipos | tipo Go dedicado con constructor validante, `Zod` en `@empires-online/protocol` | El estado inválido **no es representable** |
| `BOUNDARY` | Validación en el límite | decodificador WebSocket, verificación del ticket, límites de tamaño y tasa | El dato sucio **no entra** al proceso |
| `DOMAIN` | Chequeo en el dominio | guarda explícita en el agregado, función pura de validación | El dominio **rechaza** la transición |
| `TEST` | Verificación automatizada | unit, integration, contract, simulation, recovery (canon §19) | La regresión **rompe el CI** |

Orden de preferencia: **`DB` y `TYPE` antes que `DOMAIN`; `DOMAIN` antes que `TEST` solo**. Un invariante cuyo único nivel es `TEST` es un invariante frágil y debe declararse así explícitamente en su ficha.

### 4.1 Nombres de constraint: los reales, no los de convención

Las fichas de este catálogo citan **los nombres que la migración `000001_initial_schema.up.sql` declara realmente**, no un esquema de nomenclatura idealizado. La DDL vigente no usa prefijos `ck_` / `uq_` / `fk_`: nombra las constraints con el patrón `<tabla>_<regla>` y deja **sin nombrar** las claves foráneas, que se declaran en línea y reciben el nombre generado por PostgreSQL (`<tabla>_<columna>_fkey`).

| Constraint real | Tabla | Qué impone |
|---|---|---|
| `players_username_format` | `players` | `CHECK (username ~ '^[A-Za-z0-9_-]{3,24}$')` |
| `cities_presence_state_valid` | `cities` | Dominio cerrado de `presence_state` |
| `cities_population_within_limit` | `cities` | `CHECK (population <= population_limit)` |
| `cities_unique_center` | `cities` | `UNIQUE (center_x, center_y)` |
| `units_status_valid` | `units` | Dominio cerrado de `status` |
| `units_hp_within_max` | `units` | `CHECK (hp <= max_hp)` |
| `unit_movements_status_valid` | `unit_movements` | Dominio cerrado de `status` |
| `unit_movements_time_ordered` | `unit_movements` | `CHECK (arrival_time_ms >= start_time_ms)` |
| `unit_movements_path_is_array` | `unit_movements` | `path` es un array `jsonb` no vacío |
| `unit_movements_one_active_per_unit` | `unit_movements` | Índice **parcial** único `WHERE status = 'ACTIVE'` |
| `territories_bounds_ordered` | `territories` | `min_x <= max_x AND min_y <= max_y` |
| `territory_control_owner_type_valid` | `territory_control` | Dominio cerrado de `owner_type` |
| `territory_control_owner_consistency` | `territory_control` | `owner_type = 'NONE'` ⟺ `owner_id IS NULL` |
| `safe_zones_type_valid` | `safe_zones` | `zone_type IN ('DENSE_FOREST','CAVERN')` |
| `safe_zones_bounds_ordered` | `safe_zones` | `min_x <= max_x AND min_y <= max_y` |
| `treaties_type_valid`, `treaties_status_valid` | `treaties` | Dominios cerrados |
| `treaties_distinct_players`, `treaties_canonical_pair` | `treaties` | `player_a_id <> player_b_id` y `player_a_id < player_b_id` |
| `treaties_one_active_per_pair_and_type` | `treaties` | Índice **parcial** único `WHERE status = 'ACTIVE'` |

> **Lo que la base de datos NO garantiza.** Merece decirse en el índice y no sólo en las fichas, porque es la fuente de error más habitual al leer este catálogo:
>
> - **No hay `CHECK` de límites de coordenadas** en `units` ni en `cities`. Ni superior ni inferior. El límite superior es `EO_WORLD_WIDTH`/`EO_WORLD_HEIGHT`, que es configuración y no puede empotrarse en una migración sin congelarla. Un `INSERT` directo fuera del mundo **es aceptado**.
> - **No hay índice único sobre `cities.owner_player_id`**: `cities_owner_idx` es un índice normal. «Una ciudad por jugador» es una regla de dominio, no una constraint.
> - **No existe `units.version`**: sólo `cities` tiene columna de concurrencia optimista.
> - **`schema_migrations` no guarda checksums**: `golang-migrate` registra versión y estado `dirty`. La inmutabilidad de una migración publicada es convención de revisión, no comprobación automática.
> - **`idempotency_keys` existe pero ninguna ruta escribe en ella**: la idempotencia vigente es la de Redis.
>
> Ninguna ficha de este catálogo debe declarar `DB` para una propiedad de esta lista.

## 5. Severidad

| Nivel | Definición | Política por defecto |
|---|---|---|
| **CRITICO** | La violación corrompe el estado autoritativo, permite hacer trampa o rompe la garantía de servidor autoritativo (canon §1.1). | `FAIL_FAST` en arranque y en el loop; `REJECT` con `INTERNAL_ERROR` en el borde de comandos. Alerta inmediata. |
| **ALTO** | La violación produce estado incoherente observable por jugadores, acotado a una entidad y reparable de forma determinista. | `REPAIR` con evento de auditoría, o `REJECT` del comando. Alerta agregada. |
| **MEDIO** | La violación degrada una propiedad de calidad (determinismo, precisión, reproducibilidad) sin corromper el estado durable. | Log `error` + métrica; reparación diferida. Sin alerta de guardia. |

Políticas ante violación:

- **`FAIL_FAST`** — el proceso no continúa con estado sospechoso. En arranque: aborta antes de aceptar conexiones. En el tick: aborta el tick, marca la entidad como no simulable y escala. Nunca «continúa y espera lo mejor».
- **`REJECT`** — el comando en curso no se aplica; se devuelve `system.error` con el código canónico correspondiente (`INTERNAL_ERROR` si la violación es interna) y no se muta estado.
- **`REPAIR`** — se aplica una corrección determinista y documentada (por ejemplo, derivar `units.status` desde `unit_movements`, que es la fuente write-through), se registra un `world_events` de auditoría y se sigue.

### 5.1 Señal obligatoria ante cualquier violación

Toda detección emite un log estructurado `log/slog` de nivel `error` con los campos estándar del canon §18 (`ts`, `level`, `msg`, `player_id`, `session_id`, `request_id`, `tick`) más:

```json
{
  "level": "error",
  "msg": "invariant_violation",
  "inv_id": "INV-MOVE-001",
  "severity": "CRITICO",
  "policy": "REJECT",
  "entity": "unit:8123",
  "detail": "unit has 2 ACTIVE movements: 551, 552"
}
```

> **Nota de alcance.** El catálogo de métricas realmente implementado en `internal/observability/metrics.go` es: `eo_connected_players`, `eo_connected_websockets`, `eo_active_units`, `eo_active_movements`, `eo_persistence_queue_depth`, `eo_game_tick_duration_seconds`, `eo_game_tick_overruns_total`, `eo_commands_total{type,result}`, `eo_ws_messages_total{direction,type}`, `eo_protocol_errors_total{code}`, `eo_pathfinding_requests_total{result}`, `eo_pathfinding_duration_seconds`, `eo_database_latency_seconds` y `eo_redis_latency_seconds`. **No incluye** ninguna métrica de violación de invariantes.
>
> Este catálogo propone `eo_invariant_violations_total{inv, severity}` como **extensión pendiente de ADR**. Hasta que ese ADR se apruebe, la señal obligatoria y suficiente es el log `msg="invariant_violation"` con el campo `inv_id`, que no requiere ningún cambio en el código de métricas. Las métricas que sí existen (`eo_game_tick_overruns_total`, `eo_persistence_queue_depth`, `eo_protocol_errors_total`…) se citan en las fichas cuando la violación se correlaciona con ellas.

## 6. Convención de nombres de test

Cada invariante tiene **al menos un test cuyo nombre contiene su ID** con guiones bajos. Esto hace que la trazabilidad invariante → test sea `grep`-able y verificable en CI.

```go
func Test_INV_MOVE_001_SecondOrderCancelsPreviousMovement(t *testing.T) { ... }
```

```ts
describe('INV-SEC-005 message size limit', () => { ... })
```

Niveles de test según canon §19: `unit`, `integration`, `contract`, `simulation` (con `FakeClock`), `recovery` y `load` (diferido).

Los de **integración** están doblemente cerrados en el repositorio real: llevan `//go:build integration` y además comprueban `os.Getenv("EO_INTEGRATION") == "1"`, saltándose con un mensaje que remite a `pnpm run db:up`. Se ejecutan con `EO_INTEGRATION=1 go test -tags=integration ./...` y requieren PostgreSQL y Redis reales vía `docker compose`. Como el daemon de Docker no arrancó en la máquina de desarrollo, esa suite está **escrita pero no verificada**, y este catálogo la etiqueta siempre como «diseñado, aún no ejecutado» en lugar de contarla como cobertura.

Los tests **que ya existen** en el repositorio están escritos en español y **no** contienen el ID en su nombre: `TestEjemploNumericoCanonico`, `TestAutomataDePresencia`, `TestNuevaOrdenReemplazaLaAnterior`. Esa es la realidad del árbol hoy y este catálogo la refleja tal cual: cada ficha cita el nombre real del test que la cubre. La convención `Test_INV_*` se aplica a los tests **nuevos** que se escriban específicamente para cerrar un invariante sin cobertura.

Regla de CI **propuesta** (no implementada) para el paso `lint`: un script recorre `docs/invariants/*.md`, extrae todos los IDs y comprueba que cada uno aparece o bien en un nombre de test del repositorio, o bien en la fila `Cobertura` de su ficha con una etiqueta explícita de ausencia. La segunda mitad es imprescindible: sin ella, la regla obligaría a inventar tests vacíos para invariantes cuya mecánica todavía no existe. Detalle en [../testing/strategy.md](../testing/strategy.md).

## 7. Tabla resumen

**90 invariantes** repartidos en diez familias y nueve archivos. `Sev` = severidad; `Aplicación` usa los tokens de §4. La columna `Cob.` resume la fila `Cobertura` de cada ficha:

| Marca | Significado |
|---|---|
| ✓ | **Cubierto** por al menos un test que existe y pasa hoy |
| ~ | **Parcial**: hay test real para una parte del enunciado |
| ○ | **Sin cobertura** en verde: diseñado y no ejecutado, o todavía inexistente |

La columna `Test` cita el test **real** cuando lo hay, y el nombre **previsto** cuando no. Cada ficha lo detalla.

### WORLD — [world.md](world.md)

| ID | Invariante | Sev | Aplicación | Cob. | Test |
|---|---|---|---|---|---|
| INV-WORLD-001 | Toda coordenada válida cae en `[0, EO_WORLD_WIDTH) × [0, EO_WORLD_HEIGHT)` | CRITICO | TYPE, BOUNDARY, DOMAIN, TEST | ✓ | `TestLimitesDelMundo` |
| INV-WORLD-002 | Todo tile referenciado existe en el mundo cargado | CRITICO | DOMAIN, TEST | ~ | `TestNewRechazaDimensionesIncoherentes` |
| INV-WORLD-003 | Ningún waypoint de un path es un tile bloqueado | CRITICO | DOMAIN, TEST | ✓ | `TestRodeaElObstaculo` |
| INV-WORLD-004 | `tickNumber` es estrictamente monótono creciente | CRITICO | TYPE, DOMAIN, TEST | ~ | `TestTickDebeDividirAMil` |
| INV-WORLD-005 | El mapa generado desde un mismo seed es idéntico byte a byte | ALTO | DOMAIN, TEST | ✓ | `TestGeneracionEsDeterminista` |
| INV-WORLD-006 | Cada tile pertenece a exactamente un chunk | ALTO | TYPE, DOMAIN, TEST | ✓ | `TestConversionTileChunk` |

### PLAYER — [player.md](player.md)

| ID | Invariante | Sev | Aplicación | Cob. | Test |
|---|---|---|---|---|---|
| INV-PLAYER-001 | Un jugador sólo puede comandar unidades que le pertenecen | CRITICO | DB, BOUNDARY, DOMAIN, TEST | ✓ | `TestNoSePuedeComandarUnaUnidadAjena` |
| INV-PLAYER-002 | Todo jugador tiene exactamente una civilization y exactamente una faction | ALTO | DB, TEST | ○ | `TestBootstrapCreaMundoCompletoDelJugador` |
| INV-PLAYER-003 | Un jugador recién creado tiene exactamente una ciudad y exactamente 3 `VILLAGER` | ALTO | DOMAIN, TEST | ○ | `TestBootstrapCreaMundoCompletoDelJugador` |
| INV-PLAYER-004 | La identidad del jugador de una sesión proviene del ticket verificado | CRITICO | TYPE, BOUNDARY, TEST | ✓ | `TestTicketValidoSeVerifica` |
| INV-PLAYER-005 | `players.id` es `uuid` y único; ningún otro mecanismo identifica a un jugador | ALTO | DB, TYPE, TEST | ○ | `Test_INV_PLAYER_005_PlayerIdIsUniqueUuid` |
| INV-PLAYER-006 | `username` es único y cumple `^[A-Za-z0-9_-]{3,24}$` en boundary y en `CHECK` | ALTO | DB, BOUNDARY, TEST | ○ | `Test_INV_PLAYER_006_UsernameFormatAndUniqueness` |
| INV-PLAYER-007 | Ninguna tabla ni log contiene contraseña, hash, ticket completo ni `EO_AUTH_JWT_SECRET` | CRITICO | DB, BOUNDARY, TEST | ~ | `TestSecretoDemasiadoCorto` |
| INV-PLAYER-008 | El mundo en RAM sólo conoce jugadores ya confirmados en PostgreSQL | ALTO | DOMAIN, TEST | ○ | `Test_INV_PLAYER_008_WorldLearnsPlayerOnlyAfterCommit` |

### CITY — [city.md](city.md)

| ID | Invariante | Sev | Aplicación | Cob. | Test |
|---|---|---|---|---|---|
| INV-CITY-001 | Una ciudad tiene exactamente un owner | CRITICO | DB, TYPE, TEST | ○ | `TestBootstrapCreaMundoCompletoDelJugador` |
| INV-CITY-002 | `population` nunca supera `population_limit` | ALTO | DB, DOMAIN, TEST | ✓ | `TestLimiteDePoblacion` |
| INV-CITY-003 | `population_limit` deriva de la era vigente | ALTO | DB, DOMAIN, TEST | ○ | `Test_INV_CITY_003_PopulationLimitDerivesFromEra` |
| INV-CITY-004 | `presence_state` sólo toma `ONLINE`, `OFFLINE_PENDING` o `PROTECTED` | CRITICO | DB, TYPE, TEST | ✓ | `TestEstadosDePresenciaValidos` |
| INV-CITY-005 | Toda transición de `presence_state` pertenece al conjunto permitido | ALTO | DOMAIN, TEST | ✓ | `TestAutomataDePresencia` |
| INV-CITY-006 | Una ciudad `PROTECTED` no recibe acciones prohibidas por la protección | CRITICO | DOMAIN, TEST | ~ | `TestIsProtected` |
| INV-CITY-007 | El centro de la ciudad está sobre un tile válido y transitable | ALTO | DOMAIN, TEST | ○ | `TestBootstrapCreaMundoCompletoDelJugador` |
| INV-CITY-008 | Las zonas urbanas de dos ciudades nunca comparten tile (centro único, 24 tiles de separación) | ALTO | DB, DOMAIN, TEST | ○ | `Test_INV_CITY_008_UrbanAreasDoNotOverlap` |
| INV-CITY-009 | `population` es el recuento de `units` de la ciudad con `status <> 'DEAD'` | ALTO | DOMAIN, TEST | ○ | `Test_INV_CITY_009_PopulationMatchesLiveUnitCount` |
| INV-CITY-010 | Fundar una ciudad no muta el `TerrainType` de ningún tile | ALTO | DOMAIN, TEST | ✓ | `TestCapaDeOcupacionNoMutaElTerreno` |
| INV-CITY-011 | `presence_state <> 'ONLINE'` implica `last_offline_at IS NOT NULL` | ALTO | DOMAIN, TEST | ~ | `TestCicloDePresenciaYProteccion` |
| INV-CITY-012 | `presence_state = 'ONLINE'` implica `protection_until IS NULL` | ALTO | DOMAIN, TEST | ~ | `TestCicloDePresenciaYProteccion` |
| INV-CITY-013 | La protección nunca se concede antes de vencer el cooldown completo | CRITICO | DOMAIN, TEST | ✓ | `TestShouldEngageProtection` |
| INV-CITY-014 | Ninguna ciudad sigue protegida mientras su dueño tenga una sesión viva | CRITICO | DOMAIN, TEST | ✓ | `TestCicloDePresenciaYProteccion` |
| INV-CITY-015 | `last_online_at` y `last_offline_at` son monótonos no decrecientes en una ejecución | MEDIO | DOMAIN, TEST | ○ | `Test_INV_CITY_015_PresenceTimestampsAreMonotonic` |
| INV-CITY-016 | `simulation.Hydrate` no escribe `cities`: es idempotente para la presencia | MEDIO | TEST | ○ | `Test_INV_CITY_016_HydrateIsIdempotentForPresence` |

### UNIT — [units.md](units.md)

| ID | Invariante | Sev | Aplicación | Cob. | Test |
|---|---|---|---|---|---|
| INV-UNIT-001 | La posición de una unidad referencia un tile válido y transitable | CRITICO | DOMAIN, TEST | ✓ | `TestRechazosDeMovimiento` |
| INV-UNIT-002 | Una unidad `DEAD` no acepta órdenes | ALTO | DB, DOMAIN, TEST | ✓ | `TestRechazosDeMovimiento` |
| INV-UNIT-003 | Una unidad `GARRISONED` no ocupa tile del mundo | ALTO | DOMAIN, TEST | ~ | `TestRechazosDeMovimiento` |
| INV-UNIT-004 | `hp` está en `[0, max_hp]` | ALTO | DB, DOMAIN, TEST | ○ | `Test_INV_UNIT_004_HpWithinRange` |
| INV-UNIT-005 | `status = MOVING` si y sólo si existe un movimiento `ACTIVE` | CRITICO | DB, DOMAIN, TEST | ✓ | `TestVerticalSliceMovimiento` |
| INV-UNIT-006 | Una unidad pertenece a un único jugador | CRITICO | DB, TYPE, TEST | ~ | `TestNoSePuedeComandarUnaUnidadAjena` |
| INV-UNIT-007 | `GARRISONED` implica `city_id IS NOT NULL` y fila en `garrisons` | ALTO | DB, DOMAIN, TEST | ○ | `Test_INV_UNIT_007_GarrisonedHasCityAndGarrisonRow` |
| INV-UNIT-008 | `HIDDEN` implica Safe Zone activa y reposo; moverse deja de ocultar | MEDIO | DOMAIN, TEST | ○ | `Test_INV_UNIT_008_HiddenImpliesActiveSafeZone` |
| INV-UNIT-009 | Todo `unit_type` persistido resuelve en el catálogo de `unit.Lookup` | ALTO | DOMAIN, TEST | ~ | `TestVerticalSliceMovimiento` |
| INV-UNIT-010 | `player_id` y `unit_type` son inmutables tras la creación en MVP | MEDIO | DOMAIN, TEST | ○ | `Test_INV_UNIT_010_OwnerAndTypeAreImmutable` |
| INV-UNIT-011 | `chunk_x` y `chunk_y` son siempre el chunk de `(x, y)` | ALTO | DOMAIN, TEST | ~ | `TestFlushPositionsEscribeElLoteYMantieneElChunk` |

### MOVEMENT — [movement.md](movement.md)

| ID | Invariante | Sev | Aplicación | Cob. | Test |
|---|---|---|---|---|---|
| INV-MOVE-001 | Una unidad tiene como máximo un movimiento `ACTIVE` | CRITICO | DB, DOMAIN, TEST | ✓ | `TestNuevaOrdenReemplazaLaAnterior` |
| INV-MOVE-002 | La polilínea es no vacía y su primer waypoint es el origen con `tMs = 0` | CRITICO | DB, DOMAIN, TEST | ✓ | `TestValidateComprobaciones` |
| INV-MOVE-003 | Los `tMs` de la polilínea son estrictamente crecientes | CRITICO | DOMAIN, TEST | ✓ | `TestNingunPasoEsInstantaneo` |
| INV-MOVE-004 | Waypoints transitables y contiguos en 8-vecindad, sin corner cutting | CRITICO | DOMAIN, TEST | ✓ | `TestValidateComprobaciones` |
| INV-MOVE-005 | `arrival_time_ms = start_time_ms + tMs` del último waypoint | ALTO | DB, DOMAIN, TEST | ✓ | `TestMovementCicloDeVida` |
| INV-MOVE-006 | La posición autoritativa derivada nunca adelanta al tiempo del servidor | CRITICO | DOMAIN, TEST | ✓ | `TestPositionAt` |
| INV-MOVE-007 | Un movimiento terminado nunca vuelve a `ACTIVE` | CRITICO | DB, DOMAIN, TEST | ✓ | `TestEstadosDeMovimiento` |
| INV-MOVE-008 | Cancelar deja la unidad exactamente en el último tile alcanzado | ALTO | DOMAIN, TEST | ✓ | `TestCancelacionExplicita` |
| INV-MOVE-009 | La posición es función pura de `(path, start_time_ms, T)`, sin replay de ticks | ALTO | DOMAIN, TEST | ✓ | `TestSimulacionEsReproducible` |
| INV-MOVE-010 | La posición sólo cambia a tiles de la propia polilínea, nunca desde datos del cliente | CRITICO | DOMAIN, TEST | ✓ | `TestVerticalSliceMovimiento` |
| INV-MOVE-011 | Ningún mensaje cliente→servidor transporta posición, ruta, `tMs`, velocidad ni llegada | CRITICO | TYPE, BOUNDARY, TEST | ✓ | `TestElComandoDeMovimientoNoAdmiteRuta` |
| INV-MOVE-012 | El último waypoint es exactamente el destino pedido; no hay rutas parciales | MEDIO | DOMAIN, TEST | ✓ | `TestVerticalSliceMovimiento` |
| INV-MOVE-013 | Redondeo por segmento con aritmética entera pura; resultado idéntico en toda plataforma | ALTO | DOMAIN, TEST | ✓ | `TestEjemploNumericoCanonico` |
| INV-MOVE-014 | Moverse al tile propio se acepta sin crear movimiento y cancela el activo | MEDIO | DOMAIN, TEST | ✓ | `TestMoverseAlSitioDondeYaEstas` |

### PERSISTENCE — [persistence.md](persistence.md)

| ID | Invariante | Sev | Aplicación | Cob. | Test |
|---|---|---|---|---|---|
| INV-PERSIST-001 | Una transacción durable confirmada no se pierde por una desconexión de WebSocket | CRITICO | DB, DOMAIN, TEST | ✓ | `TestElMovimientoContinuaConElJugadorDesconectado` |
| INV-PERSIST-002 | Tras reinicio el estado reconstruido es consistente con lo confirmado antes del corte | CRITICO | DOMAIN, TEST | ✓ | `TestRecuperacionCompletaTrasReinicio` |
| INV-PERSIST-003 | Ningún dato durable existe únicamente en Redis | CRITICO | DOMAIN, TEST | ○ | `TestPresenciaSeRegistraYExpira` |
| INV-PERSIST-004 | El flush periódico nunca escribe una posición que contradiga un movimiento `ACTIVE` | ALTO | DOMAIN, TEST | ~ | `TestVolcadoPeriodicoNoOcurreEnCadaTick` |
| INV-PERSIST-005 | Las migraciones son ordenadas, idempotentes en su aplicación e inmutables tras publicarse | ALTO | DB, DOMAIN, TEST | ○ | `Test_INV_PERSIST_005_MigrationsOrderedIdempotentImmutable` |

### SECURITY — [security.md](security.md)

| ID | Invariante | Sev | Aplicación | Cob. | Test |
|---|---|---|---|---|---|
| INV-SEC-001 | El estado provisto por el cliente jamás muta directamente el estado autoritativo | CRITICO | TYPE, BOUNDARY, TEST | ✓ | `TestPayloadConCamposExtraNoAportaEstado` |
| INV-SEC-002 | Toda conexión WebSocket está autenticada antes de aceptar cualquier comando | CRITICO | TYPE, BOUNDARY, TEST | ✓ | `TestPreAuthSoloAdmiteElHandshake` |
| INV-SEC-003 | Un ticket sólo puede canjearse una vez | CRITICO | BOUNDARY, TEST | ✓ | `TestTicketSoloSeConsumeUnaVez` |
| INV-SEC-004 | Todo comando valida ownership antes de ejecutarse | CRITICO | DOMAIN, TEST | ✓ | `TestRechazosDeMovimiento` |
| INV-SEC-005 | Los mensajes que superan el límite de tamaño o de tasa se rechazan sin procesarse | ALTO | BOUNDARY, TEST | ~ | `TestValoresPorDefectoDelCanon` |
| INV-SEC-006 | Las credenciales de infraestructura nunca llegan al cliente ni al repositorio | CRITICO | BOUNDARY, TEST | ~ | `TestObligatoriasAusentes` |
| INV-SEC-007 | Un `requestId` repetido nunca produce un segundo efecto | CRITICO | BOUNDARY, TEST | ✓ | `TestComandoDuplicadoSeIgnora` |

### TERRITORY — [territory.md](territory.md)

| ID | Invariante | Sev | Aplicación | Cob. | Test |
|---|---|---|---|---|---|
| INV-TERR-001 | El rectángulo de un territorio está ordenado y contenido en el mundo | ALTO | DB, DOMAIN, TEST | ○ | `Test_INV_TERR_001_TerritoryBoundsValid` |
| INV-TERR-002 | Dos territorios no comparten ningún tile | ALTO | DOMAIN, TEST | ○ | `Test_INV_TERR_002_TerritoriesDoNotOverlap` |
| INV-TERR-003 | Un territorio tiene como máximo una fila de control, y toda fila referencia un territorio | ALTO | DB, TEST | ○ | `Test_INV_TERR_003_OneControlRowPerTerritory` |
| INV-TERR-004 | `owner_type = 'NONE'` si y sólo si `owner_id IS NULL` | CRITICO | DB, TEST | ○ | `Test_INV_TERR_004_OwnerTypeIdConsistency` |
| INV-TERR-005 | `owner_type = 'PLAYER'` implica que `owner_id` es un `players.id` existente | ALTO | DOMAIN, TEST | ○ | `Test_INV_TERR_005_PlayerOwnerExists` |
| INV-TERR-006 | En MVP, `control_points = 0` y `contested = false` en toda fila de control | MEDIO | TEST | ○ | `Test_INV_TERR_006_NoCaptureProgressInMVP` |
| INV-TERR-007 | `owner_type <> 'NONE'` implica `captured_at IS NOT NULL` | MEDIO | DOMAIN, TEST | ○ | `Test_INV_TERR_007_OwnedTerritoryHasCapturedAt` |
| INV-TERR-008 | El índice `territoryOfTile` es función pura de `territories` | MEDIO | TEST | ○ | `Test_INV_TERR_008_TerritoryIndexIsDeterministic` |
| INV-TERR-009 | Todo cambio de control emite `territory.update` a la huella de chunks | ALTO | BOUNDARY, TEST | ○ | `Test_INV_TERR_009_ControlChangeEmitsTerritoryUpdate` |

### SAFE ZONES — [territory.md](territory.md)

| ID | Invariante | Sev | Aplicación | Cob. | Test |
|---|---|---|---|---|---|
| INV-SAFE-001 | Dos Safe Zones no comparten ningún tile | ALTO | DOMAIN, TEST | ○ | `Test_INV_SAFE_001_SafeZonesDoNotOverlap` |
| INV-SAFE-002 | Ningún tile de una Safe Zone es intransitable | ALTO | DOMAIN, TEST | ○ | `Test_INV_SAFE_002_SafeZoneTilesAreWalkable` |
| INV-SAFE-003 | `HIDDEN` implica estar en una zona y sin movimiento `ACTIVE` | CRITICO | DOMAIN, TEST | ○ | `Test_INV_SAFE_003_HiddenImpliesInZoneAndAtRest` |
| INV-SAFE-004 | Ninguna unidad `HIDDEN` aparece en un delta dirigido a un tercero | CRITICO | BOUNDARY, TEST | ○ | `Test_INV_SAFE_004_HiddenInvisibleToThirdParties` |
| INV-SAFE-005 | El rectángulo de una Safe Zone está ordenado y contenido en el mundo | ALTO | DB, DOMAIN, TEST | ○ | `Test_INV_SAFE_005_SafeZoneBoundsValid` |
| INV-SAFE-006 | Ninguna Safe Zone se solapa con la zona urbana de una ciudad | MEDIO | TEST | ○ | `Test_INV_SAFE_006_SafeZonesDoNotOverlapCities` |
| INV-SAFE-007 | El índice `safeZoneOfTile` es función pura de `(safe_zones, terreno)` | MEDIO | TEST | ○ | `Test_INV_SAFE_007_SafeZoneIndexIsDeterministic` |

### GARRISON — [diplomacy.md](diplomacy.md)

| ID | Invariante | Sev | Aplicación | Cob. | Test |
|---|---|---|---|---|---|
| INV-GARR-001 | Una unidad tiene como máximo una guarnición abierta (`unit_id` es `PRIMARY KEY`) | CRITICO | DB, TEST | ○ | `Test_INV_GARR_001_OneOpenGarrisonPerUnit` |
| INV-GARR-002 | `status = 'GARRISONED'` si y sólo si existe fila en `garrisons` | CRITICO | DOMAIN, TEST | ~ | `TestRechazosDeMovimiento` |
| INV-GARR-003 | `units.city_id` es igual a `garrisons.city_id` cuando hay guarnición | ALTO | DOMAIN, TEST | ○ | `Test_INV_GARR_003_UnitCityMatchesGarrisonCity` |
| INV-GARR-004 | Guarnecer en ciudad ajena exige un `treaty` `ACTIVE` con `allows_garrison` en la entrada | CRITICO | DOMAIN, TEST | ~ | `TestCatalogoDeErroresCoincideConElExportado` |
| INV-GARR-005 | Ninguna unidad `GARRISONED` figura en la capa de ocupación | ALTO | DOMAIN, TEST | ○ | `Test_INV_GARR_005_GarrisonedNotInBlockedOverlay` |
| INV-GARR-006 | Ninguna unidad `GARRISONED` aparece en un delta dirigido a un tercero | CRITICO | BOUNDARY, TEST | ○ | `Test_INV_GARR_006_GarrisonedInvisibleToThirdParties` |
| INV-GARR-007 | El ownership no cambia al entrar ni al salir de la guarnición | ALTO | DOMAIN, TEST | ○ | `Test_INV_GARR_007_OwnershipPreservedAcrossGarrison` |

### Distribución

Por severidad:

| Severidad | Cantidad |
|---|---|
| CRITICO | 39 |
| ALTO | 40 |
| MEDIO | 11 |
| **Total** | **90** |

Por familia:

| Familia | Rango asignado | Nº | Archivo |
|---|---|---|---|
| `INV-WORLD` | 001–006 | 6 | [world.md](world.md) |
| `INV-PLAYER` | 001–008 | 8 | [player.md](player.md) |
| `INV-CITY` | 001–016 | 16 | [city.md](city.md) |
| `INV-UNIT` | 001–011 | 11 | [units.md](units.md) |
| `INV-MOVE` | 001–014 | 14 | [movement.md](movement.md) |
| `INV-PERSIST` | 001–005 | 5 | [persistence.md](persistence.md) |
| `INV-SEC` | 001–007 | 7 | [security.md](security.md) |
| `INV-TERR` | 001–009 | 9 | [territory.md](territory.md) |
| `INV-SAFE` | 001–007 | 7 | [territory.md](territory.md) |
| `INV-GARR` | 001–007 | 7 | [diplomacy.md](diplomacy.md) |

Los rangos son **contiguos y sin huecos**: el siguiente ID libre de cada familia es el máximo más uno. Ningún número está quemado todavía, porque ningún invariante se ha derogado.

### Estado de cobertura

| Marca | Nº | Lectura |
|---|---|---|
| ✓ Cubierto | 36 | Tienen al menos un test que existe y pasa hoy |
| ~ Parcial | 15 | Hay test real para parte del enunciado |
| ○ Sin cobertura | 39 | Diseñado y no ejecutado, o todavía inexistente |
| **Total** | **90** | |

Los 39 sin cobertura se concentran donde cabe esperar, y por dos causas distintas que no conviene mezclar:

1. **Tests de integración diseñados pero no ejecutados.** Requieren PostgreSQL y Redis reales vía `docker compose`, y el daemon de Docker no arrancó en la máquina de desarrollo. Están escritos; no están verificados. Afecta sobre todo a `INV-PLAYER-002/003`, `INV-CITY-001/003/007` y `INV-PERSIST-003/005`.
2. **Mecánica todavía no implementada.** Las familias `INV-TERR`, `INV-SAFE` y `INV-GARR` describen propiedades de entidades que existen en el esquema desde la migración `000001` pero cuya lógica está diferida (canon §11). Sus garantías `DB` **sí** están vigentes; sus garantías `DOMAIN` son diseño.

Los once invariantes `MEDIO` desmienten la nota anterior de este catálogo, que afirmaba que no había ninguno en el MVP: están repartidos entre `CITY` (015, 016), `UNIT` (008, 010), `MOVE` (012, 014), `SAFE` (006, 007) y `TERR` (006, 007, 008). Cubren exactamente lo que la escala prevé: propiedades de calidad —determinismo, reproducibilidad, monotonía, idempotencia, alcance de MVP— cuya violación degrada garantías sin corromper estado durable.

### Nota sobre las anclas de los enlaces

Las referencias cruzadas de este catálogo enlazan al archivo de la familia y añaden un fragmento con el ID en minúsculas —`archivo.md`, más `#inv-xxx-nnn`—, que es la convención establecida en todo el árbol `docs/`. El **archivo** siempre existe —eso se puede verificar mecánicamente y se verifica—; el fragmento `#inv-xxx-nnn` es una convención de lectura, no el slug que GitHub genera para un encabezado que incluye además el título del invariante. Normalizar todas las anclas es trabajo pendiente y afecta a todo el árbol de documentación, no sólo a este directorio: **TBD (fuera de MVP)**.

## 8. Cómo usar este catálogo

1. **Al escribir una spec funcional.** La sección «Invariantes» de la spec (canon §22) **cita IDs**, no los redefine. Si un feature necesita una propiedad nueva, se añade aquí primero, continuando la numeración de su familia. Una spec que renumera o redefine un ID ya asignado introduce una colisión, y una colisión de IDs es un defecto que hay que corregir en la spec, nunca aquí.
2. **Al implementar.** Cada guarda defensiva cita el ID en comentario y en el log de violación.
3. **Al escribir tests.** El nombre del test lleva el ID. La suite de invariantes es la columna vertebral de la Definition of Done (canon §19).
4. **Al revisar un PR.** «¿Qué invariante puede romper este cambio?» es una pregunta obligatoria de la revisión. Un PR que toca movimiento sin tocar ningún test `Test_INV_MOVE_*` merece justificación explícita.

## 9. Archivos de este catálogo

| Archivo | Familias | Nº |
|---|---|---|
| [world.md](world.md) | `INV-WORLD` | 6 |
| [player.md](player.md) | `INV-PLAYER` | 8 |
| [city.md](city.md) | `INV-CITY` | 16 |
| [units.md](units.md) | `INV-UNIT` | 11 |
| [movement.md](movement.md) | `INV-MOVE` | 14 |
| [persistence.md](persistence.md) | `INV-PERSIST` | 5 |
| [security.md](security.md) | `INV-SEC` | 7 |
| [territory.md](territory.md) | `INV-TERR`, `INV-SAFE` | 16 |
| [diplomacy.md](diplomacy.md) | `INV-GARR` | 7 |

## 10. Documentos relacionados

- [../architecture/game-loop.md](../architecture/game-loop.md) — orden fijo de fases del tick, base de INV-WORLD-004 e INV-MOVE-006.
- [../architecture/persistence.md](../architecture/persistence.md) — capas de estado y estrategia write-through / flush.
- [../architecture/pathfinding.md](../architecture/pathfinding.md) — A\*, escalas de coste y heurística; base de INV-MOVE-004.
- [../architecture/networking.md](../architecture/networking.md) — límites de transporte e interest management.
- [../operations/monitoring.md](../operations/monitoring.md) — logging estructurado y catálogo de métricas.
- [../specs/websocket-protocol.md](../specs/websocket-protocol.md) — envelopes, límites, códigos de cierre y de error.
- [../database/schema.md](../database/schema.md) — DDL canónica y nombres reales de constraints.
- [../testing/strategy.md](../testing/strategy.md) — niveles de test y gating por `EO_INTEGRATION=1`.
- [../decisions/ADR-011-movement-timed-polyline.md](../decisions/ADR-011-movement-timed-polyline.md) — decisión de la polilínea temporizada.
- [../decisions/ADR-012-database-migrations.md](../decisions/ADR-012-database-migrations.md) — disciplina de migraciones.

> Las rutas relativas asumen la estructura de `docs/` definida por el grupo de arquitectura. Si un documento se mueve, se actualiza esta sección y los enlaces de las fichas.
