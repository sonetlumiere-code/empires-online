# Registro de decisiones de arquitectura (ADR)

Propósito: definir qué es un ADR en Empires Online, cuándo es obligatorio escribir uno, qué formato debe seguir y qué decisiones están ya registradas.

---

## 1. Qué es un ADR

Un **Architecture Decision Record** es un documento corto, inmutable y fechado que captura **una** decisión de arquitectura: el contexto en el que se tomó, la opción elegida, las alternativas que se descartaron y las consecuencias que el equipo acepta a cambio.

Un ADR **no** es documentación de diseño ni un manual de uso. La diferencia práctica:

| Tipo de documento | Responde a | Ejemplo |
|---|---|---|
| ADR | *¿Por qué el sistema es así y no de otra forma?* | [ADR-001](ADR-001-game-server-language.md): por qué el game server es Go y no Node |
| Documento de arquitectura | *¿Cómo está construido?* | [../architecture/game-loop.md](../architecture/game-loop.md) |
| Especificación funcional | *¿Qué debe hacer y bajo qué reglas?* | [../specs/movement.md](../specs/movement.md) |
| Guía | *¿Cómo lo ejecuto o lo modifico?* | [../operations/local-development.md](../operations/local-development.md) |

Un ADR es **append-only**. Una vez en estado `Aceptado` no se reescribe para reflejar un cambio de opinión: se escribe un ADR nuevo que lo sustituye y se marca el antiguo como `Sustituido por ADR-NNN`. Corregir una errata o añadir un enlace sí es admisible; cambiar la decisión o borrar una consecuencia negativa, no. El valor del registro está precisamente en poder leer, dos años después, por qué alguien aceptó un coste que hoy duele.

## 2. Cuándo se escribe un ADR

Se escribe un ADR para **toda decisión que sea costosa de revertir o que condicione la arquitectura**. En la práctica, si al menos uno de estos criterios se cumple, el ADR es obligatorio:

1. **Coste de reversión alto.** Deshacerla implicaría reescribir más de un módulo, migrar datos persistidos o romper el contrato de red.
2. **Condiciona otras decisiones.** Otras piezas del sistema quedarán construidas asumiéndola (ejemplo: elegir Go obliga a resolver cómo se comparte el protocolo con TypeScript, ver [ADR-009](ADR-009-shared-protocol-package.md)).
3. **Cruza un límite de proceso o de lenguaje.** Protocolos, formatos serializados, esquemas de base de datos, contratos entre `apps/web` y `services/game-server`.
4. **Compromete un principio no negociable** del canon técnico: servidor autoritativo, mundo persistente, capas de estado (PostgreSQL durable / Redis hot / RAM simulación), determinismo.
5. **Introduce una dependencia operativa nueva** que puede caer en producción y obliga a documentar un modo degradado.
6. **Fija un presupuesto medible** (latencia de tick, tamaño de mensaje, límite de nodos de pathfinding) del que dependerá el resto del sistema.

No se escribe un ADR para: elegir el nombre de una función, añadir un endpoint dentro de un contrato ya decidido, refactors internos que no cambian contratos, o preferencias de estilo que ya cubre el linter.

Regla operativa del *Definition of Done* del proyecto: una PR que introduce una decisión de las anteriores **no es válida** sin su ADR. El ADR se escribe *antes* o *junto con* la implementación, nunca después como justificación retroactiva.

## 3. Formato obligatorio

Todo ADR contiene exactamente estas secciones, en este orden y con estos nombres:

```markdown
# ADR-NNN: Título de la decisión

Propósito: una línea que resume la decisión.

- **Estado:** Aceptado
- **Fecha:** AAAA-MM-DD
- **Decisores:** equipo de arquitectura
- **Relacionados:** ADR-XXX, ADR-YYY

## Contexto
## Decisión
## Alternativas consideradas
## Consecuencias
### Positivas
### Negativas
### Neutras
## Estado
```

El bloque de metadatos admite dos formas equivalentes, ambas presentes en el registro: la lista de
viñetas anterior (ADR-001 a ADR-008) y una tabla `| Campo | Valor |` con las mismas claves
(ADR-009 a ADR-012), que además añade las filas `Ámbito` e `Índice`. Lo obligatorio es el contenido
—estado, fecha y decisiones relacionadas—, no la sintaxis del bloque.

### 3.1 Contenido de cada sección

- **Contexto.** Las fuerzas en juego: requisitos, restricciones del entorno real, presupuestos numéricos, invariantes afectados. Se escribe en presente y sin mencionar la decisión. Un lector debe poder llegar por sí mismo a varias opciones razonables leyendo solo esta sección.
- **Decisión.** Una frase afirmativa en presente ("El game server se implementa en Go 1.23"), seguida del alcance exacto: qué queda dentro y qué queda fuera. Aquí se fijan versiones, nombres y constantes.
- **Alternativas consideradas.** Cada alternativa se evalúa **de verdad**: se enuncian sus ventajas reales antes que sus inconvenientes, y se explica el motivo concreto del descarte. Una alternativa presentada como hombre de paja invalida el ADR. Si una alternativa era mejor en algún eje, se dice.
- **Consecuencias.** Divididas en tres bloques obligatorios:
  - *Positivas*: lo que se gana. Verificable, no aspiracional.
  - *Negativas*: el precio, sin adornos. Deuda técnica asumida, trabajo extra, riesgos operativos. Si esta sección está vacía, el ADR está mal escrito.
  - *Neutras*: hechos que cambian sin ser buenos ni malos (más ficheros, otro ciclo de release, otra herramienta en el CI).
- **Estado.** Uno de:

| Estado | Significado |
|---|---|
| `Propuesto` | Redactado y en discusión. No se implementa contra él todavía. |
| `Aceptado` | Vigente. El código y el resto de la documentación deben respetarlo. |
| `Sustituido por ADR-NNN` | Ya no vigente. El documento se conserva íntegro y enlaza a su sustituto. |
| `Rechazado` | Se evaluó y se descartó. Se conserva para no volver a discutirlo desde cero. |

No existen estados `Deprecado` ni `Obsoleto`: si algo deja de ser vigente, alguna decisión nueva ocupa su lugar y el estado es `Sustituido por`.

## 4. Convención de numeración y nombres

- Fichero: `ADR-NNN-slug-en-ingles.md` dentro de `docs/decisions/`.
- `NNN` es un entero de tres dígitos con ceros a la izquierda, **asignado de forma monótona y nunca reutilizado**. Un ADR rechazado consume igualmente su número.
- El `slug` va en inglés, en `kebab-case`, y describe el objeto de la decisión, no el veredicto: `game-server-language`, no `use-go`.
- El título interno (`# ADR-NNN: ...`) va en español, igual que el resto de la prosa. Identificadores, tablas, tipos de mensaje, códigos de error, constantes y variables de entorno se escriben en inglés y **exactamente** como los define el canon técnico.
- Los enlaces entre documentos son **relativos** (`ADR-003-postgresql-source-of-truth.md`, `../architecture/game-loop.md`).
- Para reservar un número en paralelo se abre la PR con el fichero vacío en estado `Propuesto`; el número queda tomado desde ese momento.

## 5. Índice de ADR

Doce decisiones registradas para el primer vertical slice. Los doce ficheros viven en este mismo
directorio, `docs/decisions/`, y todos están en estado `Aceptado` con fecha **2026-09-09**.

| ADR | Título | Estado | Resumen |
|---|---|---|---|
| [001](ADR-001-game-server-language.md) | Lenguaje del game server — Go 1.23 | Aceptado | El servidor autoritativo se implementa en Go 1.23 por concurrencia con goroutines, GC de baja latencia compatible con un tick de 100 ms y despliegue como binario estático. |
| [002](ADR-002-authoritative-server.md) | Servidor autoritativo | Aceptado | El cliente envía intención y jamás estado; el servidor es la única autoridad sobre posición, HP, recursos, ownership, cooldowns y resultados. |
| [003](ADR-003-postgresql-source-of-truth.md) | PostgreSQL como fuente de verdad durable | Aceptado | Todo estado durable vive en PostgreSQL, con transacciones ACID, `CHECK` e índices parciales que materializan invariantes del dominio. |
| [004](ADR-004-redis-hot-state.md) | Redis como hot state | Aceptado | Redis almacena solo estado caliente y transitorio (presencia, sesiones, locks, cooldowns, idempotencia, caché); su pérdida total nunca pierde estado durable. |
| [005](ADR-005-pixijs-renderer.md) | PixiJS como renderer del cliente | Aceptado | El mundo isométrico se dibuja con PixiJS 8 sobre WebGL, con el ciclo de vida de la escena fuera del árbol de React. |
| [006](ADR-006-websocket-protocol.md) | Protocolo WebSocket con JSON versionado | Aceptado | Transporte WSS con JSON UTF-8 y `"v": 1` explícito en cada mensaje, con criterio medible para migrar a binario sin tocar el dominio. |
| [007](ADR-007-game-loop-frequency.md) | Frecuencia del game loop fija a 10 Hz configurable | Aceptado | El bucle autoritativo corre a `EO_TICK_RATE_HZ=10` (período de 100 ms), con presupuesto de cómputo por tick y política explícita ante un tick que se pasa de tiempo. |
| [008](ADR-008-grid-coordinate-system.md) | Grid cartesiano, chunks de 32×32 y vecindad de 8 sin corner cutting | Aceptado | El dominio razona en enteros sobre un grid cartesiano (la proyección isométrica es exclusiva del cliente), el chunk mide 32 × 32 tiles y la diagonal exige ambos ortogonales transitables. |
| [009](ADR-009-shared-protocol-package.md) | Paquete de protocolo compartido: Zod como fuente de verdad y JSON Schema exportado | Aceptado | Zod es la fuente de verdad del protocolo, el build exporta JSON Schema y el game server lo embebe con `go:embed` para *contract tests*; la validación en runtime del servidor se escribe a mano en Go. |
| [010](ADR-010-authentication-game-ticket.md) | Autenticación mediante game ticket efímero de un solo uso | Aceptado | Next.js autentica al usuario y emite un JWT HS256 de 60 s con `jti` consumible una sola vez en Redis; el game server lo verifica en `session.hello` y nunca gestiona contraseñas. |
| [011](ADR-011-movement-timed-polyline.md) | Movimiento persistido como polilínea temporizada | Aceptado | El camino se persiste una sola vez como array de waypoints `{x, y, tMs}`, de modo que la posición autoritativa es una función pura de `(polilínea, tiempo)` sin replay de ticks. |
| [012](ADR-012-database-migrations.md) | Migraciones SQL versionadas con golang-migrate embebidas en el binario | Aceptado | El esquema evoluciona con ficheros SQL versionados gestionados por golang-migrate y embebidos con `go:embed`; el game server es el único propietario del esquema. |

Decisiones que el proyecto aplica pero que **no** tienen ADR propio, y de las que por tanto no debe
citarse ninguno: el monorepo pnpm con módulo Go (canon §2), el determinismo mediante `Clock` y
`RandomSource` (canon §1.5), el *interest management* por suscripción a chunks (canon §13) y la
elección de A\* con heurística octile tras la interfaz `Pathfinder`
([../architecture/pathfinding.md](../architecture/pathfinding.md)). Si alguna de ellas necesita
registrarse, toma el siguiente número libre —**013** en adelante—, nunca un número ya asignado.

## 6. Cómo se propone un ADR nuevo

1. Copia el esqueleto de la sección 3 en `docs/decisions/ADR-NNN-<slug>.md` con el siguiente número libre del índice.
2. Redacta **Contexto** y **Alternativas consideradas** antes que **Decisión**. Si al terminar el contexto la decisión no parece forzada, faltan restricciones por escribir.
3. Estado inicial `Propuesto`. Abre PR enlazando la especificación o el issue que la motiva.
4. Al aprobarse: cambiar a `Aceptado`, fijar la fecha del día de aprobación y añadir la fila al índice de la sección 5 en la misma PR.
5. Si sustituye a un ADR previo, editar el antiguo únicamente para poner `Sustituido por ADR-NNN` y añadir el enlace. Nada más.

## 7. Referencias

- [../architecture/overview.md](../architecture/overview.md) — visión general del sistema y de las capas de estado.
- [../architecture/game-loop.md](../architecture/game-loop.md) — fases del tick y presupuesto de 100 ms.
- [../specs/websocket-protocol.md](../specs/websocket-protocol.md) — contrato de red v1 completo.
- [../operations/local-development.md](../operations/local-development.md) — entorno de desarrollo real y prerrequisitos.
- [../README.md](../README.md) — índice maestro de la documentación y estado actual del proyecto.
