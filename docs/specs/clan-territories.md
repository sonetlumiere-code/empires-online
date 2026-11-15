# Territorios de clan: tierra que conquista un miembro y defiende el clan entero

Propuesta para que un clan pueda poseer territorios: un miembro planta el estandarte **del clan** con su guerrero y,
si aguanta, el territorio pasa a ser del clan, cuenta como tierra afín para todos sus miembros y lo defienden entre
todos.

| Campo | Valor |
|---|---|
| Spec ID | `SPEC-CLAN-TERRITORIES` |
| Estado | Draft (2026-11-15). Pendiente de las decisiones de producto de §10; prohibido implementar contra ella hasta su aceptación. |
| Milestone | E19 Territorios de clan (decimonoveno slice después de M7) |
| Canon | §4 y §10 (territorios), §11 (tratados), §12 (persistencia), §13 (protocolo), §16 (errores), §21 (fuera de MVP: clanes) |
| Depende de | [territory.md](territory.md), [territory-conquest.md](territory-conquest.md), [clans.md](clans.md), [factions.md](factions.md), [diplomacy.md](diplomacy.md), [combat-raid.md](combat-raid.md), [combat-response.md](combat-response.md), [presence.md](presence.md), [event-log.md](event-log.md), [live-notices.md](live-notices.md), [websocket-protocol.md](websocket-protocol.md) |
| Decisiones | [ADR-034](../decisions/ADR-034-territory-claims.md) (la reclamación como encargo durable y la captura en dos fases con dos claves), [ADR-039](../decisions/ADR-039-faction-peace-and-affinity.md) (la tierra afín calculada en cada golpe), [ADR-040](../decisions/ADR-040-clans-membership-and-peace.md) (la pertenencia a un clan como estado de la simulación, la clave del clan en la cola y la paz del clan). Propone el ADR-041 (§7). |
| Reemplaza a | En [clans.md](clans.md), la Q11 («territorio del clan o tierra afín del clan: no en este slice») y, de su no-scope (§2), los territorios cuyo dueño es un clan. En [territory.md](territory.md), que `owner_type = 'CLAN'` no tenga productor (§2 y §6). En [territory-conquest.md](territory-conquest.md) §2, «dueños `CLAN`» de su no-scope. Precisa `RN-CONQ-002`, `RN-CONQ-003`, `RN-CONQ-005`, `RN-CONQ-006` y `RN-CONQ-010` (qué se reclama, contra quién es hostil, qué lo rompe, quién defiende y a quién se avisa cuando el dueño es un clan) y `RN-FAC-007` de [factions.md](factions.md) (qué es tierra afín). Responde a la parte de territorios de la fase Clans de [roadmap.md](../roadmap/roadmap.md) §5. |
| Invariantes | Cita [INV-TERR-004](../invariants/territory.md#inv-terr-004), [INV-TERR-007](../invariants/territory.md#inv-terr-007), [INV-TERR-009](../invariants/territory.md#inv-terr-009), [INV-TERR-010](../invariants/territory.md#inv-terr-010), [INV-TERR-011](../invariants/territory.md#inv-terr-011), [INV-CLAN-001](../invariants/clans.md#inv-clan-001), [INV-CLAN-003](../invariants/clans.md#inv-clan-003), [INV-CITY-006](../invariants/city.md#inv-city-006), [INV-ECON-004](../invariants/economy.md#inv-econ-004), [INV-SEC-009](../invariants/security.md#inv-sec-009) e [INV-PERSIST-005](../invariants/persistence.md#inv-persist-005); precisa [INV-TERR-005](../invariants/territory.md#inv-terr-005); propone dos nuevos, uno de la familia `INV-TERR` y otro de la familia `INV-CLAN` (§8) |

## 1. Objetivo

Que un clan tenga algo que defender juntos sobre el mapa. E18 dio a los clanes paz, canal y sigla: un clan protege a
sus miembros entre sí, pero no tiene un **objetivo común**. Los territorios ya son el recurso disputado del juego
([game-pillars.md](../product/game-pillars.md), pilar 5) desde E12, y el esquema los preparó para un dueño de clan
desde M6 (`owner_type = 'CLAN'`, [territory.md](territory.md) §6), sin que nada lo escriba. Esta spec lo activa:
un miembro conquista para el clan, el clan entero lo aprovecha y el clan entero lo defiende.

La elección que abre es la que hace interesante al clan: un guerrero que reclama un territorio puede quedárselo
**para sí** (el +5 de población de E12, sólo para su ciudad) o **para el clan** (tierra afín para todos sus
compañeros, Q3).

## 2. Scope

**Entra.** Reclamar un territorio para el propio clan con `unit.claim` (Q1, Q2), con los plazos y las reglas de E12;
qué da un territorio del clan: tierra afín para sus miembros (Q3, Q10); quién puede quitárselo y cuándo, con los
tratados, la paz del clan y la protección offline de sus miembros (Q5); la defensa automática de los guerreros de
cualquier miembro (Q6); los avisos y el registro de todos los miembros (Q7); qué pasa al salir del clan y al
disolverlo (Q8); el territorio del clan en el mapa, en el panel del territorio y en el panel del clan (Q11); la
migración `000021`; `scripts/smoke.mjs --territorio-clan` con cuentas propias.

**No entra** (`Fuera de MVP` de este slice): ceder un territorio entre un jugador y su clan en cualquiera de los dos
sentidos, o soltarlo sin que nadie lo reclame (Q4); un tope de territorios por clan (Q9); que la tierra de un
compañero, o la del clan para los del bando de sus miembros, cuente como afín (Q10); un bono de población, de
recursos o de recolección por territorio del clan (Q3); un almacén, edificios o una capital del clan; rangos que
decidan quién puede reclamar (Q2); guerras o alianzas entre clanes; dueños `FACTION`; perder un territorio del clan
por inactividad (sigue `RN-CONQ-009`).

## 3. Actores

El miembro que reclama para su clan (con un guerrero), los demás miembros (aprovechan la tierra afín, la defienden y
reciben los avisos), el líder (no tiene ningún poder nuevo, Q2), quien reclama un territorio de un clan ajeno, los
terceros con el territorio en su área (ven la sigla y la disputa), Game Server (la simulación valida la orden, lleva
el plazo, decide la captura y la afinidad, [INV-SEC-001](../invariants/security.md#inv-sec-001)), PostgreSQL (el
control, la reclamación con su clan y los eventos).

## 4. Modelo

### 4.1 Qué es un territorio del clan

Un territorio con `owner_type = 'CLAN'` y `owner_id` = el `id` del clan (como texto, [territory.md](territory.md)
§6.1). Su dueño es el clan, no un jugador: no cuenta como territorio conquistado de nadie para la población
([INV-ECON-004](../invariants/economy.md#inv-econ-004) no cambia), no es la capital de nadie
([INV-TERR-011](../invariants/territory.md#inv-terr-011) sólo protege el territorio que contiene la ciudad de su
dueño, y un clan no tiene ciudad) y sigue siendo del clan aunque salga quien lo conquistó (Q8). Un clan sólo posee
territorios mientras está vivo.

### 4.2 Reclamar para el clan

`unit.claim { unitId, forClan: true }` es la misma orden de E12 con un campo opcional más. Un miembro del clan
reclama, con su guerrero, el territorio en que está **en nombre de su clan** (Q1). Cualquier miembro puede, no sólo
el líder (Q2). Los plazos son los de E12: `EO_TERRITORY_CLAIM_FREE_SECONDS` (300) para uno libre y
`EO_TERRITORY_CLAIM_SECONDS` (600) para uno con dueño. La reclamación queda atada al clan del que reclama en el
momento de darla: al completarse, el territorio pasa a ese clan y el guerrero vuelve a `IDLE`, como en E12.

Además de lo que pide `RN-CONQ-002`, con `forClan`: el jugador es miembro de un clan; el territorio no es ya de su
clan; no es suyo (ceder lo propio al clan es la Q4, fuera); y la hostilidad se mide contra el dueño como siempre
(§4.4). Sin `forClan`, `unit.claim` es exactamente la de E12.

### 4.3 Qué da un territorio del clan

**Tierra afín para todos los miembros** (Q3): una unidad de un miembro abierto del clan golpeada en un tile de un
territorio del clan recibe el daño reducido de `RN-FAC-008` (×`EO_FACTION_AFFINE_DAMAGE_PERMILLE`, hoy un 20 % menos).
Se evalúa en cada golpe con la pertenencia y el control de la RAM, como la afinidad de E17
([ADR-039](../decisions/ADR-039-faction-peace-and-affinity.md) §2): quien sale del clan deja de tenerla en el acto.
`RN-FAC-007` queda: es afín a un jugador el territorio que es suyo, el de un jugador de su bando si es de `ORDER` o de
`CHAOS`, **y el de su clan**. La tierra del clan no es afín a los del bando de sus miembros que no son del clan, ni
la de un compañero a los demás (Q10).

**Nada de población** (Q3): el +5 de E12 es de una ciudad, y un clan no la tiene. Quien reclama para el clan renuncia
a ese +5 a cambio de dar tierra afín a todo su clan. Si el territorio era de un jugador, ese jugador pierde sus 5 como
en E12 (`RN-CONQ-007`).

### 4.4 Quitarle un territorio a un clan

Reclamar un territorio del clan es una **acción hostil contra todos sus miembros** (Q5):

- **La paz.** No se puede si el que reclama está en paz (`State.AtPeace`: un tratado vigente, los dos de la Orden, el
  mismo clan o la paz residual de E18) con **cualquiera** de los miembros abiertos del clan dueño: no se toma la
  tierra de quien no se puede agredir. En particular, ningún miembro reclama la tierra de su propio clan, y quien
  acaba de salir no la reclama durante su paz residual.
- **La protección offline.** No se puede si **todos** los miembros abiertos del clan están `PROTECTED`: un clan está
  ausente sólo cuando lo están todos. Basta uno `ONLINE` u `OFFLINE_PENDING` para que su tierra se pueda disputar.
  La protección del que reclama sigue impidiéndolo, como en E12.
- **Qué la rompe.** Lo mismo que en `RN-CONQ-005`, con el predicado de arriba recalculado en cada tick de la fase 5:
  que el que reclama firme la paz con un miembro, que un aliado suyo entre en el clan o que pasen todos a
  `PROTECTED`.
- **La defensa automática** (Q6). Al empezar, responden los `WARRIOR` parados de **cualquier miembro** del clan a
  ≤ `EO_COMBAT_GUARD_RADIUS_TILES` del reclamante (`RN-CONQ-006`, con su correa y su vuelta al puesto).

Un clan puede quitarle un territorio a otro clan: la reclamación es hostil contra todos los miembros del clan dueño,
y el que reclama lo hace para el suyo.

### 4.5 Salir, expulsar, disolver

- **Salir o ser expulsado** (Q8). El territorio se queda en el clan. Quien sale pierde en el acto la tierra afín del
  clan (§4.3) y su paz residual le impide reclamarla durante `EO_CLAN_PEACE_GRACE_SECONDS`. Una reclamación **para el
  clan** que estuviera haciendo se rompe en ese tick: ya no es miembro.
- **Disolver** (Q8). Cuando sale el último miembro, los territorios del clan quedan **libres** (`owner_type =
  'NONE'`), en la misma escritura que la disolución, y se difunden con `territory.update`. Una reclamación ajena en
  curso sobre uno de ellos sigue con su plazo y termina como la de un territorio libre.
- **Entrar en el clan** no cambia nada de lo que el jugador ya poseía: sus territorios siguen siendo suyos (Q4).
  Desde la confirmación tiene la tierra afín del clan, y una reclamación suya sobre un territorio del clan se rompe
  (pasa a estar en paz con todos, `RN-CLAN-004`).

### 4.6 Reglas

Dominio: `RN-CLANTERR`.

| ID | Regla |
|---|---|
| `RN-CLANTERR-001` | **La orden.** `unit.claim { unitId, forClan: true }` reclama para el clan del jugador el territorio del tile del guerrero. Exige ser miembro abierto de un clan; el territorio no es ya del clan ni del propio jugador. El resto, como `RN-CONQ-001` y `RN-CONQ-002` (Q1, Q2, Q4). |
| `RN-CLANTERR-002` | **Quién puede.** Cualquier miembro abierto del clan, sin rango (Q2). |
| `RN-CLANTERR-003` | **Qué da.** Un territorio del clan es afín a sus miembros abiertos (`RN-FAC-008`) y a nadie más; no suma población a ninguna ciudad (Q3, Q10). |
| `RN-CLANTERR-004` | **Hostilidad.** Reclamar un territorio de un clan se rechaza si el que reclama está en paz con cualquiera de sus miembros abiertos, o si todos ellos están `PROTECTED`; un tick en que deja de cumplirse rompe la reclamación (Q5). |
| `RN-CLANTERR-005` | **Defensa.** Al empezar una reclamación sobre un territorio del clan, responden los guerreros parados de cualquier miembro dentro del radio de guardia (Q6). |
| `RN-CLANTERR-006` | **Avisos y registro.** Una reclamación sobre un territorio del clan se avisa y se registra a cada miembro abierto al empezar (`TERRITORY_CLAIMED`) y, si se completa, al perderlo (`TERRITORY_LOST`); ganarlo para el clan se avisa y se registra sólo al que reclamó (`TERRITORY_TAKEN`) (Q7). |
| `RN-CLANTERR-007` | **Permanencia.** El territorio es del clan mientras el clan viva, salga quien salga; sólo cambia de manos por una reclamación completada o por la disolución, que lo deja libre (Q8). |
| `RN-CLANTERR-008` | **Reclamación atada al clan.** Una reclamación para el clan se rompe en el tick en que el que reclama deja de ser miembro abierto de ese clan. |
| `RN-CLANTERR-009` | **Público.** El clan dueño (nombre y sigla) de un territorio y la lista de territorios de un clan son públicos (Q11). |

### 4.7 Estados

Los de E12 (`territory-conquest.md` §4.4) con el dueño ampliado:

| Origen | Destino | Disparador | Fase |
|---|---|---|---|
| `NONE` o `PLAYER` o `CLAN` (otro) | `CLAN` | Captura de una reclamación para el clan confirmada por la base | buzón |
| `CLAN` | `PLAYER` u otro `CLAN` | Captura de una reclamación ajena confirmada por la base | buzón |
| `CLAN` | `NONE` | Disolución del clan, en su misma escritura | buzón |
| reclamación para el clan activa | sin reclamación | Lo de `RN-CONQ-005` o la salida del que reclama (`RN-CLANTERR-008`) | 5 |

### 4.8 Qué ve el jugador

- **Guerrero seleccionado**, en un clan: junto a «Reclamar territorio», «Reclamar para el clan [TAG]», deshabilitado
  con su motivo cuando no se puede (§5).
- **Panel del territorio**: «Del clan Humo [HUM]» en vez del nombre de la ciudad del dueño, y la línea de tierra afín
  de E17 si lo es («Tierra afín: tus unidades reciben un 20 % menos de daño»). La reclamación en curso dice si es
  para un clan.
- **Mapa**: el territorio del clan con la sigla en su etiqueta y el color de su dueño de clan; el propio, distinto de
  uno ajeno.
- **Panel del clan**: «Territorios del clan», la lista con «Ir» a cada uno, y la misma lista en la vista pública
  («Ver clan»).
- **Avisos**: «Villa Norte reclama Llanuras del Norte, de tu clan», «Tu clan ha perdido Llanuras del Norte» y, al que
  reclamó, «Has ganado Llanuras del Norte para tu clan».

## 5. Errores

Sólo códigos que ya existen en el catálogo (canon §16).

| Código | Cuándo |
|---|---|
| Los de `unit.claim` en E12 | Todos siguen igual (`territory-conquest.md` §5). |
| `INVALID_TARGET` | Con `forClan`: no estás en un clan; el territorio ya es de tu clan; el territorio es tuyo (Q4). Sobre un territorio de un clan: eres de ese clan; estás en paz con alguno de sus miembros («estás en paz con un miembro del clan»). El mensaje dice cuál. |
| `CITY_PROTECTED` | Todos los miembros del clan dueño están protegidos, o lo está quien reclama. |
| `INVALID_MESSAGE` | `forClan` que no es booleano, o campos de más: lo rechaza el esquema estricto. |

## 6. Eventos y contratos de red

**Cliente → servidor.** Ningún comando nuevo: `unit.claim` gana `forClan`, opcional (aditivo en v1, R-VER-2). El
catálogo sigue en 39 comandos y `unit.claim` sigue clasificado como hostil.

```ts
// unit.claim
payload: z.object({ unitId: EntityId, forClan: z.boolean().optional() }).strict()
```

**Servidor → cliente.** Ningún tipo nuevo (siguen 31); todo aditivo:

- `TerritoryView` gana `ownerClan` (`{ id, name, tag }` o `null`), obligatorio como `CityView.clan`: el clan dueño
  cuando `ownerType` es `CLAN`. `ownerFaction` sigue siendo `null` para un dueño que no es un jugador.
- `TerritoryClaimView` gana `clan` (`{ id, tag }`), opcional: presente en una reclamación para un clan.
- La vista del propio clan de `clan.update` y la vista pública de `clan.info` ganan `territories[]`
  (`{ id, name }`). Un cambio en los territorios del clan vuelve a enviar `clan.update` a las sesiones de todos sus
  miembros.
- `event_log.notice` y la entrada del registro ganan `clanTag`, opcional, en los tres tipos de E12, para decir «de tu
  clan» o «para tu clan».

**Eventos de dominio.** Los de E12, sin tipos nuevos (siguen 27 en `eventlog.EventTypes`): `TerritoryClaimStarted` y
`TerritoryControlChanged` con un clan como dueño se escriben **una fila por miembro abierto** en el momento del hecho
(`player_id` el miembro, `counterpart_player_id` el que reclama), de modo que el registro de cada uno sigue saliendo
de sus propias filas ([ADR-028](../decisions/ADR-028-event-log-from-world-events.md)) sin cruzar la historia de
pertenencia (§7). El payload lleva `clanId` y `clanTag`. Ganar para el clan escribe la fila del que reclamó. La
disolución que libera territorios escribe un `TerritoryControlChanged` por territorio, sin `counterpart_player_id`
(no lo hace nadie: no va al registro).

**Métricas.** Sin métricas nuevas. **Configuración.** Sin variables nuevas: los plazos de E12, el radio de guardia de
E3 y el daño en tierra afín de E17.

## 7. Persistencia

| Pregunta (canon §12) | Respuesta |
|---|---|
| Autoritativo en RAM | El dueño de cada territorio, ahora también de tipo `CLAN`; la reclamación activa con su clan; los territorios de cada clan, derivados del control al cargar |
| Write-through | La reclamación con su clan al empezar y al terminar; la captura con el control, el límite de la ciudad que pierde (si era de un jugador) y los eventos de cada miembro; la liberación de los territorios en la escritura de la disolución |
| Dirty-flag | Nada nuevo |
| Reconstruible / no persistido | Qué territorio es afín a quién; la lista de territorios de un clan |

**Por qué hace falta un ADR (ADR-041, por escribir al aceptar).** Cuatro decisiones estructurales:

1. **La captura con un clan como parte usa la clave del clan.** La captura de E12 es un trabajo de dos claves con las
   dos ciudades ([ADR-034](../decisions/ADR-034-territory-claims.md) §3). Con un clan de por medio, la clave de ese
   lado es la del clan (`clan:<id>`, la de las entradas y salidas de E18): así una captura para el clan y la
   disolución del mismo clan quedan en el orden de la misma cola, y nunca se escribe un territorio a nombre de un clan
   que acaba de disolverse.
2. **La disolución con territorios deja de ser optimista.** En E18 salir es optimista; la última salida de un clan
   que tiene territorios cambia dueños, y el canon pide que la RAM cambie el dueño después del `COMMIT`. La propuesta:
   la última salida de un clan con territorios va en dos fases (la RAM libera los territorios y disuelve el clan al
   volver la confirmación), y la de un clan sin territorios sigue como hoy.
3. **Hostilidad contra un grupo.** `AtPeace` es entre dos jugadores; la reclamación contra un clan consulta la paz con
   cada miembro abierto (como mucho `EO_CLAN_MAX_MEMBERS`) y la protección de todos, en la orden y en cada tick de la
   fase 5. Alternativa descartada: medir la paz y la protección sólo contra el líder, más barato, pero que dejaría la
   tierra de un clan en manos de lo que firme o haga una sola persona.
4. **Los eventos por miembro.** Una fila por miembro en `world_events` en vez de que el registro cruce
   `clan_members` por fecha: el registro no cambia y un ex miembro conserva lo que vivió con el clan. Alternativa
   descartada: una sola fila con el clan y que el registro la reparta por la historia de pertenencia, que obliga a una
   consulta por rangos de fechas en cada lectura.

**Migración `000021`.**

- `territory_claims` gana `clan_id bigint NULL REFERENCES clans(id)`: la reclamación para un clan. Se recarga con ella
  al arrancar.
- Un trigger sobre `territory_control` rechaza `owner_type = 'CLAN'` si `owner_id` no es el `id` de un clan vivo, y
  otro sobre `clans` rechaza marcar `dissolved_at` mientras el clan posea algún territorio: la base garantiza que un
  clan disuelto no posee nada, bajo la comprobación de la simulación
  ([INV-PERSIST-005](../invariants/persistence.md#inv-persist-005)). Los dos toman la fila del clan con `FOR UPDATE`,
  como los triggers de bandos de `000020`.
- Ninguna tabla nueva. La siguiente migración libre pasará a ser `000022`.

## 8. Invariantes

**Cita** [INV-TERR-004](../invariants/territory.md#inv-terr-004) y
[INV-TERR-007](../invariants/territory.md#inv-terr-007) (un dueño `CLAN` tiene `owner_id` y fecha de captura),
[INV-TERR-009](../invariants/territory.md#inv-terr-009) (la disolución también emite `territory.update`),
[INV-TERR-010](../invariants/territory.md#inv-terr-010) (una reclamación activa por territorio, sea para quien sea),
[INV-TERR-011](../invariants/territory.md#inv-terr-011) (la capital de un miembro sigue siendo suya e intocable),
[INV-CLAN-001](../invariants/clans.md#inv-clan-001) e [INV-CLAN-003](../invariants/clans.md#inv-clan-003) (la
pertenencia y la paz del clan, que deciden quién reclama y quién no),
[INV-CITY-006](../invariants/city.md#inv-city-006) (la protección frena la acción hostil: ahora la de todos los
miembros), [INV-ECON-004](../invariants/economy.md#inv-econ-004) (un territorio del clan no entra en el límite de
nadie), [INV-SEC-009](../invariants/security.md#inv-sec-009) (los avisos y el registro llegan sólo a los implicados:
los miembros del clan dueño y el que reclama) e [INV-PERSIST-005](../invariants/persistence.md#inv-persist-005).

**Precisa** [INV-TERR-005](../invariants/territory.md#inv-terr-005): además de un dueño `PLAYER` que existe, un dueño
`CLAN` es un clan vivo.

**Propone** dos invariantes nuevos, que se numerarán al aceptar:

- De la familia `INV-TERR`: ningún clan disuelto posee un territorio, y todo `owner_id` de tipo `CLAN` es el de un clan
  vivo (los triggers de `000021` y un test de la disolución con territorios).
- De la familia `INV-CLAN`: una reclamación para un clan sólo avanza mientras quien la sostiene es miembro abierto de
  ese clan; en el tick en que deja de serlo, termina sin captura.

## 9. Tests esperados

| Nivel | Qué |
|---|---|
| unit | El predicado de la reclamación con `forClan` (cada fila de §5); la hostilidad contra un clan: paz con un miembro por tratado, por la Orden, por el clan y por la paz residual; todos los miembros protegidos frente a uno sin proteger; la afinidad de un territorio del clan para un miembro, un ex miembro, un compañero de bando que no es del clan y un tercero; la fórmula del límite sin los territorios del clan |
| simulation | Golden «territorio de clan» en Go con los ticks exactos: un miembro reclama uno libre para su clan y a los 300 s es del clan, con su `territory.update` y su `clan.update` a los miembros; un tercero se lo quita a los 600 s; la reclamación se rompe cuando el que reclama sale del clan, cuando firma un pacto con un miembro y cuando todos los miembros pasan a `PROTECTED`; los guerreros de un miembro que no es el líder responden al empezar; el daño reducido en tierra del clan para un miembro y el completo para quien acaba de salir; la disolución deja libres los territorios; el catálogo de comandos (39) y de trabajos |
| integration | La migración: `clan_id` en `territory_claims`, el trigger que rechaza un dueño `CLAN` disuelto o inexistente y el que rechaza disolver un clan con territorios; la captura para un clan, de un clan y entre dos clanes en una transacción, con una fila de evento por miembro; la última salida que libera los territorios y disuelve en la misma transacción |
| recovery | Una reclamación para un clan sobrevive a un reinicio con su clan y su plazo; los territorios del clan se cargan con su dueño y dan tierra afín en el primer tick; una reclamación cuyo dueño salió del clan con el servidor parado no se completa |
| contract / e2e | `unit.claim` con `forClan`, `TerritoryView.ownerClan`, `TerritoryClaimView.clan`, `territories[]` en la vista del clan y `clanTag` en los avisos contra el JSON Schema; por WebSocket, dos miembros conectados que reciben el aviso de una reclamación sobre la tierra de su clan |
| cliente | «Reclamar para el clan» con su motivo deshabilitado; el panel del territorio y la etiqueta del mapa con la sigla; la línea de tierra afín; «Territorios del clan» con «Ir»; los textos de los avisos y del registro; `scripts/smoke.mjs --territorio-clan` con **cuentas de humo propias** (nunca `smoke_runner` ni `smoke_victim`) y los plazos cortos de E12: el líder de `[HUM]` reclama para su clan un territorio libre junto a su ciudad y, en pasadas siguientes, `smoke_caos` (sin paz posible con la Orden de `smoke_orden`) se lo quita y `[HUM]` lo recupera, alternando |

## 10. Preguntas de producto (Draft)

Cada pregunta lleva mi recomendación. Son decisiones de producto: hasta que se decidan, la spec no se acepta.

| # | Pregunta | Recomendación |
|---|---|---|
| Q1 | ¿Cómo gana un clan un territorio? | **Un miembro lo reclama para el clan con su guerrero**: la misma orden de E12 con `forClan`, los mismos plazos (5 min libre, 10 con dueño) y la misma disputa. No hay una segunda manera de conquistar que aprender ni que equilibrar. Alternativas: **el clan lo funda** con una orden propia del líder sobre un territorio libre (un camino nuevo sin guerreros); **ceder** territorios propios al clan (Q4). |
| Q2 | ¿Quién puede reclamar para el clan? | **Cualquier miembro.** Reclamar exige un guerrero plantado diez minutos en el sitio; si sólo pudiera el líder, el clan dependería de que el líder esté conectado y tenga guerreros allí. No hay rangos (Q4 de E18). Alternativa: **sólo el líder**, que evita que un miembro comprometa al clan en una disputa que el resto no quiere, pero que con un líder ausente deja al clan sin tierra. |
| Q3 | ¿Qué da un territorio del clan? | **Tierra afín para todos los miembros** (−20 % de daño recibido, el bono de E17) **y nada de población.** Es el bono que ya existe y que se nota donde se lucha: la tierra del clan es donde el clan defiende mejor. El +5 de población es de una ciudad y no se reparte bien entre veinte. Así reclamar abre una elección real: el +5 para mí o la tierra afín para todos. Alternativas: **+5 a la ciudad del que reclamó** (pero entonces, ¿qué pasa cuando sale?); **+1 a cada miembro** (balance sin datos que crece con el clan); **un recurso o un almacén del clan** (otro sistema entero). |
| Q4 | ¿Se pueden ceder territorios entre un jugador y su clan? | **No en este slice**, en ningún sentido. Un traspaso instantáneo permitiría esquivar una reclamación en curso (ceder al clan justo antes de perderlo) o vaciar el clan antes de salir, y obliga a decidir quién puede ceder lo del clan. Lo que se tiene se conserva al entrar y lo del clan se queda en el clan. Alternativa: **ceder lo propio al clan** (sólo en ese sentido, sin disputa en curso, nunca la capital), que da al clan un arranque rápido. |
| Q5 | ¿Quién puede quitarle un territorio a un clan? | **Quien no esté en paz con ninguno de sus miembros, y sólo mientras alguno de ellos no esté `PROTECTED`.** La paz con uno (un pacto, la Orden, la paz residual) protege la tierra de todo el clan, porque la tierra es de todos; y el clan está ausente sólo si lo están todos (pilar 3). Alternativas: **medirlo contra el líder** (más simple, pero un pacto del líder protegería la tierra que el resto quiere disputar y un líder dormido la dejaría expuesta); **la protección de cualquier miembro la protege** (con veinte miembros siempre hay alguno protegido: la tierra del clan sería intocable). |
| Q6 | ¿Quién la defiende sola? | **Los guerreros parados de cualquier miembro** cerca del que reclama, como en E12 los del dueño. Es lo que hace que defender juntos tenga sentido sin coordinar nada. Alternativa: **sólo los del líder**, o nadie (sólo el aviso). |
| Q7 | ¿A quién se avisa y qué queda en el registro? | **A cada miembro: el aviso en vivo y el registro de que reclaman y de que se pierde un territorio del clan; ganarlo, sólo al que reclamó.** Defenderlo es cosa de todos, así que todos tienen que enterarse a tiempo; ganarlo ya se ve en el panel del clan y en el mapa. Alternativas: **sólo al líder**; **también avisar a todos al ganarlo**, que llena los avisos de un clan activo. |
| Q8 | ¿Qué pasa con los territorios al salir del clan y al disolverlo? | **Al salir, se quedan en el clan** (el que sale pierde la tierra afín en el acto y no puede reclamarlos durante su paz residual); **al disolverse, quedan libres.** El territorio es del clan, no de quien lo ganó, y un clan que se disuelve no deja herederos. Alternativas: **al disolverse pasan al último miembro**, que premia quedarse el último; **quien lo conquistó se lo lleva al salir**, que hace del clan un préstamo. |
| Q9 | ¿Hay tope de territorios por clan? | **No**, como no lo hay por jugador (Q9 de E12): cada uno hay que ganarlo y después defenderlo, y sin bono de población el clan no crece en unidades por tenerlos. Se mide con jugadores. Alternativa: **un tope proporcional a los miembros** (p. ej. uno por cada cinco). |
| Q10 | ¿Qué más cuenta como tierra afín? | **Nada más: la tierra del clan, sólo para sus miembros.** Ni la de un compañero de clan (la alternativa de la Q11 de E18), ni la del clan para los del bando de sus miembros que no son del clan. La tierra personal sigue siendo personal y la del clan, de los que están dentro: así la Q3 es una elección de verdad. Alternativas: **la tierra de un compañero también** (barato, pero quita el motivo de reclamar para el clan); **la del clan también para el bando**, que haría de cada clan de la Orden una fuente de tierra afín para toda la Orden. |
| Q11 | ¿Qué es público? | **El clan dueño de un territorio (nombre y sigla) en el mapa y en el panel, y la lista de territorios de un clan en su vista pública**, como la sigla y los miembros en E18 (`RN-CLAN-008`). El dueño de un territorio ya es público; esconder que es de un clan escondería contra quién es hostil reclamarlo. Alternativa: **sólo la sigla en el mapa**, sin la lista en la vista pública. |

## 11. Documentos relacionados

- [territory-conquest.md](territory-conquest.md) (la reclamación, los plazos, la defensa y los avisos que esta spec
  amplía) y [territory.md](territory.md) (el dueño polimórfico y `TerritoryView`).
- [clans.md](clans.md) (la pertenencia, la paz del clan y la residual, la clave del clan en la cola y la Q11 que esta
  spec responde).
- [factions.md](factions.md) (la tierra afín y su daño), [diplomacy.md](diplomacy.md) (los tratados que dan paz) y
  [presence.md](presence.md) (la protección offline).
- [combat-raid.md](combat-raid.md) y [combat-response.md](combat-response.md) (la acción hostil y la respuesta
  automática).
- [event-log.md](event-log.md) y [live-notices.md](live-notices.md) (el registro y los avisos).
- [../database/schema.md](../database/schema.md) (`territory_control`, `territory_claims` y las tablas de los clanes).
