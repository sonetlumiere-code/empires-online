# ADR-017: Los comandos de guarnición entran por el loop y el tratado se decide en la transacción

Propósito: abrir la guarnición a los jugadores —el entregable 6 de M7— con dos comandos de red, y fijar las
tres cosas que la spec dejaba pendientes: cómo se consulta un tratado sin que el tick haga I/O, qué es
`units.city_id` de una unidad guarnecida y quién la ve.

| Campo | Valor |
|---|---|
| **Estado** | **Aceptado** |
| **Fecha** | **2026-10-20** |
| Ámbito | `packages/protocol`, `internal/protocol`, `internal/websocket`, `internal/game/simulation`, `postgres.GarrisonRepo`, [../specs/garrison.md](../specs/garrison.md) |
| Índice | [README.md](README.md) |
| Relacionados | [ADR-013](ADR-013-persistence-pipeline.md), [ADR-014](ADR-014-command-idempotency.md), [ADR-016](ADR-016-foreign-territory-actions.md) |

---

## 1. Contexto

Las reglas de la guarnición existen y están probadas contra PostgreSQL: `garrison.CanEnter`, `CanLeave` y
`ReentryTile` deciden; `postgres.GarrisonRepo.Enter` y `Leave` ejecutan en una transacción. Ningún jugador
puede alcanzarlas, porque el protocolo v1 no tiene un comando que guarnezca.

Abrirlas obliga a resolver tres cosas:

1. **El tratado vive sólo en PostgreSQL.** Los tratados se crean por vía administrativa o de seed
   (alcance de M7): no hay comandos de diplomacia, y el servidor no es el único que los escribe. El tick, por
   su parte, nunca hace I/O contra la base.
2. **`units.city_id` tiene dos lecturas incompatibles.** La spec decía que guarnecer lo fija a la ciudad
   anfitriona ([INV-GARR-003](../invariants/diplomacy.md#inv-garr-003)), pero la población de una ciudad se
   cuenta por `units.city_id` (`RN-CITY-012`) y la misma spec dice que guarnecer no es una vía para esquivar
   el límite de población.
3. **La visibilidad.** Una unidad guarnecida no debe verla nadie más que su dueño
   ([INV-GARR-006](../invariants/diplomacy.md#inv-garr-006)), y hoy sale en el snapshot de todos
   ([INV-UNIT-003](../invariants/units.md#inv-unit-003)).

---

## 2. Decisión

### 2.1 Protocolo

Dos mensajes cliente→servidor nuevos, con la misma forma que `unit.move`:

| Mensaje | Payload |
|---|---|
| `unit.garrison` | `{ unitId, cityId }` |
| `unit.ungarrison` | `{ unitId }` |

La respuesta directa va al dueño con el `requestId` del comando: un `entity.update` con el estado nuevo si se
acepta, un `system.error` con el código si se rechaza. Los códigos son los del catálogo, sin ninguno nuevo:
`UNIT_NOT_FOUND`, `UNIT_NOT_OWNED`, `UNIT_DEAD`, `UNIT_GARRISONED`, `UNIT_NOT_MOVABLE`, `CITY_NOT_FOUND`,
`INVALID_TARGET`, `TREATY_REQUIRED` e `INTERNAL_ERROR`.

`UnitView` y `entity.update` ganan el campo `garrisonCityId`: la ciudad que aloja a la unidad, o `null`. No
se reutiliza `cityId`, que es la ciudad de origen (§2.4) y significa lo mismo en todos los mensajes.

La idempotencia es la de `unit.move`: el `requestId` se reserva en Redis antes de encolar ([ADR-014](ADR-014-command-idempotency.md)).

### 2.2 Entrada: el tick valida, la transacción decide el tratado

1. **En el tick**, con lo que hay en RAM: existe la unidad, es del solicitante, está `IDLE`, existe la ciudad
   y la unidad es adyacente a su zona urbana. Es `garrison.CanEnter` sin tratados: en ciudad propia acepta, en
   ciudad ajena devuelve «hace falta tratado», que aquí significa «falta la comprobación que sólo puede hacer
   la base». Cualquier otro rechazo se responde en el mismo tick.
2. La unidad queda **entrando**: sigue `IDLE` en la RAM, pero rechaza `unit.move`, otro `unit.garrison` y
   `unit.ungarrison` con `UNIT_NOT_MOVABLE`, y el volcado periódico no la escribe —escribiría `IDLE` encima del
   `GARRISONED` que la transacción está a punto de confirmar—.
3. **En la cola de persistencia**, con la clave de la unidad, `GarrisonRepo.Enter` vuelve a ejecutar
   `CanEnter` con el tratado **actual** dentro de la transacción (`RN-GARR-018`) y, si autoriza, escribe estado,
   fila y evento juntos. Es idempotente por construcción: si la fila de esa unidad en esa ciudad ya existe, el
   reintento es un éxito sin un segundo evento.
4. El resultado vuelve al loop como comando (`Deps.Inbox`, igual que las compensaciones de
   [ADR-013](ADR-013-persistence-pipeline.md) §2.4). Aceptado: la unidad pasa a `GARRISONED` en la RAM, su
   dueño recibe el `entity.update` y los demás observadores del chunk un `entity.despawn` con
   `reason: GARRISONED`. Rechazado: `system.error` y la unidad vuelve a admitir órdenes.

La respuesta llega uno o dos ticks después de la orden: el tiempo de una transacción. Guarnecer no es una
acción de reflejos y no lo nota nadie.

### 2.3 Salida: la decide el tick y se compensa si no se puede escribir

La salida no depende de nada que no esté en la RAM, así que sigue el camino del movimiento: el tick decide y
aplica, y la cola escribe.

1. `garrison.CanLeave` y `garrison.ReentryTile`: el primer tile transitable del anillo exterior de la zona
   urbana en orden `(y, x)` (`RN-GARR-011`). Las unidades no ocupan tile en el MVP —la colisión está fuera de
   alcance ([../specs/movement.md](../specs/movement.md))—, así que sólo cuentan terreno y murallas.
2. La unidad pasa a `IDLE` en ese tile. Su dueño recibe el `entity.update` (estado, posición y
   `garrisonCityId: null`) y los demás observadores del chunk un `entity.spawn`.
3. `GarrisonRepo.Leave` escribe posición, estado, el borrado de la fila y el evento. Es idempotente: el evento
   sólo se escribe si el borrado quitó una fila.
4. Si la escritura falla de forma definitiva, la compensación vuelve al loop y devuelve la unidad a la ciudad
   en la RAM —deteniendo el movimiento que hubiera empezado—: el mundo no puede quedarse con una salida que la
   base nunca conoció.

Si no hay ningún tile de reentrada (`RN-GARR-014`), la salida queda pendiente y se reintenta en la fase 5 de
cada tick; la respuesta al comando llega cuando la unidad sale. Con el emplazamiento de `founding.FindSite`,
que exige un entorno transitable de radio 3, el anillo nunca está cerrado; la regla protege un caso que sólo
un mapa hecho a mano produce.

### 2.4 `units.city_id` es la ciudad de origen y no cambia

Guarnecer no cambia `units.city_id`. La unidad sigue contando en la población de su ciudad de origen, como en
la referencia, donde las unidades guarnecidas siguen ocupando población. La ciudad anfitriona está en
`garrisons.city_id`, y en la RAM, en un índice unidad → anfitriona que se reconstruye al arrancar desde
`garrisons`.

Fijar `units.city_id` a la anfitriona movería la población con la unidad: un jugador liberaría plazas
guarneciendo en la ciudad de un aliado y consumiría las de éste. Por eso:

- [INV-GARR-003](../invariants/diplomacy.md#inv-garr-003) («`units.city_id` coincide con la ciudad de la
  guarnición») queda **DEROGADO** por este ADR.
- Se registra [INV-GARR-009](../invariants/diplomacy.md#inv-garr-009): entrar y salir de la guarnición no
  cambian `units.city_id`.
- La guarnición no cambia nada de lo que transporta `city.update` —ni la población ni el límite—, así que no
  se emite. La frase del entregable 6 de M7 que pedía un `city.update` al guarnecer queda retirada: un
  `city.update` sin cambio contradiría `T-CITY-C-002`.

### 2.5 Visibilidad: como una unidad oculta

Una unidad `GARRISONED` se filtra por destinatario igual que una `HIDDEN` ([INV-SAFE-004](../invariants/territory.md#inv-safe-004)):

- Sólo su dueño la recibe en `world.snapshot`, con `status: GARRISONED` y `garrisonCityId`.
- Sigue indexada en el chunk de su último tile exterior, que es lo que permite que su dueño la encuentre al
  reconectar.
- Los terceros —el anfitrión incluido— reciben `entity.despawn { reason: GARRISONED }` al entrar y
  `entity.spawn` al salir. Que el anfitrión vea qué aloja sigue fuera del MVP.

### 2.6 Lo que no cambia

- La caducidad o ruptura de un tratado sigue sin efecto sobre las guarniciones abiertas: la expulsión diferida
  de `RN-GARR-015..019` necesita una migración y una variable de configuración que no existen. No es
  explotable por un jugador, porque ningún jugador crea ni rompe tratados.
- La capacidad máxima de una guarnición sigue sin definir.

---

## 3. Alternativas descartadas

| Alternativa | Por qué no |
|---|---|
| Copiar los tratados a la RAM y refrescarlos cada segundo | Crea una segunda fuente de verdad para datos que escribe alguien más que el servidor, con hasta un segundo de retraso; cuando la base contradijera a la RAM habría que deshacer una guarnición que terceros ya vieron desaparecer. Se adoptará cuando los tratados los escriba sólo el servidor, con comandos de diplomacia. |
| Aplicar la entrada en la RAM y compensar si la base la rechaza | Muestra a terceros un `entity.despawn` que puede deshacerse un tick después, y da al jugador un éxito que luego retira. |
| Fijar `units.city_id` a la ciudad anfitriona | Mueve la población con la unidad (§2.4). |
| Reutilizar `cityId` en `entity.update` para la anfitriona | El mismo campo significaría dos cosas según el mensaje; el cliente no tendría forma de distinguirlas. |
| Guardar el `requestId` en `garrisons` con `UNIQUE` | Exige una migración que la idempotencia por construcción de `Enter` y `Leave` hace innecesaria ([ADR-014](ADR-014-command-idempotency.md) §2). |

---

## 4. Consecuencias

- El protocolo v1 pasa de cinco a siete mensajes cliente→servidor. `CITY_NOT_FOUND` e `INVALID_TARGET`, que
  estaban en el catálogo sin emisor, tienen ya uno.
- El entregable 6 de M7 y su test de contrato dejan de estar bloqueados.
- Un resultado de entrada que no cabe en la cola de comandos del loop deja la unidad «entrando» hasta el
  siguiente arranque, que la carga con el estado de la base. Es el mismo límite aceptado para las
  compensaciones en [ADR-013](ADR-013-persistence-pipeline.md) §2.4, y se registra en el log.
- La familia `INV-GARR` deja de describir efectos inalcanzables y se reconcilia con la suite.
