/**
 * Tests de las funciones puras de scripts/smoke.mjs.
 *
 * El resto del script son llamadas de red y no se testea aquí: su verificación
 * es ejecutarlo contra un servidor de verdad. Lo que sí necesita test es la
 * decodificación del terreno, porque un error ahí no se manifiesta como un
 * error: se manifiesta como un destino intransitable, un `unit.move.rejected`
 * y un diagnóstico que apunta al servidor cuando el problema estaba en el
 * cliente de prueba.
 *
 * Runner integrado de Node (>= 22), sin dependencias:
 *   node --test scripts/
 */
import { test } from 'node:test';
import assert from 'node:assert/strict';

import {
  ciudadRefundada,
  esLlegadaNueva,
  ultimoIdDelRegistro,
  ciudadTomada,
  motivoParaNoAsaltar,
  apuntaA,
  bienDe,
  castilloDe,
  comisionDelMercado,
  costeDePublicar,
  mercadoDe,
  murallaDe,
  golpesParaAbrir,
  guardiasCerca,
  pasosHastaCastillos,
  cookieDeSesion,
  elegirBosque,
  elegirColina,
  elegirDestino,
  escalarDuracion,
  huecoParaCasa,
  indexarTerreno,
  esReclamacionDe,
  pactoVigente,
  pasoDeFaccion,
  pasoDeClan,
  alianzaCon,
  esperaDelComienzo,
  guerraCon,
  motivoDelAtaqueEntreAliados,
  rolEnClan,
  pasoDelTerritorioDeClan,
  rasgo,
  territorioDelHumo,
  territorioLibreCerca,
  tiemposDePolilinea,
  elaboracionesQueFaltan,
  unidadesDelHumo,
  componer,
  escalarCantidad,
  rasgoCompuesto,
  tecnologiaDelHumo,
  plazoDeInvestigacion,
  sitioParaFundar,
} from './smoke.mjs';

// Terrenos según internal/game/world/tile.go
const GRASSLAND = 0;
const FOREST = 1;
const MOUNTAIN = 3;
const WATER = 4;
const ROAD = 5;

/** Construye un chunk como lo serializa el servidor: filas contiguas, base64. */
function chunkDesdeFilas(cx, cy, filas) {
  const size = filas.length;
  const bytes = Buffer.alloc(size * size);
  filas.forEach((fila, y) => {
    assert.equal(fila.length, size, 'el mapa de prueba debe ser cuadrado');
    fila.forEach((tipo, x) => {
      bytes[y * size + x] = tipo;
    });
  });
  return { cx, cy, size, terrain: bytes.toString('base64') };
}

test('indexarTerreno respeta el orden por filas del servidor', () => {
  // Asimétrico a propósito: si se confundieran filas y columnas, un mapa
  // simétrico pasaría el test igualmente y no probaría nada.
  const chunk = chunkDesdeFilas(0, 0, [
    [GRASSLAND, WATER],
    [ROAD, MOUNTAIN],
  ]);

  const mapa = indexarTerreno([chunk]);

  assert.equal(mapa.get('0,0'), GRASSLAND);
  assert.equal(mapa.get('1,0'), WATER, 'x avanza dentro de la fila');
  assert.equal(mapa.get('0,1'), ROAD, 'y avanza entre filas');
  assert.equal(mapa.get('1,1'), MOUNTAIN);
});

test('indexarTerreno desplaza cada chunk a sus coordenadas de mundo', () => {
  const chunk = chunkDesdeFilas(2, 3, [
    [GRASSLAND, ROAD],
    [WATER, MOUNTAIN],
  ]);

  const mapa = indexarTerreno([chunk]);

  // El chunk (2,3) de tamaño 2 empieza en el tile (4,6).
  assert.equal(mapa.get('4,6'), GRASSLAND);
  assert.equal(mapa.get('5,6'), ROAD);
  assert.equal(mapa.get('4,7'), WATER);
  assert.equal(mapa.get('5,7'), MOUNTAIN);
  assert.equal(mapa.size, 4);
});

test('indexarTerreno combina varios chunks sin pisarse', () => {
  const mapa = indexarTerreno([
    chunkDesdeFilas(0, 0, [[GRASSLAND, GRASSLAND], [GRASSLAND, GRASSLAND]]),
    chunkDesdeFilas(1, 0, [[WATER, WATER], [WATER, WATER]]),
  ]);

  assert.equal(mapa.size, 8);
  assert.equal(mapa.get('1,0'), GRASSLAND);
  assert.equal(mapa.get('2,0'), WATER, 'el segundo chunk empieza en x=2');
});

test('elegirDestino nunca devuelve un tile intransitable', () => {
  const mapa = new Map();
  for (let y = 0; y < 30; y++) {
    for (let x = 0; x < 30; x++) {
      // Todo agua salvo una única isla transitable a distancia 8.
      mapa.set(`${x},${y}`, x === 8 && y === 0 ? GRASSLAND : WATER);
    }
  }

  const destino = elegirDestino(mapa, { x: 0, y: 0 });

  assert.deepEqual({ x: destino.x, y: destino.y }, { x: 8, y: 0 });
});

test('elegirDestino descarta lo que queda fuera del rango de distancia', () => {
  const mapa = new Map();
  mapa.set('0,0', GRASSLAND);
  mapa.set('3,0', GRASSLAND); // demasiado cerca (< 6)
  mapa.set('40,0', GRASSLAND); // demasiado lejos (> 12)

  assert.equal(elegirDestino(mapa, { x: 0, y: 0 }), null);
});

test('elegirDestino es determinista ante empates', () => {
  const mapa = new Map();
  // Tres candidatos a la MISMA distancia de Chebyshev (10).
  for (const [x, y] of [[10, 4], [10, 2], [4, 10]]) mapa.set(`${x},${y}`, GRASSLAND);

  const primero = elegirDestino(mapa, { x: 0, y: 0 });
  const segundo = elegirDestino(new Map([...mapa.entries()].reverse()), { x: 0, y: 0 });

  // Desempate por y y luego x: (10,2) gana a (10,4) y a (4,10).
  assert.deepEqual({ x: primero.x, y: primero.y }, { x: 10, y: 2 });
  assert.deepEqual(
    { x: segundo.x, y: segundo.y },
    { x: primero.x, y: primero.y },
    'el orden de inserción del Map no debe cambiar el resultado',
  );
});

test('elegirDestino prefiere el candidato más lejano dentro del rango', () => {
  const mapa = new Map();
  mapa.set('6,0', GRASSLAND);
  mapa.set('12,0', GRASSLAND);

  const destino = elegirDestino(mapa, { x: 0, y: 0 });

  // Cuanto más largo el trayecto, más margen tiene el paso de la desconexión.
  assert.equal(destino.x, 12);
});

test('cookieDeSesion devuelve sólo nombre=valor, lo que el navegador reenvía', () => {
  assert.equal(
    cookieDeSesion('eo_session=abc-123; Path=/api/auth; HttpOnly; SameSite=Strict; Max-Age=604800'),
    'eo_session=abc-123',
  );
  assert.equal(cookieDeSesion(null), null);
});

test('elegirBosque toma el bosque más cercano a la ciudad, fuera de su muralla', () => {
  const mapa = new Map([
    ['10,10', FOREST], // el centro: es muralla
    ['11,9', FOREST], // dentro del 3×3: también
    ['14,10', FOREST], // a 4
    ['10,13', FOREST], // a 3, el más cercano
    ['7,7', FOREST], // a 3, pero de menor y: gana el desempate
    ['12,10', GRASSLAND],
  ]);
  assert.deepEqual(elegirBosque(mapa, { centerX: 10, centerY: 10 }), { x: 7, y: 7, d: 3 });
  assert.equal(elegirBosque(new Map([['3,3', GRASSLAND]]), { centerX: 10, centerY: 10 }), null);
  // SPEC-ECON-FOREST: un tile agotado no se elige; uno a medias, sí.
  assert.deepEqual(elegirBosque(mapa, { centerX: 10, centerY: 10 }, [{ x: 7, y: 7, remaining: 0 }]), { x: 10, y: 13, d: 3 });
  assert.deepEqual(elegirBosque(mapa, { centerX: 10, centerY: 10 }, [{ x: 7, y: 7, remaining: 40 }]), { x: 7, y: 7, d: 3 });
});

test('huecoParaCasa toma el primer tile libre del 3 × 3, fila a fila y sin el centro', () => {
  const ciudad = { centerX: 10, centerY: 10 };
  assert.deepEqual(huecoParaCasa(ciudad, []), { x: 9, y: 9 });
  assert.deepEqual(huecoParaCasa(ciudad, [{ x: 9, y: 9 }, { x: 10, y: 9 }]), { x: 11, y: 9 });
  const todos = [];
  for (let dy = -1; dy <= 1; dy++) for (let dx = -1; dx <= 1; dx++) if (dx || dy) todos.push({ x: 10 + dx, y: 10 + dy });
  assert.equal(huecoParaCasa(ciudad, todos), null);
  assert.deepEqual(huecoParaCasa(ciudad, todos.slice(0, 4)), { x: 11, y: 10 }, 'el centro nunca es un hueco');
});

test('elegirColina toma la colina más cercana a la ciudad y salta las agotadas (SPEC-ECON-STONE)', () => {
  const mapa = new Map([['30,10', 2], ['12,20', 2], ['13,20', 2], ['11,11', 1]]);
  const ciudad = { centerX: 10, centerY: 10 };
  assert.deepEqual(elegirColina(mapa, ciudad), { x: 12, y: 20, d: 10 }, 'a igual distancia, la de menor x');
  assert.deepEqual(elegirColina(mapa, ciudad, [{ x: 12, y: 20, remaining: 0 }]), { x: 13, y: 20, d: 10 });
  assert.equal(elegirColina(new Map([['11,11', 1]]), ciudad), null, 'un bosque no es colina');
});

test('pactoVigente no toma un acuerdo comercial por un pacto: TRADE no da paz (SPEC-TRADE §4.1)', () => {
  const comercial = { id: 3, type: 'TRADE', status: 'ACTIVE', breaksAtMs: null };
  assert.equal(pactoVigente([comercial]), undefined, 'con sólo un TRADE vigente hay que proponer el pacto');
  const pacto = { id: 5, type: 'NON_AGGRESSION', status: 'ACTIVE', breaksAtMs: null };
  assert.equal(pactoVigente([comercial, pacto]), pacto);
  const alianza = { id: 6, type: 'ALLIANCE', status: 'ACTIVE', breaksAtMs: null };
  assert.equal(pactoVigente([alianza]), alianza);
  assert.equal(pactoVigente([{ ...pacto, status: 'PROPOSED' }]), undefined, 'una oferta no es un tratado');
});

// SPEC-TERRITORY-CONQUEST: el humo --territorio elige el territorio libre más
// cercano y un tile bien dentro de él.
test('territorioLibreCerca elige el libre más cercano y un tile dentro de su borde', () => {
  const t = (id, minX, minY, extra = {}) => ({ id, name: `T${id}`, minX, minY, maxX: minX + 63, maxY: minY + 63, ownerType: 'NONE', ownerId: null, ownerFaction: null, contested: false, ...extra });
  const territorios = [
    t(24, 448, 128, { ownerType: 'PLAYER', ownerId: 'x' }),
    t(32, 448, 192),
    t(23, 384, 128, { contested: true, claim: { playerId: 'y' } }),
    t(31, 384, 192),
  ];
  const r = territorioLibreCerca(territorios, { x: 451, y: 184 });
  assert.equal(r.territorio.id, 32);
  assert.deepEqual(r.destino, { x: 454, y: 198 });
  assert.equal(territorioLibreCerca([territorios[0], territorios[2]], { x: 0, y: 0 }), null);
});

// Fallo del humo --territorio (2026-11-04): la víctima tomó como suya la
// reclamación anterior de smoke_runner, que seguía en su cola, y esperó la
// pérdida con un plazo ya vencido. Sólo vale la reclamación de quien reclama.
test('esReclamacionDe no toma la reclamación de otro jugador que quedó en la cola', () => {
  const msg = (id, quien) => ({
    type: 'territory.update',
    payload: { territory: { id, contested: quien !== null, ...(quien ? { claim: { playerId: quien, endsAtMs: 1 } } : {}) } },
  });
  assert.equal(esReclamacionDe(msg(31, 'victima'), 31, 'victima'), true);
  assert.equal(esReclamacionDe(msg(31, 'runner'), 31, 'victima'), false, 'la de otro jugador no confirma la propia');
  assert.equal(esReclamacionDe(msg(31, null), 31, 'victima'), false);
  assert.equal(esReclamacionDe(msg(30, 'victima'), 31, 'victima'), false);
  assert.equal(esReclamacionDe({ type: 'system.error', payload: {} }, 31, 'victima'), false);
});

// SPEC-CIVILIZATION: el humo recalcula la polilínea con la misma aritmética
// entera que el servidor (player.ScaleDuration y movement.StepDurationMs).
test('escalarDuracion es player.ScaleDuration', () => {
  assert.equal(escalarDuracion(600, 1000), 600);
  assert.equal(escalarDuracion(600, 1100), 545);
  assert.equal(escalarDuracion(600, 950), 632);
  assert.equal(escalarDuracion(15_000, 1100), 13_636);
  assert.equal(escalarDuracion(1, 1_000_000), 1);
});

test('rasgo lee el catálogo y una clave ausente vale 1000', () => {
  const catalogo = [{ code: 'NORSE', name: 'Nórdicos', traits: { 'unit.move_speed': 1100 } }];
  assert.equal(rasgo(catalogo, 'NORSE', 'unit.move_speed'), 1100);
  assert.equal(rasgo(catalogo, 'NORSE', 'building.house_hp'), 1000);
  assert.throws(() => rasgo(catalogo, 'AZTEC', 'unit.move_speed'));
});

test('tiemposDePolilinea suma pasos de hierba, bosque y diagonal como el servidor', () => {
  const terreno = new Map([['1,0', GRASSLAND], ['2,0', 1], ['3,1', GRASSLAND]]);
  const path = [{ x: 0, y: 0 }, { x: 1, y: 0 }, { x: 2, y: 0 }, { x: 3, y: 1 }];
  // 545 en hierba; 872 en bosque (545 × 16 / 10); la diagonal, 545 × √2 = 771.
  assert.deepEqual(tiemposDePolilinea(path, terreno, 545), [0, 545, 1417, 2188]);
  assert.equal(tiemposDePolilinea([{ x: 0, y: 0 }, { x: 9, y: 9 }], terreno, 545), null, 'sin terreno conocido');
});

test('bienDe da el bien de cada civilización del catálogo (SPEC-CIVILIZATION-GOODS)', () => {
  const catalogo = [{ code: 'ROMAN', good: 'WINE' }, { code: 'NORSE', good: 'FURS' }];
  assert.equal(bienDe(catalogo, 'ROMAN'), 'WINE');
  assert.equal(bienDe(catalogo, 'NORSE'), 'FURS');
  assert.equal(bienDe(catalogo, 'AZTEC'), null);
});


test('pasoDeFaccion: pedir desde NEUTRAL, esperar la propia entrada, nada si ya está dentro (SPEC-FACTIONS)', () => {
  assert.equal(pasoDeFaccion({ faction: 'NEUTRAL', factionJoin: null }, 'ORDER'), 'pedir');
  assert.equal(pasoDeFaccion({ faction: 'NEUTRAL', factionJoin: { faction: 'ORDER', atMs: 1 } }, 'ORDER'), 'esperar');
  assert.equal(pasoDeFaccion({ faction: 'ORDER', factionJoin: null }, 'ORDER'), 'dentro');
  assert.equal(pasoDeFaccion({ faction: 'CHAOS', factionJoin: null }, 'ORDER'), 'otro-bando');
  assert.equal(pasoDeFaccion({ faction: 'NEUTRAL', factionJoin: { faction: 'CHAOS', atMs: 1 } }, 'ORDER'), 'otro-bando');
  assert.equal(pasoDeFaccion({}, 'CHAOS'), 'pedir', 'un servidor sin facción es NEUTRAL');
});

test('pasoDeClan: fundar sin clan, nada si ya lo dirige, fallar en otro clan (SPEC-CLANS)', () => {
  const yo = '00000000-0000-4000-8000-000000000001';
  const clan = (leaderId, tag = 'HUM') => ({ clan: { id: 1, name: 'Humo', tag, leaderId, members: [], sentInvitations: [] }, invitations: [] });
  assert.equal(pasoDeClan({ clan: null, invitations: [] }, yo), 'fundar');
  assert.equal(pasoDeClan(clan(yo), yo), 'lider');
  assert.equal(pasoDeClan(clan('otro'), yo), 'no-lider');
  assert.equal(pasoDeClan(clan(yo, 'OTR'), yo), 'otro-clan');
});

// SPEC-CLAN-ALLIANCES: el humo --alianza-clan busca la alianza por la sigla del otro clan.
test('alianzaCon encuentra la alianza o la oferta con un clan, o null', () => {
  const alianza = { id: 9, clanId: 2, name: 'Eco', tag: 'ECO', status: 'ACTIVE', proposedByUs: true, breaksAtMs: null };
  const upd = { clan: { id: 1, tag: 'HUM', alliances: [alianza] }, invitations: [] };
  assert.deepEqual(alianzaCon(upd, 'ECO'), alianza);
  assert.equal(alianzaCon(upd, 'OTR'), null);
  assert.equal(alianzaCon({ clan: null, invitations: [] }, 'ECO'), null);
  assert.equal(alianzaCon({ clan: { id: 1, tag: 'HUM' } }, 'ECO'), null, 'un servidor anterior sin alliances');
});

// SPEC-CLAN-WARS: el humo --guerra-clan busca la guerra por la sigla del otro clan y
// espera su comienzo hasta la preparación y un margen para el barrido.
test('guerraCon encuentra la guerra abierta con un clan, o null', () => {
  const guerra = { id: 4, clanId: 2, name: 'Rivales', tag: 'RIV', declaredByUs: true, status: 'DECLARED', startsAtMs: 9, endsAtMs: null, ourScore: 0, theirScore: 0 };
  const upd = { clan: { id: 1, tag: 'HUM', wars: [guerra] }, invitations: [] };
  assert.deepEqual(guerraCon(upd, 'RIV'), guerra);
  assert.equal(guerraCon(upd, 'OTR'), null);
  assert.equal(guerraCon({ clan: null, invitations: [] }, 'RIV'), null);
  assert.equal(guerraCon({ clan: { id: 1, tag: 'HUM' } }, 'RIV'), null, 'un servidor anterior sin wars');
});

test('esperaDelComienzo espera hasta el comienzo y 15 s de margen', () => {
  assert.equal(esperaDelComienzo({ startsAtMs: 70_000 }, 10_000), 75_000);
  assert.equal(esperaDelComienzo({ startsAtMs: 5_000 }, 10_000), 15_000, 'ya empezada: sólo el margen');
});

// SPEC-CLAN-TERRITORIES: el humo --territorio-clan disputa siempre el mismo
// territorio entre [HUM] y smoke_norse, y sabe qué toca según su dueño.
test('territorioDelHumo elige el más cercano libre, de [HUM] o del rival sin ser su capital', () => {
  const t = (id, minX, minY, extra = {}) => ({ id, name: `T${id}`, minX, minY, maxX: minX + 63, maxY: minY + 63, ownerType: 'NONE', ownerId: null, ownerFaction: null, ownerClan: null, contested: false, ...extra });
  const rival = 'norse';
  const capital = { x: 508, y: 283 };
  const ciudadClan = { x: 503, y: 198 };
  const deHum = { ownerType: 'CLAN', ownerId: '1', ownerClan: { id: 1, name: 'Humo', tag: 'HUM' } };
  const casos = [
    [[t(39, 384, 256), t(16, 448, 64)], 39, 'libre'],
    [[t(39, 384, 256, deHum), t(16, 448, 64)], 39, 'de [HUM]'],
    [[t(39, 384, 256, { ownerType: 'PLAYER', ownerId: rival }), t(16, 448, 64)], 39, 'del rival'],
    [[t(39, 384, 256, { ownerType: 'CLAN', ownerId: '2', ownerClan: { id: 2, name: 'Otro', tag: 'OTR' } }), t(16, 448, 64)], 16, 'de otro clan no'],
    [[t(40, 448, 256, { ownerType: 'PLAYER', ownerId: rival }), t(16, 448, 64)], 16, 'la capital del rival no'],
    [[t(39, 384, 256, { claim: { playerId: 'x' } }), t(16, 448, 64)], 16, 'en disputa no'],
  ];
  for (const [territorios, id, que] of casos) {
    assert.equal(territorioDelHumo(territorios, ciudadClan, rival, capital).territorio.id, id, que);
  }
  assert.equal(pasoDelTerritorioDeClan(t(39, 0, 0), rival), 'clan-lo-gana');
  assert.equal(pasoDelTerritorioDeClan(t(39, 0, 0, deHum), rival), 'rival-se-lo-quita');
  assert.equal(pasoDelTerritorioDeClan(t(39, 0, 0, { ownerType: 'PLAYER', ownerId: rival }), rival), 'clan-lo-recupera');
  assert.equal(pasoDelTerritorioDeClan(t(39, 0, 0, { ownerType: 'PLAYER', ownerId: 'otro' }), rival), 'otro');
});

test('rolEnClan: el rango de un jugador en el clan de un clan.update (SPEC-CLAN-GOVERNANCE)', () => {
  const update = { clan: { members: [{ playerId: 'a', role: 'LEADER' }, { playerId: 'b', role: 'OFFICER' }] }, invitations: [] };
  assert.equal(rolEnClan(update, 'a'), 'LEADER');
  assert.equal(rolEnClan(update, 'b'), 'OFFICER');
  assert.equal(rolEnClan(update, 'c'), null);
  assert.equal(rolEnClan({ clan: null, invitations: [] }, 'a'), null);
});

test('motivoDelAtaqueEntreAliados: el de la alianza, o el del clan si cuenta antes la paz residual', () => {
  assert.equal(motivoDelAtaqueEntreAliados({ code: 'INVALID_TARGET', message: 'vuestros clanes están aliados' }), 'vuestros clanes están aliados');
  assert.match(motivoDelAtaqueEntreAliados({ code: 'INVALID_TARGET', message: 'sois del mismo clan' }), /paz residual/);
  assert.equal(motivoDelAtaqueEntreAliados({ code: 'INVALID_TARGET', message: 'un tratado de paz impide atacar a ese jugador' }), null);
  assert.equal(motivoDelAtaqueEntreAliados({ code: 'UNIT_NOT_FOUND', message: 'la unidad objetivo no existe' }), null);
  assert.equal(motivoDelAtaqueEntreAliados(undefined), null);
});

test('unidadesDelHumo da la unidad propia, una ajena y la ficha de la propia (SPEC-CIVILIZATION-UNITS)', () => {
  const catalogo = [
    { code: 'ROMAN', unit: 'LEGIONARY' },
    { code: 'NORSE', unit: 'BERSERKER' },
  ];
  const unidades = [{ code: 'LEGIONARY', maxHp: 110 }, { code: 'BERSERKER', maxHp: 60 }];
  assert.deepEqual(unidadesDelHumo(catalogo, unidades, 'ROMAN'),
    { propia: 'LEGIONARY', ajena: 'BERSERKER', ficha: { code: 'LEGIONARY', maxHp: 110 } });
  assert.equal(unidadesDelHumo(catalogo, unidades, 'NORSE').ajena, 'LEGIONARY');
  assert.equal(unidadesDelHumo(catalogo, unidades, 'ATLANTE'), null);
  assert.equal(unidadesDelHumo(catalogo, [], 'ROMAN'), null, 'sin ficha no hay con qué comprobar');
});

test('elaboracionesQueFaltan cuenta las de 5 que hacen falta para llegar al coste', () => {
  assert.equal(elaboracionesQueFaltan(10, 10), 0);
  assert.equal(elaboracionesQueFaltan(12, 10), 0);
  assert.equal(elaboracionesQueFaltan(5, 10), 1);
  assert.equal(elaboracionesQueFaltan(4, 10), 2);
  assert.equal(elaboracionesQueFaltan(0, 10), 2);
});

// --castillo (SPEC-CASTLE-AGE): las eras que le faltan a la ciudad, su castillo y el
// building.update que dice a quién apunta.
test('pasosHastaCastillos da las eras que faltan, en orden', () => {
  assert.deepEqual(pasosHastaCastillos('STONE_AGE'), ['BRONZE_AGE', 'IRON_AGE', 'CASTLE_AGE']);
  assert.deepEqual(pasosHastaCastillos('IRON_AGE'), ['CASTLE_AGE']);
  assert.deepEqual(pasosHastaCastillos('CASTLE_AGE'), []);
  assert.deepEqual(pasosHastaCastillos('AZTEC_AGE'), []);
});

test('castilloDe encuentra el castillo de la ciudad, no el de otra ni un taller', () => {
  const edificios = [
    { id: 1, cityId: 7, type: 'WORKSHOP' },
    { id: 2, cityId: 8, type: 'CASTLE' },
    { id: 3, cityId: 7, type: 'CASTLE' },
  ];
  assert.equal(castilloDe(edificios, 7)?.id, 3);
  assert.equal(castilloDe(edificios, 9), null);
});

test('apuntaA reconoce el objetivo del castillo, y no el null ni otro edificio', () => {
  assert.equal(apuntaA({ id: 3, status: 'COMPLETE', targetUnitId: 70 }, 3, 70), true);
  assert.equal(apuntaA({ id: 3, status: 'COMPLETE', targetUnitId: null }, 3, 70), false);
  assert.equal(apuntaA({ id: 4, status: 'COMPLETE', targetUnitId: 70 }, 3, 70), false);
  assert.equal(apuntaA({ id: 3, status: 'COMPLETE', hp: 990 }, 3, 70), false);
});

test('guardiasCerca da las unidades propias que atacan junto al centro, no aldeanos ni guarnecidas', () => {
  const catalogo = [{ code: 'VILLAGER', attacks: false }, { code: 'WARRIOR', attacks: true }, { code: 'LEGIONARY', attacks: true }];
  const yo = 'p1';
  const unidades = [
    { id: 1, playerId: yo, unitType: 'WARRIOR', x: 12, y: 10, status: 'IDLE' },
    { id: 2, playerId: yo, unitType: 'VILLAGER', x: 10, y: 10, status: 'IDLE' },
    { id: 3, playerId: yo, unitType: 'LEGIONARY', x: 30, y: 10, status: 'IDLE' },
    { id: 4, playerId: 'p2', unitType: 'WARRIOR', x: 10, y: 11, status: 'IDLE' },
    { id: 5, playerId: yo, unitType: 'LEGIONARY', x: 10, y: 10, status: 'GARRISONED' },
    { id: 6, playerId: yo, unitType: 'LEGIONARY', x: 18, y: 2, status: 'ATTACKING' },
  ];
  assert.deepEqual(guardiasCerca(unidades, yo, { x: 10, y: 10 }, catalogo).map((u) => u.id), [1, 6]);
});

// --tecnologia (SPEC-TECHNOLOGIES): la composición, la elección y el plazo.
const CIVS = [
  { code: 'ROMAN', good: 'WINE', traits: { 'production.speed': 1100 } },
  { code: 'NORSE', good: 'FURS', traits: { 'production.speed': 900 } },
];
const TECS = [
  { code: 'STONE_TOOLS', era: 'STONE_AGE', civilization: null, costs: { WOOD: 150, STONE: 50 }, foreignGoods: 0, ownGood: 0, durationMs: 60_000, requires: [], effects: { 'economy.gather_speed': 1100 } },
  { code: 'MASONRY', era: 'BRONZE_AGE', civilization: null, costs: { WOOD: 200, STONE: 200 }, foreignGoods: 0, ownGood: 0, durationMs: 120_000, requires: [], effects: { 'building.house_hp': 1200 } },
  { code: 'GUILDS', era: 'IRON_AGE', civilization: null, costs: { WOOD: 300, STONE: 300 }, foreignGoods: 30, ownGood: 0, durationMs: 180_000, requires: [], effects: { 'production.speed': 1100 } },
  { code: 'ARCHITECTURE', era: 'CASTLE_AGE', civilization: null, costs: { WOOD: 400, STONE: 400 }, foreignGoods: 60, ownGood: 0, durationMs: 300_000, requires: ['GUILDS'], effects: { 'production.speed': 1100 } },
  { code: 'ROMAN_ROADS', era: 'IRON_AGE', civilization: 'ROMAN', costs: { WOOD: 200, STONE: 200 }, foreignGoods: 0, ownGood: 40, durationMs: 180_000, requires: [], effects: { 'unit.move_speed': 1100 } },
  { code: 'RAIDING', era: 'IRON_AGE', civilization: 'NORSE', costs: { WOOD: 200, STONE: 200 }, foreignGoods: 0, ownGood: 40, durationMs: 180_000, requires: [], effects: { 'combat.loot_carry': 1200 } },
];

test('componer y escalarCantidad son las fórmulas del servidor', () => {
  assert.equal(componer(1100, 1050), 1155);
  assert.equal(componer(1100, 1100), 1210);
  assert.equal(componer(1, 1), 1);
  assert.equal(escalarCantidad(150, 1200), 180);
  assert.equal(escalarCantidad(8, 1100), 9);
});

test('rasgoCompuesto parte del de la civilización y compone en orden de código', () => {
  assert.equal(rasgoCompuesto(CIVS, TECS, 'ROMAN', [], 'production.speed'), 1100);
  assert.equal(rasgoCompuesto(CIVS, TECS, 'ROMAN', ['GUILDS'], 'production.speed'), 1210);
  assert.equal(rasgoCompuesto(CIVS, TECS, 'ROMAN', ['GUILDS', 'ARCHITECTURE'], 'production.speed'), 1331);
  assert.equal(rasgoCompuesto(CIVS, TECS, 'NORSE', ['MASONRY'], 'building.house_hp'), 1200);
});

test('tecnologiaDelHumo elige una a su alcance y que pueda pagar, Albañilería primero', () => {
  const ciudad = (over) => ({ era: 'CASTLE_AGE', civilization: 'ROMAN', technologies: [], stocks: { WOOD: 5000, STONE: 5000, WINE: 0, SILK: 0 }, ...over });
  assert.equal(tecnologiaDelHumo(TECS, CIVS, ciudad())?.code, 'MASONRY');
  assert.equal(tecnologiaDelHumo(TECS, CIVS, ciudad({ technologies: ['MASONRY'] }))?.code, 'STONE_TOOLS');
  assert.equal(tecnologiaDelHumo(TECS, CIVS, ciudad({ technologies: ['MASONRY', 'STONE_TOOLS'] })), null,
    'sin bienes ajenos ni vino no paga ninguna otra');
  assert.equal(tecnologiaDelHumo(TECS, CIVS, ciudad({ technologies: ['MASONRY', 'STONE_TOOLS'], stocks: { WOOD: 5000, STONE: 5000, WINE: 40, SILK: 0 } }))?.code,
    'ROMAN_ROADS', 'su exclusiva con su vino; la nórdica nunca');
  assert.equal(tecnologiaDelHumo(TECS, CIVS, ciudad({ technologies: ['MASONRY', 'STONE_TOOLS', 'ROMAN_ROADS'], stocks: { WOOD: 5000, STONE: 5000, WINE: 500, SILK: 30 } }))?.code,
    'GUILDS', 'el vino propio no cuenta como ajeno; la seda sí, y Arquitectura pide Gremios');
  assert.equal(tecnologiaDelHumo(TECS, CIVS, ciudad({ era: 'STONE_AGE' }))?.code, 'STONE_TOOLS', 'Albañilería pide Bronce');
  assert.equal(tecnologiaDelHumo(TECS, CIVS, ciudad({ stocks: { WOOD: 100, STONE: 100 } })), null);
});

test('plazoDeInvestigacion: el del catálogo con el rasgo compuesto, o el de la variable', () => {
  assert.equal(plazoDeInvestigacion(TECS[0], CIVS, TECS, 'ROMAN', []), 54_545);
  assert.equal(plazoDeInvestigacion(TECS[3], CIVS, TECS, 'ROMAN', ['GUILDS']), 247_934);
  assert.equal(plazoDeInvestigacion(TECS[0], CIVS, TECS, 'NORSE', []), 66_667);
  assert.equal(plazoDeInvestigacion(TECS[0], CIVS, TECS, 'ROMAN', [], 20), 20_000);
});

// SPEC-MARKETS (--mercado): la comisión, lo que cuesta publicar y el mercado de una ciudad.
test('comisionDelMercado es la décima parte de cada lote, hacia arriba', () => {
  assert.deepEqual([1, 9, 10, 11, 20, 30].map(comisionDelMercado), [1, 1, 1, 2, 2, 3]);
  assert.equal(comisionDelMercado(0), 0);
});

test('costeDePublicar suma la custodia y la comisión de todos los lotes', () => {
  assert.equal(costeDePublicar(10, 2), 22, 'el humo: 20 en custodia y 2 de comisión');
  assert.equal(costeDePublicar(20, 3), 66, 'el ejemplo de la spec');
});

test('mercadoDe encuentra el mercado de la ciudad, no el de otra ni otro edificio', () => {
  const edificios = [
    { id: 1, cityId: 7, type: 'WORKSHOP' },
    { id: 2, cityId: 8, type: 'MARKET' },
    { id: 3, cityId: 7, type: 'MARKET' },
  ];
  assert.equal(mercadoDe(edificios, 7).id, 3);
  assert.equal(mercadoDe(edificios, 9), undefined);
});

// SPEC-SIEGE-ENGINES, para --ariete: la muralla de una ciudad y cuántos golpes de
// ariete la abren.
test('murallaDe encuentra la muralla de la ciudad, y sólo la suya', () => {
  const edificios = [
    { id: 1, cityId: 7, type: 'HOUSE' },
    { id: 2, cityId: 8, type: 'WALL' },
    { id: 3, cityId: 7, type: 'WALL' },
  ];
  assert.equal(murallaDe(edificios, 7)?.id, 3);
  assert.equal(murallaDe(edificios, 9), undefined);
});

test('golpesParaAbrir: 38 golpes de 40 para 1 500, redondeando hacia arriba', () => {
  assert.equal(golpesParaAbrir(1_500), 38);
  assert.equal(golpesParaAbrir(1_460), 37);
  assert.equal(golpesParaAbrir(40), 1);
  assert.equal(golpesParaAbrir(0), 0);
  assert.equal(golpesParaAbrir(100, 30), 4);
});

// SPEC-CITY-FALL, para --caida: si se puede asaltar la ciudad y cuál es la refundada.
test('motivoParaNoAsaltar: Piedra, muralla completa, inmunidad y otro asalto; en brecha, nada', () => {
  const ciudad = { id: 7, era: 'BRONZE_AGE' };
  assert.equal(motivoParaNoAsaltar(ciudad, [], 1_000), null);
  assert.match(motivoParaNoAsaltar({ ...ciudad, era: 'STONE_AGE' }, [], 1_000), /Piedra/);
  assert.match(motivoParaNoAsaltar(ciudad, [{ cityId: 7, type: 'WALL', status: 'COMPLETE' }], 1_000), /muralla/);
  assert.equal(motivoParaNoAsaltar(ciudad, [{ cityId: 7, type: 'WALL', status: 'UNDER_CONSTRUCTION' }], 1_000), null);
  assert.equal(motivoParaNoAsaltar(ciudad, [{ cityId: 8, type: 'WALL', status: 'COMPLETE' }], 1_000), null);
  assert.match(motivoParaNoAsaltar({ ...ciudad, fallImmuneUntilMs: 2_000 }, [], 1_000), /recién fundada/);
  assert.equal(motivoParaNoAsaltar({ ...ciudad, fallImmuneUntilMs: 1_000 }, [], 1_000), null);
  assert.match(motivoParaNoAsaltar({ ...ciudad, assault: { endsAtMs: 9 } }, [], 1_000), /asalto/);
});

test('ciudadRefundada: la del jugador con otro id que la caída', () => {
  const snapshot = { cities: [{ id: 7, ownerPlayerId: 'a' }, { id: 9, ownerPlayerId: 'b' }, { id: 12, ownerPlayerId: 'a' }] };
  assert.equal(ciudadRefundada(snapshot, 'a', 7)?.id, 12);
  assert.equal(ciudadRefundada({ cities: [{ id: 7, ownerPlayerId: 'a' }] }, 'a', 7), undefined);
});

test('sitioParaFundar: transitable, lejos de los centros, con bosque y en territorio libre (SPEC-MULTI-CITY)', () => {
  const terreno = new Map();
  for (let y = 0; y < 80; y++) for (let x = 0; x < 80; x++) terreno.set(`${x},${y}`, GRASSLAND);
  // Bosque al este de (40,40): 4 × 5 tiles.
  for (let y = 38; y <= 42; y++) for (let x = 44; x <= 47; x++) terreno.set(`${x},${y}`, FOREST);
  const libre = [{ minX: 0, minY: 0, maxX: 79, maxY: 79, ownerType: 'NONE' }];
  const capital = { x: 10, y: 40 };
  const s = sitioParaFundar(terreno, [capital], libre, capital);
  assert.ok(s, 'hay sitio');
  assert.ok(s.d >= 28, 'a 28 o más de la capital');
  assert.ok(Math.max(Math.abs(s.x - 45), Math.abs(s.y - 40)) <= 8, 'con el bosque cerca');
  assert.equal(sitioParaFundar(terreno, [capital], libre, capital)?.x, s.x, 'determinista');

  // Un territorio con dueño no vale; sin bosque tampoco; agua en el entorno tampoco.
  const ajeno = [{ minX: 0, minY: 0, maxX: 79, maxY: 79, ownerType: 'PLAYER' }];
  assert.equal(sitioParaFundar(terreno, [capital], ajeno, capital), null);
  const sinBosque = new Map([...terreno].map(([k, v]) => [k, v === FOREST ? GRASSLAND : v]));
  assert.equal(sitioParaFundar(sinBosque, [capital], libre, capital), null);
  const conAgua = new Map(terreno);
  for (let y = 30; y <= 50; y++) for (let x = 30; x <= 43; x++) conAgua.set(`${x},${y}`, WATER);
  const s2 = sitioParaFundar(conAgua, [capital], libre, capital);
  assert.ok(!s2 || s2.x >= 47, 'nunca con agua en su 7 × 7');
});

test('ciudadTomada: la del vencedor con ese id, en el centro de la caída, con su nombre y su era', () => {
  const caida = { id: 7, centerX: 40, centerY: 20, name: 'Villa', era: 'BRONZE_AGE' };
  const buena = { id: 12, ownerPlayerId: 'v', centerX: 40, centerY: 20, name: 'Villa', era: 'BRONZE_AGE' };
  assert.equal(ciudadTomada({ cities: [buena] }, 'v', 12, caida)?.id, 12);
  assert.equal(ciudadTomada({ cities: [{ ...buena, ownerPlayerId: 'x' }] }, 'v', 12, caida), undefined);
  assert.equal(ciudadTomada({ cities: [{ ...buena, centerX: 41 }] }, 'v', 12, caida), undefined);
  assert.equal(ciudadTomada({ cities: [buena] }, 'v', 13, caida), undefined);
  assert.equal(ciudadTomada({}, 'v', 12, caida), undefined);
});


test('esLlegadaNueva: sólo la llegada del remitente posterior al trato y con esa carga', () => {
  const nueva = { id: 41, kind: 'CARAVAN_ARRIVED', otherPlayerId: 'p', cargo: [{ resource: 'WOOD', amount: 50 }] };
  assert.equal(esLlegadaNueva(nueva, 'p', 40, 'WOOD', 50), true);
  // La de --mercado, más nueva que el trato si su fila aún no está: no corta la espera.
  assert.equal(esLlegadaNueva({ ...nueva, cargo: [{ resource: 'WOOD', amount: 10 }] }, 'p', 40, 'WOOD', 50), false);
  // La de una pasada anterior, con la misma carga: tampoco da por buena la nueva.
  assert.equal(esLlegadaNueva({ ...nueva, id: 12 }, 'p', 40, 'WOOD', 50), false);
  assert.equal(esLlegadaNueva({ ...nueva, id: 40 }, 'p', 40, 'WOOD', 50), false);
  assert.equal(esLlegadaNueva({ ...nueva, otherPlayerId: 'q' }, 'p', 40, 'WOOD', 50), false);
  assert.equal(esLlegadaNueva({ ...nueva, kind: 'CARAVAN_CAPTURED' }, 'p', 40, 'WOOD', 50), false);
  assert.equal(esLlegadaNueva({ ...nueva, cargo: undefined }, 'p', 40, 'WOOD', 50), false);
});

test('ultimoIdDelRegistro: el id más alto de la página, o 0 si está vacía', () => {
  assert.equal(ultimoIdDelRegistro({ entries: [{ id: 9 }, { id: 31 }, { id: 4 }] }), 31);
  assert.equal(ultimoIdDelRegistro({ entries: [] }), 0);
});
