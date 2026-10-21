# ADR-016: El territorio ajeno no restringe ninguna acción del MVP

Propósito: decidir qué acciones prohíbe estar dentro del territorio de otro jugador —el entregable 8 de M6,
bloqueado porque ninguna spec lo definía— y fijar el mecanismo que obliga a decidirlo de nuevo cada vez que
aparezca una acción.

| Campo | Valor |
|---|---|
| **Estado** | **Aceptado** |
| **Fecha** | **2026-10-20** |
| Ámbito | `internal/game/simulation` (clasificación de acciones), [../specs/territory.md](../specs/territory.md), [../specs/city.md](../specs/city.md) |
| Índice | [README.md](README.md) |
| Relacionados | [ADR-017](ADR-017-garrison-commands.md), [ADR-002](ADR-002-authoritative-server.md) |

---

## 1. Contexto

El entregable 8 de M6 pedía usar `FORBIDDEN` «para acciones no permitidas dentro de territorio ajeno, con la
superficie de acciones restringidas que defina la spec de territorio». La spec no la definía, y con razón:
hasta ahora el único comando de mundo era `unit.move`, y restringirlo era inventar una regla que cambia cómo
se juega. Con [ADR-017](ADR-017-garrison-commands.md) el protocolo gana `unit.garrison` y `unit.ungarrison`,
y la pregunta deja de ser teórica.

Tres hechos del juego pesan en la respuesta:

- **La rejilla de territorios cubre el mundo entero**, y fundar reclama el territorio del centro
  (`RN-TERR-008`). Alrededor de cada ciudad, el territorio es de su dueño.
- **Guarnecer en una ciudad aliada exige estar junto a ella** (`RN-GARR-003`), es decir, dentro del territorio
  del aliado. Prohibir moverse en territorio ajeno haría inalcanzable la única mecánica de diplomacia que
  existe.
- **La protección offline es indefinida** (canon §9). Si el territorio de un jugador desconectado fuera
  intransitable, bastaría con dejar de jugar para cortar rutas del mapa a todo el mundo, sin plazo.

La referencia de mecánicas es Age of Empires: el mapa es abierto, las fronteras no detienen a nadie y el
control del espacio es militar. Eso es lo que se adopta, sin copiar ninguna implementación.

---

## 2. Decisión

### 2.1 Ninguna acción del MVP se restringe por territorio

Moverse, cancelar un movimiento, guarnecer y salir de la guarnición están permitidos en cualquier territorio,
propio, ajeno o libre. Lo que limita la guarnición en una ciudad ajena es el **tratado** (`TREATY_REQUIRED`),
una regla sobre la ciudad y no sobre el suelo. El territorio sigue siendo lo que es en M6: significado
político persistente, visible en el overlay, sin efecto de juego todavía.

### 2.2 `FORBIDDEN` no se usa para el territorio

`FORBIDDEN` conserva el significado que ya tenía en el resto del sistema: una sesión válida que opera sobre
algo que no es suyo, a nivel de sesión ([../specs/player.md](../specs/player.md)). Estar en un sitio no es
operar sobre una entidad ajena. Cuando una acción futura deba prohibirse en territorio ajeno, su código se
decidirá con ella; no se reserva `FORBIDDEN` de antemano.

### 2.3 Toda acción de mundo se clasifica, y la clasificación es exhaustiva

`internal/game/simulation` declara, para cada mensaje cliente→servidor que actúa sobre el mundo, dos
propiedades:

| Propiedad | Valores | Efecto |
|---|---|---|
| **Hostil** | sí / no | Una acción hostil dirigida contra una ciudad `PROTECTED` o su territorio se rechaza con `CITY_PROTECTED` ([INV-CITY-006](../invariants/city.md#inv-city-006)). |
| **Regla de territorio** | `LIBRE` | Hoy es el único valor. Una acción restringida en territorio ajeno añadirá el suyo. |

| Acción | Hostil | Territorio |
|---|---|---|
| `unit.move` | no | `LIBRE` |
| `unit.cancel_move` | no | `LIBRE` |
| `unit.garrison` | no —la protección es contra agresión, no contra logística (`RN-GARR-009`)— | `LIBRE` |
| `unit.ungarrison` | no | `LIBRE` |

Los mensajes de sesión (`session.hello`, `session.ping`, `session.view`) no actúan sobre el mundo y se
declaran como tales en una lista aparte. Un test recorre el catálogo cerrado de mensajes cliente→servidor
(`protocol.ClientMessageTypes`) y falla si alguno no está ni clasificado ni en esa lista: añadir un comando
sin decidir si es hostil y dónde se permite deja de ser un olvido posible.

No hay todavía ninguna acción hostil, así que el punto único que aplicaría `CITY_PROTECTED` no se escribe: sería
código que ningún camino ejecuta. Nace con la primera acción clasificada como hostil, y el test de
exhaustividad es lo que garantiza que esa acción no pueda llegar sin clasificar.

---

## 3. Alternativas descartadas

| Alternativa | Por qué no |
|---|---|
| Prohibir moverse dentro de territorio ajeno | Hace inalcanzable la guarnición aliada, convierte a cualquier jugador desconectado en un muro permanente del mapa y no tiene equivalente en la referencia. |
| Prohibir sólo detenerse (`IDLE`) en territorio ajeno | Una regla que no se puede comunicar al jugador sin un sistema de avisos que no existe; y la guarnición exige precisamente detenerse junto a la ciudad aliada. |
| Usar `FORBIDDEN` para cualquier acción en territorio ajeno «por si acaso» | Reservaría un código con un segundo significado incompatible con el que ya tiene, y no protegería nada que hoy exista. |

---

## 4. Consecuencias

- El entregable 8 de M6 queda **decidido**: la superficie de acciones restringidas por territorio es vacía en el
  MVP, por diseño y no por omisión. M6 deja de tener un entregable bloqueado.
- [INV-CITY-006](../invariants/city.md#inv-city-006) gana su test de exhaustividad; el de rechazo sigue
  pendiente de la primera acción hostil.
- La spec de territorio deja escrito que el territorio no restringe acciones en el MVP y remite aquí.
- Cuando existan recolección, construcción o combate, cada una se clasificará al nacer. Las preguntas que
  entonces habrá que contestar —¿se puede recolectar en territorio ajeno?, ¿construir?— quedan abiertas a
  propósito: no tienen respuesta útil sin la mecánica delante.
