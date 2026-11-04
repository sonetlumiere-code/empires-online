# Avisos en vivo: lo que te pasa fuera de la vista

Propuesta de avisos empujados por el servidor mientras juegas: cuando una unidad tuya muere, o hace algo sola, en
una parte del mapa que no estás mirando, te enteras en el acto, con un aviso en el HUD que dice qué pasó y dónde.

| Campo | Valor |
|---|---|
| Spec ID | `SPEC-LIVE-NOTICES` |
| Estado | Draft (2026-11-04). Propuesta: **prohibido implementar** hasta que las preguntas de §10 estén decididas y la spec pase a `Accepted`. |
| Milestone | E11 Avisos (undécimo slice después de M7) |
| Canon | §13 (protocolo e interest management), §16 (errores) |
| Depende de | [event-log.md](event-log.md), [combat-raid.md](combat-raid.md), [combat-response.md](combat-response.md), [combat-pillage.md](combat-pillage.md), [combat-siege.md](combat-siege.md), [websocket-protocol.md](websocket-protocol.md) |
| Decisiones | [ADR-024](../decisions/ADR-024-combat-simultaneous-resolution.md) (la muerte se decide en la RAM y se anuncia antes de escribirse), [ADR-028](../decisions/ADR-028-event-log-from-world-events.md) (el registro sale de `world_events`). Un ADR nuevo (`ADR-033`, el aviso como mensaje efímero sin `id`) acompañará a la aceptación: ver §7. |
| Reemplaza a | La Q5 de [event-log.md](event-log.md) («¿Avisa en vivo mientras juegas? No en este slice») y `RN-LOG-007` en su segunda mitad («el servidor no empuja nada»). |
| Invariantes | Cita [INV-SEC-009](../invariants/security.md#inv-sec-009) (sólo los sucesos en los que es parte) e [INV-SEC-001](../invariants/security.md#inv-sec-001); no asigna ninguno nuevo (§8) |

## 1. Objetivo

Que el riesgo lejos de casa se note cuando ocurre. Desde E9 las expediciones largas son lo normal: un aldeano camina
160 tiles hasta la colina y, si lo matan allí, su dueño no se entera hasta que recarga o vuelve a la pestaña tras un
minuto fuera ([event-log.md](event-log.md) `RN-LOG-007`). La muerte sólo se anuncia al chunk donde ocurre
(`entity.despawn DEAD`), y quien mira su ciudad no está suscrito a la colina. Es el pilar de riesgo de Argentum: perder
algo tiene que doler, y para doler hay que saberlo.

## 2. Scope

**Entra.** Un mensaje servidor → cliente nuevo, `event_log.notice`, que el servidor manda al jugador en el tick en que
pasa algo que el registro le contaría y que hoy no le llega por otra vía (§4.1); en el cliente, un aviso en el HUD con
su texto, agrupado como en el registro, con un botón para llevar la cámara al sitio, y un punto en el botón
«Registro» que dice que hay novedades; el contador en el título de la pestaña.

**No entra** (`Fuera de MVP` de este slice): notificaciones del navegador o del sistema (Q7), sonido, correo o avisos
con la sesión cerrada (para eso está «Mientras no estabas»); avisos de los sucesos que ya tienen su aviso o su
mensaje (saqueo y asedio sufridos, tratados, tratos, Q1); avisos de lo que el propio jugador ordenó en la diplomacia;
los sucesos de E9 (colina agotada, avance terminado, Q8); que un tercero reciba avisos de lo que les pasa a otros;
guardar los avisos o reenviarlos al reconectar (Q6).

## 3. Actores

Jugador conectado (recibe el aviso), cliente web (lo muestra), Game Server (decide qué pasó y a quién se le cuenta,
[INV-SEC-001](../invariants/security.md#inv-sec-001)), tick loop (la fase 4 decide las muertes, los golpes de saqueo y
las casas derribadas).

## 4. Modelo

### 4.1 Qué se avisa

Sólo lo que hoy no llega en vivo a quien está mirando otra parte del mapa (Q1). Cada aviso corresponde a un tipo de
entrada del registro, desde el punto de vista de quien lo recibe:

| Suceso | Tipo | A quién | Por qué no llega hoy |
|---|---|---|---|
| Una unidad tuya muere | `UNIT_LOST` | Al dueño de la víctima | `entity.despawn DEAD` va sólo al chunk |
| Tus unidades matan a una ajena | `ENEMY_KILLED` | Al dueño del asesino | Ídem; con la respuesta automática de E3 pasa sin orden |
| Tus guerreros roban madera | `PILLAGE_DONE` | Al dueño del saqueador | La carga (`entity.update`) va al chunk |
| Tus guerreros derriban una casa | `HOUSE_DESTROYED` | Al dueño del atacante | `building.destroyed` va al chunk |

Lo que ya tiene su aviso no se repite: el saqueo y el asedio sufridos llevan `pillagedAtMs` y `besiegedAtMs` al dueño
de la ciudad esté donde esté, y los tratados y los tratos llegan por `treaty.update` y `trade.update` a la barra de
diplomacia.

### 4.2 Reglas

Dominio: `RN-NOTICE`.

| ID | Regla |
|---|---|
| `RN-NOTICE-001` | **Cuándo.** El aviso sale en el tick en que la simulación decide el suceso (la fase 4 para los tres de combate), por `SendToPlayer` a todas las sesiones del jugador, esté mirando donde esté. No espera a la base: es el mismo momento en que el chunk recibe `entity.despawn` o `building.destroyed` ([ADR-024](../decisions/ADR-024-combat-simultaneous-resolution.md)). Un golpe de saqueo cuya transacción falla después de forma definitiva ([ADR-026](../decisions/ADR-026-pillage-two-phase-stock-debit.md) §1) ya se avisó y no se desavisa: es raro, y el registro, que sale de la base, no lo contará. |
| `RN-NOTICE-002` | **A quién.** Sólo al jugador del que habla el aviso (§4.1), nunca a un tercero ([INV-SEC-009](../invariants/security.md#inv-sec-009)). Si los dos lados de un suceso tienen aviso (una muerte: `UNIT_LOST` para uno y `ENEMY_KILLED` para el otro), cada uno recibe el suyo. |
| `RN-NOTICE-003` | **Qué lleva.** Lo mismo que una entrada del registro sin agrupar y sin `id`: el tipo, el instante en la simulación, el otro jugador con su ciudad, y según el tipo la unidad (`unitType`), la madera (`amount`) y el tile (`x`, `y`). Nunca nada que el jugador no vería en el registro. |
| `RN-NOTICE-004` | **No es una entrada del registro.** El aviso no tiene `id`, no mueve el cursor de lo visto y el cliente no lo mete en la lista del registro: la lista sigue saliendo sólo de `event_log.page` ([ADR-028](../decisions/ADR-028-event-log-from-world-events.md)). Al abrir el registro con avisos sin leer, el cliente pide la primera página. |
| `RN-NOTICE-005` | **Sin sesión no hay aviso.** No se guarda ni se reenvía al reconectar (Q6): lo que pasó con la sesión cerrada lo cuenta «Mientras no estabas». |
| `RN-NOTICE-006` | **Agrupación en el cliente.** Los avisos del mismo tipo con el mismo otro jugador se juntan en uno mientras se ve, con su recuento («Villa Norte mató a 3 de tus aldeanos»), como el registro (Q3). Un saqueo, que da un aviso por golpe, es un solo aviso que crece. |

## 5. Errores

Ninguno nuevo: el aviso no responde a ningún comando y no puede fallar del lado del jugador.

## 6. Eventos y contratos de red

Servidor → cliente: `event_log.notice`, tipo nuevo (R-VER-3), por `SendToPlayer` y sin `requestId`:

```ts
payload: z.object({
  kind: z.enum(['UNIT_LOST', 'ENEMY_KILLED', 'PILLAGE_DONE', 'HOUSE_DESTROYED']),
  atMs: EpochMs,
  otherPlayerId: Uuid,
  otherCityId: EntityId.nullable(),
  otherCityName: z.string().min(1).max(64).nullable(),
  unitType: UnitType.optional(),   // en las muertes
  amount: z.number().int().min(0).optional(), // en el saqueo
  x: WorldCoordinate.optional(),   // dónde ocurrió
  y: WorldCoordinate.optional(),
})
```

El catálogo servidor → cliente pasa de 22 a 23 tipos. Ningún comando nuevo y ningún evento de dominio nuevo: los
hechos ya se escriben en `world_events` (`UnitKilled`, `CityPillaged`, `BuildingDestroyed`) y el registro los sigue
leyendo de ahí.

Cliente: el aviso aparece en una franja del HUD con su texto (el mismo `entryText` del registro) durante unos
segundos (Q2), hasta tres a la vez, con «Ir» cuando trae tile, que centra la cámara allí (Q4). El botón «Registro»
gana un punto mientras haya avisos sin abrir, y el título de la pestaña un contador, «(2) Empires Online», que se
borra al volver a ella (Q7).

## 7. Persistencia

| Pregunta (canon §12) | Respuesta |
|---|---|
| Autoritativo en RAM | Nada nuevo: el aviso sale de lo que la simulación acaba de decidir |
| Write-through | Nada nuevo: los hechos se siguen escribiendo con su evento, como hoy |
| Dirty-flag | Nada |
| Reconstruible / no persistido | El aviso entero: es efímero, no se guarda ni se reenvía (`RN-NOTICE-005`) |

**Por qué hace falta un ADR (`ADR-033`).** La Q5 de [event-log.md](event-log.md) descartó los avisos en vivo porque
«empujar cada suceso al instante obliga a darle un `id` antes de que la base lo asigne», el mismo problema que
`EO-120` tuvo con los movimientos. La propuesta lo evita en lugar de resolverlo: el aviso **no es una entrada**
(`RN-NOTICE-004`), así que no necesita `id`, no toca el cursor y no se mezcla con las páginas del registro. El ADR dejará
escritas las alternativas:

1. **Empujar la entrada tras el `COMMIT`**, con su `id`, por el buzón del loop. Exige que cada transacción que escribe
   un evento del registro (muerte, golpe de saqueo, casa derribada) devuelva el `id` y avise; el aviso llega unas
   decenas de milisegundos tarde y, si la escritura se reintenta, más. A cambio, el cliente podría meterla en la lista.
   No compensa: la lista ya se refresca al abrirla.
2. **Que el cliente pida la página en cuanto llegue algo** (un «hay novedades» sin contenido). Una consulta a la base
   por cada golpe de saqueo, y la fila puede no estar escrita todavía cuando la pide.
3. **Suscribir al dueño a los chunks de sus unidades.** Le llegaría todo lo de esos chunks, incluido lo que no le
   importa, y rompe el área de interés de 5 × 5 (canon §13).

**Sin migración.** Ningún cambio de esquema.

## 8. Invariantes

**Cita** [INV-SEC-009](../invariants/security.md#inv-sec-009) (el registro de un jugador sólo cuenta sucesos en los que
es parte), que el aviso amplía con el mismo enunciado: un jugador sólo recibe avisos de sucesos en los que es parte; y
[INV-SEC-001](../invariants/security.md#inv-sec-001) (el aviso no acepta nada del cliente).

**No asigna** invariantes nuevos: el aviso no es estado. Lo que debe cumplir (que no llegue a un tercero, que no se
repita lo que ya avisa otro mensaje) son reglas que verifican los tests de §9. Al aceptar, la ficha de `INV-SEC-009`
gana una precisión que extiende su enunciado a los avisos.

## 9. Tests esperados

| Nivel | Qué |
|---|---|
| unit | La traducción de cada suceso a su aviso y a su destinatario, desde los dos lados de una muerte; que un suceso sin otro jugador no da aviso |
| simulation | Un aldeano muere lejos de la vista de su dueño: el dueño recibe `UNIT_LOST` con el tile y el asesino `ENEMY_KILLED`, en el mismo tick que `entity.despawn`; un tercero conectado no recibe nada; un guerrero que saquea fuera de la vista da un aviso por golpe a su dueño y ninguno al saqueado (que ya tiene `pillagedAtMs`); una casa derribada da `HOUSE_DESTROYED` al dueño del atacante |
| contract / e2e | `event_log.notice` contra el JSON Schema, emitido por un e2e en el mismo commit (`TestMain` de `schema_e2e_test.go`); un jugador con la vista en su ciudad recibe el aviso de una muerte en otro chunk |
| cliente | La agrupación de los avisos, «Ir» con y sin tile, el punto en «Registro» y el contador del título; que abrir el registro con avisos pide la primera página |

## 10. Preguntas de producto

Cada pregunta lleva la recomendación de diseño. Son decisiones de producto: hasta que se decidan, la spec sigue en
`Draft`.

| # | Pregunta | Recomendación |
|---|---|---|
| Q1 | ¿Qué se avisa? | **Sólo lo que hoy no llega en vivo** (§4.1): tus unidades muertas y lo que las tuyas hacen fuera de la vista (matar, saquear, derribar). Alternativas: **todo lo que cuenta el registro** duplicaría el aviso de saqueo y de asedio y los tratados que ya están en la barra; **sólo tus muertes** se quedaría corta con la respuesta automática de E3, que mata sin que nadie lo ordene. |
| Q2 | ¿Cómo se ve, y cuánto dura? | **Una franja de avisos en el HUD, hasta tres a la vez, 8 s cada uno** (renovados si llega otro del mismo grupo), con el mismo texto que el registro. Un aviso modal pararía el juego por algo que ya pasó. |
| Q3 | ¿Se agrupan? | **Sí, como el registro: mismo tipo y mismo otro jugador**, mientras el aviso se ve. Sin agrupar, un saqueo de 30 golpes serían 30 avisos. |
| Q4 | ¿Lleva la cámara al sitio? | **Con un botón «Ir», nunca sola.** Mover la cámara sin pedirlo le quitaría al jugador lo que está haciendo; con el botón, la expedición muerta está a un clic. |
| Q5 | ¿Recibe avisos un tercero (un aliado, por ejemplo)? | **No**: cada aviso es de un jugador del suceso (`RN-NOTICE-002`). Avisar a los aliados de lo que les pasa a otros es información de alianza, otra spec. |
| Q6 | ¿Se guardan los avisos para quien estaba desconectado? | **No**: con la sesión cerrada, «Mientras no estabas» ya lo cuenta, agrupado y con su cursor. Guardar avisos sería un segundo registro. |
| Q7 | ¿Y con la pestaña oculta? | **Un contador en el título de la pestaña**, «(2) Empires Online», que se borra al volver. Las notificaciones del sistema piden un permiso al navegador y avisarían fuera del juego: es otra decisión, con su spec. |
| Q8 | ¿Los sucesos de E9 (colina agotada, avance terminado)? | **No en este slice.** El avance se ve en el HUD de la ciudad, y una colina agotada es lo que hacen tus aldeanos, no algo que te hace otro: el aldeano busca otro tile solo (`RN-ECON-022`). |

## 11. Documentos relacionados

- [event-log.md](event-log.md) (los tipos de entrada, su texto y la Q5 que esta spec reemplaza) e
  [../invariants/security.md](../invariants/security.md) (`INV-SEC-009`).
- [combat-raid.md](combat-raid.md) (la muerte), [combat-response.md](combat-response.md) (la respuesta automática),
  [combat-pillage.md](combat-pillage.md) (`pillagedAtMs`) y [combat-siege.md](combat-siege.md) (`besiegedAtMs`).
- [../architecture/frontend.md](../architecture/frontend.md) §4.6 (el registro en el cliente).
