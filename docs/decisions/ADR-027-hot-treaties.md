# ADR-027: Los tratados en caliente los decide la RAM de la simulación; las órdenes van en dos fases y los vencimientos son optimistas

Propósito: fijar cómo el slice de diplomacia ([SPEC-DIPLOMACY](../specs/diplomacy.md)) deja proponer, aceptar,
rechazar, retirar y romper tratados mientras se juega, qué añade al esquema y cómo se entera la simulación.

| Campo | Valor |
|---|---|
| **Estado** | **Aceptado** |
| **Fecha** | **2026-11-03** |
| Ámbito | `internal/domain/diplomacy`, `internal/game/simulation` (`diplomacy.go`, fase 5), `internal/persistence/postgres` (`diplomacy.go`), migración `000009`, `packages/protocol` (`treaty.*`, `treaties`), cliente |
| Índice | [README.md](README.md) |
| Relacionados | [ADR-017](ADR-017-garrison-commands.md) (guarnición), [ADR-022](ADR-022-durable-write-retry-and-loop-inbox.md) (toda escritura declara compensación), [ADR-023](ADR-023-economy-stock-deltas-and-two-phase-orders.md) (órdenes en dos fases y finalización optimista), [ADR-024](ADR-024-combat-simultaneous-resolution.md) (la paz frena el combate) |

---

## Contexto

Hasta E5 los tratados sólo existen si alguien los inserta en la base: el servidor lee los `ACTIVE` al arrancar
(`SetPeaceTreaties`) para que el combate respete la paz, y un barrido de la cola consulta la base cada segundo para
caducar los que tienen `expires_at`, sin avisar a la RAM. La guarnición pregunta a la base, en su transacción, si
hay un tratado que la autorice.

Con tratados en caliente eso no basta: un pacto aceptado tiene que frenar un saqueo en el tick siguiente, y una
ruptura tiene que surtir efecto en su instante exacto. Además, la tabla no guarda quién propuso (el par va en orden
canónico) ni tiene estados para una oferta rechazada o retirada.

## Decisión

**1. La RAM de la simulación es la fuente de verdad de las órdenes.** Al arrancar se cargan los tratados `PROPOSED`
y `ACTIVE` y los rechazos recientes (para `RN-DIPLO-008`). Cada orden de un jugador se valida en el tick contra esa
RAM. La paz que consulta el combate (`AtPeace`) sale de ella, evaluada por fecha: un tratado `ACTIVE` da paz
mientras `now` no alcance su `expires_at` ni su `break_effective_at`.

**2. Las órdenes de los jugadores van en dos fases**, como un encargo (ADR-023 §2). El tick valida y **reserva**:
marca el par como ocupado, de modo que una segunda orden sobre él en el mismo tick o antes de que vuelva la
transacción se rechaza con `INVALID_TARGET`. La transacción, con la clave de la cola `treaty:<a>:<b>` (el par
canónico), escribe la fila y su evento; el resultado vuelve al loop por el buzón, que confirma, difunde
`treaty.update` a los dos y actualiza la paz, o libera la reserva y responde el error.

- `treaty.propose` inserta la fila `PROPOSED` con `proposed_by` y `request_id`; es idempotente por
  `(proposed_by, request_id)`.
- `treaty.respond` y `treaty.cancel` son actualizaciones condicionadas al estado esperado (`WHERE status = …`). Un
  reintento que encuentra la fila ya en el estado de destino termina con éxito.
- Aceptar una alianza con un pacto vigente cierra el pacto (`EXPIRED`) y activa la alianza en la misma transacción.

**3. Los vencimientos son optimistas con compensación**, como completar una casa (ADR-023 §5). La fase 5 mira la RAM
una vez por segundo —sin consultar la base— y aplica en su instante: la oferta caducada (`EXPIRED`), la ruptura cuyo
preaviso venció (`BROKEN`) y el tratado administrativo con `expires_at` vencido (`EXPIRED`). La RAM cambia primero,
difunde y, si era una alianza, expulsa las guarniciones (`RN-DIPLO-005`); después encola la transacción, idempotente.
Si se pierde, la compensación vuelve al loop y el siguiente barrido la reencola. Sustituye al barrido de la base
(`TreatyExpirer`, `treaty.expire`), que no declaraba compensación (ADR-022). La otra que tampoco compensa es
`unit.attacked`, auditoría de E3 que se pierde con un log.

**4. Esquema, migración `000009`:**

| Cambio | Para qué |
|---|---|
| `proposed_by uuid NULL` (FK a `players`) | Saber quién puede responder y quién retirar (`INV-DIPLO-001`). `NULL` en los tratados administrativos. |
| `request_id text NULL` y único parcial `(proposed_by, request_id)` | Idempotencia de `treaty.propose`. |
| `break_effective_at timestamptz NULL` | El instante en que una ruptura anunciada surte efecto; con valor, el tratado está en preaviso. |
| `closed_at timestamptz NULL` | Cuándo se rechazó, retiró o caducó una oferta. |
| `REJECTED` y `WITHDRAWN` en `treaties_status_valid` | Los dos finales nuevos de una oferta. |
| Únicos parciales `(player_a_id, player_b_id)` con `status = 'ACTIVE'` y con `status = 'PROPOSED'` | `INV-DIPLO-002`: un tratado vigente y una oferta pendiente como mucho por par. Sustituye al único por par y tipo. |

**5. Los tratados viajan en `world.snapshot`, no en `session.welcome`.** El welcome lo construye el borde WebSocket,
que no puede leer la RAM de la simulación (canon: un solo escritor). El snapshot lo construye la simulación y, como
todo lo que trae, **reemplaza** la lista del cliente. `treaty.update` lleva cada cambio a las sesiones de los dos
firmantes y a nadie más (Q8).

**6. La expulsión al terminar una alianza usa la salida de siempre** (`RN-GARR-010` a `RN-GARR-014`): cada unidad de
uno guarnecida en la ciudad del otro sale al primer tile libre del anillo, y si no hay, queda pendiente y se
reintenta en la fase 5. El preaviso de 15 minutos hace de gracia, así que `garrisons` no necesita columna de
vencimiento ([garrison.md](../specs/garrison.md) §6.4, precisado por SPEC-DIPLOMACY Q6).

## Alternativas descartadas

- **Que la base decida cada orden, como la guarnición.** Funcionaría para aceptar o rechazar, pero la paz que lee el
  combate tiene que estar en la RAM igualmente, y dos fuentes de verdad para lo mismo divergen en cuanto una
  escritura se reintenta.
- **Vencimientos en dos fases.** Un preaviso que vence es un hecho del reloj, no una petición que pueda rechazarse:
  esperar a la base sólo retrasaría la expulsión y la vuelta de la guerra sin ganar nada.
- **Una sola relación por par, sin oferta encima de un pacto.** Obligaría a romper un pacto (con su preaviso) para
  proponer una alianza: castigaría al que quiere acercarse.

## Consecuencias

### Positivas

- Un tratado aceptado frena ataques y saqueos en el tick en que vuelve su transacción, y una ruptura vuelve a
  permitirlos en su instante exacto.
- Desaparece una de las dos escrituras de la cola sin compensación; queda `unit.attacked`, que es sólo auditoría.

### Negativas

- La RAM carga todos los tratados abiertos del mundo. Son pocos por jugador; si un día fueran muchos, se cargarían
  por jugador al conectar.
- Una orden sobre un par cuya transacción aún no ha vuelto se rechaza: con dos clics muy seguidos, el segundo falla.

### Neutras

- Los tratados administrativos siguen siendo posibles (`proposed_by` nulo) y siguen caducando por `expires_at`.
