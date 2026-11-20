# ADR-049: Los bots son clientes externos del protocolo, no jugadores dentro de la simulación

Propósito: fijar dónde viven y cómo deciden los bots del slice E28 ([SPEC-BOTS](../specs/bots.md)): fuera del Game
Server, como un cliente más del WebSocket v1, reutilizando la red y el estado del cliente web, con un decisor puro y
una semilla, para que lo que miden sea el juego que juega una persona y el servidor no cambie.

| Campo | Valor |
|---|---|
| **Estado** | **Aceptado** |
| **Fecha** | **2026-11-19** |
| Ámbito | `apps/bots` (nuevo), `apps/web/src/net` y `apps/web/src/state` (reutilizados), `.env.example`, [configuration.md](../operations/configuration.md) §3.10 |
| Índice | [README.md](README.md) |
| Relacionados | [ADR-002](ADR-002-authoritative-server.md) (servidor autoritativo), [ADR-006](ADR-006-websocket-protocol.md) (el protocolo), [ADR-009](ADR-009-shared-protocol-package.md) (el paquete compartido del protocolo), [ADR-018](ADR-018-identity-store-and-ticket-issuer.md) (el alta, la sesión web y el ticket) |

---

## Contexto

Producto aceptó el 2026-11-19 las nueve recomendaciones de la spec: bots en desarrollo y en staging, nunca en
producción; sin marca en el juego más que el nombre `bot_*`; dos perfiles (constructor y saqueador) que juegan la
economía, las eras, el taller, las tecnologías, la muralla y el combate básico; que sólo atacan a otros bots salvo con
`--contra-humanos`; a la velocidad del mundo real; con un JSONL por ejecución; dados de alta por la web con
`EO_BOT_PASSWORD`; cuatro por defecto y hasta veinte por proceso.

Lo que ya existe:

- **El protocolo** está en Zod (`@empires-online/protocol`), y el navegador valida con él todo lo que recibe.
- **El cliente web** separa el transporte (`apps/web/src/net/client.ts`, el `GameClient` con su handshake, latido y
  reconexión, y una `socketFactory` inyectable) del estado (`apps/web/src/state`, reductores puros que aplican los
  mensajes y deciden los motivos con que el HUD deshabilita cada botón).
- **El humo** (`scripts/smoke.mjs`) ya juega por el protocolo desde Node: se da de alta, pide tickets con la cookie y
  envía comandos. Recorre cada regla una vez; no decide.
- **Node 22** trae `WebSocket` y `fetch` globales.

## Decisión

### 1. Un bot es un jugador para el servidor y un cliente para el código

El bot se da de alta y entra por la web pública (`/api/auth/register`, `/api/auth/login`, `/api/auth/ticket`) y
juega por el WebSocket v1 con su ticket. No usa el listener interno, la base, Redis ni el secreto de los tickets. El
Game Server no sabe que es un bot y no cambia: ni una regla, ni una fase del tick, ni una tabla, ni un mensaje.

### 2. Un paquete propio que reutiliza el cliente

`apps/bots` es un paquete del workspace en TypeScript, que corre con `tsx` y se prueba con Vitest, con las mismas
versiones que ya usan los demás paquetes: no entra ninguna dependencia nueva en el lockfile. Importa
`@empires-online/protocol` como dependencia del workspace y los módulos de `apps/web/src/net` y `apps/web/src/state`
por ruta relativa, que resuelven sus propias dependencias desde `apps/web`. Si un módulo del estado arrastra React o
zustand para algo que el bot necesita, la parte pura se separa a un fichero propio **sin cambiar su comportamiento**:
los tests de `apps/web/src/state` siguen pasando sin tocarlos.

Así el bot ve el mundo con los mismos reductores que el navegador y decide qué es posible con los mismos motivos que
deshabilitan los botones del HUD (`RN-BOT-002`): un comando que el navegador no dejaría enviar, el bot tampoco lo
envía.

### 3. Un decisor puro con semilla

`decidir(espejo, perfil, ahoraMs, aleatorio) → comandos[]` es una función pura: no lee la hora ni `Math.random`, sino
el `ahoraMs` y el generador con semilla que le pasa el bucle. Los perfiles son datos (un fichero con las prioridades y
los umbrales), no ramas del código. El bucle de cada bot despierta cada `EO_BOT_THINK_MS` con un desfase de la
semilla, envía como mucho dos comandos y lleva los enfriamientos de los rechazos.

### 4. La medida es un fichero local

Cada ejecución escribe un JSONL en `bots/runs/` (sin versionar) y un resumen al terminar. Nada de la medida vuelve al
juego: ni tablas, ni series de métricas, ni eventos.

## Alternativas descartadas

1. **Jugadores no humanos dentro de la simulación**, con un sistema propio en la fase 4: verían el mundo entero y no su
   área de interés, decidirían con un `RandomSource` más dentro del tick, costarían tiempo de tick a todos y no medirían
   lo que vive un cliente (el retraso, los rechazos, la reconexión). Además tendrían que mutar el estado sin pasar por
   el protocolo, que es justo lo que el servidor autoritativo ([ADR-002](ADR-002-authoritative-server.md)) quiere que
   sólo hagan los comandos.
2. **Ampliar `scripts/smoke.mjs`** con un modo de bots: el humo es un recorrido lineal con aserciones que paran al
   primer fallo, en JavaScript sin tipos; un decisor con perfiles, reconexión y registro es otro programa, y mezclarlos
   haría más frágil la comprobación de despliegue.
3. **Un cliente propio del protocolo** en el paquete de bots, sin reutilizar el del navegador: duplicaría el
   handshake, la reconexión y los reductores, y dos espejos del mundo acabarían divergiendo del que ve la persona.
4. **Bots en Go** que importen el dominio del servidor: tendrían los catálogos a mano, pero podrían decidir con reglas
   que el cliente no conoce y dejarían de medir la experiencia de un jugador.
5. **Una vía de alta interna para bots** (por el listener interno o la semilla de desarrollo): no existe en staging y
   saltaría los límites de alta que un jugador sí tiene.

## Consecuencias

- El servidor, la base y el protocolo no cambian: el slice no tiene migración, ni invariantes de dominio nuevos, ni
  tests de Go nuevos.
- Los módulos de `apps/web/src/net` y `apps/web/src/state` tienen un segundo consumidor: un cambio que meta el DOM o
  React en ellos rompe el typecheck o los tests de `apps/bots`.
- Un bot conectado está `ONLINE` y nunca protegido; desconectado, sigue la máquina de presencia como cualquiera.
- Las altas de muchos bots chocan con el límite de alta por IP: el proceso las escalona en vez de pedir un cupo propio.
- `EO_BOT_PASSWORD` y `EO_BOT_THINK_MS` son variables del proceso de bots, documentadas en
  [configuration.md](../operations/configuration.md) §3.10, no del catálogo de `internal/config`.
