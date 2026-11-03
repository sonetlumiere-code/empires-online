# Combate: incursión (slice de riesgo)

Propuesta del primer verbo hostil del juego: una unidad militar entrenada con madera ataca a una unidad
enemiga, la mata y se queda con lo que cargaba. Una unidad, una orden, una fase del tick; nada más.

| Campo | Valor |
|---|---|
| Spec ID | `SPEC-COMBAT-RAID` |
| Estado | Accepted (2026-11-02; las preguntas de §10 se decidieron ese día con la recomendación de cada una). Implementación en la rama `claude/slice-incursion`. |
| Milestone | E2 Incursión (segundo slice después de M7) |
| Canon | §9 (protección), §12 (persistencia), §16 (errores), §21 (fuera de MVP: combate) |
| Depende de | [unit.md](unit.md), [movement.md](movement.md), [presence.md](presence.md), [safe-zones.md](safe-zones.md), [garrison.md](garrison.md), [territory.md](territory.md), [economy-wood.md](economy-wood.md) |
| Decisiones | [ADR-016](../decisions/ADR-016-foreign-territory-actions.md) (clasificación de acciones), [ADR-022](../decisions/ADR-022-durable-write-retry-and-loop-inbox.md), [ADR-023](../decisions/ADR-023-economy-stock-deltas-and-two-phase-orders.md). [ADR-024](../decisions/ADR-024-combat-simultaneous-resolution.md) (resolución simultánea, qué se persiste, tope de la protección). |
| Reemplaza a | — |
| Invariantes | Cita los existentes de §8 y asigna `INV-COMBAT-001` a `INV-COMBAT-003` ([../invariants/combat.md](../invariants/combat.md)) |

## 1. Objetivo

Que salir de casa cueste algo. Hoy un aldeano en el bosque no corre ningún riesgo; con este slice otro jugador
puede matarlo y llevarse su carga. Es la primera vez que la protección offline, las Safe Zones, el ocultamiento
y los tratados **protegen de algo** (pilares de [vision.md](../product/vision.md)).

## 2. Scope

**Entra.** El tipo de unidad `WARRIOR`, entrenable con madera en la ciudad; la orden `unit.attack` sobre una unidad
enemiga visible; la persecución y la resolución del golpe en la fase 4 del tick; HP, muerte (`DEAD`) y botín; la
primera acción **hostil** de [ADR-016](../decisions/ADR-016-foreign-territory-actions.md) y su rechazo por
protección, Safe Zone, ocultamiento y tratado.

**No entra** (`Fuera de MVP` de este slice): atacar edificios o ciudades, asedio y saqueo del almacén; respuesta
automática del atacado (llega con [combat-response.md](combat-response.md)); daño a distancia, armadura, bonos de facción o civilización; curación; formaciones;
captura de territorio (`contested` sigue sin productor); romper un tratado atacando.

## 3. Actores

Jugador atacante y atacado, cliente web, Game Server (único autoritativo: posición, HP, daño y botín los decide
él, [INV-SEC-001](../invariants/security.md#inv-sec-001)), tick loop (fase 4), PostgreSQL.

## 4. Modelo

### 4.1 La unidad militar

`WARRIOR` entra en el catálogo `unit.Definition` junto a `VILLAGER`. Se entrena con `city.train` como el aldeano
(ocupa población, `INV-CITY-017`). No recolecta. Valores de partida (parámetros de catálogo, como los de
[economy-wood.md](economy-wood.md) §4.5; ver Q7):

| Parámetro | `WARRIOR` | `VILLAGER` (hoy) |
|---|---|---|
| Coste / tiempo | 60 madera / 25 s | 50 madera / 20 s |
| `max_hp` | 60 | 40 |
| Daño por golpe / intervalo | 8 / 1 500 ms | no ataca |
| Alcance | 1 (adyacente, 8 direcciones) | — |
| Carga máxima | 10 (sólo botín) | 10 |

### 4.2 La orden y la persecución

`unit.attack { unitId, targetUnitId }` sustituye cualquier orden en curso del atacante, como un `unit.move`.
El atacante pasa a `MOVING` hacia un tile adyacente al objetivo. Una vez en alcance queda en el estado nuevo
`ATTACKING` hasta que el enfrentamiento termina. Si el objetivo se aleja, la fase 4 recalcula la ruta, como mucho
cada `EO_COMBAT_REPATH_TICKS`, y abandona si queda a más de `EO_COMBAT_MAX_CHASE_TILES`.

### 4.3 Reglas

Dominio: `RN-COMBAT`.

| ID | Regla |
|---|---|
| `RN-COMBAT-001` | Sólo un `WARRIOR` propio, vivo y no guarnecido puede atacar. El objetivo es una unidad **de otro jugador**, viva y en el mapa. |
| `RN-COMBAT-002` | `unit.attack` es **hostil** (ADR-016) y su territorio es `LIBRE`: el suelo no restringe, la protección sí. Pasa por el punto único de `INV-CITY-006`. |
| `RN-COMBAT-003` | Si el dueño del objetivo tiene su ciudad `PROTECTED`, se rechaza con `CITY_PROTECTED` (Q1, Q2). `OFFLINE_PENDING` **no** protege: es la ventana de riesgo de la desconexión. |
| `RN-COMBAT-004` | Una unidad `HIDDEN` no existe para el atacante: se responde `UNIT_NOT_FOUND`, igual que si no existiera, para no filtrar su presencia (`INV-SAFE-004`). |
| `RN-COMBAT-005` | No hay golpe si el atacante o el objetivo están sobre un tile de Safe Zone. Al ordenar, `INVALID_TARGET`; durante la persecución, el enfrentamiento termina en cuanto el objetivo pisa una (Q4). |
| `RN-COMBAT-006` | Con un tratado `ACTIVE` de tipo `NON_AGGRESSION` o `ALLIANCE` entre los dos jugadores, `INVALID_TARGET` (Q5). |
| `RN-COMBAT-007` | **Resolución simultánea en la fase 4.** Con el estado al inicio de la fase se calculan, en orden de `id` del atacante, todos los golpes que vencen en el tick (atacante en alcance y `now >= next_hit_at_ms`); después se aplican todos. Dos unidades que se matan en el mismo tick mueren las dos. Sin azar. |
| `RN-COMBAT-008` | El enfrentamiento termina, y el atacante queda `IDLE`, cuando el objetivo muere, se oculta, entra en guarnición, pisa una Safe Zone, su dueño pasa a `PROTECTED`, nace un tratado entre ambos o se supera la distancia de persecución. |
| `RN-COMBAT-009` | **Muerte.** Con `hp = 0`: `status = DEAD`; se cancelan su movimiento y su tarea; la población de su ciudad baja en uno (`INV-CITY-017`); `entity.despawn { DEAD }`. La fila se conserva (Q6). |
| `RN-COMBAT-010` | **Botín.** Lo que cargaba la víctima pasa al atacante que dio el golpe mortal, hasta su carga máxima; el resto se pierde (Q3). Un `WARRIOR` con carga la deposita al llegar al tile de depósito de su ciudad (el de `RN-ECON-004`). Si muere, su carga pasa a quien lo mate. |
| `RN-COMBAT-011` | **Retirada** (2026-11-03) · sustituida por `RN-COMBAT-012` a `RN-COMBAT-018` de [combat-response.md](combat-response.md). Decía: la unidad atacada no responde sola; defenderse es una orden del jugador (Q8). |

**Precisión (2026-11-02).** Lo que la implementación decide dentro de estas reglas:

- El guerrero se mueve a 600 ms/tile, como el aldeano: así un aldeano no puede escapar a pie.
- La persecución apunta al tile del objetivo y se detiene en cuanto queda a alcance; el primer golpe cae en
  el instante del contacto y los siguientes se anclan a él, no al tick.
- Los tratados de paz se cargan en la RAM al arrancar y su caducidad se evalúa por fecha: hoy no existe vía
  para crearlos en caliente (backlog Q-04). Al aparecer una, deberá avisar a la simulación.
- `ATTACKING` sólo existe en la RAM. Tras un reinicio, un tramo de persecución en curso termina como un
  movimiento normal y el atacante queda `IDLE`.
- La carga ganada por el asesino viaja en el volcado de su unidad, no en la transacción de la muerte
  ([ADR-024](../decisions/ADR-024-combat-simultaneous-resolution.md) §2).

## 5. Errores

Sólo códigos existentes del catálogo (canon §16); ninguno nuevo salvo que Q5 lo decida.

| Código | Condición |
|---|---|
| `UNIT_NOT_FOUND` | El atacante o el objetivo no existen, o el objetivo no es visible para el atacante (`HIDDEN`, `GARRISONED`) |
| `UNIT_NOT_OWNED` | El atacante no es del jugador |
| `UNIT_NOT_MOVABLE` | El atacante no es de un tipo que ataque (`VILLAGER`) |
| `UNIT_DEAD` / `UNIT_GARRISONED` | El atacante está muerto o guarnecido |
| `INVALID_TARGET` | Objetivo propio, atacante u objetivo en Safe Zone, o tratado que lo impide |
| `CITY_PROTECTED` | El dueño del objetivo está `PROTECTED` |
| `PATH_NOT_FOUND`, `PATH_TOO_LONG` | Como en `unit.move`, para el tramo de aproximación |

## 6. Eventos y contratos de red

Cliente → servidor: `unit.attack` (nuevo). Servidor → cliente, sin mensajes nuevos: `entity.update { status:
ATTACKING, hp, carrying }`, `unit.movement.*` para la aproximación, `entity.despawn { reason: DEAD }`,
`city.update { population, stocks }`. `UnitStatus` gana `ATTACKING` y `UnitType`, `WARRIOR`. Eventos de dominio
en `world_events`: `UnitAttacked` (primer golpe de un enfrentamiento) y `UnitKilled` (con botín).

**Precisión (2026-11-03).** E2 sólo escribió `UnitKilled`; `UnitAttacked` llega con E3
([combat-response.md](combat-response.md) Q8).

## 7. Persistencia

| Pregunta (canon §12) | Respuesta |
|---|---|
| Autoritativo en RAM | HP, enfrentamientos (`attacker → target`, `next_hit_at_ms`), persecución |
| Write-through | La muerte con su botín y su descuento de población, en una transacción con su `world_events` |
| Dirty-flag | `hp` de las unidades heridas, en el volcado de posiciones |
| Reconstruible / no persistido | El enfrentamiento: un reinicio lo termina y los atacantes quedan `IDLE`. Una caída puede perder hasta un intervalo de volcado de daño; nunca una muerte ya escrita |

## 8. Invariantes

Cita [INV-UNIT-002](../invariants/units.md#inv-unit-002) (un `DEAD` no acepta órdenes),
[INV-UNIT-004](../invariants/units.md#inv-unit-004) (`hp` en rango),
[INV-CITY-006](../invariants/city.md#inv-city-006) (este slice escribe su **test de rechazo** y la deja
**cubierta**), [INV-CITY-017](../invariants/city.md#inv-city-017),
[INV-SAFE-004](../invariants/territory.md#inv-safe-004) e [INV-ECON-003](../invariants/economy.md#inv-econ-003).
Asigna [INV-COMBAT-001](../invariants/combat.md#inv-combat-001) (`DEAD` si y sólo si `hp = 0`),
[INV-COMBAT-002](../invariants/combat.md#inv-combat-002) (ningún golpe donde la protección, el refugio o el
ocultamiento lo prohíben) e [INV-COMBAT-003](../invariants/combat.md#inv-combat-003) (un combate nunca crea
madera).

## 9. Tests esperados

| Nivel | Qué |
|---|---|
| unit | Validación de `unit.attack` (cada fila de §5); resolución simultánea; botín con y sin hueco; fin del enfrentamiento por cada causa de `RN-COMBAT-008` |
| simulation | Golden «incursión»: guerrero contra aldeano cargado, ticks exactos de cada golpe, muerte, botín y depósito; muerte mutua en el mismo tick |
| recovery | Reinicio con un enfrentamiento en curso; muerte escrita y reinicio antes del volcado de HP |
| integration | Transacción de muerte con fallo inyectado (ni población ni botín a medias) |
| contract / e2e | `unit.attack` y los valores nuevos de los enums contra el JSON Schema; recorrido completo por WebSocket |
| cliente | Selección de objetivo con clic sobre unidad enemiga, barra de vida, botón «Guerrero»; `scripts/smoke.mjs` añade una incursión entre dos jugadores |

## 10. Decisiones de producto (2026-11-02)

Preguntas que la propuesta dejó abiertas; producto aceptó la recomendación de cada una. Son decisiones de
producto, no de ingeniería: cambiarlas es cambiar la spec.

| # | Pregunta | Decisión |
|---|---|---|
| Q1 | La protección offline es indefinida ([DEBT-02](../roadmap/backlog.md), «prerrequisito duro» del combate). ¿Qué tope? | Un tope configurable (`EO_CITY_PROTECTION_MAX_HOURS`, 48 h de partida) y cerrar a la vez el caso de [presence.md](presence.md) §7.6: tras un reinicio, una ciudad `ONLINE` sin sesión reancla `last_offline_at` al arranque. Sin eso, una caída del servidor deja ciudades expuestas para siempre. |
| Q2 | ¿Protege `PROTECTED` a las unidades que están fuera de la ciudad (backlog Q-08)? | Sí: la protección es del jugador. Si no, apagar el ordenador con un aldeano en el bosque costaría ese aldeano, y la promesa de presence.md sería falsa. |
| Q3 | ¿Qué pasa con la madera de la víctima? | Pasa al atacante hasta su carga y el resto se pierde. Es lo que da sentido a la palabra «incursión» y crea un ciclo de riesgo (el botín también se puede perder). Alternativa descartada: dejarla en el suelo, que exige una entidad nueva. |
| Q4 | Safe Zones: ¿se puede atacar **desde** una? | No: ni desde ni hacia. Una Safe Zone es un refugio, no una tronera. |
| Q5 | Tratados: ¿`NON_AGGRESSION` y `ALLIANCE` impiden atacar? ¿Con qué código? | Sí, ambos, con `INVALID_TARGET` y un mensaje claro: no amplía el catálogo. Romper un tratado atacando queda para la diplomacia. |
| Q6 | ¿Se conserva la fila de una unidad `DEAD` (backlog Q-05)? | Sí, sin cargarla al arrancar: auditoría barata. Archivar es una tarea de mantenimiento posterior. |
| Q7 | Números de §4.1. | Punto de partida: un guerrero mata a un aldeano en ~7 s y a otro guerrero en ~12 s; se ajustan probando. |
| Q8 | ¿Responde solo el atacado? | No en este slice: la respuesta automática es la primera mejora del siguiente. Resuelta por [combat-response.md](combat-response.md) (E3, 2026-11-03). |

## 11. Documentos relacionados

- [presence.md](presence.md) §6.2 y §7.6, [safe-zones.md](safe-zones.md), [garrison.md](garrison.md), [territory.md](territory.md).
- [../architecture/game-loop.md](../architecture/game-loop.md) (fase 4, reservada para esto desde M0).
- [../roadmap/backlog.md](../roadmap/backlog.md): DEBT-02, Q-05 a Q-08.
