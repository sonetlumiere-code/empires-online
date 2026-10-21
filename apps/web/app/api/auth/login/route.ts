/** `POST /api/auth/login` — ADR-018. La lógica vive en src/server. */
import { handleCredentials } from '../../../../src/server/http';
import { httpDeps } from '../../../../src/server/runtime';

export const runtime = 'nodejs';
export const dynamic = 'force-dynamic';

export function POST(req: Request): Promise<Response> {
  return handleCredentials(req, httpDeps(), 'login');
}
