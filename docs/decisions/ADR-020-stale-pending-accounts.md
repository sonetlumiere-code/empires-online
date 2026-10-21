# ADR-020: Una cuenta `PENDING` abandonada se resuelve preguntando al mundo, nunca se borra a ciegas

Propósito: cerrar la deuda que dejó [ADR-018](ADR-018-identity-store-and-ticket-issuer.md) §4.2 —«una cuenta
`PENDING` cuyo dueño nunca vuelve ocupa su nombre hasta que alguien la limpie; esa limpieza no existe todavía»—
sin crear la inconsistencia contraria: un jugador en el mundo sin cuenta que lo reclame.

| Campo | Valor |
|---|---|
| **Estado** | **Aceptado** |
| **Fecha** | **2026-10-21** |
| Ámbito | `apps/web` (`authService`, almacén de identidad, migración 2), `internal/httpapi` (`GET /internal/players/{id}`), `scripts` |
| Índice | [README.md](README.md) |
| Relacionados | [ADR-018](ADR-018-identity-store-and-ticket-issuer.md) (completa su §4.2), [ADR-010](ADR-010-authentication-game-ticket.md) |

---

## 1. Contexto

El alta cruza dos procesos ([ADR-018](ADR-018-identity-store-and-ticket-issuer.md) §2.5): `apps/web` crea la
cuenta `PENDING`, pide al Game Server que funde al jugador y sólo entonces la activa. Si la fundación no
responde —red, 5xx, el proceso caído—, la cuenta queda `PENDING` y el alta responde 503; el siguiente login con
la contraseña correcta repite la fundación, que es idempotente por `playerId`.

Si el jugador no vuelve nunca, su cuenta ocupa el nombre para siempre: `accounts.username` es `UNIQUE`, y nadie
más puede darse de alta con él. Es la deuda que este ADR cierra. Las fuerzas:

- **Una cuenta `PENDING` no implica que el jugador no exista.** El 503 se da también cuando el Game Server
  **sí** fundó y la respuesta se perdió por el camino. Borrar esa cuenta dejaría al jugador en el mundo sin
  identidad que lo reclame, y su nombre ocupado en `players.username`, que también es `UNIQUE`: el próximo que
  pidiera ese nombre recibiría `USERNAME_TAKEN` del Game Server y su cuenta nueva se borraría, en bucle. Es
  peor que la deuda.
- **La fundación puede estar en vuelo.** El login de una cuenta `PENDING` repite la fundación en cualquier
  momento. Una limpieza que comprueba «el jugador no existe» y borra después puede cruzarse con una fundación
  que se confirma entre las dos cosas.
- **El daño real es el nombre, no las filas.** Una cuenta abandonada son unos cientos de bytes; lo que molesta
  es que bloquea un nombre que otro quiere usar.
- **El despliegue previsto incluye funciones *serverless*** ([ADR-018](ADR-018-identity-store-and-ticket-issuer.md)
  §4.2): un temporizador dentro del proceso de Next.js no es fiable allí.

## 2. Decisión

**Una cuenta `PENDING` cuyo último intento de fundación tiene más de una hora es *abandonada*. Se resuelve
preguntando al Game Server si su jugador existe: si existe, la cuenta se activa; si no existe, se borra; si no
contesta, se deja como está. Se resuelve cuando estorba —un alta choca con su nombre— y en un barrido explícito,
`pnpm run identity:sweep`.**

### 2.1 Qué es una cuenta abandonada

- `accounts.founding_attempted_at` (migración 2 del almacén de identidad): el instante, en el reloj de la base,
  del último intento de fundar a ese jugador. Lo fija el alta al crear la cuenta y lo **renueva** el login de una
  cuenta `PENDING` **antes** de llamar al Game Server. A las filas anteriores se les da su `created_at`.
- Abandonada ⟺ `status = 'PENDING'` y `founding_attempted_at < now() − PENDING_STALE_AFTER`, con
  `PENDING_STALE_AFTER` = **1 hora**. La fundación tiene un plazo de 10 s; una hora es tres órdenes de magnitud
  más, así que ninguna fundación iniciada antes del corte puede seguir en vuelo.

### 2.2 Cómo se resuelve

1. `GET /internal/players/{playerId}` en el Game Server, autenticado con el mismo token de fundación
   (`aud: "game-server-internal"`, `sub` = ese `playerId`, `jti` de un solo uso): `200 { playerId, username,
   city }` si el jugador existe, `404 PLAYER_NOT_FOUND` si no. Sólo lee; no funda.
2. **Existe** → la cuenta se **activa**: el alta sí terminó y sólo se perdió la respuesta. Su dueño puede entrar
   con su contraseña y el nombre sigue siendo suyo.
3. **No existe** → la cuenta se **borra**, con la guarda en la propia sentencia:
   `DELETE … WHERE id = $1 AND status = 'PENDING' AND founding_attempted_at < now() − 1 h`. Un login que haya
   renovado el intento entre la consulta y el borrado lo impide.
4. **Sin respuesta**, o cualquier otra cosa → no se toca nada. «Cualquier otra cosa» incluye un 401 por secretos
   desincronizados, un 5xx, un 404 sin el código `PLAYER_NOT_FOUND` (una ruta que no existe) y un 200 que nombra
   a otro jugador: con duda, la cuenta se queda.

El login hace su parte de la carrera: renueva el intento con
`UPDATE … SET founding_attempted_at = now() WHERE id = $1 AND status = 'PENDING'`, y si no afecta a ninguna fila
vuelve a leer la cuenta: si se activó entretanto, entra; si se borró, responde `UNAUTHORIZED` **sin llamar al
Game Server**. Así ninguna fundación arranca para una cuenta que ya no existe.

### 2.3 Cuándo se resuelve

- **En el alta, al chocar.** Si `POST /api/auth/register` encuentra el nombre ocupado por una cuenta abandonada,
  la resuelve; si la resolución la borra, reintenta la creación **una vez** y sigue con el alta normal. Si la
  activa, o no puede resolverla, responde `USERNAME_TAKEN` como siempre (o `GAME_SERVER_UNAVAILABLE` si el Game
  Server no contestó: entonces el alta tampoco podría fundar).
- **En un barrido explícito.** `pnpm run identity:sweep [límite]` resuelve hasta 500 abandonadas (o el límite
  dado), en orden de antigüedad —y por `id` a igual antigüedad— y de una en una, e informa de cuántas activó,
  borró, dejó porque cambiaron durante la consulta y dejó porque el Game Server no contestó. Termina con código 2
  si hubo alguna de estas últimas, para que un cron lo vea. Pensado para un cron del despliegue o para operación
  manual; no hay temporizador dentro de Next.js. Repetirlo es inocuo: cada pasada sólo toca lo que sigue
  abandonado.

### 2.4 Fuera de esta decisión

- Notificar al dueño de una cuenta borrada: no hay correo ni otro canal.
- Un plazo distinto por entorno: `PENDING_STALE_AFTER` es una constante; si hiciera falta, pasaría a ser
  configuración en otro cambio.

## 3. Alternativas consideradas

### 3.1 Borrar las `PENDING` antiguas sin preguntar

**Ventajas reales:** una sentencia y un cron; no hace falta ningún endpoint nuevo. **Descarte:** borra cuentas
cuyo jugador existe —el caso del 503 con la fundación confirmada—, deja un jugador sin dueño y su nombre
bloqueado en el esquema del juego, que ninguna limpieza de `apps/web` puede liberar. Cambia una deuda pequeña
por una inconsistencia entre almacenes.

### 3.2 Terminar la fundación de las abandonadas

**Ventajas reales:** reutiliza `POST /internal/players`, que ya es idempotente; toda cuenta acaba `ACTIVE`.
**Descarte:** funda en el mundo, con su ciudad, sus aldeanos y su territorio, a jugadores que se fueron antes de
entrar. Ocupa sitio del mapa para nadie, y el nombre sigue igual de bloqueado.

### 3.3 Un temporizador dentro del servidor de Next.js

**Ventajas reales:** limpia sin que nadie lo programe. **Descarte:** en funciones *serverless* no hay proceso de
larga vida; con varias instancias, cada una barrería por su cuenta. La resolución al chocar cubre el daño real,
y el barrido explícito cubre el resto donde el despliegue tenga un cron.

### 3.4 Plazo corto (minutos)

**Ventajas reales:** el nombre se libera antes. **Descarte:** acerca el corte al plazo de una fundación en
vuelo y a los reintentos de un jugador que vuelve tras un fallo; una hora sigue liberando el nombre el mismo día.

## 4. Consecuencias

### 4.1 Positivas

- Un nombre ocupado por un alta que nunca terminó se libera cuando otro lo pide, a partir de una hora.
- Ninguna limpieza deja un jugador sin cuenta: se borra sólo lo que el Game Server confirma que no existe
  ([INV-PLAYER-010](../invariants/player.md#inv-player-010)).
- Las cuentas cuyo alta sí se completó dejan de quedarse `PENDING` para siempre: el barrido las activa.

### 4.2 Negativas

- Un endpoint interno más en el Game Server, y una columna y una migración más en el almacén de identidad.
- El alta que choca con un nombre abandonado paga una ida y vuelta al Game Server.
- El barrido necesita que alguien lo programe; sin cron, sólo se resuelven las que estorban.
- Un dueño que vuelve después de una hora a una cuenta borrada ya no puede entrar: tiene que darse de alta otra
  vez, y su nombre puede haberlo tomado otro.

### 4.3 Neutras

- `PLAYER_NOT_FOUND` (404) es un código del transporte HTTP interno, no del catálogo de 22 del protocolo.

## 5. Estado

Aceptado. Completa la §4.2 de [ADR-018](ADR-018-identity-store-and-ticket-issuer.md), cuyo diseño no cambia.

## 6. Verificación

| Qué | Dónde |
|---|---|
| El endpoint distingue un jugador fundado de uno inexistente, y exige un token interno cuyo `sub` es el consultado | `TestLaConsultaDistingueUnJugadorFundadoDeUnoQueNoExiste`, `TestLaConsultaExigeUnTokenInternoDeSuSub` (`internal/persistence/postgres`, integración) |
| Sólo un `404 PLAYER_NOT_FOUND` es «no existe»; todo lo demás es «no se sabe» | `identity/pending.test.ts` |
| Existe → se activa; no existe → se borra con la guarda; sin respuesta → nada | `identity/pending.test.ts`, `src/server/authService.test.ts` |
| El alta libera un nombre abandonado y reintenta una vez; con un alta reciente, con el jugador existente o con el mundo mudo, no | `src/server/authService.test.ts` |
| El login renueva el intento antes de fundar, y no funda una cuenta borrada | `src/server/authService.test.ts` |
| La guarda del borrado está en la sentencia: ni recientes, ni `ACTIVE`, ni las renovadas durante la consulta; orden del barrido | `src/server/store.integration.test.ts` |

Cada guarda se comprobó con una mutación que la retira: las 17 hicieron fallar algún test.
