# ADR-008: Grid cartesiano de enteros, chunks de 32×32 y vecindad de 8 sin corner cutting

Propósito: fijar el sistema de coordenadas del dominio (grid cartesiano de enteros, proyección isométrica exclusivamente en el cliente), el tamaño de chunk (32×32 tiles) y la regla de vecindad (8 direcciones con prohibición de corner cutting), con el cálculo que justifica cada número.

- **Estado:** Aceptado
- **Fecha:** 2026-09-09
- **Decisores:** equipo de arquitectura
- **Relacionados:** [ADR-005](ADR-005-pixijs-renderer.md), [ADR-007](ADR-007-game-loop-frequency.md), [ADR-011](ADR-011-movement-timed-polyline.md)
- **Ámbito:** `services/game-server/internal/game/world`, `internal/pathfinding` y, cuando exista, el render de `apps/web`

---

## Contexto

Empires Online es un MMORTS de vista isométrica sobre un mundo continuo y persistente. La vista isométrica
es una decisión **de producto** (referencias: Age of Empires y Argentum Online) y por tanto una restricción
de entrada, no una consecuencia técnica. La pregunta que este ADR resuelve es otra: **en qué espacio razona
el servidor**, y con qué granularidad agrupa el mundo para gestionar el área de interés.

Todo lo que el servidor decide —caminos, colisión, ocupación, territorios, zonas seguras, suscripciones—
depende de esa elección. Y todo lo que el cliente dibuja depende de una proyección que el servidor no
necesita conocer.

Restricciones ya fijadas por el canon que este ADR debe respetar y explicar:

- Mundo MVP de **512 × 512** tiles (`EO_WORLD_WIDTH`, `EO_WORLD_HEIGHT`).
- Chunk de **32 × 32** tiles (`EO_CHUNK_SIZE=32`), radio de interés **2 chunks**
  (`EO_INTEREST_RADIUS_CHUNKS=2`).
- Terreno persistido en `world_chunks` como **bytea de 1024 bytes por chunk**.
- `TerrainType` es `uint8`, seis valores, y el bloqueo dinámico vive en una capa de ocupación separada
  (*blocked overlay*), sin mutar el terreno base.
- A\* con heurística **octile** en aritmética entera: costes de paso escalados con `costScaleOrtho = 1000`
  y `costScaleDiag = 1414`, y heurística ponderada por `MinTerrainCostUnits`, el coste mínimo de un terreno
  transitable del mundo (**6**, el de `ROAD`).

---

## Decisión

### Espacio del dominio: grid cartesiano de enteros

El mundo es un grid 2D de tiles cuadrados. Las coordenadas lógicas `x`, `y` son **int32**, con origen
`(0,0)` arriba-izquierda, **X hacia el este** e **Y hacia el sur**. En el MVP, `x, y ∈ [0, 511]`; una
coordenada fuera de rango se rechaza con `TARGET_OUT_OF_BOUNDS`. Consultar el terreno fuera de los límites
no es un error de programa: `TerrainAt` devuelve `WATER` e `IsWalkable` es `false`, de modo que el borde
del mundo se comporta como un muro y la expansión de vecinos de A\* no necesita casos especiales.

**El servidor no maneja píxeles jamás.** No conoce `TILE_W` ni `TILE_H`, no tiene noción de cámara, de
zoom ni de capas de render. La proyección isométrica es responsabilidad exclusiva del cliente:

```ts
// apps/web — única frontera donde existen los píxeles
const TILE_W = 64;
const TILE_H = 32;

const screenX = (x - y) * (TILE_W / 2);
const screenY = (x + y) * (TILE_H / 2);
```

`int32` y no `int16`: el rango cabría de sobra en 16 bits para 512×512, pero int32 mapea limpiamente a
`integer` en PostgreSQL y a `number` en JSON sin ambigüedad de signo, y deja margen para ampliar el mundo
sin migrar tipos de columna ni versionar el protocolo.

### Chunks de 32×32

```
chunkX = x >> 5
chunkY = y >> 5
chunksPerRow = EO_WORLD_WIDTH / EO_CHUNK_SIZE = 512 / 32 = 16
chunkId = chunkY * chunksPerRow + chunkX     // uint32, rango 0..255 en el MVP
```

`EO_CHUNK_SIZE=32` es potencia de dos: el desplazamiento `>> 5` sustituye a una división y, más importante,
elimina la clase de bug de las coordenadas negativas con división truncada hacia cero.

El payload de terreno de un chunk es exactamente **32 × 32 × 1 byte = 1024 bytes**, que es literalmente la
columna `bytea` de 1024 bytes que el canon define para `world_chunks`. El mundo MVP entero son
**256 chunks × 1024 B = 262 144 B = 256 KiB** de terreno: cabe íntegro y sin discusión en la RAM del
proceso, así que en el MVP no hay carga ni descarga dinámica de chunks; el chunk es una unidad de
**agrupación e interés**, no de paginación de memoria.

### Vecindad de 8 con prohibición de corner cutting

Un tile tiene 8 vecinos. El movimiento diagonal `(dx, dy)` con `dx ≠ 0` y `dy ≠ 0` solo se permite si
**ambos** tiles ortogonales adyacentes son transitables:

```
        x →
     +------+------+
  y  |  A   |  B   |     A = (x,   y  )   origen
  ↓  +------+------+     B = (x+1, y  )
     |  C   |  D   |     C = (x,   y+1)
     +------+------+     D = (x+1, y+1)   destino diagonal

  A → D es legal  ⟺  walkable(B) ∧ walkable(C) ∧ walkable(D)
```

«Transitable» combina dos capas: `TerrainType.walkable` (base, inmutable, en `world_chunks`) y la capa de
ocupación dinámica (*blocked overlay*, edificios y ciudades). Un tile de `MOUNTAIN` o `WATER` nunca es
transitable; un tile de `GRASSLAND` con un `TOWN_CENTER` encima tampoco, pero el terreno base sigue siendo
`GRASSLAND`.

Sin esta regla, una unidad atravesaría la esquina entre dos montañas o el vértice entre dos muros: visualmente
imposible en isométrico y una fuente inagotable de reportes de bug. Con ella, el coste es un par de consultas
adicionales por vecino diagonal durante la expansión de A\*.

El coste de un paso diagonal es `√2` veces el ortogonal —aplicado en punto fijo, `1414214/1000000`— en el
modelo de movimiento ([ADR-011](ADR-011-movement-timed-polyline.md)), y A\* replica esa relación con la
aproximación entera octile: `costScaleOrtho = 1000` y `costScaleDiag = 1414`, de donde `1414/1000 ≤ √2`.

Esa relación entre diagonal y ortogonal **no basta** para que la heurística sea admisible. La admisibilidad
exige que `h` nunca supere el coste real restante, y el coste real de un paso es `costUnits * escala`. Por
eso la heurística se pondera por el **coste mínimo de terreno transitable del mundo**,
`MinTerrainCostUnits`, que en el MVP vale **6** (el de `ROAD`):

```
h = minCost*1000*rectos + minCost*1414*diagonales      // minCost = MinTerrainCostUnits = 6
```

Usar el coste de la hierba (10) en su lugar haría la heurística **inadmisible** en cuanto existiera un solo
camino: un corredor recto de 10 tiles de `ROAD` cuesta realmente `10*6*1000`, mientras que una heurística
ponderada por 10 estimaría `10*10*1000`, sobreestimaría el resto y A\* dejaría de garantizar el camino
óptimo. El valor no está escrito a mano: se calcula recorriendo el catálogo de terrenos transitables, de
modo que añadir un terreno más barato que `ROAD` lo actualiza solo.

---

## Alternativas consideradas

### Coordenadas isométricas nativas en el servidor

Que el servidor razone directamente en el espacio de pantalla o en un sistema de ejes rotados 45°.

**A favor (real).** Servidor y renderer hablarían el mismo idioma: cero conversiones, cero desalineaciones
entre lo que el servidor cree y lo que el jugador ve. Las herramientas de depuración visual (volcar un
estado y superponerlo a una captura) serían inmediatas. Los movimientos «arriba-derecha» del ratón serían
ejes del dominio.

**En contra.** Contamina el dominio con presentación: `TILE_W` y `TILE_H` pasarían a ser configuración del
servidor, y cambiar el arte del cliente obligaría a tocar el servidor. La vecindad deja de ser trivial: en
un espacio rotado, «los 8 vecinos» no son un rectángulo y el pathfinding pierde la aritmética limpia de
`x±1, y±1`. La geometría rectangular de territorios (`min_x, min_y, max_x, max_y`) y las huellas
rectangulares de edificios dejarían de ser rectángulos en el espacio del dominio. Y el chunking por
desplazamiento de bits desaparecería. Rechazada.

### Grid hexagonal

**A favor (real).** Es técnicamente el mejor grid para movimiento: los seis vecinos están a la misma
distancia, no existe la asimetría ortogonal/diagonal ni el factor `√2`, no hace falta ninguna regla de
corner cutting, y los caminos resultantes se ven naturales sin post-suavizado. La heurística es más simple
y el coste de terreno se aplica sin factores direccionales.

**En contra.** Rompe dos cosas que el proyecto ya fijó. Primero, la estética: la referencia visual es el
isométrico clásico de tiles cuadrados; un mundo hexagonal es otro juego. Segundo, la construcción en
cuadrícula: `TOWN_CENTER`, la zona urbana amurallada inicial y los territorios rectangulares
(`min_x, min_y, max_x, max_y` en el canon) presuponen un grid cuadrado. Sobre hexágonos, un «rectángulo»
no existe y habría que redefinir territorios, huellas y muros. Añádase el coste del pipeline de arte y un
esquema de chunking axial u *offset* menos evidente que `>> 5`. Rechazada por incompatibilidad con el
producto, no por inferioridad técnica.

### Coordenadas continuas en punto flotante

Posiciones `float64` y movimiento libre en cualquier dirección.

**A favor (real).** Movimiento sin cuantización: giros suaves, formaciones, direcciones arbitrarias,
velocidades continuas. Es lo que hacen los RTS modernos y se ve mejor sin depender de la interpolación del
cliente. Las curvas de aceleración y el steering behaviour serían posibles.

**En contra.** Mata el determinismo, que es un principio no negociable del canon: la suma en coma flotante
no es asociativa y su resultado depende de orden, compilador y plataforma; dos ejecuciones del mismo
escenario pueden divergir, y con ellas los tests de simulación y la reproducibilidad de bugs. La detección
de colisiones deja de ser una consulta O(1) a la capa de ocupación y pasa a exigir broad-phase más
narrow-phase. El pathfinding deja de ser A\* sobre grid y pasa a exigir una navmesh. Y el modelo de
movimiento del proyecto ([ADR-011](ADR-011-movement-timed-polyline.md)) se apoya en waypoints discretos
`{x, y, tMs}`: en continuo habría que integrar en vez de evaluar, perdiendo la reconstrucción analítica que
es la base de la recuperación tras crash. Rechazada.

### Chunks de 16×16 o de 64×64

El compromiso es entre **granularidad del interest management** (chunk pequeño: el jugador se suscribe a
menos superficie inútil) y **sobrecarga por chunk** (chunk pequeño: más chunks, más conjuntos de
suscriptores, más filas, más grupos de difusión, más eventos de entrada y salida de chunk).

Para cuantificarlo hace falta saber cuánta superficie ve realmente un jugador. En proyección isométrica con
`TILE_W = 64` y `TILE_H = 32`, cada tile ocupa un rombo de `TILE_W · TILE_H / 2 = 1024 px²`. Un viewport de
1920 × 1080 px cubre por tanto `1920 · 1080 / 1024 ≈ **2025 tiles**`.

| `EO_CHUNK_SIZE` | tiles/chunk | bytes/chunk | chunks en 512×512 | chunks con radio 2 | tiles en el área de interés | veces el viewport (~2025 tiles) |
|---|---|---|---|---|---|---|
| 16 | 256 | 256 | 1024 | 25 (5×5) | 6 400 (80×80) | 3.2× |
| **32** | **1024** | **1024** | **256** | **25 (5×5)** | **25 600 (160×160)** | **12.6×** |
| 64 | 4096 | 4096 | 64 | 25 (5×5) | 102 400 (320×320) | 50.6× |

**16×16 — a favor (real).** Granularidad fina: el área de interés se ajusta mucho mejor a lo que el jugador
ve, así que se envían menos deltas de entidades que el cliente descartaría. Payload de 256 bytes por chunk,
cómodo para cualquier transporte.
**16×16 — en contra.** Con radio 2 el margen sobre el viewport es de solo 3.2×, es decir, unos 40 tiles de
colchón a cada lado: mover la cámara unas pocas decenas de tiles cruza fronteras de chunk constantemente y
provoca churn de suscripciones, y `session.view` está limitado por rate limit precisamente para que eso no
sea gratis. Habría que subir `EO_INTEREST_RADIUS_CHUNKS` a 3 (49 chunks) y entonces se pierde buena parte
de la ventaja. Además cuadruplica el número de chunks del mundo: 1024 conjuntos de suscriptores y 1024
filas en `world_chunks`.

**64×64 — a favor (real).** Sobrecarga mínima: 64 chunks para todo el mundo, cruces de frontera raros,
metadatos por chunk despreciables, menos eventos de resuscripción.
**64×64 — en contra.** El área de interés pasa a ser 50 veces el viewport. El servidor calcularía y
difundiría deltas de entidades que están a 150 tiles de la pantalla y que el cliente tira a la basura: es
ancho de banda y CPU de serialización desperdiciados en proporción directa. Y el payload de 4096 bytes por
chunk empieza a competir seriamente con `EO_WS_MAX_MESSAGE_BYTES=16384` en cuanto se combine con listas de
entidades.

**32×32 — por qué gana.** Da 12.6× de margen sobre el viewport: suficiente para que la cámara se mueva con
holgura sin resuscribir (histéresis natural y prefetch gratis), sin llegar al despilfarro de 64. Produce
exactamente los **1024 bytes por chunk** que el canon fija para `world_chunks`, un tamaño alineado con
páginas y buffers. Y mantiene el mundo en 256 chunks, un número que cabe en cualquier estructura sin
pensar. Es el punto medio, y se elige a sabiendas de que 12.6× de sobrecobertura es generoso: ese exceso es
el precio de no tener churn de suscripciones.

---

## Consecuencias

### Área de interés

- La suscripción es **por chunk**, no por radio continuo: el conjunto de interés de una sesión son los
  chunks `[cx−2, cx+2] × [cy−2, cy+2]`, recortados contra los bordes del mundo. Como máximo 25 chunks; en
  una esquina del mapa, 9.
- El área cubierta es de 160 × 160 tiles, unas 12.6 veces el viewport típico. Es deliberadamente generoso:
  el jugador recibe entidades que aún no ve, lo que elimina el *pop-in* al desplazar la cámara.
- Al conectar, el centro de vista es la ciudad del jugador; después lo actualiza `session.view`, sujeto a
  rate limit. Detalle en [../architecture/networking.md](../architecture/networking.md).
- **Negativo:** el jugador recibe información de entidades que están fuera de pantalla. Eso es superficie
  de *maphack* pasivo: un cliente modificado puede ver hasta ~80 tiles más allá del borde de su viewport.
  Reducir el radio reduce esa fuga y aumenta el churn; el MVP acepta la fuga.

### Serialización del mapa

- Un chunk de terreno es un `[1024]byte` denso, indexado como `local = (y & 31) * 32 + (x & 31)`. Sin
  compresión, sin RLE, sin estructuras: es más pequeño que cualquier alternativa comprimida con metadatos.
- El mapa se genera **determinísticamente desde `EO_WORLD_SEED`** (20260909) y de hecho **se regenera desde
  la semilla en cada arranque**: la copia de `world_chunks` existe para auditoría y para permitir mapas
  editados en el futuro, no como fuente primaria.
- La capa de ocupación dinámica es una estructura **separada** del terreno; su representación concreta la
  fija [../architecture/game-server.md](../architecture/game-server.md). Como orden de magnitud, una máscara
  de bits para 512×512 ocuparía 32 KiB, despreciable frente a los 256 KiB del terreno.
- El terreno viaja al cliente dentro de `world.snapshot`: el campo `terrain[]` lleva, por chunk, los
  `size*size` bytes **codificados en base64** (~1368 caracteres por chunk de 32×32). Como el terreno es
  inmutable, cada chunk se envía **una sola vez por sesión**: la sesión recuerda cuáles ya entregó
  (`NeedsTerrain` / `MarkTerrainSent`) y los siguientes snapshots solo llevan los chunks nuevos.
- **Negativo:** ese ahorro no basta para el primer snapshot. Los 25 chunks del área de interés en base64
  rondan los 34 KiB, muy por encima de `EO_WS_MAX_MESSAGE_BYTES=16384`. Hoy no revienta nada porque ese
  límite se aplica **solo al sentido entrante** (`SetReadLimit` sobre la conexión) y el snapshot saliente no
  se acota, pero es una asimetría conocida: el troceado del terreno para respetar el mismo presupuesto en
  ambos sentidos es **TBD (fuera de MVP)**.

### Pathfinding y movimiento

- A\* trabaja sobre enteros de principio a fin: coste de paso `costUnits * costScaleOrtho(1000)` o
  `costUnits * costScaleDiag(1414)`, heurística octile ponderada por `MinTerrainCostUnits = 6` y desempate
  determinista del heap por `(f, h, y, x)`. No hay ni un `float` en la búsqueda, lo que hace el resultado
  bit a bit reproducible en cualquier máquina.
- La prohibición de corner cutting se aplica **en la expansión de vecinos**, no como post-filtro del camino:
  un camino que atraviesa una esquina nunca llega a existir.
- El movimiento tampoco usa punto flotante: el factor diagonal es la fracción entera `1414214/1000000`,
  aplicada y redondeada al milisegundo más cercano **por segmento** antes de acumular
  ([ADR-011](ADR-011-movement-timed-polyline.md)).
- **Negativo:** los caminos de un grid de 8 direcciones tienen el aspecto escalonado característico. No hay
  suavizado de trayectoria en el MVP; la interpolación del cliente suaviza el tránsito entre waypoints, no
  la forma del camino.

### Sharding futuro

- `chunkId` es `uint32` y es la clave natural de partición: un shard futuro poseería un conjunto de chunks
  y las fronteras entre shards serían fronteras de chunk.
- **Negativo importante:** la fórmula `chunkY * chunksPerRow + chunkX` es orden *row-major* y **no preserva
  localidad 2D**. Los chunks 15 y 16 son consecutivos en id pero están en extremos opuestos del mapa. Una
  partición por rangos de `chunkId` trocearía el mundo en bandas horizontales: funcional, pero no óptimo,
  porque un jugador cerca de una frontera norte-sur cruzaría de shard con mucha más frecuencia de la
  necesaria. Un orden de curva de recubrimiento (Morton o Hilbert) preservaría localidad, pero **cambiaría
  la fórmula del id**, que es canónica y ya está en el protocolo y en `world_chunks`. Ese cambio exige un
  ADR que supersede a este, no un parche.
- Discusión de límites en [../architecture/scalability.md](../architecture/scalability.md).

### Otras consecuencias negativas

- **Dos sistemas de coordenadas conviviendo.** El cliente traduce en cada frame entre tiles y píxeles.
  Cualquier bug de conversión se manifiesta como «el servidor dice una cosa y la pantalla otra», y es de
  los más caros de diagnosticar. Mitigación: la conversión vive en un único módulo de `apps/web`, con tests
  de ida y vuelta.
- **El grid cuadrado exige la regla de corner cutting como parche.** Es un remiendo sobre una topología que,
  a diferencia de la hexagonal, no es uniforme. Es barato, pero es un caso especial que hay que recordar en
  cada sitio donde se pregunte por adyacencia (pathfinding, garrison, adyacencia de zonas seguras a
  `CAVERN`), no solo en A\*.
- **512×512 es pequeño.** 262 144 tiles para un MMO son un mundo modesto; es una decisión de MVP, no un
  límite del diseño. Ampliarlo solo cambia `EO_WORLD_WIDTH` / `EO_WORLD_HEIGHT` y el número de chunks,
  pero invalida la suposición «el mundo cabe en RAM» que este ADR usa para descartar la paginación de chunks.

### Neutras

- `EO_WORLD_WIDTH` y `EO_WORLD_HEIGHT` pasan a estar acoplados a `EO_CHUNK_SIZE`: el arranque valida que
  sean múltiplos exactos y falla si no lo son. Cambiar el tamaño del mundo es cambiar también el número de
  chunks y el rango de `chunkId`.
- La capa de ocupación (*blocked overlay*) se manipula por rectángulos (`SetBlocked(minX, minY, maxX, maxY, bool)`),
  lo que encaja con las huellas rectangulares de edificios y con la muralla 3×3 de la ciudad inicial, y
  deja el terreno base intacto: fundar una ciudad no reescribe ningún byte de `world_chunks`.
- `MinTerrainCostUnits` se deriva del catálogo de terrenos, así que el catálogo pasa a ser también una
  entrada del pathfinding: tocar un coste de terreno cambia la heurística, no solo el tiempo de viaje.

---

## Verificación

| Qué se verifica | Cómo |
|---|---|
| Ida y vuelta tile ↔ chunk | Test unitario exhaustivo sobre `[0,511]²`: `chunkId(x,y)` y su inversa coinciden. |
| Payload de chunk | Test: todo chunk serializa exactamente 1024 bytes. |
| Corner cutting | Test con dos tiles `MOUNTAIN` en diagonal: A\* nunca produce un camino que cruce la esquina. |
| Determinismo del mapa | Dos generaciones con el mismo `EO_WORLD_SEED` producen bytes idénticos en los 256 chunks. |
| Límites | `TARGET_OUT_OF_BOUNDS` para cualquier coordenada fuera de `[0, EO_WORLD_WIDTH−1] × [0, EO_WORLD_HEIGHT−1]`. |

Los invariantes `INV-WORLD-*` correspondientes están en [../invariants/world.md](../invariants/world.md).

---

## Referencias

- [../architecture/pathfinding.md](../architecture/pathfinding.md) — A\*, octile, desempate, límites.
- [../architecture/networking.md](../architecture/networking.md) — suscripción por chunk y `session.view`.
- [../architecture/frontend.md](../architecture/frontend.md) — proyección isométrica y render.
- [../architecture/scalability.md](../architecture/scalability.md) — chunk como futura unidad de sharding.
- [../invariants/world.md](../invariants/world.md) — `INV-WORLD-*`.
- [README.md](README.md) — índice de ADR.

---

## Estado

**Aceptado** el 2026-09-09.

La fórmula de `chunkId` y el tamaño de chunk están en el protocolo y en `world_chunks`: cambiarlos exige un
ADR que sustituya a este, no un parche. Los dos disparadores previstos para esa revisión son la ampliación
del mundo más allá de lo que cabe en RAM y una partición por shards que necesite un orden con localidad 2D
(Morton o Hilbert) en lugar del actual *row-major*.
