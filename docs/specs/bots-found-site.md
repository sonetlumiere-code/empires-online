# El sitio de la siguiente ciudad

Propuesta para que el constructor de los bots **busque el sitio de su siguiente ciudad alrededor de todas sus
ciudades**, no sólo de la capital, de modo que una capital pegada al borde del mapa o rodeada de territorios con dueño
no deje al bot en dos ciudades para siempre.

| Campo | Valor |
|---|---|
| Spec ID | `SPEC-BOTS-FOUND-SITE` |
| Estado | Accepted (2026-11-27). La persona usuaria aceptó las tres recomendaciones de §10. |
| Milestone | E42 El sitio de la siguiente ciudad (cuadragésimo segundo slice después de M7) |
| Canon | §1.3 (el servidor es la única verdad), §19 (niveles de test) |
| Depende de | [bots-found.md](bots-found.md), [bots-all-cities.md](bots-all-cities.md), [multi-city.md](multi-city.md), [more-cities.md](more-cities.md) |
| Decisiones | [ADR-049](../decisions/ADR-049-bots-as-protocol-clients.md) sigue valiendo. Sin ADR nuevo: no cambia el servidor, ni el protocolo, ni la base. |
| Precisa | La Q3 de [bots-found.md](bots-found.md) («el sitio libre más cercano a la capital») y su §4.2, y el plazo de la caminata (`foundWalkTimeoutMs`) de su §4.3. |
| Invariantes | No asigna invariantes. Regla nueva: `RN-BOT-026` (§4.3). Cita [INV-SEC-001](../invariants/security.md#inv-sec-001) (§8). |

## 1. Objetivo

La prueba en vivo de E41 (2026-11-27, [milestones.md](../roadmap/milestones.md) §E41) dejó a `_2_e29` con 9
búsquedas sin sitio (`found` con `status: "no_site"`) y sin tercera ciudad, con la capital en Castillos (tope 4) y
piedra de sobra. La cuenta, con las 48 ciudades en pie y los 64 territorios de la base de desarrollo de ese día (sin
mirar el terreno ni el bosque, que el espejo sólo conoce donde mira alguna conexión):

| Bot | Ciudad | Centro | Tiles del anillo de 28 a 96 en territorio libre y a 24 o más de todo centro | El más cercano |
|---|---|---|---|---|
| `_2_e29` | capital 22 | (221, 496) | **0** | a 99 |
| `_2_e29` | colonia 60 | (127, 448) | 3 933 | a 64 |
| `_3_e29` | capital 58 | (68, 104) | 2 072 | a 60 |
| `_3_e29` | colonia 59 | (40, 128) | 288 | a 88 |
| `_3_e29` | colonia 68 | (3, 192) | 3 201 | a 64 |

- **El anillo de la capital puede estar vacío.** La capital de `_2_e29` está a 15 tiles del borde de abajo del mapa
  de 512 × 512: medio anillo cae fuera, y lo que queda dentro son territorios con dueño (35 de los 64 lo tienen;
  sólo se funda en uno libre, [multi-city.md](multi-city.md)). Ningún sitio pasa, y no pasará mientras esos
  territorios tengan dueño.
- **Al lado de las colonias sí hay.** La colonia 60 de `_2_e29` tiene casi 4 000 tiles que pasan lo que se puede
  comprobar sin el terreno. Su conexión de colonia ([bots-all-cities.md](bots-all-cities.md) §4.3) ya ve el terreno y
  el bosque de alrededor; la búsqueda de hoy no lo lee: el decisor de la capital mira el terreno de la casa y de la
  campaña.
- **Ensanchar el anillo de la capital no basta.** A 128 tiles hay sitio en el papel, pero el espejo no conoce el
  terreno tan lejos (la vista de la casa llega a unos 80 tiles) y la caminata sale de la vista, el fallo de la
  ejecución larga de E32 ([bots-found.md](bots-found.md) §9.1).

## 2. Scope

**Entra.**

- En los bots (`apps/bots`), sin cambios en el servidor, el protocolo ni el cliente web: el sitio se busca en el
  anillo de 28 a 96 alrededor de **cada ciudad propia en pie**, con el terreno, el bosque y los territorios que traen
  todas las conexiones (§4.1, Q1).
- El aldeano y lo que cuesta siguen saliendo de la capital; el plazo de la caminata crece con la distancia (§4.2, Q2).
- Registro y resumen: de qué ciudad salió el anillo del sitio (§4.4).
- La prueba en vivo (Q3).

**No entra.**

- Ningún mensaje, campo, tabla, migración ni variable `EO_` nueva. Ningún cambio en el servidor ni en el cliente web.
- Que una colonia funde: su almacén es el que pagaría ([multi-city.md](multi-city.md) Q3: fundar cuesta a «la ciudad
  que funda») y una colonia no junta piedra ([bots-all-cities.md](bots-all-cities.md) Q6).
- Que el saqueador funde ([bots-found.md](bots-found.md) Q1).
- Elegir el sitio por su bosque, sus colinas o su distancia a otros bots: sigue siendo el más cercano que pasa.

## 3. Actores

| Actor | Papel |
|---|---|
| Persona que lo lanza | Lanza `pnpm run bots`, lee el resumen y decide si los números de la fundación cambian. |
| Constructor bot | Busca el sitio alrededor de todas sus ciudades y funda desde la capital. |
| Game Server | Valida el sitio y la fundación como para cualquier jugador ([INV-SEC-001](../invariants/security.md#inv-sec-001)). |

## 4. Modelo

### 4.1 El sitio (Q1)

El de [bots-found.md](bots-found.md) §4.2 (el 7 × 7 transitable, a 24 o más de todo centro conocido, 10 tiles de
bosque, territorio libre y sin reclamar, no apartado), pero el anillo de `foundMinDistance` (28) a
`foundMaxDistance` (96) se mide desde **cualquier ciudad propia en pie**: gana el sitio con menor distancia a la
ciudad propia más cercana; a igual distancia, el de la capital, luego el de la colonia de id menor, y dentro de cada
anillo el orden de hoy (`y` menor, luego `x` menor).

Lo que el decisor sabe del terreno, del bosque y de los territorios es la suma de lo que traen la casa, la campaña y
las conexiones de las colonias (hoy los territorios ya se suman, [bots-all-cities.md](bots-all-cities.md) §9.1; el
terreno, no).

### 4.2 La caminata (Q2)

El aldeano sale de la capital, como hoy (`RN-BOT-023`), con lo que cuesta apartado del almacén de la capital
(`RN-BOT-019`). Un sitio junto a una colonia lejana puede quedar a más de 96 tiles de la capital: el plazo de la
caminata (`foundWalkTimeoutMs`, 10 min) pasa a crecer con la distancia, como la vuelta de los varados: 10 min por
cada 96 tiles, y al menos 10. La campaña sigue al aldeano por sus tramos (`Founding.waypoint`).

### 4.3 Regla

| ID | Regla |
|---|---|
| `RN-BOT-026` | El sitio de la siguiente ciudad es el más cercano a cualquier ciudad propia en pie, en su anillo de `foundMinDistance` a `foundMaxDistance`, con lo que ven todas las conexiones del bot. |

### 4.4 Lo que se mide

`found` con `status: "started"` lleva además `from` (el id de la ciudad de cuyo anillo salió el sitio) y la distancia a
la capital que ya lleva (`distance`). El resumen cuenta las fundaciones por la ciudad del anillo (capital o colonia).

## 5. Errores

No hay códigos nuevos. Los de [bots-found.md](bots-found.md) §5 siguen valiendo: un sitio que el servidor rechaza se
aparta (`RN-BOT-021`) y la fundación vuelve a empezar con otro.

## 6. Eventos y contratos de red

Ningún mensaje ni valor de enum nuevo. Los bots leen el terreno de los `world.snapshot` y `world.chunk` de las
conexiones de colonia, que el espejo de `apps/web/src/state` ya guarda.

## 7. Persistencia

Sin cambios: el sitio y la fundación en curso son memoria del bot y no sobreviven a un reinicio del proceso
([bots-found.md](bots-found.md) §7).

## 8. Invariantes

No asigna invariantes. Cita [INV-SEC-001](../invariants/security.md#inv-sec-001): el bot propone un sitio y el servidor
decide.

## 9. Tests esperados

| Nivel | Qué |
|---|---|
| Unit (Vitest, `apps/bots`, `found.test.ts`) | Sin sitio junto a la capital, elige el de la colonia; a igual distancia, el de la capital; el terreno que sólo conoce la conexión de la colonia cuenta; el plazo de la caminata crece con la distancia; `found` con `from`. |
| Unit (Vitest, `log.test.ts`) | El resumen de las fundaciones por la ciudad del anillo. |
| Go, simulación / integración / e2e | Ninguno nuevo: las reglas del servidor no cambian. |

### 9.1 Precisiones de la implementación (2026-11-27)

- **Precisión (2026-11-27): un tile, en el anillo de su ciudad más cercana.** §4.1 ordena por la distancia a la
  ciudad propia más cercana: un tile a 28 de la capital pero a 26 de una colonia no está en ningún anillo (queda por
  debajo de `foundMinDistance` de la colonia), así que no se elige. Lo cubre `found.test.ts`.
- **Precisión (2026-11-27): `fromCapital`.** Además de `from` (el id), `found` con `started` lleva `fromCapital`
  (`true` si el sitio salió del anillo de la capital): el resumen cuenta así las fundaciones «junto a una colonia» sin
  tener que seguir qué id es la capital tras una caída.
- **Precisión (2026-11-27): el plazo de la caminata** (§4.2) usa `foundMaxDistance` (96) como tramo:
  `foundWalkTimeoutMs × max(1, ⌈distancia a la capital / 96⌉)` (`foundWalkMs`).
- **Precisión (2026-11-27): los bosques.** Además del terreno, la vista de la capital lleva los bosques de todas las
  conexiones, para que el sitio junto a una colonia no cuente un bosque agotado que sólo ve su conexión.

- **Precisión (2026-11-27): lo que el espejo olvida** (lo encontró la pasada 1 de la prueba en vivo). La cuarta de
  `_2_e29` volvió cinco veces con `INVALID_TARGET`, tile a tile: (62, 383), (63, 383), (64, 383) en la Región 0-5 y
  (210, 376), (210, 377) en la 3-5, las dos con dueño, y la segunda a 18 tiles de una ciudad ajena. El espejo conserva
  el terreno de lo que vio (es inmutable) pero vacía los territorios y las ciudades en cada snapshot: un sitio lejano
  con el terreno conocido pasaba por libre, y al llegar el aldeano exactamente al sitio no se volvía a mirar. Ahora el
  bot recuerda los territorios que ha visto, tal como los vio por última vez (`Bot.seenTerritories`), y los centros de
  las ciudades ajenas que ya no trae ninguna conexión (`View.rememberedCenters`, que salen al caer la ciudad); y,
  parado en el sitio, si lo que ya ve dice que no sirve, no pide fundar: la fundación termina con la causa nueva
  `site_lost` y el sitio se aparta (`RN-BOT-021`). Lo cubren `found.test.ts` y `bot.test.ts`, verificados por
  mutación (sin la comprobación al llegar, o sin recordar los territorios, cae su test).

- **Precisión (2026-11-27): la conexión de colonia vuelve a su centro** (misma pasada; un fallo de E41 que no se veía
  hasta que un bot con dos colonias fundaba otra). Tras fundar la 69, `_3_e29` marcó 37 min con una ciudad sin jugar:
  el snapshot de la fundación, centrado en la ciudad nueva (`ResnapshotAt`, [multi-city.md](multi-city.md) §9.1),
  llega a todas las sesiones del jugador, y la conexión de la colonia 59 se quedó mirando la 69. La casa ya volvía a la
  capital ([bots-found.md](bots-found.md) §4.5); ahora cada conexión de colonia cuyo snapshot no trae su ciudad vuelve
  a pedir su centro, una vez hasta ver la suya otra vez (`Colony.recentering`). Lo cubre `bot.test.ts` (su servidor
  falso da ahora a cada conexión las ciudades del jugador, `FakeServer.peer`), verificado por mutación.
- **Precisión (2026-11-27): la colonia fundada, una vez en el resumen** (misma pasada). La primera línea `colony` de la
  70 llegó dos segundos antes que su `found` con `founded`, y el resumen la contaba dos veces («logradas 2»). El
  `founded` completa ahora la colonia que ya hubiera (`log.test.ts`, verificado por mutación).

## 10. Decisiones de producto (2026-11-27)

La persona usuaria aceptó las tres recomendaciones (2026-11-27): cada fila deja la decisión, en negrita, y las
alternativas descartadas. Cambiarlas es cambiar la spec.

| # | Pregunta | Decisión | Descartadas |
|---|---|---|---|
| Q1 | ¿Dónde busca el sitio? | **En el anillo de 28 a 96 de cada ciudad propia en pie, el más cercano a cualquiera, con la capital primero a igual distancia y el terreno de todas las conexiones.** Es donde el bot ya ve el terreno, y con los números de §1 `_2_e29` pasa de 0 tiles posibles a casi 4 000. | **Ensanchar sólo el anillo de la capital** (p. ej. a 128 o 160: el espejo no conoce el terreno tan lejos y la caminata sale de la vista); **dejarlo como está** (una capital al borde del mapa o rodeada de territorios con dueño se queda en dos ciudades: es la medida de hoy). |
| Q2 | ¿De dónde sale el aldeano y quién paga? | **De la capital, como hoy, y el plazo de la caminata crece con la distancia (10 min por cada 96 tiles).** La capital es la única que junta piedra. | **De la ciudad más cercana al sitio** (camina menos, pero paga su almacén, y una colonia no junta piedra: habría que darle piedra o enviársela, otra estrategia); **de la capital con el plazo de hoy** (un sitio a 150 tiles fallaría por `timeout`). |
| Q3 | ¿Cómo es la prueba en vivo? | **Una pasada de 30 a 40 min a velocidad real con las cuentas `_e29`** (`_2_e29` sin sitio junto a la capital, `_3_e29` con tres ciudades en Castillos), sin sembrar. | **La ejecución larga de 3 h** (mide también la caída de una ciudad junto a una colonia, pero cuesta una tarde); **cuentas nuevas** (sin una capital al borde: no mide lo de §1). |

## 11. Documentos relacionados

- [bots-found.md](bots-found.md): el sitio, la caminata y su Q3, que esta spec precisa.
- [bots-all-cities.md](bots-all-cities.md): las conexiones de colonia y la fundación hasta el tope.
- [multi-city.md](multi-city.md): las reglas del servidor para el sitio y quién paga.
- [../roadmap/milestones.md](../roadmap/milestones.md): la prueba en vivo de E41 y la del repaso de la piedra.
