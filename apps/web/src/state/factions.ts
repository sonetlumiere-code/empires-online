/**
 * Las facciones en el cliente (SPEC-FACTIONS): su nombre, lo que da y cuesta
 * cada bando para la ventana «Elegir bando», la cuenta atrás de una entrada y las
 * reglas que el HUD necesita para no ofrecer órdenes que el servidor rechazará.
 *
 * No decide nada: la paz de la Orden, los tratados entre bandos y el daño en
 * tierra afín los aplica el servidor (ADR-039). El 20 % es una copia del
 * catálogo del servidor (`EO_FACTION_AFFINE_DAMAGE_PERMILLE`), sólo para el
 * texto: el daño real llega en los `hp`.
 * Ver ../../../../docs/specs/factions.md
 */
import type { TreatyView } from './diplomacy';
import type { CityView, TerritoryView } from './world';

export type FactionCode = 'ORDER' | 'CHAOS' | 'NEUTRAL';
export type JoinableFaction = 'ORDER' | 'CHAOS';

/** Lo que reduce la tierra afín el daño recibido, en porcentaje (SPEC-FACTIONS Q6). */
export const AFFINE_DAMAGE_PERCENT = 20;

const LABEL: Readonly<Record<string, string>> = { ORDER: 'Orden', CHAOS: 'Caos', NEUTRAL: 'Neutral' };

/** El nombre de un bando («Orden»), o el código si no se conoce. */
export function factionLabel(code: string | null | undefined): string {
  if (!code) return 'Neutral';
  return LABEL[code] ?? code;
}

/** «la Orden», «el Caos»: el bando con su artículo, para las frases. */
export function factionWithArticle(code: string): string {
  if (code === 'ORDER') return 'la Orden';
  if (code === 'CHAOS') return 'el Caos';
  return factionLabel(code);
}

export interface FactionChoice {
  readonly code: JoinableFaction;
  readonly name: string;
  /** Lo que da y lo que cuesta, en frases del §4.1 de la spec. */
  readonly lines: readonly { readonly text: string; readonly good: boolean }[];
}

/** Los dos bandos a los que se puede entrar, con lo que da y cuesta cada uno (§4.6). */
export const FACTION_CHOICES: readonly FactionChoice[] = [
  {
    code: 'ORDER',
    name: 'Orden',
    lines: [
      { text: 'No puedes atacar a otros de la Orden, ni ellos a ti', good: true },
      { text: `En tierra de la Orden tus unidades reciben un ${AFFINE_DAMAGE_PERCENT} % menos de daño`, good: true },
      { text: 'No puedes saquear, derribar ni conquistar a otros de la Orden', good: false },
      { text: 'No puedes firmar un pacto ni una alianza con el Caos (el comercio, sí)', good: false },
    ],
  },
  {
    code: 'CHAOS',
    name: 'Caos',
    lines: [
      { text: 'Puedes atacar a cualquiera, también a otros del Caos', good: true },
      { text: `En tierra del Caos tus unidades reciben un ${AFFINE_DAMAGE_PERCENT} % menos de daño`, good: true },
      { text: 'Otros del Caos también pueden atacarte', good: false },
      { text: 'No puedes firmar un pacto ni una alianza con la Orden (el comercio, sí)', good: false },
    ],
  },
];

/** Si la ciudad propia puede pedir unirse a un bando: es NEUTRAL y no tiene entrada (RN-FAC-001). */
export function canJoinFaction(city: Pick<CityView, 'faction' | 'factionJoin'> | undefined): boolean {
  return city !== undefined && (city.faction ?? 'NEUTRAL') === 'NEUTRAL' && city.factionJoin === null;
}

/** mm:ss que faltan hasta un instante; 0:00 si ya pasó. */
export function countdown(atMs: number, nowMs: number): string {
  const s = Math.max(0, Math.ceil((atMs - nowMs) / 1000));
  return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, '0')}`;
}

/** «Te unes a la Orden en 12:30» o, para otra ciudad, «se une al Caos en 3:05». */
export function joinLabel(join: { faction: string; atMs: number }, nowMs: number, own: boolean): string {
  const bando = factionWithArticle(join.faction);
  const art = bando.startsWith('el ') ? `al ${bando.slice(3)}` : `a ${bando}`;
  return `${own ? 'Te unes' : 'Se une'} ${art} en ${countdown(join.atMs, nowMs)}`;
}

/** Si dos jugadores están en paz por su bando: los dos son de la Orden (RN-FAC-003). */
export function orderPeace(a: string | undefined, b: string | undefined): boolean {
  return a === 'ORDER' && b === 'ORDER';
}

/** Si dos bandos son contrarios: entre ellos no hay pacto ni alianza (RN-FAC-005). */
export function opposed(a: string | undefined, b: string | undefined): boolean {
  return (a === 'ORDER' && b === 'CHAOS') || (a === 'CHAOS' && b === 'ORDER');
}

/** Si un tipo de tratado está prohibido entre dos bandos. */
export function treatyForbidden(type: string, a: string | undefined, b: string | undefined): boolean {
  return (type === 'NON_AGGRESSION' || type === 'ALLIANCE') && opposed(a, b);
}

/** Lo que hace falta de un territorio para su línea de bando y de tierra afín. */
type AffineTerritory = Pick<TerritoryView, 'ownerType' | 'ownerId' | 'ownerFaction'> & {
  ownerClan?: TerritoryView['ownerClan'];
};

/**
 * Si un territorio es afín al jugador (RN-FAC-007): es suyo, de alguien de su bando
 * si el jugador es de uno, o de su clan (RN-CLANTERR-003).
 */
export function isAffine(t: AffineTerritory, me: string | null, myFaction: string | undefined, myClanId: number | null = null): boolean {
  if (t.ownerType === 'CLAN') return !!t.ownerClan && myClanId !== null && t.ownerClan.id === myClanId;
  if (t.ownerType !== 'PLAYER' || t.ownerId === null) return false;
  if (t.ownerId === me) return true;
  const mine = myFaction ?? 'NEUTRAL';
  return mine !== 'NEUTRAL' && t.ownerFaction === mine;
}

/**
 * La línea del territorio sobre su bando y la tierra afín (§4.6), o null sin jugador ni
 * clan por dueño. Un territorio de clan no tiene bando: sólo dice si es tierra afín
 * (SPEC-CLAN-TERRITORIES §4.8).
 */
export function territoryFactionLine(t: AffineTerritory, me: string | null, myFaction: string | undefined, myClanId: number | null = null): string | null {
  if (t.ownerType === 'CLAN') {
    return isAffine(t, me, myFaction, myClanId)
      ? `Tierra afín: tus unidades reciben un ${AFFINE_DAMAGE_PERCENT} % menos de daño`
      : null;
  }
  if (t.ownerType !== 'PLAYER' || t.ownerId === null) return null;
  const bando = `Bando del dueño: ${factionLabel(t.ownerFaction)}`;
  return isAffine(t, me, myFaction)
    ? `${bando}. Tierra afín: tus unidades reciben un ${AFFINE_DAMAGE_PERCENT} % menos de daño`
    : bando;
}

/**
 * Los pactos y alianzas vigentes sin preaviso que se romperán al entrar en un
 * bando (RN-FAC-006), para enumerarlos antes de confirmar: los del jugador con
 * alguien del bando contrario.
 */
export function treatiesBrokenByJoining(
  treaties: readonly TreatyView[],
  cities: Iterable<Pick<CityView, 'ownerPlayerId' | 'faction'>>,
  target: JoinableFaction,
): TreatyView[] {
  const factionOf = new Map<string, string>();
  for (const c of cities) factionOf.set(c.ownerPlayerId, c.faction ?? 'NEUTRAL');
  return treaties.filter(
    (t) =>
      t.status === 'ACTIVE' &&
      (t.type === 'NON_AGGRESSION' || t.type === 'ALLIANCE') &&
      (t.breaksAtMs === null || t.breaksAtMs === undefined) &&
      opposed(target, factionOf.get(t.otherPlayerId)),
  );
}
