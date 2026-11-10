# Bienes de civilización: lo que sólo tu civilización produce y los demás necesitan

La civilización crea dependencia entre jugadores: cada una elabora en su taller un bien que
ninguna otra puede producir, y la Edad de Hierro exige bienes de otras civilizaciones. Para progresar hay que
comerciar.

| Campo | Valor |
|---|---|
| Spec ID | `SPEC-CIVILIZATION-GOODS` |
| Estado | Implemented (2026-11-10). Propuesta en `Draft` y aceptada el 2026-11-06, con la recomendación de cada pregunta de §10; en `main` desde la fusión `da11ac2`, con la CI en verde. |
| Milestone | E14 Bienes (decimocuarto slice después de M7) |
| Canon | §0 (civilización como identidad), §10 (eras y población), §11 (catálogos), §12 (persistencia), §13 (protocolo), §16 (errores) |
| Depende de | [civilizations.md](civilizations.md), [economy-wood.md](economy-wood.md), [economy-stone.md](economy-stone.md), [trade.md](trade.md), [combat-siege.md](combat-siege.md), [combat-pillage.md](combat-pillage.md), [city.md](city.md), [websocket-protocol.md](websocket-protocol.md) |
| Decisiones | [ADR-023](../decisions/ADR-023-economy-stock-deltas-and-two-phase-orders.md) (encargos en dos fases y deltas de stock), [ADR-029](../decisions/ADR-029-population-over-limit-and-house-destruction.md) (el asedio de un edificio), [ADR-030](../decisions/ADR-030-stone-layer-and-era-advance.md) (el avance de era como encargo con su fila), [ADR-032](../decisions/ADR-032-trade-offers-and-two-key-jobs.md) (el trato que mueve dos almacenes), [ADR-035](../decisions/ADR-035-civilization-traits.md) (los rasgos de civilización). [ADR-036](../decisions/ADR-036-civilization-goods.md) (el bien de cada civilización como recurso de catálogo, el taller y la elaboración como encargo con su fila, y el coste en bienes ajenos; migración `000016`). |
| Reemplaza a | En [economy-stone.md](economy-stone.md), la Q6 («sólo de Piedra a Bronce»): desde E14 hay avance de Bronce a Hierro. En [trade.md](trade.md), la Q4 en lo que dice de que sólo se comercia madera y piedra. En [economy-wood.md](economy-wood.md) §4.5, que la casa es el único edificio. Responde a la Q6 de [civilizations.md](civilizations.md) («capacidades exclusivas en el siguiente slice») y, en parte, a la `Q-12` del [backlog](../roadmap/backlog.md) (cómo se avanza más allá de Bronce). |
| Invariantes | Cita [INV-ECON-001](../invariants/economy.md#inv-econ-001), [INV-ECON-002](../invariants/economy.md#inv-econ-002), [INV-ECON-005](../invariants/economy.md#inv-econ-005), [INV-CITY-018](../invariants/city.md#inv-city-018), [INV-TRADE-001](../invariants/trade.md#inv-trade-001) y [INV-PLAYER-012](../invariants/player.md#inv-player-012); asigna [INV-ECON-009](../invariants/economy.md#inv-econ-009), [INV-ECON-010](../invariants/economy.md#inv-econ-010) e [INV-ECON-011](../invariants/economy.md#inv-econ-011) (§8) |

## 1. Objetivo

Que exista lo que el pilar 4 de [game-pillars.md](../product/game-pillars.md) pide y E13 dejó para después: que
ningún jugador sea autosuficiente porque las civilizaciones tienen **capacidades** distintas, no sólo números
distintos. Hoy la civilización se elige para siempre y da bonos ([civilizations.md](civilizations.md)), pero
cualquier jugador puede hacer todo solo: la madera y la piedra están en casi todo el mapa, y la Edad de Bronce se
paga con ellas. El comercio de E10 existe, pero es opcional. El anti-pilar rechaza la escasez artificial: la
dependencia tiene que venir de lo que cada civilización sabe hacer, que es legible, estable y elegido.

## 2. Scope

**Entra.** Un bien por civilización, cada uno un recurso nuevo del almacén (vino, seda, especias y pieles, Q2); un
edificio nuevo, el **taller**, que sólo elabora el bien de la civilización de su dueño; la orden nueva `city.craft`,
que paga madera y piedra y, al cabo de un plazo, deja bienes en el almacén; el avance de **Bronce a Hierro**, que pide
bienes de otras civilizaciones; los bienes en el comercio (E10), en el almacén visible y en el registro de los tratos;
los rasgos de E13 sobre el taller y la elaboración; en el cliente, el botón «Taller», el botón «Elaborar» con su
cuenta atrás, los bienes en el HUD y en el formulario de trato, y el avance a Hierro con su motivo; el humo `--bienes`.

**No entra** (`Fuera de MVP` de este slice): unidades, edificios o tecnologías exclusivos además del bien (Q1); la
Edad de los Castillos (Q11); otros usos de los bienes (mantenimiento, entrenamiento, construcción); saquear bienes
(Q9); producir sin dar la orden (Q3); más de un taller por ciudad; mercados sin tratado, caravanas o precios fijados
por el servidor; cambiar el bien de una civilización sin migración.

## 3. Actores

Jugador (construye el taller, ordena elaborar, comercia, avanza), otro jugador con otra civilización (vende o compra
bienes), Game Server (valida las órdenes, lleva los plazos, cobra el avance, [INV-SEC-001](../invariants/security.md#inv-sec-001)),
tick loop (fase 2 para las órdenes, fase 5 para los plazos), PostgreSQL (almacén, taller, elaboración, avance).

## 4. Modelo

### 4.1 El bien de cada civilización

Cada civilización tiene **un bien propio**, un recurso más del almacén que sólo su taller produce (Q2):

| Civilización | Bien | Código |
|---|---|---|
| `ROMAN` | Vino | `WINE` |
| `BYZANTINE` | Seda | `SILK` |
| `PERSIAN` | Especias | `SPICES` |
| `NORSE` | Pieles | `FURS` |

Qué bien es de qué civilización es un dato del catálogo (`civilizations`), no un condicional en el código
(`RN-PLAYER-012`): añadir una civilización es añadir su fila y su recurso. Los bienes viven en `city_stocks` como la
madera y la piedra, empiezan a cero y no tienen capacidad. Un bien **ajeno** es cualquiera que no sea el de la
civilización de la ciudad.

Los bienes propios no sirven a su productor para nada más que para comerciar (Q8): son su moneda. Los ajenos sólo
se gastan en la Edad de Hierro, en este slice.

### 4.2 El taller

El taller es un edificio de la zona urbana, como la casa ([economy-wood.md](economy-wood.md) §4.5), con estas
reglas (Q6):

- Se construye con `city.build { type: "WORKSHOP" }` en un tile libre de la zona urbana.
- Pide la **Edad de Bronce**: es lo que viene después del primer avance.
- **Uno por ciudad** en pie (en obra o completo); si se derriba, se puede volver a construir.
- Cuesta **100 de madera y 50 de piedra** y tarda **45 s**, con el rasgo `production.speed` de E13.
- No da población. Tiene **200 de vida** y se puede derribar con un asedio como una casa (Q10); derribarlo no
  devuelve nada y corta la elaboración en curso, que se pierde.

### 4.3 Elaborar

`city.craft { cityId }` es una orden en dos fases ([ADR-023](../decisions/ADR-023-economy-stock-deltas-and-two-phase-orders.md)),
como entrenar (Q7):

- Pide un taller **completo** en la ciudad y ninguna elaboración en curso.
- Cuesta **30 de madera y 15 de piedra**, que se debitan al confirmarse ([INV-ECON-002](../invariants/economy.md#inv-econ-002)).
- Tarda **30 s**, con el rasgo `production.speed` de E13 (27 273 ms un romano, 33 333 un nórdico).
- Al vencer, suma **5** del bien de la civilización de la ciudad a su almacén.
- Es independiente del entrenamiento y del avance de era: un taller elabora mientras la ciudad entrena o avanza.
- Lo que está a medias termina tras un reinicio en el mismo instante absoluto ([INV-ECON-005](../invariants/economy.md#inv-econ-005)).

### 4.4 La Edad de Hierro

`city.advance` desde la Edad de Bronce pasa a la **Edad de Hierro**, con el mismo encargo que el primer avance
([ADR-030](../decisions/ADR-030-stone-layer-and-era-advance.md)) y estos números (Q4, Q5):

- **800 de madera, 400 de piedra y 60 bienes ajenos**, en cualquier mezcla de los bienes de otras civilizaciones.
- Los bienes ajenos se cobran en el orden del catálogo de civilizaciones (vino, seda, especias, pieles), saltándose
  el propio, hasta sumar 60. El propio nunca cuenta.
- Dura **180 s** y la ciudad no entrena mientras avanza (como el primer avance, `RN-ECON-026`); el taller sigue.
- Al llegar, el límite de población sube de 50 a 100 (el `population_cap` de `IRON_AGE`, que existe desde `000002`).

Con un solo bien propio por civilización, ningún jugador puede pasar de Bronce sin haber comerciado con alguien de
otra civilización. Con 60 en cualquier mezcla basta con un socio, así que un mundo donde falte una civilización no
bloquea a nadie (Q4).

### 4.5 Reglas

| ID | Regla |
|---|---|
| `RN-GOODS-001` | Cada civilización tiene exactamente un bien, del catálogo; dos civilizaciones no comparten bien. |
| `RN-GOODS-002` | Un taller sólo elabora el bien de la civilización del dueño de su ciudad. Ningún otro camino crea un bien: ni la recolección, ni el saqueo, ni el botín. |
| `RN-GOODS-003` | El taller pide la Edad de Bronce, un tile libre de la zona urbana y que la ciudad no tenga otro en pie. |
| `RN-GOODS-004` | `city.craft` pide un taller completo y ninguna elaboración en curso; cobra al confirmarse y entrega al vencer el plazo. |
| `RN-GOODS-005` | La elaboración no bloquea el entrenamiento ni el avance, ni ellos a ella. |
| `RN-GOODS-006` | El avance de Bronce a Hierro cobra madera, piedra y 60 bienes ajenos, nunca el propio, en el orden del catálogo. |
| `RN-GOODS-007` | Derribar el taller pierde la elaboración en curso, sin devolver lo pagado. |
| `RN-GOODS-008` | Los bienes se comercian como la madera y la piedra (E10) y se ven en el almacén como ellas; el saqueo no se los lleva. |

### 4.6 Qué ve el jugador

- **HUD.** Los bienes que tiene la ciudad, junto a la madera y la piedra, con el propio primero.
- **Taller.** Un botón «Taller» como «Casa», deshabilitado con su motivo (falta Bronce, ya hay uno, sin hueco, sin
  recursos). Con el taller completo, «Elaborar (5 de vino)», con su cuenta atrás mientras elabora.
- **Edad de Hierro.** El botón de avance dice qué falta, también cuántos bienes ajenos.
- **Comercio.** El formulario «Ofrecer trato» admite los bienes en los dos lados.
- **Ciudad ajena.** Sus bienes en el almacén visible, con los demás recursos.

## 5. Errores

Sin códigos nuevos (canon §16):

| Orden | Condición | Código |
|---|---|---|
| `city.build` `WORKSHOP` | La ciudad no es propia | `CITY_NOT_FOUND` |
| | Antes de la Edad de Bronce, o ya hay un taller en pie | `INVALID_TARGET` |
| | El tile no es libre o no es de la zona urbana | `INVALID_TARGET` |
| | Falta madera o piedra | `INSUFFICIENT_RESOURCES` |
| `city.craft` | La ciudad no es propia | `CITY_NOT_FOUND` |
| | Sin taller completo | `INVALID_TARGET` |
| | Ya elabora | `CITY_BUSY` |
| | Falta madera o piedra | `INSUFFICIENT_RESOURCES` |
| `city.advance` desde Bronce | Falta madera, piedra o bienes ajenos | `INSUFFICIENT_RESOURCES` |
| `city.advance` desde Hierro | No hay más eras | `NOT_IMPLEMENTED` |

## 6. Eventos y contratos de red

**Eventos.** `CraftOrdered` y `CraftCompleted` en `world_events`, con el jugador y su ciudad; no implican a otro
jugador, así que no entran en el registro ni en los avisos. El avance a Hierro reutiliza `EraAdvanceOrdered` y
`EraAdvanced`. El taller usa los eventos de los edificios.

**WebSocket (protocolo v1).** Un mensaje cliente → servidor nuevo, `city.craft { cityId }` (24 en total), clasificado
como acción propia. Ningún mensaje servidor → cliente nuevo: el estado de la elaboración viaja como `craft` en
`CityView` y en `city.update` (`{ completesAtMs }` o `null`), y los bienes como claves nuevas de `Stocks` (`WINE`,
`SILK`, `SPICES`, `FURS`). `BuildingType` gana `WORKSHOP`. Las líneas de un trato admiten los seis recursos. Todo es
aditivo (`R-VER-2`, `R-VER-3`).

**Catálogo de presentación.** El de [civilizations.md](civilizations.md) §6 gana el bien de cada civilización, para
que el formulario de registro diga qué producirá el jugador.

## 7. Persistencia

| Pregunta (canon §12) | Respuesta |
|---|---|
| Autoritativo en RAM | La elaboración en curso de cada ciudad y su plazo, como el entrenamiento; los stocks, como hasta ahora |
| Write-through | El taller (su encargo, su fin y su derribo), la elaboración (encargo y entrega) y el avance, cada uno en su transacción con su delta de stock |
| Dirty-flag | Nada nuevo |
| Reconstruible / no persistido | Qué bien elabora cada taller: sale de la civilización del dueño y del catálogo |

**Por qué hace falta un ADR ([ADR-036](../decisions/ADR-036-civilization-goods.md)).** Tres cosas nuevas que fijan dónde vive cada dato:

1. **El bien como recurso de catálogo.** Cuatro recursos nuevos a la vez en `city_stocks`, en las líneas de los
   tratos, en `Stocks` de Zod y de Go y en `economy.Resources()`; y una columna `civilizations.good` que dice cuál
   es de quién, para que el taller no compare códigos de civilización.
2. **La elaboración como encargo con su fila**, `city_crafts`, como `city_productions`: una por ciudad en curso,
   idempotente por `request_id`, que se carga al arrancar.
3. **Un coste que no es una lista fija de recursos**: «60 bienes ajenos en cualquier mezcla». El dominio lo resuelve
   en una lista concreta de deltas, en el orden del catálogo, en el tick que reserva el avance; la transacción sigue
   cobrando deltas versionados como hoy.

El ADR deja escritas, entre otras, estas alternativas descartadas: una unidad exclusiva por civilización (Q1); un recurso de terreno por
civilización, que obligaría a cambiar el mapa canónico y ataría la capacidad a la geografía, que el pilar rechaza;
la producción pasiva del taller, que escribe el stock en cada tick o pide un volcado periódico de algo que nadie
ordenó; y un coste fijo de 20 de cada uno de los otros tres bienes, que bloquea a todos si falta una civilización.

**Migración `000016`:** `WINE`, `SILK`, `SPICES` y `FURS` en los `CHECK` de `city_stocks` y de `trade_offer_lines`, con
su fila a cero para cada ciudad; `civilizations.good` con su valor para las cuatro; `WORKSHOP` en
`buildings_type_valid` y un único parcial de un taller en pie por ciudad; `city_crafts`. El avance a `IRON_AGE` no
necesita esquema: `city_era_advances` ya admite cualquier era del catálogo.

## 8. Invariantes

**Cita** [INV-ECON-001](../invariants/economy.md#inv-econ-001) (ningún bien en negativo),
[INV-ECON-002](../invariants/economy.md#inv-econ-002) (el taller, la elaboración y el avance debitan en la misma
transacción que su efecto), [INV-ECON-005](../invariants/economy.md#inv-econ-005) (la elaboración a medias termina
en su instante), [INV-CITY-018](../invariants/city.md#inv-city-018) (de Bronce a Hierro, nunca hacia atrás),
[INV-TRADE-001](../invariants/trade.md#inv-trade-001) (un trato con bienes tampoco crea ni destruye nada) e
[INV-PLAYER-012](../invariants/player.md#inv-player-012) (el rasgo de producción sobre el taller y la elaboración).

**Asigna** tres invariantes nuevos de la familia `INV-ECON`, registrados en [../invariants/economy.md](../invariants/economy.md):

- [INV-ECON-009](../invariants/economy.md#inv-econ-009) — un bien sólo nace en el taller de la civilización que lo
  produce (`RN-GOODS-002`).
- [INV-ECON-010](../invariants/economy.md#inv-econ-010) — una ciudad tiene como mucho un taller en pie y una
  elaboración en curso (único parcial y clave de `city_crafts`).
- [INV-ECON-011](../invariants/economy.md#inv-econ-011) — el avance a la Edad de Hierro nunca cobra el bien de la
  civilización de la ciudad.

## 9. Tests esperados

| Nivel | Qué |
|---|---|
| unit | El catálogo de bienes (uno por civilización, sin repetir); el predicado del taller y de la elaboración con cada motivo de §5; la resolución de los 60 bienes ajenos en el orden del catálogo, saltando el propio, con mezclas que llegan y que no; los plazos con `production.speed` |
| simulation | Golden «bienes»: un romano construye el taller (45 s × rasgo) y elabora 5 de vino a los 27 273 ms; un nórdico, 5 de pieles a los 33 333; la elaboración sigue mientras la ciudad entrena; un trato vino por pieles entre los dos; el avance a Hierro cobra 60 ajenos y no el propio, y a los 180 s sube el límite a 100; derribar el taller pierde la elaboración; el saqueo no se lleva bienes |
| integration | Los `CHECK` y el único parcial; el encargo y la entrega de la elaboración en su transacción; el cobro del avance con varios bienes; un trato con bienes; la carga recupera la elaboración |
| recovery | Una elaboración a medias termina tras un reinicio en su instante; una que venció durante la caída se entrega al arrancar |
| contract / e2e | `city.craft`, `craft` en `CityView` y `city.update`, `WORKSHOP` y los cuatro recursos contra el JSON Schema; un e2e de elaborar por la red |
| cliente | Los botones con sus motivos, la cuenta atrás, los bienes en el HUD y en el formulario de trato, el texto del avance; `scripts/smoke.mjs --bienes` (smoke_runner, romano, y smoke_norse, nórdica, elaboran y se cambian vino por pieles) |

## 10. Decisiones de producto (2026-11-06)

Preguntas que la propuesta dejó abiertas; producto aceptó la recomendación de cada una. Son decisiones de producto,
no de ingeniería: cambiarlas es cambiar la spec.

| # | Pregunta | Decisión |
|---|---|---|
| Q1 | ¿Qué forma toma la capacidad exclusiva? | **Un bien que sólo tu civilización produce y que los demás necesitan para progresar.** Es lo que convierte el comercio en necesario: el pilar 4 quiere que la dependencia venga de capacidades, y un bien se cambia por otro. Alternativas: **una unidad exclusiva** por civilización (legionario, catafracto…) es más vistosa pero sólo crea diferencias militares, nadie necesita la unidad de otro; **una tecnología exclusiva** necesita antes un árbol tecnológico, que no existe. |
| Q2 | ¿Qué bien produce cada una? | **Vino (romanos), seda (bizantinos), especias (persas) y pieles (nórdicos).** Son legibles, históricos y no se confunden con la madera y la piedra. Los nombres son un dato del catálogo y se pueden cambiar. |
| Q3 | ¿Cómo se produce? | **Con una orden explícita en un taller**, `city.craft`, que paga madera y piedra y entrega al cabo de un plazo, como entrenar. Alternativas: **producción pasiva** del taller (sin órdenes) es más cómoda pero crea recursos mientras el jugador no está y pide escribir el stock sin que nadie lo ordene; **un recurso de terreno por civilización** ataría la capacidad a dónde fundaste, que no se elige (Q2 de [economy-stone.md](economy-stone.md)). |
| Q4 | ¿Qué bienes pide la Edad de Hierro? | **60 bienes ajenos en cualquier mezcla**: basta con comerciar con una sola civilización distinta, y un mundo sin persas no bloquea a nadie. Alternativa: **20 de cada uno de los otros tres** obliga a tratar con todas, pero si falta una civilización en el mundo nadie puede avanzar. |
| Q5 | ¿Cuánto cuesta y qué da la Edad de Hierro? | **800 de madera, 400 de piedra y 60 bienes ajenos, 180 s, sin entrenar mientras dura; sube el límite de 50 a 100.** El doble que el primer avance más los bienes. No desbloquea nada más en este slice: la población es lo que ya da la era. |
| Q6 | ¿Cómo es el taller? | **Pide Bronce, uno por ciudad, 100 de madera y 50 de piedra, 45 s, sin población, 200 de vida.** Pedir Bronce ordena la progresión (Piedra → Bronce → taller → comercio → Hierro); uno por ciudad evita que el más rico multiplique su producción. |
| Q7 | ¿Cómo es la elaboración? | **30 de madera y 15 de piedra por 5 del bien, 30 s, una a la vez, sin bloquear el entrenamiento ni el avance.** 60 bienes son 12 elaboraciones, seis minutos de taller y 360 de madera y 180 de piedra de quien los produce: un precio que se negocia. |
| Q8 | ¿Sirven los bienes propios para algo? | **No, sólo para comerciar.** Si sirvieran para el propio avance, no harían falta los demás. |
| Q9 | ¿Se saquean los bienes? | **No en este slice.** El saqueo sigue llevándose madera, como decidieron la Q8 de E9 y la de E10; robar bienes cambiaría el equilibrio del saqueo sin datos. |
| Q10 | ¿Se puede derribar el taller? | **Sí, con un asedio como una casa** (200 de vida), y se pierde la elaboración en curso. Es la forma de presionar a un vecino que acapara un bien; volver a construirlo cuesta lo mismo. |
| Q11 | ¿Más eras en este slice? | **No: sólo de Bronce a Hierro.** `CASTLE_AGE` sigue sin coste y `city.advance` desde Hierro responde `NOT_IMPLEMENTED`, como Bronce hasta ahora. |
| Q12 | ¿Aplican los rasgos de E13? | **Sí, `production.speed` al taller y a la elaboración**: son obra y producción. Así el romano elabora un 10 % más rápido y el nórdico un 10 % más lento. Ningún rasgo cambia cuántos bienes salen. |

## 11. Documentos relacionados

- [civilizations.md](civilizations.md) (la civilización, sus rasgos y la Q6 que deja este slice) y
  [../product/game-pillars.md](../product/game-pillars.md) (pilar 4 y su anti-pilar).
- [economy-wood.md](economy-wood.md) (la casa, el entrenamiento), [economy-stone.md](economy-stone.md) (la piedra y
  el primer avance), [trade.md](trade.md) (los tratos).
- [combat-siege.md](combat-siege.md) (derribar un edificio) y [combat-pillage.md](combat-pillage.md) (lo que se roba).
- [../roadmap/backlog.md](../roadmap/backlog.md) (`Q-12`).
