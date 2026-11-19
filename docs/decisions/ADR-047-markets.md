# ADR-047: El mercado guarda lo ofrecido en custodia fuera del almacén, y cada compra es un trabajo de dos claves que no toca la ciudad del vendedor

Propósito: fijar cómo el slice de los mercados ([SPEC-MARKETS](../specs/markets.md)) aparta lo que se vende sin
inventar un segundo almacén, cómo una compra mueve recursos entre dos ciudades sin esperar a la del vendedor, dónde
desaparece la comisión y cómo se lee un tablón público sin que la conexión toque la RAM de la simulación.

| Campo | Valor |
|---|---|
| **Estado** | **Aceptado** |
| **Fecha** | **2026-11-18** |
| Ámbito | `internal/domain/market` (la oferta, sus lotes, la comisión y la validación), `internal/domain/building` (`MARKET`), `internal/domain/caravan` (las mitades de una compra), `internal/game/simulation` (el mercado en la RAM, las tres órdenes, la caducidad, las caravanas de una compra), `internal/persistence/postgres` (la publicación, la compra, el cierre, la carga y la lectura del tablón), `internal/websocket` (`market.list`), `internal/protocol`, `packages/protocol`, `apps/web` |
| Índice | [README.md](README.md) |
| Relacionados | [ADR-013](ADR-013-persistence-pipeline.md) (cola ordenada por clave), [ADR-022](ADR-022-durable-write-retry-and-loop-inbox.md) (ninguna escritura se pierde en silencio), [ADR-023](ADR-023-economy-stock-deltas-and-two-phase-orders.md) (deltas de stock versionados y órdenes en dos fases), [ADR-028](ADR-028-event-log-from-world-events.md) (el registro sale de `world_events` y se lee desde la conexión), [ADR-032](ADR-032-trade-offers-and-two-key-jobs.md) (el trabajo de dos claves), [ADR-036](ADR-036-civilization-goods.md) (el taller y la entrega optimista), [ADR-037](ADR-037-caravans-as-units.md) (la caravana que carga en lugar de acreditar) |

---

## Contexto

Producto aceptó el 2026-11-18 las trece recomendaciones de la spec: un tablón público de ofertas entre jugadores, sin
contraparte del sistema; trueque de un recurso por otro, en lotes; un edificio, el mercado, desde la Edad de Bronce;
lo ofrecido sale del almacén al publicar y queda en custodia; una comisión del 10 % por lote y por parte, la del
vendedor pagada al publicar y nunca devuelta; cada compra viaja en dos caravanas; se compra a cualquiera salvo a uno
mismo, sin tratado.

Lo que ya existe:

- **El trato de E10** es una orden en dos fases que reserva en la RAM lo que da cada parte y lo escribe con un trabajo
  de **dos claves**, las de las dos ciudades, porque debita los dos almacenes ([ADR-032](ADR-032-trade-offers-and-two-key-jobs.md) §2-§3).
  Desde E15 la transacción crea además las dos caravanas con su carga ([ADR-037](ADR-037-caravans-as-units.md) §3).
- **Los cobros** de un encargo se reservan en el tick con los depósitos sin volcar del recurso y se escriben como un
  delta versionado ([ADR-023](ADR-023-economy-stock-deltas-and-two-phase-orders.md)); **los créditos** que no son
  órdenes (la elaboración, la llegada de una caravana) son optimistas: la RAM acredita y la transacción confirma, y si
  se descarta se reintenta con una versión nueva ([ADR-036](ADR-036-civilization-goods.md) §4).
- **Las lecturas largas** (el registro, los mensajes) las hace la conexión contra la base, nunca contra la RAM de la
  simulación ([ADR-028](ADR-028-event-log-from-world-events.md)).
- **Los edificios** son un catálogo en Go con su `CHECK` en `buildings.type` y, para el taller y el castillo, un único
  parcial de uno en pie por ciudad.

## Decisión

### 1. La custodia vive en la oferta, no en un almacén

La oferta guarda lo que da cada lote y cuántos lotes le quedan (`lots_left`); la custodia es su producto, y no hay una
columna de cantidad aparte que pueda descuadrar. Lo que está en custodia no está en `city_stocks`: ni el saqueo, ni su
suelo, ni un encargo lo ven.

**Publicar es una orden en dos fases**, como un encargo: el tick valida (mercado completo, recursos, menos de tres
ofertas abiertas contando las que están en vuelo) y **reserva** en la RAM lo que dan todos los lotes más la comisión
del vendedor, llevándose los depósitos sin volcar de ese recurso. La transacción, con la clave de la ciudad, aplica el
delta versionado, inserta la oferta `OPEN` y escribe `MarketOfferPosted`, idempotente por `(player_id, request_id)`.
Con la confirmación, la oferta existe en la RAM con su id; con un rechazo, la reserva vuelve. Es el débito con su
efecto de [INV-ECON-002](../invariants/economy.md#inv-econ-002): el efecto es la custodia.

### 2. La compra es un trabajo de dos claves: la ciudad del comprador y la oferta

**Comprar es una orden en dos fases.** El tick valida, busca la ruta entre las dos ciudades (como al aceptar un trato),
prepara las dos caravanas y **reserva**: lo que paga el comprador más su comisión sale de su almacén en la RAM, y los
lotes quedan apartados en la oferta (`reserved`), de modo que otra compra en vuelo sólo ve los que quedan sin
reservar. La transacción:

1. descuenta los lotes de la oferta **condicionada** a `status = 'OPEN' AND lots_left >= k`, y la pasa a `SOLD_OUT`
   si llega a cero;
2. aplica el delta versionado del almacén del comprador;
3. inserta la compra en `market_purchases`, idempotente por `(buyer_player_id, request_id)`;
4. crea las dos caravanas con su carga, con `market_purchase_id` en lugar de `offer_id`;
5. escribe `MarketPurchase`.

Todo o nada. Al volver, la RAM resta los lotes, suelta la reserva y pone las caravanas en el mapa; con un rechazo, los
devuelve al comprador y a la oferta.

La compra **no toca el almacén del vendedor**: lo que vende ya salió de él al publicar, y lo que recibe llega en una
caravana. Por eso sus claves son `city:<comprador>` y `market:<oferta>`, no la ciudad del vendedor: un vendedor con
muchas ventas no hace esperar a todos sus compradores en su partición, y la clave de la oferta ordena entre sí todo lo
que la escribe (compras, retirada, caducidad). Dos compras de la misma oferta se escriben en el orden en que el tick
las reservó.

### 3. Retirar y caducar son optimistas, por la clave de la oferta y la del vendedor

Cerrar una oferta (retirada o caducidad) no reserva nada: lo que vuelve ya era del vendedor. El tick la cierra en la
RAM, **acredita** la custodia restante al almacén del vendedor, lo difunde, y encola el cierre con las claves
`market:<oferta>` y `city:<vendedor>`. La transacción pasa la oferta a `WITHDRAWN` o `EXPIRED` **condicionada** a
`OPEN` y aplica el delta versionado del crédito; sin la oferta en `OPEN`, no acredita nada. Si se descarta del todo,
se reintenta en el siguiente volcado con una versión nueva, como la llegada de una caravana.

Para que un cierre y una compra no se crucen, el tick no retira una oferta con compras reservadas (`INVALID_TARGET`) y
la caducidad espera al primer barrido sin ninguna (`RN-MARKET-009`). Una caída entre la RAM y la transacción deja la
oferta abierta en la base con su custodia, y al arrancar se carga abierta: nada se duplica ni se pierde.

### 4. La comisión es el único sumidero, y se escribe

La comisión de cada lote es `⌈cantidad / 10⌉`, una función pura del dominio (`market.Commission`). Se debita con la
publicación o con la compra y no entra en ningún sitio: queda escrita en la oferta (`seller_commission`), en la compra
(`buyer_commission`) y en el payload de sus eventos, para que la cuenta de [INV-TRADE-001](../invariants/trade.md#inv-trade-001)
se pueda comprobar: lo que sale de los almacenes es lo que entra en las caravanas, la custodia o los almacenes al
cerrarse, más las comisiones.

### 5. El edificio `MARKET`

Un tipo más del catálogo de edificios, con los valores de la spec (Bronce, 150 de madera y 100 de piedra, 60 s, 200 de
vida, sin población) y las reglas del taller: un único parcial de uno en pie por ciudad, derribable con el golpe de
asedio de siempre. No guarda nada: derribarlo no toca las ofertas.

### 6. El tablón se lee de la base desde la conexión

`market.list` lo contesta la conexión del jugador con una consulta a `market_offers` (las `OPEN` de otros jugadores,
con el filtro, las 100 más recientes), unida a `cities` para el nombre, la civilización y la **posición de la ciudad**.
El depósito no está en la base (es una función del mundo), así que el cliente calcula la distancia con la posición de
las ciudades; difiere de la de los depósitos en un par de tiles, y la ruta del tick decide. La lectura puede ir unos
milisegundos por detrás de la RAM: una compra que llega tarde se rechaza en el tick con su motivo.

### 7. La migración `000028`

- `market_offers` y `market_purchases`, con los `CHECK` de la spec §7 y un trigger que rechaza una compra cuyo
  comprador es el dueño de la oferta.
- `caravans.offer_id` admite nulos; `caravans.market_purchase_id` con FK y un `CHECK` de que exactamente uno de los dos
  está puesto.
- `buildings_type_valid` suma `MARKET`, con su único parcial.
- Las tablas de diseño `markets` y `trade_transactions` no se crean: las sustituyen éstas.

## Alternativas descartadas

1. **Sin custodia, comprobando al comprar como E10.** Las ofertas de un tablón público fallarían sin que el comprador
   pudiera saber cuáles; producto lo descartó (Q4).
2. **La custodia como un almacén aparte** (`market_stocks` por ciudad y recurso). Un segundo stock con su versión y sus
   deltas que habría que cuadrar con las ofertas; `lots_left × give_per_lot` no puede descuadrar.
3. **La compra con las claves de las dos ciudades**, como un trato. Correcto, pero hace esperar cada compra a la
   partición del vendedor sin necesidad, porque su almacén no se escribe.
4. **Retirar en dos fases.** La respuesta llegaría tras la transacción y la oferta quedaría ocupada mientras tanto, para
   un crédito de algo que ya era del jugador; la entrega optimista ya está resuelta y documentada.
5. **El tablón en la RAM**, consultado por el buzón del loop: metería en el tick una lectura de hasta 100 filas por cada
   jugador que abre el panel.
6. **Una caravana del vendedor que sale al publicar** hacia un mercado neutral del mapa, y otra de allí al comprador:
   duplica los viajes y pide colocar mercados en el mundo (Q7).

## Consecuencias

- Una compra escribe la oferta, el almacén del comprador y dos caravanas en una transacción; el vendedor no ve nada
  hasta que llegan las caravanas, salvo `market.update` con sus lotes.
- El trabajo de la cola admite una clave que no es una ciudad (`market:<id>`); el orden por clave de ADR-013 no cambia.
- `INV-TRADE-001` e `INV-TRADE-007` se precisan, y la familia `INV-TRADE` suma tres invariantes (custodia, lotes y
  nadie se compra a sí mismo).
- Una caída entre una retirada optimista y su transacción devuelve la oferta abierta al arrancar, con lo que el jugador
  vería «reaparecer» una oferta que retiró: la misma ventana que las demás escrituras optimistas.
