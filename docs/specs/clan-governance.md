# Gobierno del clan: oficiales, traspaso del liderazgo y el líder que deja de jugar

Para que un clan no dependa de una sola persona: oficiales que invitan y expulsan, un líder que puede ceder el
liderazgo y, si el líder deja de jugar, un clan que no se queda congelado, porque el liderazgo pasa solo a quien sigue
jugando.

| Campo | Valor |
|---|---|
| Spec ID | `SPEC-CLAN-GOVERNANCE` |
| Estado | Implemented (2026-11-16). Propuesta en `Draft` y aceptada el mismo día, con la recomendación de cada pregunta de §10; en `main` desde la fusión `983035e`, con la CI en verde. |
| Milestone | E20 Gobierno del clan (vigésimo slice después de M7) |
| Canon | §1.5 (determinismo), §12 (persistencia), §13 (protocolo), §14 (seguridad), §16 (errores) |
| Depende de | [clans.md](clans.md), [clan-territories.md](clan-territories.md), [presence.md](presence.md), [event-log.md](event-log.md), [live-notices.md](live-notices.md), [websocket-protocol.md](websocket-protocol.md) |
| Decisiones | [ADR-040](../decisions/ADR-040-clans-membership-and-peace.md) (la pertenencia a un clan como estado de la simulación, con la clave del clan en la cola y su candado en la base) y [ADR-041](../decisions/ADR-041-clan-territories.md) (la última salida de un clan con territorios en dos fases). Sin ADR nuevo (§7). |
| Reemplaza a | En [clans.md](clans.md), la Q4 («un líder y miembros; sólo el líder invita y expulsa») y, de su no-scope (§2), «oficiales u otros rangos, traspaso explícito del liderazgo». Precisa `RN-CLAN-001` (los rangos), `RN-CLAN-007` (quién invita y expulsa, y a quién pasa el liderazgo) y `RN-CLAN-008` (el rango es público, como la lista de miembros). Cierra la observación abierta de E18 «un líder que deja de jugar no pierde el liderazgo». |
| Invariantes | Cita [INV-SEC-001](../invariants/security.md#inv-sec-001), [INV-SEC-004](../invariants/security.md#inv-sec-004), [INV-SEC-007](../invariants/security.md#inv-sec-007), [INV-CITY-011](../invariants/city.md#inv-city-011), [INV-CLAN-001](../invariants/clans.md#inv-clan-001), [INV-CLAN-002](../invariants/clans.md#inv-clan-002) e [INV-PERSIST-005](../invariants/persistence.md#inv-persist-005); asigna [INV-CLAN-005](../invariants/clans.md#inv-clan-005) (§8) |

## 1. Objetivo

E18 dejó un clan con un solo líder que hace todo: invitar, retirar invitaciones y expulsar. Desde E19 un clan además
posee territorios, y eso hace más grave el hueco que E18 ya anotaba: **si el líder deja de jugar, nadie puede invitar
ni expulsar**. El clan conserva su tierra, pero no puede reponer a quien se va. Su única salida es que el líder vuelva o
que salgan todos, y lo segundo disuelve el clan y libera los territorios.

Este slice reparte el trabajo del líder y garantiza que el clan sobreviva a su ausencia, sin abrir la política entre
clanes (alianzas y guerras quedan para otro slice). Es la base que esas funciones necesitarán: una alianza entre clanes
pide saber quién puede firmarla en nombre de cada uno.

## 2. Scope

**Entra.** Tres rangos: líder, oficial y miembro (Q1). Los oficiales invitan, retiran invitaciones y expulsan miembros
rasos (Q2), sin tope propio (Q3). El líder nombra y quita oficiales, y puede ceder el liderazgo a cualquier miembro, que
lo recibe en el acto (Q4). Cuando el líder sale, el liderazgo pasa al oficial más antiguo (Q5). Cuando el líder lleva
`EO_CLAN_LEADER_INACTIVE_SECONDS` sin conectarse (7 días), el liderazgo pasa solo a quien siga jugando, y el líder
ausente queda como oficial (Q6, Q7). El traspaso y la sucesión van al registro y avisan al nuevo líder (Q8). Entran
también la migración `000022`, el cliente (rango en la lista, botones por rango, «Ceder el liderazgo») y la ampliación
de `scripts/smoke.mjs --clan`.

**No entra** (`Fuera de MVP` de este slice): permisos configurables por clan o rangos con nombre propio; votaciones o
elecciones; que el líder disuelva el clan con una orden; expulsar miembros inactivos de forma automática; que un
oficial modere el canal (borrar mensajes) o nombre a otros oficiales; renombrar el clan o cambiar su sigla; restringir
por rango la reclamación para el clan (Q9: sigue pudiendo reclamar cualquier miembro, como en E19); alianzas, guerras
y directorio de clanes.

## 3. Actores

- **El líder.** Hace todo lo que hace un oficial. Además nombra y quita oficiales, cede el liderazgo y expulsa también
  a oficiales.
- **Los oficiales.** Invitan, retiran invitaciones y expulsan miembros rasos.
- **Los miembros.** Sin cambios respecto de E18.
- **El cliente web.**
- **El Game Server.** La simulación decide los rangos y la sucesión en el tick, como la pertenencia
  ([ADR-040](../decisions/ADR-040-clans-membership-and-peace.md) §1, [INV-SEC-001](../invariants/security.md#inv-sec-001)).
- **PostgreSQL.** Guarda el rango en la estancia de cada miembro y vuelve a comprobarlo con el candado del clan.

Redis no participa.

## 4. Modelo

### 4.1 Los rangos

Cada miembro abierto de un clan tiene un rango, que es exactamente uno de estos:

- `LEADER`: el líder, uno por clan ([INV-CLAN-001](../invariants/clans.md#inv-clan-001)).
- `OFFICER`: oficial. No hay tope propio; el límite es el del clan menos uno (Q3).
- `MEMBER`: miembro raso. Es el rango con el que se entra.

El rango es **público**, como la lista de miembros (`RN-CLAN-008`): sale en la vista del clan y en `clan.info`. Saber
quién puede invitar o expulsar en un clan ajeno no da ventaja de combate, y sirve para saber a quién escribir.

### 4.2 Qué puede cada rango

| Orden | Líder | Oficial | Miembro |
|---|---|---|---|
| `clan.invite` (invitar y retirar invitaciones, también las de otro) | sí | sí | no |
| `clan.expel` sobre un miembro raso | sí | sí | no |
| `clan.expel` sobre un oficial | sí | no | no |
| `clan.promote` (nombrar o quitar oficial) | sí | no | no |
| `clan.transfer` (ceder el liderazgo) | sí | no | no |
| `clan.leave`, el canal, `unit.claim` con `forClan` | sí | sí | sí |

Nadie se expulsa a sí mismo ni expulsa al líder: para dejar el clan está `clan.leave`. Las reglas de E18 siguen igual
para todos los rangos: el tope contando invitaciones, la regla de bandos (`RN-CLAN-006`) y la invitación que caduca. Un
clan que se está disolviendo (E19, `clanRecord.dissolving`) rechaza también nombrar, quitar oficiales y ceder el
liderazgo.

### 4.3 Nombrar oficiales y ceder el liderazgo

- **`clan.promote { playerId, officer }`.** Sólo el líder, sobre otro miembro abierto. Con `officer: true`, el miembro
  pasa de `MEMBER` a `OFFICER`; con `officer: false`, hace el camino contrario. Si el miembro ya tiene ese rango, se
  rechaza con `INVALID_TARGET`. Como el miembro ya está en el clan, no cambia nada de la paz.
- **`clan.transfer { playerId }`.** Sólo el líder, sobre otro miembro abierto de cualquier rango. El elegido pasa a
  `LEADER` en el acto, sin tener que aceptar (Q4), y el líder anterior pasa a `OFFICER`. No hace falta confirmación
  del que recibe porque no le impone nada: puede ceder el liderazgo o salir cuando quiera. El cliente sí pide
  confirmación a quien cede.

Las dos órdenes son **comandos de la simulación**, no hostiles. Como la salida en E18, son **optimistas**: la RAM las
aplica en el tick y un trabajo de la cola, con la clave `clan:<id>`, las escribe. Las órdenes del mismo clan salen
después en la misma cola, así que una invitación del nuevo líder nunca llega a la base antes que su liderazgo (§7).

### 4.4 La sucesión

El liderazgo cambia sin que el líder lo ceda en dos casos:

1. **El líder sale** (`clan.leave`). El liderazgo pasa al **oficial más antiguo** y, si no hay oficiales, al miembro
   más antiguo (Q5). La antigüedad es la de E18: la fecha de entrada en el clan y, a igualdad, el `id` del jugador. Es
   la fecha de entrada en el clan, no la del nombramiento como oficial. Si el líder era el último, el clan se disuelve
   como en E18 y E19.
2. **El líder deja de jugar** (Q6). Un barrido de la simulación, **una vez por minuto de juego**, mira el líder de
   cada clan vivo. El líder está **ausente** si su ciudad no está `ONLINE` y su marca de desconexión
   (`last_offline_at`, [INV-CITY-011](../invariants/city.md#inv-city-011)) tiene `EO_CLAN_LEADER_INACTIVE_SECONDS` o
   más (604 800, 7 días). En ese caso el liderazgo pasa al **oficial activo más antiguo** y, si no hay ninguno, al
   **miembro activo más antiguo**. Un miembro es **activo** si su ciudad está `ONLINE` o si su desconexión tiene menos
   de `EO_CLAN_LEADER_INACTIVE_SECONDS`. Si nadie del clan está activo, no cambia nada: no tiene sentido pasar el
   liderazgo a otro ausente. El líder ausente queda como `OFFICER` (Q7). Si vuelve, no recupera el liderazgo solo: el
   nuevo líder puede cedérselo.

La ausencia **se deriva** de la presencia que la simulación ya tiene en la RAM para la protección
([presence.md](presence.md)), no de una columna nueva. Un reinicio no la pone a cero: la carga conserva la marca de
desconexión guardada, y sólo la fecha cuando falta (INV-CITY-011). Tampoco es un temporizador que haya que guardar. Si
el servidor estuvo parado más de lo que faltaba, el primer barrido después de arrancar hace la sucesión.

No hay sucesión mientras el clan se está disolviendo.

### 4.5 Reglas

Dominio: `RN-CLANGOV`.

| ID | Regla |
|---|---|
| `RN-CLANGOV-001` | Todo miembro abierto tiene un rango: `LEADER` (exactamente uno por clan), `OFFICER` o `MEMBER`. Se entra como `MEMBER`. El rango es público (Q1, Q3). |
| `RN-CLANGOV-002` | Invitar y retirar invitaciones: el líder y los oficiales. Expulsar a un miembro raso: el líder y los oficiales. Expulsar a un oficial: sólo el líder. Al líder no lo expulsa nadie (Q2). |
| `RN-CLANGOV-003` | Sólo el líder nombra o quita oficiales y cede el liderazgo. El que recibe el liderazgo no tiene que aceptarlo, y el líder anterior pasa a oficial (Q4). |
| `RN-CLANGOV-004` | Si el líder sale, el liderazgo pasa al oficial más antiguo y, si no hay ninguno, al miembro más antiguo (Q5). |
| `RN-CLANGOV-005` | **Líder ausente.** Si la ciudad del líder lleva `EO_CLAN_LEADER_INACTIVE_SECONDS` sin estar `ONLINE`, el liderazgo pasa al oficial activo más antiguo y, si no hay ninguno, al miembro activo más antiguo. El líder ausente queda como oficial. Si no hay nadie activo, no cambia nada (Q6, Q7). |
| `RN-CLANGOV-006` | El rango que permite una orden se comprueba en el tick y otra vez en la transacción, con el candado del clan. Una orden que llega a la base cuando quien la dio ya no tiene ese rango no escribe nada. |

### 4.6 Qué ve el jugador

- **Panel del clan.** Cada miembro de la lista lleva su rango («Líder», «Oficial» o nada). Los botones que se ven
  dependen del rango de quien mira:
  - Un oficial ve «Invitar», «Retirar» y «Expulsar» junto a los miembros rasos.
  - El líder ve además «Expulsar» junto a los oficiales, «Nombrar oficial» o «Quitar oficial» junto a cada miembro, y
    «Ceder el liderazgo» con una confirmación («[Ciudad] pasará a liderar [TAG] y tú quedarás como oficial»).
  - Una línea explica la sucesión: «Si el líder pasa 7 días sin jugar, el liderazgo pasa al oficial más antiguo que
    siga jugando».
- **Ciudad ajena.** «Invitar al clan» aparece también para un oficial.
- **Registro y avisos** (Q8):
  - Quien recibe el liderazgo, por traspaso o por sucesión, recibe un aviso en vivo y lo ve en su registro: «[Ciudad]
    te cedió el liderazgo de [TAG]» o «Ahora lideras [TAG]: [Ciudad] llevaba 7 días sin jugar».
  - El líder ausente lo encuentra en «Mientras no estabas»: «Perdiste el liderazgo de [TAG] por inactividad; ahora lo
    tiene [Ciudad]».
  - Nombrar o quitar un oficial sólo se ve en el panel.
- Un rechazo del servidor sale con su motivo: «Sólo el líder o un oficial invita», «Un oficial no puede expulsar a
  otro oficial», «Ya es oficial».

## 5. Errores

Sólo códigos que ya existen en el catálogo (canon §16).

| Código | Condición |
|---|---|
| `INVALID_MESSAGE` | Un `playerId` mal formado o un `officer` que no es booleano. |
| `INVALID_TARGET` | Pasa en cualquiera de estos casos: invitar, retirar o expulsar sin ser líder ni oficial; que un oficial expulse a otro oficial; expulsar al líder; nombrar, quitar oficial o ceder el liderazgo sin ser el líder; hacerlo sobre uno mismo o sobre quien no es miembro abierto del clan; nombrar a quien ya es oficial o quitar a quien no lo es; cualquiera de estas órdenes en un clan que se está disolviendo; y lo que la base rechace por `RN-CLANGOV-006`. El mensaje dice cuál de ellos fue. |
| `INTERNAL_ERROR` | La escritura de un traspaso no llega a la base tras sus reintentos (§7). |

## 6. Eventos y contratos de red

**Cliente → servidor.** Dos comandos nuevos y estrictos (R-VER-3). Van en `CommandTypes` y en `actions.go` como **no
hostiles**, con un trabajo de la cola cada uno con la clave `clan:<id>`, que entra en `simulation.JobNames`. El
catálogo pasa de 39 a 41 comandos.

- `clan.promote { playerId, officer }`.
- `clan.transfer { playerId }`.

`clan.invite` y `clan.expel` no cambian de forma; sólo cambia quién puede darlos.

**Servidor → cliente.** No hay tipos nuevos; el catálogo sigue en 31. `ClanRole` gana `OFFICER`: el `role` de cada
miembro en `clan.update` y en `clan.info` pasa a ser `LEADER`, `OFFICER` o `MEMBER`. Es un valor nuevo en un enum
estricto que el cliente y el servidor estrenan juntos: un cliente viejo rechazaría la vista de un clan con oficiales.
Se publica en el mismo despliegue, como cuando E19 añadió campos obligatorios. Los fixtures del protocolo con
`'OFFICER'` como valor inválido (`protocol.test.ts`) pasan a ser válidos. Cada cambio de rango o de líder envía un
`clan.update` a todos los miembros, con el `requestId` a quien dio la orden.

**Eventos de dominio** en `world_events` ([ADR-028](../decisions/ADR-028-event-log-from-world-events.md)):

- `ClanRankChanged`: sin `counterpart`. No va al registro; sólo sirve de historia.
- `ClanLeaderChanged`: con `player_id` = el nuevo líder, `counterpart_player_id` = el anterior, `atMs`, y `reason` en
  `TRANSFER` o `INACTIVE`. Entra en `eventlog.EventTypes` y en `Classify` para los dos lados. Entra también en
  `eventlog.NoticeKinds` y en `EventLogNoticeKind` de Zod, que pasa de 27 a 28 tipos, para avisar en vivo al nuevo
  líder (Q8).
- La sucesión porque el líder sale no genera un evento propio: es el `ClanLeft` de E18, y su payload gana
  `newLeaderId`.

**Métricas.** `eo_clan_changes_total{kind}` gana las series `promoted`, `demoted`, `transferred` y `succeeded`, todas a
cero al arrancar. No hay métrica nueva: [../operations/monitoring.md](../operations/monitoring.md) §2 sigue en 22.

**Configuración.** `EO_CLAN_LEADER_INACTIVE_SECONDS` (604 800). Va en segundos para que el humo y los tests puedan
acortarla, como los plazos de la conquista.

## 7. Persistencia

| Pregunta (canon §12) | Respuesta |
|---|---|
| Autoritativo en RAM | El rango de cada miembro abierto, junto a su pertenencia (`clanRecord`), cargado con el mundo |
| Write-through | `clan.promote`, `clan.transfer` y la sucesión por ausencia, cada una en su trabajo de la cola con la clave del clan; la sucesión porque el líder sale, en el trabajo de la salida de E18 |
| Dirty-flag | Nada |
| Reconstruible / no persistido | La ausencia del líder, que se deriva de la presencia de su ciudad en cada barrido |

**Por qué no hace falta un ADR nuevo.** Los rangos siguen la decisión de
[ADR-040](../decisions/ADR-040-clans-membership-and-peace.md): son estado de la simulación, se escriben con la clave
del clan en la cola y la base los vuelve a comprobar con el candado del clan, como hoy el líder. Que sean optimistas,
como la salida, sale de la misma regla: no cambian la paz ni la tierra afín, así que no hace falta una segunda fase
para confirmarlos. La sucesión por ausencia es un barrido del tick sobre la presencia que la RAM ya tiene, igual que el
paso a `PROTECTED` ([presence.md](presence.md)). Se aceptó así, sin ADR; si la implementación encuentra una decisión
estructural que esta lectura no recoge, irá al ADR-042.

**El fallo de escritura.** Nombrar o quitar un oficial que no llega a la base tras sus reintentos se trata como la
salida de E18: se guarda para el siguiente volcado, porque es una escritura tolerante. Un traspaso o una sucesión que
no llegan se **deshacen** en la RAM y se avisa a quien dio la orden con `INTERNAL_ERROR`. Un líder que la base no
conoce no podría invitar, porque la transacción de la invitación comprueba el rango (`RN-CLANGOV-006`).

**Migración `000022`.**

- `clan_members.rank`, de tipo `text NOT NULL DEFAULT 'MEMBER'`, con un `CHECK (rank IN ('MEMBER', 'OFFICER'))`. El
  líder sigue en `clans.leader_player_id`, y el rango de su estancia es `MEMBER` (ver la línea siguiente). Así nunca
  hay dos fuentes de verdad de quién es el líder.
- Un trigger rechaza que la estancia abierta del líder tenga rango `OFFICER` y que una estancia cerrada cambie de
  rango (23514), como red de seguridad bajo la transacción ([INV-PERSIST-005](../invariants/persistence.md#inv-persist-005)).
- No hay tablas nuevas, así que el `TRUNCATE` de los tests y la clasificación de
  [../database/schema.md](../database/schema.md) §1 no cambian.
- La transacción de `clan.invite` y la de `clan.expel` pasan a aceptar al líder o a un oficial abierto (y, para
  expulsar a un oficial, sólo al líder), con el candado del clan que ya toman.
- La siguiente migración libre pasará a ser `000023`.

## 8. Invariantes

**Cita:**

- [INV-SEC-001](../invariants/security.md#inv-sec-001) e [INV-SEC-004](../invariants/security.md#inv-sec-004): el rango
  lo decide el servidor, no un campo del comando.
- [INV-SEC-007](../invariants/security.md#inv-sec-007): un `requestId` repetido no cede dos veces ni nombra dos veces.
- [INV-CITY-011](../invariants/city.md#inv-city-011): la marca de desconexión de la que se deriva la ausencia.
- [INV-CLAN-001](../invariants/clans.md#inv-clan-001): sigue habiendo exactamente un líder, que es miembro abierto,
  tras cada traspaso y cada sucesión.
- [INV-CLAN-002](../invariants/clans.md#inv-clan-002): un oficial que invita respeta la regla de bandos como el líder.
- [INV-PERSIST-005](../invariants/persistence.md#inv-persist-005): la migración `000022`.

**Asigna** [INV-CLAN-005](../invariants/clans.md#inv-clan-005), registrado en
[../invariants/clans.md](../invariants/clans.md): ningún cambio de pertenencia ajena, de rango o de liderazgo
se escribe si quien lo ordenó no tenía, en la transacción y con el candado del clan, el rango que lo permite
(`RN-CLANGOV-006`). Se garantiza en la base y lo comprueba un test de integración por cada orden.

## 9. Tests esperados

| Nivel | Qué |
|---|---|
| unit | La tabla de §4.2, orden a orden y rango a rango, con cada fila de §5. El sucesor al salir, con y sin oficiales y con desempate por `id`. La ausencia con el líder `ONLINE`, `OFFLINE_PENDING` y `PROTECTED`, justo antes y justo después del plazo. El sucesor activo, que salta a los oficiales ausentes; sin nadie activo, sin cambio |
| simulation | Golden «líder ausente» en Go con los ticks exactos: el líder se desconecta y, en el barrido del minuto en que se cumplen los 7 días, el oficial activo más antiguo pasa a líder, el ausente queda como oficial, sale un `clan.update` a cada miembro y el aviso al nuevo líder; con un plazo corto, sin nadie activo, nada cambia. Un oficial que invita y expulsa a un raso, y al que se le rechaza expulsar a otro oficial. Un traspaso optimista que la base rechaza y se deshace. El catálogo de comandos (41) y de trabajos |
| integration | La migración: `rank` con su `CHECK` y el trigger. Cada orden con su comprobación de rango en la transacción: un oficial invita y expulsa a un raso, no a otro oficial; un ex oficial ya no invita; un traspaso deja `leader_player_id` y los rangos coherentes en una transacción. El evento `ClanLeaderChanged` con su `counterpart` y el registro de cada lado |
| recovery | Los rangos sobreviven a un reinicio. Un líder cuya ausencia se cumplió con el servidor parado pierde el liderazgo en el primer barrido, y uno que se desconectó hace poco no la pierde por el reinicio |
| contract / e2e | `clan.promote` y `clan.transfer` contra el JSON Schema; `ClanRole` con `OFFICER` en `clan.update` y `clan.info`; el aviso `ClanLeaderChanged`. Por WebSocket: el líder nombra un oficial, el oficial invita (con `clan.update` a los implicados) y el líder cede el liderazgo |
| cliente | El rango en la lista; los botones según el rango de quien mira; «Ceder el liderazgo» con su confirmación; «Invitar al clan» para un oficial en la barra de una ciudad ajena; los textos del aviso y del registro. `scripts/smoke.mjs --clan` ampliado: `smoke_clan_a`, líder de `[HUM]`, nombra oficial a `smoke_orden`, que invita a `smoke_clan_b`; `smoke_clan_a` cede el liderazgo a `smoke_orden` y `smoke_orden` se lo devuelve en la misma pasada, para que `--territorio-clan` siga encontrando a `smoke_clan_a` como líder. La sucesión por ausencia no se prueba en vivo (pediría tener desconectado al líder durante el plazo): la cubren el golden y la recuperación |

**Precisiones (2026-11-16), al implementar.** Ninguna cambia una decisión de §10.

- **Cuatro tipos en el registro, dos avisos.** `ClanLeaderChanged` da cuatro tipos de entrada:
  - `CLAN_LEADERSHIP_RECEIVED` y `CLAN_LEADERSHIP_GIVEN`, por traspaso;
  - `CLAN_LEADERSHIP_INHERITED` y `CLAN_LEADERSHIP_LOST`, por ausencia.

  Se avisan en vivo los dos de quien recibe el liderazgo. Por eso `EventLogNoticeKind` pasa de 27 a **29** tipos, no a
  28 como decía §6, y el catálogo de tipos de entrada pasa de 27 a 31. El payload lleva `reason` y la sigla en
  `ownerClanTag` y `forClanTag`, para que cada lado la lea igual que en E19.
- **Nombrar o quitar un oficial que no se escribe se deshace.** No se guarda para el volcado como decía §7: si la base
  no lo admite o no responde, la RAM vuelve al rango anterior y quien lo ordenó recibe `INVALID_TARGET` o
  `INTERNAL_ERROR`, igual que con un traspaso. Así hay un solo camino para los dos.
- **Una expulsión que la base no admite se deshace.** La transacción de la salida comprueba el rango de quien expulsa
  (`INV-CLAN-005`). Si no lo admite, la simulación devuelve al expulsado al clan con su entrada y su rango, borra su
  paz residual y registra `invariant_violation` con `inv_id=INV-CLAN-005`, porque sólo ocurre si una escritura de
  rango anterior se perdió.
- **Invitar toma el candado del clan.** La transacción de una invitación toma ahora el candado del clan, como los
  cambios de rango, para leer el rango de quien invita bajo él.
- **El barrido de la ausencia** va en la fase 2 del tick, junto a los de los clanes, y el primero tras arrancar se hace
  en el primer tick. Un candidato es activo si tiene ciudad y no está ausente con la misma regla que el líder.
- **La sigla en el registro.** Al convertir una entrada para la red, la sigla sólo se copiaba en las entradas de un
  territorio. Ahora se copia siempre. Lo destapó la integración de esta spec: sin el cambio, el cambio de líder llegaba
  sin `clanTag`.
- **El humo.** smoke_orden, ya oficial, invita y además expulsa a smoke_clan_b. Al final de la pasada vuelve a miembro
  raso, para que la siguiente pasada la nombre otra vez. Sin smoke_orden en el clan (sin `--faccion` previo), invita y
  expulsa el líder y el traspaso no se prueba.

## 10. Decisiones de producto (2026-11-16)

Preguntas que la propuesta dejó abiertas; producto aceptó la recomendación de cada una. Son decisiones de producto,
no de ingeniería: cambiarlas es cambiar la spec.

| # | Pregunta | Decisión |
|---|---|---|
| Q1 | ¿Qué rangos hay? | **Tres: líder, oficial y miembro.** Es lo mínimo que reparte el trabajo y da un sucesor natural, sin abrir un sistema de permisos. Alternativas: **sólo traspaso**, sin oficiales (más pequeño, pero el líder sigue haciendo todo solo); **rangos configurables** por el clan, que piden una pantalla de permisos y no tienen sentido sin jugadores. |
| Q2 | ¿Qué puede hacer un oficial? | **Invitar, retirar invitaciones (también las de otros) y expulsar a miembros rasos; no a otros oficiales, no nombrar oficiales y no ceder el liderazgo.** Un oficial resuelve el día a día sin que dos oficiales puedan echarse entre sí. Alternativas: **sólo invitar** (expulsar queda para el líder, más seguro contra un oficial resentido, pero el clan sigue sin poder echar a nadie si el líder falta); **también nombrar oficiales**, que diluye el control del líder. |
| Q3 | ¿Cuántos oficiales? | **Sin tope propio**: el límite es el del clan. Sin datos de cómo se organizan los clanes, un tope es arbitrario, y el líder decide a quién nombra. Alternativas: **un tope fijo** (3 o 5), que obliga a que ser oficial signifique algo; **un tope proporcional** al tamaño del clan. |
| Q4 | ¿Cómo se cede el liderazgo? | **`clan.transfer` a cualquier miembro, en el acto y sin que el otro tenga que aceptar; el líder anterior pasa a oficial.** No impone nada al que recibe, que puede devolverlo o salir. Quedarse como oficial evita que ceder sea perder todo poder. Alternativas: **con aceptación** del que recibe (otro estado pendiente, como una invitación); **el líder anterior pasa a miembro raso**. |
| Q5 | ¿Quién hereda cuando el líder sale? | **El oficial más antiguo y, si no hay oficiales, el miembro más antiguo.** Si el líder nombró oficiales, son en quienes confiaba. Precisa `RN-CLAN-007`. Alternativas: **el miembro más antiguo**, como hoy, sin mirar el rango; **que el líder no pueda salir sin ceder antes el liderazgo**, que obliga a decidir pero deja atrapado a quien quiere irse ya. |
| Q6 | ¿Qué pasa si el líder deja de jugar? | **Si su ciudad lleva 7 días seguidos sin conectarse, el liderazgo pasa al oficial activo más antiguo o, si no hay ninguno, al miembro activo más antiguo; si nadie está activo, nada.** Siete días cubren unas vacaciones cortas sin dejar un clan congelado un mes. Se deriva de la presencia que ya existe, sin guardar nada nuevo. Alternativas: **3 días**, que le quita el clan a alguien en un fin de semana largo; **14 o 30 días**; **una votación** de los miembros, que es otro slice. |
| Q7 | ¿Qué queda del líder ausente? | **Pasa a oficial.** Al volver conserva un rango con responsabilidad, y el nuevo líder puede quitárselo o devolverle el liderazgo. Alternativas: **miembro raso**, más limpio, pero castiga una ausencia que pudo ser forzada; **recupera el liderazgo al volver**, que hace del nuevo líder un interino y vuelve a cambiar quién manda sin que nadie lo pida. |
| Q8 | ¿Qué entra en el registro y en los avisos? | **El traspaso y la sucesión: aviso en vivo y registro para quien recibe el liderazgo, y registro para quien lo pierde. Nombrar o quitar un oficial, sólo en el panel.** Que te den o te quiten el clan es «lo que otros te hicieron» (`RN-LOG-001`). Un nombramiento es vida interna del clan, como entrar o salir en E18. Alternativas: **avisar a todo el clan** de cada cambio de líder; **también los nombramientos** en el registro del nombrado. |
| Q9 | ¿Cambia quién puede reclamar un territorio para el clan? | **No: sigue pudiendo cualquier miembro, como en E19.** La reclamación ya es una apuesta del que pone su guerrero, y restringirla le quita al clan la iniciativa de sus miembros. Alternativa: **sólo el líder y los oficiales**, que da al clan control sobre qué guerras empieza, pero frena a los clanes que juegan a horas distintas. |
| Q10 | ¿Puede el líder disolver el clan con una orden? | **No en este slice.** Hoy un clan se disuelve cuando sale el último. Una orden de disolver echaría a todos de golpe, liberaría sus territorios (E19) y abriría la paz residual de 20 personas a la vez; es una decisión con más consecuencias que todo lo demás de este slice. Alternativa: **`clan.dissolve` para el líder**, con la misma paz residual para todos. |

## 11. Documentos relacionados

- [clans.md](clans.md): la pertenencia, el líder, la sucesión al salir y el candado del clan que este slice amplía.
- [clan-territories.md](clan-territories.md): la reclamación para el clan, que no cambia (Q9), y la disolución en dos
  fases, durante la que se rechazan las órdenes de este slice.
- [presence.md](presence.md): la presencia y la marca de desconexión de las que sale la ausencia del líder.
- [event-log.md](event-log.md) y [live-notices.md](live-notices.md): el traspaso y la sucesión en el registro y en los
  avisos.
- [../roadmap/roadmap.md](../roadmap/roadmap.md), la fase Clans.
