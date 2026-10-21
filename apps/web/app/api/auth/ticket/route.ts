/** `POST /api/auth/ticket` — un game ticket nuevo para la sesión de la cookie (ADR-018). */
import { handleTicket } from '../../../../src/server/http';
import { httpDeps } from '../../../../src/server/runtime';

export const runtime = 'nodejs';
export const dynamic = 'force-dynamic';

export function POST(req: Request): Promise<Response> {
  return handleTicket(req, httpDeps());
}
