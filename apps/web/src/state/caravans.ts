/**
 * Las caravanas en el cliente (SPEC-CARAVANS).
 *
 * Como el resto del estado, no deciden nada: describen lo que contó el servidor.
 * La lista viene de `caravans[]` del snapshot y de `caravan.update`, y sólo trae
 * las caravanas que el jugador ve con su carga —las suyas, las que le traen algo y
 * las que capturó— (RN-CARAVAN-009). Las demás se ven en el mapa como unidades
 * `CARAVAN`, sin carga.
 * Ver ../../../../docs/specs/caravans.md
 */
import type { v1 } from '@empires-online/protocol';

import { describeLines } from './trade';

export type CaravanView = v1.CaravanView;

/**
 * Aplica un caravan.update a la lista: la añade o la sustituye mientras está en
 * camino, y la quita cuando llega o se pierde. Devuelve una lista nueva, por unidad.
 */
export function applyCaravanUpdate(list: readonly CaravanView[], c: CaravanView): CaravanView[] {
  const rest = list.filter((x) => x.unitId !== c.unitId);
  if (c.status === 'IN_TRANSIT') rest.push({ ...c, cargo: [...c.cargo] });
  return rest.sort((a, b) => a.unitId - b.unitId);
}

/** «2 min», «45 s»: lo que falta hasta la llegada; nunca negativo. */
export function formatEta(ms: number): string {
  const s = Math.max(0, Math.ceil(ms / 1000));
  if (s < 60) return `${s} s`;
  return `${Math.ceil(s / 60)} min`;
}

/**
 * El texto de una caravana para «Caravanas en camino», desde el punto de vista del
 * jugador: la suya, la que le traen, la que capturó o la suya capturada.
 */
export function caravanLabel(c: CaravanView, playerId: string | null, serverNowMs: number): string {
  const carga = describeLines(c.cargo);
  const destino = c.toCityName ?? 'otra ciudad';
  const origen = c.fromCityName ?? 'otra ciudad';
  const eta = c.arrivesAtMs !== null ? ` (llega en ${formatEta(c.arrivesAtMs - serverNowMs)})` : '';
  const mia = c.ownerPlayerId === playerId;
  const capturada = c.toCityId !== c.originalToCityId;
  if (mia && capturada && c.senderPlayerId !== playerId) {
    return `Caravana capturada con ${carga} hacia ${destino}${eta}`;
  }
  if (mia) return `Tu caravana con ${carga} hacia ${destino}${eta}`;
  if (capturada) return `Te capturaron una caravana con ${carga}: va hacia ${destino}${eta}`;
  return `Caravana de ${origen} con ${carga} hacia tu ciudad${eta}`;
}
