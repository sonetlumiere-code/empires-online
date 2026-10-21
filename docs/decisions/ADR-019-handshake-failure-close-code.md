# ADR-019: Un handshake fallido se comunica sólo con el código de cierre

Propósito: fijar que, mientras la conexión no tiene sesión, el servidor no envía ningún mensaje de aplicación
antes de cerrarla, y que un ticket rechazado se comunica con el cierre `4401` y nada más. Sustituye en ese punto
a [ADR-010](ADR-010-authentication-game-ticket.md), que pedía un `system.error` previo.

| Campo | Valor |
|---|---|
| **Estado** | **Aceptado** |
| **Fecha** | **2026-10-21** |
| Ámbito | `internal/websocket` (handshake), `apps/web/src/net/client.ts`, [../specs/websocket-protocol.md](../specs/websocket-protocol.md) §6 y §9 |
| Índice | [README.md](README.md) |
| Relacionados | [ADR-010](ADR-010-authentication-game-ticket.md) (lo sustituye en parte), [ADR-006](ADR-006-websocket-protocol.md), [ADR-018](ADR-018-identity-store-and-ticket-issuer.md) |

---

## 1. Contexto

[ADR-010](ADR-010-authentication-game-ticket.md) §2.1 decidió que un ticket inválido, caducado, de otra
audiencia o ya consumido recibe un `system.error` con `UNAUTHORIZED` **antes** del cierre `4401`, «para que el
cliente pueda distinguir "tu ticket caducó, pide otro" de "la red se cayó"», y lo repite en §2.3, §4.1 y en su
tabla de verificación. El servidor nunca lo hizo: `handshakeCloseCode` (`internal/websocket/server.go`) cierra
con `4401` sin mensaje previo, la spec del protocolo lo fija así (§6, regla 2) y el catálogo de errores marca
`UNAUTHORIZED` como reservado (§9). El registro y el código se contradicen, y el ADR es el que manda hasta que
otro lo sustituya.

Fuerzas en juego:

- **El código de cierre ya distingue los dos casos.** Un cierre de aplicación viaja en el *close frame* y el
  navegador lo entrega en `CloseEvent.code`; una caída de red llega como `1006`, sin frame. `4401` frente a
  `1006` es exactamente la distinción que ADR-010 quería, sin mensaje.
- **Antes del handshake no hay sesión.** Todo envelope servidor→cliente lleva un `seq` monótono por conexión,
  y la spec fija `session.welcome` como `seq` 1. Un `system.error` previo sería un mensaje de una conexión sin
  sesión: o lleva `seq` 1 sin ser el `welcome`, o es una excepción a la numeración.
- **Hay cuatro formas de fallar el handshake**: mensaje inválido o versión no soportada (`4400`), credencial
  rechazada (`4401`), plazo vencido (`4408`) y frame por encima del límite (`1009`). Ninguna envía hoy un
  mensaje. Una regla para las cuatro es más fácil de cumplir que una por caso.
- **No dar información a quien sondea.** ADR-010 y la spec coinciden en traducir todas las causas
  —firma, `exp`, `aud`, `jti` consumido, `sub` inválido— a un único resultado. Esa fuerza no decide entre
  enviar el mensaje o no: con mensaje o sin él, el resultado es uno.
- **El cliente ya decide por el código.** `apps/web/src/net/client.ts` trata `4401` y `4403` como fallo de
  credenciales y no reintenta en bucle; no lee ningún `system.error` durante el handshake.

## 2. Decisión

**Mientras la conexión no tiene sesión —desde que se abre hasta que el servidor envía `session.welcome`— el
servidor no envía ningún mensaje de aplicación. Un handshake fallido se comunica sólo con el código de cierre:
`4400`, `4401`, `4408` o `1009`. Un ticket que no supera la verificación, o un primer mensaje que no es
`session.hello`, cierra con `4401` y nada más.**

- `system.error` es un mensaje **de sesión**: sólo existe a partir del `session.welcome`.
- El código `UNAUTHORIZED` sigue en el catálogo cerrado de errores, **reservado**, igual que `FORBIDDEN` y
  `MESSAGE_TOO_LARGE`: ninguna ruta del servidor lo emite hoy por el WebSocket. Las rutas HTTP de `apps/web`
  (`/api/auth/*`) y el endpoint interno sí responden `401 UNAUTHORIZED`; eso no cambia.
- El texto del cierre (`reason`) es genérico y no forma parte del contrato: no nombra la causa.
- El cliente distingue por el código: `4401` significa «la credencial no sirvió», y lo que haga con ello
  —pedir otro ticket con la cookie de sesión o volver al login— es política del cliente
  ([../architecture/frontend.md](../architecture/frontend.md) §3.3).

Sustituye de [ADR-010](ADR-010-authentication-game-ticket.md): la rama `system.error { code: "UNAUTHORIZED" }`
del diagrama de §2.1 y la frase que la acompaña, el último punto de §2.3 («Permite responder con el
protocolo»), el punto «Fallo explícito y accionable» de §4.1, y las filas de §5 que esperan `UNAUTHORIZED`, que
se leen como «cierre `4401`». El resto de ADR-010 sigue vigente.

## 3. Alternativas consideradas

### 3.1 Cumplir ADR-010: `system.error` y después `4401`

**Ventajas reales:** un cliente que sólo escucha mensajes ve un código del catálogo cerrado y su `requestId`
—el del `session.hello`— para correlacionarlo; el patrón ya existe dentro de la sesión, donde `RATE_LIMITED`
llega junto al cierre `4429`. **Descarte:** no añade información que el código de cierre no lleve ya, obliga a
numerar un envelope de una conexión sin sesión (romper que `session.welcome` es el `seq` 1 o abrir una
excepción) y, para no tener dos reglas, a extenderlo también a `4400` y `4408`. El precedente de `4429` no se
traslada: allí la sesión existe y el mensaje es de ella.

### 3.2 Rechazar en el *upgrade* HTTP con un `401`

**Ventajas reales:** ninguna conexión WebSocket llega a abrirse con una credencial mala. **Descarte:** exige
que la credencial viaje en el *upgrade*, es decir en la URL, y [ADR-010](ADR-010-authentication-game-ticket.md)
§2.3 ya lo descartó por los logs de los intermediarios. No se reabre.

## 4. Consecuencias

### 4.1 Positivas

- Una sola regla para todo fallo del handshake, y la numeración de `seq` sigue empezando en el `welcome`.
- El registro coincide con el código y con la spec sin cambiar una línea del servidor.
- Los tests que existen ya la defienden: en `TestTicketInvalidoCierraLaConexion` y
  `TestNoSeAceptanComandosSinAutenticar` la **primera** lectura del socket tiene que ser el cierre `4401`, así que
  un `system.error` previo los haría fallar.

### 4.2 Negativas

- Un cliente que no lea el código de cierre no se entera de por qué se cerró: tiene que leerlo. En un
  navegador es un campo de `CloseEvent`; en otros clientes, el del *close frame*.
- El diagnóstico de un handshake fallido sigue dependiendo de los logs del servidor, como ya decía ADR-010 §4.2:
  el cliente sabe que su credencial no sirvió, no por qué.
- `UNAUTHORIZED` queda como un código del catálogo que el WebSocket no emite. Es el tercero reservado.

### 4.3 Neutras

- ADR-010 lleva una nota de sustitución parcial, y los documentos que decían «`UNAUTHORIZED` y cierre `4401`»
  para el handshake pasan a decir «cierre `4401`».

## 5. Verificación

| Qué se verifica | Nivel | Test |
|---|---|---|
| Ticket ilegible → cierre `4401` como primera lectura, sin mensaje previo | e2e | `TestTicketInvalidoCierraLaConexion` |
| Un comando como primer mensaje → cierre `4401` sin mensaje previo | e2e | `TestNoSeAceptanComandosSinAutenticar` |
| Ticket reutilizado → cierre `4401` | e2e | `TestTicketNoSePuedeReutilizarEnOtraConexion` |
| Primer mensaje ilegible → cierre `4400` sin mensaje previo | e2e | `TestUnHandshakeIlegibleCierraCon4400SinMensaje` |
| Sin `session.hello` en plazo → cierre `4408` | e2e | `TestSinHandshakeEnPlazoSeCierraCon4408` |
| El cliente no reintenta en bucle tras `4401` | unit | `apps/web/src/net/client.test.ts` |

## 6. Estado

Aceptado. No cambia código del servidor: corrige el registro para que diga lo que el servidor hace y la spec
ya decía. Sustituye en parte a [ADR-010](ADR-010-authentication-game-ticket.md).
