# ADR-024: El combate se resuelve a la vez en la fase 4 y sólo la muerte se escribe en el acto

Propósito: fijar cómo el slice de incursión ([SPEC-COMBAT-RAID](../specs/combat-raid.md)) resuelve los golpes
en el tick, qué persiste de un combate y cómo encaja el tope de la protección offline que la spec exige antes de
abrir el combate.

| Campo | Valor |
|---|---|
| **Estado** | **Aceptado** |
| **Fecha** | **2026-11-02** |
| Ámbito | `internal/game/simulation` (`combat.go`, fase 4), `internal/domain/unit`, `internal/domain/city`, `internal/persistence/postgres`, migración `000007` |
| Índice | [README.md](README.md) |
| Relacionados | [ADR-011](ADR-011-movement-timed-polyline.md) (la persecución son tramos de la polilínea), [ADR-016](ADR-016-foreign-territory-actions.md) (primera acción hostil), [ADR-022](ADR-022-durable-write-retry-and-loop-inbox.md) (toda escritura declara compensación), [ADR-023](ADR-023-economy-stock-deltas-and-two-phase-orders.md) (stock por deltas) |

---

## Contexto

La fase 4 del tick, `resolve simulation`, está reservada para el combate desde M0 y hoy no hace nada. La spec
fija el qué —quién puede golpear a quién, cuánto, qué pasa al morir— y deja tres preguntas de ingeniería: en
qué orden se aplican los golpes de un mismo tick, qué de un combate tiene que sobrevivir a un reinicio, y cómo
se implementa el tope de la protección (decisión Q1 de la spec) sin rehacer el autómata de presencia.

## Decisión

### 1. Resolución simultánea

Al empezar la fase 4 se recorren los enfrentamientos en orden de `id` del atacante y se **calculan** los golpes
que vencen (`now >= next_hit_at_ms`, atacante en alcance, objetivo válido), leyendo el estado tal como quedó al
terminar la fase 3. Después se **aplican** todos, y al final se procesan las muertes en orden de `id` de la
víctima. Un golpe calculado no depende de otro golpe del mismo tick: dos unidades que se matan a la vez mueren
las dos, y el resultado no depende del orden de un map ni de quién envió antes su orden. `next_hit_at_ms` avanza
desde su propio valor, no desde el tick, igual que la tarea de recolección se ancla a instantes absolutos.

### 2. Qué se persiste

| Dato | Cuándo | Por qué |
|---|---|---|
| La muerte: víctima `DEAD` y `hp = 0`, su movimiento cerrado y su tarea borrada, población de su ciudad −1, botín del asesino y víctima sin carga, `world_events` `UnitKilled` | Una transacción, encolada en el tick de la muerte | Es irreversible y cruza tres tablas: una muerte a medias deja población fantasma o madera duplicada |
| `hp` de las heridas y `loot` del guerrero | En el volcado de unidades | Cambian varias veces por segundo; perder un intervalo de volcado de daño en una caída es aceptable, perder una muerte no |
| El enfrentamiento (`atacante → objetivo`, `next_hit_at_ms`) y el estado `ATTACKING` | Nunca | Es intención del jugador, no estado del mundo. El volcado escribe `ATTACKING` como `IDLE`, y un reinicio termina los enfrentamientos; un tramo de persecución en curso se reanuda como un movimiento normal |

La muerte es optimista, como la finalización de una casa (ADR-023 §5): la RAM y los mensajes van primero. Si la
transacción se descarta, la compensación vuelve al loop y el siguiente volcado la reencola, idempotente porque
sólo actúa sobre una víctima que aún no está `DEAD`.

El depósito del botín sigue el camino del depósito de la recolección: suma al stock en RAM y llega a la base en
el volcado de stocks. El volcado escribe primero las unidades y después los stocks, así que una caída entre
ambos pierde madera y nunca la crea (`INV-COMBAT-003`).

### 3. El tope de la protección, derivado

`protection_until` no se escribe: se **deriva** de datos durables, `last_offline_at + cooldown + tope`
(`EO_CITY_PROTECTION_MAX_HOURS`, 0 = sin tope), al conceder la protección y al rehidratar. `City.ProtectedAt(now)`
es `PROTECTED` y `now < protection_until`. El autómata de presencia no cambia: una ciudad con la protección
vencida sigue `PROTECTED` hasta que su dueño vuelve, pero ya no frena ataques, y el cliente lo sabe por
`protectionUntilMs`.

### 4. El reanclaje al arrancar

Justo después de rehidratar, y fuera de `Hydrate` (que sigue sin escribir, `RN-PRESENCE-028`), toda ciudad
`ONLINE` sin sesión —el servidor cayó con su dueño conectado— pasa a `OFFLINE_PENDING` con la marca del
arranque. Desde ahí sigue el autómata normal: cooldown, protección y tope. Cierra la limitación de
[presence.md](../specs/presence.md) §7.6, que con combate dejaría ciudades expuestas para siempre.

## Alternativas descartadas

| Alternativa | Por qué no |
|---|---|
| Golpes secuenciales en orden de `id` | El atacante de `id` menor tendría ventaja sistemática: en un duelo igualado ganaría siempre el más antiguo. |
| Persistir el enfrentamiento como una tarea | Copiaría la maquinaria de `unit_tasks` para una intención que dura segundos y que el jugador puede volver a dar. ADR-023 ya descartó la tarea persistente como patrón general. |
| Escribir cada golpe en el acto | Decenas de escrituras por segundo en una pelea, sin nada irreversible que proteger hasta la muerte. |
| Un estado de presencia nuevo para la protección vencida | Obliga a cambiar el autómata, sus invariantes y el cliente para expresar algo que una fecha ya expresa. |

## Consecuencias

- La fase 4 deja de estar vacía; `Step` la llama entre el movimiento y los temporizadores.
- `INV-CITY-006` gana su test de rechazo y pasa a cubierto; nace la familia `INV-COMBAT`.
- DEBT-02 (protección indefinida) se cierra con el tope; el caso ONLINE tras una caída deja de ser una
  limitación conocida.
- Un reinicio a mitad de una pelea la detiene. Es la regla más simple y no regala nada a nadie: los dos bandos
  quedan parados.
