# Los avisos de la ciudad tomada

Propuesta para que el registro y los avisos en vivo **digan que una ciudad se tomó**, no sólo que cayó: hoy el vencedor
lee «Tus unidades tomaron y arrasaron la ciudad de X» aunque se la quedara, y el vencido «Tu ciudad cayó ante X» sin
saber que X la tiene ahora. Es lo que [city-taken.md](city-taken.md) §9.1 dejó para otro slice.

| Campo | Valor |
|---|---|
| Spec ID | `SPEC-CITY-TAKEN-NOTICES` |
| Estado | Draft (2026-11-25). Las preguntas de §10 esperan la decisión de producto. |
| Milestone | E39 Los avisos de la ciudad tomada (trigésimo noveno slice después de M7) |
| Canon | §13 (protocolo), §14 (registro) |
| Depende de | [city-taken.md](city-taken.md), [event-log.md](event-log.md), [live-notices.md](live-notices.md), [websocket-protocol.md](websocket-protocol.md) |
| Decisiones | Sin ADR ni migración: `CityFell` ya guarda `takenCityId` en su payload (E36). |
| Reemplaza a | La precisión de [city-taken.md](city-taken.md) §9.1 «los textos de los avisos no cambian». |
| Invariantes | No asigna invariantes. Regla nueva: `RN-CITY-042` (§4.2). |

## 1. Objetivo

E36 hizo que el vencedor con hueco se quede la ciudad, pero el registro y los avisos siguen contando la caída de E31:
el vencedor no sabe por el registro que ganó una ciudad, y el vencido no sabe quién la tiene. El dato ya está en la base
(`takenCityId` en el payload de `CityFell`); falta llevarlo a la entrada y al aviso.

### 1.1 Qué hace hoy (lectura del código)

- `eventlog.Classify` da `CITY_LOST` al vencido y `CITY_TAKEN` al vencedor por cada `CityFell`
  (`internal/domain/eventlog/eventlog.go`); la entrada lleva el centro (`x`, `y`) y nada de la toma.
- El aviso en vivo sale de `sendNotices` en `applyFall` (`internal/game/simulation/assault.go`) con el mismo `Event`.
- `entryText` (`apps/web/src/state/eventLog.ts`) escribe «Tu ciudad cayó ante X…» y «Tus unidades tomaron y arrasaron
  la ciudad de X…».

## 2. Scope

**Entra.** `taken` (opcional) en la entrada del registro y en el aviso de `CITY_LOST` y `CITY_TAKEN`, leído de
`takenCityId` (§4.1); los dos textos nuevos (§4.2); los tests.

**No entra.** Un tipo de entrada nuevo (Q1); avisar a terceros; cambiar el texto de la caída sin toma.

## 3. Actores

Vencedor y vencido (leen el registro y el aviso), Game Server (escribe la entrada y el aviso), cliente (el texto).

## 4. Modelo

### 4.1 El dato (Q1, Q2)

`taken: true` en la entrada (`event_log.page`) y en el aviso (`event_log.notice`) de `CITY_LOST` y `CITY_TAKEN` cuando
el `CityFell` lleva `takenCityId`; ausente si se arrasó. Los eventos anteriores a E36 no lo llevan (Q3).

### 4.2 Los textos (Q4)

| Tipo | Con `taken` | Sin él (como hoy) |
|---|---|---|
| `CITY_TAKEN` | «Tomaste la ciudad de X (x, y): ahora es tuya» | «Tus unidades tomaron y arrasaron la ciudad de X (x, y)» |
| `CITY_LOST` | «X tomó tu ciudad: ahora es suya. Sigues en (x, y)…» | «Tu ciudad cayó ante X: la refundaste en (x, y)…» |

| ID | Regla |
|---|---|
| `RN-CITY-042` | La entrada y el aviso de una caída dicen si el vencedor se quedó la ciudad (`taken`), con su propio texto para los dos lados. |

## 5. Errores

Ninguno nuevo.

## 6. Eventos y contratos de red

`taken` (booleano, opcional) en `EventLogEntry` y en el payload de `event_log.notice`. Aditivo: un cliente viejo lo
ignora y escribe el texto de la caída.

## 7. Persistencia

Sin cambios: el dato sale del payload de `CityFell`.

## 8. Invariantes

No asigna invariantes.

## 9. Tests esperados

| Nivel | Qué |
|---|---|
| Unit (`internal/domain/eventlog`) | La entrada de `CITY_LOST` y `CITY_TAKEN` con `taken` cuando el evento lo trae, y sin él si no. |
| Simulación | El aviso de una toma lleva `taken` a los dos. |
| Integración | La página del registro lee `taken` del payload de `CityFell`. |
| Contract | `taken` en Zod y en Go. |
| Cliente (Vitest) | Los dos textos nuevos y los de siempre. |

## 10. Preguntas de producto

Cada fila deja la recomendación del agente (en negrita) y las alternativas descartadas.

| # | Pregunta | Recomendación | Descartadas |
|---|---|---|---|
| Q1 | ¿Un tipo nuevo o un campo? | **Un campo `taken` en los tipos de siempre**: la caída es la misma; cambia el desenlace. | **`CITY_CAPTURED` y `CITY_SEIZED`** (dos valores nuevos de un enum estricto, y dos textos más en cada sitio). |
| Q2 | ¿Booleano o la ciudad tomada? | **Booleano**: el texto no necesita el id; la tomada ya está en el mapa. | **`takenCityId`** (un id que el cliente no usa en el texto). |
| Q3 | ¿Y las caídas de antes de E36? | **Sin `taken`, con el texto de siempre.** | **Reescribir los payloads viejos** (una migración para nada: antes no se tomaba). |
| Q4 | ¿Qué dicen? | **Lo de §4.2**: el vencedor, que ahora es suya; el vencido, quién la tiene y dónde sigue. | **Sólo el del vencedor** (el vencido seguiría creyendo que se arrasó). |
