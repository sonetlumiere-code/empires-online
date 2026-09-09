# Visión de producto

Define qué es Empires Online, a quién está dirigido, qué experiencia entrega y qué decide no ser.

---

## 1. Definición en una frase

**Empires Online** es un MMORTS persistente 24/7 con componentes RPG: un único mundo continuo,
compartido, en vista isométrica, donde cada jugador funda y sostiene una civilización real que sigue
existiendo —y siendo vulnerable— cuando cierra el navegador.

No es un RTS con partidas. Es un mundo. La diferencia no es cosmética: cambia el modelo de datos,
el modelo de red, el modelo de sesión y, sobre todo, el significado de cada decisión del jugador.

---

## 2. Las dos herencias

Empires Online se construye sobre dos referencias conceptuales que aportan cosas distintas y que,
combinadas, definen un espacio que ninguna de las dos ocupa por separado.

| Referencia | Qué aporta | Qué no se toma |
|---|---|---|
| **Age of Empires** | Progresión por eras, identidad cultural mediante civilizaciones, gestión de aldeanos y ciudad, legibilidad isométrica del RTS clásico. | El formato de partida: lobby, condición de victoria, reset al terminar. |
| **Argentum Online** | Mundo persistente isométrico, tensión constante de riesgo/recompensa, comunidad como sistema de juego, consecuencias que perduran. | La estructura de RPG de personaje único y las razas fantásticas. |

De Age of Empires viene el **qué se hace**: construir, expandir, progresar de era, aprovechar los bonos
de una civilización concreta. De Argentum Online viene el **cuánto importa**: que lo construido pueda
perderse, que salir al mundo abierto sea una decisión y no un trámite, y que otros jugadores sean el
contenido principal, no un adorno.

---

## 3. Un mundo persistente cambia el género

En un RTS tradicional, el mundo existe mientras dura la partida. Todo lo que el jugador construye tiene
una fecha de caducidad conocida —el minuto en que alguien gana— y por eso ninguna decisión pesa más allá
de esa sesión. La estrategia es táctica comprimida.

En Empires Online el mundo no se detiene nunca. De ahí se derivan cuatro consecuencias que atraviesan
todo el diseño y toda la ingeniería.

**3.1. El tiempo es un recurso real.** Un aldeano que tarda 600 ms por tile de pradera tarda lo mismo
aunque nadie mire. Mover una unidad a través del mapa es una inversión de tiempo real, no una animación
que se acelera. El servidor simula a 10 Hz de forma continua y el movimiento de una unidad avanza
mientras su dueño duerme.

**3.2. El espacio tiene valor.** Cuando el mapa no se regenera cada partida, la posición donde fundaste
tu ciudad es permanente y las rutas entre ciudades son geografía estable. Un bosque denso deja de ser
textura y pasa a ser cobertura; una montaña deja de ser obstáculo y pasa a ser frontera. El territorio
se disputa porque es escaso y porque no se resetea.

**3.3. La ausencia es parte del diseño.** Si el mundo sigue girando, desconectarse es una acción con
consecuencias. Diseñar para eso —y no fingir que el jugador siempre está— es la diferencia entre un
mundo persistente y un mundo hostil. Por eso la protección offline es un sistema de primera clase, con
su propia máquina de estados (`ONLINE`, `OFFLINE_PENDING`, `PROTECTED`), y no un parche.

**3.4. La reputación existe.** En un mundo sin reinicio, romper un tratado es un hecho que queda. Las
relaciones entre jugadores se vuelven infraestructura social duradera, y por eso los tratados
(`NON_AGGRESSION`, `ALLIANCE`, `TRADE`) son estado persistido y validado por el servidor, no un acuerdo
de palabra en un chat.

El coste técnico de esto es explícito y aceptado: no hay "fin de partida" que limpie el estado, no hay
momento en que se pueda descartar la simulación, y cualquier bug de consistencia es permanente hasta que
alguien lo repara. La arquitectura se diseña con esa premisa desde el primer día
(ver [../architecture/overview.md](../architecture/overview.md)).

---

## 4. La experiencia que entrega

### 4.1 El bucle de juego

```
Fundar ciudad  ->  Expandir y progresar de era  ->  Salir al mundo abierto
      ^                                                      |
      |                                                      v
  Reconstruir  <-  Perder o defender  <-  Encontrarse con otros jugadores
```

Ese bucle no se cierra en veinte minutos. Se cierra en semanas, y cada vuelta deja huella en el mapa
compartido.

### 4.2 La tensión riesgo/recompensa

Es el corazón heredado de Argentum Online y la razón por la que el juego se sostiene sin contenido
generado artificialmente. Se articula en tres ejes:

- **Salir cuesta.** Todo lo valioso está fuera de la zona segura. Las unidades tardan tiempo real en
  llegar, y ese tiempo es exposición.
- **Volver también cuesta.** El camino de vuelta es tan largo como el de ida y se recorre con lo que se
  ha conseguido, es decir, con más que perder.
- **Esconderse es una jugada válida.** Las Safe Zones (`DENSE_FOREST`, `CAVERN`) permiten refugio en el
  mundo abierto, pero la seguridad la calcula y valida siempre el servidor: no es un estado que el
  cliente pueda afirmar. Ver [../specs/safe-zones.md](../specs/safe-zones.md).

La consecuencia buscada es que cada expedición sea una decisión consciente, con un cálculo de pérdida
esperada, en lugar de una acción rutinaria. Un juego donde nada se pierde no genera esa decisión.

### 4.3 El jugador que buscamos

| Perfil | Qué busca | Qué le damos |
|---|---|---|
| Veterano de RTS cansado del formato partida | Que sus decisiones estratégicas duren más de una sesión | Un imperio que persiste y se puede planificar a semanas |
| Veterano de mundos persistentes (Argentum Online y similares) | Riesgo real, comunidad, mundo compartido con historia | Pérdida posible, tratados vinculantes, territorio disputado |
| Jugador social/gremial | Interdependencia, negociación, política entre grupos | Civilizaciones con capacidades exclusivas y facciones globales |
| Jugador de sesiones cortas | Poder conectarse 30 minutos sin quedar destruido | Protección offline acotada y explícita, no invulnerabilidad |

Empires Online **no** está diseñado para quien quiere una partida cerrada de 25 minutos con condición de
victoria. Ese jugador está mejor servido por un RTS clásico, y decirlo por adelantado es más honesto que
intentar contentar a ambos.

---

## 5. Civilizaciones y facciones: dos ejes, no uno

Todos los jugadores son **humanos**. No hay razas. La variedad viene de dos ejes **ortogonales e
independientes**, y mantenerlos separados es una decisión de diseño deliberada.

```
                        Global Faction
                 ORDER      NEUTRAL      CHAOS
              +----------+----------+----------+
      Roman   |          |          |          |
              +----------+----------+----------+
  Byzantine   |          |          |          |
              +----------+----------+----------+
    Persian   |          |          |          |
              +----------+----------+----------+
      Norse   |          |          |          |
              +----------+----------+----------+
```

**Civilization** es la identidad cultural: unidades, tecnologías y bonos propios (Roman, Byzantine,
Persian, Norse…). Es una elección de *cómo juegas* y de *qué puedes producir que otros no pueden*.

**Global Faction** (`ORDER`, `CHAOS`, `NEUTRAL`) es la alineación en el conflicto global. Es una elección
de *con quién juegas* y define el marco de conflicto de alto nivel.

Que sean ejes independientes significa que dos romanos pueden ser enemigos declarados y que un norse de
ORDER y un persa de ORDER pueden ser aliados naturales pese a no compartir nada cultural. Esto produce
un mapa político mucho más rico que un sistema de facciones único, y evita el efecto "todos los de mi
raza son mis amigos" que aplana la diplomacia.

La consecuencia económica es la parte importante: **las capacidades exclusivas por civilización crean
comercio real**. Si una civilización puede producir algo que otra necesita y no puede fabricar, la
negociación deja de ser opcional. La interdependencia no se fuerza con reglas artificiales, se induce
desde el diseño de contenido. El detalle del sistema económico está **fuera de MVP**; lo que no está
fuera es la decisión de estructura: los dos ejes existen desde el modelo de datos inicial
(tablas `civilizations` y `factions`, ver [../database/schema.md](../database/schema.md)).

---

## 6. Progresión

La progresión temporal se articula en **eras**, definidas como datos en la tabla `eras` y no en código:

| Era | Population cap |
|---|---|
| `STONE_AGE` | 20 |
| `BRONZE_AGE` | 50 |
| `IRON_AGE` | 100 |
| `CASTLE_AGE` | 150 |

Cada era amplía el techo de población de la ciudad y, en el diseño objetivo, abre unidades y tecnologías.
Que las eras vivan en base de datos y no en constantes es lo que permite ajustar el ritmo del juego sin
recompilar el servidor, y es coherente con la regla general de que ningún valor de gameplay se hardcodea.

---

## 7. Lo que Empires Online NO es

Esta sección es tan vinculante como las anteriores. Sirve para rechazar propuestas sin volver a discutir
los fundamentos.

**No es un lobby de partidas.** No hay salas, no hay emparejamiento, no hay condición de victoria, no hay
reinicio. No existe un botón de "nueva partida" porque no existe el concepto de partida. Un jugador que
se conecta entra al mismo mundo en el que estaba, con el estado exacto que dejó más lo que haya ocurrido
en su ausencia.

**No es un idle game.** El mundo avanza sin el jugador, pero el jugador no progresa sin jugar. La
persistencia sirve para que las consecuencias duren, no para que los recursos se acumulen solos mientras
la pestaña está cerrada. La ausencia se protege; no se premia.

**No es un juego de razas fantásticas.** Todos los jugadores son humanos. Elfos, orcos y demás quedan
fuera por definición, no por falta de tiempo.

**No es un RTS de micro por unidad.** El cliente envía intenciones (`unit.move { unitId, target }`),
nunca secuencias de posiciones. El servidor calcula el camino y decide la verdad. Esto excluye por
diseño el estilo de juego basado en precisión mecánica milisegundo a milisegundo, y a cambio garantiza
que nadie gane por tener mejor conexión o un cliente modificado.

**No es un simulador militar naval.** El terreno `WATER` está bloqueado en el MVP y el sistema naval
queda **fuera de MVP**.

**No es un juego pay-to-win ni un catálogo de features.** El criterio de aceptación de cualquier
funcionalidad nueva es que refuerce uno de los pilares de
[game-pillars.md](game-pillars.md). Si no lo hace, no entra, aunque sea barata de implementar.

---

## 8. Estado actual y siguiente paso

El objetivo del primer vertical slice no es "tener un juego" sino **demostrar que el eje persistente
funciona de extremo a extremo**: un jugador entra, mueve un aldeano, se desconecta, el aldeano sigue
caminando, y al volver el mundo está donde debía estar.

**Dónde está el proyecto hoy.** El lado servidor de ese slice ya existe y compila: el módulo Go de
`services/game-server` —mundo determinista, pathfinding, game loop a 10 Hz, movimiento como polilínea
temporizada, WebSocket con sesiones e interest management, presencia y protección offline, persistencia
en PostgreSQL y Redis— junto con el paquete compartido `packages/protocol`. Su suite de tests unitarios,
de contrato, de simulación y de recuperación está **en verde**.

Lo que falta para cerrar el slice, y que ningún documento debe dar por hecho:

- **El cliente `apps/web/`** (Next.js + React + PixiJS) todavía **no existe**. Sin él el flujo no es
  observable por un humano, sólo por un test.
- **Los tests de integración** contra PostgreSQL y Redis reales están escritos pero **no ejecutados**:
  el daemon de Docker no arrancó en la máquina de desarrollo.
- **La CI** todavía no está montada.
- El alta de jugador (`POST /api/auth/register`, `POST /api/auth/login`) vive hoy **en el game server**,
  no en Next.js como prevé ADR-010. Es provisional y está registrado como deuda técnica.

Todo lo demás —combate, economía completa, árbol tecnológico, comercio, caravanas, clanes— se construye
después, sobre una base que ya haya demostrado esa propiedad.

Ver [mvp-scope.md](mvp-scope.md) para el alcance exacto y
[../roadmap/roadmap.md](../roadmap/roadmap.md) para el orden de construcción.

---

## Documentos relacionados

- [game-pillars.md](game-pillars.md) — los pilares que traducen esta visión en restricciones técnicas.
- [glossary.md](glossary.md) — vocabulario canónico del dominio.
- [mvp-scope.md](mvp-scope.md) — qué se construye primero.
- [../architecture/overview.md](../architecture/overview.md) — cómo se materializa técnicamente.
