# Invariantes de Diplomacia y Guarnición (INV-GARR-xxx)

Propiedades de la guarnición de unidades en ciudades: unicidad, coherencia con `units`, autorización por tratado, ocupación y visibilidad.

Formato y severidades: [README.md](README.md). Estados de unidad: [units.md](units.md). Ciudades: [city.md](city.md). Spec: [../specs/garrison.md](../specs/garrison.md). Decisiones: [ADR-017](../decisions/ADR-017-garrison-commands.md).

---

## Contexto: la guarnición, de extremo a extremo

Desde [ADR-017](../decisions/ADR-017-garrison-commands.md) los jugadores guarnecen con dos comandos de red, `unit.garrison` y `unit.ungarrison`. El recorrido completo es éste:

| Pieza | Dónde | Qué hace |
|---|---|---|
| Reglas | `internal/domain/garrison` | `CanEnter`, `CanLeave` y `ReentryTile` deciden; no escriben ni conocen el protocolo. |
| Tratados | `internal/domain/diplomacy`, `postgres.TreatyRepo` | Ciclo de vida y la pregunta «¿este par tiene un tratado `ACTIVE` con `allows_garrison`?». Los tratados se crean por vía administrativa: no hay comandos de diplomacia. |
| Simulación | `internal/game/simulation/garrison.go` | La entrada en dos fases —el tick valida lo que está en RAM, la transacción decide el tratado— y la salida decidida por el tick con compensación. Filtro de visibilidad por destinatario. |
| Persistencia | `postgres.GarrisonRepo` | `Enter` y `Leave`, cada uno en una transacción con su evento de `world_events`, idempotentes por construcción. |
| Arranque | `internal/game/loadcheck` | Deriva `units.status` de `garrisons` y reconstruye el índice unidad → anfitriona ([EO-119](../roadmap/backlog.md)). |

| Nivel | Estado real |
|---|---|
| `DB` | **Vigente.** `garrisons.unit_id` es `PRIMARY KEY`, sus dos claves foráneas y `garrisons_city_idx` están aplicadas; `treaties` tiene sus cuatro constraints y su índice parcial único. |
| `DOMAIN` | **Vigente.** Entrada y salida implementadas y alcanzables por la red. |
| `BOUNDARY` | **Vigente.** `entity.despawn { reason: GARRISONED }` al entrar, `entity.spawn` al salir, y el snapshot filtrado por destinatario. |
| `TEST` | **Vigente.** Cada ficha nombra los suyos. Lo que queda fuera es la expulsión diferida, que sigue sin implementar ([INV-GARR-008](#inv-garr-008)). |

### La tabla `garrisons` y el par canónico de `treaties`

```sql
CREATE TABLE garrisons (
    unit_id    bigint      PRIMARY KEY REFERENCES units (id) ON DELETE CASCADE,
    city_id    bigint      NOT NULL REFERENCES cities (id) ON DELETE CASCADE,
    entered_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX garrisons_city_idx ON garrisons (city_id);
```

La forma de la tabla **es** el invariante principal: al ser `unit_id` la clave primaria, «una unidad tiene como máximo una guarnición abierta» no es una regla que haya que comprobar, es una imposibilidad física ([INV-GARR-001](#inv-garr-001)).

`treaties` complementa el cuadro con el **par canónico**: `CHECK (player_a_id < player_b_id)` obliga a un orden fijo entre los dos firmantes, de modo que un tratado A–B y otro B–A no puedan coexistir como filas distintas; `CHECK (player_a_id <> player_b_id)` impide firmar consigo mismo; y `treaties_one_active_per_pair_and_type` —índice único parcial `WHERE status = 'ACTIVE'`— permite como máximo un tratado vigente de cada tipo entre dos jugadores. `allows_garrison boolean NOT NULL DEFAULT false` es el permiso concreto del que depende [INV-GARR-004](#inv-garr-004).

### Por qué guarnición y ocultamiento se documentan por separado

`GARRISONED` y `HIDDEN` ([territory.md](territory.md), familia `INV-SAFE-*`) coinciden en que retiran la unidad de la vista de terceros, y el código los filtra con la misma función (`simulation.VisibleTo`). Se diferencian en todo lo demás:

| | `GARRISONED` | `HIDDEN` |
|---|---|---|
| Está en el mapa | **No**: está dentro de una ciudad | Sí, en su tile |
| Acepta órdenes | Sólo `unit.ungarrison`; `unit.move` responde `UNIT_GARRISONED` | Sí; moverse la revela |
| Condición | Fila en `garrisons` | Estar en una Safe Zone, en reposo |
| Requiere autorización | Sí, si la ciudad es ajena | No |

En el MVP **ninguna** de las dos ocupa la capa de ocupación del mundo: esa capa sólo contiene murallas, y la colisión entre unidades está fuera de alcance ([../specs/movement.md](../specs/movement.md)).

---

<a id="inv-garr-001"></a>
## INV-GARR-001 — Una unidad tiene como máximo una guarnición abierta

| Campo | Valor |
|---|---|
| Severidad | CRITICO |
| Aplicación | DB, DOMAIN, TEST |
| Milestone | M7 |
| Política ante violación | FAIL_FAST (constraint) |
| Cobertura | **Cubierto** por `TestUnaUnidadNoPuedeTenerDosGuarnicionesAbiertas` y `TestUnaEntradaRepetidaEsIdempotenteYNoDuplicaElEvento` (integration, `internal/persistence/postgres`) |

**Enunciado.** Una unidad tiene como máximo una guarnición abierta: `garrisons.unit_id` es `PRIMARY KEY`.

**Razón.** Es el mismo problema que [INV-MOVE-001](movement.md#inv-move-001) resuelve para los movimientos, y se resuelve igual: haciendo imposible el estado inválido en el esquema en lugar de comprobarlo en código.

Dos guarniciones abiertas para la misma unidad significan que la unidad está en dos ciudades a la vez, y a partir de ahí todo lo que dependa de dónde está tiene dos respuestas: qué ciudad la aloja, por qué anillo sale. El sistema tendría que elegir, y cualquier criterio es arbitrario. Hay además una vía de abuso directa, análoga a la de los movimientos duplicados: si dos órdenes concurrentes crearan dos guarniciones, una sola unidad reforzaría dos ciudades a la vez.

Que la clave sea `unit_id` y no un `id` sintético con un `UNIQUE` encima dice que **la identidad de una guarnición es la unidad guarnecida**. Una fila existe mientras la unidad esté dentro y desaparece al salir; el historial está en `world_events`.

**Cómo se garantiza.**

- `DB` — `garrisons.unit_id bigint PRIMARY KEY REFERENCES units (id) ON DELETE CASCADE`: la segunda fila es estructuralmente imposible, y una unidad borrada no deja guarniciones huérfanas.
- `DB` — `city_id bigint NOT NULL REFERENCES cities (id) ON DELETE CASCADE`: si la ciudad desaparece, la guarnición desaparece con ella.
- `DOMAIN` — `GarrisonRepo.Enter` lee la fila de la unidad con `FOR UPDATE` antes de insertar: si ya está en **esa** ciudad, la entrada ya está escrita y devuelve éxito sin un segundo evento —es el reintento de la cola tras perder la confirmación del `COMMIT`—; si está en **otra**, devuelve `garrison.ErrAlreadyGarrisoned`, que llega al jugador como `UNIT_GARRISONED`, en lugar de un error de clave duplicada.
- `DOMAIN` — en la RAM, una unidad que está entrando o ya dentro rechaza un segundo `unit.garrison` antes de llegar a la base (`UNIT_NOT_MOVABLE` o `UNIT_GARRISONED`).

**Cómo se verifica.**

- `TestUnaUnidadNoPuedeTenerDosGuarnicionesAbiertas` (integration) — un segundo `INSERT` con el mismo `unit_id` lo rechaza `garrisons_pkey`; borrar la ciudad anfitriona borra la guarnición.
- `TestUnaEntradaRepetidaEsIdempotenteYNoDuplicaElEvento` (integration) — repetir la entrada en la misma ciudad es un éxito con un solo `UnitGarrisoned`; intentarla en otra devuelve `ErrAlreadyGarrisoned` y la guarnición abierta no cambia de ciudad. Verificado por mutación: sin la rama idempotente, el test falla.

**Violación en runtime.** Imposible en escritura. No hay lectura que pueda encontrar dos filas para una unidad mientras exista la clave primaria.

---

<a id="inv-garr-002"></a>
## INV-GARR-002 — `GARRISONED` si y sólo si existe fila de guarnición

| Campo | Valor |
|---|---|
| Severidad | CRITICO |
| Aplicación | DOMAIN, TEST |
| Milestone | M7 |
| Política ante violación | REPAIR (derivar desde `garrisons`) |
| Cobertura | **Cubierto**: en disco por `TestGuarnecerConTratadoActivoDejaLaUnidadGarrisonedYPersistida` y `TestSalirDeLaGuarnicionDevuelveLaUnidadAlMapaYBorraLaFila` (integration); en la RAM por `TestMientrasEntraLaUnidadNoAceptaOrdenesNiSeVuelca` y `TestUnaSalidaQueNoSePuedeEscribirDevuelveLaUnidadALaCiudad` (`internal/game/simulation`); al cargar, por `TestLaGuarnicionSeDerivaDeSuTabla` (`internal/game/loadcheck`) y `TestLaCargaReparaYAuditaLoQueElEsquemaNoGarantiza` (integration) |

**Enunciado.** `units.status = 'GARRISONED'` si y sólo si existe una fila de `garrisons` para esa unidad.

**Razón.** Es el mismo patrón que [INV-UNIT-005](units.md#inv-unit-005) —un `status` desnormalizado que debe coincidir con la existencia de una fila en otra tabla— y sus dos modos de fallo son asimétricos:

- **`GARRISONED` sin fila** deja una unidad que no está en el mapa, no acepta órdenes y no está en ninguna ciudad. Es una unidad **perdida**: salir requiere saber de qué ciudad se sale.
- **Fila con otro `status`** pone la unidad dentro de la ciudad y a la vez en el mapa, visible y ordenable. Es duplicación efectiva de un activo.

**`garrisons` es la fuente de verdad** y `units.status` es la vista: toda reparación deriva el estado de la tabla, nunca al revés.

**Cómo se garantiza.**

- `DOMAIN` — `GarrisonRepo.Enter` escribe estado, fila y evento en una transacción, y `Leave` hace lo mismo al revés. No hay ninguna ruta que toque una sin la otra.
- `DOMAIN` — la RAM no se adelanta a la base en la entrada: la unidad queda **entrando** —sigue `IDLE`— hasta que la transacción responde, y sólo entonces pasa a `GARRISONED`. Mientras tanto el volcado periódico no la escribe, porque volcaría `IDLE` encima del `GARRISONED` recién confirmado (`RN-GARR-020`).
- `DOMAIN` — en la salida la RAM sí se adelanta, como en el movimiento: si la escritura falla de forma definitiva, la compensación devuelve la unidad a la ciudad en la RAM ([ADR-017](../decisions/ADR-017-garrison-commands.md) §2.3).
- `DOMAIN` — al arrancar, la validación de carga deriva el estado de `garrisons` en los dos sentidos y borra las filas de unidades que no están vivas. La señal que emite lleva `inv_id=INV-UNIT-007`, la ficha de la misma propiedad vista desde la unidad.
- `DB` — `units_status_valid` cierra el dominio de `status`.

**Cómo se verifica.**

- `TestGuarnecerConTratadoActivoDejaLaUnidadGarrisonedYPersistida` y `TestSalirDeLaGuarnicionDevuelveLaUnidadAlMapaYBorraLaFila` (integration) — estado y fila cambian juntos, releídos de la base.
- `TestMientrasEntraLaUnidadNoAceptaOrdenesNiSeVuelca` (simulation) — la unidad que espera su transacción sigue `IDLE`, rechaza órdenes y el volcado la aplaza. Verificado por mutación: sin el aplazamiento, el test falla.
- `TestUnaSalidaQueNoSePuedeEscribirDevuelveLaUnidadALaCiudad` (simulation) — la compensación deja la RAM como la base. Verificado por mutación.
- `TestLaGuarnicionSeDerivaDeSuTabla` (unit, `internal/game/loadcheck`) y `TestLaCargaReparaYAuditaLoQueElEsquemaNoGarantiza` (integration) — las tres reparaciones de carga.

**Violación en runtime.** Detección en la validación de carga (`invariant_violation` con `inv_id=INV-UNIT-007`, política `REPAIR`, un `InvariantRepaired` de auditoría). En la RAM, una orden de salida sobre una unidad `GARRISONED` sin ciudad anfitriona conocida emite `invariant_violation` con `inv_id=INV-GARR-002` y responde `INTERNAL_ERROR`, sin mover nada.

---

<a id="inv-garr-003"></a>
## INV-GARR-003 — `units.city_id` coincide con la ciudad de la guarnición (DEROGADO)

**Estado: DEROGADO el 2026-10-20 por [ADR-017](../decisions/ADR-017-garrison-commands.md).** El número queda quemado.

**Enunciado derogado.** Si existe fila de `garrisons` para una unidad, `units.city_id` es igual a `garrisons.city_id`.

**Por qué se deroga.** `units.city_id` es la ciudad de origen de la unidad, y la población de una ciudad se cuenta por esa columna ([INV-CITY-009](city.md#inv-city-009)). Fijarla a la anfitriona al guarnecer movería la población con la unidad: un jugador liberaría plazas guarneciendo en la ciudad de un aliado y consumiría las de éste. ADR-017 decide lo contrario —la unidad sigue contando en su ciudad de origen, y la anfitriona vive sólo en `garrisons.city_id`—, y lo fija como [INV-GARR-009](#inv-garr-009).

Ninguna ruta escribió nunca `units.city_id` al guarnecer: el invariante no llegó a tener código que lo cumpliera ni que lo violara.

---

<a id="inv-garr-004"></a>
## INV-GARR-004 — Guarnecer en ciudad ajena exige tratado activo

| Campo | Valor |
|---|---|
| Severidad | CRITICO |
| Aplicación | DOMAIN, TEST |
| Milestone | M7 |
| Política ante violación | REJECT con `TREATY_REQUIRED` |
| Cobertura | **Cubierto**: la regla por `TestEnCiudadAjenaSinTratadoSeRechaza`, `TestEnCiudadAjenaConTratadoActivoYFlagSeEntra`, `TestUnTratadoActivoSinElFlagNoAutoriza`, `TestUnTratadoPropuestoConElFlagNoAutoriza` y `TestUnTratadoConOtroJugadorNoAutoriza` (`internal/domain/garrison`) y `TestSoloUnTratadoActivoConElFlagAutorizaGuarnicion` (`internal/domain/diplomacy`); contra la base por `TestGuarnecerEnCiudadAjenaSinTratadoSeRechaza`, `TestUnTratadoSinElFlagNoHabilitaGuarnicion`, `TestUnTratadoRotoDejaDeHabilitarGuarnicionesNuevas` y `TestGuarnecerYSalirNoCambianLaCiudadDeOrigenNiElDueno` (integration); por la red, `TestLaCiudadAjenaSinTratadoSeRechazaEnLaTransaccion` (simulation) y `TestGuarnecerEnUnaCiudadAjenaSinTratadoLlegaComoTreatyRequired` (e2e, `internal/websocket`) |

**Enunciado.** Guarnición en ciudad ajena (`units.player_id <> cities.owner_player_id`) implica que existía un `treaty` `ACTIVE` con `allows_garrison = true` entre ambos jugadores en el instante de la entrada.

**Razón.** Es la regla que impide meter tropas dentro de la ciudad de otro sin su permiso, y su violación es la más grave de esta familia: una unidad dentro de una ciudad ajena es invisible para el anfitrión ([INV-GARR-006](#inv-garr-006)). Sin autorización, eso es un caballo de Troya.

La cualificación **«en el instante de la entrada»** es la parte con más contenido. El tratado es un permiso **de entrada**, no una condición continua: romperlo impide **nuevas** entradas y deja las existentes a las reglas de la diplomacia, que es donde esa decisión pertenece (expulsión diferida, [INV-GARR-008](#inv-garr-008)).

**Cómo se garantiza.**

- `DOMAIN` — la decisión se toma **en la transacción** que escribe la entrada (`RN-GARR-020`): `GarrisonRepo.Enter` busca el tratado con el par canónico, `status = 'ACTIVE'` y `allows_garrison` (`TreatyRepo.FindAuthorizingGarrison`) y vuelve a ejecutar `garrison.CanEnter` con él. Sin tratado, devuelve `ErrTreatyRequired`, no escribe nada, y el loop responde `TREATY_REQUIRED` con el `requestId` de la orden. Es la consulta del estado **actual** de `treaties` (`RN-GARR-018`): los tratados se escriben fuera del servidor y no hay copia en la RAM que pueda estar desfasada.
- `DOMAIN` — el orden de `CanEnter` deja el tratado para el final: propiedad, estado, ciudad y adyacencia se comprueban antes, en el tick, y un jugador no averigua nada sobre los tratados de una unidad que no es suya (`TestElOrdenDeComprobacionNoFiltraInformacionSobreTratados`). El `playerId` es el de la sesión, no el del payload ([INV-PLAYER-004](player.md#inv-player-004)).
- `DOMAIN` — la protección offline del anfitrión **no** cuenta: guarnecer no es una acción hostil ([ADR-016](../decisions/ADR-016-foreign-territory-actions.md), `RN-GARR-009`).
- `DOMAIN` — el evento `UnitGarrisoned` registra el `treatyId` que autorizó la entrada. Es lo que hace auditable la cualificación temporal del enunciado: el estado actual de `treaties` no dice qué valía entonces.
- `DB` — el par canónico, `treaties_one_active_per_pair_and_type`, `treaties_status_valid` y `allows_garrison DEFAULT false` hacen la consulta inequívoca y el permiso opt-in.

**Cómo se verifica.**

- Los cinco tests de `internal/domain/garrison` citados y `TestSoloUnTratadoActivoConElFlagAutorizaGuarnicion` (`internal/domain/diplomacy`) — la tabla de estados × flag: sólo `ACTIVE` con el flag autoriza, y sólo entre ese par.
- `TestGuarnecerEnCiudadAjenaSinTratadoSeRechaza`, `TestUnTratadoSinElFlagNoHabilitaGuarnicion` y `TestUnTratadoRotoDejaDeHabilitarGuarnicionesNuevas` (integration) — el rechazo no escribe nada, y romper el tratado impide la siguiente entrada.
- `TestGuarnecerYSalirNoCambianLaCiudadDeOrigenNiElDueno` (integration) — el `UnitGarrisoned` de una entrada ajena lleva el `treatyId` del tratado; `TestUnaEntradaRepetidaEsIdempotenteYNoDuplicaElEvento`, que el de una entrada propia no lleva ninguno.
- `TestLaCiudadAjenaSinTratadoSeRechazaEnLaTransaccion` (simulation) y `TestGuarnecerEnUnaCiudadAjenaSinTratadoLlegaComoTreatyRequired` (e2e) — el rechazo de la transacción llega al jugador como `TREATY_REQUIRED` con su `requestId`, y la unidad vuelve a admitir órdenes. Verificado por mutación: si el rechazo se tratara como fallo transitorio, el test falla.

**Violación en runtime.** `REJECT` antes del efecto, sin log de invariante: un rechazo por tratado es la regla de juego funcionando. Una guarnición no autorizada **consumada** —sólo posible con una escritura manual— se trata como incidente de seguridad y se revisa a mano: expulsarla automáticamente sería un efecto de gameplay decidido por un mecanismo de reparación.

---

<a id="inv-garr-005"></a>
## INV-GARR-005 — Una unidad guarnecida no ocupa tile

| Campo | Valor |
|---|---|
| Severidad | ALTO |
| Aplicación | DOMAIN, TEST |
| Milestone | M7 |
| Política ante violación | REPAIR (retirar de la capa de ocupación) |
| Cobertura | **Cubierto** por `TestGuarnecerEnLaCiudadPropiaEnDosFases` (`internal/game/simulation`), que comprueba que el tile de la unidad sigue siendo transitable tras guarnecerla |

**Enunciado.** Ninguna unidad `GARRISONED` figura en la capa de ocupación del mundo.

**Razón.** Un tile bloqueado por algo invisible es de los fallos más desconcertantes para un jugador: el pathfinder lo esquiva, la ruta se alarga y nada en pantalla lo explica. Y es una fuga indirecta: quien vea a sus unidades rodear un tile vacío junto a una ciudad ajena deduce que hay algo guarnecido ahí.

**Cómo se garantiza.**

- `DOMAIN` — **por construcción**: en el MVP la capa de ocupación (`World.SetBlocked`) sólo la escriben las murallas de las ciudades. Ninguna unidad, en ningún estado, entra en ella; la colisión entre unidades está fuera de alcance ([../specs/movement.md](../specs/movement.md)). Guarnecer y salir no llaman a `SetBlocked`.
- `DOMAIN` — la posición consolidada de la unidad guarnecida se conserva —es donde su dueño la encuentra en el snapshot—, pero ninguna lógica espacial la lee: [INV-UNIT-001](units.md#inv-unit-001) y la validación de carga excluyen ese estado, y `ClearFootprints` no la toca.
- Si algún día las unidades ocupan tile, esta ficha deja de ser trivial: la entrada tendrá que retirar la unidad de la capa y la salida reinsertarla, y el test tendrá que comprobarlo.

**Cómo se verifica.**

- `TestGuarnecerEnLaCiudadPropiaEnDosFases` (simulation) — tras guarnecer, el tile que ocupaba la unidad sigue siendo transitable.
- `TestLaMurallaNuevaNoTocaUnidadesGuarnecidas` (simulation) — una muralla nueva no aparta ni detiene una unidad guarnecida.

**Violación en runtime.** No tiene por dónde ocurrir mientras ninguna unidad escriba la capa.

---

<a id="inv-garr-006"></a>
## INV-GARR-006 — Una unidad guarnecida no aparece en deltas de terceros

| Campo | Valor |
|---|---|
| Severidad | CRITICO |
| Aplicación | BOUNDARY, DOMAIN, TEST |
| Milestone | M7 |
| Política ante violación | FAIL_FAST del delta |
| Cobertura | **Cubierto** por `TestGuarnecerEnLaCiudadPropiaEnDosFases`, `TestSalirDeLaGuarnicionVuelveAlAnilloEnOrden` y `TestUnaUnidadGuarnecidaSoloLaVeSuDuenoEnElSnapshot` (`internal/game/simulation`) y `TestLaGuarnicionPorLaRedSoloLaVeSuDueno` (e2e, `internal/websocket`) |

**Enunciado.** Ninguna unidad `GARRISONED` aparece en un delta dirigido a un jugador distinto de su propietario.

**Razón.** Es la razón de ser de la guarnición: meter tropas en una ciudad las pone a salvo **y las esconde**. Si siguieran emitiéndose, revelarían cuántas hay y dónde, que es información con valor táctico directo.

La formulación «dirigido a un jugador distinto de su propietario» es la de [INV-SAFE-004](territory.md#inv-safe-004) y por la misma razón: el propietario **sí** ve sus unidades guarnecidas, y esa asimetría obliga a filtrar **por destinatario**. Con una ciudad ajena hay tres partes —el dueño de la unidad, el anfitrión y los demás— y el enunciado es estricto: sólo el dueño de la unidad la ve. Que el anfitrión vea lo que aloja es **TBD (fuera de MVP)**.

**Cómo se garantiza.**

- `BOUNDARY` — al entrar, el dueño recibe `entity.update` y los demás observadores del chunk —anfitrión incluido— un `entity.despawn { reason: GARRISONED }` por `BroadcastChunkExcept`, que excluye al dueño. Al salir, lo simétrico con `entity.spawn`.
- `BOUNDARY` — `world.snapshot` aplica el mismo filtro con `simulation.VisibleTo`, que sólo deja pasar una unidad `HIDDEN` o `GARRISONED` hacia su dueño. Un snapshot es un delta completo: sin el filtro, la unidad reaparecería al reconectar.
- `DOMAIN` — mientras está dentro, ninguna otra vía la emite: no tiene movimiento (`unit.move` responde `UNIT_GARRISONED`), el ocultamiento la ignora (`safezone.NextStatus`) y `ClearFootprints` no la toca.
- No hay aserción en el emisor de deltas que lo compruebe en runtime: la garantía es estructural, en los dos únicos puntos que emiten la entrada y la salida, más el filtro del snapshot.

**Cómo se verifica.**

- `TestGuarnecerEnLaCiudadPropiaEnDosFases` y `TestSalirDeLaGuarnicionVuelveAlAnilloEnOrden` (simulation) — el despawn y el spawn van por `BroadcastChunkExcept` con el dueño excluido. Verificado por mutación: sin el despawn, el primero falla.
- `TestUnaUnidadGuarnecidaSoloLaVeSuDuenoEnElSnapshot` (simulation) — el snapshot del dueño la trae con `garrisonCityId`; el de un tercero no. Verificado por mutación: si `VisibleTo` deja pasar `GARRISONED`, falla.
- `TestLaGuarnicionPorLaRedSoloLaVeSuDueno` (e2e) — dos jugadores reales: el tercero recibe el despawn, no la ve al reconectar, y la recibe con `entity.spawn` al salir.

**Violación en runtime.** Sin detección en runtime (ver arriba). Una fuga consumada se trata como incidente: la información ya salió y no se puede retirar.

---

<a id="inv-garr-007"></a>
## INV-GARR-007 — Entrar y salir de la guarnición no cambia el dueño

| Campo | Valor |
|---|---|
| Severidad | ALTO |
| Aplicación | DOMAIN, TEST |
| Milestone | M7 |
| Política ante violación | REJECT (la escritura no se aplica) |
| Cobertura | **Cubierto** por `TestGuarnecerYSalirNoCambianLaCiudadDeOrigenNiElDueno` (integration) y `TestNingunaEscrituraCambiaElDuenoNiElTipoYTodasMantienenElChunk` (integration) |

**Enunciado.** El ownership (`units.player_id`) de una unidad guarnecida no cambia al entrar ni al salir.

**Razón.** Es la primera mecánica que introduce una razón plausible para tocar `player_id`: la unidad está dentro de una ciudad que tiene otro dueño, y de ahí a «ahora pertenece al anfitrión» hay un paso corto. En una guarnición aliada, ese paso **regala la unidad**. Es un caso particular de [INV-UNIT-010](units.md#inv-unit-010), y se documenta aparte para que quede escrito que la guarnición no debe darlo.

**Cómo se garantiza.**

- `DOMAIN` — `GarrisonRepo.Enter` escribe `units.status` y la fila de `garrisons`; `Leave`, posición, chunk y estado. `player_id` no está en ninguna de las dos sentencias.
- `DOMAIN` — el ownership es inmutable en el MVP ([INV-UNIT-010](units.md#inv-unit-010)): no existe ninguna ruta de transferencia.
- `DOMAIN` — `garrison.CanLeave` exige que quien saca la unidad sea su dueño: el anfitrión recibe `UNIT_NOT_OWNED` (`TestSoloSePuedeSacarUnaUnidadGuarnecidaPropia`). El filtro de visibilidad de [INV-GARR-006](#inv-garr-006) también usa `units.player_id`.

**Cómo se verifica.**

- `TestGuarnecerYSalirNoCambianLaCiudadDeOrigenNiElDueno` (integration) — `(city_id, player_id)` releídos de la base no cambian al guarnecer en una ciudad ajena con tratado ni al salir.
- `TestNingunaEscrituraCambiaElDuenoNiElTipoYTodasMantienenElChunk` (integration) — ninguna escritura de unidades toca `player_id`.

**Violación en runtime.** Sin detección en runtime: ninguna sentencia de la guarnición escribe la columna. Un cambio consumado se trata como incidente, igual que en [INV-UNIT-006](units.md#inv-unit-006).

---

<a id="inv-garr-009"></a>
## INV-GARR-009 — Entrar y salir de la guarnición no cambian la ciudad de origen

| Campo | Valor |
|---|---|
| Severidad | ALTO |
| Aplicación | DOMAIN, TEST |
| Milestone | M7 |
| Política ante violación | REJECT (la escritura no se aplica) |
| Cobertura | **Cubierto** por `TestGuarnecerYSalirNoCambianLaCiudadDeOrigenNiElDueno` (integration) y `TestGuarnecerEnLaCiudadPropiaEnDosFases` (`internal/game/simulation`) |

**Enunciado.** Entrar en una guarnición y salir de ella no cambian `units.city_id`.

**Razón.** `units.city_id` es la ciudad de origen, y la población de una ciudad es el recuento de sus unidades vivas por esa columna ([INV-CITY-009](city.md#inv-city-009)). Si guarnecer la cambiara, la población viajaría con la unidad: un jugador liberaría plazas de su ciudad guarneciendo en la de un aliado y le consumiría las suyas. La unidad guarnecida sigue contando donde nació, como en la referencia, y la ciudad que la aloja está en `garrisons.city_id` ([ADR-017](../decisions/ADR-017-garrison-commands.md) §2.4). Sustituye a [INV-GARR-003](#inv-garr-003), derogado.

**Cómo se garantiza.**

- `DOMAIN` — ni `GarrisonRepo.Enter` ni `Leave` escriben `units.city_id`.
- `DOMAIN` — la RAM tampoco: la anfitriona vive en un índice aparte de `simulation.State`, y es lo que viaja como `garrisonCityId`; `cityId` sigue siendo la de origen en todos los mensajes.
- `DOMAIN` — como nada de la ciudad cambia, guarnecer no emite `city.update` (`T-CITY-C-002`).

**Cómo se verifica.**

- `TestGuarnecerYSalirNoCambianLaCiudadDeOrigenNiElDueno` (integration) — `units.city_id` es la de origen dentro y fuera de la guarnición, y la población de las dos ciudades no cambia.
- `TestGuarnecerEnLaCiudadPropiaEnDosFases` (simulation) — en la RAM, `CityID` y la población siguen igual y no sale ningún `city.update`.

**Violación en runtime.** Sin detección en runtime: ninguna sentencia de la guarnición escribe la columna. Una población que dejara de cuadrar con sus unidades la repara la validación de carga ([INV-CITY-009](city.md#inv-city-009)).

---

## Cobertura conjunta

Vistas como una sola propiedad: *una unidad guarnecida está en exactamente una ciudad, según la tabla que lo dice, con autorización si la ciudad es ajena, fuera del mapa, invisible para terceros, sin haber cambiado de dueño ni de ciudad de origen.*

```
      INV-GARR-001  a lo sumo una guarnición por unidad   (PRIMARY KEY)
             │
             ▼
      INV-GARR-002  GARRISONED  ⟺  existe fila
             │
             ├── INV-GARR-004  ciudad ajena exige tratado ACTIVE en la entrada
             └── INV-GARR-009  units.city_id no cambia      (sustituye a 003)
             │
             ▼
      INV-GARR-005  no figura en la capa de ocupación
             │
             ▼
      INV-GARR-006  no aparece en deltas de terceros
             │
             ▼
      INV-GARR-007  el ownership no cambia
```

<a id="inv-garr-008"></a>
## INV-GARR-008 — Coherencia entre gracia de expulsión y tratado vigente (RESERVADO)

**Estado: reservado, no vigente.** Este ID está apartado y **no debe reutilizarse**, pero el
invariante todavía no se puede enunciar de forma verificable.

**Enunciado previsto.** Toda unidad guarnecida en una ciudad ajena cuyo tratado ha dejado de estar
`ACTIVE` tiene una gracia de expulsión en curso y no vencida.

**Por qué no está vigente.** La gracia no tiene dónde vivir: la tabla `garrisons` no tiene columna
para su vencimiento, y la política de expulsión diferida al romperse un tratado está descrita en
[../specs/garrison.md](../specs/garrison.md) §6.4 como diseño objetivo, no como comportamiento
implementado. Mientras tanto, romper o caducar un tratado no saca a nadie; no es explotable por un
jugador porque ningún jugador crea ni rompe tratados ([ADR-017](../decisions/ADR-017-garrison-commands.md) §2.6).

**Cuándo pasa a estar vigente.** En la misma migración aditiva que introduzca el vencimiento de la
gracia. Ese cambio debe, a la vez: añadir la columna, implementar la expulsión, escribir el test, y
sustituir esta ficha por una completa con su mecanismo de garantía y su política ante violación.

**Registrado aquí y no sólo en la spec** porque el registro de invariantes es el dueño único de la
numeración: un ID mencionado en una spec pero ausente del registro es exactamente la clase de deriva
que `scripts/check-docs.mjs` existe para impedir.

---

## Trazabilidad

| Invariante | Componente propietario | Relacionado con |
|---|---|---|
| INV-GARR-001 | `migrations/`, `postgres.GarrisonRepo` | [INV-MOVE-001](movement.md#inv-move-001) |
| INV-GARR-002 | `postgres.GarrisonRepo`, `internal/game/simulation`, `internal/game/loadcheck` | [INV-UNIT-005](units.md#inv-unit-005), [INV-UNIT-007](units.md#inv-unit-007) |
| INV-GARR-003 | *derogado por ADR-017* | [INV-GARR-009](#inv-garr-009) |
| INV-GARR-004 | `internal/domain/garrison`, `postgres.GarrisonRepo` | [INV-SEC-004](security.md#inv-sec-004), [INV-CITY-006](city.md#inv-city-006) |
| INV-GARR-005 | `internal/game/world`, `internal/game/simulation` | [INV-UNIT-003](units.md#inv-unit-003) |
| INV-GARR-006 | `internal/game/simulation` | [INV-SAFE-004](territory.md#inv-safe-004) |
| INV-GARR-007 | `postgres.GarrisonRepo`, `internal/domain/garrison` | [INV-UNIT-006](units.md#inv-unit-006), [INV-UNIT-010](units.md#inv-unit-010) |
| INV-GARR-008 | *reservado, sin propietario todavía* | [INV-GARR-004](#inv-garr-004) |
| INV-GARR-009 | `postgres.GarrisonRepo`, `internal/game/simulation` | [INV-CITY-009](city.md#inv-city-009) |

## Documentos relacionados

- [README.md](README.md) — catálogo completo y tabla resumen.
- [units.md](units.md) — estados de unidad; [INV-UNIT-003](units.md#inv-unit-003) y [INV-UNIT-007](units.md#inv-unit-007) son las contrapartes de esta familia.
- [territory.md](territory.md) — familias `INV-TERR-*` e `INV-SAFE-*`; `HIDDEN` es la otra mecánica que oculta unidades.
- [city.md](city.md) — presencia, protección y población de la ciudad.
- [../specs/garrison.md](../specs/garrison.md) — spec funcional que cita estos IDs.
- [../database/schema.md](../database/schema.md) — DDL de `garrisons` y `treaties`.
