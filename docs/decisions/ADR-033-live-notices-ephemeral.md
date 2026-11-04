# ADR-033: El aviso en vivo es un mensaje efímero sin `id` que la simulación manda al decidir el suceso, clasificado con la misma función que el registro

Propósito: fijar cómo el slice de avisos ([SPEC-LIVE-NOTICES](../specs/live-notices.md)) le cuenta al jugador, en el
tick en que pasa, lo que les pasa a sus unidades fuera de la vista, sin darle a nada un `id` antes de que lo asigne la
base, sin esperar a la base y sin poder contar algo distinto de lo que contará el registro.

| Campo | Valor |
|---|---|
| **Estado** | **Aceptado** |
| **Fecha** | **2026-11-04** |
| Ámbito | `internal/domain/eventlog` (qué avisos da un suceso y a quién), `internal/game/simulation` (`combat.go`, `pillage.go`, `siege.go`: el envío en la fase 4), `internal/protocol` y `packages/protocol` (`event_log.notice`), cliente (el estado de los avisos y el HUD) |
| Índice | [README.md](README.md) |
| Relacionados | [ADR-024](ADR-024-combat-simultaneous-resolution.md) (la muerte se decide en la RAM y se anuncia antes de escribirse), [ADR-026](ADR-026-pillage-two-phase-stock-debit.md) (el golpe de saqueo en dos fases), [ADR-028](ADR-028-event-log-from-world-events.md) (el registro sale de `world_events` y descartó empujarlo), [ADR-029](ADR-029-population-over-limit-and-house-destruction.md) (la casa derribada es optimista) |

---

## Contexto

Las muertes ([ADR-024](ADR-024-combat-simultaneous-resolution.md)), los golpes de saqueo
([ADR-026](ADR-026-pillage-two-phase-stock-debit.md)) y las casas derribadas
([ADR-029](ADR-029-population-over-limit-and-house-destruction.md)) se deciden en la fase 4 del tick, en la RAM, y se
difunden en ese mismo tick al chunk donde ocurren: `entity.despawn DEAD`, `entity.update { carrying }` y
`building.destroyed`. La fila de `world_events` la escribe después la cola. Quien no está suscrito a ese chunk no se
entera. Sólo dos sucesos llegan hoy al jugador esté donde esté, y los dos a la víctima: `pillagedAtMs` y
`besiegedAtMs`.

La Q5 de [SPEC-EVENT-LOG](../specs/event-log.md) y la primera alternativa descartada de
[ADR-028](ADR-028-event-log-from-world-events.md) dejaron fuera los avisos en vivo por dos motivos: el loop no debe
esperar a PostgreSQL, y una entrada empujada necesita un `id` que la base asigna al insertar, desde cuatro workers en
paralelo (el problema de `EO-120` con los movimientos). SPEC-LIVE-NOTICES, aceptada el 2026-11-04, pide un aviso en
el tick del suceso que no sea una entrada (`RN-NOTICE-004`).

## Decisión

**1. El aviso es un mensaje servidor → cliente propio, `event_log.notice`, sin `id`.** Lleva el tipo, el instante
del suceso, el otro jugador con su ciudad y, según el tipo, la unidad, la madera y el tile. No tiene `id`, ni
`firstAtMs`/`lastAtMs`, ni `count`: no se puede confundir con una entrada. El cliente no lo mete en la lista del
registro ni lo usa para el cursor de lo visto; la lista sigue saliendo sólo de `event_log.page`. El problema de la Q5
no se resuelve: desaparece, porque no se manda nada que necesite un `id` antes de que exista. Es un tipo nuevo de v1
(`R-VER-3`): el catálogo servidor → cliente pasa de 22 a 23. No hay comando, ni trabajo de la cola, ni evento de
dominio, ni migración nuevos.

**2. Lo manda la simulación en el tick en que decide el suceso**, en la fase 4 (`ResolveCombat`), por `SendToPlayer`
a todas las sesiones del jugador, en el mismo punto en que el suceso se difunde hoy al chunk: `kill` para una muerte,
`applyPillage` para cada golpe que roba madera y `destroyHouse` para una casa en el suelo. No en `submitDeath` ni en
`submitDestruction`: el volcado las vuelve a llamar cuando una escritura falló (`flushDeaths`, `flushDestructions`), y
el aviso saldría otra vez. No espera a la base ni la consulta. Los avisos de un tick salen en el orden de sus sucesos,
que ya es determinista: las muertes por `id` de víctima, los golpes por guerrero.

**3. Qué se avisa y a quién lo decide la clasificación del registro.** La simulación arma el `eventlog.Event` que su
fila va a tener —`UnitKilled` con la víctima en `PlayerID` y el dueño del asesino en `Counterpart`; `CityPillaged` y
`BuildingDestroyed` con la víctima y el autor, como en [ADR-028](ADR-028-event-log-from-world-events.md) §2— y una
función pura nueva, `eventlog.Notices`, lo pasa por `eventlog.Classify` para cada uno de los dos jugadores y se queda
con los tipos de `eventlog.NoticeKinds()`: `UNIT_LOST`, `ENEMY_KILLED`, `PILLAGE_DONE` y `HOUSE_DESTROYED`. De ahí
sale todo lo demás:

- un tercero nunca recibe un aviso, y nadie recibe uno que su registro no le contaría
  ([INV-SEC-009](../invariants/security.md#inv-sec-009)), porque la regla es la misma función;
- un suceso sin otro jugador (sin asesino), o con el mismo jugador en los dos lados, no da aviso;
- lo que la víctima de un saqueo o de un asedio ya recibe (`PILLAGED` y `HOUSE_LOST`, que tienen `pillagedAtMs` y
  `besiegedAtMs`) queda fuera porque su tipo no está en la lista (Q1).

**4. Los datos salen de la RAM del mismo tick.**

| Campo | De dónde |
|---|---|
| `atMs` | El instante del tick en la simulación: el `atMs` del payload de la fila ([SPEC-EVENT-LOG](../specs/event-log.md) `RN-LOG-005`) |
| `otherPlayerId` | El otro jugador de la clasificación |
| `otherCityId`, `otherCityName` | La ciudad del otro jugador en la RAM (`State.CityOf`: una por jugador en el MVP, [INV-PLAYER-003](../invariants/player.md#inv-player-003)), la misma con la que el registro lo nombra al leer (SPEC-EVENT-LOG Q6); `null` si no tiene, y el cliente dice «otro jugador» |
| `unitType` | En las muertes, el de la víctima |
| `amount` | En el saqueo, la madera de ese golpe |
| `x`, `y` | El tile de la víctima en una muerte, el del guerrero en un golpe de saqueo (el `At` de su fila) y el de la casa en una casa derribada. Los cuatro tipos lo llevan, también la casa, que en el registro no lo lleva: así «Ir» funciona siempre (Q4) |

**5. Efímero, y con la entrega de cualquier otro mensaje.** El aviso no se guarda, no se reenvía al reconectar y no
existe al arrancar. Si la cola de salida de una sesión está llena, la sesión se cierra como con cualquier otro
mensaje; al reconectar, el cliente pide la página y «Mientras no estabas» cuenta lo que pasó, porque eso sí está en la
base (`RN-NOTICE-005`).

**6. Un aviso no se desavisa.** Si la transacción de un golpe de saqueo falla de forma definitiva
([ADR-026](ADR-026-pillage-two-phase-stock-debit.md) §1), la madera vuelve a la ciudad y el registro no cuenta el
golpe, pero su aviso ya salió. Es raro (un rechazo de dominio o los reintentos agotados) y el registro, al abrirlo, dice
lo que quedó. Una muerte y una casa no tienen este caso: su escritura se reencola hasta asentarse
([ADR-024](ADR-024-combat-simultaneous-resolution.md), [ADR-029](ADR-029-population-over-limit-and-house-destruction.md)
§4).

**7. En el cliente, los avisos son un estado aparte del registro.** Un módulo propio de `src/state` los guarda y decide
qué se ve; el HUD sólo lo pinta:

- **Agrupación** (Q3): por tipo, otro jugador y tipo de unidad, la misma clave del registro
  ([ADR-028](ADR-028-event-log-from-world-events.md) §4) sin el lado del cursor, mientras el grupo se ve. Un grupo suma
  sus avisos (`count`) y su madera, y se queda con el tile y la hora del último. El texto es el de `entryText` del
  registro sobre ese grupo, así que el aviso y la entrada se leen igual.
- **Duración** (Q2): cada grupo se ve 8 s desde su último aviso, y como mucho tres a la vez, los más recientes. El que
  sale de la franja no se pierde: está en el registro.
- **«Ir»** (Q4): centra la cámara en el tile del último aviso del grupo; la cámara nunca se mueve sola. Con la cámara
  movida, la vista se sincroniza como con cualquier otro desplazamiento.
- **El punto en «Registro»**: mientras haya avisos llegados desde que se abrió el registro por última vez. Abrirlo pide
  la primera página, como ya hace, y borra el punto.
- **El título de la pestaña** (Q7): con la pestaña oculta, cuenta los grupos nuevos que llegan, «(2) Empires Online»;
  un saqueo de treinta golpes cuenta uno. Al volver a la pestaña se borra.

## Alternativas descartadas

1. **Empujar la entrada tras el `COMMIT`**, con su `id`, por el buzón del loop. Exige que cada transacción que escribe
   un evento del registro devuelva el `id` y avise; el aviso llega unas decenas de milisegundos tarde y, con un
   reintento, segundos. A cambio, el cliente podría meterlo en la lista, que no hace falta: la lista se refresca al
   abrirla.
2. **Un «hay novedades» sin contenido** que haga pedir la página. Una consulta a la base por cada golpe de saqueo, y la
   fila puede no estar escrita todavía cuando llega la consulta.
3. **Suscribir al dueño a los chunks de sus unidades.** Le llegaría todo lo de esos chunks, también lo que no le
   importa, y rompe el área de interés de 5 × 5 chunks (canon §13).
4. **Una tabla de qué se avisa en la simulación**, aparte de `Classify`. Dos traducciones del mismo suceso que pueden
   separarse con el tiempo, e `INV-SEC-009` habría que probarlo dos veces.
5. **Avisar al encolar la escritura** (en `submitDeath`, `submitPillage`, `submitDestruction`). Las muertes y las casas
   se reencolan cuando falla su escritura, y el aviso se repetiría.
6. **Guardar los avisos para quien no está** (Q6). Sería un segundo registro, con su tabla y su cursor, para contar lo
   que «Mientras no estabas» ya cuenta.

## Consecuencias

### Positivas

- Ni migración, ni comando, ni trabajo de la cola, ni evento nuevos: `JobNames`, `CommandTypes` y `world_events` no
  cambian.
- El loop no espera a nadie: el aviso es un `SendToPlayer` más en la fase 4, donde ya están `pillagedAtMs` y
  `besiegedAtMs`.
- El aviso y el registro cuentan lo mismo a las mismas personas, porque los clasifica la misma función.

### Negativas

- Un golpe de saqueo cuya escritura falla del todo queda avisado (§6).
- Si el jugador abre el registro en el mismo instante en que llega el aviso, la fila puede no estar escrita todavía
  (la cola la escribe decenas de milisegundos después): la verá al volver a abrirlo.
- Un saqueo manda al dueño del saqueador un mensaje por golpe y guerrero, cada `AttackIntervalMs`. Son pequeños, y el
  cliente los junta en un aviso que crece.

### Neutras

- Un cliente que no conozca `event_log.notice` lo descarta en silencio (`R-VER-3`).
- El aviso no cambia el estado de la simulación: los golden de los escenarios y de recuperación no lo ven.
