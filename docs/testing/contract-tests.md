# Pruebas de contrato

Verificación del protocolo WebSocket v1 contra el JSON Schema exportado desde los esquemas Zod: qué se valida, en qué lenguaje, cómo se detecta la deriva de schema como fallo de CI y por qué eso es lo que impide que cliente y servidor dejen de hablar el mismo idioma.

> **Estado real.** Este nivel **existe y está en verde por los dos lados**: `internal/protocol/contract_test.go`
> (Go) y `packages/protocol/src/v1/protocol.test.ts` (18 tests, Vitest). El check de deriva de schema
> está activo en la CI. Los casos ✔ llevan el nombre de test real; los ○ están previstos.
> El protocolo completo está en [../specs/websocket-protocol.md](../specs/websocket-protocol.md);
> este documento no lo repite, lo verifica.

---

## 1. Qué garantiza este nivel

El nivel `contract` garantiza una sola propiedad, y es una propiedad fuerte: **todo byte que cruza el WebSocket, en cualquier dirección, tiene una forma declarada, y esa declaración es la misma para el cliente TypeScript y para el servidor Go**.

Lo que **no** garantiza: que el contenido sea verdad. Un `unit.movement.started` con una polilínea absurda valida perfectamente contra el schema. La semántica la verifican [unit-tests.md](./unit-tests.md) y [simulation-tests.md](./simulation-tests.md).

El modo de fallo que este nivel existe para impedir es concreto y caro: alguien añade un campo a una estructura Go, el servidor lo emite, el cliente lo ignora en silencio, y seis semanas después una feature del cliente depende de un campo que el servidor nunca envió. No hay error en los logs, no hay excepción: hay una pantalla vacía y una tarde de depuración. Con contract tests, ese cambio rompe el build el mismo día.

---

## 2. La cadena de propagación del schema

Canon §13 y regla SC-1 de [../specs/websocket-protocol.md](../specs/websocket-protocol.md) §12: **los esquemas Zod son la única fuente de verdad**.

```
packages/protocol/src/v1/*.ts          Zod  ← FUENTE ÚNICA DE VERDAD (escrito a mano)
              │
              │  pnpm run protocol:build   (tsc + src/scripts/export-schema.ts)
              ▼
packages/protocol/schema/v1/            JSON Schema  ← GENERADO, nunca editado a mano
  client-message.schema.json
  server-message.schema.json
  error-codes.json
              │                    │
              │                    │  (paso de sincronización del mismo script)
              │                    ▼
              │       services/game-server/internal/protocol/schema/v1/*.json
              │                    │
              │                    │  go:embed  (internal/protocol/schema.go)
              ▼                    ▼
     apps/web (tipos +      Game Server (SOLO en contract tests;
     validación en dev)     la validación de runtime está escrita
                            a mano en Go por rendimiento)
```

**Por qué existe el paso de sincronización.** `go:embed` solo puede incluir ficheros que estén dentro del árbol del módulo Go. `packages/protocol/schema/v1/` vive fuera de `services/game-server/`, así que `pnpm run protocol:build` genera el artefacto canónico **y** deposita una copia idéntica dentro del módulo. Esa copia es un artefacto generado más: no se edita a mano y su deriva se detecta igual que la del original (§6). El propio código lo dice:

```go
// internal/protocol/schema.go
//
// Estos archivos NO se editan a mano: los produce `pnpm run protocol:build`.
// Existen aquí porque go:embed no puede salir del módulo Go, y se versionan para
// que cualquier cambio de contrato aparezca en el diff de la PR.
//
//go:embed schema/v1/*.json
var SchemaFS embed.FS
```

**`error-codes.json` es el tercer artefacto y no es decorativo.** Declara `{version, codes[]}`, y `protocol.ExportedErrorCodes()` lo lee **comprobando además que la versión declarada coincide con la que habla el servidor**: un `error-codes.json` de otra versión del protocolo se rechaza en vez de compararse.

**Por qué la validación de runtime en Go no usa el schema.** Validar cada frame contra un JSON Schema en el camino caliente cuesta reflexión y asignaciones en cada mensaje, a 20 msg/s por conexión y con el presupuesto de un tick de 100 ms. La validación de runtime está escrita a mano; **los contract tests son la prueba de que la implementación manual y el schema coinciden** (regla SC-3). Sin este nivel, esa duplicación sería una bomba de relojería; con él, es una optimización verificada.

---

## 3. Dónde viven los tests

El contrato se verifica **desde los dos lados**, porque una sola dirección no detecta la mitad de los fallos.

| Lado | Ubicación real | Estado | Qué comprueba |
|---|---|---|---|
| Go | `services/game-server/internal/protocol/contract_test.go` | ✔ | Que el catálogo de códigos de Go coincide con el exportado; que los esquemas embebidos existen y son JSON válido; que todos los tipos de mensaje declarados en Go aparecen en el esquema; y que los envelopes y payloads serializan con los campos del contrato |
| TypeScript | `packages/protocol/src/v1/protocol.test.ts` (Vitest) | ✔ | Que los esquemas Zod aceptan y rechazan lo esperado; que el JSON Schema generado **no ha derivado** del Zod del que procede (la misma comprobación que la CI); y que un mensaje válido pasa además la validación por AJV |
| Golden files | `services/game-server/testdata/protocol/v1/*.json` | ○ | Ejemplos compartidos: los mismos ficheros los validarían Go **y** Vitest, y serían los ejemplos publicados en la spec. **Aún no existen** (§7) |

El *harness* de servidor usado en este nivel es **en proceso y sin infraestructura**: estructuras del propio paquete `protocol`, sin sockets ni bases de datos. No requiere Docker y no lleva el gate `EO_INTEGRATION=1`. Las variantes de los mismos escenarios contra Redis y PostgreSQL reales viven en [integration-tests.md](./integration-tests.md); aquí se verifica la **forma y el efecto sobre el protocolo**, no la persistencia.

**Lo que este nivel cubre con una conexión real.** Los casos que exigen una conexión WebSocket de verdad
—handshake, códigos de cierre, tipo de mensaje desconocido, versión no soportada, JSON malformado,
idempotencia y ownership— **ya existen** en `internal/websocket/e2e_test.go`, que levanta un
`httptest.Server` con el servidor WebSocket, el hub, el game loop y la simulación **reales**, y sólo
sustituye PostgreSQL y Redis por implementaciones en memoria. Su test principal,
`TestVerticalSliceEndToEnd`, recorre el flujo completo: handshake → snapshot → `unit.move` → deltas →
desconexión → el mundo sigue avanzando → reconexión → el snapshot refleja el estado autoritativo.

Ese fichero encontró un bug real de producción: el servidor pasaba `r.Context()` a la goroutine de la
sesión, y ese contexto se cancela en cuanto retorna el handler HTTP —lo que ocurre de inmediato—, así que
toda sesión habría muerto tras su primer mensaje. Hoy `Handler` recibe el contexto de vida del proceso.

Lo que sigue sin cubrir a este nivel es el **rate limiting** (exige controlar el reloj del limitador desde
fuera) y el `seq` por conexión bajo reordenación. Van marcados ○.

---

## 4. Matriz de cobertura: los 19 tipos de mensaje

Ningún tipo puede quedar fuera. **Hoy la cobertura de enumeración ya es completa y automática**, por dos caminos que se cruzan:

- **En Go**, `TestTiposDeMensajePresentesEnElEsquema` recorre `protocol.ClientMessageTypes` y la lista de los 14 tipos servidor→cliente, y exige que cada literal aparezca en el esquema exportado correspondiente. Un tipo declarado en Go que Zod no conozca rompe el test.
- **En Vitest**, la pareja `cada tipo declarado cliente → servidor existe en la unión discriminada` / `… servidor → cliente …` compara el conjunto de `ClientMessage.options` con `CLIENT_MESSAGE_TYPES` (ídem para el servidor). Un tipo listado en el `enum` que no tenga rama en la unión discriminada rompe el test, y viceversa.

Entre los dos, añadir un tipo a un lado sin añadirlo al otro es imposible sin romper la CI.

| Dirección | Tipo | Enumerado en Go | Enumerado en Zod | Golden |
|---|---|---|---|---|
| C→S | `session.hello` | ✔ | ✔ | ○ |
| C→S | `session.ping` | ✔ | ✔ | ○ |
| C→S | `session.view` | ✔ | ✔ | ○ |
| C→S | `unit.move` | ✔ | ✔ | ○ |
| C→S | `unit.cancel_move` | ✔ | ✔ | ○ |
| S→C | `session.welcome` | ✔ | ✔ | ○ |
| S→C | `session.pong` | ✔ | ✔ | ○ |
| S→C | `system.error` | ✔ | ✔ | ○ |
| S→C | `world.snapshot` | ✔ | ✔ | ○ |
| S→C | `entity.spawn` | ✔ | ✔ | ○ |
| S→C | `entity.update` | ✔ | ✔ | ○ |
| S→C | `entity.despawn` | ✔ | ✔ | ○ |
| S→C | `city.update` | ✔ | ✔ | ○ |
| S→C | `territory.update` | ✔ | ✔ | ○ |
| S→C | `unit.move.accepted` | ✔ | ✔ | ○ |
| S→C | `unit.move.rejected` | ✔ | ✔ | ○ |
| S→C | `unit.movement.started` | ✔ | ✔ | ○ |
| S→C | `unit.movement.completed` | ✔ | ✔ | ○ |
| S→C | `unit.movement.cancelled` | ✔ | ✔ | ○ |

El catálogo de comandos es además **cerrado por test**: `TestCatalogoDeComandosEsCerrado` exige `len(protocol.ClientMessageTypes) == 5` y comprueba que `"unit.teleport"`, `"admin.grant"`, `""` y `"world.snapshot"` **no** se aceptan como comandos del cliente. Un tipo servidor→cliente no puede colarse como comando entrante.

---

## 5. Casos obligatorios

### 5.1 Todo mensaje que el servidor emite valida contra el schema

| # | Estado | Test | Verifica |
|---|---|---|---|
| **K1** | ✔ | `TestEsquemasEmbebidosExistenYSonJSONValido` (Go) | Los tres artefactos —`client-message.schema.json`, `server-message.schema.json`, `error-codes.json`— están embebidos, no están vacíos y son JSON válido. Es la primera línea de defensa: sin ella, un `protocol:build` roto se manifestaría como un fallo incomprensible en cualquier otro test |
| **K2** | ✔ | `TestEnvelopeSalienteSerializaConLosCamposDelContrato` (Go) | El envelope saliente lleva `v = 1`, `type`, `seq` (uint64), `ts` (epoch ms) y, cuando responde a un comando, `requestId`. Se comprueba **sobre el JSON deserializado a `map[string]any`**, no sobre la estructura Go: es lo único que demuestra que las etiquetas `json:` son las correctas |
| **K3** | ✔ | `TestRequestIDSeOmiteCuandoNoAplica` (Go) | Un mensaje que no responde a ningún comando **no lleva `requestId` vacío**: el campo se omite (`json:"requestId,omitempty"`) para no confundir al cliente |
| **K4** | ✔ | `acepta system.error con cada código del catálogo` (Vitest) | Los 22 códigos del catálogo producen un `system.error` válido; `rechaza un código de error fuera del catálogo estable` comprueba el reverso con `"ALGO_RARO"`. El `code` pertenece a un conjunto **cerrado** |
| **K5** | ✔ | `TestEntityUpdateEsUnDeltaDeVerdad` (Go) | `entity.update` transporta **sólo lo que cambió**: con `X` e `Y` presentes, el JSON contiene `"x":10` y `"y":20` y **no contiene** `hp`, `status` ni `movement`. Los punteros `nil` se omiten; un campo sin cambios no viaja |
| **K6** | ✔ | `TestMovementStartedLlevaLaPolilineaCompleta` (Go) y `acepta unit.movement.started con la polilínea temporizada completa` (Vitest) | `unit.movement.started` lleva la polilínea **entera** dentro de `movement`, con `movementId`, `path[]`, `startTimeMs`, `arrivalTimeMs` y `target`. Es lo que permite al cliente interpolar sin sondear al servidor. El round-trip conserva los `tMs` (`0` y `1449`) y el `arrivalTimeMs` |
| **K7** | ✔ | `exige una polilínea no vacía` (Vitest) | Un `path: []` se rechaza. Una polilínea vacía no es un movimiento, es un dato corrupto |
| **K8** | ○ | `TestTodoMensajeDelServidorValidaContraElEsquema` | Para los 14 tipos servidor→cliente: se construye la estructura Go, se serializa, y el documento resultante se valida **contra el JSON Schema embebido** con un validador real. Hoy la comprobación Go es de presencia del literal del tipo en el esquema (§4), no de validación completa del documento |
| **K9** | ○ | `TestLosSellosDeTiempoSonEnterosEpochMs` | `ts`, `startTimeMs`, `arrivalTimeMs` y `tMs` son enteros; ningún flotante ni ninguna cadena ISO-8601 en el camino caliente |
| **K10** | ○ | `TestMensajesEmitidosEnUnEscenarioCompleto` | Escenario completo (hello → welcome → move → accepted → started → completed) capturando **todo** lo emitido, validando cada frame. Cubre los mensajes que un test unitario no piensa en construir |
| **K11** | ○ | `TestElSnapshotCabeEnElPresupuestoDeTamaño` | Un `world.snapshot` del área de interés cabe en `EO_WS_MAX_MESSAGE_BYTES` (16 384). **Ojo:** el `world.snapshot` implementado incluye `terrain[]` con el blob del chunk en base64 (1024 bytes ⇒ ~1368 caracteres por chunk), y la sesión sólo lo envía **una vez por chunk y sesión** (`NeedsTerrain` / `MarkTerrainSent`), porque el terreno es inmutable. El presupuesto hay que calcularlo con esa regla, no suponiendo que el terreno viaja en cada snapshot |

### 5.1.1 Los identificadores `bigint` viajan como **número JSON**

Es la convención implementada, y conviene decirla sin ambigüedad porque es fácil documentar lo contrario:

| Campo | Tipo Go | En el JSON |
|---|---|---|
| `unitId`, `movementId`, `cityId`, `id` (entidad), `territoryId` | `int64` | **número JSON** (`"unitId": 42`) |
| `playerId`, `ownerPlayerId`, `sessionId` | `string` (UUID) | **cadena UUID** (`"playerId": "3f2504e0-…"`) |
| `requestId` | `string` (UUID) | cadena UUID, con formato validado por Zod |

Los tests que lo fijan: `TestMovementStartedLlevaLaPolilineaCompleta` y `TestEnvelopeSalienteSerializaConLosCamposDelContrato` en Go, y en Vitest el `clientMove()` con `unitId: 42` y el `movement.movementId: 900`. `acepta un unit.move bien formado` fallaría si el esquema exigiera cadenas.

**Por qué no son cadenas decimales.** El riesgo teórico —un `bigint` por encima de 2^53 pierde precisión en `JSON.parse`— no se materializa en el MVP: los ids los genera `bigint GENERATED ALWAYS AS IDENTITY` desde 1, y llegar a 9 007 199 254 740 992 unidades no es un escenario de este juego. Cambiar a cadena decimal sería un cambio de contrato que exige nueva versión del protocolo (regla de evolución aditiva, §6), no un ajuste de documentación. Si algún día se hace, el test que lo fija es `TestLosIdsBigintViajanComoCadenaDecimal` y `playerId` seguiría **fuera** de esa regla, porque es un `uuid` y no un `bigint`.

### 5.2 Todo mensaje que el cliente emite valida contra el schema

| # | Estado | Test | Verifica |
|---|---|---|---|
| **K12** | ✔ | `acepta un unit.move bien formado` (Vitest) | La forma canónica de `unit.move` se acepta: `{v:1, type:'unit.move', requestId: <uuid>, payload:{unitId:42, target:{x,y}}}` |
| **K13** | ✔ | `un unit.move válido pasa también la validación por JSON Schema` (Vitest) | El **mismo** mensaje que acepta Zod lo acepta AJV compilando `client-message.schema.json`. Es la prueba de que el JSON Schema generado no es más laxo ni más estricto que su origen |
| **K14** | ✔ | `client-message.schema.json / server-message.schema.json existe y es JSON Schema válido para AJV` (Vitest, `it.each`) | Los dos esquemas compilan sin excepción en AJV con `addFormats`. Un `$ref` roto o un `format` desconocido se detecta aquí |
| **K15** | ✔ | `rechaza un tipo desconocido` (Vitest) | `type: 'unit.teleport'` ⇒ `{ ok: false, code: 'INVALID_MESSAGE' }`. El caso espejo en Go es `TestCatalogoDeComandosEsCerrado` |
| **K16** | ✔ | `exige requestId con formato UUID` (Vitest) | `requestId: 'no-es-un-uuid'` ⇒ `INVALID_MESSAGE`. No basta con que el campo esté: tiene formato |
| **K17** | ✔ | `rechaza coordenadas no enteras: el mundo es un grid de tiles` (Vitest) | `target.x = 120.5` ⇒ `INVALID_MESSAGE`. El servidor razona en tiles completos (canon §7); una coordenada fraccionaria no es un redondeo, es un mensaje inválido |
| **K18** | ✔ | `rechaza un payload que intente aportar estado autoritativo desconocido` (Vitest) | Un `hp: 9999` dentro del `payload` **se rechaza en lugar de ignorarse en silencio**. Exige que los esquemas Zod cliente→servidor sean **estrictos** y que el JSON Schema generado lleve `additionalProperties: false` |
| **K19** | ✔ | `unit.move no admite que el cliente envíe una ruta` (Vitest) + `TestElComandoDeMovimientoNoAdmiteRuta` (Go) | El cliente no puede aportar la polilínea ni por accidente ni a propósito. Doble defensa: el esquema lo rechaza en el *boundary*, y la estructura Go `UnitMovePayload` **no tiene dónde guardarla** aunque el JSON la traiga |
| **K20** | ○ | `TestTodoMensajeDelClienteValidaContraElEsquema` | Los 5 tipos cliente→servidor validan en su forma canónica, no sólo `unit.move` |
| **K21** | ○ | `TestFaltaUnCampoObligatorio` | Para cada campo obligatorio de cada uno de los 5 mensajes de cliente, omitirlo produce `INVALID_MESSAGE`. Test generado a partir de la lista de `required` del schema: añadir un campo obligatorio añade automáticamente su caso |
| **K22** | ○ | `TestTipoIncorrectoSeRechaza` | `target.x` como cadena, `payload` como array ⇒ `INVALID_MESSAGE` |

**Asimetría deliberada.** Los esquemas cliente→servidor son **estrictos** (`additionalProperties: false`, K18); los servidor→cliente **no lo son**, para poder añadir campos opcionales sin romper clientes antiguos. No es un descuido: es la única forma de que el protocolo evolucione de manera aditiva sin obligar a todos los clientes a actualizarse a la vez. Un test que exigiera estrictitud en la dirección servidor→cliente estaría verificando lo contrario del diseño.

### 5.3 Los mensajes inválidos se rechazan con `INVALID_MESSAGE` y **sin efectos secundarios**

Este es el bloque que distingue un contract test útil de uno decorativo. No basta con el código de error. **Todo este bloque está ○ previsto**: exige una sesión WebSocket con estado, y hoy no existe `internal/websocket/contract_test.go`.

| # | Test | Verifica |
|---|---|---|
| **K23** | `TestUnMensajeInvalidoNoMutaNadaDelEstado` | Para cada caso de K16 a K22: no se crea ningún `unit_movements`, no cambia ningún `units.status`, no se emite ningún evento de dominio, y el `Pathfinder` doble registra **cero** llamadas |
| **K24** | `TestUnMensajeInvalidoNoConsumeLaClaveDeIdempotencia` | Un mensaje malformado con un `requestId` válido **no** registra la clave de idempotencia: el cliente puede corregir y reenviar con el mismo `requestId` |
| **K25** | `TestUnRechazoConsumeExactamenteUnSeq` | El `seq` avanza sólo por los mensajes realmente emitidos; un rechazo emite exactamente un `system.error` y consume exactamente un `seq` |
| **K26** | `TestJsonMalformadoCierraCon4400` | Bytes que no son JSON, o JSON truncado ⇒ `INVALID_MESSAGE` y cierre `4400` |
| **K27** | `TestElValidadorManualYElEsquemaCoinciden` | Corpus de ~200 documentos (válidos e inválidos, incluidos los generados por fuzzing) pasado por el validador manual de Go **y** por el schema. Un desacuerdo en cualquiera de los dos sentidos es un fallo. Es la verificación directa de la razón de ser de §2: si la validación de runtime está escrita a mano, alguien tiene que demostrar que coincide con el contrato |

### 5.4 Versión no soportada ⇒ `UNSUPPORTED_VERSION`

| # | Estado | Test | Entrada | Esperado |
|---|---|---|---|---|
| **K28** | ✔ | `distingue versión no soportada de mensaje inválido` (Vitest) | `{...unit.move válido, v: 2}` | `{ ok: false, code: 'UNSUPPORTED_VERSION' }`, **distinto de `INVALID_MESSAGE`**. Es la distinción que permite al cliente saber si debe actualizarse o si envió basura |
| **K29** | ○ | `TestFaltaLaVersion` | Envelope sin `v` | `INVALID_MESSAGE` (falta un campo obligatorio), no `UNSUPPORTED_VERSION` |
| **K30** | ○ | `TestVersionCeroNegativaONoNumerica` | `v = 0`, `v = -1`, `v = "1"` | `UNSUPPORTED_VERSION` para los numéricos fuera de rango; `INVALID_MESSAGE` para el tipo incorrecto |
| **K31** | ○ | `TestLaVersionSeComprubaAntesDelPayload` | `v = 2` con un `payload` además malformado | Gana `UNSUPPORTED_VERSION`: la versión se comprueba primero, porque un `payload` v2 no tiene por qué ser interpretable con reglas v1 |

`ExportedErrorCodes()` aplica la misma disciplina en el otro extremo de la cadena: si `error-codes.json` declara una versión distinta de la que habla el servidor, el error dice exactamente eso en vez de comparar catálogos de versiones diferentes.

### 5.5 Comando sin autenticar ⇒ rechazo y cierre `4401`

Canon §14: el cliente abre la conexión y envía `session.hello { ticket }` **como primer mensaje**, antes de 5 s (`WSHandshakeTimeout`, constante de código no configurable por entorno).

| # | Estado | Test | Escenario | Esperado |
|---|---|---|---|---|
| **K32** | ✔ | `TestPreAuthSoloAdmiteElHandshake` (Go) | El catálogo de mensajes admitidos antes de autenticar | `protocol.IsPreAuth` es cierto **sólo** para `session.hello`; es falso para `unit.move`, `unit.cancel_move`, `session.ping` y `session.view`. **Ningún mensaje precede al hello, ni siquiera los inocuos.** Es la comprobación puramente declarativa; el efecto sobre la conexión lo cubre K33 |
| **K33** | ○ | `TestNingunComandoAntesDeAutenticar` | `unit.move` como primer frame, sin `session.hello` previo | `UNAUTHORIZED` y cierre **`4401`**. El comando **no se ejecuta**: cero llamadas al pathfinder, cero movimientos |
| **K34** | ○ | `TestTimeoutDeHandshakeCierraCon4408` | Conexión abierta sin enviar nada durante 5 s (`clock.FakeClock`) | Cierre **`4408`**, distinto de `4401`: el cliente debe poder distinguir "no me autenticaste" de "tardaste demasiado" |
| **K35** | ○ | `TestUnSegundoHelloSeRechaza` | Un segundo `session.hello` en una sesión ya autenticada | `INVALID_MESSAGE`; la identidad de la sesión no se reasigna nunca |
| **K36** | ✔ (parcial) | `rechaza un payload que intente aportar estado autoritativo desconocido` (Vitest) | `unit.move` con un `playerId` inyectado en el `payload` | El campo se rechaza como propiedad desconocida (K18); la identidad procede exclusivamente del claim `sub` del ticket verificado. La mitad del ticket la verifica `ticket_test.go` (ver [unit-tests.md](./unit-tests.md) §1) |
| **K37** | ✔ | `TestCodigosDeCierreSonDelRangoPrivado` (Go) | Los seis códigos de cierre declarados | Todos están en el rango privado de aplicación **4000–4999**. Un código fuera de ese rango colisionaría con los códigos estándar del protocolo WebSocket |
| **K38** | ○ | `TestLosCodigosDeCierreSonLosCanonicos` | Tabla completa | `4400` mensaje inválido, `4401` no autenticado, `4403` no autorizado, `4408` timeout de handshake, `4429` rate limited, `4500` error interno. Ningún código fuera de esa lista. Complementa a K37, que hoy comprueba el rango pero no el valor de cada uno |

### 5.6 Comando sobre una unidad ajena ⇒ `UNIT_NOT_OWNED`

| # | Estado | Test | Escenario | Esperado |
|---|---|---|---|---|
| **K39** | ✔ | `TestRechazosDeMovimiento/unidad ajena` (nivel `simulation`) | Jugador A envía `unit.move` sobre una unidad del jugador B | `unit.move.rejected` con `code: "UNIT_NOT_OWNED"`; **no se emite ningún `unit.movement.started`**, la unidad de B no se mueve. Ver [simulation-tests.md](./simulation-tests.md) §4.3 |
| **K40** | ✔ | `TestRechazosDeMovimiento` (el mismo) | El mismo caso | Se emite `unit.move.rejected`, **no** `system.error`: `unit.move.rejected` es el error tipado de ese comando ([../specs/websocket-protocol.md](../specs/websocket-protocol.md) §7.4) |
| **K41** | ✔ | `UnitMoveRejectedPayload` (estructura) | El mismo caso | El `payload` del rechazo es exactamente `{unitId, code, message}`: **no hay dónde filtrar** posición, propietario ni estado de la unidad ajena. La ausencia de fuga está garantizada por el tipo, no por una aserción |
| **K42** | ○ | `TestElRechazoDeUnidadAjenaLlevaElRequestId` | El mismo caso, a través del WebSocket | El envelope de respuesta lleva el `requestId` original para que el cliente correlacione |
| **K43** | ○ | `TestCancelarUnidadAjenaSeRechaza` | `unit.cancel_move` sobre unidad ajena | `UNIT_NOT_OWNED`; el movimiento de B sigue `ACTIVE` |

### 5.7 `requestId` duplicado ⇒ ningún segundo efecto

| # | Estado | Test | Escenario | Esperado |
|---|---|---|---|---|
| **K44** | ○ | `TestUnRequestIdDuplicadoNoProduceUnSegundoEfecto` | El mismo `unit.move` con el mismo `requestId`, dos veces | Un solo movimiento creado; dos respuestas con `payload` **idéntico** y `seq` distinto (el `seq` es por conexión y siempre avanza) |
| **K45** | ○ | `TestIdempotenciaTrasReconectar` | Reenvío del mismo `requestId` tras reconectar | Sigue siendo idempotente: la clave es `(playerId, requestId)`, no la conexión |
| **K46** | ○ | `TestUnDuplicadoNoEmiteUnBroadcastDuplicado` | Duplicado sobre un chunk con varios suscriptores | Los demás suscriptores reciben `unit.movement.started` **una sola vez** |
| **K47** | ✔ | `TestNuevaOrdenReemplazaLaAnterior` (nivel `simulation`) | Segundo `unit.move` con `requestId` nuevo sobre la misma unidad | El movimiento anterior se cancela con **`reason: "REPLACED"`** y `stoppedAt` en el tile realmente alcanzado, y se crea uno nuevo con id distinto que parte de ahí |

**El enum de `reason` es `REPLACED | CANCELLED_BY_PLAYER | PATH_BLOCKED | UNIT_DEAD | SERVER`** (`movement.CancelReason`, transmitido como cadena en `unit.movement.cancelled.payload.reason`). Un test que espere `SUPERSEDED` o `PLAYER_REQUEST` está escrito contra un contrato que no existe: el reemplazo es `REPLACED` y la cancelación explícita del jugador es `CANCELLED_BY_PLAYER`.

**Nota sobre la idempotencia.** La reserva del `requestId` se hace con `SETNX` en Redis antes de ejecutar (`Claim`), y si Redis no responde **el comando se ejecuta igualmente**: se prefiere jugar a bloquear al jugador. Un test de este nivel no puede verificar la garantía durable; la variante con `idempotency_keys` y el TTL de la clave vive en [integration-tests.md](./integration-tests.md) §5.5.

### 5.8 Límites de tamaño y de tasa

Canon §13: mensaje ≤ 16 KiB (`EO_WS_MAX_MESSAGE_BYTES=16384`), 20 msg/s por conexión con burst 40 (`EO_WS_RATE_LIMIT_PER_SECOND`, `EO_WS_RATE_LIMIT_BURST`). El arranque exige además que el burst **no sea menor** que la tasa, y eso ya está verificado por `TestBurstNoPuedeSerMenorQueLaTasa` (ver [unit-tests.md](./unit-tests.md) §10).

| # | Estado | Test | Entrada | Esperado |
|---|---|---|---|---|
| **K48** | ✔ | `coinciden con los valores por defecto de la configuración EO_WS_*` (Vitest) | `TRANSPORT_LIMITS` del paquete de protocolo | `maxMessageBytes === 16384`, `rateLimitPerSecond === 20`, `rateLimitBurst === 40`. El cliente y el servidor parten de los **mismos** números: si alguien cambia un valor por defecto en `internal/config` y no en el protocolo, este test lo delata |
| **K49** | ○ | `TestUnMensajeEnElLimiteSeAcepta` | Frame de exactamente **16384** bytes, bien formado | Aceptado. El límite es inclusivo |
| **K50** | ○ | `TestUnMensajeDemasiadoGrandeSeRechazaSinParsear` | Frame de **16385** bytes | `MESSAGE_TOO_LARGE` y cierre `4400`. **Sin parsear**: el rechazo ocurre por longitud, antes de deserializar. El test lo verifica con un frame cuyo contenido es JSON deliberadamente inválido: si el error fuese `INVALID_MESSAGE`, es que se parseó primero |
| **K51** | ○ | `TestElRateLimitPermiteLaRafaga` | 40 mensajes instantáneos con `clock.FakeClock` | Los 40 se aceptan: es exactamente el burst |
| **K52** | ○ | `TestElMensaje41SeLimita` | Mensaje 41 dentro del mismo segundo | `RATE_LIMITED`, sin procesar el comando |
| **K53** | ○ | `TestElCuboSeRellenaConElTiempo` | Tras 1 s de `clock.FakeClock`, 20 mensajes más | Aceptados: el cubo se rellena a `EO_WS_RATE_LIMIT_PER_SECOND` por segundo |
| **K54** | ○ | `TestElAbusoSostenidoCierraCon4429` | Presión sostenida muy por encima del límite | Cierre `4429` |
| **K55** | ○ | `TestElServidorNuncaEmiteUnFrameDemasiadoGrande` | Escenario con un `world.snapshot` grande | Ningún frame emitido supera 16 384 bytes |

**Sobre un sub-límite propio para `session.view`.** No existe: `session.view` está sujeto al **mismo cubo global** que el resto de mensajes. El canon §17 sólo define `EO_WS_RATE_LIMIT_PER_SECOND` y `EO_WS_RATE_LIMIT_BURST`, y no hay ninguna variable `EO_` para un límite específico. Un sub-límite dedicado es **TBD (fuera de MVP)**: si se añade, será primero como variable de configuración publicada al cliente en el handshake, y sólo entonces como test. Documentar un "4 mensajes por segundo" que ningún código aplica ni ningún cliente conoce provocaría que un cliente respetuoso del contrato recibiera `RATE_LIMITED` sin haber excedido ningún límite anunciado.

**Sobre el troceado del snapshot.** El `world.snapshot` implementado **no tiene campo `truncated`**: su payload es `{serverTimeMs, tick, chunks[], terrain[], units[], cities[], territories[]}`. Lo que acota su tamaño es que el terreno de un chunk se envía **una sola vez por sesión** (`NeedsTerrain` / `MarkTerrainSent`), porque es inmutable. Un mecanismo de troceado explícito para snapshots que no quepan es **TBD (fuera de MVP)**.

### 5.9 Orden por `seq`

Canon §13: `seq` es un uint64 monótono **por conexión**. Todo este bloque está ○ previsto: el `seq` lo asigna la sesión WebSocket, y `contract_test.go` lo construye hoy a mano (`protocol.NewOutbound(..., seq, ...)`) sin ejercitar al asignador.

| # | Test | Verifica |
|---|---|---|
| **K56** | `TestElSeqCreceSinHuecos` | 10 000 mensajes emitidos en una conexión: `seq` es exactamente `n, n+1, …` sin huecos ni repeticiones |
| **K57** | `TestElSeqEsPorConexion` | Dos conexiones simultáneas: cada una lleva su propia secuencia; la del jugador B no depende del tráfico de A |
| **K58** | `TestElSeqReiniciaAlReconectar` | La nueva conexión arranca su `seq` desde el inicio; el cliente descarta su `lastSeenSeq` anterior al recibir el nuevo `session.welcome` |
| **K59** | `TestElOrdenDentroDelTickSigueElOrdenDeFases` | Orden aceptada que reemplaza otra: orden exacto por `seq`, `unit.movement.cancelled(REPLACED)` → `unit.move.accepted` → `unit.movement.started`. Refleja el orden de fases del tick ([../architecture/game-loop.md](../architecture/game-loop.md)) |
| **K60** | `TestElTsNoEsCriterioDeOrdenacion` | Varios mensajes del mismo tick comparten `ts`; sólo `seq` los ordena. El test comprueba que **existen empates de `ts`**, para que el cliente no pueda apoyarse en él por casualidad |

### 5.10 Ciclo completo de reconexión

Canon §13: `world.snapshot` al conectar y luego deltas incrementales; nunca se retransmite el mundo completo.

| # | Estado | Test | Paso | Verificación |
|---|---|---|---|---|
| **K61** | ✔ | `TestSnapshotDelAreaDeInteres` (nivel `simulation`) | Snapshot del radio de interés 2 chunks centrado en la ciudad | `ServerTimeMs` correcto, los **3** aldeanos, **1** ciudad, `terrain[]` con `size == 32` y el blob **en base64**; un área lejana (`(60,60)`, radio 0) no ve ninguna unidad ni ciudad de ese jugador. Es la comprobación de que **el snapshot contiene el conjunto de interés y sólo ése** |
| **K62** | ✔ | `TestSnapshotIncluyeElMovimientoEnCurso` (ídem) | Snapshot a mitad de un movimiento | La unidad aparece con su `movement` no nulo —«debe traer su polilínea para que el cliente interpole»—, con `target` correcto, y **`X` es la posición autoritativa de ese instante** (14 tras 1200 ms), no la del inicio del movimiento |
| **K63** | ✔ | `TestTicketNoSePuedeReutilizar` (`ticket_test.go`) | Segundo canje del mismo ticket | `auth.ErrReplayedTicket`: el ticket es de un solo uso (canon §14). La traducción a `UNAUTHORIZED` + cierre `4401` está ○ pendiente, en el nivel WebSocket |
| **K64** | ○ | `TestElCicloDeReconexionProduceUnSnapshotCoherente` | 1. `session.hello` → `session.welcome` | El `welcome` valida y trae `{sessionId, playerId, serverTimeMs, tickDurationMs, heartbeatIntervalMs, world:{width,height,chunkSize}}` |
| | | | 2. `world.snapshot` inicial | Centrado en la ciudad del jugador (canon §13) |
| | | | 3. `unit.move` aceptado: llegan `unit.move.accepted` y `unit.movement.started` | Ambos validan; el `started` incluye la polilínea completa |
| | | | 4. Desconexión abrupta a mitad del movimiento, 5. reconexión con ticket nuevo | Nuevo `session.welcome` y nuevo `world.snapshot` |
| | | | 6. **Coherencia** | El snapshot trae la **misma polilínea** (mismos waypoints, mismos `tMs`, mismo `startTimeMs`, mismo `arrivalTimeMs`) y la unidad en `MOVING`. La posición mostrada corresponde al instante de la reconexión, derivada de la polilínea, no a la del momento de la desconexión |
| **K65** | ○ | `TestReconectarTrasLaLlegadaMuestraElEstadoFinal` | Reconexión después de `arrivalTimeMs` | La unidad aparece `IDLE` en el tile final; no llega un `unit.movement.started` obsoleto |
| **K66** | ○ | `TestSinRetransmisionCompletaTrasCambiarLaVista` | `session.view` que desplaza el área | Llegan `entity.spawn` de lo que entra y `entity.despawn` con `reason: "OUT_OF_INTEREST"` de lo que sale, y **ningún** `world.snapshot` nuevo |
| **K67** | ○ | `TestElTerrenoDeUnChunkViajaUnaSolaVezPorSesion` | Dos snapshots de la misma sesión que comparten chunks | El segundo **no** repite el `terrain[]` de los chunks ya enviados (`NeedsTerrain` / `MarkTerrainSent`): el terreno es inmutable y reenviarlo es puro desperdicio de presupuesto de frame |

El enum de `entity.despawn.reason` implementado es **`OUT_OF_INTEREST | DEAD | GARRISONED | HIDDEN | REMOVED`**: cubre explícitamente el ocultamiento y la guarnición, de modo que un cliente nunca tiene que elegir entre reproducir efectos de destrucción o fingir que la unidad salió del área de interés.

---

## 6. Deriva de schema: fallo de CI

La **deriva de schema** es la situación en la que la fuente de verdad y sus derivados dejan de coincidir. Es silenciosa por naturaleza: todo compila, todos los tests locales pasan, y el contrato ya está roto.

### 6.1 Las cuatro derivas y cómo se detecta cada una

| # | Deriva | Detección | Dónde |
|---|---|---|---|
| **D1** | Alguien modifica un esquema Zod y no regenera el JSON Schema | `pnpm run protocol:build` + `pnpm run protocol:check` + `git diff --exit-code`; y, en local, el test `no ha derivado respecto de los esquemas Zod (misma comprobación que la CI)` de Vitest, que regenera con `zodToJsonSchema` y compara contra el fichero en disco | job `protocol` |
| **D2** | El JSON Schema se regenera en `packages/protocol/schema/` pero la copia embebida en el módulo Go queda vieja | El mismo `git diff --exit-code` cubre **también** `services/game-server/internal/protocol/schema` | job `protocol` |
| **D3** | Alguien añade o renombra un tipo de mensaje en Go sin tocar Zod | `TestTiposDeMensajePresentesEnElEsquema`: el literal del tipo deja de aparecer en el esquema exportado | job `game-server` |
| **D4** | Alguien añade o quita un código de error en un solo lado | `TestCatalogoDeErroresCoincideConElExportado`: `require.ElementsMatch` entre `protocol.AllErrorCodes` y `ExportedErrorCodes()`, con el mensaje «¿ejecutaste `pnpm run protocol:build`?» | job `game-server` |
| **D5** | Un ejemplo publicado en la spec deja de ser válido | ○ Pendiente: requiere los golden files de §7, que aún no existen |

### 6.2 El check de deriva, literal

Éste es el paso real del workflow, copiado de `.github/workflows/ci.yml`:

```yaml
# .github/workflows/ci.yml — job `protocol`
- name: Verificar que el JSON Schema no ha derivado de Zod
  # Si esto falla, alguien cambió los esquemas Zod sin regenerar los
  # artefactos que consume el Game Server. El contrato se habría roto en
  # silencio; aquí se rompe en la CI, que es donde debe romperse.
  run: |
    pnpm run protocol:build
    pnpm run protocol:check
    if ! git diff --exit-code -- packages/protocol/schema services/game-server/internal/protocol/schema; then
      echo "::error::Los esquemas generados no están al día. Ejecuta 'pnpm run protocol:build' y versiona el resultado."
      exit 1
    fi
```

Son **dos** comprobaciones encadenadas y no redundantes: `protocol:check` ejecuta el exportador en modo verificación (falla si lo que produciría difiere de lo que hay), y el `git diff` detecta además cualquier edición manual de los artefactos versionados, incluida la copia del módulo Go.

Si el `git diff` no está limpio, el check está en rojo y el pull request no es válido. No hay auto-commit del artefacto regenerado: **quien cambia el protocolo regenera y revisa el diff**, porque ese diff es la descripción exacta del cambio de contrato y es lo que hay que leer en la revisión.

### 6.3 Por qué esto protege el contrato

Sin el check de deriva, la cadena de propagación es una convención social: funciona mientras nadie tenga prisa. Con el check, se convierte en una propiedad verificada del repositorio, y produce tres garantías concretas:

1. **El artefacto del repositorio siempre corresponde al Zod del repositorio.** Cualquiera puede clonar, no ejecutar el build, y confiar en `schema/v1/*.json`. Sin el check, ese fichero es folclore.
2. **Todo cambio de contrato es visible en la revisión.** Un campo añadido, un `required` retirado, un enum ampliado: aparecen como diff en un fichero JSON que el revisor lee. Sin el check, el cambio de contrato se esconde dentro de un cambio de TypeScript de 400 líneas.
3. **Go y TypeScript no pueden divergir en silencio.** El servidor embebe la copia sincronizada; el cliente importa los tipos generados desde el mismo Zod. Si uno de los dos se adelanta, D2, D3 o D4 lo detienen el mismo día, no seis semanas después.

Y una consecuencia de diseño: **`v2` no modifica `src/v1/`**. Se crea `src/v2/` y `schema/v2/`, y los dos árboles coexisten mientras haya clientes v1 en producción. El check de deriva se aplica a ambos por separado, y los contract tests de v1 siguen ejecutándose sin cambios. Un contrato publicado es inmutable; la evolución es aditiva. La comprobación de versión de `ExportedErrorCodes()` es la primera pieza de esa disciplina que ya está escrita.

---

## 7. Golden files — ○ previstos

**Aún no existen.** `services/game-server/testdata/` está sin crear, y hoy los ejemplos que ejercitan el contrato viven inline en los tests (el `clientMove()` de Vitest, los payloads construidos en `contract_test.go`). Funciona, pero tiene el defecto que los golden files resuelven: los ejemplos de la spec y los del test son dos copias que nadie compara.

Cuando se creen, `services/game-server/testdata/protocol/v1/` contendrá un fichero JSON por tipo de mensaje, y serán los mismos ejemplos publicados en [../specs/websocket-protocol.md](../specs/websocket-protocol.md).

```
testdata/protocol/v1/
├── client/
│   ├── session.hello.json
│   ├── session.ping.json
│   ├── session.view.json
│   ├── unit.move.json
│   └── unit.cancel_move.json
├── server/
│   ├── session.welcome.json
│   ├── … (los 14 tipos servidor→cliente)
└── invalid/
    ├── unit.move.missing_request_id.json
    ├── unit.move.unknown_property.json
    ├── envelope.unsupported_version.json
    └── … (un fichero por caso de rechazo)
```

Reglas:

- Los ficheros de `client/` y `server/` **deben** validar. Los de `invalid/` **deben** ser rechazados, y cada uno declara en su nombre el código de error esperado.
- Se regeneran solo con `go test ./internal/protocol -update-golden`, nunca de forma automática.
- El diff de un golden entra en la revisión como cualquier cambio de contrato (§6.3, punto 2).
- Los tres consumidores —Go, Vitest y la documentación— usan los mismos ficheros. Un ejemplo que solo vive en la prosa de un `.md` se queda obsoleto; uno que además es un test, no.

---

## 8. Ejecución

No hay un script `test:contract` propio: este nivel se ejecuta con los dos scripts que cubren cada lado.

```powershell
pnpm run protocol:build   # primero, para que Go y Vitest trabajen sobre el artefacto actual
pnpm run protocol:test    # lado TypeScript: vitest run en packages/protocol
pnpm run server:test      # lado Go: incluye internal/protocol/contract_test.go
```

`pnpm run verify` encadena los tres (más `typecheck` y `go vet`) y es lo más parecido a reproducir la CI en local. No requiere Docker ni `EO_INTEGRATION=1`: el nivel es hermético y debe terminar en segundos ([strategy.md](./strategy.md) §2).

**Si `TestCatalogoDeErroresCoincideConElExportado` falla con «esquema no embebido» o el catálogo sale vacío**, casi siempre es que falta ejecutar `pnpm run protocol:build`: el propio test lo dice en su mensaje de aserción.

---

## 9. Documentos relacionados

- [../specs/websocket-protocol.md](../specs/websocket-protocol.md) — el contrato completo: envelopes, los 19 mensajes, errores, idempotencia y códigos de cierre.
- [../decisions/ADR-009-shared-protocol-package.md](../decisions/ADR-009-shared-protocol-package.md) — por qué Zod es la fuente única y el JSON Schema el artefacto compartido.
- [../decisions/ADR-006-websocket-protocol.md](../decisions/ADR-006-websocket-protocol.md) — protocolo WebSocket JSON versionado.
- [strategy.md](./strategy.md) — jobs de CI y reglas duras.
- [integration-tests.md](./integration-tests.md) — las variantes durables de idempotencia, tickets y rate limiting.
- [simulation-tests.md](./simulation-tests.md) — la semántica que el schema no puede verificar.
- [../architecture/networking.md](../architecture/networking.md) — sesiones, `seq`, rate limiting e interest management.
- [../architecture/game-loop.md](../architecture/game-loop.md) — el orden de fases que determina el orden de emisión.
- [../invariants/security.md](../invariants/security.md) — `INV-SEC-*`, verificados en gran parte desde este nivel.
