# ADR-009: Paquete de protocolo compartido con Zod como fuente de verdad y JSON Schema exportado

Propósito: establecer que los esquemas Zod de `packages/protocol` son la única fuente de verdad del protocolo WebSocket v1, que el build exporta JSON Schema, que el game server lo embebe con `go:embed` para *contract tests*, y que la validación en runtime del servidor se escribe a mano en Go por rendimiento.

| Campo | Valor |
|---|---|
| **Estado** | **Aceptado** |
| **Fecha** | **2026-09-09** |
| Ámbito | `packages/protocol`, `services/game-server/internal/protocol`, `services/game-server/internal/websocket`, `apps/web` |
| Índice | [README.md](README.md) |
| Relacionados | [ADR-006](ADR-006-websocket-protocol.md), [ADR-010](ADR-010-authentication-game-ticket.md), [ADR-011](ADR-011-movement-timed-polyline.md) |

---

## 1. Contexto

El protocolo v1 es el contrato entre dos bases de código escritas en lenguajes distintos por, potencialmente,
personas distintas: `services/game-server` (Go; la versión mínima la declara su `go.mod`) y
`apps/web` (TypeScript, Next.js 15 + React 19 + PixiJS 8), cuyo cliente **todavía está en construcción** y
llegará completo en su milestone. El paquete de protocolo, en cambio, ya está escrito y sus 18 tests de
Vitest pasan.
Transporta WSS con JSON UTF-8 y versión explícita `"v": 1` en cada mensaje. Su superficie en el MVP es
pequeña y cerrada:

| Dirección | Mensajes |
|---|---|
| Cliente → servidor (5) | `session.hello`, `session.ping`, `session.view`, `unit.move`, `unit.cancel_move` |
| Servidor → cliente (14) | `session.welcome`, `session.pong`, `system.error`, `world.snapshot`, `entity.spawn`, `entity.update`, `entity.despawn`, `city.update`, `territory.update`, `unit.move.accepted`, `unit.move.rejected`, `unit.movement.started`, `unit.movement.completed`, `unit.movement.cancelled` |

Envelopes:

```jsonc
// cliente → servidor
{ "v": 1, "type": "unit.move", "requestId": "<UUIDv4>", "payload": { /* ... */ } }

// servidor → cliente
{ "v": 1, "type": "unit.movement.started", "seq": 4711, "ts": 1757404800123,
  "requestId": "<UUIDv4>", "payload": { /* ... */ } }
```

El riesgo que este ADR ataca es concreto y tiene nombre: **deriva silenciosa**. Un campo renombrado en un
lado, un entero que pasa a string, un campo que deja de ser opcional. En un protocolo binario con
generación de código eso lo detecta el compilador; en JSON sin contrato compartido lo detecta un jugador en
producción.

Restricciones del canon que este ADR desarrolla, no discute: JSON UTF-8 sobre WSS; Zod en
`packages/protocol/src/v1/`; exportación a `packages/protocol/schema/v1/*.json`; `go:embed` en el game
server para *contract tests*; validación en runtime escrita a mano en Go.

---

## 2. Decisión

**Los esquemas Zod de `packages/protocol/src/v1/` son la fuente única de verdad del protocolo. El build del
paquete exporta JSON Schema. El game server consume ese JSON Schema embebido para contract tests, y valida
en runtime con código Go escrito a mano.**

### 2.1 Cadena de derivación

```
packages/protocol/src/v1/*.ts          ← FUENTE DE VERDAD (Zod ^3.24)
        │
        │  pnpm run protocol:build
        │    = tsc -p tsconfig.build.json  &&  tsx src/scripts/export-schema.ts
        │
        ├──────────────► dist/                       tipos TS + validadores runtime  →  apps/web
        │
        └──── zod-to-json-schema ────► DOS destinos escritos por el MISMO script:
                 │
                 ├──► packages/protocol/schema/v1/*.json      artefacto publicado del paquete
                 │
                 └──► services/game-server/internal/protocol/schema/v1/*.json
                              │
                              │  //go:embed schema/v1/*.json   (internal/protocol/schema.go)
                              ▼
                    contract tests (Go)  ── validan mensajes reales del servidor
```

Los tres artefactos generados son `client-message.schema.json`, `server-message.schema.json` y
`error-codes.json` (`{version, codes}`), y se escriben con `JSON.stringify(..., 2)` y salto de línea final
para que cualquier cambio real produzca un diff legible y ninguno espurio por formato.

Puntos que conviene entender antes de tocar nada:

- **El JSON Schema se versiona en git.** No es un artefacto efímero de build: es lo que permite que un
  checkout limpio del repo pueda ejecutar los tests de Go sin instalar Node.
- **El espejo dentro del módulo Go no es accidental.** `go:embed` **no puede referenciar ficheros fuera del
  módulo Go**: los patrones no admiten `..`. Como `packages/protocol` está fuera del módulo, la única forma
  de embeber los esquemas es escribirlos también dentro de `services/game-server`, y ahí viven en
  `internal/protocol/schema/v1/`, junto al paquete que los embebe.
- **No hay un paso de copia separado.** `src/scripts/export-schema.ts` escribe los dos destinos en la misma
  ejecución, así que no existe ninguna ventana en la que uno esté actualizado y el otro no por haberse
  olvidado un `cp`.
- **Ambos destinos están cubiertos por el mismo gate de CI**, descrito en §2.3.

### 2.2 Qué valida cada lado

| Capa | Implementación | Cuándo corre | Qué garantiza |
|---|---|---|---|
| Cliente | Validadores Zod de `dist/` | En cada mensaje entrante y saliente del navegador | El cliente no envía basura ni asume campos que el servidor no manda. |
| Servidor, runtime | Código Go escrito a mano en `internal/websocket` | En cada frame recibido | Rechazo rápido y con el código de error canónico correcto. |
| Servidor, tests | JSON Schema embebido (`protocol.SchemaFS`) + catálogo de errores exportado | En CI, nivel *contract* | Que lo que el servidor produce y acepta coincide con la fuente de verdad. |

Los dos sentidos **no son igual de estrictos, y es deliberado**: los esquemas cliente→servidor llevan
`additionalProperties: false`, así que un campo extra se rechaza y un cliente no puede colar nada que el
servidor no espere; los esquemas servidor→cliente **no** son estrictos, para poder añadir campos opcionales
sin romper clientes antiguos. Es la asimetría normal de un protocolo con un servidor autoritativo.

La validación en runtime del servidor comprueba, en este orden:

| Orden | Comprobación | Si falla |
|---|---|---|
| 0 | **Handshake**: el primer mensaje de la conexión es un `session.hello` con un ticket verificable, dentro de `WSHandshakeTimeout` (5 s) | Se cierra sin sesión: `4408` por timeout, `4401` por ticket inválido, `4400` por envelope o versión inválidos. |
| 1 | Tamaño del frame ≤ `EO_WS_MAX_MESSAGE_BYTES` (16384) | Lo impone el transporte con `SetReadLimit`: la lectura aborta y la conexión termina; **no se emite un `system.error` de aplicación**. |
| 2 | Presupuesto de rate limit (`EO_WS_RATE_LIMIT_PER_SECOND=20`, `EO_WS_RATE_LIMIT_BURST=40`) | `system.error` con `RATE_LIMITED` y cierre `4429`. |
| 3 | JSON parseable y envelope bien formado | `system.error` con `INVALID_MESSAGE`; la conexión **sigue abierta**. |
| 4 | `v == 1` | `system.error` con `UNSUPPORTED_VERSION`; la conexión sigue abierta. |
| 5 | `type` en el conjunto conocido cliente→servidor | `system.error` con `INVALID_MESSAGE`; la conexión sigue abierta. |
| 6 | `payload` deserializable en la struct del tipo | `system.error` con `INVALID_MESSAGE`; la conexión sigue abierta. |
| 7 | `requestId` presente en los comandos (`unit.move`, `unit.cancel_move`) y no reclamado ya | `INVALID_MESSAGE` si falta; si el `requestId` ya se procesó, el comando se descarta por idempotencia. |

Dos detalles que la documentación no debe suavizar. El primero: **la autenticación no es una comprobación
por mensaje**, sino la puerta de entrada — un socket que no supera el handshake nunca llega al bucle de
lectura. El segundo: `MESSAGE_TOO_LARGE` existe en el catálogo de códigos, pero el servidor **no lo emite
hoy** al superarse el límite de tamaño, porque el límite lo aplica el propio transporte antes de que haya
un mensaje que responder. El mapeo completo de error a código de cierre es autoridad de
[../specs/websocket-protocol.md](../specs/websocket-protocol.md) §6, y este ADR no lo redefine: los códigos
de cierre existentes son `4400`, `4401`, `4403`, `4408`, `4429` y `4500`.

**Por qué a mano y no con un validador genérico de JSON Schema en runtime.** Un validador genérico
interpreta el esquema en cada mensaje, hace reflexión y asigna memoria por campo. El camino caliente aquí
es cada frame de cada conexión, con un techo de 20 mensajes por segundo y por conexión. Y el conjunto a
validar son **cinco** tipos de mensaje con payloads de tres o cuatro campos. Con eso, la validación del
servidor se reduce a las comprobaciones de envelope de la tabla anterior más una deserialización tipada por
tipo de mensaje (`json.Unmarshal` sobre la struct correspondiente): cuesta menos que ajustar un validador
genérico y su coste por mensaje es una fracción del de deserializar el JSON. El precio —que es real y se
enuncia en §4.2— es que ese código puede divergir del esquema, y por eso los contract tests no son
opcionales.

### 2.3 Gate de CI contra la deriva

El pipeline (`format → lint → typecheck → unit → integration → build → docker build`) incluye un paso que
convierte la deriva en un fallo de build en lugar de un bug de producción:

```bash
# Compara los artefactos en disco contra lo que Zod produce AHORA.
# Si alguno falta o está desactualizado, imprime cuáles y sale con código 1.
pnpm run protocol:check      # = tsx src/scripts/export-schema.ts --check
```

El modo `--check` no escribe nada: recorre los mismos dos destinos que la generación y acumula la lista de
artefactos con deriva. Quien edite un esquema Zod y no ejecute `pnpm run protocol:build` verá la CI en rojo
con el nombre exacto del fichero que falta regenerar, y quien edite a mano un artefacto derivado verá lo
mismo. `pnpm run verify` encadena `protocol:build` con el typecheck, los tests de TypeScript, `go vet` y
`go test`.

Y, en el nivel *contract*, los tests de Go comprueban contra los artefactos embebidos que **los 14 tipos
servidor→cliente y los 5 cliente→servidor declarados en Go aparecen en el esquema exportado**, que el
catálogo de códigos de error coincide exactamente con `error-codes.json`, y que los envelopes salientes y
los deltas parciales se serializan con los campos del contrato. El servidor **no incrusta un validador
genérico de JSON Schema**: compara contra los artefactos, no los interpreta. Detalle en
[../testing/contract-tests.md](../testing/contract-tests.md).

---

## 3. Alternativas consideradas

### 3.1 Protobuf o gRPC como fuente única

**A favor (real).** Es la respuesta ortodoxa y tiene ventajas que no son discutibles: un único `.proto`
genera tipos e implementación de serialización para Go y para TypeScript sin escribir un validador dos
veces; los campos numerados hacen la evolución del esquema segura por construcción (añadir un campo nunca
rompe un cliente viejo); la codificación binaria es varias veces más compacta que JSON, lo que importa en
mensajes como `world.snapshot` o una polilínea larga
([ADR-011](ADR-011-movement-timed-polyline.md)); y elimina de raíz la clase de bug que este ADR intenta
detectar con tests, porque no hay dos implementaciones que puedan divergir.

**En contra.** La depurabilidad en navegador se desploma: hoy, la pestaña de red de las DevTools muestra
cada frame WebSocket como texto legible y cualquiera puede leer un `unit.move` sin herramientas. Con
Protobuf hay que decodificar para mirar, y en un juego donde la mayor parte de la depuración durante el
desarrollo es «qué le mandé y qué me contestó», eso se paga todos los días. Añade además cadena de
herramientas (`protoc` o `buf`, plugins por lenguaje, un paso de generación en CI) sobre un entorno de
desarrollo Windows donde ni `make` ni `psql` están instalados y donde cada dependencia nueva es fricción
real. Y el canon fija JSON UTF-8 como transporte. **No descartado para el futuro**: si el volumen de
`world.snapshot` se convierte en un problema medido, un transporte binario es la vía, y merecerá su ADR.

### 3.2 OpenAPI como fuente única

**A favor (real).** Ecosistema enorme, generadores para prácticamente todos los lenguajes, documentación
navegable gratis, y el equipo probablemente ya lo conoce.

**En contra.** OpenAPI describe **HTTP request/response**: recursos, verbos, códigos de estado, parámetros
de ruta. Nuestro protocolo es mensajería **bidireccional y asíncrona** donde el servidor inicia la mayor
parte del tráfico (`entity.spawn`, `city.update`, `unit.movement.completed`) sin ninguna petición previa, y
donde existe un `seq` monótono por conexión que OpenAPI no tiene dónde colocar. Se puede forzar el encaje
declarando cada payload como un `schema` suelto, pero entonces se usa OpenAPI como contenedor de esquemas y
se pierde todo lo que lo hace valioso. AsyncAPI encajaría mejor conceptualmente, pero su tooling es
inmaduro comparado con Zod para TypeScript. Rechazada.

### 3.3 Definir el esquema en Go y generar TypeScript

**A favor (real).** El servidor es el dueño de la verdad del juego, así que hay una simetría atractiva en
que también sea el dueño del contrato. Evitaría el espejo de §2.1, porque los esquemas nacerían
dentro del módulo Go. Y garantizaría que la validación en runtime del servidor y el esquema son literalmente
lo mismo, eliminando la deriva interna del lado Go.

**En contra.** Invierte la ergonomía en contra del lado que más la necesita. Las structs de Go con tags son
un lenguaje de esquema pobre: no expresan `min`/`max`, patrones, uniones discriminadas ni refinamientos sin
librerías adicionales, y precisamente el envelope del protocolo **es** una unión discriminada por `type`.
Zod expresa eso de forma nativa y, además, produce **tipos TypeScript inferidos**, de modo que el
autocompletado y el estrechamiento por `type` funcionan en el editor del frontend sin generación de código.
El equipo de cliente perdería eso a cambio de ahorrarle al servidor un directorio espejo que un script
mantiene solo. Rechazada.

### 3.4 Duplicar los tipos a mano en ambos lados

**A favor (real).** Cero herramientas, cero pasos de build, cero artefactos generados en el repositorio.
Es lo más rápido durante la primera semana.

**En contra.** La deriva silenciosa es inevitable, no probable. No hace falta mala fe: basta un PR que
añade un campo opcional al payload de `unit.movement.started` en Go y no toca el tipo de TypeScript.
Nada falla, nadie se entera, y seis semanas después alguien depura durante horas por qué el cliente ignora
un campo que el servidor lleva mandando desde entonces. Rechazada sin más discusión.

---

## 4. Consecuencias

### 4.1 Positivas

- **Un contrato que no se puede romper en silencio.** Romperlo requiere que fallen a la vez el gate de
  regeneración de CI y el corpus de contract tests. Sigue siendo posible; deja de ser invisible.
- **Tipado del cliente sin generación de código.** `z.infer` da los tipos de TypeScript directamente desde
  los esquemas; `apps/web` importa `@empires-online/protocol` como dependencia de workspace y obtiene
  autocompletado y estrechamiento por `type` gratis.
- **Los tests de Go no necesitan Node.** El JSON Schema está versionado y embebido en el binario, así que
  `go test ./...` funciona en un checkout limpio sin ejecutar el build de TypeScript. Esto importa
  especialmente en el entorno de desarrollo descrito en
  [../operations/local-development.md](../operations/local-development.md).
- **Un único lugar donde mirar.** Ante cualquier duda sobre la forma de un mensaje, la respuesta está en
  `packages/protocol/src/v1/`. No hay «depende de a quién preguntes».
- **JSON legible en producción y en desarrollo.** Un frame se puede leer en DevTools, pegarse en un ticket
  y reproducirse a mano. En un sistema autoritativo donde la mayoría de los bugs son de contrato, eso vale
  mucho más de lo que cuesta en bytes.

### 4.2 Negativas (enunciadas sin adornos)

- **Hay dos implementaciones de las mismas reglas.** Zod valida en el navegador; código Go escrito a mano
  valida en el servidor. Es exactamente la duplicación que el ADR dice combatir, con la diferencia de que
  aquí es **deliberada, acotada a cinco tipos de mensaje y vigilada por CI**. No es que no pueda divergir:
  es que si diverge, el corpus de contract tests debería atraparlo.
- **La garantía es tan buena como el corpus de tests.** Si el validador Go acepta un `unit.move` con
  `target.x` negativo y el corpus no incluye ese caso, la divergencia pasa. La calidad del corpus es la
  garantía real, no el mecanismo. Cada código de error alcanzable debe tener su caso, según la Definition of
  Done del canon.
- **Un paso de build extra y los mismos artefactos derivados por duplicado en el repositorio.**
  `packages/protocol/schema/v1/*.json` y su espejo en
  `services/game-server/internal/protocol/schema/v1/*.json` están versionados, aparecen en los diffs y hay
  que regenerarlos. Quien edite Zod y no regenere verá el build rojo, que es el comportamiento deseado, pero
  también es fricción.
- **Un directorio espejo como parte del contrato.** El segundo destino existe solo por una limitación de
  `go:embed`, que no puede salir del módulo Go. Es feo y hay que explicarlo a cada persona nueva; el
  atenuante es que lo escribe el mismo script que el original, no una copia manual. La alternativa (mover el
  protocolo dentro del módulo Go) es peor por §3.3.
- **JSON es voluminoso.** Los envelopes repiten claves en cada mensaje y los enteros viajan como texto
  decimal. En `world.snapshot` y en las polilíneas largas se nota, y no hay compresión definida en el canon.
  El techo es `EO_WS_MAX_MESSAGE_BYTES=16384` por mensaje, y hay mensajes que se acercan
  ([ADR-011](ADR-011-movement-timed-polyline.md), §4.2).
- **La versión del protocolo es un entero y no hay negociación.** `v: 1` o `UNSUPPORTED_VERSION`. Cuando
  llegue `v: 2` habrá que decidir si el servidor sirve ambas, y esa decisión no está tomada:
  **TBD (fuera de MVP)**.

---

## 5. Verificación

| Qué se verifica | Nivel | Cómo | Estado |
|---|---|---|---|
| Los derivados corresponden a la fuente | CI | `pnpm run protocol:check`: falla y nombra el fichero con deriva. | En verde |
| Los esquemas Zod aceptan y rechazan lo que deben | unit (Vitest) | 18 tests en `packages/protocol`: envelopes, versión no soportada, coordenadas no enteras, rechazo de campos extra en cliente→servidor, catálogo de errores, JSON Schema válido y sin deriva. | En verde |
| El catálogo de errores de Go coincide con el de TypeScript | contract (Go) | `ExportedErrorCodes()` lee `error-codes.json` embebido y se compara con `AllErrorCodes`; una divergencia rompe la CI. | En verde |
| Los esquemas embebidos son legibles y de la versión correcta | contract (Go) | `SchemaFile` sobre los tres artefactos; `error-codes.json` debe declarar `version: 1`. | En verde |
| Los casos límite se rechazan con el código correcto | contract | `v: 2` → `UNSUPPORTED_VERSION`; `type` desconocido y `requestId` ausente → `INVALID_MESSAGE`; exceso de mensajes → `RATE_LIMITED`. Un frame por encima de `EO_WS_MAX_MESSAGE_BYTES` lo corta el transporte, sin mensaje de aplicación. | Cubierto en Vitest y en los contract tests de Go; el recorrido completo sobre un socket real es parte de la suite de integración pendiente de Docker |
| El cliente y el servidor coinciden extremo a extremo | E2E | Cliente WS contra el servidor real. | Pendiente: requiere el cliente web terminado y la infraestructura de integración |

---

## 6. Referencias

- [../specs/websocket-protocol.md](../specs/websocket-protocol.md) — autoridad del protocolo v1 completo.
- [../architecture/networking.md](../architecture/networking.md) — capa WebSocket, sesiones, `seq`, rate limiting.
- [../testing/contract-tests.md](../testing/contract-tests.md) — corpus y ejecución del nivel *contract*.
- [ADR-010](ADR-010-authentication-game-ticket.md) — `session.hello` como primer mensaje del protocolo.
