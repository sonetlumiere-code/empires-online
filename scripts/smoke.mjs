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
 *   2. alta o login  ->  ticket efímero + ciudad
 *   3. WebSocket + session.hello  ->  session.welcome
 *   4. world.snapshot con la ciudad y los aldeanos
 *   5. unit.move  ->  el servidor calcula la ruta y responde con la polilínea
 *   6. SE CIERRA la conexión a mitad de movimiento
 *   7. se espera, sin cliente conectado, más allá de la hora de llegada
 *   8. reconexión con ticket nuevo  ->  la unidad está EN EL DESTINO
 *
 * El paso 6-8 es el que importa: demuestra que el mundo no depende de que
 * nadie esté mirando. Si el movimiento sólo avanzara mientras hay un
 * WebSocket abierto, aquí es donde se vería.
 *
 * Uso:
 *   node scripts/smoke.mjs [--url http://localhost:8080] [--verbose]
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
const VERBOSE = args.includes('--verbose');
const PROTOCOL_VERSION = 1;

// Terreno: el byte que viaja en el snapshot es el valor de world.TerrainType.
// MOUNTAIN y WATER no son transitables (internal/game/world/tile.go).
const NO_TRANSITABLE = new Set([3, 4]);

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

async function pedir(ruta, opciones = {}) {
  let res;
  try {
    res = await fetch(BASE + ruta, { ...opciones, signal: AbortSignal.timeout(15_000) });
  } catch (err) {
    fallar(
      `no se pudo contactar con ${BASE}${ruta}`,
      `${err.message}\n\n¿Está el servidor arrancado? \`pnpm run server:run\``,
    );
  }
  const texto = await res.text();
  let cuerpo = texto;
  try {
    cuerpo = JSON.parse(texto);
  } catch {
    /* se queda como texto */
  }
  return { status: res.status, cuerpo };
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
      this.recibidos.push(msg);
      // Despierta a quien estuviera esperando este tipo concreto.
      this.esperando = this.esperando.filter((e) => {
        if (e.tipos.includes(msg.type)) {
          e.resolver(msg);
          return false;
        }
        return true;
      });
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

// ─────────────────────────────────────────────────────────────
// Credenciales
// ─────────────────────────────────────────────────────────────

const USUARIO = process.env.EO_SMOKE_USER || 'smoke_runner';
const CLAVE = process.env.EO_SMOKE_PASSWORD || 'smoke-password-1234';

/** Da de alta al jugador de humo o entra con el que ya existe. Devuelve un ticket NUEVO. */
async function obtenerTicket({ silencioso = false } = {}) {
  const cuerpo = JSON.stringify({ username: USUARIO, password: CLAVE });
  const cabeceras = { 'content-type': 'application/json' };

  const alta = await pedir('/api/auth/register', { method: 'POST', headers: cabeceras, body: cuerpo });
  if (alta.status === 429) {
    fallar(
      'el servidor está limitando por tasa las peticiones de esta máquina',
      'Cada ejecución gasta 2 o 3 peticiones de `/api/auth/*`, y el límite por defecto son 10 por\n' +
        'minuto (EO_AUTH_RATE_LIMIT_PER_MINUTE). Espera un minuto y reintenta.\n\n' +
        'NO es un fallo del despliegue: el límite existe porque el alta ejecuta bcrypt sin\n' +
        'autenticación previa y sin él sería un amplificador de denegación de servicio.',
    );
  }
  if (alta.status === 201 || alta.status === 200) {
    if (!silencioso) ok('alta del jugador de humo', `${USUARIO}`);
    return alta.cuerpo;
  }
  if (alta.status !== 409 || alta.cuerpo?.code !== 'USERNAME_TAKEN') {
    fallar(`el alta falló con ${alta.status}`, alta.cuerpo);
  }

  const login = await pedir('/api/auth/login', { method: 'POST', headers: cabeceras, body: cuerpo });
  if (login.status !== 200) fallar(`el login falló con ${login.status}`, login.cuerpo);
  if (!silencioso) ok('login del jugador de humo', `${USUARIO} (ya existía)`);
  return login.cuerpo;
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
  console.log(`\nComprobación de humo contra ${BASE}\n`);

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
  const cuenta = await obtenerTicket();
  if (!cuenta.ticket) fallar('la respuesta no trae ticket', cuenta);
  if (!cuenta.city) fallar('el jugador no tiene ciudad: el alta no fundó nada', cuenta);
  ok('ciudad fundada', `"${cuenta.city.name}" en (${cuenta.city.centerX}, ${cuenta.city.centerY})`);

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

  const propias = snap.units.filter((u) => u.playerId === cuenta.playerId);
  if (propias.length === 0) fallar('el snapshot no trae ninguna unidad propia', snap.units);
  ok('unidades propias en el snapshot', `${propias.length}`);

  // 5. Movimiento ───────────────────────────────────────────
  const unidad = propias.find((u) => !u.movement) ?? propias[0];
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
  const cuenta2 = await obtenerTicket({ silencioso: true });
  const segunda = await conectar(cuenta2.ticket);
  ok('reconexión aceptada con ticket nuevo');

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

  segunda.sesion.cerrar();
  await dormir(200);

  console.log(`\n${pasos} comprobaciones superadas. El vertical slice funciona en este despliegue.\n`);
  process.exit(0);
}

// Sólo se ejecuta el recorrido cuando el script se invoca directamente. Importarlo
// —lo que hacen sus tests— no debe intentar conectarse a ningún servidor.
const invocadoDirectamente =
  process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href;

if (invocadoDirectamente) {
  main().catch((err) => fallar('error inesperado', err?.stack || String(err)));
}
