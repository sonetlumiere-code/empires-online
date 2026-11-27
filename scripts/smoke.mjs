#!/usr/bin/env node
/**
 * Comprobación de humo: el vertical slice completo contra un servidor VIVO.
 *
 * No sustituye a ningún test. Los tests verifican el código; esto verifica un
 * DESPLIEGUE: que este binario, contra esta base de datos, con este estado
 * caliente y esta configuración, hace lo que promete. Es lo que hay que
 * ejecutar tras desplegar en una máquina nueva.
 *
 * Recorre el flujo que define el producto:
 *
 *   1. /health y /ready responden
 *   2. alta o login en Next.js  ->  cookie de sesión + ticket efímero
 *   3. WebSocket + session.hello  ->  session.welcome
 *   4. world.snapshot con la ciudad y los aldeanos
 *   5. unit.move  ->  el servidor calcula la ruta y responde con la polilínea
 *   6. SE CIERRA la conexión a mitad de movimiento
 *   7. se espera, sin cliente conectado, más allá de la hora de llegada
 *   8. ticket nuevo pedido con la cookie, reconexión  ->  la unidad está EN EL DESTINO
 *   9. unit.gather sobre el bosque más cercano a la ciudad  ->  un viaje completo:
 *      tramo al bosque, WORKING, vuelta y city.update con la madera depositada
 *      (SPEC-ECON-WOOD)
 *   9b. sólo con --civilizacion (SPEC-CIVILIZATION): un alta sin civilización o con una
 *      desconocida se rechaza; smoke_norse (que se da de alta NORSE la primera vez) ve
 *      su ciudad nórdica y mueve un aldeano a la velocidad nórdica; y una casa de la
 *      ciudad del humo tiene el plazo de su civilización. En todas las pasadas, además,
 *      la ciudad lleva su civilización y la polilínea del paso 5 va a su velocidad.
 *  10. sólo con --tala (SPEC-ECON-FOREST): todos los aldeanos recolectan del mismo
 *      tile hasta agotarlo, y al menos uno pasa solo a otro tile con madera a 6 o
 *      menos. Tarda minutos y gasta bosque de verdad, así que no corre por defecto.
 *  10b. sólo con --piedra (SPEC-ECON-STONE): busca la colina más cercana a la ciudad
 *      moviendo la vista (las de desarrollo quedan a 160 tiles o más, fuera del área
 *      de interés), manda un aldeano a recoger piedra y espera la primera carga
 *      (hill.update y la carga con carryingResource STONE) y su depósito en la
 *      piedra de la ciudad. Tarda minutos, así que no corre por defecto.
 *  10c. sólo con --avance (SPEC-ECON-STONE): con 400 de madera y 200 de piedra en la
 *      ciudad (EO_DEVSEED_STOCK_FOR=smoke_runner pnpm run dev:seed los siembra en
 *      desarrollo), encarga el avance a la Edad de Bronce, comprueba que no entrena
 *      mientras dura y espera los 90 s hasta la era nueva con el límite subido. Es
 *      irreversible: una ciudad en Bronce no puede repetirlo.
 *  11. sólo con --incursion (SPEC-COMBAT-RAID): con 60 de madera se entrena un
 *      guerrero; un segundo jugador acerca un aldeano y el guerrero lo mata. Tarda
 *      minutos (juntar madera, entrenar, caminar) y mata una unidad de otra cuenta,
 *      así que no corre por defecto ni tras un despliegue en producción.
 *      El aldeano va escoltado por un guardia de la víctima, que responde solo al
 *      ataque (SPEC-COMBAT-RESPONSE); si le faltan guardia o aldeanos, los entrena. La
 *      víctima recibe el aviso en vivo UNIT_LOST y smoke_runner ENEMY_KILLED.
 *  12. sólo con --saqueo (SPEC-COMBAT-PILLAGE): un guerrero propio (el que haya en
 *      casa, o uno nuevo) va hasta la muralla de la víctima, que se conecta para no
 *      estar protegida, y la saquea: su almacén baja, la víctima recibe el aviso
 *      pillagedAtMs y el guerrero carga lo robado. Otra sesión de smoke_runner, con la
 *      vista en su ciudad, recibe el aviso en vivo PILLAGE_DONE (SPEC-LIVE-NOTICES), y
 *      el saqueado ninguno. Tarda minutos y roba madera de otra cuenta, así que no
 *      corre por defecto.
 *  12b. sólo con --asedio (SPEC-COMBAT-SIEGE): la víctima, conectada, tiene una casa
 *      completa (si no, junta 30 de madera y la construye); un guerrero propio la
 *      golpea hasta derribarla: la vida baja, la víctima recibe besiegedAtMs, llega
 *      building.destroyed y su límite de población baja en 5; smoke_runner, con la vista
 *      en su ciudad, recibe el aviso en vivo HOUSE_DESTROYED. Tarda minutos y derriba
 *      una casa de otra cuenta, así que no corre por defecto.
 *  12d. sólo con --ariete (SPEC-SIEGE-ENGINES): smoke_victim, en Bronce o después, tiene su
 *      muralla (si no, la levanta en el centro: 200 de piedra, 100 de madera y 120 s con el
 *      rasgo de su civilización); smoke_runner, en Hierro o después, tiene un ariete (si no,
 *      lo entrena: 150 de madera, 75 de piedra y dos plazas), lo lleva junto a la ciudad de
 *      la víctima (a 1 800 ms por tile, unos minutos) y le ordena derribar una casa, que se
 *      rechaza (INVALID_TARGET: primero, la muralla), y después la muralla: su vida baja de
 *      1 500 y la víctima recibe besiegedAtMs. Un guerrero de smoke_runner junto a la víctima
 *      (el de --asedio), si lo hay, tampoco puede derribar la casa. Con --ariete-completo, el
 *      ariete sigue hasta abrirla (38 golpes, unos dos minutos): building.destroyed y el aviso
 *      HOUSE_DESTROYED con buildingType WALL en la sesión de smoke_runner con la vista en
 *      casa. Necesita madera y piedra en las dos ciudades (con el servidor parado,
 *      EO_DEVSEED_STOCK_FOR=smoke_runner,smoke_victim pnpm run dev:seed) y que no haya un
 *      pacto entre los dos: va después de --asedio y antes de --diplomacia.
 *  12c. sólo con --territorio (SPEC-TERRITORY-CONQUEST): un guerrero de smoke_runner
 *      junto a la víctima (el que dejan --saqueo o --asedio) va al territorio libre más
 *      cercano y lo reclama: llega territory.update en disputa y, al vencer el plazo, el
 *      territorio es suyo y su ciudad gana 5 de población. Después smoke_victim, con un
 *      guerrero suyo (si no tiene, lo entrena), se lo reconquista: smoke_runner, con la vista en su ciudad,
 *      recibe TERRITORY_CLAIMED y TERRITORY_LOST, su límite vuelve, y smoke_victim recibe
 *      TERRITORY_TAKEN. Con los plazos por defecto tarda unos 16 minutos: en
 *      desarrollo, EO_TERRITORY_CLAIM_FREE_SECONDS y EO_TERRITORY_CLAIM_SECONDS al
 *      arrancar el servidor. Va antes de --diplomacia, cuyo pacto impediría la
 *      reconquista.
 *  13. sólo con --diplomacia (SPEC-DIPLOMACY): smoke_runner y smoke_victim firman un
 *      pacto de no agresión (o reutilizan el vigente), el pacto impide el saqueo, y
 *      smoke_runner lo rompe: el tratado sigue vigente con su preaviso. Como el preaviso
 *      dura 15 minutos, repetirlo antes falla con un aviso de cuánto falta.
 *  13b. sólo con --comercio (SPEC-TRADE): smoke_runner y smoke_victim usan el tratado
 *      vigente entre los dos o firman uno TRADE; una oferta de más madera de la que hay
 *      se rechaza con INSUFFICIENT_RESOURCES; smoke_runner ofrece 50 de madera por 10 de
 *      piedra, los dos la ven con su lado, smoke_victim la acepta y los dos almacenes
 *      cambian en el acto; el registro de smoke_runner lo cuenta. smoke_victim tiene que
 *      tener 10 de piedra (con el servidor parado, EO_DEVSEED_STOCK_FOR=smoke_victim
 *      pnpm run dev:seed). Va después de --diplomacia: si su pacto está en preaviso, se
 *      comercia con él.
 *  13c. sólo con --bienes (SPEC-CIVILIZATION-GOODS): la ciudad del humo, en Bronce, tiene un
 *      taller (si no, lo construye con su plazo, el de su civilización) y elabora 5 de su bien
 *      (vino si es romana) con su plazo; el avance a Hierro se rechaza sin bienes de otras
 *      civilizaciones; y, si hay un tratado vigente con smoke_victim (lo deja --comercio), le
 *      cambia 5 de su bien por 1 de madera. Va después de --comercio. Necesita 130 de madera
 *      y 65 de piedra si no hay taller (EO_DEVSEED_STOCK_FOR=smoke_runner pnpm run dev:seed).
 *  13d. sólo con --caravana (SPEC-CARAVANS): con el tratado vigente entre smoke_runner y
 *      smoke_victim (el de --comercio), un trato de 1 de madera por 1 de piedra saca dos
 *      caravanas; los guerreros de smoke_runner que están junto a la ciudad de la víctima
 *      (los de --saqueo o --asedio) atacan la de piedra y la capturan, la víctima recibe el
 *      aviso CARAVAN_CAPTURED y la caravana llega a la ciudad de smoke_runner. Va después de
 *      --bienes. Desde E15, --comercio y --bienes esperan además a que llegue la caravana de
 *      su trato (unos minutos entre las dos ciudades del humo).
 *  13e. sólo con --unidad-civilizacion (SPEC-CIVILIZATION-UNITS): la ciudad del humo pide la
 *      unidad de otra civilización y se rechaza (INVALID_TARGET); en la Edad de Piedra, la
 *      propia también. En Bronce, si le faltan los 10 de su bien, los elabora en su taller
 *      (el de --bienes); entrena su unidad propia pagando 80 de madera, 40 de piedra y 10 del
 *      bien, con el plazo de su civilización, y la ve nacer con su tipo y su vida. Va después
 *      de --bienes. Necesita 80 de madera y 40 de piedra, más 30 y 15 por elaboración.
 *  13f. sólo con --castillo (SPEC-CASTLE-AGE): la ciudad del humo avanza lo que le falte
 *      hasta la Edad de los Castillos (Bronce 90 s, Hierro 180 s, Castillos 360 s; cada
 *      avance es irreversible, como --avance, y desde Castillos se rechaza con
 *      NOT_IMPLEMENTED), construye su castillo si no lo tiene (180 s con el rasgo de su
 *      civilización), aparta a sus guardias (responderían al asedio y rematarían al
 *      sitiador) y un guerrero de smoke_victim lo asedia: el castillo apunta a él
 *      (building.update con targetUnitId), le dispara y lo mata, y los dos reciben el
 *      aviso (UNIT_LOST_TO_CASTLE y CASTLE_KILL). La primera pasada tarda unos doce
 *      minutos; las siguientes, sólo el disparo. Necesita el almacén de los avances, los
 *      bienes ajenos y el castillo (con el servidor parado,
 *      EO_DEVSEED_STOCK_FOR=smoke_runner,smoke_victim pnpm run dev:seed) y que no haya un
 *      pacto entre los dos: va antes de --diplomacia.
 *  13g. sólo con --tecnologia (SPEC-TECHNOLOGIES): la ciudad del humo ve en su propia vista
 *      sus tecnologías; una tecnología que no existe se rechaza (INVALID_TARGET); elige
 *      la primera a su alcance que pueda pagar (Albañilería si puede: su efecto se ve
 *      en la vida de una casa), la encarga y recibe la investigación con su plazo (el
 *      del catálogo con su production.speed, o EO_TECHNOLOGY_RESEARCH_SECONDS si se
 *      exporta igual que al servidor para no esperar); otra a la vez es CITY_BUSY; al
 *      vencer recibe la tecnología, y con Albañilería sus casas aguantan un 20 % más
 *      (encarga una si no tiene). Cuando ya tiene todas las que puede pagar, lo dice y
 *      sigue. Necesita madera y piedra (con el servidor parado,
 *      EO_DEVSEED_STOCK_FOR=smoke_runner pnpm run dev:seed).
 *  13h. sólo con --mercado (SPEC-MARKETS): smoke_runner y smoke_victim, los dos en la Edad de
 *      Bronce o después, tienen su mercado (si no, lo construyen: 150 de madera, 100 de piedra
 *      y 60 s con el rasgo de su civilización); las ofertas abiertas de smoke_runner de una
 *      pasada anterior se retiran; dar y pedir lo mismo se rechaza (INVALID_TARGET);
 *      smoke_runner publica 2 lotes de 10 de madera por 2 de piedra y su almacén pierde 22
 *      (20 en custodia y 2 de comisión); smoke_victim lee el tablón (market.listing), no se
 *      la puede comprar el vendedor (INVALID_TARGET) y compra un lote pagando 3 de piedra
 *      (2 y 1 de comisión); los dos reciben market.update, y la caravana de madera llega a
 *      smoke_victim; smoke_runner retira el lote que queda y le vuelven 10 de madera, sin la
 *      comisión; su registro cuenta la venta (MARKET_SOLD). La caravana tarda minutos.
 *      Necesita madera y piedra en las dos ciudades (con el servidor parado,
 *      EO_DEVSEED_STOCK_FOR=smoke_runner,smoke_victim pnpm run dev:seed).
 *  13i. sólo con --caida (SPEC-CITY-FALL): una cuenta propia, smoke_caida (nunca
 *      smoke_victim, cuya ciudad se mueve), en la Edad de Bronce o después (la primera vez
 *      avanza desde Piedra: con el servidor parado, EO_DEVSEED_STOCK_FOR=smoke_caida pnpm run
 *      dev:seed le siembra lo del avance) y sin muralla, se conecta; un guerrero de
 *      smoke_runner (el que haya, o uno nuevo) va hasta su ciudad y la asalta (unit.assault):
 *      smoke_caida recibe el aviso CITY_ASSAULTED y los dos el asalto en city.update; al
 *      vencer el plazo, la ciudad cae: los dos reciben city.fallen, smoke_caida el aviso
 *      CITY_LOST y un snapshot con su ciudad refundada (con el mismo nombre, otro id, otra
 *      era no) y smoke_runner CITY_TAKEN. Si smoke_runner tiene una sola ciudad, se la queda
 *      (SPEC-CITY-TAKEN): city.fallen trae takenCityId y su snapshot, la tomada en el mismo centro. Arranca el servidor con EO_CITY_ASSAULT_SECONDS=20
 *      y EO_CITY_FALL_IMMUNITY_HOURS=0. La ciudad refundada se puede volver a asaltar en
 *      la pasada siguiente. Va la última de las que mueven guerreros de smoke_runner.
 *  13j. sólo con --multiciudad (SPEC-MULTI-CITY): una cuenta propia, smoke_multi
 *      (EO_SMOKE_MULTI_USER), en la Edad de Bronce o después (la primera vez avanza desde
 *      Piedra) con 800 de madera y 400 de piedra (con el servidor parado,
 *      EO_DEVSEED_STOCK_FOR=smoke_multi pnpm run dev:seed), lleva un aldeano a un sitio
 *      libre a 28 o más tiles de su capital (lo busca moviendo la vista) y funda
 *      (unit.found_city): recibe un snapshot centrado en la ciudad nueva, con el primer
 *      nombre libre de su serie (SPEC-CITY-NAMES: smoke_multipolis II si sólo tiene la
 *      capital), en su era y en el tile del aldeano; la capital paga, y una
 *      tercera se rechaza (INVALID_TARGET). En una
 *      pasada siguiente la cuenta ya tiene dos y el humo sólo comprueba ese rechazo. No
 *      depende de las demás opciones.
 *  14. sólo con --registro (SPEC-EVENT-LOG): smoke_victim se conecta y su registro
 *      nombra a smoke_runner (el saqueo, con --saqueo; la casa, con --asedio; la
 *      ruptura, con --diplomacia);
 *      smoke_runner ve su lado; la víctima da por visto lo asentado, reconecta con un
 *      ticket nuevo y el cursor sigue marcado. Necesita que antes haya pasado algo
 *      entre los dos: en la misma pasada con --saqueo, --asedio o --diplomacia, o en
 *      una anterior. En una misma pasada el orden es saqueo, asedio, territorio,
 *      diplomacia, comercio y registro: el pacto de --diplomacia impediría el asedio y
 *      la reconquista durante su preaviso.
 *  15. sólo con --mensajes (SPEC-MESSAGES): smoke_runner escribe a smoke_victim, que lo
 *      recibe en vivo con smoke_runner nombrado por su ciudad; con smoke_victim
 *      desconectado, el segundo mensaje le espera en la bandeja como no leído, lo lee y
 *      deja de estarlo; smoke_victim bloquea a smoke_runner y ninguno de los dos puede
 *      escribir al otro; al desbloquear, vuelve a llegar. No depende de las demás
 *      opciones y puede ir al final de cualquier pasada.
 *  16. sólo con --faccion (SPEC-FACTIONS): dos cuentas propias, smoke_orden y
 *      smoke_caos (nunca smoke_runner ni smoke_victim, que tienen que seguir NEUTRAL
 *      para el saqueo, el asedio y la diplomacia), entran en la Orden y en el Caos;
 *      la entrada vuelve con su requestId y cuenta al vencer el preaviso (arranca el
 *      servidor con EO_FACTION_JOIN_SECONDS=20), una segunda se rechaza, un pacto
 *      entre las dos se rechaza y un trato de comercio, no (se retira al momento).
 *      En una pasada siguiente, con las cuentas ya en su bando, sólo comprueba los
 *      rechazos. No depende de las demás opciones.
 *  17. sólo con --clan (SPEC-CLANS): dos cuentas propias, smoke_clan_a y smoke_clan_b
 *      (nunca smoke_runner ni smoke_victim). smoke_clan_a funda el clan «Humo» [HUM]
 *      (o ya es su líder), invita a smoke_clan_b, que acepta; los dos reciben el clan
 *      con su requestId; un mensaje del canal llega en vivo al otro y su página lo
 *      trae; con smoke_orden dentro (si --faccion ya la metió en la Orden), invitar a
 *      smoke_caos se rechaza; smoke_orden ve el clan por su vista pública; y al final
 *      smoke_clan_b es expulsado, con su aviso, para que la pasada siguiente vuelva a
 *      empezar. Con smoke_orden dentro (SPEC-CLAN-GOVERNANCE), smoke_clan_a la nombra
 *      oficial, ella invita y expulsa a smoke_clan_b, smoke_clan_a le cede el liderazgo
 *      y ella se lo devuelve (cada uno con su aviso), y al final vuelve a miembro raso.
 *      No depende de las demás opciones; la parte de los bandos y del gobierno, de
 *      --faccion.
 *  18. sólo con --territorio-clan (SPEC-CLAN-TERRITORIES): el clan [HUM] de --clan y
 *      smoke_norse (NEUTRAL y sin clan) se disputan por turnos el territorio libre más
 *      cercano a la ciudad de smoke_clan_a: si está libre, smoke_clan_a lo reclama para
 *      el clan; si es del clan, smoke_norse se lo quita y el miembro conectado recibe
 *      los avisos con la sigla; si es de smoke_norse, el clan lo recupera. Arranca el
 *      servidor con los plazos cortos de --territorio y, la primera vez, con 60 de
 *      madera en las dos ciudades (EO_DEVSEED_STOCK_FOR=smoke_clan_a,smoke_norse) para
 *      entrenar sus guerreros. Va antes de --clan en la pasada.
 *  19. sólo con --alianza-clan (SPEC-CLAN-ALLIANCES): smoke_clan_b (sin clan tras
 *      --clan; si le quedó uno, sale) funda «Eco» [ECO]; smoke_clan_a, líder de [HUM],
 *      le propone una alianza, smoke_clan_b la recibe con su aviso y la acepta, y a
 *      smoke_clan_a le llega el aviso; la vista pública de [HUM] la lleva; el guerrero
 *      de smoke_clan_a no puede atacar a un aldeano de smoke_clan_b (justo después de
 *      --clan cuenta antes la paz residual de su expulsión; con el servidor arrancado
 *      con EO_CLAN_PEACE_GRACE_SECONDS=5 se ve la de la alianza); smoke_clan_a la
 *      rompe y smoke_clan_b recibe el aviso del preaviso; y smoke_clan_b sale de [ECO],
 *      que se disuelve y expira la alianza, para que --clan lo vuelva a encontrar sin
 *      clan. Necesita el clan de --clan y va después de él.
 *  20. sólo con --guerra-clan (SPEC-CLAN-WARS): smoke_clan_b (sin clan; si le quedó uno,
 *      sale) funda «Rivales» [RIV]; smoke_clan_a, líder de [HUM], le declara la guerra y
 *      smoke_clan_b recibe el aviso; la vista pública de [HUM] la lleva; al terminar la
 *      preparación (EO_TREATY_BREAK_NOTICE_MINUTES: arrancar el servidor con 1 para no
 *      esperar 15) la guerra empieza; un aldeano de smoke_clan_b se acerca al guerrero de
 *      smoke_clan_a, que lo mata, y el marcador de [HUM] sube a 1; smoke_clan_b ofrece la
 *      paz, smoke_clan_a recibe el aviso y la acepta, y la guerra termina en empate con
 *      su aviso y su entrada del registro; y smoke_clan_b sale de [RIV], que se disuelve
 *      (un clan nuevo cada pasada, así que la tregua no estorba). Necesita el clan de
 *      --clan y un guerrero de smoke_clan_a (el de --territorio-clan); va después de
 *      --alianza-clan.
 *
 * El paso 6-8 es el que importa: demuestra que el mundo no depende de que
 * nadie esté mirando. Si el movimiento sólo avanzara mientras hay un
 * WebSocket abierto, aquí es donde se vería.
 *
 * Uso:
 *   node scripts/smoke.mjs [--url http://localhost:8080] [--web http://localhost:3000] [--tala] [--piedra] [--avance] [--incursion] [--saqueo] [--asedio] [--ariete] [--ariete-completo] [--territorio] [--diplomacia] [--comercio] [--bienes] [--unidad-civilizacion] [--castillo] [--tecnologia] [--mercado] [--caravana] [--registro] [--mensajes] [--faccion] [--territorio-clan] [--clan] [--alianza-clan] [--guerra-clan] [--civilizacion] [--verbose]
 *
 * `--url` es el Game Server (salud y WebSocket); `--web` es Next.js, que desde
 * ADR-018 da de alta, abre la sesión web y emite los tickets.
 *
 * Requiere Node 22 o superior: usa `fetch` y `WebSocket` globales, sin
 * dependencias. Sale con código 1 al primer fallo.
 */

import { readFileSync } from 'node:fs';
import { pathToFileURL } from 'node:url';

const args = process.argv.slice(2);
const flag = (name, fallback) => {
  const i = args.indexOf(name);
  return i >= 0 && args[i + 1] ? args[i + 1] : fallback;
};

const BASE = (flag('--url', process.env.EO_SMOKE_URL || 'http://localhost:8080')).replace(/\/+$/, '');
const WS_URL = BASE.replace(/^http/, 'ws') + '/ws';
const WEB = (flag('--web', process.env.EO_SMOKE_WEB_URL || 'http://localhost:3000')).replace(/\/+$/, '');
const VERBOSE = args.includes('--verbose');
const INCURSION = args.includes('--incursion');
const TALA = args.includes('--tala');
const PIEDRA = args.includes('--piedra');
const AVANCE = args.includes('--avance');
const SAQUEO = args.includes('--saqueo');
const ASEDIO = args.includes('--asedio');
const ARIETE_COMPLETO = args.includes('--ariete-completo');
const ARIETE = args.includes('--ariete') || ARIETE_COMPLETO;
const DIPLOMACIA = args.includes('--diplomacia');
const COMERCIO = args.includes('--comercio');
const TERRITORIO = args.includes('--territorio');
const REGISTRO = args.includes('--registro');
const CIVILIZACION = args.includes('--civilizacion');
const BIENES = args.includes('--bienes');
const UNIDAD_CIVILIZACION = args.includes('--unidad-civilizacion');
const CASTILLO = args.includes('--castillo');
const TECNOLOGIA = args.includes('--tecnologia');
const MERCADO = args.includes('--mercado');
const CARAVANA = args.includes('--caravana');
const MENSAJES = args.includes('--mensajes');
const FACCION = args.includes('--faccion');
const CLAN = args.includes('--clan');
const ALIANZA_CLAN = args.includes('--alianza-clan');
const GUERRA_CLAN = args.includes('--guerra-clan');
const CAIDA = args.includes('--caida');
const MULTICIUDAD = args.includes('--multiciudad');
const PROTOCOL_VERSION = 1;

// Terreno: el byte que viaja en el snapshot es el valor de world.TerrainType.
// MOUNTAIN y WATER no son transitables (internal/game/world/tile.go).
const NO_TRANSITABLE = new Set([3, 4]);
// FOREST da madera (RN-ECON-001) y HILL piedra (SPEC-ECON-STONE, RN-ECON-019).
const FOREST = 1;
const HILL = 2;
// Coste de cada terreno transitable en décimas del base (world.CostBase = 10).
const COSTE_TERRENO = new Map([[0, 10], [1, 16], [2, 18], [5, 6]]);

/**
 * El catálogo de presentación de las civilizaciones (SPEC-CIVILIZATION §6), el
 * mismo que embebe el Game Server y que un test de integración ata a la tabla.
 * Con él, el humo calcula lo que tarda cada paso sin copiar ningún número.
 */
const CATALOGO = JSON.parse(
  readFileSync(new URL('../packages/protocol/schema/v1/civilizations.json', import.meta.url), 'utf8'),
).civilizations;

/**
 * El catálogo de presentación de las unidades (SPEC-CIVILIZATION-UNITS §6), el que
 * un test del Game Server ata al catálogo de Go: costes, plazos y vida sin copiar
 * ningún número.
 */
const UNIDADES = JSON.parse(
  readFileSync(new URL('../packages/protocol/schema/v1/units.json', import.meta.url), 'utf8'),
).units;

/**
 * El catálogo de presentación de las tecnologías (SPEC-TECHNOLOGIES §6), el que un
 * test de integración ata a la tabla: costes, plazos y efectos sin copiar números.
 */
const TECNOLOGIAS = JSON.parse(
  readFileSync(new URL('../packages/protocol/schema/v1/technologies.json', import.meta.url), 'utf8'),
).technologies;

/** El rasgo por mil de una civilización; 1000 si no lo declara (RN-CIV-003). */
/**
 * La ciudad tomada del snapshot del vencedor (SPEC-CITY-TAKEN): la de id
 * `tomadaId`, suya, en el centro de la caída, con su nombre y su era.
 */
export function ciudadTomada(snapshot, vencedorId, tomadaId, caida) {
  return (snapshot.cities ?? []).find(
    (c) => c.id === tomadaId && c.ownerPlayerId === vencedorId && c.centerX === caida.centerX &&
      c.centerY === caida.centerY && c.name === caida.name && c.era === caida.era,
  );
}

export function rasgo(catalogo, codigo, clave) {
  const c = catalogo.find((x) => x.code === codigo);
  if (!c) throw new Error(`civilización desconocida: ${codigo}`);
  return c.traits[clave] ?? 1000;
}

/** player.ScaleDuration: divide por el rasgo, redondeando al más cercano, mínimo 1. */
export function escalarDuracion(base, porMil) {
  return Math.max(1, Math.floor((base * 1000 + Math.floor(porMil / 2)) / porMil));
}

/**
 * Los tMs que el servidor tiene que poner en una polilínea con `msPorTile`: la
 * misma aritmética entera que movement.StepDurationMs, paso a paso.
 */
export function tiemposDePolilinea(path, terreno, msPorTile) {
  const out = [0];
  let acc = 0;
  for (let i = 1; i < path.length; i++) {
    const coste = COSTE_TERRENO.get(terreno.get(`${path[i].x},${path[i].y}`));
    if (coste === undefined) return null; // fuera del área conocida o intransitable
    let ms = Math.floor((msPorTile * coste + 5) / 10);
    if (path[i].x !== path[i - 1].x && path[i].y !== path[i - 1].y) {
      ms = Math.floor((ms * 1414214 + 500000) / 1000000);
    }
    acc += Math.max(1, ms);
    out.push(acc);
  }
  return out;
}

/**
 * Comprueba que una polilínea va a la velocidad de la civilización de su dueño;
 * `base` es la del tipo de unidad: 600 ms por tile, 1 200 una caravana.
 */
function comprobarVelocidad(mov, terreno, civilizacion, quien, base = 600) {
  const msPorTile = escalarDuracion(base, rasgo(CATALOGO, civilizacion, 'unit.move_speed'));
  const esperados = tiemposDePolilinea(mov.path, terreno, msPorTile);
  if (!esperados) {
    ok(`velocidad de ${quien}: sin terreno para recalcularla`, 'la ruta sale del área cargada');
    return;
  }
  const reales = mov.path.map((w) => w.tMs);
  if (JSON.stringify(reales) !== JSON.stringify(esperados)) {
    fallar(`la polilínea de ${quien} no va a la velocidad de su civilización (${civilizacion}, ${msPorTile} ms por tile)`,
      `esperados ${JSON.stringify(esperados)}\nrecibidos ${JSON.stringify(reales)}`);
  }
  ok(`la polilínea de ${quien} va a la velocidad de su civilización`, `${civilizacion}: ${msPorTile} ms por tile de hierba`);
}

let pasos = 0;
const log = (...a) => VERBOSE && console.log('   ', ...a);

function ok(mensaje, detalle = '') {
  pasos++;
  console.log(`  ✓ ${mensaje}${detalle ? `  ${detalle}` : ''}`);
}

function fallar(mensaje, detalle) {
  console.error(`\n  ✗ ${mensaje}`);
  if (detalle !== undefined) {
    console.error(`\n${typeof detalle === 'string' ? detalle : JSON.stringify(detalle, null, 2)}\n`);
  }
  process.exit(1);
}

const dormir = (ms) => new Promise((r) => setTimeout(r, ms));

// ─────────────────────────────────────────────────────────────
// HTTP
// ─────────────────────────────────────────────────────────────

async function pedir(ruta, opciones = {}, base = BASE) {
  let res;
  try {
    res = await fetch(base + ruta, { ...opciones, signal: AbortSignal.timeout(15_000) });
  } catch (err) {
    fallar(
      `no se pudo contactar con ${base}${ruta}`,
      `${err.message}\n\n¿Están arrancados el servidor (\`pnpm run server:run\`) y la web (\`pnpm run web:dev\`)?`,
    );
  }
  const texto = await res.text();
  let cuerpo = texto;
  try {
    cuerpo = JSON.parse(texto);
  } catch {
    /* se queda como texto */
  }
  return { status: res.status, cuerpo, cookie: res.headers.get('set-cookie') };
}

// ─────────────────────────────────────────────────────────────
// WebSocket: una sesión con cola de mensajes
// ─────────────────────────────────────────────────────────────

class Sesion {
  constructor() {
    this.ws = null;
    this.recibidos = [];
    this.esperando = [];
    this.cierre = null;
  }

  async abrir() {
    this.ws = new WebSocket(WS_URL);
    this.ws.addEventListener('message', (ev) => {
      let msg;
      try {
        msg = JSON.parse(ev.data);
      } catch {
        return;
      }
      log('<-', msg.type);
      // Si alguien esperaba este tipo, el mensaje es suyo y no se encola: si se
      // quedara en la cola, una espera posterior del mismo tipo lo recibiría
      // otra vez (p. ej. un world.snapshot viejo tras un session.view nuevo).
      const espera = this.esperando.find((e) => e.tipos.includes(msg.type));
      if (espera) {
        this.esperando = this.esperando.filter((e) => e !== espera);
        espera.resolver(msg);
        return;
      }
      this.recibidos.push(msg);
    });
    this.ws.addEventListener('close', (ev) => {
      this.cierre = { code: ev.code, reason: ev.reason };
      log('cerrado', ev.code, ev.reason);
    });

    await new Promise((resolver, rechazar) => {
      const tOut = setTimeout(() => rechazar(new Error('timeout abriendo el WebSocket')), 15_000);
      this.ws.addEventListener('open', () => {
        clearTimeout(tOut);
        resolver();
      });
      this.ws.addEventListener('error', () => {
        clearTimeout(tOut);
        rechazar(new Error(`no se pudo abrir ${WS_URL}`));
      });
    }).catch((err) => fallar(err.message));
  }

  enviar(type, payload) {
    const msg = { v: PROTOCOL_VERSION, type, requestId: crypto.randomUUID(), payload };
    log('->', type);
    this.ws.send(JSON.stringify(msg));
    return msg.requestId;
  }

  /** Espera el primer mensaje de esos tipos que cumpla `cumple`, descartando los demás. */
  async esperarQue(tipos, cumple, msTimeout = 15_000) {
    const limite = Date.now() + msTimeout;
    for (;;) {
      const msg = await this.esperar(tipos, Math.max(1, limite - Date.now()));
      if (cumple(msg)) return msg;
    }
  }

  /** Espera el primer mensaje de alguno de esos tipos, incluidos los ya recibidos. */
  esperar(tipos, msTimeout = 15_000) {
    const lista = Array.isArray(tipos) ? tipos : [tipos];
    const yaEsta = this.recibidos.findIndex((m) => lista.includes(m.type));
    if (yaEsta >= 0) return Promise.resolve(this.recibidos.splice(yaEsta, 1)[0]);

    return new Promise((resolver) => {
      const entrada = { tipos: lista, resolver };
      this.esperando.push(entrada);
      setTimeout(() => {
        if (!this.esperando.includes(entrada)) return;
        this.esperando = this.esperando.filter((e) => e !== entrada);
        const vistos = this.recibidos.map((m) => m.type).join(', ') || '(ninguno)';
        fallar(
          `se esperaba ${lista.join(' o ')} y no llegó en ${msTimeout} ms`,
          `mensajes recibidos: ${vistos}` +
            (this.cierre ? `\ncierre: ${this.cierre.code} ${this.cierre.reason}` : ''),
        );
      }, msTimeout);
    });
  }

  cerrar() {
    if (this.ws && this.ws.readyState === WebSocket.OPEN) this.ws.close(1000, 'smoke');
  }
}

// ─────────────────────────────────────────────────────────────
// Terreno
// ─────────────────────────────────────────────────────────────

/** Construye un índice "x,y" -> byte de terreno a partir de los chunks del snapshot. */
export function indexarTerreno(chunks) {
  const mapa = new Map();
  for (const ch of chunks) {
    const bytes = Buffer.from(ch.terrain, 'base64');
    // Orden por filas: out[fila * size + columna], fila = y local (world.go).
    for (let i = 0; i < bytes.length; i++) {
      const lx = i % ch.size;
      const ly = Math.floor(i / ch.size);
      mapa.set(`${ch.cx * ch.size + lx},${ch.cy * ch.size + ly}`, bytes[i]);
    }
  }
  return mapa;
}

/**
 * Elige un destino transitable a media distancia. No vale cualquiera: tiene que
 * estar lo bastante lejos para que el movimiento dure varios segundos (si
 * terminara al instante, el paso de la desconexión no probaría nada) y lo
 * bastante cerca para no salirse del área de interés.
 */
export function elegirDestino(terreno, desde) {
  const candidatos = [];
  for (const [clave, tipo] of terreno) {
    if (NO_TRANSITABLE.has(tipo)) continue;
    const [x, y] = clave.split(',').map(Number);
    const d = Math.max(Math.abs(x - desde.x), Math.abs(y - desde.y));
    if (d >= 6 && d <= 12) candidatos.push({ x, y, d });
  }
  if (candidatos.length === 0) return null;
  // Determinista: el más lejano y, a igualdad, el de menor (y, x).
  candidatos.sort((a, b) => b.d - a.d || a.y - b.y || a.x - b.x);
  return candidatos[0];
}

/**
 * Elige el bosque al que mandar a recolectar: el tile FOREST más cercano al
 * centro de la ciudad, fuera de su zona urbana. Cuanto más cerca, más corto el
 * viaje que el humo tiene que esperar. Determinista: a igual distancia, el de
 * menor (y, x).
 */
export function elegirBosque(terreno, ciudad, bosqueAMedias = []) {
  // Un tile sin madera no se recolecta (SPEC-ECON-FOREST, RN-ECON-014).
  const agotados = new Set(bosqueAMedias.filter((f) => f.remaining === 0).map((f) => `${f.x},${f.y}`));
  let mejor = null;
  for (const [clave, tipo] of terreno) {
    if (tipo !== FOREST || agotados.has(clave)) continue;
    const [x, y] = clave.split(',').map(Number);
    const d = Math.max(Math.abs(x - ciudad.centerX), Math.abs(y - ciudad.centerY));
    if (d <= 1) continue; // la zona urbana es muralla
    if (!mejor || d < mejor.d || (d === mejor.d && (y < mejor.y || (y === mejor.y && x < mejor.x)))) {
      mejor = { x, y, d };
    }
  }
  return mejor;
}

/**
 * La colina más cercana al centro de la ciudad (Chebyshev), sin contar las
 * agotadas. Determinista: a igual distancia, la de menor (y, x).
 */
export function elegirColina(terreno, ciudad, colinaAMedias = []) {
  const agotadas = new Set(colinaAMedias.filter((h) => h.remaining === 0).map((h) => `${h.x},${h.y}`));
  let mejor = null;
  for (const [clave, tipo] of terreno) {
    if (tipo !== HILL || agotadas.has(clave)) continue;
    const [x, y] = clave.split(',').map(Number);
    const d = Math.max(Math.abs(x - ciudad.centerX), Math.abs(y - ciudad.centerY));
    if (!mejor || d < mejor.d || (d === mejor.d && (y < mejor.y || (y === mejor.y && x < mejor.x)))) {
      mejor = { x, y, d };
    }
  }
  return mejor;
}

// ─────────────────────────────────────────────────────────────
// Credenciales
// ─────────────────────────────────────────────────────────────

const USUARIO = process.env.EO_SMOKE_USER || 'smoke_runner';
const CLAVE = process.env.EO_SMOKE_PASSWORD || 'smoke-password-1234';
/** La civilización con que se da de alta una cuenta de humo nueva (SPEC-CIVILIZATION). */
const CIVILIZACION_DEL_ALTA = process.env.EO_SMOKE_CIVILIZATION || 'ROMAN';
/** La cuenta nórdica de --civilizacion. */
const NORDICA = 'smoke_norse';

/** Sólo `nombre=valor` de un Set-Cookie: lo que el navegador devolvería. */
export function cookieDeSesion(setCookie) {
  return setCookie ? setCookie.split(';')[0].trim() : null;
}

/**
 * Da de alta al jugador de humo o entra con el que ya existe. Devuelve el cuerpo
 * (con el primer ticket) y la cookie de sesión con la que pedir los siguientes.
 */
async function abrirSesion(usuario = USUARIO, civilizacion = CIVILIZACION_DEL_ALTA) {
  // El login ignora la civilización; el alta la exige (RN-CIV-001).
  const cuerpo = JSON.stringify({ username: usuario, password: CLAVE, civilization: civilizacion });
  const cabeceras = { 'content-type': 'application/json' };

  const alta = await pedir('/api/auth/register', { method: 'POST', headers: cabeceras, body: cuerpo }, WEB);
  if (alta.status === 429) {
    fallar(
      'la web está limitando por tasa las peticiones de esta máquina',
      'Cada ejecución gasta 1 o 2 peticiones de alta o login, y el límite por defecto son 10 por\n' +
        'minuto (EO_AUTH_RATE_LIMIT_PER_MINUTE). Espera un minuto y reintenta.\n\n' +
        'NO es un fallo del despliegue: el límite existe porque cada intento calcula scrypt.',
    );
  }
  if (alta.status === 201 || alta.status === 200) {
    ok('alta del jugador de humo', `${usuario}`);
    return { cuenta: alta.cuerpo, cookie: cookieDeSesion(alta.cookie) };
  }
  if (alta.status !== 409 || alta.cuerpo?.code !== 'USERNAME_TAKEN') {
    fallar(`el alta falló con ${alta.status}`, alta.cuerpo);
  }

  const login = await pedir('/api/auth/login', { method: 'POST', headers: cabeceras, body: cuerpo }, WEB);
  if (login.status !== 200) fallar(`el login falló con ${login.status}`, login.cuerpo);
  ok('login del jugador de humo', `${usuario} (ya existía)`);
  return { cuenta: login.cuerpo, cookie: cookieDeSesion(login.cookie) };
}

/** Ticket nuevo con la cookie de sesión: lo que hace el cliente en cada reconexión. */
async function ticketConCookie(cookie) {
  const r = await pedir('/api/auth/ticket', { method: 'POST', headers: { cookie } }, WEB);
  if (r.status !== 200 || !r.cuerpo?.ticket) fallar(`la sesión web no dio un ticket nuevo (${r.status})`, r.cuerpo);
  return r.cuerpo;
}

/** Abre una sesión completa: hello, welcome y el snapshot inicial. */
async function conectar(ticket) {
  const s = new Sesion();
  await s.abrir();
  s.enviar('session.hello', { ticket, clientVersion: 'smoke' });

  const bienvenida = await s.esperar(['session.welcome', 'system.error']);
  if (bienvenida.type === 'system.error') {
    fallar('el handshake fue rechazado', bienvenida.payload);
  }
  const snapshot = await s.esperar(['world.snapshot', 'system.error']);
  if (snapshot.type === 'system.error') {
    fallar('no llegó el snapshot inicial', snapshot.payload);
  }
  return { sesion: s, bienvenida, snapshot };
}

// ─────────────────────────────────────────────────────────────
// El recorrido
// ─────────────────────────────────────────────────────────────

async function main() {
  console.log(`\nComprobación de humo contra ${BASE} (web: ${WEB})\n`);

  // 1. Salud ────────────────────────────────────────────────
  const salud = await pedir('/health');
  if (salud.status !== 200) fallar(`/health devolvió ${salud.status}`, salud.cuerpo);
  ok('/health responde');

  const listo = await pedir('/ready');
  if (listo.status !== 200) {
    fallar(
      `/ready devolvió ${listo.status}: el servidor está vivo pero no operativo`,
      listo.cuerpo,
    );
  }
  const checks = listo.cuerpo?.checks ?? {};
  ok('/ready responde', Object.entries(checks).map(([k, v]) => `${k}=${v}`).join(' '));

  // 2. Identidad ────────────────────────────────────────────
  const { cuenta, cookie } = await abrirSesion();
  if (!cuenta.ticket) fallar('la respuesta no trae ticket', cuenta);
  if (!cookie?.startsWith('eo_session=')) fallar('la web no abrió una sesión (falta la cookie eo_session)', cuenta);
  ok('sesión web abierta', 'cookie eo_session');

  // 3-4. Sesión y snapshot ──────────────────────────────────
  const primera = await conectar(cuenta.ticket);
  ok('handshake aceptado', `tick=${primera.bienvenida.payload.tickDurationMs}ms`);

  const mundo = primera.bienvenida.payload.world;
  const snap = primera.snapshot.payload;
  ok(
    'snapshot recibido',
    `mundo ${mundo.width}x${mundo.height} · ${snap.chunks.length} chunks · ` +
      `${snap.units.length} unidades · ${snap.cities.length} ciudades`,
  );

  const ciudad = snap.cities.find((c) => c.ownerPlayerId === cuenta.playerId);
  if (!ciudad) fallar('el snapshot no trae la ciudad del jugador: la fundación no llegó al mundo', snap.cities);
  ok('ciudad propia en el snapshot', `"${ciudad.name}" en (${ciudad.centerX}, ${ciudad.centerY})`);
  if (!CATALOGO.some((c) => c.code === ciudad.civilization)) {
    fallar('la ciudad no lleva una civilización del catálogo (SPEC-CIVILIZATION RN-CIV-009)', ciudad);
  }
  ok('la ciudad lleva su civilización', ciudad.civilization);

  const propias = snap.units.filter((u) => u.playerId === cuenta.playerId);
  if (propias.length === 0) fallar('el snapshot no trae ninguna unidad propia', snap.units);
  ok('unidades propias en el snapshot', `${propias.length}`);

  // 5. Movimiento ───────────────────────────────────────────
  // Un aldeano: el mismo recorre después el viaje de recolección, y un guerrero
  // (de una incursión anterior) no recolecta.
  const aldeanos = propias.filter((u) => u.unitType === 'VILLAGER');
  if (aldeanos.length === 0) fallar('el jugador de humo no tiene aldeanos', propias);
  const unidad = aldeanos.find((u) => !u.movement) ?? aldeanos[0];
  const terreno = indexarTerreno(snap.terrain);
  const destino = elegirDestino(terreno, unidad);
  if (!destino) fallar('no encontré ningún tile transitable a distancia media', { desde: unidad });

  primera.sesion.enviar('unit.move', { unitId: unidad.id, target: { x: destino.x, y: destino.y } });

  const respuesta = await primera.sesion.esperar([
    'unit.move.accepted',
    'unit.move.rejected',
    'system.error',
  ]);
  if (respuesta.type !== 'unit.move.accepted') {
    fallar('el servidor rechazó la orden de movimiento', respuesta.payload);
  }

  const iniciado = await primera.sesion.esperar('unit.movement.started');
  const mov = iniciado.payload.movement;
  const duracionMs = mov.arrivalTimeMs - mov.startTimeMs;
  if (!Array.isArray(mov.path) || mov.path.length < 2) {
    fallar('la polilínea del movimiento es demasiado corta', mov);
  }
  ok(
    'movimiento aceptado y calculado',
    `unidad ${unidad.id}: (${unidad.x},${unidad.y}) -> (${destino.x},${destino.y}) · ` +
      `${mov.path.length} tiles · ${duracionMs} ms`,
  );

  comprobarVelocidad(mov, terreno, ciudad.civilization, USUARIO);

  if (duracionMs < 2000) {
    fallar(
      `el movimiento dura sólo ${duracionMs} ms: demasiado poco para probar la desconexión`,
      'Elige un mundo o un destino que den un trayecto más largo.',
    );
  }

  // 6-7. Desconexión y espera ───────────────────────────────
  primera.sesion.cerrar();
  ok('conexión cerrada a mitad de movimiento');

  const margenMs = 2500;
  const esperaMs = mov.arrivalTimeMs - Date.now() + margenMs;
  console.log(
    `    esperando ${Math.max(0, Math.round(esperaMs / 1000))} s SIN cliente conectado ` +
      `(llegada prevista en ${new Date(mov.arrivalTimeMs).toISOString().slice(11, 23)})`,
  );
  await dormir(Math.max(0, esperaMs));

  // 8. Reconexión ───────────────────────────────────────────
  // Ticket nuevo: el `jti` del anterior se consumió en el primer handshake.
  const cuenta2 = await ticketConCookie(cookie);
  const segunda = await conectar(cuenta2.ticket);
  ok('reconexión aceptada con un ticket pedido con la cookie');

  const tras = segunda.snapshot.payload.units.find((u) => u.id === unidad.id);
  if (!tras) fallar('la unidad no aparece en el snapshot tras reconectar', segunda.snapshot.payload.units);

  if (tras.x !== destino.x || tras.y !== destino.y) {
    fallar(
      'la unidad NO llegó al destino mientras el cliente estaba desconectado',
      `esperado (${destino.x}, ${destino.y})\nobtenido (${tras.x}, ${tras.y})\n` +
        `movimiento activo: ${JSON.stringify(tras.movement)}\n\n` +
        'Esto es exactamente lo que el producto promete que NO pasa: el mundo\n' +
        'debe seguir simulando aunque nadie esté conectado.',
    );
  }
  if (tras.movement !== null) {
    fallar('la unidad llegó pero sigue con un movimiento activo', tras.movement);
  }
  ok('la unidad llegó al destino con el cliente desconectado', `(${tras.x}, ${tras.y})`);

  // 9. Recolección ─────────────────────────────────────────
  // Un viaje completo con el dueño conectado: el tramo al bosque, el trabajo,
  // la vuelta y la madera en la ciudad. Los tiempos y la cantidad los decide el
  // servidor; el humo sólo comprueba que el stock sube.
  const snap2 = segunda.snapshot.payload;
  const ciudad2 = snap2.cities.find((c) => c.id === ciudad.id) ?? ciudad;
  const bosque = elegirBosque(indexarTerreno(snap2.terrain), ciudad2, snap2.forests);
  if (!bosque) fallar('no hay ningún bosque en el área de interés de la ciudad para probar la recolección', ciudad2);
  const maderaInicial = ciudad2.stocks?.WOOD ?? 0;
  segunda.sesion.enviar('unit.gather', { unitId: unidad.id, target: { x: bosque.x, y: bosque.y } });
  const ida = await segunda.sesion.esperarQue(['unit.movement.started', 'system.error'],
    (m) => m.type === 'system.error' || m.payload.unitId === unidad.id);
  if (ida.type === 'system.error') fallar('el servidor rechazó la orden de recolección', ida.payload);
  const llegadaMs = ida.payload.movement.arrivalTimeMs - Date.now();
  ok('recolección aceptada', `unidad ${unidad.id} -> bosque (${bosque.x}, ${bosque.y}) · ida de ${ida.payload.movement.path.length} tiles`);

  await segunda.sesion.esperarQue('entity.update',
    (m) => m.payload.id === unidad.id && m.payload.status === 'WORKING', Math.max(0, llegadaMs) + 5_000);
  ok('la unidad trabaja en el bosque', 'WORKING');

  // El trabajo (10 s en el catálogo) y una vuelta como la ida, con margen.
  const vueltaMs = ida.payload.movement.arrivalTimeMs - ida.payload.movement.startTimeMs;
  const deposito = await segunda.sesion.esperarQue('city.update',
    (m) => m.payload.id === ciudad.id && m.payload.stocks && m.payload.stocks.WOOD > maderaInicial,
    vueltaMs + 30_000);
  ok('viaje de recolección completo', `madera ${maderaInicial} -> ${deposito.payload.stocks.WOOD}`);

  if (CIVILIZACION) {
    await civilizaciones(segunda, ciudad2);
  }

  if (TALA) {
    await tala(segunda, cuenta.playerId, snap2, bosque);
  }

  if (PIEDRA) {
    await piedra(segunda, ciudad2, cuenta.playerId, snap2, unidad.id);
  }

  if (AVANCE) {
    await avance(segunda, ciudad);
  }

  if (INCURSION) {
    await incursion(segunda, ciudad, cuenta.playerId, snap2);
  }

  if (SAQUEO) {
    await saqueo(segunda, ciudad, cuenta.playerId, snap2);
  }

  if (ASEDIO) {
    await asedio(segunda, ciudad, cuenta.playerId, snap2);
  }

  if (ARIETE) {
    await ariete(segunda, ciudad, cuenta.playerId, snap2);
  }

  if (TERRITORIO) {
    await territorio(segunda, ciudad, cuenta.playerId);
  }

  if (CASTILLO) {
    await castillo(segunda, ciudad, cuenta.playerId);
  }

  if (DIPLOMACIA) {
    await diplomacia(segunda, cuenta.playerId, snap2);
  }

  if (COMERCIO) {
    await comercio(segunda, ciudad);
  }

  if (BIENES) {
    await bienes(segunda, ciudad);
  }

  if (UNIDAD_CIVILIZACION) {
    await unidadCivilizacion(segunda, ciudad);
  }

  if (TECNOLOGIA) {
    await tecnologia(segunda, ciudad);
  }

  if (MERCADO) {
    await mercado(segunda, ciudad);
  }

  if (CARAVANA) {
    await caravana(segunda, ciudad, cuenta.playerId);
  }

  if (CAIDA) {
    await caida(segunda, ciudad, cuenta.playerId, snap2);
  }

  if (MULTICIUDAD) {
    await multiciudad();
  }

  if (REGISTRO) {
    await registro(segunda, cuenta.playerId);
  }

  if (MENSAJES) {
    await mensajes(segunda, cuenta.playerId, ciudad);
  }

  if (FACCION) {
    await faccion();
  }

  if (TERRITORIO_CLAN) {
    await territorioDeClan();
  }

  if (CLAN) {
    await clanes();
  }

  if (ALIANZA_CLAN) {
    await alianzaDeClan();
  }

  if (GUERRA_CLAN) {
    await guerraDeClan();
  }

  segunda.sesion.cerrar();
  await dormir(200);

  console.log(`\n${pasos} comprobaciones superadas. El vertical slice funciona en este despliegue.\n`);
  process.exit(0);
}

// ─────────────────────────────────────────────────────────────
// Civilizaciones (SPEC-CIVILIZATION), sólo con --civilizacion
// ─────────────────────────────────────────────────────────────

/**
 * El alta sin civilización se rechaza sin crear cuenta; una cuenta nórdica
 * (smoke_norse, que se da de alta la primera vez) ve su ciudad nórdica y mueve
 * un aldeano a 545 ms por tile; y una casa de la ciudad del humo tiene el plazo
 * de su civilización (13 636 ms la romana).
 */
async function civilizaciones(propia, ciudad) {
  const cabeceras = { 'content-type': 'application/json' };
  for (const civilization of [undefined, 'AZTEC']) {
    const r = await pedir('/api/auth/register', {
      method: 'POST', headers: cabeceras,
      body: JSON.stringify({ username: 'smoke_sin_civ', password: CLAVE, civilization }),
    }, WEB);
    if (r.status !== 400 || r.cuerpo?.code !== 'INVALID_MESSAGE') {
      fallar(`un alta con civilización ${civilization ?? '(ninguna)'} no se rechazó con 400 INVALID_MESSAGE`, r);
    }
  }
  ok('el alta sin una civilización del catálogo se rechaza', '400 INVALID_MESSAGE');

  const { cuenta } = await abrirSesion(NORDICA, 'NORSE');
  const nordica = await conectar(cuenta.ticket);
  const vista = nordica.snapshot.payload;
  const suya = vista.cities.find((c) => c.ownerPlayerId === cuenta.playerId);
  if (!suya) fallar(`${NORDICA} no tiene ciudad en su snapshot`, vista.cities);
  if (suya.civilization !== 'NORSE') fallar(`la ciudad de ${NORDICA} no es nórdica`, suya);
  ok(`la ciudad de ${NORDICA} es nórdica`, `"${suya.name}" en (${suya.centerX}, ${suya.centerY})`);
  const aldeano = vista.units.find((u) => u.playerId === cuenta.playerId && u.unitType === 'VILLAGER' && !u.movement);
  if (!aldeano) fallar(`${NORDICA} no tiene un aldeano parado`, vista.units);
  const terreno = indexarTerreno(vista.terrain);
  const destino = elegirDestino(terreno, aldeano);
  if (!destino) fallar(`no hay un destino transitable cerca del aldeano de ${NORDICA}`, aldeano);
  nordica.sesion.enviar('unit.move', { unitId: aldeano.id, target: { x: destino.x, y: destino.y } });
  const iniciado = await nordica.sesion.esperarQue(['unit.movement.started', 'unit.move.rejected', 'system.error'],
    (m) => m.type !== 'unit.movement.started' || m.payload.unitId === aldeano.id);
  if (iniciado.type !== 'unit.movement.started') fallar(`${NORDICA} no pudo mover su aldeano`, iniciado.payload);
  comprobarVelocidad(iniciado.payload.movement, terreno, 'NORSE', NORDICA);
  nordica.sesion.cerrar();

  // Una casa en la ciudad del humo: su plazo es el de su civilización. Se mide
  // contra el ts del building.spawn, que sale un tick o dos después de la orden.
  const edificios = propia.snapshot.payload.buildings.filter((b) => b.cityId === ciudad.id);
  const hueco = huecoParaCasa(ciudad, edificios);
  const madera = ciudad.stocks?.WOOD ?? 0;
  if (!hueco || madera < 30) {
    ok('plazo de la casa: se omite', hueco ? `sólo hay ${madera} de madera` : 'no queda hueco junto al centro');
    return;
  }
  propia.sesion.enviar('city.build', { cityId: ciudad.id, type: 'HOUSE', x: hueco.x, y: hueco.y });
  const spawn = await propia.sesion.esperarQue(['building.spawn', 'system.error'],
    (m) => m.type === 'system.error' ||
      (m.payload.building.cityId === ciudad.id && m.payload.building.x === hueco.x && m.payload.building.y === hueco.y),
    15_000);
  if (spawn.type === 'system.error') fallar('no se pudo encargar la casa', spawn.payload);
  const plazo = escalarDuracion(15_000, rasgo(CATALOGO, ciudad.civilization, 'production.speed'));
  const queda = spawn.payload.building.completesAtMs - spawn.ts;
  if (queda > plazo || queda < plazo - 2_000) {
    fallar(`la casa no tiene el plazo de su civilización (${ciudad.civilization}: ${plazo} ms)`,
      `completesAtMs - ts = ${queda} ms`);
  }
  ok('la casa tiene el plazo de su civilización', `${ciudad.civilization}: ${plazo} ms (quedaban ${queda} al anunciarse)`);
}

// ─────────────────────────────────────────────────────────────
// Bienes de civilización (SPEC-CIVILIZATION-GOODS), sólo con --bienes
// ─────────────────────────────────────────────────────────────

/** El bien de una civilización del catálogo de presentación. */
export function bienDe(catalogo, codigo) {
  return catalogo.find((c) => c.code === codigo)?.good ?? null;
}

/** Comprueba un plazo anunciado contra el de la civilización, con margen por el tick. */
function comprobarPlazo(que, completesAtMs, ts, plazo) {
  const queda = completesAtMs - ts;
  if (queda > plazo || queda < plazo - 2_000) {
    fallar(`${que} no tiene el plazo de su civilización (${plazo} ms)`, `completesAtMs - ts = ${queda} ms`);
  }
  return queda;
}

async function bienes(propio, ciudad) {
  propio.sesion.enviar('session.view', { center: { x: ciudad.centerX, y: ciudad.centerY } });
  const vista = (await propio.sesion.esperar('world.snapshot')).payload;
  const c = vista.cities.find((x) => x.id === ciudad.id) ?? ciudad;
  if (c.era === 'STONE_AGE') {
    ok('bienes: se omite', 'la ciudad del humo sigue en la Edad de Piedra y el taller pide Bronce (--avance)');
    return;
  }
  const bien = bienDe(CATALOGO, c.civilization);
  const velocidad = rasgo(CATALOGO, c.civilization, 'production.speed');
  let stocks = { ...c.stocks };

  // El taller: el que haya, o uno nuevo con su plazo (RN-GOODS-003).
  const edificios = vista.buildings.filter((b) => b.cityId === c.id);
  let taller = edificios.find((b) => b.type === 'WORKSHOP');
  if (!taller) {
    if ((stocks.WOOD ?? 0) < 130 || (stocks.STONE ?? 0) < 65) {
      fallar('la ciudad del humo no tiene para el taller y una elaboración (130 de madera y 65 de piedra)',
        'con el servidor parado: EO_DEVSEED_STOCK_FOR=smoke_runner pnpm run dev:seed');
    }
    const hueco = huecoParaCasa(c, edificios);
    if (!hueco) fallar('no queda hueco para el taller junto al centro', edificios);
    propio.sesion.enviar('city.build', { cityId: c.id, type: 'WORKSHOP', x: hueco.x, y: hueco.y });
    const spawn = await propio.sesion.esperarQue(['building.spawn', 'system.error'],
      (m) => m.type === 'system.error' || (m.payload.building.cityId === c.id && m.payload.building.type === 'WORKSHOP'), 15_000);
    if (spawn.type === 'system.error') fallar('no se pudo encargar el taller', spawn.payload);
    const plazo = escalarDuracion(45_000, velocidad);
    const queda = comprobarPlazo('el taller', spawn.payload.building.completesAtMs, spawn.ts, plazo);
    ok('taller encargado con el plazo de su civilización', `${c.civilization}: ${plazo} ms (quedaban ${queda})`);
    taller = spawn.payload.building;
  }
  if (taller.status !== 'COMPLETE') {
    await propio.sesion.esperarQue('building.update', (m) => m.payload.id === taller.id && m.payload.status === 'COMPLETE', 60_000);
  }
  ok('taller completo', `(${taller.x}, ${taller.y})`);

  // La elaboración: 5 del bien de la civilización, con su plazo (RN-GOODS-002, RN-GOODS-004).
  if (c.craft) {
    await propio.sesion.esperarQue('city.update', (m) => m.payload.id === c.id && m.payload.craft === null, 45_000);
  }
  propio.sesion.enviar('city.craft', { cityId: c.id });
  const encargo = await propio.sesion.esperarQue(['city.update', 'system.error'],
    (m) => m.type === 'system.error' || (m.payload.id === c.id && m.payload.craft), 15_000);
  if (encargo.type === 'system.error') fallar('no se pudo elaborar', encargo.payload);
  const cr = encargo.payload.craft;
  if (cr.good !== bien || cr.amount !== 5) fallar(`el taller no elabora 5 de ${bien}`, cr);
  const plazo = escalarDuracion(30_000, velocidad);
  const queda = comprobarPlazo('la elaboración', cr.completesAtMs, encargo.ts, plazo);
  const antes = encargo.payload.stocks?.[bien] ?? stocks[bien] ?? 0;
  ok('elaboración encargada', `5 de ${bien} en ${plazo} ms (${c.civilization}; quedaban ${queda})`);
  const fin = await propio.sesion.esperarQue('city.update',
    (m) => m.payload.id === c.id && m.payload.craft === null, plazo + 10_000);
  if (fin.payload.stocks?.[bien] !== antes + 5) fallar(`la entrega no sumó 5 de ${bien}`, fin.payload);
  stocks = { ...stocks, ...fin.payload.stocks };
  ok('elaboración entregada', `${bien}: ${antes} -> ${antes + 5}`);

  // La Edad de Hierro: sin bienes ajenos, rechazada (RN-GOODS-006).
  const ajenos = CATALOGO.map((x) => x.good).filter((g) => g !== bien).reduce((n, g) => n + (stocks[g] ?? 0), 0);
  if (c.era === 'BRONZE_AGE' && ajenos < 60 && !c.advance) {
    const id = propio.sesion.enviar('city.advance', { cityId: c.id });
    const r = await propio.sesion.esperarQue('system.error', (m) => m.requestId === id, 10_000);
    if (r.payload.code !== 'INSUFFICIENT_RESOURCES') fallar('el avance a Hierro sin bienes ajenos no se rechazó', r.payload);
    ok('Hierro sin bienes de otras civilizaciones se rechaza', `${ajenos}/60: ${r.payload.message}`);
  }

  // Un trato con el bien (RN-GOODS-008), si hay tratado vigente con la víctima.
  const { cuenta } = await abrirSesion(VICTIMA);
  const victima = await conectar(cuenta.ticket);
  const ciudadV = victima.snapshot.payload.cities.find((x) => x.ownerPlayerId === cuenta.playerId);
  const vigente = (victima.snapshot.payload.treaties ?? []).find((t) => t.otherCityId === c.id && t.status === 'ACTIVE');
  const pendiente = (victima.snapshot.payload.tradeOffers ?? []).find((o) => o.otherCityId === c.id && o.status === 'PROPOSED');
  if (!ciudadV || !vigente || pendiente || (ciudadV.stocks?.WOOD ?? 0) < 1) {
    ok('trato con bienes: se omite', 'sin tratado vigente con smoke_victim, con una oferta pendiente o sin madera (--comercio)');
    victima.sesion.cerrar();
    return;
  }
  propio.sesion.enviar('trade.propose', {
    playerId: cuenta.playerId, give: [{ resource: bien, amount: 5 }], want: [{ resource: 'WOOD', amount: 1 }],
  });
  const propia = await oferta(propio.sesion, (x) => x.otherPlayerId === cuenta.playerId && x.status === 'PROPOSED');
  victima.sesion.enviar('trade.respond', { offerId: propia.id, accept: true });
  await oferta(victima.sesion, (x) => x.id === propia.id && x.status === 'ACCEPTED');
  const tenia = ciudadV.stocks?.[bien] ?? 0;
  // Desde E15 el bien viaja en una caravana (RN-CARAVAN-001).
  const deBienes = await caravanaDe(victima.sesion, (x) => x.senderPlayerId === c.ownerPlayerId && x.toCityId === ciudadV.id);
  ok('trato con bienes aceptado', `5 de ${bien} en camino hacia ${ciudadV.name}, llegan en ${segundosHasta(deBienes.arrivesAtMs)} s`);
  await llegada(victima.sesion, deBienes, ciudadV.id, bien, tenia + 5);
  victima.sesion.cerrar();
}

// ─────────────────────────────────────────────────────────────
// Unidades de civilización (SPEC-CIVILIZATION-UNITS), sólo con --unidad-civilizacion
// ─────────────────────────────────────────────────────────────

/**
 * La unidad propia de una civilización y una ajena (la de la primera otra del
 * catálogo), con la ficha de presentación de la propia; `null` si la
 * civilización no está en el catálogo.
 */
export function unidadesDelHumo(catalogo, unidades, codigo) {
  const propia = catalogo.find((c) => c.code === codigo)?.unit;
  const ajena = catalogo.find((c) => c.code !== codigo)?.unit;
  const ficha = unidades.find((u) => u.code === propia);
  if (!propia || !ajena || !ficha) return null;
  return { propia, ajena, ficha };
}

/** Las elaboraciones de 5 que faltan para tener `necesita` del bien con `tiene`. */
export function elaboracionesQueFaltan(tiene, necesita, porElaboracion = 5) {
  return tiene >= necesita ? 0 : Math.ceil((necesita - tiene) / porElaboracion);
}

async function unidadCivilizacion(propio, ciudad) {
  propio.sesion.enviar('session.view', { center: { x: ciudad.centerX, y: ciudad.centerY } });
  const vista = (await propio.sesion.esperar('world.snapshot')).payload;
  const c = vista.cities.find((x) => x.id === ciudad.id) ?? ciudad;
  const u = unidadesDelHumo(CATALOGO, UNIDADES, c.civilization);
  if (!u) fallar('la civilización de la ciudad del humo no tiene unidad en el catálogo', c.civilization);
  const entrenar = async (tipo) => {
    const id = propio.sesion.enviar('city.train', { cityId: c.id, type: tipo });
    return propio.sesion.esperarQue(['system.error', 'city.update'],
      (m) => (m.type === 'system.error' && m.requestId === id) ||
        (m.type === 'city.update' && m.payload.id === c.id && m.payload.production?.unitType === tipo), 15_000);
  };

  // La de otra civilización, nunca (INV-UNIT-012).
  const ajena = await entrenar(u.ajena);
  if (ajena.type !== 'system.error' || ajena.payload.code !== 'INVALID_TARGET') {
    fallar(`${c.civilization} pudo encargar ${u.ajena}`, ajena.payload);
  }
  ok('la unidad de otra civilización se rechaza', `${c.civilization} pide ${u.ajena}: ${ajena.payload.message}`);

  if (c.era === 'STONE_AGE') {
    const antes = await entrenar(u.propia);
    if (antes.type !== 'system.error' || antes.payload.code !== 'INVALID_TARGET') {
      fallar(`${u.propia} se encargó en la Edad de Piedra`, antes.payload);
    }
    ok('la unidad propia, antes de Bronce, se rechaza', `${u.propia}: ${antes.payload.message} (el resto pide --avance)`);
    return;
  }

  // El bien: 10 de los suyos; si faltan, los elabora el taller (--bienes).
  const bien = bienDe(CATALOGO, c.civilization);
  let stocks = { ...c.stocks };
  const faltan = elaboracionesQueFaltan(stocks[bien] ?? 0, u.ficha.ownGoodCost);
  if (faltan > 0) {
    const taller = vista.buildings.find((b) => b.cityId === c.id && b.type === 'WORKSHOP' && b.status === 'COMPLETE');
    if (!taller) fallar(`faltan ${u.ficha.ownGoodCost - (stocks[bien] ?? 0)} de ${bien} y no hay taller`, 'pasa antes --bienes');
    if (c.craft) {
      await propio.sesion.esperarQue('city.update', (m) => m.payload.id === c.id && m.payload.craft === null, 60_000);
    }
    for (let i = 0; i < faltan; i++) {
      propio.sesion.enviar('city.craft', { cityId: c.id });
      const fin = await propio.sesion.esperarQue(['city.update', 'system.error'],
        (m) => m.type === 'system.error' || (m.payload.id === c.id && m.payload.craft === null && m.payload.stocks), 60_000);
      if (fin.type === 'system.error') fallar('no se pudo elaborar el bien para la unidad', fin.payload);
      stocks = { ...stocks, ...fin.payload.stocks };
    }
    ok('bien elaborado para la unidad', `${bien}: ${stocks[bien]}`);
  }
  for (const coste of u.ficha.trainCosts) {
    if ((stocks[coste.resource] ?? 0) < coste.amount) {
      fallar(`la ciudad del humo no tiene ${coste.amount} de ${coste.resource} para su unidad`,
        'con el servidor parado: EO_DEVSEED_STOCK_FOR=smoke_runner pnpm run dev:seed');
    }
  }
  if (c.production) {
    await propio.sesion.esperarQue('city.update', (m) => m.payload.id === c.id && m.payload.production === null, 60_000);
  }

  // La propia: tres recursos, el plazo de su civilización y su tipo al nacer.
  const encargo = await entrenar(u.propia);
  if (encargo.type === 'system.error') fallar(`no se pudo entrenar ${u.propia}`, encargo.payload);
  const p = encargo.payload;
  for (const coste of [...u.ficha.trainCosts, { resource: bien, amount: u.ficha.ownGoodCost }]) {
    const esperado = (stocks[coste.resource] ?? 0) - coste.amount;
    if (p.stocks?.[coste.resource] !== esperado) {
      fallar(`el encargo no debitó ${coste.amount} de ${coste.resource}`, { antes: stocks, despues: p.stocks });
    }
  }
  const plazo = escalarDuracion(u.ficha.trainMs, rasgo(CATALOGO, c.civilization, 'production.speed'));
  const queda = comprobarPlazo(`el ${u.propia}`, p.production.completesAtMs, encargo.ts, plazo);
  ok('unidad propia encargada', `${u.propia}: ${u.ficha.trainCosts.map((x) => `${x.amount} ${x.resource}`).join(', ')} y ${u.ficha.ownGoodCost} ${bien}; ${plazo} ms (quedaban ${queda})`);
  const nace = await propio.sesion.esperarQue('entity.spawn',
    (m) => m.payload.unit.unitType === u.propia && m.payload.unit.cityId === c.id, plazo + 15_000);
  if (nace.payload.unit.maxHp !== u.ficha.maxHp) fallar(`${u.propia} nació sin su vida`, nace.payload.unit);
  ok('unidad propia nacida', `${u.propia} ${nace.payload.unit.id} con ${nace.payload.unit.maxHp} de vida`);
}

// ─────────────────────────────────────────────────────────────
// Edad de los Castillos (SPEC-CASTLE-AGE), sólo con --castillo
// ─────────────────────────────────────────────────────────────

/** Las eras de la escalera del canon, en orden (SPEC-CASTLE-AGE §4.1). */
const ERAS = ['STONE_AGE', 'BRONZE_AGE', 'IRON_AGE', 'CASTLE_AGE'];

/** Lo que tarda el castillo sin rasgo (SPEC-CASTLE-AGE Q4). */
const CASTILLO_MS = 180_000;

/** Las eras a las que le falta avanzar a una ciudad hasta Castillos, en orden. */
export function pasosHastaCastillos(era) {
  const i = ERAS.indexOf(era);
  return i < 0 ? [] : ERAS.slice(i + 1);
}

/** El castillo en pie de una ciudad, de una lista de edificios, o `null`. */
export function castilloDe(edificios, cityId) {
  return edificios.find((b) => b.cityId === cityId && b.type === 'CASTLE') ?? null;
}

/**
 * Las unidades propias que atacan y están a `radio` o menos del centro de la ciudad:
 * responderían a un asedio (RN-COMBAT-037) y rematarían al sitiador antes que el
 * castillo. `unidades` es el catálogo de presentación (`attacks`).
 */
export function guardiasCerca(vistas, playerId, centro, unidades, radio = 8) {
  const atacan = new Set(unidades.filter((u) => u.attacks).map((u) => u.code));
  return vistas.filter((u) => u.playerId === playerId && atacan.has(u.unitType) && u.status !== 'GARRISONED' &&
    Math.max(Math.abs(u.x - centro.x), Math.abs(u.y - centro.y)) <= radio);
}

/** Si un building.update dice que el castillo apunta a la unidad. */
export function apuntaA(payload, castilloId, unitId) {
  return payload.id === castilloId && payload.targetUnitId === unitId;
}

async function castillo(propio, ciudadInicial, playerId) {
  const s = propio.sesion;
  const cityId = ciudadInicial.id;
  const mirar = async () => {
    s.enviar('session.view', { center: { x: ciudadInicial.centerX, y: ciudadInicial.centerY } });
    return (await s.esperar('world.snapshot')).payload;
  };
  let vista = await mirar();
  let c = vista.cities.find((x) => x.id === cityId);
  if (!c) fallar('la ciudad del humo no está en su snapshot', vista.cities.map((x) => x.id));

  // Los avances que falten, uno tras otro (cada uno, con su plazo y sin entrenar).
  if (c.advance) {
    await s.esperarQue('city.update', (m) => m.payload.id === cityId && m.payload.era,
      Math.max(0, c.advance.completesAtMs - Date.now()) + 15_000);
    vista = await mirar();
    c = vista.cities.find((x) => x.id === cityId);
  }
  for (const era of pasosHastaCastillos(c.era)) {
    if (c.production) {
      await s.esperarQue('city.update', (m) => m.payload.id === cityId && m.payload.production === null, 60_000);
    }
    s.enviar('city.advance', { cityId });
    const inicio = await s.esperarQue(['city.update', 'system.error'],
      (m) => m.type === 'system.error' || (m.payload.id === cityId && m.payload.advance), 15_000);
    if (inicio.type === 'system.error') {
      fallar(`el servidor rechazó el avance a ${era}`,
        `${inicio.payload.code}: ${inicio.payload.message} (con el servidor parado: EO_DEVSEED_STOCK_FOR=smoke_runner pnpm run dev:seed)`);
    }
    const fin = inicio.payload.advance.completesAtMs;
    ok(`avance a ${era} encargado`, `quedan ${Math.round((fin - Date.now()) / 1000)} s`);
    const nueva = await s.esperarQue('city.update', (m) => m.payload.id === cityId && m.payload.era,
      Math.max(0, fin - Date.now()) + 15_000);
    if (nueva.payload.era !== era) fallar(`el avance terminó en ${nueva.payload.era} y no en ${era}`, nueva.payload);
    ok(eraLabelDelHumo(era), `límite de población ${nueva.payload.populationLimit}`);
    c = { ...c, era, advance: null };
  }
  s.enviar('city.advance', { cityId });
  const otra = await s.esperarQue(['system.error', 'city.update'],
    (m) => m.type === 'system.error' || (m.payload.id === cityId && m.payload.advance), 15_000);
  if (otra.type !== 'system.error' || otra.payload.code !== 'NOT_IMPLEMENTED') {
    fallar('desde Castillos se pudo avanzar otra vez', otra.payload);
  }
  ok('desde la Edad de los Castillos no hay más eras', 'NOT_IMPLEMENTED');

  // El castillo: si no lo tiene, lo encarga en el primer hueco y espera la obra.
  vista = await mirar();
  let fuerte = castilloDe(vista.buildings, cityId);
  if (!fuerte) {
    const hueco = huecoParaCasa(c, vista.buildings.filter((b) => b.cityId === cityId));
    if (!hueco) fallar('no queda hueco en la zona urbana para el castillo', vista.buildings);
    s.enviar('city.build', { cityId, type: 'CASTLE', x: hueco.x, y: hueco.y });
    const alta = await s.esperarQue(['building.spawn', 'system.error'],
      (m) => m.type === 'system.error' || (m.payload.building.cityId === cityId && m.payload.building.type === 'CASTLE'), 15_000);
    if (alta.type === 'system.error') {
      fallar('el servidor rechazó el castillo', `${alta.payload.code}: ${alta.payload.message}`);
    }
    fuerte = alta.payload.building;
    const plazo = escalarDuracion(CASTILLO_MS, rasgo(CATALOGO, c.civilization, 'production.speed'));
    const queda = comprobarPlazo('el castillo', fuerte.completesAtMs, alta.ts, plazo);
    ok('castillo encargado', `en (${fuerte.x}, ${fuerte.y}), ${plazo} ms (quedaban ${queda}), ${fuerte.maxHp} de vida`);
  }
  if (fuerte.status !== 'COMPLETE') {
    await s.esperarQue('building.update', (m) => m.payload.id === fuerte.id && m.payload.status === 'COMPLETE',
      Math.max(0, fuerte.completesAtMs - Date.now()) + 15_000);
    ok('castillo terminado', `${fuerte.id}`);
  }

  // Los guardias propios se apartan: responderían al asedio y rematarían al
  // sitiador antes que el castillo (RN-COMBAT-037). Van al lado contrario por el
  // que llega la víctima.
  vista = await mirar();
  const lejos = { x: c.centerX + (c.centerX >= 20 ? -14 : 14), y: c.centerY };
  for (const g of guardiasCerca(vista.units, playerId, { x: c.centerX, y: c.centerY }, UNIDADES)) {
    await acercar(s, g, lejos, 3);
    ok('un guardia propio se aparta', `${g.unitType} ${g.id} hacia (${lejos.x}, ${lejos.y})`);
  }

  // Un guerrero de la víctima lo asedia: el castillo apunta, dispara y lo mata.
  const { cuenta } = await abrirSesion(VICTIMA);
  const victima = await conectar(cuenta.ticket);
  const ciudadV = victima.snapshot.payload.cities.find((x) => x.ownerPlayerId === cuenta.playerId);
  if (!ciudadV) fallar('la víctima no tiene ciudad en su snapshot', victima.snapshot.payload.cities);
  let guerrero = victima.snapshot.payload.units.find((u) => u.playerId === cuenta.playerId && u.unitType === 'WARRIOR');
  if (!guerrero) {
    await maderaDeLaVictima(victima, cuenta.playerId, ciudadV, { x: c.centerX, y: c.centerY }, 60);
    guerrero = await entrenar(victima.sesion, ciudadV, cuenta.playerId, 'WARRIOR');
  }
  ok('guerrero de la víctima', `unidad ${guerrero.id} en (${guerrero.x}, ${guerrero.y})`);
  await acercar(victima.sesion, guerrero, { x: c.centerX, y: c.centerY + 6 }, 4);
  victima.sesion.enviar('session.view', { center: { x: c.centerX, y: c.centerY } });
  await victima.sesion.esperar('world.snapshot');
  await mirar();

  victima.sesion.enviar('unit.demolish', { unitId: guerrero.id, buildingId: fuerte.id });
  const herido = await victima.sesion.esperarQue(['entity.update', 'system.error'],
    (m) => m.type === 'system.error' || (m.payload.id === guerrero.id && typeof m.payload.hp === 'number' && m.payload.hp < guerrero.maxHp),
    60_000);
  if (herido.type === 'system.error') {
    fallar('el servidor rechazó el asedio del castillo', `${herido.payload.code}: ${herido.payload.message} (¿un pacto de --diplomacia?)`);
  }
  ok('el castillo le dispara', `vida ${guerrero.maxHp} -> ${herido.payload.hp}`);
  const apunta = await s.esperarQue(['building.update'], (m) => apuntaA(m.payload, fuerte.id, guerrero.id), 10_000);
  ok('el castillo apunta al sitiador', `building.update targetUnitId ${apunta.payload.targetUnitId}`);
  const perdida = await avisoEnVivo(victima.sesion, 'UNIT_LOST_TO_CASTLE', playerId, 30_000);
  ok('el sitiador muere con su aviso', `UNIT_LOST_TO_CASTLE ${perdida.payload.unitType} ante ${perdida.payload.otherCityName}`);
  const muerte = await avisoEnVivo(s, 'CASTLE_KILL', cuenta.playerId, 10_000);
  ok('el dueño del castillo recibe su aviso', `CASTLE_KILL ${muerte.payload.unitType} de ${muerte.payload.otherCityName}`);
  victima.sesion.cerrar();
}

/** El nombre de una era en los mensajes del humo. */
function eraLabelDelHumo(era) {
  return { BRONZE_AGE: 'Edad de Bronce', IRON_AGE: 'Edad de Hierro', CASTLE_AGE: 'Edad de los Castillos' }[era] ?? era;
}

// ─────────────────────────────────────────────────────────────
// Árbol tecnológico (SPEC-TECHNOLOGIES), sólo con --tecnologia
// ─────────────────────────────────────────────────────────────

/** La vida de una casa sin rasgo (SPEC-COMBAT-SIEGE §4.2). */
const VIDA_DE_CASA = 150;

/** technology.Compose: la fórmula de cantidad de los rasgos, mínimo 1. */
export function componer(r, e) {
  return Math.max(1, Math.floor((r * e + 500) / 1000));
}

/** player.ScaleAmount: multiplica por el rasgo, redondeando al más cercano, mínimo 1. */
export function escalarCantidad(base, porMil) {
  return Math.max(1, Math.floor((base * porMil + 500) / 1000));
}

/**
 * El rasgo efectivo de un jugador: el de su civilización compuesto con el efecto de
 * cada tecnología que tiene, en orden de código (SPEC-TECHNOLOGIES §4.4).
 */
export function rasgoCompuesto(catalogo, tecnologias, civilizacion, tenidas, clave) {
  let r = rasgo(catalogo, civilizacion, clave);
  for (const code of [...tenidas].sort()) {
    const e = tecnologias.find((t) => t.code === code)?.effects[clave];
    if (e !== undefined) r = componer(r, e);
  }
  return r;
}

/**
 * La tecnología que investiga el humo: una a su alcance (su civilización, su era, sin
 * tenerla y con sus requisitos) que el almacén pueda pagar; Albañilería si lo es, por
 * su efecto fácil de ver; si no, la primera del catálogo. `null` si no hay ninguna.
 */
export function tecnologiaDelHumo(tecnologias, catalogo, ciudad) {
  const tenidas = ciudad.technologies ?? [];
  const propio = bienDe(catalogo, ciudad.civilization);
  const ajenos = Object.entries(ciudad.stocks ?? {})
    .filter(([r]) => !['WOOD', 'STONE', propio].includes(r))
    .reduce((n, [, v]) => n + v, 0);
  const candidatas = tecnologias.filter((t) =>
    (t.civilization === null || t.civilization === ciudad.civilization) &&
    ERAS.indexOf(ciudad.era) >= ERAS.indexOf(t.era) &&
    !tenidas.includes(t.code) &&
    t.requires.every((r) => tenidas.includes(r)) &&
    (t.costs.WOOD ?? 0) <= (ciudad.stocks?.WOOD ?? 0) &&
    (t.costs.STONE ?? 0) <= (ciudad.stocks?.STONE ?? 0) &&
    t.ownGood <= (propio ? ciudad.stocks?.[propio] ?? 0 : 0) &&
    t.foreignGoods <= ajenos);
  return candidatas.find((t) => t.code === 'MASONRY') ?? candidatas[0] ?? null;
}

/**
 * El plazo de una investigación: el del catálogo con production.speed compuesto, o el
 * de EO_TECHNOLOGY_RESEARCH_SECONDS si es positivo (ADR-046 §3).
 */
export function plazoDeInvestigacion(tec, catalogo, tecnologias, civilizacion, tenidas, segundos = 0) {
  if (segundos > 0) return segundos * 1000;
  return escalarDuracion(tec.durationMs, rasgoCompuesto(catalogo, tecnologias, civilizacion, tenidas, 'production.speed'));
}

async function tecnologia(propio, ciudadInicial) {
  const s = propio.sesion;
  const cityId = ciudadInicial.id;
  s.enviar('session.view', { center: { x: ciudadInicial.centerX, y: ciudadInicial.centerY } });
  const vista = (await s.esperar('world.snapshot')).payload;
  const c = vista.cities.find((x) => x.id === cityId);
  if (!c) fallar('la ciudad del humo no está en su snapshot', vista.cities.map((x) => x.id));
  if (!Array.isArray(c.technologies)) fallar('la vista propia no lleva las tecnologías (RN-TECH-008)', c);
  ok('la vista propia lleva sus tecnologías', c.technologies.length > 0 ? c.technologies.join(', ') : 'ninguna');
  if (c.research) {
    await s.esperarQue('city.update', (m) => m.payload.id === cityId && m.payload.technologies,
      Math.max(0, c.research.completesAtMs - Date.now()) + 15_000);
    fallar('la ciudad tenía una investigación a medias: ya terminó; repite la pasada');
  }

  s.enviar('city.research', { cityId, technology: 'POLVORA' });
  const rechazo = await s.esperar('system.error');
  if (rechazo.payload.code !== 'INVALID_TARGET') fallar('una tecnología que no existe no se rechazó', rechazo.payload);
  ok('una tecnología que no existe se rechaza', `${rechazo.payload.code}: ${rechazo.payload.message}`);

  const tec = tecnologiaDelHumo(TECNOLOGIAS, CATALOGO, c);
  if (!tec) {
    ok('no queda ninguna tecnología que pueda pagar', `tiene ${c.technologies.join(', ') || 'ninguna'}; nada más que probar`);
    return;
  }
  const segundos = Number(process.env.EO_TECHNOLOGY_RESEARCH_SECONDS ?? 0);
  s.enviar('city.research', { cityId, technology: tec.code });
  const inicio = await s.esperarQue(['city.update', 'system.error'],
    (m) => m.type === 'system.error' || (m.payload.id === cityId && m.payload.research), 15_000);
  if (inicio.type === 'system.error') {
    fallar(`el servidor rechazó ${tec.name}`, `${inicio.payload.code}: ${inicio.payload.message}`);
  }
  const plazo = plazoDeInvestigacion(tec, CATALOGO, TECNOLOGIAS, c.civilization, c.technologies, segundos);
  const queda = comprobarPlazo(tec.name, inicio.payload.research.completesAtMs, inicio.ts, plazo);
  ok(`${tec.name} encargada`, `${plazo} ms (quedaban ${queda})${segundos > 0 ? ', con EO_TECHNOLOGY_RESEARCH_SECONDS' : ''}`);

  s.enviar('city.research', { cityId, technology: tec.code });
  const ocupada = await s.esperar('system.error');
  if (ocupada.payload.code !== 'CITY_BUSY') fallar('una segunda investigación a la vez no se rechazó', ocupada.payload);
  ok('otra investigación a la vez se rechaza', 'CITY_BUSY');

  const fin = await s.esperarQue('city.update', (m) => m.payload.id === cityId && m.payload.technologies,
    Math.max(0, inicio.payload.research.completesAtMs - Date.now()) + 15_000);
  if (!fin.payload.technologies.includes(tec.code) || fin.payload.research !== null) {
    fallar(`al vencer ${tec.name} no llegó como tecnología`, fin.payload);
  }
  ok(`${tec.name} investigada`, `tecnologías: ${fin.payload.technologies.join(', ')}`);

  if (tec.code !== 'MASONRY') {
    ok('su efecto no se comprueba en el humo', 'sólo el de Albañilería; lo cubren los tests de simulación');
    return;
  }
  const esperada = escalarCantidad(VIDA_DE_CASA,
    rasgoCompuesto(CATALOGO, TECNOLOGIAS, c.civilization, fin.payload.technologies, 'building.house_hp'));
  s.enviar('session.view', { center: { x: ciudadInicial.centerX, y: ciudadInicial.centerY } });
  const despues = (await s.esperar('world.snapshot')).payload;
  let casa = despues.buildings.find((b) => b.cityId === cityId && b.type === 'HOUSE');
  if (!casa) {
    const hueco = huecoParaCasa(c, despues.buildings.filter((b) => b.cityId === cityId));
    if (!hueco) fallar('no queda hueco para la casa que mostraría el efecto', despues.buildings);
    s.enviar('city.build', { cityId, type: 'HOUSE', x: hueco.x, y: hueco.y });
    const alta = await s.esperarQue(['building.spawn', 'system.error'],
      (m) => m.type === 'system.error' || (m.payload.building.cityId === cityId && m.payload.building.type === 'HOUSE'), 15_000);
    if (alta.type === 'system.error') fallar('el servidor rechazó la casa', `${alta.payload.code}: ${alta.payload.message}`);
    casa = alta.payload.building;
  }
  if (casa.maxHp !== esperada) fallar(`con Albañilería, una casa no aguanta ${esperada}`, casa);
  ok('con Albañilería, sus casas aguantan más', `${VIDA_DE_CASA} -> ${casa.maxHp}`);
}

// ─────────────────────────────────────────────────────────────
// Mercado (SPEC-MARKETS), sólo con --mercado
// ─────────────────────────────────────────────────────────────

/** Lo que cuesta y tarda el mercado (SPEC-MARKETS Q3). */
const COSTE_DEL_MERCADO = { WOOD: 150, STONE: 100 };
const PLAZO_DEL_MERCADO = 60_000;

/** La comisión de un lote que da `cantidad`: la décima parte, hacia arriba (RN-MARKET-006). */
export function comisionDelMercado(cantidad) {
  return cantidad > 0 ? Math.ceil(cantidad / 10) : 0;
}

/** Lo que sale del almacén al publicar `lotes` lotes que dan `cantidad`: custodia y comisión. */
export function costeDePublicar(cantidad, lotes) {
  return lotes * (cantidad + comisionDelMercado(cantidad));
}

/** El mercado en pie de una ciudad, o `undefined`. */
export function mercadoDe(edificios, cityId) {
  return edificios.find((b) => b.cityId === cityId && b.type === 'MARKET');
}

/**
 * El mercado de una ciudad, completo: el que haya, o uno nuevo con el plazo de su
 * civilización. Devuelve los edificios de la ciudad tras construirlo.
 */
async function asegurarMercado(sesion, ciudad, edificios, quien) {
  let m = mercadoDe(edificios, ciudad.id);
  if (!m) {
    const stocks = ciudad.stocks ?? {};
    if ((stocks.WOOD ?? 0) < COSTE_DEL_MERCADO.WOOD || (stocks.STONE ?? 0) < COSTE_DEL_MERCADO.STONE) {
      fallar(`${quien} no tiene para el mercado (150 de madera y 100 de piedra)`,
        'con el servidor parado: EO_DEVSEED_STOCK_FOR=smoke_runner,smoke_victim pnpm run dev:seed');
    }
    const hueco = huecoParaCasa(ciudad, edificios.filter((b) => b.cityId === ciudad.id));
    if (!hueco) fallar(`no queda hueco para el mercado de ${quien}`, edificios);
    sesion.enviar('city.build', { cityId: ciudad.id, type: 'MARKET', x: hueco.x, y: hueco.y });
    const spawn = await sesion.esperarQue(['building.spawn', 'system.error'],
      (m2) => m2.type === 'system.error' || (m2.payload.building.cityId === ciudad.id && m2.payload.building.type === 'MARKET'), 15_000);
    if (spawn.type === 'system.error') fallar(`no se pudo encargar el mercado de ${quien}`, spawn.payload);
    const plazo = escalarDuracion(PLAZO_DEL_MERCADO, rasgo(CATALOGO, ciudad.civilization, 'production.speed'));
    const queda = comprobarPlazo('el mercado', spawn.payload.building.completesAtMs, spawn.ts, plazo);
    ok(`mercado de ${quien} encargado`, `${ciudad.civilization}: ${plazo} ms (quedaban ${queda})`);
    m = spawn.payload.building;
  }
  if (m.status !== 'COMPLETE') {
    await sesion.esperarQue('building.update', (u) => u.payload.id === m.id && u.payload.status === 'COMPLETE',
      Math.max(0, m.completesAtMs - Date.now()) + 15_000);
  }
  ok(`mercado de ${quien} completo`, `(${m.x}, ${m.y})`);
}

async function ofertaDelMercado(sesion, cond, ms = 10_000) {
  return (await sesion.esperarQue('market.update', (m) => cond(m.payload.offer), ms)).payload.offer;
}

async function mercado(propio, ciudadInicial) {
  const s = propio.sesion;
  s.enviar('session.view', { center: { x: ciudadInicial.centerX, y: ciudadInicial.centerY } });
  const vista = (await s.esperar('world.snapshot')).payload;
  const c = vista.cities.find((x) => x.id === ciudadInicial.id) ?? ciudadInicial;
  const { cuenta } = await abrirSesion(VICTIMA);
  const victima = await conectar(cuenta.ticket);
  const v = victima.snapshot.payload;
  const cv = v.cities.find((x) => x.ownerPlayerId === cuenta.playerId);
  if (!cv) fallar('la víctima no tiene ciudad en su snapshot', v.cities);
  for (const [quien, ciudad] of [[USUARIO, c], [VICTIMA, cv]]) {
    if (ciudad.era === 'STONE_AGE') {
      fallar(`${quien} sigue en la Edad de Piedra y el mercado pide Bronce`, 'llévala a Bronce (--avance para smoke_runner)');
    }
  }
  await asegurarMercado(s, c, vista.buildings, USUARIO);
  await asegurarMercado(victima.sesion, cv, v.buildings, VICTIMA);

  // Las ofertas abiertas de una pasada anterior: las retira su dueño (Q10: 3 como mucho).
  for (const vieja of propio.snapshot.payload.marketOffers ?? []) {
    s.enviar('market.cancel', { offerId: vieja.id });
    await ofertaDelMercado(s, (o) => o.id === vieja.id && o.status !== 'OPEN');
    ok('oferta de una pasada anterior retirada', `oferta ${vieja.id}`);
  }

  // RN-MARKET-002: dar y pedir lo mismo se rechaza.
  const id0 = s.enviar('market.post', { give: { resource: 'WOOD', amount: 1 }, want: { resource: 'WOOD', amount: 1 }, lots: 1 });
  const r0 = await s.esperarQue('system.error', (m) => m.requestId === id0);
  if (r0.payload.code !== 'INVALID_TARGET') fallar('dar y pedir lo mismo no se rechazó', r0.payload);
  ok('dar y pedir lo mismo se rechaza', `INVALID_TARGET: ${r0.payload.message}`);

  // La publicación: 2 lotes de 10 de madera por 2 de piedra; salen 22 de madera.
  s.enviar('session.view', { center: { x: c.centerX, y: c.centerY } });
  const antes = (await s.esperar('world.snapshot')).payload.cities.find((x) => x.id === c.id);
  const maderaAntes = antes.stocks.WOOD;
  const coste = costeDePublicar(10, 2);
  if (maderaAntes < coste) fallar(`${USUARIO} tiene ${maderaAntes} de madera y publicar cuesta ${coste}`, 'EO_DEVSEED_STOCK_FOR=smoke_runner pnpm run dev:seed');
  s.enviar('market.post', { give: { resource: 'WOOD', amount: 10 }, want: { resource: 'STONE', amount: 2 }, lots: 2 });
  const publicada = await ofertaDelMercado(s, (o) => o.status === 'OPEN' && o.lotsTotal === 2 && o.give.amount === 10);
  await s.esperarQue('city.update', (m) => m.payload.id === c.id && m.payload.stocks && m.payload.stocks.WOOD === maderaAntes - coste);
  ok('oferta publicada con su custodia', `${publicada.id}: madera ${maderaAntes} -> ${maderaAntes - coste} (20 en custodia y 2 de comisión)`);

  // El tablón de la víctima la lleva; el vendedor no se la puede comprar.
  const idL = victima.sesion.enviar('market.list', { receive: 'WOOD' });
  const tablon = await victima.sesion.esperarQue('market.listing', (m) => m.requestId === idL);
  if (!tablon.payload.offers.some((o) => o.id === publicada.id)) fallar('el tablón de la víctima no lleva la oferta', tablon.payload);
  ok('el tablón la lleva', `${tablon.payload.offers.length} ofertas de madera`);
  const idP = s.enviar('market.buy', { offerId: publicada.id, lots: 1 });
  const rP = await s.esperarQue('system.error', (m) => m.requestId === idP);
  if (rP.payload.code !== 'INVALID_TARGET') fallar('el vendedor pudo comprarse su oferta', rP.payload);
  ok('nadie se compra a sí mismo', 'INVALID_TARGET (INV-TRADE-010)');

  // La compra de un lote: la víctima paga 3 de piedra y recibe la caravana de madera.
  // El almacén de la víctima, releído: construir su mercado lo cambió.
  victima.sesion.enviar('session.view', { center: { x: cv.centerX, y: cv.centerY } });
  const ahoraV = (await victima.sesion.esperar('world.snapshot')).payload.cities.find((x) => x.id === cv.id) ?? cv;
  const piedraV = ahoraV.stocks?.STONE ?? 0;
  const maderaV = ahoraV.stocks?.WOOD ?? 0;
  if (piedraV < 3) fallar(`${VICTIMA} tiene ${piedraV} de piedra y el lote cuesta 3`, 'EO_DEVSEED_STOCK_FOR=smoke_victim pnpm run dev:seed');
  victima.sesion.enviar('market.buy', { offerId: publicada.id, lots: 1 });
  const comprada = await ofertaDelMercado(victima.sesion, (o) => o.id === publicada.id && o.lotsLeft === 1, 15_000);
  await ofertaDelMercado(s, (o) => o.id === publicada.id && o.lotsSold === 1, 15_000);
  await victima.sesion.esperarQue('city.update', (m) => m.payload.id === cv.id && m.payload.stocks && m.payload.stocks.STONE === piedraV - 3);
  ok('lote comprado', `${cv.name}: piedra ${piedraV} -> ${piedraV - 3} (2 y 1 de comisión); quedan ${comprada.lotsLeft}`);
  const deMadera = await caravanaDe(victima.sesion, (k) => k.senderPlayerId === c.ownerPlayerId && k.toCityId === cv.id);
  ok('caravana del mercado en camino', `${deMadera.unitId}: 10 de madera hacia ${cv.name}, llega en ${segundosHasta(deMadera.arrivesAtMs)} s`);

  // La retirada del lote que queda: vuelven 10 de madera; la comisión, no.
  s.enviar('session.view', { center: { x: c.centerX, y: c.centerY } });
  const ahora = (await s.esperar('world.snapshot')).payload.cities.find((x) => x.id === c.id);
  s.enviar('market.cancel', { offerId: publicada.id });
  await ofertaDelMercado(s, (o) => o.id === publicada.id && o.status === 'WITHDRAWN');
  await s.esperarQue('city.update', (m) => m.payload.id === c.id && m.payload.stocks && m.payload.stocks.WOOD === ahora.stocks.WOOD + 10);
  ok('oferta retirada', `vuelven 10 de madera; la comisión, no`);

  await llegada(victima.sesion, deMadera, cv.id, 'WOOD', maderaV + 10);
  const { entrada, pagina } = await entradaDelRegistro(s, (e) => e.kind === 'MARKET_SOLD' && e.otherPlayerId === cuenta.playerId);
  if (!entrada || entrada.lots !== 1) fallar('el registro del vendedor no cuenta la venta', pagina.entries.slice(0, 5));
  ok('el registro cuenta la venta', `MARKET_SOLD: 1 lote a ${cv.name}`);
  victima.sesion.cerrar();
}

// ─────────────────────────────────────────────────────────────
// Tala (SPEC-ECON-FOREST), sólo con --tala
// ─────────────────────────────────────────────────────────────

/**
 * Todos los aldeanos propios recolectan del tile `bosque` hasta que se agota
 * (forest.update a 0). Después, el aldeano que vuelve a salir hacia un bosque
 * lo hace solo hacia otro tile, a 6 o menos del agotado (RN-ECON-015).
 */
async function tala(sesion, playerId, snap, bosque) {
  const aldeanos = snap.units.filter((u) => u.playerId === playerId && u.unitType === 'VILLAGER');
  for (const a of aldeanos) {
    sesion.sesion.enviar('unit.gather', { unitId: a.id, target: { x: bosque.x, y: bosque.y } });
  }
  const queda = (snap.forests ?? []).find((f) => f.x === bosque.x && f.y === bosque.y)?.remaining ?? 100;
  ok('tala en marcha', `${aldeanos.length} aldeanos sobre (${bosque.x}, ${bosque.y}), con ${queda} de madera`);
  const ids = new Set(aldeanos.map((a) => a.id));
  // Diez de madera por viaje y un viaje cada ~35 s por aldeano, con margen.
  const plazo = Math.ceil(queda / 10 / Math.max(1, aldeanos.length)) * 60_000 + 60_000;
  const agotado = await sesion.sesion.esperarQue('forest.update',
    (m) => m.payload.x === bosque.x && m.payload.y === bosque.y && m.payload.remaining === 0, plazo);
  ok('el tile se agotó', `(${bosque.x}, ${bosque.y}) vuelve a crecer en ${new Date(agotado.payload.regrowsAtMs).toISOString()}`);

  const otro = await sesion.sesion.esperarQue('unit.movement.started', (m) => {
    if (!ids.has(m.payload.unitId)) return false;
    const t = m.payload.movement.target;
    return !(t.x === bosque.x && t.y === bosque.y) &&
      Math.max(Math.abs(t.x - bosque.x), Math.abs(t.y - bosque.y)) <= 6;
  }, 120_000);
  const t = otro.payload.movement.target;
  ok('un aldeano pasa solo al tile siguiente', `unidad ${otro.payload.unitId} -> (${t.x}, ${t.y})`);
}

// ─────────────────────────────────────────────────────────────
// Piedra y avance de era (SPEC-ECON-STONE), sólo con --piedra y --avance
// ─────────────────────────────────────────────────────────────

/**
 * Busca la colina más cercana a la ciudad, moviendo la vista en anillos si en la
 * de la ciudad no hay ninguna, manda a un aldeano a recoger piedra y espera la
 * primera carga y su depósito.
 */
async function piedra(sesion, ciudad, playerId, snap, ocupado) {
  const s = sesion.sesion;
  const terreno = indexarTerreno(snap.terrain);
  const aMedias = [...(snap.hills ?? [])];
  let colina = elegirColina(terreno, ciudad, aMedias);
  const centro = { x: ciudad.centerX, y: ciudad.centerY };
  for (const r of [96, 160, 224]) {
    if (colina && colina.d <= r - 64) break;
    for (const [dx, dy] of [[1, 0], [1, 1], [0, 1], [-1, 1], [-1, 0], [-1, -1], [0, -1], [1, -1]]) {
      const c = { x: Math.min(511, Math.max(0, centro.x + dx * r)), y: Math.min(511, Math.max(0, centro.y + dy * r)) };
      s.enviar('session.view', { center: c });
      const vista = (await s.esperar('world.snapshot')).payload;
      for (const [k, v] of indexarTerreno(vista.terrain)) terreno.set(k, v);
      aMedias.push(...(vista.hills ?? []));
    }
    colina = elegirColina(terreno, ciudad, aMedias);
  }
  if (!colina) fallar('no encontré ninguna colina a 288 tiles de la ciudad', centro);
  ok('colina encontrada', `(${colina.x}, ${colina.y}), a ${colina.d} tiles de la ciudad`);

  // La orden, con la vista en la ciudad: el tramo empieza en su chunk.
  s.enviar('session.view', { center: centro });
  await s.esperar('world.snapshot');
  // Un aldeano que no sea el que ya recolecta madera (el paso 9): sus tramos de
  // madera se confundirían con el de la piedra.
  const aldeano = snap.units.find((u) => u.playerId === playerId && u.unitType === 'VILLAGER' &&
    u.garrisonCityId === null && u.id !== ocupado && u.status === 'IDLE') ??
    snap.units.find((u) => u.playerId === playerId && u.unitType === 'VILLAGER' && u.garrisonCityId === null && u.id !== ocupado);
  if (!aldeano) fallar('no hay ningún aldeano propio libre en el mapa', snap.units);
  const piedraInicial = ciudad.stocks?.STONE ?? 0;
  const id = s.enviar('unit.gather', { unitId: aldeano.id, target: { x: colina.x, y: colina.y } });
  const ida = await s.esperarQue(['unit.movement.started', 'system.error'],
    (m) => (m.type === 'system.error' && m.requestId === id) ||
      (m.type === 'unit.movement.started' && m.payload.unitId === aldeano.id &&
        m.payload.movement.target.x === colina.x && m.payload.movement.target.y === colina.y), 30_000);
  if (ida.type === 'system.error') {
    const pista = ida.payload.code === 'PATH_TOO_LONG'
      ? ` (la colina está a ${colina.d} tiles: el tope de nodos del A*, EO_PATHFINDING_MAX_NODES, no alcanza esa ruta; con el de ADR-031, 50 000, llega el 98 % de la tierra; ver SPEC-ECON-STONE §4.1)`
      : '';
    fallar(`el servidor rechazó la orden de recoger piedra: ${ida.payload.code}${pista}`, ida.payload);
  }
  const mov = ida.payload.movement;
  ok('recogida de piedra aceptada', `aldeano ${aldeano.id} -> (${colina.x}, ${colina.y}) · ida de ${mov.path.length} tiles`);

  // La carga ocurre lejos: la vista se lleva a la colina para verla.
  s.enviar('session.view', { center: { x: colina.x, y: colina.y } });
  await s.esperar('world.snapshot');
  const plazo = Math.max(0, mov.arrivalTimeMs - Date.now()) + 10_000 + 30_000;
  const carga = await s.esperarQue('hill.update', (m) => m.payload.x === colina.x && m.payload.y === colina.y, plazo);
  const lleva = await s.esperarQue('entity.update',
    (m) => m.payload.id === aldeano.id && m.payload.carrying > 0, 15_000);
  if (lleva.payload.carryingResource !== 'STONE') fallar('la carga no es piedra', lleva.payload);
  ok('primera carga de piedra', `quedan ${carga.payload.remaining} en la colina · el aldeano lleva ${lleva.payload.carrying} de STONE`);

  // La vuelta, con la vista otra vez en la ciudad.
  s.enviar('session.view', { center: centro });
  await s.esperar('world.snapshot');
  const vuelta = mov.arrivalTimeMs - mov.startTimeMs;
  const deposito = await s.esperarQue('city.update',
    (m) => m.payload.id === ciudad.id && m.payload.stocks && (m.payload.stocks.STONE ?? 0) > piedraInicial,
    vuelta + 60_000);
  ok('piedra depositada', `piedra ${piedraInicial} -> ${deposito.payload.stocks.STONE}`);
}

/**
 * Encarga el avance a la Edad de Bronce, comprueba que la ciudad no entrena
 * mientras dura y espera a la era nueva.
 */
async function avance(sesion, ciudadInicial) {
  const s = sesion.sesion;
  const cityId = ciudadInicial.id;
  s.enviar('session.view', { center: { x: ciudadInicial.centerX, y: ciudadInicial.centerY } });
  const vista = (await s.esperar('world.snapshot')).payload;
  const ciudad = vista.cities.find((c) => c.id === cityId);
  if (!ciudad) fallar('la ciudad no está en el snapshot', vista.cities.map((c) => c.id));
  if (ciudad.era !== 'STONE_AGE') fallar(`la ciudad ya está en ${ciudad.era}: el avance no se repite`, ciudad);
  const stocks = ciudad.stocks ?? {};
  if ((stocks.WOOD ?? 0) < 400 || (stocks.STONE ?? 0) < 200) {
    fallar('faltan recursos para el avance: EO_DEVSEED_STOCK_FOR=smoke_runner pnpm run dev:seed los siembra', stocks);
  }
  if (ciudad.production) {
    await s.esperarQue('city.update', (m) => m.payload.id === cityId && m.payload.production === null, 60_000);
  }
  s.enviar('city.advance', { cityId });
  const inicio = await s.esperarQue(['city.update', 'system.error'],
    (m) => m.type === 'system.error' || (m.payload.id === cityId && m.payload.advance), 15_000);
  if (inicio.type === 'system.error') fallar('el servidor rechazó el avance', inicio.payload);
  const fin = inicio.payload.advance.completesAtMs;
  ok('avance encargado', `a ${inicio.payload.advance.era} · madera ${inicio.payload.stocks.WOOD}, piedra ${inicio.payload.stocks.STONE}`);

  s.enviar('city.train', { cityId, type: 'VILLAGER' });
  const ocupada = await s.esperar('system.error');
  if (ocupada.payload.code !== 'CITY_BUSY') fallar('la ciudad entrenó durante el avance', ocupada.payload);
  ok('mientras avanza, no entrena', 'CITY_BUSY');

  const nueva = await s.esperarQue('city.update', (m) => m.payload.id === cityId && m.payload.era, Math.max(0, fin - Date.now()) + 15_000);
  if (nueva.payload.era !== 'BRONZE_AGE' || nueva.payload.advance !== null) fallar('el fin del avance no es el esperado', nueva.payload);
  ok('Edad de Bronce', `límite de población ${nueva.payload.populationLimit}`);
}

// ─────────────────────────────────────────────────────────────
// Incursión (SPEC-COMBAT-RAID), sólo con --incursion
// ─────────────────────────────────────────────────────────────

const VICTIMA = process.env.EO_SMOKE_VICTIM || 'smoke_victim';

/** Espera a que la madera de la ciudad llegue a `n`, mandando a recolectar a todos los aldeanos. */
async function juntarMadera(sesion, ciudad, playerId, snap, n) {
  const bosque = elegirBosque(indexarTerreno(snap.terrain), ciudad, snap.forests);
  for (const u of snap.units.filter((u) => u.playerId === playerId && u.unitType === 'VILLAGER')) {
    sesion.enviar('unit.gather', { unitId: u.id, target: { x: bosque.x, y: bosque.y } });
  }
  let madera = ciudad.stocks?.WOOD ?? 0;
  while (madera < n) {
    const m = await sesion.esperarQue('city.update', (m) => m.payload.id === ciudad.id && m.payload.stocks, 120_000);
    madera = m.payload.stocks.WOOD;
  }
  return madera;
}

/** Lleva una unidad hacia `destino` por tramos: si la ruta es demasiado larga, prueba tiles junto al punto medio. */
async function acercar(sesion, unidad, destino, cerca) {
  let pos = { x: unidad.x, y: unidad.y };
  for (let tramo = 0; tramo < 8; tramo++) {
    if (Math.max(Math.abs(pos.x - destino.x), Math.abs(pos.y - destino.y)) <= cerca) return pos;
    // Como hace la cámara del cliente: el área de interés sigue a la unidad. Sin
    // esto, una unidad propia lejos de la ciudad no está en vista y su
    // unit.movement.started, que se difunde por chunk, no llega.
    sesion.enviar('session.view', { center: { x: pos.x, y: pos.y } });
    await sesion.esperar('world.snapshot');
    let objetivo = destino;
    let mov = null;
    for (let intento = 0; intento < 40 && !mov; intento++) {
      sesion.enviar('unit.move', { unitId: unidad.id, target: objetivo });
      const r = await sesion.esperar(['unit.move.accepted', 'unit.move.rejected', 'system.error']);
      if (r.type === 'unit.move.accepted') {
        mov = (await sesion.esperarQue('unit.movement.started', (m) => m.payload.unitId === unidad.id)).payload.movement;
        break;
      }
      const code = r.payload.code;
      const medio = { x: Math.round((pos.x + destino.x) / 2), y: Math.round((pos.y + destino.y) / 2) };
      // Un tile cercano al punto medio, en espiral: si no es transitable, el servidor lo dice.
      const d = 1 + Math.floor(intento / 8);
      const giro = [[1, 0], [0, 1], [-1, 0], [0, -1], [1, 1], [-1, 1], [1, -1], [-1, -1]][intento % 8];
      objetivo = code === 'TARGET_NOT_WALKABLE' && objetivo === destino
        ? { x: destino.x + giro[0] * d, y: destino.y + giro[1] * d }
        : { x: medio.x + giro[0] * d, y: medio.y + giro[1] * d };
    }
    if (!mov) fallar('no encontré cómo acercar la unidad', { desde: pos, destino });
    // Se espera a la hora de llegada de la polilínea, no a unit.movement.completed:
    // una unidad que camina lejos sale del área de interés de su propio dueño, y
    // ese mensaje sólo llega a quien la ve.
    await dormir(Math.max(0, mov.arrivalTimeMs - Date.now()) + 1500);
    pos = mov.target;
  }
  return pos;
}

/**
 * Unidades propias de un jugador: las de su ciudad y, si no están ahí, las que
 * quedaron por el camino hacia `hacia` en incursiones anteriores. La vista del
 * servidor es por área de interés, así que se muestrea la recta cada 40 tiles.
 * El terreno de cada chunk llega una sola vez por sesión: se acumula.
 */
async function unidadesPropias(sesion, playerId, ciudad, hacia, terrenoInicial) {
  const vistas = [];
  const dx = hacia.x - ciudad.centerX;
  const dy = hacia.y - ciudad.centerY;
  const pasos = Math.ceil(Math.max(Math.abs(dx), Math.abs(dy)) / 40);
  for (let i = 0; i <= pasos; i++) {
    const t = pasos === 0 ? 0 : i / pasos;
    vistas.push({ x: Math.round(ciudad.centerX + dx * t), y: Math.round(ciudad.centerY + dy * t) });
  }
  const propias = new Map();
  const terreno = [...terrenoInicial];
  for (const centro of vistas) {
    sesion.enviar('session.view', { center: centro });
    const snap = (await sesion.esperar('world.snapshot')).payload;
    terreno.push(...snap.terrain);
    for (const u of snap.units) if (u.playerId === playerId) propias.set(u.id, u);
  }
  // Se vuelve a mirar la ciudad: es donde se junta la madera y se entrena.
  sesion.enviar('session.view', { center: { x: ciudad.centerX, y: ciudad.centerY } });
  const casa = (await sesion.esperar('world.snapshot')).payload;
  terreno.push(...casa.terrain);
  return { unidades: [...propias.values()], casa, terreno };
}

/** Entrena una unidad y devuelve la que nace. */
async function entrenar(sesion, ciudad, playerId, tipo) {
  sesion.enviar('city.train', { cityId: ciudad.id, type: tipo });
  const nace = await sesion.esperarQue(['entity.spawn', 'system.error'],
    (m) => m.type === 'system.error' || (m.payload.unit?.playerId === playerId && m.payload.unit.unitType === tipo), 60_000);
  if (nace.type === 'system.error') fallar(`el servidor rechazó entrenar ${tipo}`, nace.payload);
  return nace.payload.unit;
}

async function incursion(atacante, ciudad, playerId, snap) {
  const madera = await juntarMadera(atacante.sesion, ciudad, playerId, snap, 60);
  ok('madera para un guerrero', `${madera}`);
  const guerrero = await entrenar(atacante.sesion, ciudad, playerId, 'WARRIOR');
  ok('guerrero entrenado', `unidad ${guerrero.id} en (${guerrero.x}, ${guerrero.y})`);

  // La víctima se prepara: un guardia y al menos dos aldeanos, para que el humo
  // se pueda repetir aunque cada incursión le cueste uno.
  const { cuenta } = await abrirSesion(VICTIMA);
  const victima = await conectar(cuenta.ticket);
  const ciudadV = victima.snapshot.payload.cities.find((c) => c.ownerPlayerId === cuenta.playerId);
  if (!ciudadV) fallar('la víctima no tiene ciudad en su snapshot', victima.snapshot.payload.cities);
  const { unidades, casa, terreno } = await unidadesPropias(victima.sesion, cuenta.playerId, ciudadV, guerrero,
    victima.snapshot.payload.terrain);
  const aldeanos = unidades.filter((u) => u.unitType === 'VILLAGER');
  let guardia = unidades.find((u) => u.unitType === 'WARRIOR');
  if (aldeanos.length === 0) {
    fallar('la víctima se quedó sin aldeanos',
      `Usa EO_SMOKE_VICTIM con otra cuenta cuya ciudad esté a menos de ~200 tiles de la de ${USUARIO}.`);
  }
  const falta = (guardia ? 0 : 60) + (aldeanos.length < 2 ? 50 : 0);
  if (falta > 0) {
    // Un aldeano varado lejos vuelve primero: la ruta al bosque de su ciudad
    // podría superar el límite del pathfinding.
    for (const a of aldeanos) {
      if (Math.max(Math.abs(a.x - ciudadV.centerX), Math.abs(a.y - ciudadV.centerY)) > 12) {
        await acercar(victima.sesion, a, { x: ciudadV.centerX, y: ciudadV.centerY }, 8);
      }
    }
    victima.sesion.enviar('session.view', { center: { x: ciudadV.centerX, y: ciudadV.centerY } });
    await victima.sesion.esperar('world.snapshot');
    const stockCasa = casa.cities.find((c) => c.id === ciudadV.id) ?? ciudadV;
    const m = await juntarMadera(victima.sesion, stockCasa, cuenta.playerId, { terrain: terreno, units: aldeanos, forests: casa.forests }, falta);
    ok('la víctima junta madera', `${m}`);
    if (!guardia) guardia = await entrenar(victima.sesion, ciudadV, cuenta.playerId, 'WARRIOR');
    if (aldeanos.length < 2) await entrenar(victima.sesion, ciudadV, cuenta.playerId, 'VILLAGER');
  }
  ok('la víctima tiene guardia', `guerrero ${guardia.id}`);

  // Las posiciones de arriba pueden ser viejas (el aldeano volvió a casa y
  // juntó madera): se leen otra vez, desde donde estén.
  const actual = new Map();
  for (const centro of [{ x: ciudadV.centerX, y: ciudadV.centerY }, { x: guardia.x, y: guardia.y }, { x: aldeanos[0].x, y: aldeanos[0].y }]) {
    victima.sesion.enviar('session.view', { center: centro });
    for (const u of (await victima.sesion.esperar('world.snapshot')).payload.units) actual.set(u.id, u);
  }
  aldeanos[0] = actual.get(aldeanos[0].id) ?? aldeanos[0];
  guardia = actual.get(guardia.id) ?? guardia;

  // El aldeano se acerca escoltado: el guardia se queda a un par de tiles. Un
  // unit.move sustituye la recolección, así que no hace falta cancelarla.
  const aldeano = aldeanos[0];
  const llegada = await acercar(victima.sesion, aldeano, { x: guerrero.x + 4, y: guerrero.y }, 6);
  const escolta = await acercar(victima.sesion, guardia, llegada, 2);
  ok('la víctima se acerca con escolta',
    `aldeano ${aldeano.id} en (${llegada.x}, ${llegada.y}) · guardia en (${escolta.x}, ${escolta.y})`);

  // El incursor ataca al aldeano. Sin ninguna orden de la víctima, su guardia
  // responde (SPEC-COMBAT-RESPONSE): el incursor pierde vida. El aldeano muere
  // igual: el guardia llega tarde para salvarlo, no para castigarlo.
  atacante.sesion.enviar('unit.attack', { unitId: guerrero.id, targetUnitId: aldeano.id });
  let muerto = false;
  let herido = null;
  while (!muerto || herido === null) {
    const m = await atacante.sesion.esperarQue(['entity.despawn', 'entity.update', 'system.error'],
      (m) => m.type === 'system.error' ||
        (m.type === 'entity.despawn' && m.payload.id === aldeano.id) ||
        (m.type === 'entity.update' && m.payload.id === guerrero.id && typeof m.payload.hp === 'number' && m.payload.hp < 60),
      60_000);
    if (m.type === 'system.error') fallar('el servidor rechazó el ataque', m.payload);
    if (m.type === 'entity.despawn') {
      if (m.payload.reason !== 'DEAD') fallar('la víctima desapareció sin morir', m.payload);
      muerto = true;
    } else if (herido === null) {
      herido = m.payload.hp;
    }
  }
  ok('incursión completa', `el guerrero ${guerrero.id} mató al aldeano ${aldeano.id}`);
  ok('respuesta automática', `el guardia ${guardia.id} respondió sin órdenes: el incursor bajó a ${herido} de vida`);
  const perdida = await avisoEnVivo(victima.sesion, 'UNIT_LOST', playerId);
  ok('aviso en vivo a la víctima', `UNIT_LOST ${perdida.payload.unitType} en (${perdida.payload.x}, ${perdida.payload.y})`);
  const baja = await avisoEnVivo(atacante.sesion, 'ENEMY_KILLED', cuenta.playerId);
  ok('aviso en vivo al incursor', `ENEMY_KILLED ${baja.payload.unitType} de ${baja.payload.otherCityName ?? 'otro jugador'}`);
  victima.sesion.cerrar();
}

// ─────────────────────────────────────────────────────────────
// Saqueo (SPEC-COMBAT-PILLAGE), sólo con --saqueo
// ─────────────────────────────────────────────────────────────

async function saqueo(atacante, ciudad, playerId, snap) {
  let guerrero = snap.units.find((u) => u.playerId === playerId && u.unitType === 'WARRIOR');
  if (!guerrero) {
    const madera = await juntarMadera(atacante.sesion, ciudad, playerId, snap, 60);
    ok('madera para un guerrero', `${madera}`);
    guerrero = await entrenar(atacante.sesion, ciudad, playerId, 'WARRIOR');
  }
  ok('guerrero para saquear', `unidad ${guerrero.id} en (${guerrero.x}, ${guerrero.y})`);

  // La víctima se conecta: desconectada más de cinco minutos estaría protegida
  // y el saqueo se rechazaría con CITY_PROTECTED. Necesita madera por encima
  // del suelo (50) para que haya algo que robar.
  const { cuenta } = await abrirSesion(VICTIMA);
  const victima = await conectar(cuenta.ticket);
  const ciudadV = victima.snapshot.payload.cities.find((c) => c.ownerPlayerId === cuenta.playerId);
  if (!ciudadV) fallar('la víctima no tiene ciudad en su snapshot', victima.snapshot.payload.cities);
  const almacen = await maderaDeLaVictima(victima, cuenta.playerId, ciudadV, guerrero, 70);

  // El guerrero va por tramos hasta cerca de la muralla y desde ahí se ordena el
  // saqueo: el servidor elige el tile del anillo.
  const cerca = await acercar(atacante.sesion, guerrero, { x: ciudadV.centerX, y: ciudadV.centerY + 5 }, 4);
  ok('el guerrero llega junto a la ciudad', `(${cerca.x}, ${cerca.y}) · ciudad en (${ciudadV.centerX}, ${ciudadV.centerY})`);
  atacante.sesion.enviar('session.view', { center: { x: ciudadV.centerX, y: ciudadV.centerY } });
  const vista = (await atacante.sesion.esperar('world.snapshot')).payload;
  const antes = vista.cities.find((c) => c.id === ciudadV.id)?.stocks?.WOOD ?? almacen;
  const cargaAntes = vista.units.find((u) => u.id === guerrero.id)?.carrying ?? 0;
  const casa = await vistaEnCasa(ciudad, ciudadV);

  atacante.sesion.enviar('unit.pillage', { unitId: guerrero.id, cityId: ciudadV.id });
  const golpe = await atacante.sesion.esperarQue(['city.update', 'system.error'],
    (m) => m.type === 'system.error' || (m.payload.id === ciudadV.id && m.payload.stocks && m.payload.stocks.WOOD < antes),
    60_000);
  if (golpe.type === 'system.error') fallar('el servidor rechazó el saqueo', golpe.payload);
  ok('golpe de saqueo', `almacén de la víctima ${antes} -> ${golpe.payload.stocks.WOOD}`);

  const aviso = await victima.sesion.esperarQue('city.update',
    (m) => m.payload.id === ciudadV.id && typeof m.payload.pillagedAtMs === 'number', 10_000);
  ok('aviso al saqueado', `pillagedAtMs ${aviso.payload.pillagedAtMs}`);

  const carga = await atacante.sesion.esperarQue('entity.update',
    (m) => m.payload.id === guerrero.id && typeof m.payload.carrying === 'number' && m.payload.carrying > cargaAntes, 10_000);
  ok('el guerrero carga lo robado', `${cargaAntes} -> ${carga.payload.carrying}`);

  const enVivo = await avisoEnVivo(casa.sesion, 'PILLAGE_DONE', cuenta.playerId);
  ok('aviso en vivo al saqueador, con la vista en casa',
    `PILLAGE_DONE ${enVivo.payload.amount} de madera a ${enVivo.payload.otherCityName} en (${enVivo.payload.x}, ${enVivo.payload.y})`);
  sinAvisos(victima.sesion, 'el saqueado', ['PILLAGE_DONE']);
  ok('el saqueado no recibe aviso en vivo', 'ya tiene pillagedAtMs');
  casa.sesion.cerrar();
  victima.sesion.cerrar();
}

/**
 * Deja el almacén de la víctima en `minimo` de madera como poco y lo devuelve. Sus
 * aldeanos pueden haberse quedado por el camino hacia el atacante en incursiones
 * anteriores: vuelven a casa y recolectan.
 */
async function maderaDeLaVictima(victima, victimaId, ciudadV, hacia, minimo) {
  let almacen = ciudadV.stocks?.WOOD ?? 0;
  if (almacen >= minimo) return almacen;
  const { unidades, casa, terreno } = await unidadesPropias(victima.sesion, victimaId, ciudadV, hacia,
    victima.snapshot.payload.terrain);
  const aldeanos = unidades.filter((u) => u.unitType === 'VILLAGER');
  if (aldeanos.length === 0) {
    fallar('la víctima no tiene madera ni aldeanos para juntarla',
      `Usa EO_SMOKE_VICTIM con otra cuenta cuya ciudad esté a menos de ~200 tiles de la de ${USUARIO}.`);
  }
  for (const a of aldeanos) {
    if (Math.max(Math.abs(a.x - ciudadV.centerX), Math.abs(a.y - ciudadV.centerY)) > 12) {
      await acercar(victima.sesion, a, { x: ciudadV.centerX, y: ciudadV.centerY }, 8);
    }
  }
  victima.sesion.enviar('session.view', { center: { x: ciudadV.centerX, y: ciudadV.centerY } });
  await victima.sesion.esperar('world.snapshot');
  const stockCasa = casa.cities.find((c) => c.id === ciudadV.id) ?? ciudadV;
  almacen = await juntarMadera(victima.sesion, stockCasa, victimaId,
    { terrain: terreno, units: aldeanos, forests: casa.forests }, minimo);
  ok('la víctima junta madera', `${almacen}`);
  return almacen;
}

// ─────────────────────────────────────────────────────────────
// Asedio (SPEC-COMBAT-SIEGE), sólo con --asedio
// ─────────────────────────────────────────────────────────────

/**
 * El primer tile libre de la zona urbana 3 × 3 de una ciudad, fila a fila y sin el
 * centro, como encarga casas el cliente; `null` si no queda ninguno.
 */
export function huecoParaCasa(ciudad, edificios) {
  const ocupados = new Set(edificios.map((b) => `${b.x},${b.y}`));
  for (let dy = -1; dy <= 1; dy++) {
    for (let dx = -1; dx <= 1; dx++) {
      if (dx === 0 && dy === 0) continue;
      const x = ciudad.centerX + dx;
      const y = ciudad.centerY + dy;
      if (!ocupados.has(`${x},${y}`)) return { x, y };
    }
  }
  return null;
}

async function asedio(atacante, ciudad, playerId, snap) {
  const { cuenta } = await abrirSesion(VICTIMA);
  const victima = await conectar(cuenta.ticket);
  const vistaV = victima.snapshot.payload;
  const ciudadV = vistaV.cities.find((c) => c.ownerPlayerId === cuenta.playerId);
  if (!ciudadV) fallar('la víctima no tiene ciudad en su snapshot', vistaV.cities);

  // El guerrero: el que quedó junto a la víctima tras --saqueo, el de casa, o uno nuevo.
  atacante.sesion.enviar('session.view', { center: { x: ciudadV.centerX, y: ciudadV.centerY } });
  const cerca = (await atacante.sesion.esperar('world.snapshot')).payload;
  let guerrero = cerca.units.find((u) => u.playerId === playerId && u.unitType === 'WARRIOR')
    ?? snap.units.find((u) => u.playerId === playerId && u.unitType === 'WARRIOR');
  if (!guerrero) {
    const madera = await juntarMadera(atacante.sesion, ciudad, playerId, snap, 60);
    ok('madera para un guerrero', `${madera}`);
    guerrero = await entrenar(atacante.sesion, ciudad, playerId, 'WARRIOR');
  }
  ok('guerrero para asediar', `unidad ${guerrero.id} en (${guerrero.x}, ${guerrero.y})`);

  // Una casa completa de la víctima; si no la tiene, la construye.
  const suyas = vistaV.buildings.filter((b) => b.cityId === ciudadV.id);
  let casa = suyas.find((b) => b.status === 'COMPLETE');
  if (!casa) {
    await maderaDeLaVictima(victima, cuenta.playerId, ciudadV, guerrero, 30);
    const hueco = huecoParaCasa(ciudadV, suyas);
    if (!hueco) fallar('la víctima no tiene hueco para una casa', suyas);
    victima.sesion.enviar('city.build', { cityId: ciudadV.id, type: 'HOUSE', x: hueco.x, y: hueco.y });
    const spawn = await victima.sesion.esperarQue(['building.spawn', 'system.error'],
      (m) => m.type === 'system.error' ||
        (m.payload.building.cityId === ciudadV.id && m.payload.building.x === hueco.x && m.payload.building.y === hueco.y),
      15_000);
    if (spawn.type === 'system.error') fallar('la víctima no pudo encargar la casa', spawn.payload);
    await victima.sesion.esperarQue('building.update',
      (m) => m.payload.id === spawn.payload.building.id && m.payload.status === 'COMPLETE', 30_000);
    casa = { ...spawn.payload.building, status: 'COMPLETE' };
    ok('la víctima construye una casa', `(${casa.x}, ${casa.y})`);
  }
  ok('casa que derribar', `id ${casa.id} en (${casa.x}, ${casa.y}), vida ${casa.hp ?? 150}/${casa.maxHp ?? 150}`);

  // El guerrero va por tramos hasta cerca de la ciudad y desde ahí se ordena el
  // derribo: el servidor elige el tile junto a la casa.
  const junto = await acercar(atacante.sesion, guerrero, { x: ciudadV.centerX, y: ciudadV.centerY + 5 }, 4);
  ok('el guerrero llega junto a la ciudad', `(${junto.x}, ${junto.y})`);
  atacante.sesion.enviar('session.view', { center: { x: ciudadV.centerX, y: ciudadV.centerY } });
  const vista = (await atacante.sesion.esperar('world.snapshot')).payload;
  const limiteAntes = vista.cities.find((c) => c.id === ciudadV.id)?.populationLimit;
  const enCasa = await vistaEnCasa(ciudad, ciudadV);

  atacante.sesion.enviar('unit.demolish', { unitId: guerrero.id, buildingId: casa.id });
  const golpe = await atacante.sesion.esperarQue(['building.update', 'system.error'],
    (m) => m.type === 'system.error' || (m.payload.id === casa.id && typeof m.payload.hp === 'number' && m.payload.hp < (casa.maxHp ?? 150)),
    60_000);
  if (golpe.type === 'system.error') fallar('el servidor rechazó el derribo', golpe.payload);
  ok('golpe de asedio', `vida de la casa ${golpe.payload.hp}`);

  const aviso = await victima.sesion.esperarQue('city.update',
    (m) => m.payload.id === ciudadV.id && typeof m.payload.besiegedAtMs === 'number', 10_000);
  ok('aviso al asediado', `besiegedAtMs ${aviso.payload.besiegedAtMs}`);

  // 150 de vida a 8 cada 1,5 s: unos 27 s con un solo guerrero.
  await atacante.sesion.esperarQue('building.destroyed', (m) => m.payload.id === casa.id, 60_000);
  ok('la casa cae', `building.destroyed ${casa.id}`);
  const limite = await victima.sesion.esperarQue('city.update',
    (m) => m.payload.id === ciudadV.id && typeof m.payload.populationLimit === 'number', 10_000);
  if (limiteAntes !== undefined && limite.payload.populationLimit !== limiteAntes - 5) {
    fallar('el límite de la víctima no bajó en 5', { antes: limiteAntes, despues: limite.payload.populationLimit });
  }
  ok('el límite de la víctima baja', `${limiteAntes} -> ${limite.payload.populationLimit}`);
  const derribo = await avisoEnVivo(enCasa.sesion, 'HOUSE_DESTROYED', cuenta.playerId);
  ok('aviso en vivo al atacante, con la vista en casa', `HOUSE_DESTROYED en (${derribo.payload.x}, ${derribo.payload.y})`);
  sinAvisos(victima.sesion, 'el asediado', ['HOUSE_DESTROYED']);
  enCasa.sesion.cerrar();
  victima.sesion.cerrar();
}

// ─────────────────────────────────────────────────────────────
// Arietes y murallas (SPEC-SIEGE-ENGINES), sólo con --ariete
// ─────────────────────────────────────────────────────────────

const COSTE_DE_LA_MURALLA = { WOOD: 100, STONE: 200 };
const PLAZO_DE_LA_MURALLA = 120_000;
const VIDA_DE_LA_MURALLA = 1_500;
const COSTE_DEL_ARIETE = { WOOD: 150, STONE: 75 };

/** La muralla en pie de una ciudad, o `undefined`. */
export function murallaDe(edificios, cityId) {
  return edificios.find((b) => b.cityId === cityId && b.type === 'WALL');
}

/** Cuántos golpes de `dano` abren una muralla con `vida` (SPEC-SIEGE-ENGINES §4.2). */
export function golpesParaAbrir(vida, dano = 40) {
  return vida > 0 ? Math.ceil(vida / dano) : 0;
}

/** Si alcanza para un coste en madera y piedra. */
function alcanza(stocks, coste) {
  return (stocks?.WOOD ?? 0) >= coste.WOOD && (stocks?.STONE ?? 0) >= coste.STONE;
}

async function ariete(atacante, ciudad, playerId, snap) {
  const { cuenta } = await abrirSesion(VICTIMA);
  const victima = await conectar(cuenta.ticket);
  const vistaV = victima.snapshot.payload;
  const ciudadV = vistaV.cities.find((c) => c.ownerPlayerId === cuenta.playerId);
  if (!ciudadV) fallar('la víctima no tiene ciudad en su snapshot', vistaV.cities);
  if (ciudadV.era === 'STONE_AGE') fallar(`${VICTIMA} sigue en la Edad de Piedra y la muralla pide Bronce`, ciudadV.era);

  // La muralla de la víctima: la que haya, o una nueva en el centro.
  let muralla = murallaDe(vistaV.buildings, ciudadV.id);
  if (!muralla) {
    if (!alcanza(ciudadV.stocks, COSTE_DE_LA_MURALLA)) {
      fallar(`${VICTIMA} no tiene para la muralla (200 de piedra y 100 de madera)`,
        'con el servidor parado: EO_DEVSEED_STOCK_FOR=smoke_victim pnpm run dev:seed');
    }
    victima.sesion.enviar('city.build', { cityId: ciudadV.id, type: 'WALL', x: ciudadV.centerX, y: ciudadV.centerY });
    const spawn = await victima.sesion.esperarQue(['building.spawn', 'system.error'],
      (m) => m.type === 'system.error' || (m.payload.building.cityId === ciudadV.id && m.payload.building.type === 'WALL'), 15_000);
    if (spawn.type === 'system.error') fallar('la víctima no pudo encargar la muralla', spawn.payload);
    const plazo = escalarDuracion(PLAZO_DE_LA_MURALLA, rasgo(CATALOGO, ciudadV.civilization, 'production.speed'));
    const queda = comprobarPlazo('la muralla', spawn.payload.building.completesAtMs, spawn.ts, plazo);
    ok('la víctima levanta su muralla en el centro', `${ciudadV.civilization}: ${plazo} ms (quedaban ${queda})`);
    muralla = spawn.payload.building;
  }
  if (muralla.status !== 'COMPLETE') {
    await victima.sesion.esperarQue('building.update', (u) => u.payload.id === muralla.id && u.payload.status === 'COMPLETE',
      Math.max(0, muralla.completesAtMs - Date.now()) + 15_000);
    muralla = { ...muralla, status: 'COMPLETE', hp: VIDA_DE_LA_MURALLA, maxHp: VIDA_DE_LA_MURALLA };
  }
  ok('muralla completa', `id ${muralla.id} en (${muralla.x}, ${muralla.y}), vida ${muralla.hp ?? VIDA_DE_LA_MURALLA}`);
  const casa = vistaV.buildings.find((b) => b.cityId === ciudadV.id && b.type !== 'WALL');

  // El ariete: el que quedó junto a la víctima, el de casa, o uno nuevo.
  atacante.sesion.enviar('session.view', { center: { x: ciudadV.centerX, y: ciudadV.centerY } });
  const cerca = (await atacante.sesion.esperar('world.snapshot')).payload;
  let ram = cerca.units.find((u) => u.playerId === playerId && u.unitType === 'RAM')
    ?? snap.units.find((u) => u.playerId === playerId && u.unitType === 'RAM');
  if (!ram) {
    atacante.sesion.enviar('session.view', { center: { x: ciudad.centerX, y: ciudad.centerY } });
    const casaPropia = (await atacante.sesion.esperar('world.snapshot')).payload;
    const propia = casaPropia.cities.find((c) => c.id === ciudad.id) ?? ciudad;
    if (propia.era === 'STONE_AGE' || propia.era === 'BRONZE_AGE') {
      fallar(`${USUARIO} está en ${propia.era} y el ariete pide Hierro`, 'llévala a Hierro (--castillo avanza las eras)');
    }
    if (!alcanza(propia.stocks, COSTE_DEL_ARIETE)) {
      fallar(`${USUARIO} no tiene para el ariete (150 de madera y 75 de piedra)`,
        'con el servidor parado: EO_DEVSEED_STOCK_FOR=smoke_runner pnpm run dev:seed');
    }
    ram = await entrenar(atacante.sesion, propia, playerId, 'RAM');
    ok('ariete entrenado', `unidad ${ram.id}, ${ram.hp}/${ram.maxHp}`);
  }
  ok('ariete para el asedio', `unidad ${ram.id} en (${ram.x}, ${ram.y})`);

  const junto = await acercar(atacante.sesion, ram, { x: ciudadV.centerX, y: ciudadV.centerY + 5 }, 4);
  ok('el ariete llega junto a la ciudad', `(${junto.x}, ${junto.y})`);
  atacante.sesion.enviar('session.view', { center: { x: ciudadV.centerX, y: ciudadV.centerY } });
  const vista = (await atacante.sesion.esperar('world.snapshot')).payload;
  const enCasa = ARIETE_COMPLETO ? await vistaEnCasa(ciudad, ciudadV) : null;

  // RN-SIEGE-004: con la muralla en pie, ni el ariete ni el guerrero derriban la casa.
  if (casa) {
    const id1 = atacante.sesion.enviar('unit.demolish', { unitId: ram.id, buildingId: casa.id });
    const r1 = await atacante.sesion.esperarQue('system.error', (m) => m.requestId === id1);
    if (r1.payload.code !== 'INVALID_TARGET') fallar('el ariete pudo ir a por la casa con la muralla en pie', r1.payload);
    ok('el ariete no derriba la casa', `INVALID_TARGET: ${r1.payload.message}`);
    const guerrero = vista.units.find((u) => u.playerId === playerId && u.unitType === 'WARRIOR');
    if (guerrero) {
      const id2 = atacante.sesion.enviar('unit.demolish', { unitId: guerrero.id, buildingId: casa.id });
      const r2 = await atacante.sesion.esperarQue('system.error', (m) => m.requestId === id2);
      if (r2.payload.code !== 'INVALID_TARGET') fallar('el guerrero pudo derribar con la muralla en pie', r2.payload);
      ok('el guerrero tampoco', `INVALID_TARGET: ${r2.payload.message}`);
    } else {
      ok('sin guerrero junto a la víctima', 'el rechazo del guerrero no se comprueba en esta pasada (--asedio deja uno)');
    }
  }

  atacante.sesion.enviar('unit.demolish', { unitId: ram.id, buildingId: muralla.id });
  const golpe = await atacante.sesion.esperarQue(['building.update', 'system.error'],
    (m) => m.type === 'system.error' || (m.payload.id === muralla.id && typeof m.payload.hp === 'number' && m.payload.hp < (muralla.hp ?? VIDA_DE_LA_MURALLA)),
    60_000);
  if (golpe.type === 'system.error') fallar('el servidor rechazó el derribo de la muralla', golpe.payload);
  ok('golpe del ariete', `vida de la muralla ${golpe.payload.hp}/${VIDA_DE_LA_MURALLA}`);
  const aviso = await victima.sesion.esperarQue('city.update',
    (m) => m.payload.id === ciudadV.id && typeof m.payload.besiegedAtMs === 'number', 10_000);
  ok('aviso al asediado', `besiegedAtMs ${aviso.payload.besiegedAtMs}`);

  if (ARIETE_COMPLETO) {
    const golpes = golpesParaAbrir(golpe.payload.hp);
    await atacante.sesion.esperarQue(['building.destroyed', 'entity.despawn'],
      (m) => (m.type === 'building.destroyed' && m.payload.id === muralla.id) || (m.type === 'entity.despawn' && m.payload.id === ram.id && m.payload.reason === 'DEAD'),
      golpes * 3_000 + 30_000).then((m) => {
      if (m.type === 'entity.despawn') fallar('los defensores mataron al ariete antes de abrir la muralla', m.payload);
    });
    ok('la muralla cae', `building.destroyed ${muralla.id}: la ciudad queda en brecha`);
    const derribo = await avisoEnVivo(enCasa.sesion, 'HOUSE_DESTROYED', cuenta.playerId);
    if (derribo.payload.buildingType !== 'WALL') fallar('el aviso del derribo no dice que fue la muralla', derribo.payload);
    ok('aviso en vivo al atacante, con la vista en casa', `HOUSE_DESTROYED con buildingType WALL`);
    enCasa.sesion.cerrar();
  } else {
    ok('la muralla sigue en pie', `abrirla tarda ${golpesParaAbrir(golpe.payload.hp)} golpes más con un ariete; --ariete-completo lo espera`);
  }
  victima.sesion.cerrar();
}

// ─────────────────────────────────────────────────────────────
// Avisos en vivo (SPEC-LIVE-NOTICES), en --incursion, --saqueo y --asedio
// ─────────────────────────────────────────────────────────────

/**
 * Otra sesión de smoke_runner con la vista en su propia ciudad. La de la víctima
 * le queda fuera del área de interés, así que lo que pasa allí sólo le llega por el
 * aviso en vivo.
 */
async function vistaEnCasa(ciudad, lejos) {
  const { cuenta } = await abrirSesion(USUARIO);
  const casa = await conectar(cuenta.ticket);
  casa.sesion.enviar('session.view', { center: { x: ciudad.centerX, y: ciudad.centerY } });
  const vista = (await casa.sesion.esperar('world.snapshot')).payload;
  if (vista.cities.some((c) => c.id === lejos.id)) {
    fallar('la ciudad de la víctima está en el área de interés de la ciudad propia: el aviso no probaría nada',
      `propia en (${ciudad.centerX}, ${ciudad.centerY}), víctima en (${lejos.centerX}, ${lejos.centerY})`);
  }
  return casa;
}

/** El primer aviso en vivo de ese tipo sobre el otro jugador. */
function avisoEnVivo(sesion, kind, otroId, ms = 10_000) {
  return sesion.esperarQue('event_log.notice', (m) => m.payload.kind === kind && m.payload.otherPlayerId === otroId, ms);
}

/**
 * Falla si la sesión recibió un aviso en vivo de esos tipos: el saqueado y el
 * asediado ya tienen pillagedAtMs y besiegedAtMs (SPEC-LIVE-NOTICES Q1). Una muerte
 * sí puede llegarles, si sus guardias responden.
 */
function sinAvisos(sesion, quien, kinds) {
  const avisos = sesion.recibidos.filter((m) => m.type === 'event_log.notice' && kinds.includes(m.payload.kind));
  if (avisos.length > 0) fallar(`${quien} recibió avisos en vivo que no le tocan`, avisos.map((m) => m.payload));
}

// ─────────────────────────────────────────────────────────────
// La caída de una ciudad (SPEC-CITY-FALL), sólo con --caida
// ─────────────────────────────────────────────────────────────

const CAIDA_USUARIO = process.env.EO_SMOKE_FALL_USER || 'smoke_caida';

/**
 * Si se puede asaltar la ciudad con lo que el humo sabe de ella (RN-SIEGE-010): el motivo
 * si no, o `null`. La paz y la protección las dice el servidor.
 */
export function motivoParaNoAsaltar(ciudad, edificios, ahoraMs) {
  if (ciudad.era === 'STONE_AGE') return 'está en la Edad de Piedra';
  if (edificios.some((b) => b.cityId === ciudad.id && b.type === 'WALL' && b.status === 'COMPLETE')) return 'tiene la muralla en pie';
  if (typeof ciudad.fallImmuneUntilMs === 'number' && ciudad.fallImmuneUntilMs > ahoraMs) {
    return 'es recién fundada (arranca el servidor con EO_CITY_FALL_IMMUNITY_HOURS=0)';
  }
  if (ciudad.assault) return 'ya tiene un asalto';
  return null;
}

/** La ciudad refundada de un jugador en un snapshot: la suya en pie, con otro id que la caída. */
export function ciudadRefundada(snapshot, playerId, caidaId) {
  return snapshot.cities.find((c) => c.ownerPlayerId === playerId && c.id !== caidaId);
}

async function caida(atacante, ciudad, playerId, snap) {
  const { cuenta } = await abrirSesion(CAIDA_USUARIO);
  const victima = await conectar(cuenta.ticket);
  let vistaV = victima.snapshot.payload;
  let ciudadV = vistaV.cities.find((c) => c.ownerPlayerId === cuenta.playerId);
  if (!ciudadV) fallar(`${CAIDA_USUARIO} no tiene ciudad en su snapshot`, vistaV.cities);
  if (ciudadV.era === 'STONE_AGE') {
    // La primera vez: el avance a Bronce, con lo que siembra dev:seed.
    await avance(victima, ciudadV);
    victima.sesion.enviar('session.view', { center: { x: ciudadV.centerX, y: ciudadV.centerY } });
    vistaV = (await victima.sesion.esperar('world.snapshot')).payload;
    ciudadV = vistaV.cities.find((c) => c.id === ciudadV.id);
  }
  const motivo = motivoParaNoAsaltar(ciudadV, vistaV.buildings ?? [], Date.now());
  if (motivo) fallar(`la ciudad de ${CAIDA_USUARIO} no se puede asaltar: ${motivo}`, ciudadV);
  ok('ciudad que asaltar', `${ciudadV.name} (${ciudadV.id}) en (${ciudadV.centerX}, ${ciudadV.centerY}), ${ciudadV.era}`);

  // El guerrero: el que quedó junto a la víctima de pasadas anteriores, el de casa, o uno nuevo.
  atacante.sesion.enviar('session.view', { center: { x: ciudad.centerX, y: ciudad.centerY } });
  const casa = (await atacante.sesion.esperar('world.snapshot')).payload;
  let guerrero = casa.units.find((u) => u.playerId === playerId && u.unitType === 'WARRIOR')
    ?? snap.units.find((u) => u.playerId === playerId && u.unitType === 'WARRIOR');
  if (!guerrero) {
    const madera = await juntarMadera(atacante.sesion, ciudad, playerId, snap, 60);
    ok('madera para un guerrero', `${madera}`);
    guerrero = await entrenar(atacante.sesion, ciudad, playerId, 'WARRIOR');
  }
  const junto = await acercar(atacante.sesion, guerrero, { x: ciudadV.centerX, y: ciudadV.centerY + 4 }, 4);
  ok('el guerrero llega junto a la ciudad', `(${junto.x}, ${junto.y})`);
  atacante.sesion.enviar('session.view', { center: { x: ciudadV.centerX, y: ciudadV.centerY } });
  await atacante.sesion.esperar('world.snapshot');

  atacante.sesion.enviar('unit.assault', { unitId: guerrero.id, cityId: ciudadV.id });
  const abierto = await atacante.sesion.esperarQue(['city.update', 'system.error'],
    (m) => m.type === 'system.error' || (m.payload.id === ciudadV.id && m.payload.assault), 60_000);
  if (abierto.type === 'system.error') fallar('el servidor rechazó el asalto', abierto.payload);
  const cae = abierto.payload.assault.endsAtMs;
  ok('asalto plantado', `cae en ${Math.round((cae - abierto.payload.assault.startedAtMs) / 1000)} s`);
  const aviso = await avisoEnVivo(victima.sesion, 'CITY_ASSAULTED', playerId);
  if (aviso.payload.fallsAtMs !== cae) fallar('el aviso del asalto no dice cuándo cae', aviso.payload);
  ok('aviso al asaltado', `CITY_ASSAULTED, cae a las ${new Date(cae).toISOString().slice(11, 19)}`);

  const espera = Math.max(0, cae - Date.now()) + 15_000;
  const cayo = await atacante.sesion.esperarQue('city.fallen', (m) => m.payload.cityId === ciudadV.id, espera);
  ok('la ciudad cae', `city.fallen ${ciudadV.id}`);
  // SPEC-CITY-TAKEN: si smoke_runner tenía hueco, se la queda en el mismo centro.
  const tomadaId = cayo.payload.takenCityId;
  if (tomadaId !== undefined) {
    const snapT = await atacante.sesion.esperarQue('world.snapshot',
      (m) => ciudadTomada(m.payload, playerId, tomadaId, ciudadV) !== undefined, 10_000);
    const t = ciudadTomada(snapT.payload, playerId, tomadaId, ciudadV);
    ok('la ciudad se toma', `${t.name} (${t.id}) de ${USUARIO} en (${t.centerX}, ${t.centerY}), ${t.era}`);
  } else {
    ok('la ciudad se arrasa', `${USUARIO} no tenía hueco`);
  }
  await avisoEnVivo(atacante.sesion, 'CITY_TAKEN', cuenta.playerId);
  ok('aviso al vencedor', 'CITY_TAKEN');
  await victima.sesion.esperarQue('city.fallen', (m) => m.payload.cityId === ciudadV.id, 10_000);
  const perdida = await avisoEnVivo(victima.sesion, 'CITY_LOST', playerId);
  const nuevo = await victima.sesion.esperarQue('world.snapshot',
    (m) => ciudadRefundada(m.payload, cuenta.playerId, ciudadV.id) !== undefined, 10_000);
  const refundada = ciudadRefundada(nuevo.payload, cuenta.playerId, ciudadV.id);
  if (refundada.name !== ciudadV.name || refundada.era !== ciudadV.era) {
    fallar('la ciudad refundada no lleva el nombre o la era de la caída', { antes: ciudadV, despues: refundada });
  }
  if (perdida.payload.x !== refundada.centerX || perdida.payload.y !== refundada.centerY) {
    fallar('el aviso CITY_LOST no dice dónde refundó', { aviso: perdida.payload, ciudad: refundada });
  }
  ok('refundada sin reconectar', `${refundada.name} (${refundada.id}) en (${refundada.centerX}, ${refundada.centerY}), ${refundada.era}`);
  victima.sesion.cerrar();
}

// ─────────────────────────────────────────────────────────────
// La segunda ciudad (SPEC-MULTI-CITY), sólo con --multiciudad
// ─────────────────────────────────────────────────────────────

const MULTI_USUARIO = process.env.EO_SMOKE_MULTI_USER || 'smoke_multi';

// nombreDeLaFundada es el nombre de la ciudad que funda usuario con estas ciudades
// en pie: el primero libre de su serie, `<usuario>polis` y ese nombre con « II»,
// « III» o « IV» (SPEC-CITY-NAMES RN-CITY-043, RN-CITY-044; `city.FoundedName`).
export function nombreDeLaFundada(usuario, enPie) {
  const base = `${usuario}polis`;
  for (const numeral of ['', ' II', ' III', ' IV']) {
    if (!enPie.includes(base + numeral)) return base + numeral;
  }
  return base;
}

/**
 * Un sitio donde fundar visto desde el humo (RN-CITY-030): el 7 × 7 alrededor
 * transitable, a 24 o más de todo centro conocido, con 10 tiles de bosque a 8 o
 * menos fuera de la zona urbana y en un territorio libre de los conocidos. El más
 * cercano a `desde` a `minimo` tiles o más; a igual distancia, el de menor (y, x).
 * Sólo lo aproxima: el bosque alcanzable y el agotado los mira el servidor.
 */
export function sitioParaFundar(terreno, centros, territorios, desde, minimo = 28, maximo = 56) {
  const libre = (x, y) => {
    const t = territorios.find((tt) => x >= tt.minX && x <= tt.maxX && y >= tt.minY && y <= tt.maxY);
    return !t || (t.ownerType === 'NONE' && !t.claim);
  };
  const pisa = (x, y) => {
    const tipo = terreno.get(`${x},${y}`);
    return tipo !== undefined && !NO_TRANSITABLE.has(tipo);
  };
  let mejor = null;
  for (let y = desde.y - maximo; y <= desde.y + maximo; y++) {
    for (let x = desde.x - maximo; x <= desde.x + maximo; x++) {
      const d = Math.max(Math.abs(x - desde.x), Math.abs(y - desde.y));
      if (d < minimo || (mejor && d > mejor.d)) continue;
      if (centros.some((c) => Math.max(Math.abs(c.x - x), Math.abs(c.y - y)) < 24)) continue;
      if (!libre(x, y)) continue;
      let despejado = true;
      for (let dy = -3; dy <= 3 && despejado; dy++) for (let dx = -3; dx <= 3; dx++) if (!pisa(x + dx, y + dy)) { despejado = false; break; }
      if (!despejado) continue;
      let bosque = 0;
      for (let dy = -8; dy <= 8; dy++) {
        for (let dx = -8; dx <= 8; dx++) {
          if (Math.max(Math.abs(dx), Math.abs(dy)) <= 1) continue;
          if (terreno.get(`${x + dx},${y + dy}`) === FOREST) bosque++;
        }
      }
      if (bosque < 10) continue;
      if (!mejor || d < mejor.d || (d === mejor.d && (y < mejor.y || (y === mejor.y && x < mejor.x)))) mejor = { x, y, d };
    }
  }
  return mejor;
}

/**
 * Lleva una unidad a un tile por tramos de 32 como mucho, con la vista siguiéndola. Un
 * tramo aceptado con `movementId: 0` (ya estaba allí) cuenta como llegado; uno que no se
 * pisa se desplaza en espiral. Devuelve dónde acaba.
 */
async function caminarHasta(sesion, unidad, destino) {
  let pos = { x: unidad.x, y: unidad.y };
  const giros = [[0, 0], [1, 0], [0, 1], [-1, 0], [0, -1], [1, 1], [-1, 1], [1, -1], [-1, -1]];
  for (let tramo = 0; tramo < 16; tramo++) {
    const dx = destino.x - pos.x;
    const dy = destino.y - pos.y;
    const d = Math.max(Math.abs(dx), Math.abs(dy));
    if (d === 0) return pos;
    const k = d <= 32 ? 1 : 32 / d;
    sesion.enviar('session.view', { center: pos });
    await sesion.esperar('world.snapshot');
    let hecho = false;
    for (let intento = 0; intento < giros.length * 2 && !hecho; intento++) {
      const [gx, gy] = giros[intento % giros.length];
      const r = 1 + Math.floor(intento / giros.length);
      const meta = { x: Math.round(pos.x + dx * k) + gx * r, y: Math.round(pos.y + dy * k) + gy * r };
      sesion.enviar('unit.move', { unitId: unidad.id, target: meta });
      const resp = await sesion.esperarQue(['unit.move.accepted', 'unit.move.rejected', 'system.error'],
        (m) => m.type === 'system.error' || m.payload.unitId === unidad.id);
      if (resp.type !== 'unit.move.accepted') continue;
      if (resp.payload.movementId === 0) {
        pos = meta;
      } else {
        const fin = await sesion.esperarQue(['unit.movement.completed', 'unit.movement.cancelled'],
          (m) => m.payload.unitId === unidad.id && m.payload.movementId === resp.payload.movementId, 180_000);
        if (fin.type !== 'unit.movement.completed') fallar('el paseo del aldeano se interrumpió', fin.payload);
        pos = fin.payload.finalPosition;
      }
      hecho = true;
      if (k === 1) return pos;
    }
    if (!hecho) fallar('el aldeano no encuentra tramo hacia el sitio', { pos, destino });
  }
  return pos;
}

async function multiciudad() {
  const { cuenta } = await abrirSesion(MULTI_USUARIO);
  const yo = await conectar(cuenta.ticket);
  let vista = yo.snapshot.payload;
  const propias = () => vista.cities.filter((c) => c.ownerPlayerId === cuenta.playerId).sort((a, b) => a.id - b.id);
  let capital = propias()[0];
  if (!capital) fallar(`${MULTI_USUARIO} no tiene ciudad en su snapshot`, vista.cities);
  if (capital.era === 'STONE_AGE') {
    await avance(yo, capital);
    yo.sesion.enviar('session.view', { center: { x: capital.centerX, y: capital.centerY } });
    vista = (await yo.sesion.esperar('world.snapshot')).payload;
    capital = propias()[0];
  }
  const aldeanos = vista.units.filter((u) => u.playerId === cuenta.playerId && u.unitType === 'VILLAGER'
    && u.cityId === capital.id && u.garrisonCityId === null);
  if (aldeanos.length < 2) fallar(`${MULTI_USUARIO} necesita dos aldeanos de ${capital.name}`, aldeanos);

  // Ya fundó en una pasada anterior: la tercera se rechaza.
  const intentar = async (aldeano) => {
    yo.sesion.enviar('unit.found_city', { unitId: aldeano.id });
    // El snapshot de la fundación va centrado en la ciudad nueva (spec §9.1).
    return yo.sesion.esperarQue(['world.snapshot', 'system.error'], (m) => m.type === 'system.error'
      || m.payload.cities.some((c) => c.ownerPlayerId === cuenta.playerId && c.id !== capital.id), 60_000);
  };
  const stocks = capital.stocks ?? {};
  // La segunda puede quedar fuera de la vista: el servidor mira el tope antes que el sitio,
  // así que una orden desde la capital dice si ya tiene dos (y si no, se rechaza por el
  // sitio sin reservar nada).
  const sonda = await intentar(aldeanos[0]);
  if (sonda.type === 'system.error' && sonda.payload.code === 'INVALID_TARGET' && /dos ciudades|el tope de tu capital/.test(sonda.payload.message)) {
    ok('ya tiene dos ciudades de una pasada anterior', 'la tercera se rechaza (INVALID_TARGET)');
    yo.sesion.cerrar();
    return;
  }
  if ((stocks.WOOD ?? 0) < 800 || (stocks.STONE ?? 0) < 400) {
    fallar(`${capital.name} necesita 800 de madera y 400 de piedra (con el servidor parado, EO_DEVSEED_STOCK_FOR=${MULTI_USUARIO} pnpm run dev:seed)`, stocks);
  }
  ok('ciudad que funda', `${capital.name} (${capital.id}), ${capital.era}, ${stocks.WOOD} de madera y ${stocks.STONE} de piedra`);

  // El mundo de desarrollo es denso: se mira alrededor de la capital, moviendo la vista,
  // y se junta lo visto (terreno, ciudades y territorios).
  const terreno = new Map();
  const centros = new Map();
  const territorios = new Map();
  const juntar = (v) => {
    for (const [k, t] of indexarTerreno(v.terrain)) terreno.set(k, t);
    for (const c of v.cities) centros.set(c.id, { x: c.centerX, y: c.centerY });
    for (const t of v.territories ?? []) territorios.set(t.id, t);
  };
  juntar(vista);
  const desde = { x: capital.centerX, y: capital.centerY };
  let sitio = sitioParaFundar(terreno, [...centros.values()], [...territorios.values()], desde);
  for (const [dx, dy] of [[48, 0], [-48, 0], [0, 48], [0, -48], [48, 48], [-48, 48], [48, -48], [-48, -48]]) {
    if (sitio) break;
    yo.sesion.enviar('session.view', { center: { x: desde.x + dx, y: desde.y + dy } });
    juntar((await yo.sesion.esperar('world.snapshot')).payload);
    sitio = sitioParaFundar(terreno, [...centros.values()], [...territorios.values()], desde, 28, 96);
  }
  if (!sitio) {
    const libres = [...territorios.values()].filter((t) => t.ownerType === 'NONE').length;
    fallar('no hay un sitio para fundar alrededor de la capital', { tiles: terreno.size, ciudades: centros.size, territorios: territorios.size, libres });
  }
  yo.sesion.enviar('session.view', { center: desde });
  vista = (await yo.sesion.esperar('world.snapshot')).payload;
  const fundador = aldeanos[0];
  ok('sitio elegido', `(${sitio.x}, ${sitio.y}) para el aldeano ${fundador.id}, en (${fundador.x}, ${fundador.y})`);
  const llega = await caminarHasta(yo.sesion, fundador, sitio);
  ok('el aldeano llega al sitio', `(${llega.x}, ${llega.y}), a ${sitio.d} tiles de la capital`);

  // SPEC-CITY-NAMES RN-CITY-044: el nombre que debe llevar, con las propias que
  // se ven antes de fundar (la capital, con el humo de siempre).
  const esperado = nombreDeLaFundada(MULTI_USUARIO, propias().map((c) => c.name));
  const r = await intentar(fundador);
  if (r.type === 'system.error' && r.payload.code === 'INVALID_TARGET' && /dos ciudades|el tope de tu capital/.test(r.payload.message)) {
    ok('ya tiene dos ciudades de una pasada anterior', 'la tercera se rechaza (INVALID_TARGET)');
    yo.sesion.cerrar();
    return;
  }
  if (r.type === 'system.error') fallar('el servidor rechazó la fundación', r.payload);
  vista = r.payload;
  const nueva = propias().find((c) => c.id !== capital.id);
  if (!nueva || nueva.name !== esperado || nueva.era !== capital.era) {
    fallar('la ciudad nueva no lleva el primer nombre libre de la serie o la era de la capital', { esperado, capital, nueva });
  }
  if (nueva.centerX !== llega.x || nueva.centerY !== llega.y) fallar('la ciudad nueva no está en el tile del aldeano', { nueva, llega });
  const capitalDespues = vista.cities.find((c) => c.id === capital.id);
  if (capitalDespues && ((capitalDespues.stocks?.WOOD ?? 0) > (stocks.WOOD ?? 0) - 800)) {
    fallar('la capital no pagó la fundación', { antes: stocks, despues: capitalDespues.stocks });
  }
  ok('segunda ciudad fundada', `${nueva.name} (${nueva.id}) en (${nueva.centerX}, ${nueva.centerY}), ${nueva.era}`);

  const otro = vista.units.find((u) => u.playerId === cuenta.playerId && u.unitType === 'VILLAGER' && u.id !== fundador.id);
  if (!otro) fallar('no queda otro aldeano para probar la tercera', vista.units.length);
  yo.sesion.enviar('unit.found_city', { unitId: otro.id });
  const tercera = await yo.sesion.esperar('system.error', 30_000);
  if (tercera.payload.code !== 'INVALID_TARGET') fallar('una tercera ciudad no se rechaza con INVALID_TARGET', tercera.payload);
  ok('la tercera se rechaza', `${tercera.payload.code}: ${tercera.payload.message}`);
  yo.sesion.cerrar();
}

// ─────────────────────────────────────────────────────────────
// Conquista (SPEC-TERRITORY-CONQUEST), sólo con --territorio
// ─────────────────────────────────────────────────────────────

/**
 * El territorio libre (sin dueño ni reclamación) más cercano a `desde`, y un tile
 * `margen` tiles dentro de él lo más cerca posible de `desde`: lo bastante dentro
 * para que el guerrero quede en el territorio aunque se pare un tile antes, y para
 * que los guerreros de alrededor no lo tengan a su alcance. Determinista: a igual
 * distancia (Chebyshev, la de los pasos), el de menor distancia Manhattan y después
 * el de menor id.
 */
export function territorioLibreCerca(territorios, desde, margen = 6) {
  return territorioCerca(territorios, desde, (t) => t.ownerType === 'NONE', margen);
}

/**
 * Como territorioLibreCerca, con el criterio de qué territorio vale en `admite`. Un
 * territorio con una reclamación en curso no vale nunca.
 */
export function territorioCerca(territorios, desde, admite, margen = 6) {
  let mejor = null;
  for (const t of territorios) {
    if (!admite(t) || t.claim) continue;
    if (t.maxX - t.minX < 2 * margen || t.maxY - t.minY < 2 * margen) continue;
    const destino = {
      x: Math.min(Math.max(desde.x, t.minX + margen), t.maxX - margen),
      y: Math.min(Math.max(desde.y, t.minY + margen), t.maxY - margen),
    };
    const dx = Math.abs(destino.x - desde.x);
    const dy = Math.abs(destino.y - desde.y);
    const d = Math.max(dx, dy);
    const m = dx + dy;
    if (!mejor || d < mejor.d || (d === mejor.d && (m < mejor.m || (m === mejor.m && t.id < mejor.territorio.id)))) {
      mejor = { territorio: t, destino, d, m };
    }
  }
  return mejor && { territorio: mejor.territorio, destino: mejor.destino };
}

/** La posición actual de una unidad, leída de un snapshot centrado en `cerca`. */
async function posicionDe(sesion, unitId, cerca) {
  sesion.enviar('session.view', { center: cerca });
  const vista = (await sesion.esperar('world.snapshot')).payload;
  return { vista, unidad: vista.units.find((u) => u.id === unitId) };
}

/**
 * Si el mensaje es el territory.update de `territoryId` con una reclamación de
 * `playerId`. Sólo ésa confirma la propia: la sesión puede tener en la cola el
 * territory.update de una reclamación anterior de otro jugador sobre el mismo
 * territorio, y su plazo no es el nuestro.
 */
export function esReclamacionDe(m, territoryId, playerId) {
  return m.type === 'territory.update' && m.payload.territory.id === territoryId &&
    m.payload.territory.claim?.playerId === playerId;
}

/** Reclama con `unidad` el territorio en que está y espera la confirmación con su plazo. */
async function reclamar(sesion, unidad, t, playerId) {
  sesion.enviar('unit.claim', { unitId: unidad.id });
  const m = await sesion.esperarQue(['territory.update', 'system.error'],
    (m) => m.type === 'system.error' || esReclamacionDe(m, t.id, playerId), 15_000);
  if (m.type === 'system.error') fallar('el servidor rechazó la reclamación', m.payload);
  return m.payload.territory.claim;
}

async function limiteDe(sesion, ciudadId, ms) {
  const m = await sesion.esperarQue('city.update',
    (m) => m.payload.id === ciudadId && typeof m.payload.populationLimit === 'number', ms);
  return m.payload.populationLimit;
}

async function territorio(propio, ciudad, playerId) {
  const { cuenta } = await abrirSesion(VICTIMA);
  const victima = await conectar(cuenta.ticket);
  const ciudadV = victima.snapshot.payload.cities.find((c) => c.ownerPlayerId === cuenta.playerId);
  if (!ciudadV) fallar('la víctima no tiene ciudad en su snapshot', victima.snapshot.payload.cities);

  // El guerrero de smoke_runner que quedó junto a la víctima.
  propio.sesion.enviar('session.view', { center: { x: ciudadV.centerX, y: ciudadV.centerY } });
  const cerca = (await propio.sesion.esperar('world.snapshot')).payload;
  const guerrero = cerca.units.find((u) => u.playerId === playerId && u.unitType === 'WARRIOR');
  if (!guerrero) fallar(`${USUARIO} no tiene guerreros junto a la víctima`, 'Corre antes --saqueo o --asedio.');
  const libre = territorioLibreCerca(cerca.territories, guerrero);
  if (!libre) fallar('no hay territorios libres cerca de la víctima', cerca.territories);
  const t = libre.territorio;
  ok('territorio libre que reclamar', `${t.name} (${t.id}), (${t.minX},${t.minY})-(${t.maxX},${t.maxY})`);

  const casa = await vistaEnCasa(ciudad, ciudadV);
  casa.sesion.enviar('session.view', { center: { x: ciudad.centerX, y: ciudad.centerY } });
  const limiteAntes = (await casa.sesion.esperar('world.snapshot')).payload.cities
    .find((c) => c.id === ciudad.id)?.populationLimit;

  // smoke_runner lo conquista.
  const llegada = await acercar(propio.sesion, guerrero, libre.destino, 1);
  const { unidad: plantado } = await posicionDe(propio.sesion, guerrero.id, llegada);
  if (!plantado || plantado.x < t.minX || plantado.x > t.maxX || plantado.y < t.minY || plantado.y > t.maxY) {
    fallar('el guerrero no llegó al territorio', { guerrero: plantado, territorio: t });
  }
  const claim = await reclamar(propio.sesion, plantado, t, playerId);
  ok('reclamación en disputa', `el guerrero ${plantado.id} en (${plantado.x}, ${plantado.y}); vence en ${Math.round((claim.endsAtMs - Date.now()) / 1000)} s`);
  await propio.sesion.esperarQue('territory.update',
    (m) => m.payload.territory.id === t.id && m.payload.territory.ownerId === playerId && !m.payload.territory.contested,
    Math.max(0, claim.endsAtMs - Date.now()) + 30_000);
  ok('territorio conquistado', `${t.name} es de ${USUARIO}`);
  const limite = await limiteDe(casa.sesion, ciudad.id, 15_000);
  if (limiteAntes !== undefined && limite !== limiteAntes + 5) fallar('el límite no subió en 5', { antes: limiteAntes, despues: limite });
  ok('+5 de población por el territorio', `${limiteAntes} -> ${limite}`);

  // smoke_victim se lo reconquista, con smoke_runner mirando su ciudad.
  victima.sesion.enviar('session.view', { center: { x: ciudadV.centerX, y: ciudadV.centerY } });
  const vistaV = (await victima.sesion.esperar('world.snapshot')).payload;
  let guardia = vistaV.units.find((u) => u.playerId === cuenta.playerId && u.unitType === 'WARRIOR' && u.status !== 'GARRISONED');
  if (!guardia) {
    // Sin guerrero, la víctima entrena uno: con 60 de madera en el almacén basta.
    const madera = vistaV.cities.find((c) => c.id === ciudadV.id)?.stocks?.WOOD ?? 0;
    if (madera < 60) fallar('la víctima no tiene guerrero ni madera para entrenarlo', { madera });
    guardia = await entrenar(victima.sesion, ciudadV, cuenta.playerId, 'WARRIOR');
    ok('la víctima entrena un guerrero', `unidad ${guardia.id}`);
  }
  // El guerrero de smoke_runner se aparta, para que no responda solo (Q11).
  await acercar(propio.sesion, plantado, { x: ciudadV.centerX, y: ciudadV.centerY + 5 }, 4);
  const llegadaV = await acercar(victima.sesion, guardia, libre.destino, 1);
  const { unidad: plantadoV } = await posicionDe(victima.sesion, guardia.id, llegadaV);
  const claimV = await reclamar(victima.sesion, plantadoV, t, cuenta.playerId);
  ok('la víctima reclama el territorio', `guerrero ${plantadoV.id}; vence en ${Math.round((claimV.endsAtMs - Date.now()) / 1000)} s`);
  const reclamado = await avisoEnVivo(casa.sesion, 'TERRITORY_CLAIMED', cuenta.playerId, 15_000);
  ok('aviso en vivo al dueño, con la vista en casa', `TERRITORY_CLAIMED ${reclamado.payload.territoryName}`);
  const perdido = await avisoEnVivo(casa.sesion, 'TERRITORY_LOST', cuenta.playerId,
    Math.max(0, claimV.endsAtMs - Date.now()) + 30_000);
  ok('aviso en vivo de la pérdida', `TERRITORY_LOST ${perdido.payload.territoryName}`);
  const ganado = await avisoEnVivo(victima.sesion, 'TERRITORY_TAKEN', playerId, 15_000);
  ok('aviso en vivo al conquistador', `TERRITORY_TAKEN ${ganado.payload.territoryName}`);
  const limiteFinal = await limiteDe(casa.sesion, ciudad.id, 15_000);
  if (limiteFinal !== limite - 5) fallar('el límite no bajó en 5', { antes: limite, despues: limiteFinal });
  ok('el límite de smoke_runner baja', `${limite} -> ${limiteFinal}`);
  casa.sesion.cerrar();
  victima.sesion.cerrar();
}

// ─────────────────────────────────────────────────────────────
// Diplomacia (SPEC-DIPLOMACY), sólo con --diplomacia
// ─────────────────────────────────────────────────────────────

/**
 * El tratado vigente que da paz entre los dos, si lo hay: un pacto o una alianza.
 * Un acuerdo comercial (TRADE, de --comercio) deja comerciar pero no da paz, y
 * proponer el pacto encima es subir de escalón (SPEC-TRADE §4.1).
 */
export function pactoVigente(tratados) {
  return tratados.find((x) => x.status === 'ACTIVE' && (x.type === 'NON_AGGRESSION' || x.type === 'ALLIANCE'));
}

/** Espera el treaty.update de un tratado que cumpla `cond`. */
async function tratado(sesion, cond, ms = 10_000) {
  return (await sesion.esperarQue('treaty.update', (m) => cond(m.payload.treaty), ms)).payload.treaty;
}

async function diplomacia(propio, playerId, snap) {
  const { cuenta } = await abrirSesion(VICTIMA);
  const victima = await conectar(cuenta.ticket);
  const ciudadV = victima.snapshot.payload.cities.find((c) => c.ownerPlayerId === cuenta.playerId);
  if (!ciudadV) fallar('la víctima no tiene ciudad en su snapshot', victima.snapshot.payload.cities);
  const entre = (snap.treaties ?? []).filter((x) => x.otherPlayerId === cuenta.playerId);

  let vigente = pactoVigente(entre);
  if (vigente?.breaksAtMs) {
    const min = Math.ceil((vigente.breaksAtMs - Date.now()) / 60_000);
    fallar('el tratado de una pasada anterior sigue en preaviso', `vuelve a probar dentro de ${min} min`);
  }
  const comercial = entre.find((x) => x.status === 'ACTIVE' && x.type === 'TRADE');
  if (comercial?.breaksAtMs) {
    const min = Math.ceil((comercial.breaksAtMs - Date.now()) / 60_000);
    fallar('el acuerdo comercial de una pasada anterior está en preaviso y no se puede subir', `vuelve a probar dentro de ${min} min`);
  }
  if (!vigente) {
    let oferta = entre.find((x) => x.status === 'PROPOSED');
    if (!oferta) {
      propio.sesion.enviar('treaty.propose', { playerId: cuenta.playerId, type: 'NON_AGGRESSION' });
      oferta = await tratado(propio.sesion, (x) => x.otherPlayerId === cuenta.playerId && x.status === 'PROPOSED');
      ok('pacto propuesto', `tratado ${oferta.id} a ${ciudadV.name}${comercial ? `, encima del acuerdo comercial ${comercial.id}` : ''}`);
    }
    // Responde quien no la propuso (INV-DIPLO-001).
    const responde = oferta.proposedByMe ? victima.sesion : propio.sesion;
    responde.enviar('treaty.respond', { treatyId: oferta.id, accept: true });
    // La sustitución llega antes que el pacto: el acuerdo comercial cerrado en la
    // misma transacción (esperarQue descarta lo que no espera, así que se mira aquí).
    let sustituido = false;
    vigente = await tratado(propio.sesion, (x) => {
      if (comercial && x.id === comercial.id && x.status === 'EXPIRED') sustituido = true;
      return x.id === oferta.id && x.status === 'ACTIVE';
    });
    await tratado(victima.sesion, (x) => x.id === oferta.id && x.status === 'ACTIVE');
    ok('pacto aceptado', `tratado ${vigente.id} vigente para los dos`);
    if (comercial) {
      if (!sustituido) fallar('el pacto no sustituyó al acuerdo comercial', comercial);
      ok('el pacto sustituye al acuerdo comercial', `tratado ${comercial.id} EXPIRED`);
    }
  } else {
    ok('pacto vigente de una pasada anterior', `tratado ${vigente.id}`);
  }

  // La paz impide el saqueo (RN-COMBAT-020): se rechaza al ordenar, esté donde esté el guerrero.
  propio.sesion.enviar('session.view', { center: { x: ciudadV.centerX, y: ciudadV.centerY } });
  const alli = (await propio.sesion.esperar('world.snapshot')).payload;
  const guerrero = [...alli.units, ...snap.units].find((u) => u.playerId === playerId && u.unitType === 'WARRIOR');
  if (guerrero) {
    propio.sesion.enviar('unit.pillage', { unitId: guerrero.id, cityId: ciudadV.id });
    const r = await propio.sesion.esperar('system.error');
    if (r.payload.code !== 'INVALID_TARGET') fallar('el pacto no impidió el saqueo', r.payload);
    ok('el pacto impide el saqueo', `INVALID_TARGET: ${r.payload.message}`);
  } else {
    ok('sin guerrero a mano: no se comprueba el saqueo', '');
  }

  // Romper abre el preaviso: el tratado sigue vigente, con su fin.
  propio.sesion.enviar('treaty.cancel', { treatyId: vigente.id });
  const roto = await tratado(victima.sesion, (x) => x.id === vigente.id && x.breaksAtMs !== null);
  if (roto.status !== 'ACTIVE') fallar('romper no dejó el tratado en preaviso', roto);
  ok('ruptura con preaviso', `sigue ACTIVE hasta ${new Date(roto.breaksAtMs).toISOString()}`);
  victima.sesion.cerrar();
}

// ─────────────────────────────────────────────────────────────
// Comercio (SPEC-TRADE), sólo con --comercio
// ─────────────────────────────────────────────────────────────

/** Espera el trade.update de una oferta que cumpla `cond`. */
async function oferta(sesion, cond, ms = 10_000) {
  return (await sesion.esperarQue('trade.update', (m) => cond(m.payload.offer), ms)).payload.offer;
}

async function comercio(propio, ciudad) {
  const { cuenta } = await abrirSesion(VICTIMA);
  const victima = await conectar(cuenta.ticket);
  const ciudadV = victima.snapshot.payload.cities.find((c) => c.ownerPlayerId === cuenta.playerId);
  if (!ciudadV) fallar('la víctima no tiene ciudad en su snapshot', victima.snapshot.payload.cities);
  const delPar = (victima.snapshot.payload.treaties ?? []).filter((x) => x.otherCityId === ciudad.id);

  // SPEC-TRADE §4.1: cualquier tratado vigente deja comerciar, también en preaviso.
  let vigente = delPar.find((x) => x.status === 'ACTIVE');
  if (!vigente) {
    let ofertaT = delPar.find((x) => x.status === 'PROPOSED');
    if (!ofertaT) {
      victima.sesion.enviar('treaty.propose', { playerId: ciudad.ownerPlayerId, type: 'TRADE' });
      ofertaT = await tratado(victima.sesion, (x) => x.otherCityId === ciudad.id && x.status === 'PROPOSED');
      ok('acuerdo comercial propuesto', `tratado ${ofertaT.id} a ${ciudad.name}`);
    }
    const responde = ofertaT.proposedByMe ? propio.sesion : victima.sesion;
    responde.enviar('treaty.respond', { treatyId: ofertaT.id, accept: true });
    vigente = await tratado(victima.sesion, (x) => x.id === ofertaT.id && x.status === 'ACTIVE');
    ok('acuerdo comercial aceptado', `tratado ${vigente.id}: TRADE no da paz`);
  } else {
    ok('tratado vigente que deja comerciar', `tratado ${vigente.id} (${vigente.type})`);
  }

  // Una oferta pendiente de una pasada anterior (una por par, Q5): la retira quien
  // la hizo. El snapshot de la víctima trae las suyas, en las dos direcciones.
  const vieja = (victima.snapshot.payload.tradeOffers ?? []).find((o) => o.otherCityId === ciudad.id);
  if (vieja) {
    const quien = vieja.proposedByMe ? victima.sesion : propio.sesion;
    quien.enviar('trade.cancel', { offerId: vieja.id });
    await oferta(victima.sesion, (x) => x.id === vieja.id && x.status === 'WITHDRAWN');
    ok('oferta de una pasada anterior retirada', `oferta ${vieja.id}`);
  }

  const piedraV = ciudadV.stocks?.STONE ?? 0;
  const maderaV = ciudadV.stocks?.WOOD ?? 0;
  if (piedraV < 10) {
    fallar(`${VICTIMA} tiene ${piedraV} de piedra y el trato pide 10`,
      'con el servidor parado: EO_DEVSEED_STOCK_FOR=smoke_victim pnpm run dev:seed');
  }

  // RN-TRADE-001: no se ofrece lo que no se tiene.
  const id0 = propio.sesion.enviar('trade.propose', {
    playerId: cuenta.playerId, give: [{ resource: 'WOOD', amount: 1_000_000 }], want: [{ resource: 'STONE', amount: 1 }],
  });
  const r0 = await propio.sesion.esperarQue('system.error', (m) => m.requestId === id0);
  if (r0.payload.code !== 'INSUFFICIENT_RESOURCES') fallar('ofrecer lo que no se tiene no se rechazó', r0.payload);
  ok('ofrecer lo que no se tiene se rechaza', `INSUFFICIENT_RESOURCES: ${r0.payload.message}`);

  // La vista de smoke_runner, en su ciudad: ahí nace su caravana (pasos anteriores
  // pueden haberla llevado a la de la víctima).
  propio.sesion.enviar('session.view', { center: { x: ciudad.centerX, y: ciudad.centerY } });
  await propio.sesion.esperar('world.snapshot');
  propio.sesion.enviar('trade.propose', {
    playerId: cuenta.playerId, give: [{ resource: 'WOOD', amount: 50 }], want: [{ resource: 'STONE', amount: 10 }],
  });
  const propia = await oferta(propio.sesion, (x) => x.otherPlayerId === cuenta.playerId && x.status === 'PROPOSED');
  const recibida = await oferta(victima.sesion, (x) => x.id === propia.id);
  if (!propia.proposedByMe || recibida.proposedByMe) fallar('la oferta no dice quién la hizo', { propia, recibida });
  if (recibida.give[0]?.resource !== 'STONE' || recibida.get[0]?.resource !== 'WOOD') {
    fallar('la oferta no se ve desde el lado de quien la recibe', recibida);
  }
  ok('oferta de comercio', `${propia.id}: 50 de madera por 10 de piedra; ${ciudadV.name} la ve como 10 de piedra por 50 de madera`);

  // Lo que el registro de quien recibe ya tenía antes del trato: --mercado, o una pasada
  // anterior, le dejan llegadas de caravana del mismo remitente que no son ésta.
  const antesDelTrato = ultimoIdDelRegistro(await paginaDelRegistro(victima.sesion));

  // RN-CARAVAN-001: aceptar sólo debita; lo que da cada parte sale en su caravana.
  victima.sesion.enviar('trade.respond', { offerId: recibida.id, accept: true });
  await oferta(victima.sesion, (x) => x.id === recibida.id && x.status === 'ACCEPTED');
  await oferta(propio.sesion, (x) => x.id === propia.id && x.status === 'ACCEPTED');
  const debito = await victima.sesion.esperarQue('city.update',
    (m) => m.payload.id === ciudadV.id && m.payload.stocks && m.payload.stocks.STONE === piedraV - 10);
  if (debito.payload.stocks.WOOD !== maderaV) fallar('la madera llegó a quien acepta sin caravana', debito.payload);
  ok('trato aceptado: cada parte paga lo suyo', `${ciudadV.name}: piedra ${piedraV} -> ${piedraV - 10}; la madera viene en camino`);

  // La caravana de madera, a la velocidad de una caravana romana, y su llegada.
  const deMadera = await caravanaDe(victima.sesion, (c) => c.senderPlayerId === ciudad.ownerPlayerId && c.toCityId === ciudadV.id);
  const salida = await propio.sesion.esperarQue('entity.spawn',
    (m) => m.payload.unit.id === deMadera.unitId && m.payload.unit.movement, 15_000);
  comprobarVelocidad(salida.payload.unit.movement, indexarTerreno(propio.snapshot.payload.terrain), ciudad.civilization,
    'la caravana', 1_200);
  ok('caravana en camino', `${deMadera.unitId}: 50 de madera hacia ${ciudadV.name}, llega en ${segundosHasta(deMadera.arrivesAtMs)} s`);
  await llegada(victima.sesion, deMadera, ciudadV.id, 'WOOD', maderaV + 50);

  // El registro de quien ofreció lo cuenta, con sus cantidades (SPEC-TRADE §6).
  const pagina = await paginaDelRegistro(propio.sesion);
  const entrada = pagina.entries.find((e) => e.kind === 'TRADE_ACCEPTED' && e.otherPlayerId === cuenta.playerId);
  if (!entrada) fallar('el registro no cuenta el trato aceptado', pagina.entries.slice(0, 5));
  if (entrada.tradeGive?.[0]?.amount !== 50 || entrada.tradeGet?.[0]?.amount !== 10) {
    fallar('la entrada del trato no lleva sus cantidades', entrada);
  }
  ok('el registro cuenta el trato', `TRADE_ACCEPTED: diste 50 de madera, recibiste 10 de piedra`);
  const { entrada: llego, pagina: paginaV } = await entradaDelRegistro(victima.sesion,
    (e) => esLlegadaNueva(e, ciudad.ownerPlayerId, antesDelTrato, 'WOOD', 50));
  if (!llego) fallar('el registro de quien recibe no cuenta la caravana', paginaV.entries.slice(0, 5));
  ok('el registro cuenta la caravana', `CARAVAN_ARRIVED: 50 de madera de ${ciudad.name}`);
  victima.sesion.cerrar();
}

// ─────────────────────────────────────────────────────────────
// Caravanas (SPEC-CARAVANS), en --comercio, --bienes y --caravana
// ─────────────────────────────────────────────────────────────

/** Espera el caravan.update de una caravana en camino que cumpla `cond`. */
async function caravanaDe(sesion, cond, ms = 15_000) {
  return (await sesion.esperarQue('caravan.update',
    (m) => m.payload.caravan.status === 'IN_TRANSIT' && cond(m.payload.caravan), ms)).payload.caravan;
}

function segundosHasta(ms) {
  return ms === null ? '?' : Math.max(0, Math.round((ms - Date.now()) / 1000));
}

/**
 * Espera la llegada de una caravana (RN-CARAVAN-004): su caravan.update DELIVERED y
 * el almacén de su destino con `recurso` en `esperado`, cerca de su hora.
 */
async function llegada(sesion, c, ciudadId, recurso, esperado) {
  const margen = Math.max(0, (c.arrivesAtMs ?? Date.now()) - Date.now()) + 30_000;
  await sesion.esperarQue('caravan.update',
    (m) => m.payload.caravan.unitId === c.unitId && m.payload.caravan.status === 'DELIVERED', margen);
  const tarde = Date.now() - (c.arrivesAtMs ?? Date.now());
  const almacen = await sesion.esperarQue('city.update',
    (m) => m.payload.id === ciudadId && m.payload.stocks && m.payload.stocks[recurso] === esperado, 15_000);
  if (tarde > 5_000) fallar(`la caravana ${c.unitId} llegó ${tarde} ms tarde`, c);
  ok('la caravana llegó a su hora', `${c.toCityName}: ${recurso} -> ${almacen.payload.stocks[recurso]} (${tarde} ms después de lo anunciado)`);
}

async function caravana(propio, ciudad, playerId) {
  const { cuenta } = await abrirSesion(VICTIMA);
  const victima = await conectar(cuenta.ticket);
  const ciudadV = victima.snapshot.payload.cities.find((c) => c.ownerPlayerId === cuenta.playerId);
  if (!ciudadV) fallar('la víctima no tiene ciudad en su snapshot', victima.snapshot.payload.cities);
  const vigente = (victima.snapshot.payload.treaties ?? []).find((t) => t.otherCityId === ciudad.id && t.status === 'ACTIVE');
  if (!vigente) fallar(`${USUARIO} y ${VICTIMA} no tienen un tratado vigente`, 'Corre antes --comercio.');
  if (vigente.type !== 'TRADE') fallar(`el tratado vigente es ${vigente.type}: con paz no se captura nada`, vigente);
  const vieja = (victima.snapshot.payload.tradeOffers ?? []).find((o) => o.otherCityId === ciudad.id);
  if (vieja) {
    (vieja.proposedByMe ? victima.sesion : propio.sesion).enviar('trade.cancel', { offerId: vieja.id });
    await oferta(victima.sesion, (x) => x.id === vieja.id && x.status === 'WITHDRAWN');
  }
  if ((ciudadV.stocks?.STONE ?? 0) < 1) fallar(`${VICTIMA} no tiene piedra`, 'con el servidor parado: EO_DEVSEED_STOCK_FOR=smoke_victim pnpm run dev:seed');

  // Los guerreros de smoke_runner junto a la víctima, mirando su ciudad.
  propio.sesion.enviar('session.view', { center: { x: ciudadV.centerX, y: ciudadV.centerY } });
  const cerca = (await propio.sesion.esperar('world.snapshot')).payload;
  const guerreros = cerca.units.filter((u) => u.playerId === playerId && u.unitType === 'WARRIOR' && u.status !== 'GARRISONED');
  if (guerreros.length === 0) fallar(`${USUARIO} no tiene guerreros junto a la víctima`, 'Corre antes --saqueo o --asedio.');

  // Un trato pequeño: dos caravanas en camino.
  propio.sesion.enviar('trade.propose', {
    playerId: cuenta.playerId, give: [{ resource: 'WOOD', amount: 1 }], want: [{ resource: 'STONE', amount: 1 }],
  });
  const propia = await oferta(propio.sesion, (x) => x.otherPlayerId === cuenta.playerId && x.status === 'PROPOSED');
  victima.sesion.enviar('trade.respond', { offerId: propia.id, accept: true });
  const dePiedra = await caravanaDe(propio.sesion, (c) => c.senderPlayerId === cuenta.playerId);
  ok('dos caravanas en camino', `la de piedra de ${ciudadV.name} es la unidad ${dePiedra.unitId}`);

  // RN-CARAVAN-006: los guerreros la atacan y el golpe mortal la captura.
  for (const g of guerreros) propio.sesion.enviar('unit.attack', { unitId: g.id, targetUnitId: dePiedra.unitId });
  const capturada = await propio.sesion.esperarQue(['caravan.update', 'system.error'],
    (m) => m.type === 'system.error' ||
      (m.payload.caravan.unitId === dePiedra.unitId && m.payload.caravan.ownerPlayerId === playerId), 60_000);
  if (capturada.type === 'system.error') fallar('el servidor rechazó el ataque a la caravana', capturada.payload);
  const c = capturada.payload.caravan;
  if (c.toCityId !== ciudad.id || c.originalToCityId !== ciudad.id) {
    // La caravana iba a la ciudad de smoke_runner: capturarla no cambia su destino.
    fallar('la caravana capturada no va a la ciudad del captor', c);
  }
  if (c.cargo?.[0]?.resource !== 'STONE' || c.cargo[0].amount !== 1) fallar('la carga cambió al capturarla', c);
  ok('caravana capturada', `${guerreros.length} guerrero(s); ahora es de ${USUARIO} y va a ${ciudad.name} con su carga`);

  const aviso = await victima.sesion.esperarQue('event_log.notice',
    (m) => m.payload.kind === 'CARAVAN_CAPTURED' && m.payload.otherPlayerId === playerId, 15_000);
  ok('la víctima recibe el aviso', `CARAVAN_CAPTURED con ${aviso.payload.cargo?.[0]?.amount} de piedra`);
  await propio.sesion.esperarQue('caravan.update',
    (m) => m.payload.caravan.unitId === c.unitId && m.payload.caravan.status === 'DELIVERED',
    Math.max(0, (c.arrivesAtMs ?? Date.now()) - Date.now()) + 30_000);
  ok('la caravana capturada llega a la ciudad del captor', ciudad.name);

  const { entrada: captura, pagina } = await entradaDelRegistro(victima.sesion,
    (e) => e.kind === 'CARAVAN_CAPTURED' && e.otherPlayerId === playerId);
  if (!captura) fallar('el registro de la víctima no cuenta la captura', pagina.entries.slice(0, 5));
  ok('el registro de la víctima cuenta la captura', 'CARAVAN_CAPTURED');
  victima.sesion.cerrar();
}

// ─────────────────────────────────────────────────────────────
// Registro (SPEC-EVENT-LOG), sólo con --registro
// ─────────────────────────────────────────────────────────────

/** Pide una página del registro (la primera, o la anterior a beforeId) y la devuelve. */
async function paginaDelRegistro(sesion, beforeId) {
  const id = sesion.enviar('event_log.fetch', beforeId === undefined ? {} : { beforeId });
  const r = await sesion.esperarQue(['event_log.page', 'system.error'], (m) => m.requestId === id, 10_000);
  if (r.type === 'system.error') fallar('el servidor no sirvió el registro', r.payload);
  return r.payload;
}

/** El `id` más alto de una página del registro; 0 si está vacía. */
export function ultimoIdDelRegistro(pagina) {
  return pagina.entries.reduce((max, e) => Math.max(max, e.id), 0);
}

/**
 * Si una entrada del registro es la llegada de una caravana de `remitente` posterior a
 * `desdeId` y con `cantidad` de `recurso`. Las caravanas no se agrupan (SPEC-EVENT-LOG
 * §4.2): cada llegada es su entrada, con su `id`. Sin `desdeId`, una llegada vieja del
 * mismo remitente (la de --mercado, o la de una pasada anterior) cortaba la espera antes
 * de que se escribiera la nueva.
 */
export function esLlegadaNueva(entrada, remitente, desdeId, recurso, cantidad) {
  return entrada.kind === 'CARAVAN_ARRIVED' && entrada.otherPlayerId === remitente && entrada.id > desdeId
    && (entrada.cargo ?? []).some((c) => c.resource === recurso && c.amount === cantidad);
}

/**
 * Espera a que la primera página del registro tenga una entrada que cumpla `cond`.
 * Lo de una caravana se escribe de forma optimista (ADR-037 §4 y §5): el almacén y el
 * aviso llegan en el tick, y la fila de world_events unos milisegundos después.
 */
async function entradaDelRegistro(sesion, cond, ms = 5_000) {
  const limite = Date.now() + ms;
  for (;;) {
    const pagina = await paginaDelRegistro(sesion);
    const e = pagina.entries.find(cond);
    if (e || Date.now() > limite) return { entrada: e, pagina };
    await dormir(250);
  }
}

// ADR-028 §6: el servidor sólo marca lo guardado hace más de 25 s; uno de holgura.
const ASENTADO_MS = 26_000;

async function registro(propio, playerId) {
  const { cuenta, cookie } = await abrirSesion(VICTIMA);
  let victima = await conectar(cuenta.ticket);
  const pagina = await paginaDelRegistro(victima.sesion);
  const suyas = pagina.entries.filter((e) => e.otherPlayerId === playerId);
  if (suyas.length === 0) {
    fallar(`el registro de ${VICTIMA} no nombra a ${USUARIO}`,
      `Corre antes --saqueo, --asedio o --diplomacia (o varios en la misma pasada). Primeras entradas: ${JSON.stringify(pagina.entries.slice(0, 5), null, 2)}`);
  }
  const tipos = new Set(suyas.map((e) => e.kind));
  if (SAQUEO && !tipos.has('PILLAGED')) fallar('el registro de la víctima no tiene el saqueo', suyas);
  if (ASEDIO && !tipos.has('HOUSE_LOST')) fallar('el registro de la víctima no tiene la casa derribada', suyas);
  if (DIPLOMACIA && !tipos.has('TREATY_BREAK_ANNOUNCED')) fallar('el registro de la víctima no tiene la ruptura', suyas);
  ok(`la víctima ve lo que le hizo ${USUARIO}`, `${[...tipos].join(', ')} · «${suyas[0].otherCityName}»`);

  const propia = (await paginaDelRegistro(propio.sesion)).entries.filter((e) => e.otherPlayerId === cuenta.playerId);
  if (SAQUEO && !propia.some((e) => e.kind === 'PILLAGE_DONE')) fallar(`el registro de ${USUARIO} no tiene su saqueo`, propia);
  if (ASEDIO && !propia.some((e) => e.kind === 'HOUSE_DESTROYED')) fallar(`el registro de ${USUARIO} no tiene su derribo`, propia);
  if (DIPLOMACIA && !propia.some((e) => e.kind === 'TREATY_ACCEPTED')) fallar(`el registro de ${USUARIO} no tiene la aceptación`, propia);
  if (propia.length > 0) ok(`${USUARIO} ve su lado`, [...new Set(propia.map((e) => e.kind))].join(', '));

  // Lo visto sólo cubre lo asentado: se espera a que lo más reciente lo esté y se
  // pide otra página, que es la que le da a la sesión su tope marcable.
  let fresca = pagina;
  for (;;) {
    const espera = fresca.entries[0].lastAtMs + ASENTADO_MS - Date.now();
    if (espera <= 0) break;
    ok('esperando a que lo más reciente se asiente', `${Math.ceil(espera / 1000)} s`);
    await dormir(espera);
    fresca = await paginaDelRegistro(victima.sesion);
  }
  if (fresca === pagina) fresca = await paginaDelRegistro(victima.sesion);
  const hasta = fresca.entries[0].id;
  victima.sesion.enviar('event_log.seen', { upToId: hasta });
  await dormir(500);
  victima.sesion.cerrar();

  // Una recarga: ticket nuevo con la cookie y otra conexión.
  const { ticket } = await ticketConCookie(cookie);
  victima = await conectar(ticket);
  const despues = await paginaDelRegistro(victima.sesion);
  if (despues.seenUpToId < hasta) {
    fallar('al reconectar, lo visto no quedó marcado', { hasta, seenUpToId: despues.seenUpToId });
  }
  const nuevas = despues.entries.filter((e) => e.id > despues.seenUpToId).length;
  ok('lo visto sigue marcado al reconectar', `cursor ${despues.seenUpToId} ≥ ${hasta} · ${nuevas} entradas nuevas`);
  victima.sesion.cerrar();
}

// ─────────────────────────────────────────────────────────────
// Mensajes entre jugadores (SPEC-MESSAGES), sólo con --mensajes
// ─────────────────────────────────────────────────────────────

/** Envía un mensaje y espera su message.new o su rechazo, por el requestId. */
async function escribirA(sesion, toPlayerId, body) {
  const id = sesion.enviar('message.send', { toPlayerId, body });
  return sesion.esperarQue(['message.new', 'system.error'], (m) => m.requestId === id, 10_000);
}

/** Pide la bandeja (o bloquea, que también responde con ella). */
async function bandeja(sesion, tipo = 'message.list', payload = {}) {
  const id = sesion.enviar(tipo, payload);
  const r = await sesion.esperarQue(['message.inbox', 'system.error'], (m) => m.requestId === id, 10_000);
  if (r.type === 'system.error') fallar(`el servidor no sirvió la bandeja (${tipo})`, r.payload);
  return r.payload;
}

async function mensajes(propio, playerId, ciudad) {
  const { cuenta, cookie } = await abrirSesion(VICTIMA);
  let victima = await conectar(cuenta.ticket);
  const victimaId = cuenta.playerId;
  // De una pasada anterior que se cortara a medias, el bloqueo podría seguir puesto.
  await bandeja(victima.sesion, 'message.block', { playerId, blocked: false });

  // 1. En vivo: llega a la víctima con el remitente nombrado por su ciudad.
  const texto = `  ¿60 de vino por 200 de piedra? (humo ${new Date().toISOString()})\n`;
  const enviado = await escribirA(propio.sesion, victimaId, texto);
  if (enviado.type === 'system.error') fallar('el envío fue rechazado', enviado.payload);
  const m1 = enviado.payload.message;
  if (m1.body !== texto.trim()) fallar('el servidor no recortó el texto', { enviado: texto, guardado: m1.body });
  const enVivo = await victima.sesion.esperarQue(['message.new'], (m) => m.payload.message.id === m1.id, 10_000);
  const visto = enVivo.payload.message;
  if (visto.otherPlayerId !== playerId || visto.otherCityName !== ciudad.name) {
    fallar('la víctima no ve al remitente por su ciudad', { visto, ciudad: ciudad.name });
  }
  ok('un mensaje llega en vivo al otro jugador', `id ${m1.id} · de «${visto.otherCityName}»`);

  // 2. Desconectada: el segundo le espera en la bandeja como no leído.
  victima.sesion.cerrar();
  await dormir(300);
  const segundo = await escribirA(propio.sesion, victimaId, 'Contesta cuando vuelvas.');
  if (segundo.type === 'system.error') fallar('el segundo envío fue rechazado', segundo.payload);
  const m2 = segundo.payload.message;
  const { ticket } = await ticketConCookie(cookie);
  victima = await conectar(ticket);
  let inbox = await bandeja(victima.sesion);
  let conv = inbox.conversations.find((c) => c.otherPlayerId === playerId);
  if (!conv || conv.unread < 1 || conv.lastFromMe) fallar('al volver, el mensaje no está en la bandeja como no leído', inbox);
  ok('al volver, el mensaje espera en la bandeja', `${conv.unread} sin leer · «${conv.lastBody}»`);

  const pid = victima.sesion.enviar('message.fetch', { otherPlayerId: playerId });
  const pagina = await victima.sesion.esperarQue(['message.page', 'system.error'], (m) => m.requestId === pid, 10_000);
  if (pagina.type === 'system.error') fallar('no se sirvió la conversación', pagina.payload);
  const ids = pagina.payload.messages.map((m) => m.id);
  if (!ids.includes(m1.id) || ids.at(-1) !== m2.id) fallar('la conversación no tiene los dos mensajes en orden', ids);
  victima.sesion.enviar('message.read', { otherPlayerId: playerId, upToId: m2.id });
  await dormir(300);
  inbox = await bandeja(victima.sesion);
  conv = inbox.conversations.find((c) => c.otherPlayerId === playerId);
  if (!conv || conv.unread !== 0) fallar('leída la conversación, sigue con no leídos', conv);
  ok('leer la conversación la deja sin no leídos', `${ids.length} mensajes en la página`);

  // 3. Bloqueo: ninguno de los dos escribe al otro; al desbloquear, vuelve a llegar.
  inbox = await bandeja(victima.sesion, 'message.block', { playerId, blocked: true });
  if (!inbox.blocked.some((b) => b.playerId === playerId)) fallar('el bloqueo no sale en la bandeja', inbox.blocked);
  const rechazado = await escribirA(propio.sesion, victimaId, 'esto no debe llegar');
  if (rechazado.type !== 'system.error' || rechazado.payload.code !== 'INVALID_TARGET') {
    fallar('con el bloqueo, el mensaje no se rechazó con INVALID_TARGET', rechazado);
  }
  const alReves = await escribirA(victima.sesion, playerId, 'tampoco esto');
  if (alReves.type !== 'system.error' || alReves.payload.code !== 'INVALID_TARGET') {
    fallar('quien bloquea tampoco debería poder escribir', alReves);
  }
  ok('con un bloqueo nadie escribe a nadie', `«${rechazado.payload.message}» · «${alReves.payload.message}»`);
  await bandeja(victima.sesion, 'message.block', { playerId, blocked: false });
  const otraVez = await escribirA(propio.sesion, victimaId, 'Desbloqueado: ¿hablamos?');
  if (otraVez.type === 'system.error') fallar('al desbloquear, el envío sigue rechazado', otraVez.payload);
  await victima.sesion.esperarQue(['message.new'], (m) => m.payload.message.id === otraVez.payload.message.id, 10_000);
  ok('al desbloquear, los mensajes vuelven a llegar');
  victima.sesion.cerrar();
}

// ─────────────────────────────────────────────────────────────
// Facciones (SPEC-FACTIONS), sólo con --faccion
// ─────────────────────────────────────────────────────────────

const ORDEN = process.env.EO_SMOKE_ORDER_USER || 'smoke_orden';
const CAOS = process.env.EO_SMOKE_CHAOS_USER || 'smoke_caos';

/**
 * Lo que tiene que hacer el humo con una cuenta y el bando al que debe entrar:
 * pedir la entrada, esperarla o nada (ya está dentro). Una cuenta del bando
 * contrario o con otra entrada es un fallo: las cuentas del humo no cambian de bando.
 */
export function pasoDeFaccion(ciudad, bando) {
  const actual = ciudad.faction ?? 'NEUTRAL';
  if (actual === bando) return 'dentro';
  if (actual !== 'NEUTRAL') return 'otro-bando';
  if (!ciudad.factionJoin) return 'pedir';
  return ciudad.factionJoin.faction === bando ? 'esperar' : 'otro-bando';
}

/** Entra (o comprueba que ya entró) en un bando; devuelve la sesión y el jugador. */
async function entrarEnBando(usuario, bando) {
  const { cuenta } = await abrirSesion(usuario);
  const s = await conectar(cuenta.ticket);
  const propia = s.snapshot.payload.cities.find((c) => c.ownerPlayerId === cuenta.playerId);
  if (!propia) fallar(`el snapshot de ${usuario} no trae su ciudad`, s.snapshot.payload.cities.map((c) => c.id));
  let paso = pasoDeFaccion(propia, bando);
  if (paso === 'otro-bando') fallar(`${usuario} ya es de otro bando o se está uniendo a otro`, propia);
  let atMs = propia.factionJoin?.atMs;
  if (paso === 'pedir') {
    const id = s.sesion.enviar('faction.join', { faction: bando });
    const r = await s.sesion.esperarQue(['city.update', 'system.error'], (m) => m.requestId === id, 10_000);
    if (r.type === 'system.error') fallar(`la entrada de ${usuario} en ${bando} fue rechazada`, r.payload);
    if (!r.payload.factionJoin || r.payload.factionJoin.faction !== bando) fallar('la entrada no volvió en city.update', r.payload);
    atMs = r.payload.factionJoin.atMs;
    ok(`${usuario} pide entrar en ${bando}`, `cuenta en ${Math.round((atMs - Date.now()) / 1000)} s`);
    paso = 'esperar';
  }
  if (paso === 'esperar') {
    const espera = atMs - Date.now();
    if (espera > 120_000) {
      fallar(`el preaviso de ${usuario} dura ${Math.round(espera / 1000)} s`,
        'Arranca el servidor con EO_FACTION_JOIN_SECONDS=20 delante para el humo de facciones.');
    }
    await s.sesion.esperarQue('city.update', (m) => m.payload.id === propia.id && m.payload.faction === bando,
      Math.max(0, espera) + 15_000);
    ok(`la entrada de ${usuario} en ${bando} cuenta al vencer el preaviso`);
  } else {
    ok(`${usuario} ya es de ${bando}`, 'de una pasada anterior');
  }
  const otra = s.sesion.enviar('faction.join', { faction: bando === 'ORDER' ? 'CHAOS' : 'ORDER' });
  const r = await s.sesion.esperarQue(['city.update', 'system.error'], (m) => m.requestId === otra, 10_000);
  if (r.type !== 'system.error' || r.payload.code !== 'INVALID_TARGET') fallar('una segunda entrada no se rechazó', r);
  ok(`${usuario} no puede cambiar de bando`, `«${r.payload.message}»`);
  return { ...s, playerId: cuenta.playerId };
}

async function faccion() {
  const orden = await entrarEnBando(ORDEN, 'ORDER');
  const caos = await entrarEnBando(CAOS, 'CHAOS');

  // RN-FAC-005: pacto y alianza no; comercio sí.
  for (const type of ['NON_AGGRESSION', 'ALLIANCE']) {
    const id = orden.sesion.enviar('treaty.propose', { playerId: caos.playerId, type });
    const r = await orden.sesion.esperarQue(['treaty.update', 'system.error'], (m) => m.requestId === id, 10_000);
    if (r.type !== 'system.error' || r.payload.code !== 'INVALID_TARGET') fallar(`un ${type} entre la Orden y el Caos no se rechazó`, r);
    ok(`la Orden y el Caos no firman ${type}`, `«${r.payload.message}»`);
  }
  const id = orden.sesion.enviar('treaty.propose', { playerId: caos.playerId, type: 'TRADE' });
  const r = await orden.sesion.esperarQue(['treaty.update', 'system.error'], (m) => m.requestId === id, 10_000);
  if (r.type === 'system.error') fallar('un trato de comercio entre la Orden y el Caos se rechazó', r.payload);
  const retirar = orden.sesion.enviar('treaty.cancel', { treatyId: r.payload.treaty.id });
  const w = await orden.sesion.esperarQue(['treaty.update', 'system.error'], (m) => m.requestId === retirar, 10_000);
  if (w.type === 'system.error') fallar('no se pudo retirar la oferta de comercio', w.payload);
  ok('el comercio entre la Orden y el Caos sí se propone', `oferta ${r.payload.treaty.id}, retirada`);
  orden.sesion.cerrar();
  caos.sesion.cerrar();
}

// ─────────────────────────────────────────────────────────────
// Clanes (SPEC-CLANS), sólo con --clan
// ─────────────────────────────────────────────────────────────

const CLAN_A = process.env.EO_SMOKE_CLAN_LEADER || 'smoke_clan_a';
const CLAN_B = process.env.EO_SMOKE_CLAN_MEMBER || 'smoke_clan_b';
const CLAN_NOMBRE = 'Humo';
const CLAN_SIGLA = 'HUM';
const TERRITORIO_CLAN = args.includes('--territorio-clan');

/**
 * Qué tiene que hacer el humo con el líder según su clan.update de la conexión:
 * fundar el clan, nada (ya es su líder) o fallar (está en otro clan, o no lo dirige).
 */
export function pasoDeClan(update, playerId) {
  if (!update.clan) return 'fundar';
  if (update.clan.tag !== CLAN_SIGLA) return 'otro-clan';
  return update.clan.leaderId === playerId ? 'lider' : 'no-lider';
}

/** Entra con una cuenta y devuelve su sesión, su jugador y su clan.update inicial. */
async function entrarConClan(usuario) {
  const { cuenta } = await abrirSesion(usuario);
  const s = await conectar(cuenta.ticket);
  const update = await s.sesion.esperar('clan.update');
  return { ...s, playerId: cuenta.playerId, clan: update.payload };
}

/** Manda un comando y espera su respuesta (con su requestId) o su rechazo. */
async function ordenDeClan(sesion, tipo, payload, respuestas = ['clan.update']) {
  const id = sesion.enviar(tipo, payload);
  return sesion.esperarQue([...respuestas, 'system.error'], (m) => m.requestId === id, 10_000);
}

async function clanes() {
  const a = await entrarConClan(CLAN_A);
  const b = await entrarConClan(CLAN_B);

  // El líder: funda el clan o ya lo dirige de una pasada anterior.
  let clan;
  switch (pasoDeClan(a.clan, a.playerId)) {
    case 'fundar': {
      const r = await ordenDeClan(a.sesion, 'clan.create', { name: CLAN_NOMBRE, tag: CLAN_SIGLA });
      if (r.type === 'system.error') fallar(`${CLAN_A} no pudo fundar el clan`, r.payload);
      clan = r.payload.clan;
      if (!clan || clan.leaderId !== a.playerId) fallar('el clan fundado no vuelve con su líder', r.payload);
      ok(`${CLAN_A} funda el clan [${CLAN_SIGLA}]`, `clan ${clan.id}`);
      const ciudad = await a.sesion.esperarQue('city.update', (m) => m.payload.clan?.tag === CLAN_SIGLA, 5_000);
      ok('la ciudad del fundador lleva la sigla', `ciudad ${ciudad.payload.id}`);
      break;
    }
    case 'lider':
      clan = a.clan.clan;
      ok(`${CLAN_A} ya dirige el clan [${CLAN_SIGLA}]`, 'de una pasada anterior');
      break;
    default:
      fallar(`${CLAN_A} está en otro clan o no lo dirige`, a.clan);
  }

  // La regla de bandos (RN-CLAN-006), si --faccion ya metió a las cuentas en su bando.
  const orden = await entrarConClan(ORDEN);
  const caos = await entrarConClan(CAOS);
  const bandoDe = (s) => s.snapshot.payload.cities.find((c) => c.ownerPlayerId === s.playerId)?.faction;
  let ordenDentro = orden.clan.clan?.id === clan.id;
  if (bandoDe(orden) === 'ORDER' && bandoDe(caos) === 'CHAOS') {
    if (!ordenDentro) {
      if (orden.clan.clan) fallar(`${ORDEN} está en otro clan`, orden.clan);
      if (!orden.clan.invitations.some((i) => i.clanId === clan.id)) {
        const r = await ordenDeClan(a.sesion, 'clan.invite', { playerId: orden.playerId, invited: true });
        if (r.type === 'system.error') fallar(`la invitación a ${ORDEN} fue rechazada`, r.payload);
        await orden.sesion.esperarQue('clan.update', (m) => m.payload.invitations.some((i) => i.clanId === clan.id), 10_000);
      }
      const r = await ordenDeClan(orden.sesion, 'clan.respond', { clanId: clan.id, accept: true });
      if (r.type === 'system.error') fallar(`${ORDEN} no pudo entrar en el clan`, r.payload);
      ok(`${ORDEN} entra en el clan`);
      ordenDentro = true;
    }
    const r = await ordenDeClan(a.sesion, 'clan.invite', { playerId: caos.playerId, invited: true });
    if (r.type !== 'system.error' || r.payload.code !== 'INVALID_TARGET') fallar('invitar al Caos a un clan con la Orden no se rechazó', r);
    ok('un clan no junta a la Orden y al Caos', `«${r.payload.message}»`);
  } else {
    ok('la regla de bandos no se comprueba', `pasa antes --faccion para meter a ${ORDEN} y ${CAOS} en su bando`);
  }

  // El gobierno del clan (SPEC-CLAN-GOVERNANCE): smoke_orden, oficial, invita y
  // expulsa a smoke_clan_b; sin smoke_orden dentro, lo hace el líder.
  let gestor = a;
  if (ordenDentro && rolEnClan(a.clan, orden.playerId) === 'OFFICER') {
    ok(`${ORDEN} ya es oficial`, 'de una pasada que no terminó');
    gestor = orden;
  } else if (ordenDentro) {
    const nombrado = await ordenDeClan(a.sesion, 'clan.promote', { playerId: orden.playerId, officer: true });
    if (nombrado.type === 'system.error') fallar(`${CLAN_A} no pudo nombrar oficial a ${ORDEN}`, nombrado.payload);
    if (rolEnClan(nombrado.payload, orden.playerId) !== 'OFFICER') fallar('el nombramiento no vuelve con el rango', nombrado.payload);
    await orden.sesion.esperarQue('clan.update', (m) => rolEnClan(m.payload, orden.playerId) === 'OFFICER', 10_000);
    ok(`${CLAN_A} nombra oficial a ${ORDEN}`, 'los dos lo ven en su clan');
    gestor = orden;
  } else {
    ok('el oficial no se comprueba', `${ORDEN} no está en el clan: pasa antes --faccion`);
  }
  const quienGestiona = gestor === a ? CLAN_A : ORDEN;

  // smoke_clan_b fuera (la pasada anterior lo expulsó), invitado y dentro.
  if (b.clan.clan) {
    const r = await ordenDeClan(b.sesion, 'clan.leave', {});
    if (r.type === 'system.error') fallar(`${CLAN_B} no pudo salir de su clan`, r.payload);
    ok(`${CLAN_B} sale del clan que le quedó de una pasada anterior`);
  }
  if (!b.clan.invitations.some((i) => i.clanId === clan.id)) {
    const r = await ordenDeClan(gestor.sesion, 'clan.invite', { playerId: b.playerId, invited: true });
    if (r.type === 'system.error') fallar(`la invitación a ${CLAN_B} fue rechazada`, r.payload);
    await b.sesion.esperarQue('clan.update', (m) => m.payload.invitations.some((i) => i.clanId === clan.id), 10_000);
    ok(`${quienGestiona} invita a ${CLAN_B}`, 'la invitación le llega en vivo');
  }
  const dentro = await ordenDeClan(b.sesion, 'clan.respond', { clanId: clan.id, accept: true });
  if (dentro.type === 'system.error') fallar(`${CLAN_B} no pudo aceptar la invitación`, dentro.payload);
  if (!dentro.payload.clan?.members.some((m) => m.playerId === b.playerId)) fallar('la entrada no vuelve con el clan', dentro.payload);
  ok(`${CLAN_B} acepta y entra`, `${dentro.payload.clan.members.length} miembros`);

  // El canal: llega en vivo al otro, y su página lo trae.
  const texto = `humo ${Date.now()}`;
  const enviado = await ordenDeClan(a.sesion, 'clan.chat.send', { body: texto }, ['clan.chat.new']);
  if (enviado.type === 'system.error') fallar('el mensaje del canal fue rechazado', enviado.payload);
  const recibido = await b.sesion.esperarQue('clan.chat.new', (m) => m.payload.message.body === texto, 10_000);
  ok('un mensaje del canal llega en vivo al otro miembro', `mensaje ${recibido.payload.message.id}`);
  const pagina = await ordenDeClan(b.sesion, 'clan.chat.fetch', {}, ['clan.chat.page']);
  if (pagina.type === 'system.error' || !pagina.payload.messages.some((m) => m.body === texto)) {
    fallar('la página del canal no trae el mensaje', pagina.payload);
  }
  ok('la página del canal lo trae', `${pagina.payload.messages.length} mensajes`);

  // La vista pública, para cualquiera.
  const info = await ordenDeClan(caos.sesion, 'clan.lookup', { clanId: clan.id }, ['clan.info']);
  if (info.type === 'system.error' || info.payload.tag !== CLAN_SIGLA) fallar('la vista pública del clan no llegó', info.payload);
  ok('cualquiera ve los miembros del clan', `${info.payload.members.length} miembros`);

  // El traspaso de ida y vuelta: --territorio-clan sigue encontrando a
  // smoke_clan_a de líder.
  if (ordenDentro) {
    const ida = await ordenDeClan(a.sesion, 'clan.transfer', { playerId: orden.playerId });
    if (ida.type === 'system.error') fallar(`${CLAN_A} no pudo ceder el liderazgo`, ida.payload);
    if (ida.payload.clan?.leaderId !== orden.playerId || rolEnClan(ida.payload, a.playerId) !== 'OFFICER') {
      fallar('el traspaso no deja al nuevo líder y al anterior de oficial', ida.payload);
    }
    const recibe = await orden.sesion.esperarQue('event_log.notice', (m) => m.payload.kind === 'CLAN_LEADERSHIP_RECEIVED', 10_000);
    if (recibe.payload.clanTag !== CLAN_SIGLA) fallar('el aviso del liderazgo no trae la sigla', recibe.payload);
    ok(`${CLAN_A} cede el liderazgo a ${ORDEN}`, `aviso con [${recibe.payload.clanTag}]`);
    const vuelta = await ordenDeClan(orden.sesion, 'clan.transfer', { playerId: a.playerId });
    if (vuelta.type === 'system.error') fallar(`${ORDEN} no pudo devolver el liderazgo`, vuelta.payload);
    if (vuelta.payload.clan?.leaderId !== a.playerId) fallar('el liderazgo no volvió', vuelta.payload);
    await a.sesion.esperarQue('event_log.notice', (m) => m.payload.kind === 'CLAN_LEADERSHIP_RECEIVED', 10_000);
    ok(`${ORDEN} se lo devuelve`, `${CLAN_A} vuelve a liderar [${CLAN_SIGLA}]`);
  }

  // La expulsión, con su aviso; deja a smoke_clan_b fuera para la pasada siguiente.
  const fuera = await ordenDeClan(gestor.sesion, 'clan.expel', { playerId: b.playerId });
  if (fuera.type === 'system.error') fallar(`${quienGestiona} no pudo expulsar a ${CLAN_B}`, fuera.payload);
  const aviso = await b.sesion.esperarQue('event_log.notice', (m) => m.payload.kind === 'CLAN_EXPELLED', 10_000);
  ok(`${CLAN_B} recibe el aviso de su expulsión`, `de ${aviso.payload.otherCityName}`);

  // smoke_orden vuelve a ser miembro raso: la pasada siguiente lo nombra otra vez.
  if (ordenDentro) {
    const quitado = await ordenDeClan(a.sesion, 'clan.promote', { playerId: orden.playerId, officer: false });
    if (quitado.type === 'system.error') fallar(`${CLAN_A} no pudo quitar el rango a ${ORDEN}`, quitado.payload);
    if (rolEnClan(quitado.payload, orden.playerId) !== 'MEMBER') fallar('quitar el rango no vuelve con el clan', quitado.payload);
    ok(`${CLAN_A} le quita el rango de oficial a ${ORDEN}`);
  }

  for (const s of [a, b, orden, caos]) s.sesion.cerrar();
}

/** El rango de un jugador en el clan de un clan.update, o null (SPEC-CLAN-GOVERNANCE). */
export function rolEnClan(update, playerId) {
  return update?.clan?.members?.find((m) => m.playerId === playerId)?.role ?? null;
}

// ─────────────────────────────────────────────────────────────
// Alianzas entre clanes (SPEC-CLAN-ALLIANCES), sólo con --alianza-clan
// ─────────────────────────────────────────────────────────────

const ALIADO_NOMBRE = 'Eco';
const ALIADO_SIGLA = 'ECO';

/**
 * El texto del rechazo de un ataque entre aliados, o null si no es uno de los que valen:
 * el de la alianza o, si la paz residual de una expulsión reciente cuenta antes, el del
 * clan (RN-CLAN-005).
 */
export function motivoDelAtaqueEntreAliados(error) {
  if (error?.code !== 'INVALID_TARGET') return null;
  if (error.message === 'vuestros clanes están aliados') return error.message;
  if (error.message === 'sois del mismo clan') return 'aún cuenta la paz residual de la expulsión de --clan';
  return null;
}

/** La alianza (o la oferta) de un clan.update con el clan de sigla `tag`, o null. */
export function alianzaCon(update, tag) {
  return update?.clan?.alliances?.find((a) => a.tag === tag) ?? null;
}

async function alianzaDeClan() {
  const a = await entrarConClan(CLAN_A);
  const b = await entrarConClan(CLAN_B);
  if (pasoDeClan(a.clan, a.playerId) !== 'lider') fallar(`${CLAN_A} no dirige [${CLAN_SIGLA}]: pasa antes --clan`, a.clan);
  const hum = a.clan.clan;

  // smoke_clan_b sin clan; si le quedó uno ([ECO] de una pasada a medias), sale y lo disuelve.
  if (b.clan.clan) {
    const r = await ordenDeClan(b.sesion, 'clan.leave', {});
    if (r.type === 'system.error') fallar(`${CLAN_B} no pudo salir de su clan`, r.payload);
    ok(`${CLAN_B} sale del clan que le quedó de una pasada anterior`);
  }
  const f = await ordenDeClan(b.sesion, 'clan.create', { name: ALIADO_NOMBRE, tag: ALIADO_SIGLA });
  if (f.type === 'system.error') fallar(`${CLAN_B} no pudo fundar [${ALIADO_SIGLA}]`, f.payload);
  const eco = f.payload.clan;
  ok(`${CLAN_B} funda «${ALIADO_NOMBRE}» [${ALIADO_SIGLA}]`, `clan ${eco.id}`);

  // La oferta, con su aviso al otro líder.
  const p = await ordenDeClan(a.sesion, 'clan.alliance.propose', { clanId: eco.id });
  if (p.type === 'system.error') fallar(`${CLAN_A} no pudo proponer la alianza`, p.payload);
  const oferta = alianzaCon(p.payload, ALIADO_SIGLA);
  if (oferta?.status !== 'PROPOSED' || !oferta.proposedByUs) fallar('la oferta no vuelve en el clan de quien la propone', p.payload);
  ok(`${CLAN_A} propone una alianza a [${ALIADO_SIGLA}]`, `alianza ${oferta.id}`);
  const avisoOferta = await b.sesion.esperarQue('event_log.notice', (m) => m.payload.kind === 'CLAN_ALLIANCE_OFFERED', 10_000);
  if (avisoOferta.payload.clanTag !== CLAN_SIGLA) fallar('el aviso de la oferta no trae la sigla', avisoOferta.payload);
  ok(`${CLAN_B} recibe el aviso de la oferta`, `de [${avisoOferta.payload.clanTag}]`);

  // La aceptación, con su aviso al que propuso.
  const acepta = await ordenDeClan(b.sesion, 'clan.alliance.respond', { allianceId: oferta.id, accept: true });
  if (acepta.type === 'system.error') fallar(`${CLAN_B} no pudo aceptar la alianza`, acepta.payload);
  if (alianzaCon(acepta.payload, CLAN_SIGLA)?.status !== 'ACTIVE') fallar('la aceptación no deja la alianza activa', acepta.payload);
  ok(`${CLAN_B} la acepta`, `[${ALIADO_SIGLA}] y [${CLAN_SIGLA}] están aliados`);
  const avisoAcepta = await a.sesion.esperarQue('event_log.notice', (m) => m.payload.kind === 'CLAN_ALLIANCE_ACCEPTED', 10_000);
  if (avisoAcepta.payload.clanTag !== ALIADO_SIGLA) fallar('el aviso de la aceptación no trae la sigla', avisoAcepta.payload);
  ok(`${CLAN_A} recibe el aviso de la aceptación`, `de [${avisoAcepta.payload.clanTag}]`);

  // La vista pública lleva la alianza.
  const info = await ordenDeClan(b.sesion, 'clan.lookup', { clanId: hum.id }, ['clan.info']);
  if (info.type === 'system.error' || !info.payload.alliances?.some((x) => x.tag === ALIADO_SIGLA)) {
    fallar(`la vista pública de [${CLAN_SIGLA}] no lleva la alianza`, info.payload);
  }
  ok(`la vista pública de [${CLAN_SIGLA}] lleva su alianza`, info.payload.alliances.map((x) => `[${x.tag}]`).join(', '));

  // La paz: el guerrero de smoke_clan_a no ataca a un aldeano de smoke_clan_b.
  const guerrero = a.snapshot.payload.units.find((u) => u.playerId === a.playerId && u.unitType === 'WARRIOR');
  const aldeano = b.snapshot.payload.units.find((u) => u.playerId === b.playerId && u.unitType === 'VILLAGER');
  if (!guerrero || !aldeano) fallar('no hay guerrero de smoke_clan_a o aldeano de smoke_clan_b para probar la paz');
  // Si --clan acaba de expulsar a smoke_clan_b de [HUM], su paz residual cuenta antes que
  // la de la alianza y el motivo es el del clan; con EO_CLAN_PEACE_GRACE_SECONDS corto ya
  // venció y el motivo es el de la alianza.
  const ataque = await ordenDeClan(a.sesion, 'unit.attack', { unitId: guerrero.id, targetUnitId: aldeano.id }, []);
  const motivo = motivoDelAtaqueEntreAliados(ataque.payload);
  if (motivo === null) fallar('el ataque entre aliados no se rechaza con su motivo', ataque.payload);
  ok('entre aliados no hay ataque', motivo);

  // La ruptura: el preaviso, con su aviso a los miembros del otro clan.
  const rompe = await ordenDeClan(a.sesion, 'clan.alliance.cancel', { allianceId: oferta.id });
  if (rompe.type === 'system.error') fallar(`${CLAN_A} no pudo romper la alianza`, rompe.payload);
  if (alianzaCon(rompe.payload, ALIADO_SIGLA)?.breaksAtMs == null) fallar('la ruptura no abre el preaviso', rompe.payload);
  const avisoRuptura = await b.sesion.esperarQue('event_log.notice', (m) => m.payload.kind === 'CLAN_ALLIANCE_BREAK_ANNOUNCED', 10_000);
  if (avisoRuptura.payload.clanTag !== CLAN_SIGLA) fallar('el aviso del preaviso no trae la sigla', avisoRuptura.payload);
  ok(`${CLAN_A} la rompe`, `${CLAN_B} recibe el aviso del preaviso con [${avisoRuptura.payload.clanTag}]`);

  // La disolución de [ECO] expira la alianza; [HUM] lo ve.
  const sale = await ordenDeClan(b.sesion, 'clan.leave', {});
  if (sale.type === 'system.error') fallar(`${CLAN_B} no pudo salir de [${ALIADO_SIGLA}]`, sale.payload);
  await a.sesion.esperarQue('clan.update', (m) => m.payload.clan && !alianzaCon(m.payload, ALIADO_SIGLA), 10_000);
  ok(`${CLAN_B} sale de [${ALIADO_SIGLA}], que se disuelve`, `la alianza expira y [${CLAN_SIGLA}] la ve desaparecer`);

  // El registro del que propuso cuenta la aceptación (se escribe tras el COMMIT).
  const { entrada } = await entradaDelRegistro(a.sesion, (e) => e.kind === 'CLAN_ALLIANCE_ACCEPTED' && e.clanTag === ALIADO_SIGLA);
  if (!entrada) fallar('el registro de quien propuso no cuenta la aceptación');
  ok('el registro de quien propuso cuenta la aceptación', `entrada ${entrada.id}, «${entrada.otherCityName}»`);

  for (const s of [a, b]) s.sesion.cerrar();
}

// ─────────────────────────────────────────────────────────────
// Guerras entre clanes (SPEC-CLAN-WARS), sólo con --guerra-clan
// ─────────────────────────────────────────────────────────────

const RIVAL_NOMBRE = 'Rivales';
const RIVAL_SIGLA = 'RIV';

/** La guerra abierta de un clan.update con el clan de sigla `tag`, o null. */
export function guerraCon(update, tag) {
  return update?.clan?.wars?.find((w) => w.tag === tag) ?? null;
}

/** Cuánto esperar a que empiece una guerra: hasta su comienzo y un margen para el barrido. */
export function esperaDelComienzo(guerra, ahoraMs) {
  return Math.max(0, guerra.startsAtMs - ahoraMs) + 15_000;
}

async function guerraDeClan() {
  const a = await entrarConClan(CLAN_A);
  const b = await entrarConClan(CLAN_B);
  if (pasoDeClan(a.clan, a.playerId) !== 'lider') fallar(`${CLAN_A} no dirige [${CLAN_SIGLA}]: pasa antes --clan`, a.clan);
  const hum = a.clan.clan;

  // smoke_clan_b sin clan; si le quedó uno ([RIV] de una pasada a medias), sale y lo disuelve.
  if (b.clan.clan) {
    const r = await ordenDeClan(b.sesion, 'clan.leave', {});
    if (r.type === 'system.error') fallar(`${CLAN_B} no pudo salir de su clan`, r.payload);
    ok(`${CLAN_B} sale del clan que le quedó de una pasada anterior`);
  }
  const f = await ordenDeClan(b.sesion, 'clan.create', { name: RIVAL_NOMBRE, tag: RIVAL_SIGLA });
  if (f.type === 'system.error') fallar(`${CLAN_B} no pudo fundar [${RIVAL_SIGLA}]`, f.payload);
  const riv = f.payload.clan;
  ok(`${CLAN_B} funda «${RIVAL_NOMBRE}» [${RIVAL_SIGLA}]`, `clan ${riv.id}`);

  // La declaración, con su aviso a los miembros del otro clan.
  const d = await ordenDeClan(a.sesion, 'clan.war.declare', { clanId: riv.id });
  if (d.type === 'system.error') fallar(`${CLAN_A} no pudo declarar la guerra`, d.payload);
  let guerra = guerraCon(d.payload, RIVAL_SIGLA);
  if (guerra?.status !== 'DECLARED' || !guerra.declaredByUs || guerra.endsAtMs !== null) {
    fallar('la declaración no vuelve en el clan de quien la declara, en preparación', d.payload);
  }
  ok(`${CLAN_A} declara la guerra a [${RIVAL_SIGLA}]`, `guerra ${guerra.id}, empieza en ${Math.round((guerra.startsAtMs - Date.now()) / 1000)} s`);
  const aviso = await b.sesion.esperarQue('event_log.notice', (m) => m.payload.kind === 'CLAN_WAR_DECLARED', 10_000);
  if (aviso.payload.clanTag !== CLAN_SIGLA || aviso.payload.startsAtMs !== guerra.startsAtMs) {
    fallar('el aviso de la declaración no trae la sigla ni el comienzo', aviso.payload);
  }
  ok(`${CLAN_B} recibe el aviso de la declaración`, `de [${aviso.payload.clanTag}]`);

  // La vista pública lleva la guerra.
  const info = await ordenDeClan(b.sesion, 'clan.lookup', { clanId: hum.id }, ['clan.info']);
  if (info.type === 'system.error' || !info.payload.wars?.some((w) => w.tag === RIVAL_SIGLA)) {
    fallar(`la vista pública de [${CLAN_SIGLA}] no lleva la guerra`, info.payload);
  }
  ok(`la vista pública de [${CLAN_SIGLA}] lleva su guerra`, info.payload.wars.map((w) => `[${w.tag}] ${w.status}`).join(', '));

  // El comienzo, al terminar la preparación.
  const empieza = await a.sesion.esperarQue('clan.update', (m) => guerraCon(m.payload, RIVAL_SIGLA)?.status === 'ACTIVE',
    esperaDelComienzo(guerra, Date.now()));
  guerra = guerraCon(empieza.payload, RIVAL_SIGLA);
  ok('la guerra empieza al terminar la preparación', `termina en ${Math.round((guerra.endsAtMs - Date.now()) / 60_000)} min`);

  // Una muerte: un aldeano de smoke_clan_b se acerca al guerrero de smoke_clan_a, que lo mata.
  const guerrero = a.snapshot.payload.units.find((u) => u.playerId === a.playerId && u.unitType === 'WARRIOR' && u.status !== 'GARRISONED');
  const aldeano = b.snapshot.payload.units.find((u) => u.playerId === b.playerId && u.unitType === 'VILLAGER');
  if (!guerrero || !aldeano) fallar('no hay guerrero de smoke_clan_a o aldeano de smoke_clan_b para la guerra');
  const llegada = await acercar(b.sesion, aldeano, { x: guerrero.x + 3, y: guerrero.y }, 4);
  ok(`un aldeano de ${CLAN_B} se acerca al guerrero de ${CLAN_A}`, `(${llegada.x}, ${llegada.y})`);
  a.sesion.enviar('session.view', { center: { x: guerrero.x, y: guerrero.y } });
  await a.sesion.esperar('world.snapshot');
  a.sesion.enviar('unit.attack', { unitId: guerrero.id, targetUnitId: aldeano.id });
  const muerte = await a.sesion.esperarQue(['entity.despawn', 'system.error'],
    (m) => m.type === 'system.error' || (m.type === 'entity.despawn' && m.payload.id === aldeano.id), 60_000);
  if (muerte.type === 'system.error') fallar('el servidor rechazó el ataque', muerte.payload);
  const marcador = await a.sesion.esperarQue('clan.update', (m) => (guerraCon(m.payload, RIVAL_SIGLA)?.ourScore ?? 0) >= 1, 20_000);
  const g = guerraCon(marcador.payload, RIVAL_SIGLA);
  ok('la muerte suma al marcador', `[${CLAN_SIGLA}] ${g.ourScore} – ${g.theirScore} [${RIVAL_SIGLA}]`);

  // La paz: la ofrece smoke_clan_b, la acepta smoke_clan_a; empate.
  const ofrece = await ordenDeClan(b.sesion, 'clan.war.peace', { warId: guerra.id, offer: true });
  if (ofrece.type === 'system.error') fallar(`${CLAN_B} no pudo ofrecer la paz`, ofrece.payload);
  const avisoPaz = await a.sesion.esperarQue('event_log.notice', (m) => m.payload.kind === 'CLAN_WAR_PEACE_OFFERED', 10_000);
  if (avisoPaz.payload.clanTag !== RIVAL_SIGLA) fallar('el aviso de la oferta de paz no trae la sigla', avisoPaz.payload);
  ok(`${CLAN_B} ofrece la paz`, `${CLAN_A} recibe el aviso de [${avisoPaz.payload.clanTag}]`);
  const acepta = await ordenDeClan(a.sesion, 'clan.war.peace', { warId: guerra.id, offer: true });
  if (acepta.type === 'system.error') fallar(`${CLAN_A} no pudo aceptar la paz`, acepta.payload);
  if (guerraCon(acepta.payload, RIVAL_SIGLA)) fallar('la paz aceptada no cierra la guerra', acepta.payload);
  ok(`${CLAN_A} la acepta`, `historial de [${CLAN_SIGLA}]: ${JSON.stringify(acepta.payload.clan.warRecord)}`);
  const fin = await b.sesion.esperarQue('event_log.notice', (m) => m.payload.kind === 'CLAN_WAR_ENDED', 10_000);
  if (fin.payload.warResult !== 'DRAWN' || fin.payload.warReason !== 'PEACE') fallar('el final no es un empate por la paz', fin.payload);
  ok(`${CLAN_B} recibe el aviso del final`, `empate por ${fin.payload.ourScore} – ${fin.payload.theirScore}`);

  // El registro de smoke_clan_b cuenta el final (se escribe tras el COMMIT).
  const { entrada } = await entradaDelRegistro(b.sesion, (e) => e.kind === 'CLAN_WAR_ENDED' && e.clanTag === CLAN_SIGLA);
  if (!entrada) fallar(`el registro de ${CLAN_B} no cuenta el final de la guerra`);
  ok(`el registro de ${CLAN_B} cuenta el final`, `entrada ${entrada.id}, ${entrada.warResult}`);

  // smoke_clan_b sale de [RIV], que se disuelve.
  const sale = await ordenDeClan(b.sesion, 'clan.leave', {});
  if (sale.type === 'system.error') fallar(`${CLAN_B} no pudo salir de [${RIVAL_SIGLA}]`, sale.payload);
  ok(`${CLAN_B} sale de [${RIVAL_SIGLA}], que se disuelve`);

  for (const s of [a, b]) s.sesion.cerrar();
}

// ─────────────────────────────────────────────────────────────
// Territorios de clan (SPEC-CLAN-TERRITORIES), sólo con --territorio-clan
// ─────────────────────────────────────────────────────────────

/**
 * El territorio que se disputan [HUM] y smoke_norse: el más cercano a `desde` (la
 * ciudad de smoke_clan_a) que está libre, es de [HUM] o es de smoke_norse sin ser
 * su capital. Como la disputa sólo lo pasa de uno a otro de ellos, cada pasada
 * encuentra el mismo.
 */
export function territorioDelHumo(territorios, desde, rivalId, capitalRival) {
  return territorioCerca(territorios, desde, (t) => {
    if (t.ownerType === 'NONE') return true;
    if (t.ownerType === 'CLAN') return t.ownerClan?.tag === CLAN_SIGLA;
    if (t.ownerType !== 'PLAYER' || t.ownerId !== rivalId) return false;
    return !(capitalRival.x >= t.minX && capitalRival.x <= t.maxX && capitalRival.y >= t.minY && capitalRival.y <= t.maxY);
  });
}

/** Qué le toca hacer a esta pasada con el territorio, según su dueño. */
export function pasoDelTerritorioDeClan(t, rivalId) {
  if (t.ownerType === 'NONE') return 'clan-lo-gana';
  if (t.ownerType === 'CLAN') return 'rival-se-lo-quita';
  if (t.ownerType === 'PLAYER' && t.ownerId === rivalId) return 'clan-lo-recupera';
  return 'otro';
}

/**
 * Un guerrero del jugador, en el mapa: cerca de `donde` o en su ciudad; si no tiene,
 * lo entrena (60 de madera: la semilla con EO_DEVSEED_STOCK_FOR los da).
 */
async function guerreroDe(s, ciudad, donde) {
  for (const centro of [donde, { x: ciudad.centerX, y: ciudad.centerY }]) {
    s.sesion.enviar('session.view', { center: centro });
    const vista = (await s.sesion.esperar('world.snapshot')).payload;
    const g = vista.units.find((u) => u.playerId === s.playerId && u.unitType === 'WARRIOR' && u.status !== 'GARRISONED');
    if (g) return g;
  }
  const g = await entrenar(s.sesion, ciudad, s.playerId, 'WARRIOR');
  ok(`entrena un guerrero en ${ciudad.name}`, `unidad ${g.id}`);
  return g;
}

/** Lleva el guerrero al territorio, lo planta y reclama; devuelve la reclamación. */
async function plantarEstandarte(s, guerrero, t, destino, paraElClan) {
  const llegada = await acercar(s.sesion, guerrero, destino, 1);
  const { unidad } = await posicionDe(s.sesion, guerrero.id, llegada);
  if (!unidad || unidad.x < t.minX || unidad.x > t.maxX || unidad.y < t.minY || unidad.y > t.maxY) {
    fallar('el guerrero no llegó al territorio', { guerrero: unidad, territorio: t });
  }
  s.sesion.enviar('unit.claim', paraElClan ? { unitId: unidad.id, forClan: true } : { unitId: unidad.id });
  const m = await s.sesion.esperarQue(['territory.update', 'system.error'],
    (m) => m.type === 'system.error' || esReclamacionDe(m, t.id, s.playerId), 15_000);
  if (m.type === 'system.error') fallar('el servidor rechazó la reclamación', m.payload);
  return { claim: m.payload.territory.claim, unidad };
}

/** Devuelve el guerrero a su ciudad, para que no defienda solo el territorio en la pasada siguiente. */
async function volverACasa(s, unidad, ciudad) {
  await acercar(s.sesion, unidad, { x: ciudad.centerX + 3, y: ciudad.centerY + 3 }, 2);
}

/**
 * El clan [HUM] de smoke_clan_a y smoke_norse se disputan por turnos el territorio
 * de territorioDelHumo (arranca el servidor con los plazos cortos de --territorio):
 * libre, [HUM] lo gana para el clan con forClan; de [HUM], smoke_norse se lo quita y
 * cada miembro conectado recibe el aviso con la sigla; de smoke_norse, [HUM] lo
 * recupera. Comprueba también que reclamar para un clan sin clan se rechaza y que la
 * vista pública del clan lista sus territorios. Necesita el clan de --clan.
 */
async function territorioDeClan() {
  const a = await entrarConClan(CLAN_A);
  if (pasoDeClan(a.clan, a.playerId) !== 'lider') fallar(`${CLAN_A} no dirige [${CLAN_SIGLA}]`, 'Pasa antes --clan.');
  const clan = a.clan.clan;
  const rival = await entrarConClan(NORDICA);
  if (rival.clan.clan) fallar(`${NORDICA} está en un clan`, rival.clan.clan);
  const ciudadA = a.snapshot.payload.cities.find((c) => c.ownerPlayerId === a.playerId);
  const ciudadR = rival.snapshot.payload.cities.find((c) => c.ownerPlayerId === rival.playerId);
  if (!ciudadA || !ciudadR) fallar('sin ciudad en el snapshot', { ciudadA, ciudadR });

  // El territorio, visto entre las dos ciudades.
  a.sesion.enviar('session.view', { center: { x: Math.round((ciudadA.centerX + ciudadR.centerX) / 2), y: Math.round((ciudadA.centerY + ciudadR.centerY) / 2) } });
  const vista = (await a.sesion.esperar('world.snapshot')).payload;
  const elegido = territorioDelHumo(vista.territories, { x: ciudadA.centerX, y: ciudadA.centerY }, rival.playerId,
    { x: ciudadR.centerX, y: ciudadR.centerY });
  if (!elegido) fallar('no hay territorio que disputar cerca de la ciudad del clan', vista.territories);
  const t = elegido.territorio;
  const paso = pasoDelTerritorioDeClan(t, rival.playerId);
  ok('territorio que se disputan', `${t.name} (${t.id}), ${paso}`);

  const esDelClan = (m) => m.payload.territory.id === t.id && m.payload.territory.ownerType === 'CLAN' &&
    m.payload.territory.ownerClan?.tag === CLAN_SIGLA && !m.payload.territory.contested;

  switch (paso) {
    case 'clan-lo-gana':
    case 'clan-lo-recupera': {
      const g = await guerreroDe(a, ciudadA, elegido.destino);
      const { claim, unidad } = await plantarEstandarte(a, g, t, elegido.destino, true);
      if (claim.clan?.tag !== CLAN_SIGLA) fallar('la reclamación no dice para qué clan', claim);
      ok(`${CLAN_A} reclama para [${CLAN_SIGLA}]`, `vence en ${Math.round((claim.endsAtMs - Date.now()) / 1000)} s`);
      await a.sesion.esperarQue('territory.update', esDelClan, Math.max(0, claim.endsAtMs - Date.now()) + 30_000);
      ok(`${t.name} es del clan`, `ownerClan [${CLAN_SIGLA}]`);
      await a.sesion.esperarQue('clan.update', (m) => m.payload.clan?.territories.some((x) => x.id === t.id), 10_000);
      ok('el clan lo lista entre sus territorios');
      if (paso === 'clan-lo-recupera') {
        const perdido = await avisoEnVivo(rival.sesion, 'TERRITORY_LOST', a.playerId, 15_000);
        if (perdido.payload.clanTag) fallar('el aviso de quien lo pierde para sí no lleva sigla', perdido.payload);
        ok(`${NORDICA} recibe el aviso de la pérdida`, perdido.payload.territoryName);
        const ganado = await avisoEnVivo(a.sesion, 'TERRITORY_TAKEN', rival.playerId, 15_000);
        if (ganado.payload.clanTag !== CLAN_SIGLA) fallar('el aviso de lo ganado para el clan no lleva su sigla', ganado.payload);
        ok(`${CLAN_A} lo gana para su clan`, `TERRITORY_TAKEN [${ganado.payload.clanTag}]`);
      }
      await volverACasa(a, unidad, ciudadA);
      break;
    }
    case 'rival-se-lo-quita': {
      const g = await guerreroDe(rival, ciudadR, elegido.destino);
      // Sin clan no se reclama para un clan (SPEC-CLAN-TERRITORIES §5).
      const r = await ordenDeClan(rival.sesion, 'unit.claim', { unitId: g.id, forClan: true }, []);
      if (r.type !== 'system.error' || r.payload.code !== 'INVALID_TARGET') fallar('reclamar para un clan sin clan no se rechazó', r);
      ok('reclamar para el clan sin clan se rechaza', `«${r.payload.message}»`);
      a.sesion.enviar('session.view', { center: { x: ciudadA.centerX, y: ciudadA.centerY } });
      await a.sesion.esperar('world.snapshot');
      const { claim, unidad } = await plantarEstandarte(rival, g, t, elegido.destino, false);
      ok(`${NORDICA} reclama la tierra de [${CLAN_SIGLA}]`, `vence en ${Math.round((claim.endsAtMs - Date.now()) / 1000)} s`);
      const reclamado = await avisoEnVivo(a.sesion, 'TERRITORY_CLAIMED', rival.playerId, 15_000);
      if (reclamado.payload.clanTag !== CLAN_SIGLA) fallar('el aviso al miembro no lleva la sigla', reclamado.payload);
      ok('el miembro recibe el aviso, con la vista en su ciudad', `TERRITORY_CLAIMED [${reclamado.payload.clanTag}]`);
      const perdido = await avisoEnVivo(a.sesion, 'TERRITORY_LOST', rival.playerId, Math.max(0, claim.endsAtMs - Date.now()) + 30_000);
      if (perdido.payload.clanTag !== CLAN_SIGLA) fallar('el aviso de la pérdida no lleva la sigla', perdido.payload);
      ok('y el de la pérdida', `TERRITORY_LOST [${perdido.payload.clanTag}]`);
      await avisoEnVivo(rival.sesion, 'TERRITORY_TAKEN', a.playerId, 15_000);
      ok(`${NORDICA} lo gana`, 'TERRITORY_TAKEN, una vez');
      await a.sesion.esperarQue('clan.update', (m) => m.payload.clan && !m.payload.clan.territories.some((x) => x.id === t.id), 10_000);
      ok('el clan ya no lo lista');
      await volverACasa(rival, unidad, ciudadR);
      break;
    }
    default:
      fallar('el territorio que se disputan tiene otro dueño', t);
  }

  // La vista pública lista los territorios del clan.
  const info = await ordenDeClan(rival.sesion, 'clan.lookup', { clanId: clan.id }, ['clan.info']);
  if (info.type === 'system.error' || !Array.isArray(info.payload.territories)) fallar('la vista pública no trae los territorios', info.payload);
  ok('la vista pública lista sus territorios', `${info.payload.territories.length}`);
  a.sesion.cerrar();
  rival.sesion.cerrar();
}

// Sólo se ejecuta el recorrido cuando el script se invoca directamente. Importarlo
// —lo que hacen sus tests— no debe intentar conectarse a ningún servidor.
const invocadoDirectamente =
  process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href;

if (invocadoDirectamente) {
  main().catch((err) => fallar('error inesperado', err?.stack || String(err)));
}
