/**
 * Cliente de la fundación interna del Game Server (ADR-018 §2.5).
 *
 * `POST /internal/players` con un token de la audiencia interna cuyo `sub` es el
 * jugador que se funda. Traduce cada respuesta a una de tres salidas, que es lo
 * único que necesita saber la orquestación del alta:
 *
 *   founded      el jugador existe en el mundo (recién creado o ya estaba)
 *   rejected     el Game Server dijo que no y repetir no cambia nada
 *   unavailable  no se sabe: red, 5xx, o una configuración rota entre los dos
 */
import 'server-only';

import { issueFoundingToken } from './tokens';

export interface CityInfo {
  readonly id: number;
  readonly name: string;
  readonly centerX: number;
  readonly centerY: number;
}

export type FoundResult =
  | { readonly kind: 'founded'; readonly city: CityInfo }
  | { readonly kind: 'rejected'; readonly status: number; readonly code: string; readonly message: string }
  | { readonly kind: 'unavailable'; readonly reason: string };

export interface GameServerClient {
  found(playerId: string, username: string): Promise<FoundResult>;
}

export interface GameServerClientOptions {
  readonly baseUrl: string;
  readonly secret: string;
  readonly nowMs: () => number;
  readonly randomUUID: () => string;
  readonly fetch?: typeof fetch;
  readonly timeoutMs?: number;
}

/** Códigos con los que el Game Server rechaza de forma definitiva. */
const DEFINITIVE = new Set(['USERNAME_TAKEN', 'NO_SITE_AVAILABLE', 'SITE_CONTENDED', 'INVALID_USERNAME']);

export function createGameServerClient(opts: GameServerClientOptions): GameServerClient {
  const doFetch = opts.fetch ?? fetch;
  return {
    async found(playerId, username) {
      const token = issueFoundingToken({
        playerId,
        secret: opts.secret,
        nowMs: opts.nowMs(),
        jti: opts.randomUUID(),
      });
      let response: Response;
      try {
        response = await doFetch(`${opts.baseUrl}/internal/players`, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${token}` },
          body: JSON.stringify({ playerId, username }),
          signal: AbortSignal.timeout(opts.timeoutMs ?? 10_000),
        });
      } catch (err) {
        return { kind: 'unavailable', reason: `sin respuesta del Game Server: ${(err as Error).name}` };
      }

      const body = (await response.json().catch(() => null)) as
        | { city?: CityInfo; code?: string; message?: string }
        | null;

      if ((response.status === 200 || response.status === 201) && body?.city) {
        return { kind: 'founded', city: body.city };
      }
      const code = body?.code ?? 'INTERNAL_ERROR';
      if (DEFINITIVE.has(code)) {
        return { kind: 'rejected', status: response.status, code, message: body?.message ?? code };
      }
      // 401/403 aquí no es culpa del jugador: los dos procesos no comparten el
      // secreto, o el reloj de uno va desfasado. Se trata como indisponible para
      // que la cuenta quede PENDING y se pueda terminar cuando se arregle.
      return { kind: 'unavailable', reason: `el Game Server respondió ${response.status} ${code}` };
    },
  };
}
