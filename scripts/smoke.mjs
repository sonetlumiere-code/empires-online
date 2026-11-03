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
 *  10. sólo con --tala (SPEC-ECON-FOREST): todos los aldeanos recolectan del mismo
 *      tile hasta agotarlo, y al menos uno pasa solo a otro tile con madera a 6 o
 *      menos. Tarda minutos y gasta bosque de verdad, así que no corre por defecto.
 *  11. sólo con --incursion (SPEC-COMBAT-RAID): con 60 de madera se entrena un
 *      guerrero; un segundo jugador acerca un aldeano y el guerrero lo mata. Tarda
 *      minutos (juntar madera, entrenar, caminar) y mata una unidad de otra cuenta,
 *      así que no corre por defecto ni tras un despliegue en producción.
 *      El aldeano va escoltado por un guardia de la víctima, que responde solo al
 *      ataque (SPEC-COMBAT-RESPONSE); si le faltan guardia o aldeanos, los entrena.
 *  12. sólo con --saqueo (SPEC-COMBAT-PILLAGE): un guerrero propio (el que haya en
 *      casa, o uno nuevo) va hasta la muralla de la víctima, que se conecta para no
 *      estar protegida, y la saquea: su almacén baja, la víctima recibe el aviso
 *      pillagedAtMs y el guerrero carga lo robado. Tarda minutos y roba madera de
 *      otra cuenta, así que no corre por defecto.
 *
 * El paso 6-8 es el que importa: demuestra que el mundo no depende de que
 * nadie esté mirando. Si el movimiento sólo avanzara mientras hay un
 * WebSocket abierto, aquí es donde se vería.
 *
 * Uso:
 *   node scripts/smoke.mjs [--url http://localhost:8080] [--web http://localhost:3000] [--tala] [--incursion] [--saqueo] [--verbose]
 *
 * `--url` es el Game Server (salud y WebSocket); `--web` es Next.js, que desde
 * ADR-018 da de alta, abre la sesión web y emite los tickets.
 *
 * Requiere Node 22 o superior: usa `fetch` y `WebSocket` globales, sin
 * dependencias. Sale con código 1 al primer fallo.
 */

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
const SAQUEO = args.includes('--saqueo');
const PROTOCOL_VERSION = 1;

// Terreno: el byte que viaja en el snapshot es el valor de world.TerrainType.
// MOUNTAIN y WATER no son transitables (internal/game/world/tile.go).
const NO_TRANSITABLE = new Set([3, 4]);
// FOREST: el único terreno que se recolecta (RN-ECON-001).
const FOREST = 1;

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

// ─────────────────────────────────────────────────────────────
// Credenciales
// ─────────────────────────────────────────────────────────────

const USUARIO = process.env.EO_SMOKE_USER || 'smoke_runner';
const CLAVE = process.env.EO_SMOKE_PASSWORD || 'smoke-password-1234';

/** Sólo `nombre=valor` de un Set-Cookie: lo que el navegador devolvería. */
export function cookieDeSesion(setCookie) {
  return setCookie ? setCookie.split(';')[0].trim() : null;
}

/**
 * Da de alta al jugador de humo o entra con el que ya existe. Devuelve el cuerpo
 * (con el primer ticket) y la cookie de sesión con la que pedir los siguientes.
 */
async function abrirSesion(usuario = USUARIO) {
  const cuerpo = JSON.stringify({ username: usuario, password: CLAVE });
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

  if (TALA) {
    await tala(segunda, cuenta.playerId, snap2, bosque);
  }

  if (INCURSION) {
    await incursion(segunda, ciudad, cuenta.playerId, snap2);
  }

  if (SAQUEO) {
    await saqueo(segunda, ciudad, cuenta.playerId, snap2);
  }

  segunda.sesion.cerrar();
  await dormir(200);

  console.log(`\n${pasos} comprobaciones superadas. El vertical slice funciona en este despliegue.\n`);
  process.exit(0);
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
  let almacen = ciudadV.stocks?.WOOD ?? 0;
  if (almacen < 70) {
    // Sus aldeanos pueden haberse quedado por el camino hacia el atacante en
    // incursiones anteriores: vuelven a casa y recolectan.
    const { unidades, casa, terreno } = await unidadesPropias(victima.sesion, cuenta.playerId, ciudadV, guerrero,
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
    almacen = await juntarMadera(victima.sesion, stockCasa, cuenta.playerId,
      { terrain: terreno, units: aldeanos, forests: casa.forests }, 70);
    ok('la víctima junta madera', `${almacen}`);
  }

  // El guerrero va por tramos hasta cerca de la muralla y desde ahí se ordena el
  // saqueo: el servidor elige el tile del anillo.
  const cerca = await acercar(atacante.sesion, guerrero, { x: ciudadV.centerX, y: ciudadV.centerY + 5 }, 4);
  ok('el guerrero llega junto a la ciudad', `(${cerca.x}, ${cerca.y}) · ciudad en (${ciudadV.centerX}, ${ciudadV.centerY})`);
  atacante.sesion.enviar('session.view', { center: { x: ciudadV.centerX, y: ciudadV.centerY } });
  const vista = (await atacante.sesion.esperar('world.snapshot')).payload;
  const antes = vista.cities.find((c) => c.id === ciudadV.id)?.stocks?.WOOD ?? almacen;
  const cargaAntes = vista.units.find((u) => u.id === guerrero.id)?.carrying ?? 0;

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
  victima.sesion.cerrar();
}

// Sólo se ejecuta el recorrido cuando el script se invoca directamente. Importarlo
// —lo que hacen sus tests— no debe intentar conectarse a ningún servidor.
const invocadoDirectamente =
  process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href;

if (invocadoDirectamente) {
  main().catch((err) => fallar('error inesperado', err?.stack || String(err)));
}
