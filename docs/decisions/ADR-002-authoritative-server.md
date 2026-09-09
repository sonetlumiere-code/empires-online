# ADR-002: Servidor autoritativo

Propósito: establecer que el cliente envía únicamente intención y que el servidor es la única autoridad sobre el estado del mundo, sin excepciones ni zonas de confianza.

- **Estado:** Aceptado
- **Fecha:** 2026-09-09
- **Decisores:** equipo de arquitectura
- **Relacionados:** [ADR-001](ADR-001-game-server-language.md), [ADR-003](ADR-003-postgresql-source-of-truth.md), [ADR-006](ADR-006-websocket-protocol.md), [ADR-010](ADR-010-authentication-game-ticket.md), [ADR-011](ADR-011-movement-timed-polyline.md)

---

## Contexto

Empires Online es un mundo persistente compartido con riesgo real: las unidades ocupan un espacio común, las ciudades tienen ownership, el territorio se disputa y —fuera del MVP— habrá combate y economía. En un juego así, **el estado del mundo es el activo del jugador**. Cualquier vía por la que un cliente pueda inyectar estado equivale a una vía para fabricar ese activo de la nada.

El cliente es código que se ejecuta en una máquina que el operador no controla. `apps/web` —todavía no implementado; llegará en su milestone— será una aplicación web: su JavaScript es legible, modificable e instrumentable con las herramientas del propio navegador. No existe ofuscación, anti-cheat de cliente ni atestación que cambie ese hecho; solo elevan el coste del ataque, nunca lo eliminan. La conexión es WSS, lo que impide a un tercero interceptar la sesión, pero **no** impide que el dueño de la sesión envíe lo que quiera por ella.

Al mismo tiempo, el juego tiene componentes de tiempo real percibido: las unidades se mueven de forma continua en pantalla y el usuario espera reacción inmediata al hacer clic. Ese requisito de sensación empuja hacia dar poder al cliente, y hay una tradición larga de hacerlo (predicción del lado del cliente, movimiento local con validación posterior). La tensión entre integridad y latencia percibida es el centro de esta decisión.

El canon técnico ya fija el principio como no negociable: *"Client sends intent, server determines truth"*. Este ADR existe para dejar registrado **por qué** es no negociable, qué se compra con él y qué se paga, de modo que nadie lo relaje más adelante por conveniencia.

Restricciones concretas del sistema que enmarcan la decisión:

- El game loop corre a 10 Hz con un presupuesto de 100 ms por tick, y la validación de comandos es la fase 2 de ocho ([../architecture/game-loop.md](../architecture/game-loop.md)).
- El protocolo v1 solo define cinco mensajes cliente→servidor: `session.hello`, `session.ping`, `session.view`, `unit.move`, `unit.cancel_move`. Ninguno transporta estado del mundo.
- El mundo evoluciona sin jugadores conectados; ningún estado durable depende de que un WebSocket siga vivo.
- El servidor es el único que decide el estado de presencia de una ciudad (`ONLINE`, `OFFLINE_PENDING`, `PROTECTED`); el cliente lo observa.

## Decisión

**El servidor es la única autoridad sobre el estado del juego. El cliente jamás aporta estado autoritativo.**

Se concreta en cuatro reglas operativas:

1. **El cliente envía intención, no resultados.** Un comando expresa qué quiere hacer el jugador, con la mínima información necesaria para identificar la intención. `unit.move` transporta `{ unitId, target:{x,y} }` y nada más: **nunca** una secuencia de posiciones, un path precalculado, un ETA ni una posición final.
2. **El servidor calcula todo lo que tenga consecuencias.** Quedan bajo autoridad exclusiva del servidor, sin posibilidad de que el cliente los proponga: posición final y posiciones intermedias, HP, recursos, resultados de combate (fuera de MVP), ownership, cooldowns, ETA, paths, estado de presencia y protección, y la seguridad de las SafeZone.
3. **Todo comando se valida siempre, aunque la interfaz ya lo hubiera impedido.** El orden de validación de `unit.move` es: existencia de la unidad → ownership → estado de la unidad → destino dentro del mundo → destino transitable → pathfinding → creación del movimiento. La propiedad se comprueba antes que cualquier condición del mundo a propósito: así el error recibido no filtra información sobre unidades ajenas. Cada paso tiene su código de error estable, y ninguno se salta porque "el cliente no debería poder enviar eso".
4. **La interfaz de usuario nunca aplica un cambio de estado por su cuenta.** El cliente puede mostrar retroalimentación inmediata (un marcador de destino, un cursor de espera), pero el estado que dibuja procede exclusivamente de mensajes del servidor: `unit.move.accepted`, `unit.movement.started`, `entity.update`, `unit.movement.completed`.

### Superficie de confianza

```
┌──────────────────────── NO CONFIABLE ────────────────────────┐
│  Navegador del jugador                                       │
│  · Render PixiJS, interpolación visual, input                │
│  · Puede ser modificado, automatizado o reemplazado          │
└───────────────┬──────────────────────────────────────────────┘
                │  WSS · JSON v1 · SOLO intención
                │  session.hello / session.ping / session.view
                │  unit.move / unit.cancel_move
                ▼
┌──────────────────────── FRONTERA DE CONFIANZA ───────────────┐
│  Game Server (Go)                                            │
│  1. Autenticación (ticket JWT, jti consumido en Redis)       │
│  2. Límites de transporte (16 KiB, 20 msg/s, burst 40)       │
│  3. Validación de esquema y de versión                       │
│  4. Validación de dominio (ownership, estado, destino)       │
│  5. Simulación autoritativa en el tick                       │
└───────────────┬──────────────────────────────────────────────┘
                │  deltas: entity.spawn / entity.update /
                │  entity.despawn / unit.movement.* / city.update
                ▼
        PostgreSQL (durable)  +  Redis (hot state)
```

Nada a la izquierda de la frontera es dato de entrada fiable. Todo lo que cruza hacia la derecha es **una petición**, no un hecho.

### Qué comprueba el servidor en `unit.move`

| Comprobación | Falla con |
|---|---|
| Sesión autenticada y viva | `UNAUTHORIZED` (cierre `4401` si no hay sesión) |
| Mensaje bien formado y `v` soportado | `INVALID_MESSAGE`, `UNSUPPORTED_VERSION` |
| Tamaño ≤ `EO_WS_MAX_MESSAGE_BYTES` | `MESSAGE_TOO_LARGE` (cierre `4400`) |
| Tasa dentro de `EO_WS_RATE_LIMIT_PER_SECOND` / burst | `RATE_LIMITED` (cierre `4429`) |
| La unidad existe | `UNIT_NOT_FOUND` |
| La unidad pertenece al jugador de la sesión | `UNIT_NOT_OWNED` |
| La unidad puede moverse: `IDLE` y `MOVING` la aceptan (una orden nueva reemplaza a la anterior) y `HIDDEN` también, porque salir de una Safe Zone es legítimo y revela la unidad | `UNIT_DEAD`, `UNIT_GARRISONED`, `UNIT_NOT_MOVABLE` |
| El destino está dentro del mundo | `TARGET_OUT_OF_BOUNDS` |
| El tile destino es transitable | `TARGET_NOT_WALKABLE` |
| Existe un path dentro de los límites | `PATH_NOT_FOUND`, `PATH_TOO_LONG` |

El cliente puede deshabilitar botones para evitar peticiones inútiles, pero esa comprobación es **cortesía de interfaz**, no seguridad. La lista anterior se ejecuta íntegra en cada comando.

## Alternativas consideradas

### A. Confianza en el cliente (el cliente reporta su estado)

**Ventajas reales.** Es, con diferencia, la opción más barata y la que produce mejor sensación de respuesta inmediata: el cliente mueve su unidad al instante y comunica el resultado. El servidor se reduce a un relé que retransmite y persiste, lo que elimina el pathfinding del servidor, reduce su CPU a casi nada y simplifica el protocolo. Para un juego cooperativo entre amigos, o para un prototipo, es una elección legítima y muchos títulos pequeños la usan con éxito.

**Por qué se descarta.** Empires Online es un mundo persistente compartido con riesgo entre extraños. Con esta opción, un jugador con la consola del navegador abierta puede reescribir su posición, sus recursos y el resultado de cualquier interacción. No hay mitigación posible: el estado inyectado *es* el estado. Un único jugador deshonesto degrada la experiencia de todos los demás de forma permanente, porque el mundo persiste y el daño no se borra al terminar la partida.

### B. Autoridad distribuida por región o "host migration"

**Ventajas reales.** Un cliente promovido a autoridad de su propia región (su ciudad, su chunk) reduce el trabajo del servidor central y escala horizontalmente sin coste de infraestructura; es un patrón conocido en juegos peer-to-peer y en algunos MMO con sharding agresivo, y elimina la latencia para las acciones locales del jugador que hospeda.

**Por qué se descarta.**

- El árbitro de una región tiene interés directo en el resultado de lo que arbitra. El conflicto de intereses es estructural, no un fallo de implementación.
- Aparecen problemas que el modelo centralizado ni siquiera tiene: consenso entre regiones, resolución de acciones que cruzan fronteras (una unidad que camina de un chunk a otro), y qué ocurre cuando el árbitro se desconecta a mitad de una interacción disputada.
- Contradice el principio de mundo persistente: el estado durable pasaría a depender de que un WebSocket concreto siga vivo.
- La complejidad total sería **mayor** que la del servidor autoritativo, no menor. Se descarta también por coste, no solo por seguridad.

### C. Autoridad del servidor con predicción y reconciliación en el cliente

**Ventajas reales.** Es el estándar de la industria en shooters y action-RPG y **no compromete la integridad**: el servidor sigue siendo la autoridad; el cliente simplemente adelanta el resultado que espera y lo corrige cuando llega la verdad. Elimina por completo la latencia percibida al iniciar una acción.

**Por qué se descarta *para el MVP*, y aquí conviene ser preciso.** Predicción y reconciliación exigen que el cliente ejecute **una copia fiel de la simulación del servidor**: mismo A\*, mismos costes de terreno, mismo redondeo a milisegundo entero, misma regla de corner cutting. Eso significa duplicar el dominio en TypeScript y mantener las dos implementaciones sincronizadas bit a bit, con un buffer de comandos y un mecanismo de rollback cuando divergen. Es un coste alto y una fuente permanente de bugs sutiles.

Y para este juego el beneficio es pequeño: las acciones del MVP no son de reflejos. Un `unit.move` con `VILLAGER` a 600 ms por tile tolera perfectamente un round-trip de red antes de que la unidad arranque. **La mitigación elegida es interpolación visual, no predicción**: el cliente interpola suavemente entre las posiciones que el servidor le da, sin simular nada por su cuenta. Esta alternativa queda **fuera de MVP**, no rechazada para siempre: si en el futuro llega el combate en tiempo real, se reevaluará en un ADR propio. Nótese que la polilínea temporizada de [ADR-011](ADR-011-movement-timed-polyline.md) ya entrega al cliente toda la información necesaria para interpolar con exactitud.

### D. Autoridad del servidor con validación posterior ("optimistic client")

**Ventajas reales.** El cliente actúa de inmediato y el servidor valida a posteriori, revirtiendo solo cuando detecta una violación. Da la respuesta inmediata de la opción A con parte de la protección de la opción C, y es más barata que la predicción completa porque no exige simulación espejo.

**Por qué se descarta.** Convierte la reversión en algo visible y frecuente para el usuario honesto (una unidad que "salta hacia atrás" cuando el servidor discrepa), y obliga igualmente a implementar la validación completa en el servidor —el ahorro es menor de lo que parece—. Peor aún: la validación posterior es difícil de hacer correcta cuando el estado ya se propagó a otros jugadores, y el rango de tolerancia que se conceda al cliente se convierte, exactamente, en el margen que un atacante explotará.

## Ataques que este modelo previene

| Ataque | Cómo se intentaría | Por qué falla |
|---|---|---|
| **Speedhack** | Acelerar el reloj local o emitir comandos a mayor frecuencia para moverse más rápido. | El tiempo de recorrido lo calcula el servidor en aritmética entera: `ms = (baseMsPerTile * costUnits + 5) / 10` por segmento y, si es diagonal, `ms = (ms * 1414214 + 500000) / 1000000` (√2 en punto fijo), redondeando cada segmento antes de acumularlo en la polilínea. `baseMsPerTile` es propiedad del tipo de unidad (`VILLAGER` = 600 ms) y `costUnits` sale del terreno del tile destino. El reloj del cliente no participa. Además, el spam de comandos choca con el rate limit de `EO_WS_RATE_LIMIT_PER_SECOND=20` (burst 40). |
| **Teleport** | Enviar una posición arbitraria, o un destino al otro extremo del mapa. | El cliente no puede enviar posiciones: solo un destino. El servidor exige un path válido con A\*, rechaza destinos fuera del mundo (`TARGET_OUT_OF_BOUNDS`), destinos intransitables (`TARGET_NOT_WALKABLE`) y paths que superen `EO_PATHFINDING_MAX_DISTANCE=256` tiles (`PATH_TOO_LONG`). La posición en cualquier instante se deriva de la polilínea persistida, no de lo que diga el cliente. |
| **Atravesar obstáculos** | Pedir un destino al otro lado de una montaña o de un edificio. | A\* opera sobre el terreno (`MOUNTAIN` y `WATER` no son walkable) más la capa de ocupación (`blocked overlay`), y prohíbe el corner cutting en diagonal. No hay ruta que el servidor no haya calculado él mismo. |
| **Item duping / duplicación de estado** | Repetir un comando durable para que se aplique dos veces, o forzar una condición de carrera. | Cada comando lleva `requestId` (UUIDv4). El servidor lo registra en Redis (`idem:{playerId}:{requestId}`, TTL 300 s) y, para comandos durables, en la tabla `idempotency_keys`. La reserva se hace con `SETNX` **antes** de ejecutar, y un `requestId` repetido no re-ejecuta. Límite honesto: si Redis no responde, el comando se ejecuta igualmente —se prefiere dejar jugar antes que bloquear al jugador—, de modo que la deduplicación es *best effort* mientras Redis esté caído. Los efectos durables se aplican en una única transacción. |
| **Falsificación de combate** | Reportar un resultado favorable de un enfrentamiento. | El combate está **fuera de MVP**, pero la regla ya está fijada: los resultados de combate son estado autoritativo del servidor y ningún mensaje del protocolo permitirá al cliente proponerlos. La fase 4 del tick, `resolve simulation`, está reservada precisamente para eso. |
| **Movimiento de unidades ajenas** | Enviar `unit.move` con el `unitId` de otro jugador. | Validación de ownership antes que nada: `UNIT_NOT_OWNED`. El `playerId` procede del ticket JWT verificado, nunca del payload. |
| **Espionaje del mapa** | Suscribirse a chunks lejanos para ver movimientos enemigos. | El interest management es del servidor: `session.view` mueve el centro de vista con rate limit y el radio es `EO_INTEREST_RADIUS_CHUNKS=2`. El servidor solo emite deltas de las entidades del área de interés, y nunca retransmite el mundo completo. El interest management por chunk no tiene ADR propio: está descrito en [../architecture/networking.md](../architecture/networking.md) y en el canon §13. |
| **Evasión de la protección offline** | Manipular el cliente para atacar una ciudad `PROTECTED`. | El estado de presencia lo decide el **game loop en RAM**, comparando el tiempo sin sesiones vivas contra el período de gracia (`EO_PRESENCE_TTL_SECONDS=30`); un jugador con varias sesiones abiertas sigue `ONLINE` mientras le quede una. La clave `presence:player:{playerId}` de Redis publica esa presencia para observadores externos, pero la transición no depende de que expire. El cliente solo la observa. Las acciones contra una ciudad protegida se rechazan con `CITY_PROTECTED`. |
| **Replay de credenciales** | Reutilizar un game ticket capturado. | JWT HS256 con TTL de 60 s, `aud: "game-server"`, y `jti` consumido en Redis (`ticket:jti:{jti}`, TTL 120 s). Un ticket reusado se rechaza. |

Este modelo **no** previene por sí solo: automatización del cliente (bots que envían comandos legales a ritmo humano), coordinación externa entre jugadores, ni abuso de información legítimamente recibida. Son problemas distintos, se combaten con detección de comportamiento y con diseño de juego, y quedan **fuera de MVP**.

## Consecuencias

### Positivas

- **La integridad del mundo no depende del cliente.** Se puede publicar el código del cliente, permitir clientes alternativos e incluso documentar el protocolo sin abrir un vector de trampas de estado.
- **Una sola implementación del dominio**, en Go, dentro de `services/game-server`. No hay reglas de juego duplicadas en TypeScript que puedan divergir.
- **Los bugs de estado tienen un único lugar donde reproducirse.** Con `Clock` y `RandomSource` inyectados (`internal/clock`), un tick es reproducible en un test de simulación: `Loop.Step(nowMs)` se invoca directamente con un `FakeClock`, sin temporizadores reales ([../testing/simulation-tests.md](../testing/simulation-tests.md)).
- **La persistencia es coherente por construcción**: lo que se escribe en PostgreSQL es lo que el servidor calculó, y los invariantes de base de datos pueden ser estrictos porque nada externo escribe ([ADR-003](ADR-003-postgresql-source-of-truth.md)).
- **El protocolo es pequeño.** Cinco mensajes cliente→servidor en v1, todos de intención, lo que reduce la superficie de ataque y el coste de los contract tests.
- El servidor puede evolucionar reglas de juego (costes de terreno, cooldowns, límites) sin desplegar un cliente nuevo, siempre que el contrato de red no cambie.

### Negativas

- **Latencia percibida en cada acción.** Entre el clic y el movimiento visible hay al menos un round-trip más el tiempo hasta el siguiente tick (hasta 100 ms adicionales). En el peor caso realista se acumulan varios cientos de milisegundos. **No se mitiga con predicción autoritativa en el MVP**: se mitiga con interpolación visual entre las posiciones que envía el servidor y con retroalimentación de interfaz inmediata que no altera estado (marcador de destino, cursor de espera). El jugador con mala conexión notará el juego menos reactivo, y se acepta.
- **Todo el coste computacional recae en el servidor.** A\* hasta 20 000 nodos por consulta, interest management, emisión de deltas y validación de cada comando compiten por el mismo presupuesto de 100 ms. La capacidad por proceso está acotada por esto, y es la razón principal de las restricciones de [ADR-001](ADR-001-game-server-language.md).
- **Más ancho de banda.** El servidor debe enviar cada actualización de estado relevante, porque el cliente no puede deducir nada por sí mismo. Esto presiona directamente el presupuesto de mensajes de [ADR-006](ADR-006-websocket-protocol.md).
- **Más código de validación, y hay que escribirlo entero.** Cada mensaje nuevo del protocolo arrastra su batería de comprobaciones, sus códigos de error y sus tests. Es trabajo que en un modelo confiado simplemente no existe.
- **La interfaz debe saber esperar.** Cada acción tiene un estado intermedio "enviada, sin confirmar" que hay que representar, y un camino de error visible cuando llega `unit.move.rejected` o `system.error`. Es complejidad real en el cliente, aunque no sea lógica de juego.
- **Una desconexión corta el flujo de comandos.** El mundo sigue evolucionando (el movimiento activo continúa según su polilínea), pero el jugador no puede intervenir hasta reconectar y recibir un `world.snapshot` nuevo.

### Neutras

- El cliente se convierte en un renderizador y un capturador de input. Esa reducción de responsabilidad simplifica `apps/web` y encaja bien con la separación de PixiJS respecto de React ([ADR-005](ADR-005-pixijs-renderer.md)).
- Las herramientas de depuración del navegador siguen siendo útiles para inspeccionar el tráfico, pero no para inspeccionar el estado del juego: para eso están los logs estructurados y las métricas del servidor.
- El protocolo distingue explícitamente entre **Command** (`MoveUnit`, `CancelMovement`), **Event** (`UnitMovementStarted`, `UnitMovementCompleted`, `UnitSpawned`, `CityProtectionEngaged`) y **State** (`Unit.status = MOVING`). Esa separación es consecuencia directa de esta decisión.
- Un servidor de pruebas con validación relajada no es una opción: cualquier entorno donde el cliente pueda inyectar estado produciría hábitos y código que luego habría que deshacer.

## Estado

**Aceptado** el 2026-09-09.

Este ADR recoge un **principio no negociable** del canon técnico. No se prevé sustituirlo. Lo que sí puede evolucionar mediante ADR posteriores, sin tocar este:

- Añadir predicción y reconciliación en el cliente como *optimización de presentación*, si aparece jugabilidad sensible a reflejos. La autoridad seguiría siendo del servidor.
- Repartir la simulación entre varios procesos autoritativos por región. Eso es sharding del servidor, no autoridad del cliente, y no contradice esta decisión. **Fuera de MVP.**
