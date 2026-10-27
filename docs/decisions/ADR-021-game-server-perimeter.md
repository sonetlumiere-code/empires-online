# ADR-021: El perímetro del Game Server: `Origin` obligatorio, margen de reloj del ticket, topes de conexión y listener interno

Propósito: cerrar los cuatro hallazgos de perímetro de la auditoría del 2026-10-26 —comprobación de `Origin`
inerte, ticket sin margen de reloj, conexiones sin tope y `/internal` en el puerto público— con decisiones
que el código valida al arrancar y la suite cubre, en vez de con casillas de un checklist.

| Campo | Valor |
|---|---|
| **Estado** | **Aceptado** |
| **Fecha** | **2026-10-26** |
| Ámbito | `internal/config`, `internal/websocket`, `internal/auth`, `internal/observability`, `cmd/server`, `infra/docker`, `.env.example`, `docs/operations` |
| Índice | [README.md](README.md) |
| Relacionados | [ADR-010](ADR-010-authentication-game-ticket.md) (fija el *leeway* que dejó TBD), [ADR-018](ADR-018-identity-store-and-ticket-issuer.md) (el listener de `/internal/players`), [ADR-019](ADR-019-handshake-failure-close-code.md), [ADR-020](ADR-020-stale-pending-accounts.md) |

---

> **Precisión (2026-10-27).** El punto 3 de la decisión tomaba la **primera** IP de `X-Forwarded-For` «por
> coherencia con `apps/web`», y la alternativa «la última IP» se descartó por el mismo motivo. Ese mismo día
> `apps/web` pasó a tomar el **N-ésimo elemento por la derecha**, con `EO_TRUSTED_PROXY_COUNT` (1–10, por
> defecto 1) como número de proxies de confianza, precisamente porque el primero lo escribe quien quiera. El
> argumento de la decisión —«dos criterios para la misma variable serían un error esperando a ocurrir»— obliga
> entonces al Game Server a seguirla: desde esta fecha lee `EO_TRUSTED_PROXY_COUNT`, toma ese elemento, usa
> `X-Real-IP` sólo a falta de `X-Forwarded-For` y, si la lista es más corta que N, cuenta por la IP del socket
> (`internal/websocket/limits.go`, `clientIP`/`forwardedClientIP`; cubierto por
> `TestLaIPDelClienteSeLeeComoEnAppsWeb` y `TestLaIPDeXForwardedForSoloCuentaConProxyDeConfianza`). El
> despliegue ya no exige que el proxy sobrescriba la cabecera: con N correcto, añadir también vale. Nada más de
> esta decisión cambia; el texto original se conserva tal cual más abajo.

## Contexto

El Game Server expone cuatro superficies: el upgrade de WebSocket en `/ws`, los endpoints de salud, la API
interna de fundación (`POST /internal/players`, `GET /internal/players/{id}`) y las métricas. Hoy las tres
primeras comparten un único `http.Server` en `EO_HTTP_ADDR`, y el Caddyfile de referencia de
[../operations/deployment.md](../operations/deployment.md) proxya a ese puerto todo lo que no es `/ws`,
`/health` ni `/ready`.

Una auditoría independiente del 2026-10-26 señaló, con severidad alta o media, lo siguiente:

- **La comprobación de `Origin` está apagada.** `server.go` tiene un `checkOrigin` contra `AllowedOrigins`,
  pero nada rellena esa lista: `cmd/server` no la pasa y `internal/config` no tiene variable. Con la lista
  vacía se acepta todo, en todos los entornos. Dos documentos de operación daban la comprobación por
  existente, y una mutación que la anula sobrevivió a la suite entera: ningún test la ejercitaba. Un
  navegador envía `Origin` en todo upgrade y no deja al script cambiarla; es la única señal que distingue a
  la web del juego de cualquier otra página abierta en el mismo navegador. `apps/web` sí comprueba el origen
  en `/api/auth/*`, así que una página ajena no obtiene un ticket por la vía normal; lo que queda abierto es
  todo lo demás: un ticket filtrado o robado por XSS canjeable desde cualquier página, y el propio `/ws` como
  destino de sondeos y aperturas de socket desde cualquier sitio que el jugador visite.
- **El ticket se verifica sin margen de reloj.** `auth.Verifier` exige `iat` y `exp` exactos
  (`jwt.WithIssuedAt`, sin `jwt.WithLeeway`). El emisor es `apps/web`, en otra máquina, y trunca `iat` a
  segundos (`apps/web/identity/tokens.mjs`): basta con que su reloj vaya un segundo por delante para que todo
  ticket llegue «emitido en el futuro» y se rechace con `4401`, el mismo código que un ticket falso
  ([ADR-019](ADR-019-handshake-failure-close-code.md)). [ADR-010](ADR-010-authentication-game-ticket.md)
  dejó el margen como «TBD (fuera de MVP)». La clave anti-replay `ticket:jti:{jti}` dura 120 s, el doble del
  TTL de 60 s, precisamente para absorber un margen que no existía.
- **No hay tope de conexiones.** Ni global, ni de sockets en handshake, ni por IP. El plazo de handshake de
  5 s acota cuánto dura un socket mudo, no cuántos puede haber a la vez: abrir sockets y callar agota
  descriptores y goroutines a razón de lo que la red permita. El `http.Server` sólo tiene
  `ReadHeaderTimeout`: una petición que no termina de llegar ocupa su conexión sin límite.
- **`/internal` está en el puerto público.** La fundación y la consulta de jugadores comparten mux y puerto
  con `/ws`, y el proxy de referencia las publica. Desde Internet son alcanzables con sólo el secreto
  compartido de [ADR-018](ADR-018-identity-store-and-ticket-issuer.md) §3.6, que firma también el ticket de
  todos los jugadores: una superficie que sólo debería ver `apps/web` tiene la misma exposición que el juego.

Además, `EO_ENV` admite texto libre: `prod`, `Production` o un error de tecleo activan en silencio la
semántica de desarrollo (migración automática, estado caliente en proceso, secreto de ejemplo admitido),
y cualquier validación «sólo en producción» que se añada hereda ese agujero.

Restricciones del entorno: `scripts/smoke.mjs`, `curl` y cualquier cliente que no sea un navegador no envían
`Origin`. `apps/web` corre hoy en Vercel, cuyas IP de salida no son fijas salvo opción de pago, y llama a
`/internal` por HTTPS a través del proxy. Detrás del proxy de referencia todas las conexiones llegan al
servidor desde `127.0.0.1`. `apps/web` ya lee `EO_TRUST_PROXY_HEADERS` y toma la **primera** IP de
`X-Forwarded-For` para su límite de alta y login. Y el proyecto exige que una regla de seguridad viva en
`internal/config` o en el código y tenga test; un checklist manual no cuenta.

## Decisión

El perímetro del Game Server queda así:

1. **`Origin` contra una lista exacta, obligatoria en producción.** `EO_WS_ALLOWED_ORIGINS` es una lista
   separada por comas de orígenes `scheme://host[:port]` con `http` o `https` y sin ruta, normalizada a
   minúsculas en `internal/config`; una entrada con ruta, sin esquema o con `ws://` aborta el arranque. Con
   `EO_ENV=production` no puede estar vacía. El upgrade compara la cabecera `Origin` con la lista sin
   distinguir mayúsculas y **responde `403` antes del handshake** a un origen no listado: no se abre socket.
   **Una petición sin `Origin` se admite siempre**, haya o no lista: no viene de un navegador, y el riesgo
   cross-site sólo existe en navegadores, que la envían siempre. Con la lista vacía, posible sólo fuera de
   producción, se admite cualquier origen. No hay comodines.
2. **30 s de margen de reloj en el ticket**, como constante `auth.ClockSkew` aplicada con `jwt.WithLeeway` a
   `iat` y `exp`, para el ticket de juego y el token de fundación por igual. Es una constante y no una
   variable `EO_*`: forma parte del contrato del ticket, como su TTL de 60 s, que también es una constante
   (en `apps/web`), y la vida efectiva máxima —TTL más margen, 90 s— queda por debajo de los 120 s de la
   clave anti-replay de [INV-SEC-003](../invariants/security.md#inv-sec-003) sin que nadie tenga que
   validar la relación entre dos variables. Cierra el «TBD» de ADR-010.
3. **Topes de conexiones en el servidor WebSocket, antes del upgrade.** `EO_WS_MAX_CONNECTIONS` (5000) y
   `EO_WS_MAX_CONNECTIONS_PER_IP` (32), con 0 como «sin tope». La plaza se reserva **antes** de `Upgrade` y
   se libera cuando la goroutine de la conexión termina, así que un socket que nunca envía `session.hello`
   también cuenta. Por encima, el upgrade responde **`503 Service Unavailable`** (servidor lleno) o
   **`429 Too Many Requests`** (esa IP) con `Retry-After: 5`, sin abrir socket. La IP es la del socket; sólo
   con `EO_TRUST_PROXY_HEADERS=true` —la misma variable, con el mismo significado, que lee `apps/web`— se
   toma la primera de `X-Forwarded-For`, y el despliegue exige entonces un proxy que **sobrescriba** la
   cabecera. En producción, con tope por IP y sin esa variable, el arranque lo avisa en `warn`. Los
   `http.Server` llevan además `ReadTimeout` (15 s) e `IdleTimeout` (60 s) junto al `ReadHeaderTimeout`
   (10 s) que ya tenían; gorilla retira esos plazos del socket al hacer el upgrade, así que no afectan a un
   WebSocket establecido. Nueva métrica `eo_ws_connections_rejected_total{reason}` con el catálogo cerrado
   `origin`, `max_connections` y `max_connections_per_ip`, con las tres series a cero desde el arranque. El
   contador vive en `server.go`, no en el `Hub`: el `Hub` sólo conoce sesiones autenticadas.
4. **La API interna escucha en su propio `http.Server`**, en `EO_INTERNAL_ADDR`, por defecto
   `127.0.0.1:8081`: sólo loopback. En él viven `POST /internal/players` y `GET /internal/players/{id}`, y
   nada más; `/health` y `/ready` siguen únicamente en el listener público, que es donde los sondea el host.
   El proxy de referencia no publica el listener interno: desde `game.<dominio>`, `/internal/*` responde
   `404` del proxy. `internal/config` exige que `EO_HTTP_ADDR`, `EO_METRICS_ADDR` y `EO_INTERNAL_ADDR` sean
   direcciones `host:port` válidas con **puertos distintos**. Cómo llega `apps/web` hasta el listener
   depende de dónde corra, y lo fija [../operations/deployment.md](../operations/deployment.md) §4.4: por
   loopback si comparte máquina (el objetivo), por red privada si comparte red, y, mientras siga en Vercel,
   por un **segundo sitio** del proxy dedicado a `/internal/*` y restringido por IP de salida cuando sea
   posible.
5. **`EO_ENV` es una lista cerrada**: `development`, `test` o `production`, tal cual. Otro valor aborta el
   arranque con un mensaje que enumera los tres.

Queda fuera: comodines en la lista de orígenes, agregación de IPv6 por `/64` en el tope por IP, un
*token bucket* durante el handshake (sigue TBD en
[INV-SEC-002](../invariants/security.md#inv-sec-002)), mTLS o un segundo secreto para `/internal`, y cambiar
el valor por defecto de `EO_GAME_SERVER_INTERNAL_URL` en `apps/web/src/server/config.ts`, que sigue
diciendo `8080` y debe pasar a `8081` en `apps/web`.

## Alternativas consideradas

### Sobre `Origin`

**Dejar la comprobación en el proxy** (un *matcher* `header Origin` en Caddy). Ventaja real: ni una línea
de Go, y el proxy ya es quien termina TLS. Descartada porque la protección viviría en un fichero de
configuración que ningún test ejecuta —exactamente la forma en que esta comprobación llevaba apagada desde
el primer día—, el servidor no se defendería solo (en desarrollo no hay proxy; en producción, una
reescritura del Caddyfile la perdería sin que nada lo dijera), y el proyecto exige que las reglas de
seguridad tengan test.

**Rechazar también las peticiones sin `Origin`.** Ventaja real: es la política más estricta y la más fácil
de explicar. Descartada porque rompe `scripts/smoke.mjs`, `curl` y cualquier cliente de servidor sin ganar
nada: un cliente que no es navegador no tiene credenciales ambientales de nadie, y puede poner la cabecera
que quiera, así que exigirla sólo filtra a los honestos.

**Comodines para los previews de Vercel** (`*.vercel.app`). Ventaja real: los previews por commit, de URL
dinámica, funcionarían sin tocar la lista. Descartada porque un comodín sobre un dominio compartido admite
el despliegue de cualquier otro inquilino de la plataforma; en su lugar, staging lista el alias estable de
rama. Un sufijo sobre dominio propio podría reconsiderarse si hiciera falta.

### Sobre el margen de reloj

**Una variable `EO_AUTH_CLOCK_SKEW_SECONDS`** con 30 por defecto. Ventaja real: ajustable por entorno sin
recompilar, y coherente con que todo pase por `internal/config`. Descartada porque es el tipo de valor que
se sube para «arreglar» un reloj desincronizado en lugar de arreglar el NTP, porque el TTL con el que se
suma ya es una constante en el otro proceso, y porque obligaría a validar al arrancar que TTL más margen no
supere los 120 s de la clave anti-replay, una relación entre tres números en dos procesos. Una constante la
fija de una vez.

**Margen sólo en `iat`, `exp` estricto.** Ventaja real: nunca alarga la validez de un ticket. Descartada
porque un reloj del servidor por delante del emisor rechazaría igualmente los tickets al final de su vida, y
la asimetría no compra nada mientras la clave anti-replay cubra la vida efectiva, que la cubre.

**Dejar de validar `iat`** (quitar `jwt.WithIssuedAt`). Ventaja real: la más simple, y es la validación de
menor valor —quien tiene el secreto puede poner la fecha que quiera—. Descartada porque el margen resuelve
el problema sin renunciar a rechazar un token datado en el futuro, que es una señal barata de error de
configuración.

### Sobre los topes de conexiones

**Contar en el `Hub`.** Ventaja real: el `Hub` ya cuenta sesiones y alimenta `eo_connected_websockets`.
Descartada porque el `Hub` sólo conoce sesiones autenticadas y el coste a acotar está **antes** del
handshake; además, otra sesión de trabajo modifica `hub.go` ahora mismo.

**Dejarlo al proxy y al sistema** (`ulimit`, límites de Caddy). Ventaja real: nada que programar. Descartada
como único mecanismo: el proxy abre una conexión al servidor por cada cliente y no sabe cuáles están en
handshake, y agotar `ulimit` falla en `accept` sin un código que el cliente o el operador puedan leer. Se
conserva como complemento: el despliegue sube `nofile` por encima de `EO_WS_MAX_CONNECTIONS`.

**La última IP de `X-Forwarded-For` en vez de la primera.** Ventaja real: es robusta ante un proxy que
*añade* la cabecera a lo que trajo el cliente, donde la primera es falsificable. Descartada por coherencia
con `apps/web`, que ya toma la primera: con un único proxy que **sobrescribe** la cabecera —Caddy por
defecto; Nginx con `$remote_addr`— las dos coinciden, y el despliegue exige esa forma en los dos procesos.
Dos criterios para la misma variable serían un error esperando a ocurrir.

**Un solo código HTTP (`503`) para los dos topes.** Ventaja real: un caso menos que documentar. Descartada
porque el operador, que sólo ve los logs del proxy, no podría distinguir «el servidor está lleno» de «esa IP
se pasa», y la distinción sale gratis.

### Sobre el listener interno

**Un solo puerto y filtrar `/internal/*` en el proxy.** Ventaja real: cero código. Descartada por lo mismo
que la primera alternativa de `Origin`: la protección viviría en un fichero sin test —hoy el fichero decía lo
contrario—, y cualquier host que alcance el puerto 8080 (otro contenedor, un `ports` sin `127.0.0.1`)
tendría la API interna igualmente.

**mTLS o un segundo secreto para `/internal`.** Ventaja real: autenticación más fuerte, y rotable aparte
del ticket. Descartada en este ADR porque no cambia la exposición —seguiría siendo alcanzable— y
[ADR-018](ADR-018-identity-store-and-ticket-issuer.md) §3.6 decidió un único secreto; puede añadirse encima
del listener propio cuando haga falta, sin deshacer nada.

**Servir también `/health` y `/ready` en el listener interno.** Ventaja real: sondear la salud sin tocar el
puerto público. Descartada por ahora: dos sitios donde leer la *readiness* son dos verdades, y el
`HEALTHCHECK` de la imagen y los pasos de despliegue ya la leen en 8080.

**`:8081` como valor por defecto**, en todas las interfaces. Ventaja real: funciona de serie entre
contenedores. Descartada porque el valor por defecto de un listener que no debe publicarse tiene que ser el
cerrado; exponerlo a una red privada es una decisión explícita del despliegue.

### Sobre `EO_ENV`

**Normalizar** (`Production` → `production`, `prod` → `production`). Ventaja real: más amable. Descartada
porque adivinar sobre el único valor que cambia la semántica de seguridad es justo lo que el proyecto
prohíbe: ante una configuración dudosa, el proceso no arranca.

## Consecuencias

### Positivas

- Un despliegue de producción **no arranca** sin `EO_WS_ALLOWED_ORIGINS`, y un origen ajeno recibe `403`
  antes de abrir socket. La mutación que anulaba la comprobación hace fallar la suite.
- Un segundo de desfase entre `apps/web` y el Game Server ya no rechaza todos los tickets; hasta 30 s pasan
  desapercibidos y de 30 s en adelante el síntoma es el mismo `4401` de antes, documentado en el runbook.
- El número de sockets está acotado, con `503`/`429` limpios, `Retry-After` y una métrica por motivo; un
  socket que no termina de enviar la petición ya no ocupa su conexión indefinidamente.
- `/internal/players` deja de ser alcanzable por el host público; el token de fundación es la segunda
  barrera. Y tres listeners con el mismo puerto fallan al arrancar, no al abrir el segundo.
- `prod` ya no es un entorno de desarrollo.

### Negativas

- **Cada despliegue de producción tiene dos variables nuevas que no puede olvidar**: `EO_WS_ALLOWED_ORIGINS`
  (sin ella no arranca) y `EO_TRUST_PROXY_HEADERS=true` detrás del proxy (sin ella el tope por IP de 32
  sería el tope del servidor entero; el arranque sólo avisa).
- **Todo `.env` anterior que apunte `EO_GAME_SERVER_INTERNAL_URL` a `8080` rompe el alta** con
  `503 GAME_SERVER_UNAVAILABLE` hasta que se cambie a `8081`. El valor por defecto de `apps/web` sigue en
  `8080` y queda pendiente en ese paquete.
- La vida efectiva de un ticket pasa de 60 s a 90 s. La clave anti-replay lo cubre; la ventana en la que un
  ticket robado y no canjeado sirve es un 50 % más larga.
- El tope por IP penaliza a quienes comparten IP: una oficina, una universidad o un operador con CGNAT
  ven `429` a partir del jugador 33. El valor se sube o se pone a 0 a la vista de los logs; no hay
  heurística.
- Los previews de Vercel necesitan un alias estable de rama listado en staging; un preview por commit no
  entra.
- Mientras `apps/web` siga en Vercel sin IP de salida fijas, el segundo sitio del proxy queda abierto a
  Internet con el token como única barrera: la exposición de `/internal` **se mueve y se puede acotar**,
  pero no desaparece hasta que `apps/web` comparta máquina o red privada con el Game Server.
- Tres listeners son tres puertos que documentar, publicar o no, y vigilar en el firewall.
- El tope por IP es por IP exacta; un atacante con un `/64` de IPv6 dispone de tantas IP como quiera.

### Neutras

- Cinco variables `EO_*` nuevas para el Game Server (`EO_WS_ALLOWED_ORIGINS`, `EO_WS_MAX_CONNECTIONS`,
  `EO_WS_MAX_CONNECTIONS_PER_IP`, `EO_INTERNAL_ADDR`, `EO_TRUST_PROXY_HEADERS`, esta última compartida con
  `apps/web`), dos validaciones cruzadas más (ocho en total) y tres constantes de plazo en `cmd/server`.
- Una métrica más, la decimoquinta: `eo_ws_connections_rejected_total{reason}`.
- `auth.ClockSkew` entra en el contrato del ticket junto al TTL; el fixture compartido
  `internal/auth/testdata/web-tokens.json` no cambia, pero el test que lo verifica fija ahora que a los 61 s
  todavía vale y a los 91 s no.
- `scripts/smoke.mjs` no cambia: no envía `Origin` y no usa `/internal`.
- El `Dockerfile` declara `EXPOSE 8081`; el `docker-compose.yml` del VPS no lo publica.
- Las fichas [INV-SEC-002](../invariants/security.md#inv-sec-002),
  [INV-SEC-003](../invariants/security.md#inv-sec-003), [INV-SEC-005](../invariants/security.md#inv-sec-005)
  e [INV-SEC-008](../invariants/security.md#inv-sec-008) recogen las garantías nuevas y sus tests; el
  modelo de amenazas gana dos filas.

## Estado

Aceptado.
