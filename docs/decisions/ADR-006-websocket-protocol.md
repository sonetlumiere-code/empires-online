# ADR-006: Protocolo WebSocket con JSON versionado

Propósito: fijar WSS con JSON UTF-8 y versión explícita en cada mensaje como protocolo de red del MVP, y definir el criterio medible que justificaría migrar a un formato binario sin tocar el dominio.

- **Estado:** Aceptado
- **Fecha:** 2026-09-09
- **Decisores:** equipo de arquitectura
- **Relacionados:** [ADR-001](ADR-001-game-server-language.md), [ADR-002](ADR-002-authoritative-server.md), [ADR-005](ADR-005-pixijs-renderer.md), [ADR-009](ADR-009-shared-protocol-package.md), [ADR-008](ADR-008-grid-coordinate-system.md)

---

## Contexto

El canal entre `apps/web` y `services/game-server` es la única vía por la que el jugador interactúa con el mundo. Sus características, derivadas del resto del diseño:

- **Bidireccional y de larga vida.** El cliente envía comandos esporádicos; el servidor emite deltas continuamente mientras la sesión esté abierta. WebSocket sobre TLS (WSS) es el transporte, y esa parte no se discute aquí: es el único mecanismo bidireccional de baja latencia disponible en un navegador sin recurrir a WebRTC.
- **Asimétrico en volumen.** El cliente solo puede enviar cinco tipos de mensaje en v1 —`session.hello`, `session.ping`, `session.view`, `unit.move`, `unit.cancel_move`— todos pequeños y de intención pura ([ADR-002](ADR-002-authoritative-server.md)). El servidor emite catorce tipos, incluido `world.snapshot`, que es con diferencia el mayor. **El presupuesto de bytes es un problema del sentido servidor→cliente**, no del contrario.
- **Frecuencia acotada.** Los deltas se emiten en la fase 7 del tick, a 10 Hz como máximo. No hay emisión continua fuera del tick.
- **Volumen acotado por el interest management.** Solo se envían entidades del área de interés: `EO_INTEREST_RADIUS_CHUNKS=2` alrededor del centro de vista, con chunks de 32 × 32 tiles ([ADR-008](ADR-008-grid-coordinate-system.md)). Nunca se retransmite el mundo completo.
- **Límites duros de transporte ya fijados**: mensaje ≤ 16 KiB (`EO_WS_MAX_MESSAGE_BYTES=16384`), rate limit de 20 msg/s por conexión con burst 40 (`EO_WS_RATE_LIMIT_PER_SECOND`, `EO_WS_RATE_LIMIT_BURST`), ping cada 15 s y timeout de lectura 45 s.
- **Dos lenguajes en los extremos**: TypeScript en el cliente, Go en el servidor ([ADR-001](ADR-001-game-server-language.md)). Cualquier formato elegido necesita representación fiel en ambos.
- **El protocolo cambiará mucho en los próximos meses.** El proyecto es greenfield y va por milestones (M3 Realtime, M4 Movement, M5 Offline protection, M6 Territories, M7 Diplomacy). Cada uno añadirá mensajes o campos. La **velocidad de iteración del protocolo** es un requisito de primer orden ahora, y dejará de serlo cuando el contrato se estabilice.
- **El cliente es depurable con las herramientas del navegador.** La pestaña de red muestra los frames de un WebSocket; si son texto, se leen. Ese detalle tiene un valor desproporcionado durante el desarrollo de un sistema donde el estado se construye a base de deltas encadenados.

## Decisión

**El protocolo v1 usa WSS con mensajes JSON codificados en UTF-8, con la versión declarada explícitamente en cada mensaje mediante el campo `"v": 1`.**

### Envelope

Cliente → servidor:

```json
{ "v": 1, "type": "unit.move", "requestId": "3f2b...uuid-v4", "payload": { "unitId": 42, "target": { "x": 118, "y": 74 } } }
```

Servidor → cliente:

```json
{ "v": 1, "type": "unit.move.accepted", "seq": 918, "ts": 1789459200123, "requestId": "3f2b...uuid-v4", "payload": { } }
```

| Campo | Sentido | Descripción |
|---|---|---|
| `v` | ambos | Versión del protocolo. Entero. Obligatorio en todo mensaje |
| `type` | ambos | Identificador del mensaje, con espacio de nombres por punto |
| `requestId` | C→S obligatorio en comandos | UUIDv4. Correlación e idempotencia |
| `requestId` | S→C opcional | Presente cuando el mensaje responde a un comando concreto |
| `seq` | S→C | uint64 monótono **por conexión**. Permite detectar huecos y ordenar |
| `ts` | S→C | Epoch ms del servidor. Base temporal para la interpolación del cliente |
| `payload` | ambos | Cuerpo específico del tipo. Es la única parte que conoce el dominio |

La separación entre **envelope** y **payload** es deliberada y es lo que hace posible la evolución descrita más abajo: el envelope es infraestructura (versión, correlación, orden, tiempo) y el payload es dominio. Ninguna capa de dominio en Go o en TypeScript debe leer `seq`, `ts` ni `v`; ninguna capa de transporte debe interpretar el contenido de `payload`.

### Mensajes v1

| Sentido | Tipos |
|---|---|
| Cliente → servidor | `session.hello`, `session.ping`, `session.view`, `unit.move`, `unit.cancel_move` |
| Servidor → cliente | `session.welcome`, `session.pong`, `system.error`, `world.snapshot`, `entity.spawn`, `entity.update`, `entity.despawn`, `city.update`, `territory.update`, `unit.move.accepted`, `unit.move.rejected`, `unit.movement.started`, `unit.movement.completed`, `unit.movement.cancelled` |

### Fuente de verdad de los esquemas

Los esquemas se definen con **Zod** en `packages/protocol/src/v1/`; el build exporta **JSON Schema** a `packages/protocol/schema/v1/*.json`; el game server los embebe con `go:embed` y los usa en *contract tests*. La validación en runtime se escribe a mano en Go por rendimiento. El detalle de este mecanismo es materia de [ADR-009](ADR-009-shared-protocol-package.md).

### Errores

Los errores se transportan en `system.error` con `{ code, message, requestId?, details? }`. **La lógica de control usa `code`, jamás el texto humano de `message`.** Los códigos son estables y forman parte del contrato tanto como los tipos de mensaje: `UNAUTHORIZED`, `FORBIDDEN`, `INVALID_MESSAGE`, `UNSUPPORTED_VERSION`, `RATE_LIMITED`, `MESSAGE_TOO_LARGE`, `UNIT_NOT_FOUND`, `UNIT_NOT_OWNED`, `UNIT_NOT_MOVABLE`, `UNIT_DEAD`, `UNIT_GARRISONED`, `INVALID_TARGET`, `TARGET_OUT_OF_BOUNDS`, `TARGET_NOT_WALKABLE`, `PATH_NOT_FOUND`, `PATH_TOO_LONG`, `CITY_NOT_FOUND`, `CITY_PROTECTED`, `TREATY_REQUIRED`, `POPULATION_LIMIT_REACHED`, `INTERNAL_ERROR`, `NOT_IMPLEMENTED`.

Los cierres de conexión usan códigos WS propios: `4400` mensaje inválido, `4401` no autenticado, `4403` no autorizado, `4408` timeout de handshake, `4429` rate limited, `4500` error interno.

### Versionado y política de compatibilidad

`v` identifica la **versión del contrato completo**, no la de un mensaje individual. Reglas:

1. **Todo mensaje declara `v`.** Un mensaje sin `v`, o con un `v` no soportado por el servidor, se rechaza con `UNSUPPORTED_VERSION`. No hay valor por defecto ni inferencia: la ausencia de versión es un error, no una versión implícita.
2. **Cambios aditivos dentro de la misma versión.** Se consideran compatibles y **no** incrementan `v`:
   - Añadir un tipo de mensaje nuevo.
   - Añadir un campo **opcional** a un payload existente.
   - Añadir un valor nuevo a un conjunto enumerado, **siempre que** el receptor ya esté obligado a tolerar valores desconocidos.
   - Añadir un código de error nuevo.
3. **Cambios rompedores incrementan `v`.** Renombrar o eliminar un campo, cambiar su tipo, volver obligatorio un campo opcional, cambiar la semántica de un campo existente o eliminar un tipo de mensaje.
4. **Obligación de tolerancia en ambos extremos.** El cliente ignora campos que no conoce y descarta silenciosamente tipos de mensaje que no reconoce (registrando una traza de diagnóstico, nunca cerrando la conexión). El servidor rechaza lo desconocido con `INVALID_MESSAGE`, porque en ese sentido la entrada no es de confianza y la permisividad es una vulnerabilidad, no una cortesía. **La asimetría es deliberada**: el cliente es un consumidor de datos ya validados; el servidor es la frontera de confianza ([ADR-002](ADR-002-authoritative-server.md)).
5. **Convivencia durante la transición.** Cuando exista `v: 2`, el servidor podrá aceptar `v: 1` y `v: 2` simultáneamente durante una ventana de despliegue, resolviendo por el valor del campo. Los esquemas conviven en directorios separados (`packages/protocol/src/v1/`, `.../v2/`) y ninguna versión se edita para parecerse a la otra. La duración de esa ventana es **TBD (fuera de MVP)**.
6. **El versionado de mensajes individuales no existe.** Se rechaza expresamente la idea de versionar cada tipo por separado (`unit.move@2`): multiplica los estados posibles del contrato y hace intratables los contract tests.

## Alternativas consideradas

### A. Protocol Buffers

**Ventajas reales, y son muchas.** Es la opción técnicamente más completa. Serialización binaria compacta —típicamente una fracción del tamaño del JSON equivalente, sobre todo con muchos campos numéricos, que es exactamente el perfil de un delta de posiciones—, esquema tipado como fuente única de verdad con generación de código para Go y TypeScript, evolución de esquema resuelta de fábrica mediante números de campo (los campos desconocidos se ignoran o se preservan sin ambigüedad) y serialización rápida con poca asignación de memoria. Resuelve de un plumazo el problema de dos lenguajes que motiva [ADR-009](ADR-009-shared-protocol-package.md).

**Por qué se descarta para el MVP.**

- **Los frames dejan de ser legibles.** En la pestaña de red del navegador se ven bytes. Depurar una cadena de deltas —`entity.spawn` seguido de varios `entity.update` y un `unit.movement.completed`— pasa de leer texto a necesitar utillaje de decodificación propio. Durante los milestones M3 y M4, en los que el protocolo se está descubriendo, esa pérdida es cara y se paga a diario.
- **Introduce un paso de generación de código** en la cadena de build de ambos extremos: `protoc` o equivalente, plugins por lenguaje, artefactos generados versionados o generados en CI. En un entorno de desarrollo Windows donde ni siquiera `make` está disponible, cada herramienta externa adicional tiene un coste real de arranque.
- **Frena la iteración.** Cambiar un payload deja de ser editar un esquema Zod y pasa a ser editar el `.proto`, regenerar, ajustar ambos lados y confirmar los artefactos.
- **El beneficio es de ancho de banda, y hoy no hay evidencia de que el ancho de banda sea un problema.** Optimizar sin medición es adivinar.

Protobuf es el candidato natural el día que se cumplan los criterios de migración de la sección siguiente.

### B. MessagePack

**Ventajas reales.** Es "JSON binario": mismo modelo de datos, misma flexibilidad sin esquema, pero codificado de forma compacta. La migración desde JSON es casi mecánica —se sustituye el codec y el resto del sistema no se entera—, hay bibliotecas maduras en Go y en TypeScript, y no requiere generación de código ni un paso nuevo de build. Ofrece una reducción de tamaño apreciable a coste de integración casi nulo. Es, con diferencia, **la migración más barata desde el punto donde estamos**.

**Por qué se descarta ahora.** Al no tener esquema, no aporta nada en tipado ni en evolución del contrato: seguirían haciendo falta los esquemas Zod y los contract tests exactamente igual. Y pierde la única ventaja decisiva del JSON en esta fase: la legibilidad directa en las herramientas del navegador. Se paga el coste (frames opacos) y solo se cobra una parte del beneficio (menos bytes, pero sin tipado). Queda anotada como **la opción por defecto si el disparador de migración es el ancho de banda y no el coste de CPU de serialización**, porque su relación beneficio/coste de integración es la mejor del conjunto.

### C. FlatBuffers

**Ventajas reales.** Acceso a los datos **sin deserializar**: se lee directamente del buffer recibido, sin asignaciones intermedias. Para un servidor que emite deltas a miles de conexiones dentro de un presupuesto de tick de 100 ms, eliminar la asignación por mensaje es una ventaja real y medible, y en el cliente permite leer un `world.snapshot` grande sin construir un árbol de objetos.

**Por qué se descarta.** Es la opción más compleja de las cuatro: esquema propio, generación de código, y una API de construcción de mensajes considerablemente más incómoda que la de Protobuf. Su ventaja específica —cero copia— importa cuando el cuello de botella es la deserialización de mensajes grandes y frecuentes, y el perfil de Empires Online es el contrario: mensajes pequeños a 10 Hz. Además, el soporte en TypeScript es notablemente menos cómodo que en Go o C++. Complejidad alta para resolver un problema que el proyecto no tiene.

### D. JSON sin versión

**Ventajas reales.** Es lo más simple posible: menos bytes por mensaje, menos ceremonia, un campo menos que rellenar y validar. Es lo que se hace por defecto cuando se empieza, y funciona perfectamente mientras cliente y servidor se despliegan juntos.

**Por qué se descarta.** En una aplicación web, cliente y servidor **no** se despliegan juntos: hay pestañas abiertas desde hace horas, clientes cacheados, y en un mundo persistente 24/7 no existe la ventana de mantenimiento en la que "todos se actualizan a la vez". Sin `v`, un cambio rompedor produce fallos silenciosos y difíciles de diagnosticar: mensajes que se parsean pero significan otra cosa, campos ausentes interpretados como valores por defecto, estado corrupto en el cliente. Con `v`, el mismo escenario produce un rechazo explícito (`UNSUPPORTED_VERSION`) que el cliente puede convertir en "recarga la página". El coste es un campo entero por mensaje; el beneficio es la diferencia entre un error diagnosticable y un error invisible. La relación no admite discusión.

### E. Comparativa

| Criterio | JSON v1 | Protobuf | MessagePack | FlatBuffers | JSON sin `v` |
|---|---|---|---|---|---|
| Tamaño en red | Alto | Bajo | Medio-bajo | Bajo | Alto |
| Coste de CPU al serializar | Medio | Bajo | Bajo | Muy bajo | Medio |
| Legible en el navegador | **Sí** | No | No | No | Sí |
| Requiere generación de código | No | Sí | No | Sí | No |
| Esquema y tipado | Vía Zod | Nativo | No | Nativo | Vía Zod |
| Velocidad de iteración | **Alta** | Media | Alta | Baja | Alta |
| Evolución segura del contrato | Sí (`v` + reglas) | Sí (nativa) | Manual | Sí (nativa) | **No** |
| Coste de migrar desde aquí | — | Alto | **Bajo** | Alto | — |

## Criterio medible de migración a un formato binario

La decisión de seguir con JSON es **provisional por diseño**, y para que eso signifique algo hace falta saber cuándo deja de valer. Se define un disparador explícito.

Los umbrales siguientes son **criterios operativos definidos en este ADR**, no valores de gameplay: no viven en `internal/config` ni son configurables en tiempo de ejecución. Se derivan de las constantes ya fijadas en el canon.

| Id | Disparador | Umbral | Cómo se mide |
|---|---|---|---|
| **T1** | Tamaño de mensaje | El p95 del tamaño serializado de un mensaje servidor→cliente supera el **50 % de `EO_WS_MAX_MESSAGE_BYTES`** (8 192 bytes) de forma sostenida durante 24 h en condiciones normales de juego | Histograma de bytes emitidos por mensaje, junto a `eo_ws_messages_total` |
| **T2** | Coste de CPU | La serialización de deltas consume más del **10 % del presupuesto de tick** (más de 10 ms de los 100 ms) en el p95 | Perfilado con `pprof` correlacionado con `eo_game_tick_duration_seconds` |
| **T3** | Fragmentación | Un lote de deltas de un solo tick para una sola conexión requiere partirse por exceder `EO_WS_MAX_MESSAGE_BYTES` en operación normal, y no solo en el `world.snapshot` inicial | Contador de fragmentaciones, distinguido por tipo de mensaje |

**Con que se cumpla uno solo de forma sostenida se abre la evaluación**; el ADR de migración deberá aportar la medición previa y la medición posterior sobre el mismo escenario. Un ahorro de bytes que no se ha medido no es una justificación.

Antes de cambiar de formato hay que agotar, en este orden, las optimizaciones que no tocan el contrato:

1. Recortar lo que se envía: los deltas transportan solo campos que han cambiado.
2. Revisar el interest management, que es el mecanismo que de verdad acota el volumen ([ADR-008](ADR-008-grid-coordinate-system.md)).
3. Agrupar deltas del mismo tick en menos mensajes.
4. Activar la compresión de extensión de WebSocket (`permessage-deflate`), que sobre JSON —muy redundante— da una reducción sustancial a coste de CPU. Evaluarla **antes** de cambiar de formato es obligatorio: es la opción con mejor relación beneficio/coste y no toca el contrato en absoluto.

### Por qué el envelope permite la migración sin romper el dominio

El punto clave es que **el formato de serialización es una propiedad del transporte, no del dominio**:

```
┌──────────────┐   comandos/eventos de   ┌──────────────┐
│   Dominio    │  ─────  dominio  ────▶  │   Dominio    │
│   (Go)       │                         │   (TS)       │
└──────┬───────┘                         └──────▲───────┘
       │  structs / tipos de payload             │
┌──────▼───────────────────────────────────────┬─┴─────────┐
│  Capa de protocolo: envelope { v, type, ... } │           │
│  ─ conoce versión, correlación, orden, tiempo │           │
└──────┬────────────────────────────────────────┴───────────┘
       │  ÚNICO punto de contacto con el formato
┌──────▼─────────────────────────────────────────────────────┐
│  Codec:  JSON  │  MessagePack  │  Protobuf  │  FlatBuffers │
└────────────────────────────────────────────────────────────┘
```

Tres propiedades del diseño actual hacen barata la sustitución:

1. **El envelope es fijo y de tamaño mínimo.** Sus campos (`v`, `type`, `seq`, `ts`, `requestId`, `payload`) tienen equivalente directo en cualquier formato binario. No hay nada específico de JSON en su semántica.
2. **`type` es un discriminador explícito**, no una consecuencia de la forma del objeto. Un codec binario puede mapearlo a un entero sin que el dominio se entere.
3. **El dominio nunca ve el formato.** Los tipos de payload se generan o validan a partir de una única fuente de verdad ([ADR-009](ADR-009-shared-protocol-package.md)); cambiar de codec cambia cómo se producen esos tipos, no qué significan.

En la práctica, la migración consistiría en sustituir la implementación del codec en ambos extremos y negociar el formato en el handshake WebSocket mediante el subprotocolo (`Sec-WebSocket-Protocol`), lo que permitiría convivencia durante el despliegue. El mecanismo exacto de negociación es **TBD (fuera de MVP)**.

## Consecuencias

### Positivas

- **Depurabilidad inmediata.** Los frames se leen en la pestaña de red del navegador sin herramientas. Durante M3 y M4, cuando el protocolo todavía se está descubriendo, esto acelera cada ciclo de diagnóstico.
- **Iteración rápida.** Añadir un campo opcional o un tipo de mensaje es editar el esquema Zod y regenerar; no hay compiladores de esquema ni artefactos generados que coordinar.
- **Cero dependencias externas de build**, algo especialmente valioso en el entorno real de desarrollo (Windows, sin `make`, sin herramientas de línea de comandos adicionales instaladas).
- **Los tests son legibles.** Los ficheros de `testdata/` con mensajes de ejemplo se leen y se editan a mano, y los contract tests comparan estructuras comprensibles.
- **`v` explícito convierte una incompatibilidad en un error diagnosticable.** `UNSUPPORTED_VERSION` es accionable; un parseo silenciosamente incorrecto no lo es.
- **`seq` monótono por conexión** permite al cliente detectar huecos y decidir si necesita un `world.snapshot` nuevo, sin acuerdos implícitos sobre el orden de entrega.
- **El coste de la decisión es reversible**, y el criterio para revertirla está escrito y es medible.

### Negativas

- **JSON es voluminoso.** Los nombres de campo viajan en cada mensaje, los números van en decimal ASCII y el sobrecoste de sintaxis es constante. En un delta de posiciones, la mayor parte de los bytes no son información. Es el precio directo de la legibilidad.
- **Serializar y parsear cuesta CPU en el servidor**, y ese coste sale del presupuesto de 100 ms por tick. Con muchas conexiones, la emisión de deltas puede convertirse en una fracción no despreciable del tick, y es exactamente lo que vigila el disparador T2.
- **JSON no distingue enteros de flotantes** y en JavaScript todo número es un `double` de 64 bits. Los `uint64` (`seq`) y los `bigint` de epoch ms superan el entero exactamente representable de JavaScript (2^53 − 1). Los valores de epoch ms actuales quedan holgadamente por debajo de ese límite, pero **`seq` es un contador sin cota superior conocida** y cualquier identificador de 64 bits que se añada al protocolo en el futuro deberá serializarse como cadena. Es una trampa concreta que hay que recordar en cada extensión del contrato.
- **La validación se escribe dos veces**: los esquemas Zod en TypeScript y la validación manual en Go, que existe por rendimiento ([ADR-001](ADR-001-game-server-language.md)). Los contract tests contra el JSON Schema exportado acotan la deriva, pero es trabajo recurrente en cada cambio.
- **Las reglas de compatibilidad dependen de la disciplina del equipo.** A diferencia de Protobuf, donde los números de campo hacen la evolución segura por construcción, aquí nada impide técnicamente que alguien renombre un campo sin subir `v`. La red de seguridad son los contract tests y la revisión, no el compilador.
- **Existe una migración probable en el horizonte.** Se asume deliberadamente que el trabajo de cambiar de formato se hará más adelante, con el sistema ya en producción y por tanto en peores condiciones que si se hubiera hecho al principio.

### Neutras

- Un campo entero (`v`) por mensaje se suma al sobrecoste. Frente a los nombres de campo del propio JSON, es despreciable.
- `packages/protocol` publica el paquete npm `@empires-online/protocol` y exporta JSON Schema; los contract tests consumen esos artefactos en ambos extremos.
- El cliente necesita una máquina de estados de conexión —handshake con ticket antes de 5 s (cierre `4408` si no), ping cada 15 s, timeout de lectura de 45 s, reconexión con `world.snapshot` nuevo— que es independiente del formato de serialización y no cambiaría con una migración.
- Añadir un tipo de mensaje nuevo obliga a tocar cuatro sitios: el esquema Zod, la validación en Go, el manejador y los contract tests. Es propiedad del diseño de dos lenguajes, no del formato JSON.

## Estado

**Aceptado** el 2026-09-09.

Este ADR se sustituiría por uno nuevo cuando se cumpla al menos uno de los disparadores T1, T2 o T3 de forma sostenida y se hayan agotado las optimizaciones que no tocan el contrato. El ADR sustituto deberá incluir la medición previa, el formato elegido, el mecanismo de negociación y el plan de convivencia con `v: 1`.
