/** `POST /api/auth/logout` — borra la sesión web (ADR-018). */
import { handleLogout } from '../../../../src/server/http';
import { httpDeps } from '../../../../src/server/runtime';

export const runtime = 'nodejs';
export const dynamic = 'force-dynamic';

export function POST(req: Request): Promise<Response> {
  return handleLogout(req, httpDeps());
}
