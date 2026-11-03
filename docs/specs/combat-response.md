# Combate: respuesta automática

Slice que cierra el ciclo de la incursión: un guerrero parado responde solo cuando le pegan a él o a
una unidad suya cercana, sin que su dueño tenga que estar mirando.

| Campo | Valor |
|---|---|
| Spec ID | `SPEC-COMBAT-RESPONSE` |
| Estado | Implemented (2026-11-03). Aceptada el 2026-11-03 con las preguntas de §10 decididas ese día con la recomendación de cada una; en `main` desde `1e99a96`, con la CI en verde el mismo día. |
| Milestone | E3 Defensa (tercer slice después de M7) |
| Canon | §9 (protección), §12 (persistencia), §16 (errores) |
| Depende de | [combat-raid.md](combat-raid.md), [presence.md](presence.md), [safe-zones.md](safe-zones.md), [garrison.md](garrison.md), [movement.md](movement.md) |
| Decisiones | [ADR-024](../decisions/ADR-024-combat-simultaneous-resolution.md) (resolución simultánea). No se prevé ADR nuevo: la respuesta reutiliza el enfrentamiento y la fase 4 tal cual; si el diseño cambia algo estructural, irá en ADR-025. |
| Reemplaza a | `RN-COMBAT-011` de [combat-raid.md](combat-raid.md) (y su Q8) |
| Invariantes | Cita los existentes de §8; no asigna nuevos |

## 1. Objetivo

Que defender no exija estar delante de la pantalla. Hoy un guerrero al lado de sus aldeanos mira cómo los matan
si su dueño no da la orden, y una incursión contra alguien que no está atento sale gratis. Con este slice, dejar
un guerrero de guardia junto al bosque sirve de algo, y atacar tiene un precio aunque el otro esté ausente
(pilar de riesgo de [vision.md](../product/vision.md)).

## 2. Scope

**Entra.** La respuesta automática de un `WARRIOR` parado cuando le golpean a él o a una unidad propia dentro de un
radio de guardia (Q1, Q2); su persecución con correa (Q4) y su vuelta al puesto (Q5); la simetría de la
protección para quien ataca (Q6).

**No entra** (`Fuera de MVP` de este slice): agresión proactiva (atacar a un enemigo que pasa sin golpear); huida
automática de los aldeanos; posturas configurables por unidad (agresiva, defensiva, pasiva); que una unidad
guarnecida salga a defender; avisos al jugador fuera de la escena («te atacan»); atacar edificios o ciudades.

## 3. Actores

Jugador atacante y defensor (presente o ausente), Game Server (único que decide quién responde y cuándo,
[INV-SEC-001](../invariants/security.md#inv-sec-001)), tick loop (fase 4), cliente web (sólo muestra lo que ya
recibe: `ATTACKING`, movimiento y vida).

## 4. Modelo

### 4.1 El disparo

La respuesta no mira alrededor en cada tick: la dispara un **golpe**. Cuando la fase 4 aplica los golpes del tick
(`RN-COMBAT-007`), cada golpe de un atacante `A` sobre una víctima `V` alerta a:

1. `V`, si es un `WARRIOR`;
2. los `WARRIOR` del dueño de `V` a una distancia (Chebyshev) ≤ `EO_COMBAT_GUARD_RADIUS_TILES` de `V`.

Un guerrero alertado responde si está **disponible** (`RN-COMBAT-013`) y si `A` es atacable por él con el mismo
predicado que una orden del jugador (`INV-COMBAT-002`). Responder es abrir un enfrentamiento automático contra `A`,
con la misma persecución y los mismos golpes que el de `unit.attack`.

### 4.2 El puesto y la correa

Al responder, el guerrero recuerda su **puesto**: el tile donde estaba parado. Persigue mientras `A` siga a una
distancia de su puesto ≤ `EO_COMBAT_LEASH_TILES`; si `A` se aleja más, abandona. Cuando el enfrentamiento automático
termina por cualquier causa de `RN-COMBAT-008` (incluida la muerte de `A`), el guerrero vuelve a su puesto con un
movimiento normal y queda `IDLE` allí.

### 4.3 Reglas

Dominio: `RN-COMBAT`, continuando la numeración de [combat-raid.md](combat-raid.md). [combat-raid.md](combat-raid.md)
marca `RN-COMBAT-011` como retirada y sustituida por `RN-COMBAT-012` a `RN-COMBAT-018`.

| ID | Regla |
|---|---|
| `RN-COMBAT-012` | Sólo responde un `WARRIOR` vivo, en el mapa y no guarnecido. Los aldeanos no responden ni huyen (Q1). |
| `RN-COMBAT-013` | **Disponible** es `IDLE` (parado, sin tarea, sin movimiento y sin enfrentamiento) o volviendo a su puesto tras una respuesta; en el segundo caso, si responde, conserva su puesto original. Una orden del jugador en curso nunca se sustituye por una respuesta (Q3). |
| `RN-COMBAT-014` | El disparo es un golpe aplicado sobre el guerrero o sobre una unidad de su dueño a ≤ `EO_COMBAT_GUARD_RADIUS_TILES` (Q2). Ver o tener cerca a un enemigo que no golpea no dispara nada. Desde E5 también dispara un golpe de saqueo sobre su ciudad, medido desde el centro ([combat-pillage.md](combat-pillage.md) `RN-COMBAT-027`). |
| `RN-COMBAT-015` | **Orden y determinismo.** Las alertas se procesan después de aplicar todos los golpes del tick, en el orden de cálculo de los golpes (`id` del atacante); dentro de un golpe, primero la víctima y luego los guardias por `id`. Un guerrero responde a la primera alerta que le llega en el tick y no cambia de objetivo por alertas posteriores. Su primer golpe sigue la regla de contacto de `RN-COMBAT-007`. Sin azar. |
| `RN-COMBAT-016` | La respuesta es una acción hostil más: se valida con el predicado de `INV-COMBAT-002` desde el guerrero hacia `A` (protección, Safe Zone, ocultamiento, tratado). Si está bloqueada, no hay respuesta y el guerrero sigue `IDLE`. |
| `RN-COMBAT-017` | **Correa y vuelta.** Abandona cuando `A` queda a más de `EO_COMBAT_LEASH_TILES` de su puesto; al terminar el enfrentamiento automático por cualquier causa, vuelve a su puesto y queda `IDLE` (Q4, Q5). Si no hay ruta de vuelta, queda `IDLE` donde está. |
| `RN-COMBAT-018` | **Protección simétrica.** Cuando la ciudad de un jugador pasa a `PROTECTED`, terminan también los enfrentamientos **en los que él ataca**, no sólo los que sufre, y no puede abrir uno nuevo (Q6). |

Cualquier orden del jugador sobre el guerrero (`unit.move`, `unit.attack`, recolectar, guarnecer) sustituye la
respuesta y descarta el puesto, como ya sustituye cualquier orden anterior.

**Precisión (2026-11-03).** Lo que la implementación decide dentro de estas reglas:

- Las respuestas se abren en un cuarto paso de la fase 4, después de aplicar los golpes y las muertes del tick:
  sus golpes se calculan a partir del tick siguiente. Un guardia ya adyacente devuelve el golpe 100 ms después
  del que lo despertó.
- Un golpe despierta a los guardias aunque mate a la víctima: el guardia venga a la unidad que no pudo salvar.
- Tras un reinicio, la vuelta al puesto en curso es un movimiento normal: mientras dura, el guerrero no está
  disponible, porque la RAM ya no sabe que volvía.
- `UnitAttacked` es auditoría: si su escritura agota los reintentos de la cola, se registra el error y el evento
  se pierde, sin compensación. Ningún estado del mundo depende de él.

## 5. Errores

Ninguno nuevo y ninguno que llegue al cliente: la respuesta no es un comando, así que un bloqueo no produce
`command.rejected`; el guerrero simplemente no responde. `RN-COMBAT-018` hace que `unit.attack` de un jugador
`PROTECTED` sea imposible en la práctica (no tiene sesión), y si llegara, se rechazaría con `CITY_PROTECTED`.

| Código | Condición |
|---|---|
| — | La respuesta automática no tiene códigos de error propios. |

## 6. Eventos y contratos de red

Sin mensajes nuevos ni valores nuevos de enums. El cliente ve la respuesta con lo que ya recibe:
`unit.movement.*` para la persecución y la vuelta, `entity.update { status: ATTACKING, hp }` y
`entity.despawn { DEAD }`. Evento de dominio: según Q8, el primer golpe de cada enfrentamiento escribe
`UnitAttacked`, con `auto: true` en su payload cuando es una respuesta.

## 7. Persistencia

| Pregunta (canon §12) | Respuesta |
|---|---|
| Autoritativo en RAM | El enfrentamiento automático y su puesto |
| Write-through | Nada nuevo: la muerte sigue siendo la transacción de `RN-COMBAT-009` |
| Dirty-flag | Nada nuevo: `hp` y posición en el volcado habitual |
| Reconstruible / no persistido | La respuesta y el puesto: un reinicio los termina y el guerrero queda `IDLE` donde estuviera, igual que un enfrentamiento de `unit.attack` (combat-raid §4.3, precisión del 2026-11-02). La vuelta al puesto en curso es un movimiento normal y se recupera como tal |

## 8. Invariantes

Cita [INV-COMBAT-002](../invariants/combat.md#inv-combat-002) (la respuesta usa el mismo predicado, así que ningún
golpe automático cae donde la protección, el refugio o el ocultamiento lo prohíben; con `RN-COMBAT-018` el
predicado gana la protección del atacante), [INV-COMBAT-001](../invariants/combat.md#inv-combat-001),
[INV-COMBAT-003](../invariants/combat.md#inv-combat-003), [INV-UNIT-002](../invariants/units.md#inv-unit-002) e
[INV-CITY-006](../invariants/city.md#inv-city-006). No asigna invariantes nuevos: lo que añade son reglas de
comportamiento (quién responde, cuándo y hasta dónde), no propiedades del estado entre transacciones.

## 9. Tests esperados

| Nivel | Qué |
|---|---|
| unit | Disponibilidad (cada estado de `RN-COMBAT-013`); radio de guardia en el borde; bloqueo por cada causa de `RN-COMBAT-016`; correa en el borde; vuelta al puesto y vuelta sin ruta; orden del jugador que sustituye la respuesta; `RN-COMBAT-018` en ambos sentidos |
| simulation | Golden «escolta»: guerrero de guardia junto a un aldeano que recolecta, incursión, ticks exactos de la respuesta, muerte del incursor y vuelta al puesto; dos guardias y dos incursores en el mismo tick (orden de `RN-COMBAT-015`); incursor que huye más allá de la correa |
| recovery | Reinicio con una respuesta en curso y con una vuelta al puesto en curso |
| integration | Si Q8 se acepta: `UnitAttacked` en `world_events`, con `auto: true` en una respuesta |
| contract / e2e | Sin cambios de esquema: el contrato existente debe seguir en verde |
| cliente | Sin cambios de código previstos; `scripts/smoke.mjs --incursion` añade un guardia y comprueba que responde |

## 10. Decisiones de producto (2026-11-03)

Preguntas que la propuesta dejó abiertas; producto aceptó la recomendación de cada una. Son decisiones de
producto, no de ingeniería: cambiarlas es cambiar la spec.

| # | Pregunta | Decisión |
|---|---|---|
| Q1 | ¿Quién responde? | Sólo el `WARRIOR`. Los aldeanos no atacan; hacerlos huir es otra mecánica (rutas de escape, a dónde) y queda fuera. |
| Q2 | ¿Ante qué responde: sólo si le pegan a él, o también si pegan a una unidad suya cercana? | También a las suyas, en un radio de **4 tiles** (`EO_COMBAT_GUARD_RADIUS_TILES`). El incursor va a por aldeanos, que son los que cargan madera; un guardia que sólo se defiende a sí mismo no protege nada. Sin agresión proactiva: un enemigo que pasa sin golpear no provoca nada. |
| Q3 | ¿Responde un guerrero ocupado? | Sólo el que está `IDLE`. La orden del jugador manda siempre; la respuesta nunca la pisa. |
| Q4 | ¿Hasta dónde persigue? | Correa de **8 tiles** desde su puesto (`EO_COMBAT_LEASH_TILES`), más corta que los 32 de la persecución ordenada. Sin correa, un incursor se lleva al guardia lejos y vuelve a por los aldeanos. |
| Q5 | ¿Qué hace al terminar? | Vuelve a su puesto y queda `IDLE`. Alternativa descartada: quedarse donde terminó, que deja el puesto vacío y es fácil de explotar. |
| Q6 | Si el incursor se desconecta y su ciudad pasa a `PROTECTED` a mitad de la incursión, ¿sigue golpeando? | No: la protección es simétrica, como la Safe Zone (combat-raid Q4). Hoy la protección sólo impide **ser** atacado; sin este cambio, un incursor protegido seguiría golpeando a un defensor que ya no puede responderle. |
| Q7 | Números. | Punto de partida: radio 4, correa 8; se ajustan probando, sin tocar la spec mientras sigan siendo parámetros. |
| Q8 | [combat-raid.md](combat-raid.md) §6 promete el evento `UnitAttacked` (primer golpe de un enfrentamiento), pero E2 sólo escribe `UnitKilled`. ¿Lo escribimos en este slice o lo retiramos de la spec de E2? | Escribirlo aquí, para ataques ordenados y automáticos (`auto: true`): es una escritura diferida por enfrentamiento, barata, y es la única huella de una incursión que no mata. |

## 11. Documentos relacionados

- [combat-raid.md](combat-raid.md) (la incursión que esta spec completa), [presence.md](presence.md) §6,
  [safe-zones.md](safe-zones.md), [garrison.md](garrison.md).
- [../architecture/game-loop.md](../architecture/game-loop.md) (fase 4).
