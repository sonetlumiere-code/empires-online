# Invariantes de Comercio (INV-TRADE-xxx)

Propiedades del comercio entre dos ciudades: lo que un trato mueve, quién puede cerrar una oferta, cuántas puede
haber entre dos jugadores, desde E15 las caravanas que llevan lo que se movió y, desde E26, el mercado: la custodia de
lo ofrecido, sus lotes y quién compra.

Formato y severidades: [README.md](README.md). Stock: [economy.md](economy.md). Tratados:
[diplomacy.md](diplomacy.md). Specs: [../specs/trade.md](../specs/trade.md), [../specs/caravans.md](../specs/caravans.md).
Decisiones: [ADR-023](../decisions/ADR-023-economy-stock-deltas-and-two-phase-orders.md),
[ADR-027](../decisions/ADR-027-hot-treaties.md), [ADR-032](../decisions/ADR-032-trade-offers-and-two-key-jobs.md),
[ADR-037](../decisions/ADR-037-caravans-as-units.md), [ADR-047](../decisions/ADR-047-markets.md). Mercado:
[../specs/markets.md](../specs/markets.md).

---

## Contexto: el comercio, de extremo a extremo

| Pieza | Dónde | Qué hace |
|---|---|---|
| Reglas | `internal/domain/trade`, `internal/domain/diplomacy` | La oferta y sus líneas, quién puede responderla o retirarla; la escalera de tratados y qué tratado deja comerciar. No escriben ni conocen el protocolo. |
| Simulación | `internal/game/simulation/trade.go` | Las órdenes en dos fases contra la RAM; la reserva de los dos almacenes al aceptar; la caducidad en la fase 5. |
| Cola | `internal/persistence/queue.go` | El trabajo de dos claves: el trato espera a las dos ciudades (ADR-032 §3). |
| Persistencia | `postgres.TradeRepo` | Cada transición de una oferta con su evento; el trato aceptado, con los deltas de los dos almacenes, en una transacción. |
| Reglas del mercado | `internal/domain/market` | La oferta en lotes, la comisión por lote redondeada hacia arriba y la custodia como `lots_left × give_per_lot` (E26). |
| Simulación del mercado | `internal/game/simulation/market.go` | Publicar, comprar y retirar en dos fases contra la RAM; la reserva de los lotes y del almacén del comprador; la caducidad en el barrido de los tratados. |
| Cola del mercado | `internal/persistence/queue.go` | `market.buy` espera a `city:<comprador>` y `market:<id>`; `market.close` (retirar o caducar), a `city:<vendedor>` y `market:<id>` (ADR-047 §2). |
| Persistencia del mercado | `postgres.MarketRepo` | La publicación con su débito, la compra con sus lotes, el débito y las dos caravanas, y el cierre con su devolución, cada una en una transacción con su evento; el tablón que lee la conexión; `MarketRepo.Violations` en la carga. |

### Lo que la base de datos garantiza y lo que no

| Constraint | Tabla | Qué impone |
|---|---|---|
| `trade_offers_status_valid` | `trade_offers` | Dominio cerrado de `status` |
| `trade_offers_distinct_players` | `trade_offers` | `proposer_id <> target_id` |
| `trade_offers_one_pending_per_pair` | `trade_offers` | Índice **parcial** único sobre el par sin orden `WHERE status = 'PROPOSED'` |
| `trade_offers_unique_request` | `trade_offers` | Único `(proposer_id, request_id)`: una orden no produce dos ofertas |
| `trade_offer_lines_pkey` | `trade_offer_lines` | `PRIMARY KEY (offer_id, resource)`: un recurso en un solo lado |
| `trade_offer_lines_amount_positive`, `trade_offer_lines_resource_valid`, `trade_offer_lines_side_valid` | `trade_offer_lines` | `amount > 0`, el recurso del catálogo y `side` en `GIVE` o `WANT` |
| `caravans_status_valid`, `caravans_unit_while_in_transit` | `caravans` | Dominio cerrado de `status`; una caravana tiene unidad si y sólo si está `IN_TRANSIT` (E15, ADR-037 §8) |
| `caravans_two_per_offer` | `caravans` | Único `(offer_id, from_city_id)`: como mucho una caravana por sentido de cada trato |
| `caravan_cargo_pkey`, `caravan_cargo_amount_positive`, `caravan_cargo_resource_valid` | `caravan_cargo` | Un recurso una vez por caravana, `amount > 0` y el recurso del catálogo |
| `caravans_one_source` | `caravans` | Una caravana nace de un trato (`offer_id`) o de una compra (`market_purchase_id`), exactamente de uno (E26) |
| `caravans_two_per_purchase` | `caravans` | Índice **parcial** único `(market_purchase_id, from_city_id) WHERE market_purchase_id IS NOT NULL`: como mucho una caravana por sentido de cada compra |
| `market_offers_status_valid`, `market_offers_give_resource_valid`, `market_offers_want_resource_valid`, `market_offers_distinct_resources`, `market_offers_amounts_positive` | `market_offers` | Dominio cerrado de `status`; los dos recursos del catálogo y distintos; cantidades por lote positivas |
| `market_offers_lots_valid`, `market_offers_open_has_lots`, `market_offers_sold_out_empty` | `market_offers` | De 1 a 100 lotes, `0 <= lots_left <= lots_total`; una abierta tiene lotes y una agotada ninguno (`INV-TRADE-008`, `INV-TRADE-009`) |
| `market_offers_totals_capped`, `market_offers_commission_valid`, `market_offers_closed_has_time`, `market_offers_expiry_after_creation` | `market_offers` | La cota de 1 000 000 por lado; la comisión del vendedor de `RN-MARKET-006`; `closed_at` sólo en una cerrada; caduca después de publicarse |
| `market_offers_unique_request` | `market_offers` | Único `(player_id, request_id)`: una orden no publica dos ofertas |
| `market_purchases_lots_valid`, `market_purchases_paid_positive`, `market_purchases_commission_valid` | `market_purchases` | De 1 a 100 lotes, un pago positivo y la comisión del comprador de `RN-MARKET-006` |
| `market_purchases_unique_request` | `market_purchases` | Único `(buyer_player_id, request_id)`: una orden no compra dos veces |
| `market_purchases_not_own` (trigger) | `market_purchases` | Rechaza con `23514` la compra del dueño de la oferta (`INV-TRADE-010`) |

> **Lo que el esquema NO garantiza.** Que una oferta aceptada tuviera un tratado vigente cruza `trade_offers` y
> `treaties`, y lo decide la RAM, que es la fuente de verdad de los tratados (ADR-027 §1); que lo que sale de una
> ciudad entre en la otra cruza dos filas de `city_stocks`, y lo garantiza la transacción del trato. Ninguna ficha de
> esta familia declara `DB` para ellas.

---

<a id="inv-trade-001"></a>
## INV-TRADE-001 — Un trato nunca crea ni destruye recursos

| Campo | Valor |
|---|---|
| Severidad | CRITICO |
| Aplicación | DB, DOMAIN, TEST |
| Milestone | E10 |
| Política ante violación | REJECT (la transacción revierte entera; la RAM se compensa) |
| Cobertura | **Cubierto** (desde E26 también la comisión del mercado: `TestElEjemploDeLaSpec` en `internal/domain/market`, `TestGoldenMercadoPublicarComprarLlegarYCaducar` en simulation y `TestPublicarEnElMercadoEsIdempotente` en integration) por `Test_INV_TRADE_001_UnTratoConFalloEnUnDeltaNoMueveNada`, `TestUnTratoDebitaLosDosAlmacenesYCreaSusCaravanasUnaVez` y `TestUnaCapturaSinRutaPierdeLaCaravana` (integration), `TestGoldenComercioOfertaAceptacionYLosDosAlmacenes`, `TestGoldenCaravanasDelTratoALaLlegada`, `TestCapturadaSinRutaSePierde` y `TestUnTratoQueNoSeEscribeDevuelveLoReservado` (simulation) y `TestLasPatasDeUnTratoConservanCadaRecurso` (`internal/domain/trade`) |

**Enunciado.** Para todo trato aceptado y para cada recurso, la cantidad que resta del almacén de una ciudad es
exactamente la que suma al de la otra, y las dos escrituras se confirman en la misma transacción que pasa la oferta a
`ACCEPTED`.

**Razón.** Un trato que acreditara sin debitar, o que se escribiera a medias, crearía piedra o madera de la nada: la
forma más directa de hacer trampa en una economía entre jugadores, y la que más se nota cuando se repite.

**Cómo se garantiza.** `DB` — `TradeRepo.Accept` aplica los deltas de las dos ciudades y la transición de la oferta en
un solo `InTx`; cualquier error revierte todo. `DOMAIN` — los deltas de las dos ciudades se calculan de las mismas
líneas de la oferta; la reserva y su devolución recorren las mismas líneas (ADR-032 §2).

**Cómo se verifica.** `Test_INV_TRADE_001_UnTratoConFalloEnUnDeltaNoMueveNada` (integration: con la versión de un
delta ya escrita, o con un débito que dejaría un stock negativo, no se mueve nada y la oferta sigue pendiente),
`TestUnTratoDebitaLosDosAlmacenesYCreaSusCaravanasUnaVez` (integration, desde E15: repetirlo no mueve nada otra
vez y devuelve las mismas caravanas), el golden
`TestGoldenComercioOfertaAceptacionYLosDosAlmacenes` (simulation: los cuatro deltas suman cero por recurso y cada uno
lleva una versión mayor que la del stock), `TestUnTratoQueNoSeEscribeDevuelveLoReservado` (simulation) y
`TestLasPatasDeUnTratoConservanCadaRecurso` (domain) — **existen y pasan**. Verificado por mutación: confirmar sin
acreditar lo recibido, un trato fallido que no devuelve la reserva, no llevarse los depósitos sin volcar y no subir la
versión del recurso que sólo se recibe ponen en rojo su test.

**Violación en runtime.** No hay estado intermedio que detectar: la transacción es la garantía.

**Precisión (2026-11-13, [SPEC-CARAVANS](../specs/caravans.md) §4.2, [ADR-037](../decisions/ADR-037-caravans-as-units.md) §3).**
Desde E15 un trato no acredita en el acto: lo que resta del almacén de cada ciudad entra en la carga de su caravana
en la misma transacción que pasa la oferta a `ACCEPTED`, y lo que lleva una caravana entra entero en un almacén al
llegar ([INV-TRADE-005](#inv-trade-005), [INV-TRADE-006](#inv-trade-006)). Un trato sigue sin crear recursos nunca; el
único camino por el que los destruye es la pérdida de una caravana capturada sin ruta a la ciudad del captor
(`RN-CARAVAN-006`), que queda en `world_events` como `CaravanLost`. La ficha queda parcial hasta que los tests del trato
que carga existan. **Desde el mismo día, cubierta:** `TestGoldenCaravanasDelTratoALaLlegada` (simulation) comprueba
que lo debitado es lo que cargan las dos caravanas y lo que entra en cada almacén al llegar, y
`TestUnTratoDebitaLosDosAlmacenesYCreaSusCaravanasUnaVez` (integration), que la transacción crea las dos con su carga.

**Precisión (2026-11-18, [SPEC-MARKETS](../specs/markets.md) §4.3, [ADR-047](../decisions/ADR-047-markets.md) §4).**
Desde E26 el comercio tiene un segundo camino, el mercado, y un segundo sumidero, su comisión. El enunciado pasa a ser:
el comercio nunca crea recursos; lo que sale de un almacén por un trato o por el mercado entra, en la misma transacción,
en una caravana o en la custodia de una oferta, salvo la comisión del mercado, que desaparece y queda escrita en la
oferta, en la compra y en sus eventos; lo que sale de la custodia entra en una caravana o vuelve al almacén del
vendedor; y sólo la comisión y la pérdida de una caravana capturada sin ruta destruyen recursos. La ficha queda parcial
hasta que los tests de la comisión existan. **Desde el mismo día, cubierta:** `TestElEjemploDeLaSpec` (domain) y el
golden `TestGoldenMercadoPublicarComprarLlegarYCaducar` (simulation) comprueban que publicar el ejemplo de la spec
debita 66 de piedra (60 en custodia y 6 de comisión), que comprar 2 lotes debita 66 de madera y que las caravanas llevan
exactamente 40 de piedra y 60 de madera; `TestPublicarEnElMercadoEsIdempotente` (integration), que la base debita una
sola vez la custodia con su comisión.

---

<a id="inv-trade-002"></a>
## INV-TRADE-002 — Toda oferta aceptada se aceptó con un tratado vigente entre los dos

| Campo | Valor |
|---|---|
| Severidad | ALTO |
| Aplicación | DOMAIN, TEST |
| Milestone | E10 |
| Política ante violación | REJECT |
| Cobertura | **Cubierto** por `TestAceptarSinTratadoSeRechaza`, `TestSinTratadoNoSeComercia` y `TestElFinDelPreavisoCierraLasOfertas` (simulation) y `TestQueTratadoDejaComerciar` (`internal/domain/diplomacy`) |

**Enunciado.** Una oferta pasa a `ACCEPTED` sólo si, en el tick en que se acepta, los dos jugadores tienen un tratado
`ACTIVE` de cualquier escalón (`TRADE`, `NON_AGGRESSION` o `ALLIANCE`) cuyo fin no ha llegado.

**Razón.** Sin esta propiedad el tratado de comercio no significaría nada, y romper un tratado no cortaría el comercio
al terminar su preaviso.

**Cómo se garantiza.** `DOMAIN` — la validación del tick comprueba `Treaty.AllowsTradeAt(now)` antes de reservar; la
fase 5 cierra las ofertas de un par sin tratado (`RN-TRADE-006`).

**Cómo se verifica.** `TestAceptarSinTratadoSeRechaza`, `TestSinTratadoNoSeComercia` y
`TestElFinDelPreavisoCierraLasOfertas` (simulation), y `TestQueTratadoDejaComerciar` (domain) — **existen y pasan**.
Verificado por mutación: aceptar sin mirar el tratado y caducar sin mirarlo ponen en rojo el primero y el tercero.

**Violación en runtime.** La orden se rechaza con `TREATY_REQUIRED` y no se reserva nada.

---

<a id="inv-trade-003"></a>
## INV-TRADE-003 — Entre dos jugadores, como mucho una oferta pendiente

| Campo | Valor |
|---|---|
| Severidad | ALTO |
| Aplicación | DB, DOMAIN, TEST |
| Milestone | E10 |
| Política ante violación | REJECT |
| Cobertura | **Cubierto** por `Test_INV_TRADE_003_UnaOfertaPendientePorPar` (integration) y `TestRechazosDelComercio` (simulation) |

**Enunciado.** Para cada par de jugadores existe como mucho una fila de `trade_offers` con `status = 'PROPOSED'`, sea
quien sea el que la propuso.

**Razón.** Con dos ofertas abiertas entre los mismos jugadores, aceptar una no diría nada de la otra, y dos
aceptaciones seguidas podrían vaciar un almacén que el jugador creía comprometido sólo una vez.

**Cómo se garantiza.** `DB` — `trade_offers_one_pending_per_pair`, el índice único parcial sobre
`(least(proposer_id, target_id), greatest(proposer_id, target_id))` (migración `000013`). `DOMAIN` — `trade.propose` se
valida contra las ofertas pendientes de la RAM (`RN-TRADE-001`).

**Cómo se verifica.** `Test_INV_TRADE_003_UnaOfertaPendientePorPar` (integration: el índice, en las dos direcciones, y
otra oferta en cuanto se cierra la primera) y `TestRechazosDelComercio` (simulation) — **existen y pasan**.

**Violación en runtime.** La transacción falla por el índice y la orden se rechaza con `INVALID_TARGET`.

---

<a id="inv-trade-004"></a>
## INV-TRADE-004 — Una oferta sólo la cierra quien tiene derecho a hacerlo

| Campo | Valor |
|---|---|
| Severidad | CRITICO |
| Aplicación | DOMAIN, TEST |
| Milestone | E10 |
| Política ante violación | REJECT |
| Cobertura | **Cubierto** por `TestSoloRespondeElDestinatarioYSoloRetiraQuienOfrece` (`internal/domain/trade`) y `TestRechazosDelComercio` (simulation) |

**Enunciado.** Una oferta pasa de `PROPOSED` a `ACCEPTED` o a `REJECTED` sólo por una orden de `target_id`, y a
`WITHDRAWN` sólo por una de `proposer_id`.

**Razón.** Si quien ofrece pudiera aceptar su propia oferta, vaciaría el almacén del otro a cambio de lo que él
quisiera darle.

**Cómo se garantiza.** `DOMAIN` — la validación del tick compara al autor de la orden con `proposer_id` y `target_id`
antes de reservar; la transacción condiciona la escritura al estado esperado.

**Cómo se verifica.** `TestSoloRespondeElDestinatarioYSoloRetiraQuienOfrece` (`internal/domain/trade`) y
`TestRechazosDelComercio` (simulation) — **existen y pasan**.

**Violación en runtime.** La orden se rechaza con `INVALID_TARGET` y no se escribe nada.

---

<a id="inv-trade-005"></a>
## INV-TRADE-005 — La carga de una caravana no cambia en el camino

| Campo | Valor |
|---|---|
| Severidad | CRITICO |
| Aplicación | DB, DOMAIN, TEST |
| Milestone | E15 |
| Política ante violación | REJECT |
| Cobertura | **Cubierto** por `TestUnaCapturaCambiaElDuenoYNoLaCarga` y `TestLasCaravanasQueNoCuadranSeSenalanAlArrancar` (integration) y `TestUnTerceroCapturaLaCaravanaYSeLaLleva` (simulation) |

**Enunciado.** Las filas de `caravan_cargo` de una caravana son, recurso a recurso, las líneas del lado del trato que
da su ciudad de origen, y no cambian desde la transacción que la crea hasta la que la entrega o la pierde, tampoco al
capturarla. En la RAM, la carga de una caravana `IN_TRANSIT` es la misma que en la base.

**Razón.** La caravana es el único sitio donde un recurso existe fuera de un almacén. Si su carga pudiera cambiar en
el camino, una captura, un reinicio o una entrega repetida crearían o borrarían recursos sin que ningún almacén lo
reflejara.

**Cómo se garantiza.** `DB` — la carga sólo se escribe al insertar la caravana en la transacción del trato; la
captura y la entrega no tocan `caravan_cargo` (ADR-037 §5 y §4). `DOMAIN` — la carga de la RAM se construye de las
mismas líneas de la oferta y la captura sólo cambia el dueño, la vida y el destino.

**Cómo se verifica.** `TestUnaCapturaCambiaElDuenoYNoLaCarga` (integration: capturar, también dos veces, no toca
`caravan_cargo`), `TestLasCaravanasQueNoCuadranSeSenalanAlArrancar` (integration: una carga alterada a mano sale en la
comprobación del arranque) y `TestUnTerceroCapturaLaCaravanaYSeLaLleva` y `TestElDestinatarioRecuperaLaCaravana`
(simulation: la caravana capturada y recapturada llega con su carga) — **existen y pasan**.

**Violación en runtime.** La comprobación del arranque (`CaravanRepo.Violations`) compara la carga de cada caravana
`IN_TRANSIT` con las líneas de su lado del trato; una discrepancia se registra como `invariant_violation` con
`inv_id=INV-TRADE-005` y esa caravana no se incorpora a la simulación: queda en la base para revisarla a mano.

---

<a id="inv-trade-006"></a>
## INV-TRADE-006 — Una caravana se entrega como mucho una vez

| Campo | Valor |
|---|---|
| Severidad | CRITICO |
| Aplicación | DB, DOMAIN, TEST |
| Milestone | E15 |
| Política ante violación | REJECT |
| Cobertura | **Cubierto** por `Test_INV_TRADE_006_UnaCaravanaSeEntregaUnaSolaVez` y `TestUnaEntregaAdelantadaNoEscribeNada` (integration) y `TestGoldenCaravanasDelTratoALaLlegada`, `TestUnaEntregaDescartadaSeReintentaConVersionNueva` y `TestTrasUnReinicioLasCaravanasLleganASuHora` (simulation) |

**Enunciado.** Una caravana pasa de `IN_TRANSIT` a `DELIVERED` o a `LOST` una sola vez. El paso a `DELIVERED` y el
crédito de su carga en el almacén de un único destino se confirman en la misma transacción; una transacción que la
encuentra fuera de `IN_TRANSIT` no acredita nada.

**Razón.** La entrega es optimista y se reintenta: sin esta propiedad, un reintento acreditaría dos veces y un trato
crearía recursos.

**Cómo se garantiza.** `DB` — la entrega condiciona la transición a `status = 'IN_TRANSIT'` y aplica los deltas en la
misma transacción; `caravans_unit_while_in_transit` impide una caravana cerrada con unidad. `DOMAIN` — la RAM quita
la caravana del mapa en el tick en que la entrega, así que no la vuelve a entregar.

**Cómo se verifica.** `Test_INV_TRADE_006_UnaCaravanaSeEntregaUnaSolaVez` (integration: la segunda entrega no
acredita nada, la unidad y su movimiento se borran), `TestUnaEntregaAdelantadaNoEscribeNada` (integration),
`TestUnaEntregaDescartadaSeReintentaConVersionNueva` (simulation: la RAM acredita una vez y el volcado reintenta con
versión nueva), `TestTrasUnReinicioLasCaravanasLleganASuHora` (simulation, recovery) y el golden
`TestGoldenCaravanasDelTratoALaLlegada` — **existen y pasan**. Verificado por mutación: sin la condición
`status = 'IN_TRANSIT'` de la entrega, el primero se pone en rojo (la madera se acredita dos veces).

**Violación en runtime.** La transacción repetida no escribe nada; la ventana de una entrega que falla del todo tras
el `COMMIT` es la documentada en ADR-037 §4.

---

<a id="inv-trade-007"></a>
## INV-TRADE-007 — Toda caravana nace de un trato aceptado, dos por trato

| Campo | Valor |
|---|---|
| Severidad | ALTO |
| Aplicación | DB, DOMAIN, TEST |
| Milestone | E15 |
| Política ante violación | REJECT |
| Cobertura | **Cubierto** (desde E26 también las caravanas de una compra: `TestUnaCompraDescuentaLosLotesYCreaSusCaravanasUnaVez` y `TestLasRestriccionesDelMercado` en integration, `TestUnaCompraDaDosCaravanasUnaPorSentido` en `internal/domain/caravan`) por `TestUnTratoDebitaLosDosAlmacenesYCreaSusCaravanasUnaVez`, `Test_INV_TRADE_001_UnTratoConFalloEnUnDeltaNoMueveNada`, `TestLasRestriccionesDeLasCaravanas` y `TestLasCaravanasQueNoCuadranSeSenalanAlArrancar` (integration), `TestGoldenCaravanasDelTratoALaLlegada` (simulation) y, en TypeScript, «city.train no admite CARAVAN» (`packages/protocol`) |

**Enunciado.** Toda fila de `caravans` apunta a una oferta `ACCEPTED`, y toda oferta aceptada desde E15 tiene
exactamente dos caravanas, una que sale de cada ciudad, creadas en la misma transacción que la acepta. Ninguna otra
orden ni ningún otro trabajo crea una unidad `CARAVAN`.

**Razón.** Si una caravana pudiera nacer sin trato, sería un envío sin contrapartida (lo que la Q12 de SPEC-CARAVANS
deja fuera) o un recurso salido de la nada.

**Cómo se garantiza.** `DB` — `caravans.offer_id` con FK a `trade_offers` y `caravans_two_per_offer`; `TradeRepo.Accept`
inserta las dos en la transacción de la aceptación. `DOMAIN` — `CARAVAN` no se entrena y ninguna orden la crea.

**Cómo se verifica.** `TestUnTratoDebitaLosDosAlmacenesYCreaSusCaravanasUnaVez` (integration: dos caravanas, una por
sentido, en la transacción de la aceptación), `Test_INV_TRADE_001_UnTratoConFalloEnUnDeltaNoMueveNada` (integration:
un trato que no se escribe no crea ninguna), `TestLasRestriccionesDeLasCaravanas` (integration: una tercera del mismo
sentido se rechaza), `TestLasCaravanasQueNoCuadranSeSenalanAlArrancar` (integration) y el test de contrato que
rechaza `CARAVAN` en `city.train` — **existen y pasan**.

**Violación en runtime.** La comprobación del arranque (`CaravanRepo.Violations`) señala una caravana cuya oferta no
esté `ACCEPTED` o sin su unidad `CARAVAN` viva, con `inv_id=INV-TRADE-007`, y no la incorpora.

**Precisión (2026-11-18, [SPEC-MARKETS](../specs/markets.md) §4.6, [ADR-047](../decisions/ADR-047-markets.md) §2).**
Desde E26 una caravana nace de un trato aceptado **o de una compra en el mercado**, y cada uno tiene exactamente dos,
creadas en su transacción. Toda fila de `caravans` apunta a exactamente uno de los dos (`offer_id` o
`market_purchase_id`), y la comprobación del arranque mira el que tenga. La ficha queda parcial hasta que los tests de
las caravanas de una compra existan. **Desde el mismo día, cubierta:**
`TestUnaCompraDescuentaLosLotesYCreaSusCaravanasUnaVez` (integration) comprueba las dos caravanas de una compra, con
`market_purchase_id` y sin `offer_id`, su carga y que la comprobación del arranque no señala nada;
`TestLasRestriccionesDelMercado` (integration), que una caravana sin trato ni compra, o con los dos, no entra.

---

<a id="inv-trade-008"></a>
## INV-TRADE-008 — La custodia de una oferta del mercado cuadra

| Campo | Valor |
|---|---|
| Severidad | CRITICO |
| Aplicación | DB, DOMAIN, TEST |
| Milestone | E26 |
| Política ante violación | REJECT (la transacción revierte entera; la RAM se compensa) |
| Cobertura | **Cubierto** por `TestElEjemploDeLaSpec` y `TestRetirarUnaOferta` (`internal/domain/market`), `TestGoldenMercadoPublicarComprarLlegarYCaducar`, `TestRetirarConUnaCompraEnCursoSeRechaza`, `TestUnCierreDescartadoSeReintentaConVersionNueva` y, desde el repaso de E26, `TestUnSaqueoNoTocaLaCustodia`, `TestTrasUnReinicioLaOfertaCaducaEnSuInstanteYDevuelveUnaVez` y `TestTrasUnReinicioUnaCompraSinConfirmarNoQuedaAMedias` (simulation), `Test_INV_TRADE_008_UnaCompraConElDeltaSuperadoNoMueveNada`, `TestCerrarUnaOfertaDevuelveUnaVez`, `TestPublicarEnElMercadoEsIdempotente` y `TestUnaCompraQueFallaAMitadNoDejaNadaYLaCargaCuadra` (integration) |

**Enunciado.** Lo que guarda en custodia una oferta `OPEN` del mercado es exactamente `lots_left × give_per_lot` del
recurso que da, y la de una oferta cerrada es cero. Lo que entra en la custodia sale del almacén del vendedor en la
transacción que inserta la oferta, junto con su comisión; lo que sale de ella va, en la misma transacción que descuenta
sus lotes, a la caravana del vendedor de una compra, o vuelve al almacén del vendedor en la transacción que cierra la
oferta, una sola vez.

**Razón.** La custodia está fuera de `city_stocks` y nadie más la ve: si se pudiera descuadrar, el mercado crearía o
tragaría recursos sin que ningún otro invariante del stock lo notara.

**Cómo se garantiza.** `DB` — la custodia no se guarda aparte (se deriva de `lots_left`), los `CHECK` de
`market_offers` y el cierre condicionado a `status = 'OPEN'`, que no acredita dos veces. `DOMAIN` — la publicación, la
compra y el cierre calculan sus deltas de la misma oferta (ADR-047 §1-§3).

**Cómo se verifica.** `TestElEjemploDeLaSpec` (domain: la custodia del ejemplo, 60, y 20 tras vender 2 lotes; cero al
cerrarse), el golden `TestGoldenMercadoPublicarComprarLlegarYCaducar` (simulation: la custodia sale del almacén con su
comisión, va a la caravana del vendedor y el lote que queda vuelve al caducar), `TestUnCierreDescartadoSeReintentaConVersionNueva`
(simulation: la RAM acredita una vez), `Test_INV_TRADE_008_UnaCompraConElDeltaSuperadoNoMueveNada` (integration: con el
débito del comprador superado no se descuenta ningún lote ni se crea ninguna caravana) y `TestCerrarUnaOfertaDevuelveUnaVez`
(integration: un cierre repetido no acredita dos veces) — **existen y pasan**. **Precisión (2026-11-19):** el repaso de
E26 suma `TestUnSaqueoNoTocaLaCustodia` (simulation: el saqueo baja el almacén al suelo y la oferta conserva sus lotes),
los dos de recuperación de la simulación (tras un reinicio, la oferta caduca en su instante absoluto y devuelve una vez;
una compra sin confirmar deja o nada o todo, sin reservas fantasma) y `TestUnaCompraQueFallaAMitadNoDejaNadaYLaCargaCuadra`
(integration: un fallo en la última inserción de la compra no deja lotes, débito, compra ni caravanas). Verificado por mutación: publicar sin
cobrar la comisión (`PostCost`), devolver al cerrar la custodia de los lotes publicados en vez de los que quedan
(`Custody`) y cerrar sin la condición `status = 'OPEN'` (`MarketRepo.Close`) ponen en rojo sus tests.

**Violación en runtime.** No hay estado intermedio que detectar: la transacción es la garantía.

---

<a id="inv-trade-009"></a>
## INV-TRADE-009 — Ninguna oferta del mercado vende más lotes de los que publicó

| Campo | Valor |
|---|---|
| Severidad | CRITICO |
| Aplicación | DB, DOMAIN, TEST |
| Milestone | E26 |
| Política ante violación | REJECT |
| Cobertura | **Cubierto** por `TestQuienCompraYCuantosLotes` (`internal/domain/market`), `TestDosComprasEnElMismoTick` (simulation) y `TestUnaCompraDescuentaLosLotesYCreaSusCaravanasUnaVez` (integration) |

**Enunciado.** Para toda oferta del mercado, la suma de los lotes de sus compras más `lots_left` es `lots_total`, con
`0 <= lots_left <= lots_total <= 100`; una compra nunca toma más lotes de los que quedan, ni siquiera dos compras de la
misma oferta en vuelo a la vez.

**Razón.** Vender un lote dos veces sería sacar de la custodia lo que ya no está: recursos creados de la nada en la
caravana del segundo comprador.

**Cómo se garantiza.** `DB` — los `CHECK` de `market_offers` y la compra que descuenta los lotes condicionada a
`lots_left >= k`. `DOMAIN` — la simulación aparta los lotes de cada compra al reservarla y sólo deja comprar los que
quedan sin reservar (`RN-MARKET-009`).

**Cómo se verifica.** `TestDosComprasEnElMismoTick` (simulation: dos compras de 2 lotes de una oferta de 3 en el mismo
tick, la segunda rechazada; después la de 1 la agota y las compras suman 3), `TestQuienCompraYCuantosLotes` (domain) y
`TestUnaCompraDescuentaLosLotesYCreaSusCaravanasUnaVez` (integration: con un lote abierto la base no vende dos, y una
oferta agotada no admite más) — **existen y pasan**. Verificado por mutación: comprar sin descontar los lotes
reservados por otra compra en vuelo (simulation) y descontar los lotes sin la condición `lots_left >= k`
(`MarketRepo.Buy`) ponen en rojo sus tests.

**Violación en runtime.** La carga señala una oferta cuyos lotes no cuadran con sus compras, con
`inv_id=INV-TRADE-009`, y no la incorpora.

---

<a id="inv-trade-010"></a>
## INV-TRADE-010 — Nadie se compra a sí mismo en el mercado

| Campo | Valor |
|---|---|
| Severidad | ALTO |
| Aplicación | DB, DOMAIN, TEST |
| Milestone | E26 |
| Política ante violación | REJECT |
| Cobertura | **Cubierto** por `TestQuienCompraYCuantosLotes` (`internal/domain/market`), `TestRechazosDelMercado` (simulation) y `TestLasRestriccionesDelMercado` (integration) |

**Enunciado.** Toda fila de `market_purchases` tiene un comprador distinto del jugador que publicó su oferta.

**Razón.** Comprarse a uno mismo no mueve nada entre jugadores y sólo crea caravanas y comisiones sin sentido; como
regla de la base, cierra además la puerta a que un error de la simulación las escriba.

**Cómo se garantiza.** `DB` — un trigger sobre `market_purchases`. `DOMAIN` — `market.buy` sobre la oferta propia se
rechaza con `INVALID_TARGET`.

**Cómo se verifica.** `TestQuienCompraYCuantosLotes` (domain), `TestRechazosDelMercado` (simulation: comprar la propia
es `INVALID_TARGET`) y `TestLasRestriccionesDelMercado` (integration: la base rechaza la compra del dueño de la oferta) —
**existen y pasan**. Verificado por mutación: quitar la comprobación del dominio pone en rojo los dos primeros, y el
trigger desactivado con `ALTER TABLE … DISABLE TRIGGER` en la base de tests pone en rojo el tercero (volvió a
`tgenabled = 'O'`).

**Violación en runtime.** El trigger rechaza la transacción entera.

---

## Trazabilidad

| Invariante | Componente propietario | Relacionado con |
|---|---|---|
| INV-TRADE-001 | `postgres.TradeRepo`, `internal/game/simulation` | [INV-ECON-001](economy.md#inv-econ-001), [INV-ECON-002](economy.md#inv-econ-002) |
| INV-TRADE-002 | `internal/game/simulation`, `internal/domain/diplomacy` | [INV-DIPLO-002](diplomacy.md#inv-diplo-002) |
| INV-TRADE-003 | `migrations/`, `internal/game/simulation` | [INV-DIPLO-002](diplomacy.md#inv-diplo-002) |
| INV-TRADE-004 | `internal/domain/trade`, `internal/game/simulation` | [INV-DIPLO-001](diplomacy.md#inv-diplo-001), [INV-SEC-004](security.md#inv-sec-004) |
| INV-TRADE-005 | `internal/domain/caravan`, `postgres.TradeRepo`, `internal/game/loadcheck` | [INV-TRADE-001](#inv-trade-001) |
| INV-TRADE-006 | `postgres` (la entrega), `internal/game/simulation` | [INV-TRADE-001](#inv-trade-001), [INV-ECON-002](economy.md#inv-econ-002) |
| INV-TRADE-007 | `postgres.TradeRepo`, `internal/domain/unit`, `internal/game/loadcheck` | [INV-TRADE-001](#inv-trade-001) |
| INV-TRADE-008 | `postgres` (el mercado), `internal/game/simulation` | [INV-TRADE-001](#inv-trade-001), [INV-ECON-002](economy.md#inv-econ-002) |
| INV-TRADE-009 | `migrations/`, `internal/game/simulation` | [INV-TRADE-008](#inv-trade-008) |
| INV-TRADE-010 | `migrations/`, `internal/domain/market` | [INV-TRADE-004](#inv-trade-004) |
