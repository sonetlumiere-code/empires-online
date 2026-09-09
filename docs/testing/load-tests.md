# Pruebas de carga

Diseño del nivel `load`: qué se mide, con qué herramientas, bajo qué perfiles y con qué criterios de aprobación. **Diferido: no forma parte del MVP.** Se documenta ahora para que las métricas, los contadores y las decisiones de diseño de las que depende existan desde el principio.

---

## 1. Estado: diferido

| Aspecto | Valor |
|---|---|
| Estado | **Fuera de MVP** (canon §19: *load — diferido, k6*) |
| Bloquea el vertical slice | **No.** Ningún pull request del MVP se rechaza por carecer de pruebas de carga |
| Se ejecuta en CI de pull request | **No** |
| Se ejecuta en la nocturna del MVP | **No** ([strategy.md](./strategy.md) §8.2) |
| Milestone de activación | Posterior al vertical slice (canon §20: después de M0–M7). El milestone concreto es **TBD (fuera de MVP)** |
| Qué sí se construye durante el MVP | Las métricas `eo_*` del canon §18 y los contadores de saturación de §8. Sin ellas, activar este nivel más tarde exigiría instrumentar a posteriori un sistema ya en producción |

Este documento describe un diseño, no un resultado. **Ninguna cifra de aquí es un número medido**: unas son presupuestos derivados aritméticamente del canon, y las que no lo son están marcadas explícitamente como pendientes de fijar.

---

## 2. Qué mide este nivel, y qué no

La carga **no descubre errores de lógica**. Un servidor que calcula mal una polilínea la calcula igual de mal con 10 000 conexiones. Lo que este nivel mide es la degradación: en qué punto el sistema deja de cumplir sus propias promesas temporales.

| Se mide aquí | No se mide aquí |
|---|---|
| Conexiones concurrentes sostenibles | Corrección funcional → [unit](./unit-tests.md), [simulation](./simulation-tests.md) |
| Comandos por segundo aceptados sin degradación | Consistencia del estado → [integration](./integration-tests.md) |
| Latencia comando → confirmación (p50, p95, p99, máx) | Forma de los mensajes → [contract](./contract-tests.md) |
| Duración del tick bajo carga y desbordamientos | Supervivencia a un crash → [recovery](./integration-tests.md#56-recuperación-tras-caída-del-servidor) |
| Memoria por jugador conectado y su evolución en el tiempo | |
| Crecimiento de la cola de persistencia | |

Regla de método: **un test de carga que falla funcionalmente es un test de carga inválido**, no un hallazgo. Antes de dar por buena cualquier medición, la suite funcional debe estar en verde sobre el mismo binario.

---

## 3. Magnitudes objetivo

### 3.1 Presupuestos derivados del canon

Estos valores **no se eligen**: se siguen de constantes que ya están fijadas. Son restricciones duras, no aspiraciones.

| Magnitud | Valor | De dónde sale |
|---|---|---|
| Período del tick | **100 ms** | `EO_TICK_RATE_HZ = 10` (canon §6) |
| Duración máxima admisible de un tick | **< 100 ms** siempre | Si un tick supera su período, `eo_game_tick_overruns_total` se incrementa (canon §6) |
| Presupuesto de trabajo por tick, objetivo de diseño | **≤ 50 ms** (50 % del período) | Deja margen para picos y para la variación del planificador del sistema operativo |
| Techo de tráfico por conexión | **20 msg/s**, ráfaga **40** | `EO_WS_RATE_LIMIT_PER_SECOND`, `EO_WS_RATE_LIMIT_BURST` (canon §13) |
| Techo teórico de comandos por segundo con N conexiones | **20 × N** | Consecuencia del anterior. El generador de carga no debe superarlo: si lo hace, mide el rate limiter, no el servidor |
| Tamaño máximo de frame | **16 384 bytes** | `EO_WS_MAX_MESSAGE_BYTES` (canon §13) |
| Cola de salida por conexión | **256** mensajes (rango 8–65536) | `EO_WS_OUTBOUND_QUEUE_SIZE`. Al llenarse, la sesión se cierra con **4500** y el cliente reconecta con un snapshot limpio: es una de las formas de degradación que el perfil de **pico** debe provocar y observar |
| Tráfico mínimo de fondo por conexión | ~**0,1 msg/s** de heartbeat | `EO_PRESENCE_HEARTBEAT_SECONDS = 10` (canon §9) |
| Chunks por conjunto de interés | **25** (5 × 5) | `EO_INTEREST_RADIUS_CHUNKS = 2` (canon §13) |
| Coste máximo de una consulta A\* | **20 000 nodos** | `EO_PATHFINDING_MAX_NODES` (canon §8) |
| Cadencia del flush de posiciones | cada **50 ticks** = 5 s | `EO_PERSISTENCE_FLUSH_INTERVAL_TICKS` (canon §12) |

**Latencia comando → confirmación: cota inferior estructural.** Un comando se drena en la fase 1 del siguiente tick y se aplica en la fase 2 (canon §6). Por tanto, incluso con carga cero y red de latencia nula, la confirmación tarda entre 0 y 100 ms según dónde caiga el comando dentro del período, con una media de ~50 ms. Cualquier objetivo de latencia se expresa **en ticks**, no en milisegundos absolutos, y el RTT de red se mide y se resta por separado. Un objetivo de "p99 < 30 ms" sería una imposibilidad aritmética, no una meta ambiciosa.

### 3.2 Objetivos por fijar

Estos dependen del hardware de destino y del presupuesto de operación, ninguno de los cuales está definido. Se fijan en el milestone de activación.

| Magnitud | Cómo se mide | Objetivo |
|---|---|---|
| Conexiones concurrentes (**N**) | `eo_connected_websockets` sostenido sin degradación de las demás magnitudes | **TBD (fuera de MVP)** |
| Jugadores concurrentes | `eo_connected_players` | **TBD (fuera de MVP)** |
| Comandos por segundo agregados | tasa de `eo_commands_total` | **TBD (fuera de MVP)**, con el techo `20 × N` de §3.1 |
| Unidades activas simuladas | `eo_active_units` | **TBD (fuera de MVP)** |
| p95 de latencia comando → confirmación | Histograma del generador de carga, correlacionando `requestId` con `unit.move.accepted` | Expresado en ticks; valor **TBD (fuera de MVP)** |
| p99 de latencia comando → confirmación | Ídem | Ídem |
| Memoria por jugador conectado | `(RSS con N conexiones − RSS en reposo) / N` | **TBD (fuera de MVP)** |
| Crecimiento de memoria en 24 h a carga constante | Pendiente de la regresión lineal del RSS | **≈ 0**. Este sí es un criterio absoluto: una pendiente positiva sostenida es una fuga, con independencia del hardware |

La distinción importa: las magnitudes de capacidad dependen de la máquina; **la ausencia de fugas y la ausencia de desbordamientos de tick no dependen de la máquina** y son criterios permanentes.

---

## 4. Cómo se mide la latencia comando → confirmación

Es la métrica que representa la experiencia real del jugador, y la más fácil de medir mal.

```
t0  cliente: envía  unit.move { requestId: R }          <- marca de tiempo del generador
                      │
                      │  red (RTT/2)
                      ▼
    servidor: el frame entra en la cola de comandos
                      │
                      │  espera hasta el siguiente tick: 0..100 ms
                      ▼
    tick n, fase 1: drain commands
    tick n, fase 2: validate & apply  ->  se crea unit_movements, se emite accepted
    tick n, fase 7: emit deltas
                      │
                      │  red (RTT/2)
                      ▼
t1  cliente: recibe  unit.move.accepted { requestId: R } <- marca de tiempo del generador

latencia = t1 - t0
```

Reglas de medición:

1. **Se correlaciona por `requestId`**, nunca por orden de llegada: los deltas de otros jugadores se intercalan.
2. Se mide y se reporta **el RTT de red por separado** (con `session.ping` / `session.pong`), para poder atribuir la latencia al servidor o al transporte.
3. Se reportan **p50, p95, p99 y el máximo**. La media se ignora: esconde exactamente lo que interesa.
4. Se reporta también la **latencia expresada en ticks** (`latencia / 100 ms`), que es la magnitud comparable entre entornos.
5. Los comandos rechazados (`unit.move.rejected`) se miden por separado: su camino es más corto y mezclarlos falsea la distribución.

---

## 5. Herramientas

Dos, con propósitos distintos y complementarios.

### 5.1 k6 — orquestación, umbrales e informes

Canon §19: la herramienta de carga es **k6**. Aporta perfiles de carga declarativos, umbrales que hacen fallar la ejecución, y salida en formato estándar.

Escenario descrito:

```
FASE A — Obtención del ticket (HTTP)
  Cada VU hace POST /api/auth/login contra el propio Game Server, que HOY es
  quien emite el game ticket (JWT HS256, TTL 60 s, canon §14) desde
  internal/httpapi. Es provisional: ADR-010 traslada la emisión a Next.js, y
  cuando eso ocurra esta fase apunta al frontend sin más cambios.
  Un ticket por conexión: son de un solo uso.

FASE B — Handshake (WSS)
  Abre wss://.../ws y envía session.hello { ticket } antes de 5 s
  (WSHandshakeTimeout, constante de código, no configurable por entorno).
  Espera session.welcome. Si llega un cierre 4401 o 4408, la iteración falla.

FASE C — Régimen (bucle por VU, con jitter para no sincronizar a todos los VU)
  - session.ping cada 15 s, la cadencia de WSPingInterval; se mide el RTT
    con el session.pong.
  - unit.move sobre una unidad propia cada X s, con destino dentro del área
    de interés; se registra t0 y se espera unit.move.accepted correlacionado
    por requestId; se registra t1.
  - session.view ocasional para desplazar el centro de interés un chunk.
  - Se consumen y descartan los deltas entrantes, contando bytes y frames.
  - El ritmo por VU se mantiene POR DEBAJO de EO_WS_RATE_LIMIT_PER_SECOND (20 msg/s):
    superarlo mediría el rate limiter, no el servidor.

FASE D — Cierre
  Cierre limpio del socket y volcado de métricas personalizadas.
```

Umbrales de k6 (`thresholds`) que hacen fallar la ejecución: tasa de errores de handshake, tasa de cierres inesperados, tasa de `RATE_LIMITED` no provocados deliberadamente, y los percentiles de latencia una vez fijados (§3.2).

**Limitaciones conocidas de k6 en este proyecto**, y por eso existe la segunda herramienta: no comparte los tipos del protocolo (podría derivar respecto de `@empires-online/protocol`), el coste por VU limita la densidad de conexiones por máquina generadora, y la lógica de correlación por `requestId` hay que escribirla a mano en JavaScript.

### 5.2 Generador propio en Go — fidelidad de protocolo y densidad

Un binario en `services/game-server/cmd/loadgen` (**TBD (fuera de MVP)** en cuanto a existencia; aquí solo se especifica).

| Propiedad | Por qué importa |
|---|---|
| Reutiliza las **estructuras de mensaje del propio servidor** y valida contra el mismo JSON Schema embebido | Imposible que el generador derive del protocolo real: si el contrato cambia, el generador no compila ([contract-tests.md](./contract-tests.md) §6) |
| Una goroutine por conexión | Decenas de miles de conexiones por máquina generadora, muy por encima de la densidad de VU de k6 |
| Histogramas de latencia con resolución alta y percentiles exactos | Los percentiles se calculan sobre todas las muestras, no sobre un muestreo |
| Modela el comportamiento real: movimiento del área de interés, reconexiones, desconexiones abruptas | Reproduce la carga de *interest management* y de presencia, que es la parte cara |
| Semilla explícita para el patrón de comandos | Dos ejecuciones comparables; misma disciplina de determinismo que el resto del proyecto (canon §1.5) |

**Reparto de responsabilidades.** k6 conduce los perfiles y produce el informe; el generador Go produce la carga de alta densidad y las mediciones finas. En una campaña completa se usan los dos: k6 para la rampa y el pico, el generador Go para la sostenida y el soak.

---

## 6. Perfiles de carga

| Perfil | Forma | Duración | Qué revela |
|---|---|---|---|
| **Rampa** | 0 → N conexiones con incremento lineal | 10–30 min | El **punto de inflexión**: el N a partir del cual la latencia o la duración del tick empiezan a crecer de forma no lineal. Es el dato más informativo de toda la campaña |
| **Sostenida** | N constante, tráfico constante | ≥ 60 min | Estabilidad en régimen: tick estable, latencia estable, memoria plana, cola de persistencia plana |
| **Pico** | N constante con saltos bruscos a 3–5 × N durante 30–60 s | 20 min | Comportamiento en saturación: ¿degrada de forma controlada (rate limiting, cierres `4429`) o colapsa? Y sobre todo, ¿**se recupera** al volver a N? |
| **Soak** | N moderado (≈ 50 % del punto de inflexión), tráfico constante | **24 h** | Fugas de memoria, crecimiento de la cola de persistencia, agotamiento de descriptores de fichero o de conexiones del pool, y cualquier degradación lenta que una hora de prueba no ve |

### 6.1 Qué se vigila específicamente en el soak de 24 h

El soak es el único perfil que detecta problemas acumulativos, y hay cuatro que este diseño puede sufrir:

1. **Fuga de memoria por conexión.** RSS con carga constante debe ser plano. Se ajusta una recta al RSS y se exige pendiente ≈ 0. Sospechosos habituales: sesiones que no se liberan al cerrar el socket y suscripciones a chunks que no se retiran.
2. **Crecimiento de la cola de persistencia.** `eo_persistence_queue_depth` debe oscilar alrededor de un valor estable. Una tendencia creciente significa que los workers no drenan al ritmo al que el loop encola: el tick sigue siendo rápido, pero el sistema acumula una deuda de escritura que acabará en pérdida de datos ante un crash. **Es el indicador más importante del soak**, porque el tick puede parecer sano mientras esto se degrada.
3. **Crecimiento monótono de tablas de historial.** `unit_movements` acumula filas `COMPLETED`, `CANCELLED` y `FAILED` (por diseño: preservar el historial es la razón de que `unit_movements_one_active_per_unit` sea un índice **parcial** sobre `status = 'ACTIVE'`, ver [../database/schema.md](../database/schema.md)). Se mide el crecimiento por hora y se comprueba que las consultas de arranque y de recuperación no se degradan con el tamaño del historial; la de arranque se apoya en `unit_movements_active_idx`, también parcial, así que su coste debería depender del número de movimientos vivos y no del historial. Verificarlo es justamente el objeto de este punto.
4. **Fuga de descriptores o de conexiones del pool.** Número de descriptores abiertos y conexiones activas del pool: planos con carga constante.

---

## 7. Criterios de aprobación

Una campaña de carga se declara aprobada si se cumplen **todos** los criterios. Igual que la Definition of Done, no hay aprobación parcial.

| # | Criterio | Umbral | Naturaleza |
|---|---|---|---|
| **L1** | Desbordamientos de tick | `eo_game_tick_overruns_total` **no se incrementa** durante toda la fase sostenida | Absoluto, independiente del hardware |
| **L2** | Duración del tick | p99 de `eo_game_tick_duration_seconds` **< 100 ms**; objetivo de diseño p95 ≤ 50 ms | Absoluto (el período es canónico) |
| **L3** | Latencia comando → confirmación | p95 y p99 dentro del objetivo, medidos en ticks y descontando el RTT | Umbral **TBD (fuera de MVP)** |
| **L4** | Errores | Cero cierres `4500` en los perfiles de **rampa**, **sostenida** y **soak**. Cierres `4429` sólo en el perfil de **pico** y sólo por encima del rate limit. En el pico se admiten cierres `4500` por desbordamiento de la cola de salida (`EO_WS_OUTBOUND_QUEUE_SIZE`), que es contrapresión declarada y no un fallo, siempre que L7 se cumpla | Absoluto |
| **L10** | Contrapresión de comandos | Ningún `INTERNAL_ERROR` por cola de comandos llena fuera del perfil de **pico**. Es la otra mitad de la contrapresión: cola de comandos llena ⇒ el comando se descarta y se responde `INTERNAL_ERROR` | Absoluto |
| **L5** | Cola de persistencia | `eo_persistence_queue_depth` estable, sin tendencia creciente en 24 h | Absoluto |
| **L6** | Memoria | Pendiente del RSS ≈ 0 en el soak de 24 h | Absoluto |
| **L7** | Recuperación tras el pico | Tras un pico de 5 × N, las métricas vuelven a los valores de régimen en **< 60 s** sin reiniciar el proceso | Absoluto |
| **L8** | Integridad | Al terminar la campaña, el estado en PostgreSQL es consistente: ninguna unidad `MOVING` sin movimiento `ACTIVE`, ningún `unit_movements` que viole `INV-MOVE-*`, y ninguna `units.chunk_x`/`chunk_y` desincronizada de su `x`/`y`. Se verifica ejecutando las comprobaciones de invariante sobre la base de datos resultante | Absoluto |
| **L9** | Funcionalidad bajo carga | Un cliente "testigo" ejecuta durante toda la campaña un guion funcional (mover, cancelar, reconectar) y **todas** sus aserciones pasan | Absoluto |

L8 y L9 son los que impiden el resultado más engañoso posible: un sistema que aguanta la carga porque ha empezado a perder trabajo en silencio.

---

## 8. Contadores de saturación a vigilar

Todas las métricas son las del canon §18, expuestas en `EO_METRICS_ADDR/metrics`. No se inventan métricas nuevas para este nivel: si algo no se puede diagnosticar con ellas, la carencia se corrige añadiendo la métrica al canon, no al script de carga.

| Métrica | Qué indica su saturación | Umbral de alerta |
|---|---|---|
| `eo_game_tick_overruns_total` | El loop no termina su trabajo dentro del período. **El síntoma más grave**: a partir de aquí el mundo va más lento que el tiempo real | Cualquier incremento en régimen |
| `eo_game_tick_duration_seconds` | Presupuesto del tick consumido. Su p99 acercándose a 100 ms anticipa L1 antes de que ocurra | p95 > 50 ms |
| `eo_persistence_queue_depth` | Los workers no drenan al ritmo del loop. Deuda de escritura acumulada | Tendencia creciente sostenida |
| `eo_database_latency_seconds` | PostgreSQL es el cuello de botella; suele ser la causa de la métrica anterior | Crecimiento correlacionado con la carga |
| `eo_redis_latency_seconds` | Redis satura: presencia, idempotencia y locks se degradan | Ídem |
| `eo_pathfinding_duration_seconds` | A\* consume el presupuesto del tick. Con `EO_PATHFINDING_MAX_NODES = 20000`, unas pocas consultas patológicas simultáneas bastan | p99 comparado con el presupuesto del tick |
| `eo_pathfinding_requests_total` | Volumen de búsquedas; se cruza con la métrica anterior para separar "muchas baratas" de "pocas caras" | — |
| `eo_commands_total` | Carga real aceptada. Si se aplana mientras el generador sube, el rate limiter está actuando | Aplanamiento inesperado |
| `eo_ws_messages_total` | Volumen total de frames, entrada y salida. Su crecimiento superlineal respecto a `eo_connected_websockets` delata un problema de *interest management* | Crecimiento superlineal |
| `eo_connected_websockets` / `eo_connected_players` | Conexiones sostenidas de verdad. Una caída sin caída del generador indica cierres del servidor | Divergencia entre ambas |
| `eo_active_units` | Tamaño real de la simulación; contextualiza todo lo demás | — |

**Señal compuesta más útil.** `eo_ws_messages_total` creciendo de forma superlineal frente a `eo_connected_websockets` con `eo_commands_total` plano significa que el coste está en el *broadcast*, no en los comandos: demasiados suscriptores por chunk o un conjunto de interés mal acotado. Es un problema de `EO_INTEREST_RADIUS_CHUNKS` y de la agrupación de deltas, no de capacidad de cómputo, y se arregla en otro sitio.

---

## 9. Entorno de ejecución

**No se ejecuta en la máquina de desarrollo.** Windows 10 con Docker Desktop y el generador de carga compartiendo CPU con el servidor produce números que solo describen esa máquina. Los resultados obtenidos así no se registran como medición.

Requisitos del entorno de campaña:

| Elemento | Requisito |
|---|---|
| Servidor bajo prueba | Máquina o contenedor **dedicado**, con recursos declarados y anotados junto a los resultados |
| Generadores de carga | Máquinas **distintas** de la del servidor. Si una sola no alcanza el N objetivo, se usan varias coordinadas |
| PostgreSQL y Redis | Instancias dedicadas y dimensionadas, no las de desarrollo |
| Red | Latencia y ancho de banda medidos y anotados: sin ese dato, la latencia p99 no es interpretable |
| Observabilidad | Scrapeo de `EO_METRICS_ADDR/metrics` durante toda la campaña, con retención de las series |
| Registro de resultados | Cada campaña anota: commit del binario, configuración `EO_*` completa, recursos de las máquinas, perfil ejecutado y series de métricas |

Sin el registro completo, dos campañas no son comparables y la tendencia entre versiones —que es el verdadero valor de este nivel a medio plazo— se pierde.

---

## 10. Qué se hace con los resultados

1. **El punto de inflexión de la rampa se documenta** como la capacidad conocida de una instancia. Es el número que gobierna cualquier discusión de escalado ([../architecture/scalability.md](../architecture/scalability.md)).
2. **Toda regresión de capacidad entre versiones se investiga**, aunque la campaña siga aprobando: una caída del 30 % en el punto de inflexión es un cambio de diseño que alguien introdujo sin darse cuenta.
3. **Ningún resultado de carga justifica relajar un invariante.** Si el sistema no llega al objetivo, se optimiza o se reduce el objetivo; no se quita el índice único, no se hace la persistencia *best effort*, no se mueve I/O de Postgres dentro del tick.
4. **Los hallazgos que exigen cambio estructural** (particionado del mundo, varios procesos de simulación) van a un ADR, no a un parche.

---

## 11. Documentos relacionados

- [strategy.md](./strategy.md) — la pirámide completa y por qué `load` está diferido.
- [simulation-tests.md](./simulation-tests.md) — coste del tick medido de forma determinista, sin carga real.
- [integration-tests.md](./integration-tests.md) — consistencia del estado, que L8 vuelve a verificar tras la campaña.
- [contract-tests.md](./contract-tests.md) — por qué el generador Go no puede derivar del protocolo.
- [../operations/monitoring.md](../operations/monitoring.md) — las métricas `eo_*`, su significado y sus alertas.
- [../architecture/scalability.md](../architecture/scalability.md) — límites conocidos del diseño de un solo proceso y vías de crecimiento.
- [../architecture/game-loop.md](../architecture/game-loop.md) — el presupuesto de 100 ms por tick y el origen de `eo_game_tick_overruns_total`.
- [../operations/configuration.md](../operations/configuration.md) — las `EO_*` que definen los techos de §3.1.
