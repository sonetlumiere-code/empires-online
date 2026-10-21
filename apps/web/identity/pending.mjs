/**
 * Cuentas PENDING abandonadas (ADR-020).
 *
 * Una cuenta PENDING es un alta que no terminó: el Game Server no confirmó la
 * fundación. Puede ser que no fundara nada, o que fundara y la respuesta se
 * perdiera. Por eso una cuenta abandonada nunca se borra a ciegas: se pregunta al
 * mundo si su jugador existe. Si existe, la cuenta se activa; si no, se borra;
 * si no contesta, se deja (INV-PLAYER-010).
 *
 * Módulo puro, sin `server-only`: lo usan el servidor de Next.js (a través de
 * `src/server/store.ts`, `gameServer.ts` y `authService.ts`) y el comando
 * `pnpm run identity:sweep`. El SQL de las cuentas pendientes vive sólo aquí.
 */
import { issueFoundingToken } from './tokens.mjs';

/**
 * Antigüedad del último intento de fundación a partir de la cual una cuenta
 * PENDING es abandonada. La fundación tiene un plazo de 10 s: ninguna iniciada
 * antes del corte puede seguir en vuelo.
 */
export const PENDING_STALE_AFTER_MS = 60 * 60 * 1000;

/** @typedef {{ query: (text: string, values?: unknown[]) => Promise<{ rows: any[], rowCount?: number | null }> }} Db */
/** @typedef {{ id: string, username: string }} PendingAccount */

const STALE = `founding_attempted_at < now() - make_interval(secs => $2::double precision / 1000)`;

/**
 * Renueva el intento de fundación de una cuenta que sigue PENDING. Lo hace el
 * login ANTES de llamar al Game Server: así un barrido concurrente ya no puede
 * borrarla (su guarda exige un intento antiguo).
 *
 * @param {Db} db
 * @param {string} id
 * @returns {Promise<'pending' | 'active' | 'gone'>} el estado de la cuenta tras intentarlo
 */
export async function markFoundingAttempt(db, id) {
  const touched = await db.query(
    `UPDATE accounts SET founding_attempted_at = now() WHERE id = $1 AND status = 'PENDING'`,
    [id],
  );
  if ((touched.rowCount ?? 0) > 0) return 'pending';
  const { rows } = await db.query('SELECT status FROM accounts WHERE id = $1', [id]);
  return rows[0]?.status === 'ACTIVE' ? 'active' : 'gone';
}

/**
 * PENDING → ACTIVE, sólo después de que el Game Server confirme el jugador
 * (INV-PLAYER-009). No hace nada si la cuenta ya no es PENDING.
 * @param {Db} db
 * @param {string} id
 */
export async function activatePending(db, id) {
  await db.query(`UPDATE accounts SET status = 'ACTIVE', activated_at = now() WHERE id = $1 AND status = 'PENDING'`, [
    id,
  ]);
}

/**
 * La cuenta PENDING abandonada con ese nombre, o null.
 * @param {Db} db
 * @param {string} username
 * @param {number} [staleMs]
 * @returns {Promise<PendingAccount | null>}
 */
export async function findStalePending(db, username, staleMs = PENDING_STALE_AFTER_MS) {
  const { rows } = await db.query(
    `SELECT id, username FROM accounts WHERE username = $1 AND status = 'PENDING' AND ${STALE}`,
    [username, staleMs],
  );
  return rows[0] ?? null;
}

/**
 * Las cuentas PENDING abandonadas, de la más antigua a la más reciente.
 * @param {Db} db
 * @param {number} limit
 * @param {number} [staleMs]
 * @returns {Promise<PendingAccount[]>}
 */
export async function listStalePending(db, limit, staleMs = PENDING_STALE_AFTER_MS) {
  const { rows } = await db.query(
    `SELECT id, username FROM accounts WHERE status = 'PENDING' AND ${STALE}
      ORDER BY founding_attempted_at, id LIMIT $1`,
    [limit, staleMs],
  );
  return rows;
}

/**
 * Borra la cuenta sólo si sigue PENDING y abandonada. La guarda va en la propia
 * sentencia: un login que renovó el intento entretanto lo impide.
 * @param {Db} db
 * @param {string} id
 * @param {number} [staleMs]
 * @returns {Promise<boolean>} si la borró
 */
export async function deleteStalePending(db, id, staleMs = PENDING_STALE_AFTER_MS) {
  const result = await db.query(`DELETE FROM accounts WHERE id = $1 AND status = 'PENDING' AND ${STALE}`, [
    id,
    staleMs,
  ]);
  return (result.rowCount ?? 0) > 0;
}

/**
 * @typedef {{ kind: 'exists', username: string }
 *   | { kind: 'absent' }
 *   | { kind: 'unavailable', reason: string }} LookupResult
 */

/**
 * `GET /internal/players/{id}` del Game Server, con un token de fundación cuyo
 * `sub` es ese jugador. Sólo consulta: nunca funda.
 *
 * @param {{ baseUrl: string, secret: string, nowMs: () => number, randomUUID: () => string,
 *           fetch?: typeof fetch, timeoutMs?: number }} opts
 * @param {string} playerId
 * @returns {Promise<LookupResult>}
 */
export async function lookupPlayer(opts, playerId) {
  const token = issueFoundingToken({
    playerId,
    secret: opts.secret,
    nowMs: opts.nowMs(),
    jti: opts.randomUUID(),
  });
  const doFetch = opts.fetch ?? fetch;
  let response;
  try {
    response = await doFetch(`${opts.baseUrl}/internal/players/${encodeURIComponent(playerId)}`, {
      method: 'GET',
      headers: { Authorization: `Bearer ${token}` },
      signal: AbortSignal.timeout(opts.timeoutMs ?? 10_000),
    });
  } catch (err) {
    return { kind: 'unavailable', reason: `sin respuesta del Game Server: ${/** @type {Error} */ (err).name}` };
  }
  const body = await response.json().catch(() => null);
  if (response.status === 200 && body && typeof body.username === 'string' && body.playerId === playerId) {
    return { kind: 'exists', username: body.username };
  }
  if (response.status === 404 && body?.code === 'PLAYER_NOT_FOUND') return { kind: 'absent' };
  // Cualquier otra cosa —401 por secretos desincronizados, 5xx, una respuesta
  // que no cuadra— es «no se sabe»: con duda, la cuenta no se toca.
  return { kind: 'unavailable', reason: `el Game Server respondió ${response.status} ${body?.code ?? ''}`.trim() };
}

/**
 * @typedef {object} SettleDeps
 * @property {(id: string) => Promise<LookupResult>} lookup
 * @property {(id: string) => Promise<void>} activate      PENDING → ACTIVE
 * @property {(id: string) => Promise<boolean>} removeIfStale borra si sigue PENDING y abandonada
 * @property {{ info: Function, warn: Function }} log
 */

/**
 * Resuelve una cuenta PENDING abandonada (ADR-020 §2.2).
 *
 * @param {SettleDeps} deps
 * @param {PendingAccount} account
 * @returns {Promise<'activated' | 'deleted' | 'kept' | 'unavailable'>} `kept` si la cuenta cambió mientras
 *   se consultaba; `unavailable` si el Game Server no contestó
 */
export async function settleStalePending(deps, account) {
  const found = await deps.lookup(account.id);
  if (found.kind === 'exists') {
    // El alta sí terminó: sólo se perdió la respuesta. El nombre sigue siendo suyo.
    await deps.activate(account.id);
    deps.log.info('cuenta pendiente resuelta: su jugador existe, se activa', {
      player_id: account.id,
      username: account.username,
    });
    return 'activated';
  }
  if (found.kind === 'absent') {
    // Ningún jugador la reclama: se borra, si nadie la ha tocado entretanto.
    const removed = await deps.removeIfStale(account.id);
    deps.log.info(
      removed
        ? 'cuenta pendiente resuelta: sin jugador, se borra'
        : 'cuenta pendiente no borrada: cambió mientras se consultaba',
      { player_id: account.id, username: account.username },
    );
    return removed ? 'deleted' : 'kept';
  }
  deps.log.warn('cuenta pendiente sin resolver: el Game Server no contestó', {
    player_id: account.id,
    username: account.username,
    reason: found.reason,
  });
  return 'unavailable';
}

/**
 * Resuelve hasta `limit` cuentas abandonadas, de la más antigua a la más
 * reciente y de una en una (ADR-020 §2.3).
 *
 * @param {SettleDeps & { list: (limit: number) => Promise<PendingAccount[]> }} deps
 * @param {number} limit
 * @returns {Promise<Record<'activated' | 'deleted' | 'kept' | 'unavailable', number>>}
 */
export async function sweepStalePending(deps, limit) {
  const report = { activated: 0, deleted: 0, kept: 0, unavailable: 0 };
  for (const account of await deps.list(limit)) {
    report[await settleStalePending(deps, account)] += 1;
  }
  return report;
}
