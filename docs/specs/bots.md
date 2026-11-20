# Bots: jugadores sin interfaz que juegan por el protocolo

Propuesta para poblar el mundo con **bots**: procesos que se dan de alta, entran y juegan exactamente como un cliente
—un ticket, un WebSocket, `session.hello` y comandos v1—, sin interfaz y sin ninguna ventaja del servidor. Recolectan,
construyen, entrenan, avanzan de era y atacan, y dejan un registro de cada partida con el que medir el equilibrio.

| Campo | Valor |
|---|---|
| Spec ID | `SPEC-BOTS` |
| Estado | Accepted (2026-11-19). Propuesta en `Draft` y aceptada el mismo día con la recomendación de cada pregunta de §10. |
| Milestone | E28 Bots (vigesimoctavo slice después de M7) |
| Canon | §1.3 (el servidor es la única verdad), §9 (presencia y protección), §13 (límites del WebSocket), §18 (métricas), §19 (niveles de test) |
| Depende de | [player.md](player.md), [websocket-protocol.md](websocket-protocol.md), [presence.md](presence.md), [economy-wood.md](economy-wood.md), [economy-stone.md](economy-stone.md), [civilization-goods.md](civilization-goods.md), [castle-age.md](castle-age.md), [technologies.md](technologies.md), [combat-raid.md](combat-raid.md), [combat-pillage.md](combat-pillage.md), [combat-siege.md](combat-siege.md) |
| Decisiones | [ADR-049](../decisions/ADR-049-bots-as-protocol-clients.md) (los bots son clientes externos del protocolo, no una IA dentro del tick). |
| Reemplaza a | — |
| Invariantes | No asigna invariantes de dominio; cita los de la familia `INV-SEC` que un bot no puede saltarse (§8) |

## 1. Objetivo

Hoy el mundo está vacío salvo por las cuentas del humo, y nadie juega una partida entera: el humo recorre cada regla
una vez, con la madera sembrada y los plazos acortados. Así no se puede saber si el juego **se juega**: cuánto se
tarda en llegar a Bronce, a Hierro y a los Castillos; si la madera sobra y la piedra falta; si saquear compensa; si
una civilización va siempre por delante. Tampoco hay nada que mirar en el mapa: ninguna unidad se mueve si no la mueve
una persona.

Los bots resuelven las dos cosas con lo que ya existe. Juegan por el mismo protocolo que el navegador, así que lo que
miden es el juego de verdad, y llenan el mapa de unidades que caminan, recolectan, construyen y pelean: lo que piden
el pase visual (para ver las animaciones) y el despliegue a staging (para tener tráfico).

## 2. Scope

**Entra.**

- Un paquete nuevo del monorepo, `apps/bots`, en TypeScript sobre Node 22, que **no añade dependencias**: el
  `WebSocket` y el `fetch` de Node, `zod` y `@empires-online/protocol`, que ya están, y los módulos puros del cliente
  (`apps/web/src/net` y `apps/web/src/state`) que no tocan el DOM.
- Un comando, `pnpm run bots`, que arranca N bots en un proceso contra una web y un Game Server dados, durante un
  tiempo dado o hasta Ctrl-C, con una semilla (§4.4).
- El alta y la entrada por la web, como el humo (`/api/auth/register`, `/api/auth/login`, `/api/auth/ticket`), con
  la reconexión del cliente del navegador.
- Dos **perfiles** de juego (Q3): el **constructor** (economía, casas, eras, taller, bienes, tecnologías, muralla) y el
  **saqueador** (economía mínima, guerreros, saqueo y asedio a otros bots).
- El **registro de la partida**: un fichero JSONL por ejecución con lo que cada bot hizo y vio, y un resumen al
  terminar (§4.5, Q6).
- La prueba en vivo: los bots jugando contra el servidor de desarrollo, con el navegador abierto mirándolos (§4.6).

**No entra.**

- **Ningún cambio en el Game Server, en la base ni en el protocolo** (Q2). Si un bot necesita algo que el protocolo
  no da, se anota y se decide en otra spec; el bot no lo esquiva.
- Diplomacia, comercio, mercados, clanes, facciones, mensajes, conquista de territorios y arietes: los bots de este
  slice no los usan (Q3). Entran en un slice posterior con datos de éste.
- Bots en producción (Q1) y cualquier marca visible de «esto es un bot» en el juego (Q2).
- Pruebas de carga: siguen aparcadas ([../testing/load-tests.md](../testing/load-tests.md)). Un bot no es un
  generador de carga: juega a ritmo humano (§4.3) y se mide el juego, no el servidor.
- Acelerar el tiempo del mundo para medir más deprisa (Q5).

## 3. Actores

| Actor | Papel |
|---|---|
| Persona que lo lanza | Elige cuántos bots, qué perfiles, contra qué web y servidor, cuánto tiempo y con qué semilla; lee el resumen. |
| Proceso de bots (`apps/bots`) | Un proceso de Node con N bots, cada uno con su cookie, su conexión, su espejo del mundo y su decisor. |
| Web (Next.js) | Da de alta y abre la sesión de cada bot, y le da tickets; con los mismos límites que a cualquiera ([player.md](player.md) §4.1). |
| Game Server | El de siempre: valida y aplica los comandos del bot como los de cualquier jugador. No sabe que es un bot. |
| Navegador | Una persona (o el agente, con el panel visible) mira el mundo con su propia cuenta mientras los bots juegan. |

## 4. Modelo

### 4.1 Un bot es un cliente, no una IA del servidor

Cada bot es, para el servidor, un jugador más: se da de alta con una civilización, tiene su ciudad, su presencia y
sus límites. Decide fuera del servidor, con lo que le llega por el WebSocket, y sólo envía comandos v1. De ahí salen
las tres propiedades que importan para medir:

- **Lo medido es el juego real.** El bot ve lo que ve un jugador (su área de interés, sus avisos) y choca con los
  mismos errores. Un decisor dentro del tick vería todo el mundo y tomaría atajos que ningún jugador tiene.
- **El servidor no cambia.** Ni una regla, ni un determinismo, ni un coste por tick: un bot cuesta lo que un
  jugador conectado.
- **Se puede apuntar a cualquier despliegue**: el de desarrollo hoy, staging cuando exista.

La alternativa descartada —jugadores no humanos dentro de la simulación, con su propio sistema en la fase 4— está en
[ADR-049](../decisions/ADR-049-bots-as-protocol-clients.md) con sus motivos: rompería el pilar 2 en espíritu (estado que nadie pidió por el protocolo), metería
decisiones no deterministas o una `RandomSource` más en el tick, y no mediría la experiencia de un cliente.

### 4.2 Qué tiene cada bot

| Pieza | Qué es | De dónde sale |
|---|---|---|
| Cuenta | `bot_<perfil>_<n>` (p. ej. `bot_constructor_3`), con la contraseña de `EO_BOT_PASSWORD` del `.env` y una civilización por turno (`ROMAN`, `BYZANTINE`, `PERSIAN`, `NORSE`) | El alta de la web, como el humo (Q7) |
| Sesión | La cookie `eo_session` y un ticket nuevo en cada reconexión | `/api/auth/login` y `/api/auth/ticket` |
| Conexión | El `GameClient` de `apps/web/src/net/client.ts` con el `WebSocket` de Node: handshake, latido, reconexión con backoff | Reutilizado tal cual |
| Espejo del mundo | El estado del cliente, aplicado mensaje a mensaje con los reductores puros de `apps/web/src/state` | Reutilizado; lo que dependa de zustand o del DOM se separa sin cambiar su comportamiento |
| Decisor | Una función pura `decidir(espejo, perfil, ahoraMs, aleatorio) → comandos[]` | Nuevo (§4.3) |
| Registro | Una línea JSONL por suceso relevante | Nuevo (§4.5) |

### 4.3 El decisor

Cada bot despierta cada `EO_BOT_THINK_MS` (por defecto 2 000 ms, con un desfase aleatorio por bot para que no
despierten todos a la vez), mira su espejo y envía **como mucho dos comandos** por despertar. El decisor es una lista
de prioridades por perfil: la primera regla que se cumple y es posible manda. «Posible» se decide con los mismos
motivos que usa el HUD para deshabilitar un botón (`apps/web/src/state`), así que el bot no envía lo que el cliente
del navegador no dejaría enviar; si aun así el servidor rechaza, el bot registra el código y no repite esa orden
hasta pasado un enfriamiento.

**Constructor** (en este orden):

1. Aldeanos parados → a recolectar: madera hasta tener la de la siguiente meta, luego piedra.
2. Población llena y madera para una casa → casa en el primer hueco.
3. Menos de `n` aldeanos (8 en Piedra, 12 en Bronce, 16 en Hierro) → entrenar aldeano.
4. Recursos del avance de era → avanzar.
5. En Bronce: taller, luego bien de su civilización; muralla cuando sobre piedra.
6. Tecnologías: la primera común que pueda pagar.
7. Dos guerreros de guardia en casa (la defensa de E3 hace el resto).

**Saqueador:** las reglas 1-3 del constructor con la mitad de aldeanos, y además:

1. Mantener entre 3 y 6 guerreros (o la unidad propia de su civilización desde Bronce).
2. Con 3 o más guerreros parados en casa → elegir la ciudad de **otro bot** más cercana que no esté protegida y
   mandarlos a saquear junto a su muralla; si no hay almacén que saquear (suelo), asediar una casa.
3. Con el botín encima, el guerrero vuelve solo (regla de E5); el bot no hace nada especial.
4. Si pierde más de la mitad de la partida de ataque, espera 5 min antes de la siguiente.

Los números de esta sección son el punto de partida (Q8): viven en un fichero de perfiles, no repartidos por el
código, para poder ajustarlos sin tocar el decisor.

**Ritmo.** Dos comandos cada dos segundos son 1 msg/s por bot de media, frente a los 20 msg/s del límite
(`EO_WS_RATE_LIMIT_PER_SECOND`): el bot nunca toca el rate limiter. Un bot cuyo servidor cierra la conexión por
cualquier motivo reconecta como el navegador.

### 4.4 La ejecución

```
pnpm run bots -- --web http://localhost:3001 --server ws://localhost:8080/ws \
  --constructores 3 --saqueadores 1 --duracion 2h --semilla 42 [--contra-humanos]
```

| Opción | Por defecto | Qué hace |
|---|---|---|
| `--constructores`, `--saqueadores` | 3 y 1 | Cuántos bots de cada perfil; los nombres se numeran desde 1, así que una segunda ejecución reutiliza las mismas cuentas y sigue sus partidas. |
| `--duracion` | sin límite | Para limpiamente al vencer: cierra las conexiones y escribe el resumen. Ctrl-C hace lo mismo. |
| `--semilla` | la hora | Fija el aleatorio del decisor (desfases, desempates, elección de blanco). La partida no es reproducible al milisegundo —el servidor y la red no lo son—, pero dos ejecuciones con la misma semilla toman las mismas decisiones ante el mismo estado. |
| `--contra-humanos` | apagado | Sólo con ella un saqueador puede elegir como blanco una ciudad que no sea de un bot (Q4). |

Las altas van escalonadas para no pisar el límite de alta por IP (`EO_AUTH_RATE_LIMIT_PER_MINUTE`, 10 por minuto con
ráfaga 5): con más de cinco cuentas nuevas, el proceso espera entre altas lo que el límite pida y lo dice. Las entradas
posteriores usan la cookie, que no gasta ese cupo.

`EO_BOT_PASSWORD` y `EO_BOT_THINK_MS` las lee sólo el proceso de bots, como `EO_SMOKE_*` el humo: van en
`.env.example` y en [configuration.md §3.10](../operations/configuration.md#310-variables-que-no-son-configuración-del-servidor),
no en `internal/config`. Sin `EO_BOT_PASSWORD` el proceso se niega a arrancar; nunca hay una contraseña por defecto.

### 4.5 Lo que se mide

Cada ejecución escribe `bots/runs/<fecha>-<semilla>.jsonl` (el directorio va en `.gitignore`), una línea por suceso:

| Suceso | Campos |
|---|---|
| `start` | bot, perfil, civilización, ciudad, era, almacén y unidades al empezar |
| `command` | bot, tipo de comando, `requestId` |
| `rejected` | bot, tipo de comando, código de error |
| `era` | bot, era nueva, ms desde el primer `start` de esa ciudad |
| `stock` | bot, almacén completo, cada 60 s |
| `trained`, `built`, `researched`, `crafted` | bot, qué, plazo real |
| `lost`, `killed`, `pillaged`, `besieged` | bot, contra quién, cuánto (de los avisos de E11 y el registro de E7) |
| `stop` | bot, era, almacén, unidades vivas, tiempo jugado |

Al terminar, el proceso imprime un resumen por bot y por perfil: tiempo hasta cada era, recursos recolectados por
hora, unidades entrenadas y perdidas, saqueos y derribos hechos y sufridos, y los comandos rechazados agrupados por
código. Un rechazo frecuente es un hallazgo: o el decisor se equivoca (y se arregla el bot) o la regla es confusa
(y se anota para producto).

Las cifras **no** se escriben en las docs como resultado del slice: la primera ejecución larga se anota en milestones
como medida de partida, con su semilla y su duración, y las decisiones de equilibrio que salgan de ella van a sus
specs como precisiones.

### 4.6 Cómo se comprueba que se ve

La prueba en vivo del slice tiene dos partes:

1. **Sin navegador:** el servidor y la web de desarrollo, `pnpm run bots -- --duracion 30m` con los plazos acortados
   por entorno (`EO_*_SECONDS`, como en el humo), y el resumen con al menos un avance de era, una casa construida y
   un saqueo entre bots.
2. **Con el panel del navegador visible** (sin él no hay `requestAnimationFrame` y el mapa no se dibuja): una cuenta
   humana del humo mirando la zona de los bots, y capturas de aldeanos recolectando, de una obra y de un saqueo.
   Las ciudades las coloca el servidor, así que la vista se mueve hasta ellas con las coordenadas del registro.

Lo que no se pueda ver en el navegador se anota en milestones como «Sin comprobar en el navegador».

### 4.7 Reglas

| ID | Regla |
|---|---|
| `RN-BOT-001` | Un bot sólo habla con el juego por la web pública y el WebSocket v1, con las credenciales de su cuenta. Nunca usa el listener interno, la base, Redis ni `EO_AUTH_JWT_SECRET`. |
| `RN-BOT-002` | Un bot nunca envía un comando que el HUD tendría deshabilitado con el mismo espejo; si el servidor lo rechaza igualmente, no lo repite antes de su enfriamiento. |
| `RN-BOT-003` | Un bot envía como mucho dos comandos por despertar y despierta como mucho cada `EO_BOT_THINK_MS`. |
| `RN-BOT-004` | Sin `--contra-humanos`, un bot nunca elige como blanco una unidad, una casa o una ciudad de un jugador que no sea bot (nombre fuera de `bot_*`). La defensa automática de E3 sigue actuando sola, porque es del servidor. |
| `RN-BOT-005` | Con la misma semilla y el mismo espejo, el decisor devuelve los mismos comandos. |
| `RN-BOT-006` | El proceso nunca imprime ni escribe en el registro la contraseña, la cookie ni un ticket. |
| `RN-BOT-007` | Al parar (vencida `--duracion` o Ctrl-C), el proceso cierra todas las conexiones y escribe `stop` y el resumen antes de salir. |

### 4.8 Precisiones de la implementación (2026-11-19)

- **Dos conexiones por saqueador.** La de **casa** tiene la vista en la ciudad; la de **campaña** se abre con la primera
  partida y mira al blanco o al tramo de marcha en curso, porque los guerreros que se alejan salen del área de interés
  de la casa. Las órdenes salen siempre por la de casa: el servidor conoce las unidades estén donde estén. Un
  constructor tiene sólo la de casa.
- **La exploración.** Al arrancar, el bot mueve la vista en anillos de 96, 160 y 224 tiles alrededor de su ciudad
  (como `scripts/smoke.mjs --piedra`) y se queda con las 24 colinas más cercanas del terreno visto; los bosques los
  toma del terreno a 40 tiles de la ciudad. El decisor no corre mientras explora.
- **La piedra sin ruta.** Las colinas de desarrollo quedan a 60-200 tiles de las ciudades, y algunas no tienen ruta
  dentro del tope del A\* (`PATH_TOO_LONG`). Un rechazo así aparta la piedra entera durante el enfriamiento de blanco
  (10 min): los aldeanos van a la madera en vez de probar colina tras colina. Lo encontró la primera pasada en vivo,
  en la que un saqueador gastó nueve órdenes seguidas en colinas a 192 tiles.
- **El saqueo.** La partida ordena `unit.pillage` directamente, y el servidor lleva a cada guerrero al anillo. Si
  responde `PATH_TOO_LONG` o `PATH_NOT_FOUND`, la partida marcha en tramos de 48 tiles hasta quedar a 12 del blanco y
  vuelve a ordenar el saqueo; un tramo que no se puede pisar se desplaza en espiral. El botín sólo se deposita en el
  tile de depósito de la ciudad (`RN-ECON-004`), así que un guerrero cargado vuelve con `unit.move` a ese tile, el
  primero transitable del anillo de nacimiento, en el orden de `city.SpawnTilesAround`. Si el saqueo acaba en el
  suelo del almacén, el guerrero derriba una casa visible de la víctima; con la muralla en pie el servidor lo rechaza
  (`INVALID_TARGET`) y vuelve a casa. Un blanco protegido, sin ruta o con tratado (`CITY_PROTECTED`,
  `TREATY_REQUIRED`, `INVALID_TARGET` al saquear) se enfría y la partida vuelve. Las bajas se cuentan con
  `entity.despawn` de motivo `DEAD` (o `entity.update` con `DEAD`), que dicen qué unidad murió; el aviso `UNIT_LOST`
  no lo dice.
- **Lo recolectado** del resumen es la suma de las subidas de madera y piedra que traen los `city.update` de la ciudad:
  recolección, botín y devoluciones de encargos. Un `world.snapshot` reinicia la referencia sin contar, porque su
  almacén puede venir de fuera de la partida (un `dev:seed` con el servidor parado, una reconexión); la primera pasada
  en vivo lo contaba y daba 2 269 de madera por hora a un constructor de cuatro minutos.
- **Lo construido.** `building.update` sólo trae `id` y `status`: el bot lee la ciudad y el tipo del espejo y apunta
  `built` en la transición a `COMPLETE`, una vez; lo que ya está en pie al conectar no cuenta.
- **Quién es un bot** (`RN-BOT-004`): una ciudad cuyo nombre empieza por `bot_`, porque el servidor nombra cada ciudad
  con el usuario más `polis`. El directorio del proceso sólo da la ubicación de las ciudades de sus bots.
- **La Edad de Hierro pide 60 bienes ajenos**, que sólo se consiguen comerciando, y los bots no comercian (Q3): un
  constructor se queda en Bronce. Es un dato de equilibrio, no un fallo del bot.
- **Opciones de más.** `EO_BOT_WEB` y `EO_BOT_SERVER` dan los valores por defecto de `--web` y `--server`; del `.env`
  de la raíz sólo se leen las `EO_BOT_*`; `EO_BOT_PASSWORD` pide al menos 12 caracteres.
- **El empaquetado.** `tsx` carga los `.ts` de `apps/web` como CommonJS, y `@empires-online/protocol` sólo exportaba
  la condición `import`: el paquete exporta ahora también `default` (el mismo fichero ESM, que Node 22 carga con
  `require`). El importador de `apps/bots` se añadió al lockfile con las versiones ya resueltas de los demás paquetes,
  sin resolver nada en la red.

## 5. Errores

No hay códigos nuevos. El bot recibe los del canon §16 como cualquier cliente y los trata así:

| Código | Qué hace el bot |
|---|---|
| `RATE_LIMITED`, o el cierre 4500 por cola de salida llena | Lo registra como fallo del bot (no debería pasar, §4.3) y reconecta con backoff. |
| `UNAUTHORIZED`, `FORBIDDEN` al pedir ticket | Vuelve a entrar con la contraseña una vez; si falla, el bot se para y lo dice en el resumen. |
| `INSUFFICIENT_RESOURCES`, `INVALID_TARGET`, `POPULATION_LIMIT_REACHED`, `CITY_PROTECTED`, `NOT_IMPLEMENTED` y el resto de rechazos de una orden | `rejected` en el registro y enfriamiento de esa orden (`RN-BOT-002`). |
| 429 de la web en el alta | Espera lo que pida el límite y reintenta (§4.4). |

## 6. Eventos y contratos de red

Ninguno nuevo. El bot consume los mensajes servidor→cliente de [websocket-protocol.md](websocket-protocol.md) §8 y
envía los cliente→servidor de §7 que usa el HUD: `unit.move`, `unit.gather`, `unit.attack`, `unit.pillage`,
`unit.demolish`, `city.build`, `city.train`, `city.advance`, `city.craft`, `city.research` y los de sesión. Valida
lo que recibe con los esquemas Zod de `@empires-online/protocol`, igual que el navegador: un mensaje que no valida es
un fallo de contrato y se registra.

## 7. Persistencia

| Pregunta | Respuesta |
|---|---|
| ¿Qué es autoritativo en RAM del game server? | Lo de siempre: la ciudad, las unidades y el almacén de cada bot, como los de cualquier jugador. |
| ¿Qué se persiste inmediatamente? | Lo de siempre. El alta del bot pasa por el bootstrap atómico de [player.md](player.md). |
| ¿Qué se persiste eventualmente? | Lo de siempre. |
| ¿Qué es reconstruible y no se persiste? | Todo el estado del proceso de bots: el espejo se rehace del snapshot al reconectar, y el decisor no guarda nada entre despertares salvo los enfriamientos, que se pierden al reiniciar sin consecuencias. El registro JSONL es un fichero local de medida, no estado del juego. |

## 8. Invariantes

No asigna invariantes de dominio: el servidor no cambia y ninguna de sus propiedades se mueve. Las reglas del bot
(§4.7) se verifican con tests del propio paquete (§9).

Cita, porque un bot es justo el tipo de cliente que podría intentar saltárselos y el slice lo comprueba en la prueba
en vivo: [INV-SEC-001](../invariants/security.md#inv-sec-001) (el estado del cliente nunca muta el autoritativo),
[INV-SEC-002](../invariants/security.md#inv-sec-002) (nada antes de autenticarse),
[INV-SEC-004](../invariants/security.md#inv-sec-004) (ownership en cada comando) e
[INV-SEC-007](../invariants/security.md#inv-sec-007) (un `requestId` repetido no repite el efecto: el bot reintenta con
el mismo).

## 9. Tests esperados

| Nivel | Qué |
|---|---|
| Unit (Vitest, `apps/bots`) | El decisor de cada perfil sobre espejos fijos: cada regla de §4.3 en su orden; el tope de dos comandos (`RN-BOT-003`); que no envía lo deshabilitado (`RN-BOT-002`) usando los mismos motivos que el HUD; el enfriamiento tras un rechazo; la elección de blanco sin `--contra-humanos` y con ella (`RN-BOT-004`); la misma semilla y el mismo espejo dan los mismos comandos (`RN-BOT-005`). |
| Unit (Vitest) | El registro: una línea por suceso con sus campos; el resumen a partir de un JSONL fijo; que ninguna línea contiene la contraseña, la cookie o un ticket (`RN-BOT-006`). |
| Unit (Vitest) | Las opciones de la línea de órdenes y el escalonado de altas frente al límite. |
| Contract | Lo cubre el cliente reutilizado: el bot valida con Zod lo que recibe. Si para reutilizar `net` o `state` hay que separar algo de zustand o del DOM, los tests de `apps/web/src/state` siguen pasando sin cambios. |
| Integración / e2e | Ninguno nuevo en Go: el servidor no cambia. La prueba en vivo de §4.6 (servidor, web y bots) es la que demuestra el conjunto; su resumen se anota en milestones. |
| Parada limpia | Con un servidor falso en proceso (`socketFactory` del `GameClient`): al pedir la parada se cierran las conexiones y se escribe `stop` (`RN-BOT-007`). |

`pnpm run verify:full` corre los tests del paquete nuevo con los demás del cliente.

**Precisión (2026-11-19).** La CI recorre el workspace en el typecheck (`pnpm run typecheck` llega a `apps/bots` y, con
él, a los módulos de `apps/web` que importa), pero sus tests los lanza paquete a paquete. Con el visto bueno de producto
del mismo día, el job de TypeScript suma el paso «Tests de los bots» (`pnpm --filter @empires-online/bots run test`),
detrás de los del cliente; `scripts/verify.sh` los corre en su paso `bots-test`.

## 10. Decisiones de producto (2026-11-19)

Producto aceptó el 2026-11-19 la recomendación de cada pregunta. Cada fila deja la decisión (en negrita) y las
alternativas que se descartaron. Son decisiones de producto, no de ingeniería: cambiarlas es cambiar la spec.

| # | Pregunta | Decisión |
|---|---|---|
| Q1 | ¿Dónde pueden jugar los bots? | **En desarrollo y, cuando exista, en staging; nunca en producción en este slice.** Sirven para medir y para ver el mundo vivo, y en producción jugarían contra personas sin que lo sepan. Alternativas: **también en producción** como relleno de un mundo vacío, que pide antes la marca de Q2 y decidir si pueden atacar a personas; **sólo en desarrollo**, que deja staging sin tráfico. |
| Q2 | ¿Se distinguen los bots en el juego? | **No en este slice: sólo por el nombre `bot_*`, sin cambios en el servidor ni en el protocolo.** Mientras sólo jueguen en desarrollo y staging, la convención basta, y el slice no toca ni la base ni Zod. Alternativas: **una columna `players.is_bot`** (migración `000030`) que el servidor fija en el alta por una vía propia y que el cliente pinta con una marca, necesaria si van a producción; **reservar el prefijo** `bot_` para que ninguna persona pueda registrarlo, un cambio en la web pequeño pero que es una regla de alta nueva. |
| Q3 | ¿Qué juegan los bots? | **La economía, las eras, el taller, las tecnologías, la muralla y el combate básico (guerreros, saqueo y asedio de casas), con dos perfiles: constructor y saqueador.** Es lo que da las medidas que más importan (ritmo de las eras, valor del saqueo) con el decisor más pequeño. Alternativas: **sólo economía**, un solo perfil, más rápido de hacer pero sin nada sobre el combate; **todo lo que el protocolo permite** (diplomacia, comercio, mercados, clanes, conquista, arietes), con un decisor mucho mayor y medidas difíciles de leer en la primera ejecución. |
| Q4 | ¿A quién atacan los saqueadores? | **Sólo a otros bots, salvo con `--contra-humanos`.** Así se puede dejar a los bots jugando en el mundo de desarrollo sin que arrasen las cuentas del humo ni la de quien está probando. Alternativas: **a cualquiera** que no esté protegido, más realista pero imprevisible para quien juega a la vez; **a nadie**, que deja el combate sin medir. |
| Q5 | ¿A qué velocidad se juega? | **A la del mundo real, con el servidor sin cambios; los plazos se acortan por entorno sólo en la prueba en vivo.** Medir el equilibrio a velocidad acelerada mide otra cosa (los viajes y las regeneraciones no se acortan igual que las obras). Las ejecuciones largas corren en segundo plano durante horas. Alternativas: **un multiplicador global de tiempo** en el servidor (`EO_TIME_SCALE`), que pide un ADR, tocar todos los plazos y su determinismo; **siempre con plazos acortados**, que da números rápidos pero no comparables con una partida real. |
| Q6 | ¿Qué se mide y dónde queda? | **Un JSONL por ejecución en un directorio sin versionar y un resumen impreso al terminar (§4.5); la primera ejecución larga se anota en milestones como medida de partida.** Sin tablas ni series nuevas. Alternativas: **métricas Prometheus** en el proceso de bots, útiles en staging pero sin nada que las lea hoy; **una tabla de partidas** en la base, que es estado nuevo para datos de medida. |
| Q7 | ¿Cómo se dan de alta? | **Por la web, como el humo, con nombres `bot_<perfil>_<n>`, la contraseña de `EO_BOT_PASSWORD` en el `.env` (nunca en el repositorio) y una civilización por turno.** Reutilizar las cuentas entre ejecuciones deja partidas largas, de días. Alternativas: **cuentas nuevas en cada ejecución** (con un sufijo), que mide siempre desde cero pero llena el mundo de ciudades abandonadas; **sembrarlas con `dev:seed`**, que se salta el alta y no sirve en staging. |
| Q8 | ¿Los números del decisor (§4.3) son el punto de partida? | **Sí, ajustables en el fichero de perfiles sin cambiar el modelo**: despertar cada 2 s, dos comandos por despertar, 8/12/16 aldeanos por era, 3-6 guerreros en el saqueador y 5 min de espera tras una derrota. Alternativa: proponer otros antes de aceptar. |
| Q9 | ¿Cuántos bots por defecto y hasta cuántos? | **Cuatro por defecto (tres constructores y un saqueador) y hasta 20 por proceso.** Cuatro ciudades bastan para ver saqueos y comparar civilizaciones; 20 es la mitad de lo que el límite global de altas (120 por minuto) y el portátil aguantan con holgura. Alternativas: **un constructor y un saqueador por civilización** (ocho), que compara mejor las civilizaciones pero dobla el ruido en el mapa de desarrollo; **sin tope**, que acaba en una prueba de carga encubierta. |

## 11. Documentos relacionados

- [player.md](player.md) — el alta, la sesión web y los límites por IP y por usuario.
- [websocket-protocol.md](websocket-protocol.md) — los mensajes que el bot envía y recibe.
- [presence.md](presence.md) — un bot conectado está `ONLINE` y nunca protegido; desconectado, pasa a `PROTECTED` como
  cualquiera.
- [../architecture/frontend.md](../architecture/frontend.md) — el cliente cuyos módulos de red y de estado reutiliza.
- [../testing/load-tests.md](../testing/load-tests.md) — lo que este slice no es.
- [../product/game-pillars.md](../product/game-pillars.md) — el pilar 2 que obliga a que el bot sea un cliente.
