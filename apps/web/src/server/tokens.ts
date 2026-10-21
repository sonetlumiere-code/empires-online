/**
 * Emisión del game ticket y del token de fundación (ADR-010, ADR-018 §2.4).
 *
 * La firma vive en `identity/tokens.mjs`, que comparten el servidor y los
 * comandos de operación; este módulo la expone al servidor de Next.js detrás de
 * `server-only`, para que ningún componente de cliente pueda importarla.
 * El formato lo fija un fixture compartido con el Game Server (`tokens.test.ts`).
 */
import 'server-only';

export {
  GAME_AUDIENCE,
  INTERNAL_AUDIENCE,
  TOKEN_TTL_SECONDS,
  issueFoundingToken,
  issueGameTicket,
} from '../../identity/tokens.mjs';

export interface TokenInput {
  readonly playerId: string;
  readonly secret: string;
  readonly nowMs: number;
  /** Identificador de un solo uso. Aleatorio en producción; fijo en el fixture. */
  readonly jti: string;
}
