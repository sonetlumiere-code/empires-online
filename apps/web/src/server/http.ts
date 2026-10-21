/**
 * Capa HTTP de `/api/auth/*`: lee la petición, aplica las defensas de borde y
 * traduce el resultado del `AuthService` a una respuesta con su cookie.
 *
 * La cookie de sesión (ADR-018 §2.3): opaca, `HttpOnly`, `SameSite=Strict`,
 * `Secure` en producción y limitada a `/api/auth`, que es la única ruta que la
 * necesita.
 */
import 'server-only';

import type { AuthResult, AuthService, Logger } from './authService';
import { SESSION_TTL_MS } from './authService';
import { RateLimiter, clientKey } from './rateLimit';

export const SESSION_COOKIE = 'eo_session';
const MAX_BODY_BYTES = 4096;

export interface HttpDeps {
  readonly service: AuthService;
  readonly limiter: RateLimiter;
  readonly trustProxyHeaders: boolean;
  readonly secureCookies: boolean;
  readonly nowMs: () => number;
  readonly log: Logger;
}

const json = (status: number, body: unknown, headers: Record<string, string> = {}) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json; charset=utf-8', 'Cache-Control': 'no-store', ...headers },
  });

const error = (status: number, code: string, message: string) => json(status, { code, message });

export function sessionCookie(token: string, secure: boolean): string {
  return [
    `${SESSION_COOKIE}=${token}`,
    'Path=/api/auth',
    'HttpOnly',
    'SameSite=Strict',
    `Max-Age=${Math.floor(SESSION_TTL_MS / 1000)}`,
    ...(secure ? ['Secure'] : []),
  ].join('; ');
}

export function clearedSessionCookie(secure: boolean): string {
  return [`${SESSION_COOKIE}=`, 'Path=/api/auth', 'HttpOnly', 'SameSite=Strict', 'Max-Age=0', ...(secure ? ['Secure'] : [])].join('; ');
}

export function readSessionCookie(req: Request): string | undefined {
  for (const part of (req.headers.get('cookie') ?? '').split(';')) {
    const [name, ...rest] = part.trim().split('=');
    if (name === SESSION_COOKIE) return rest.join('=') || undefined;
  }
  return undefined;
}

/**
 * Una petición con `Origin` de otro sitio se rechaza. `SameSite=Strict` ya impide
 * que viaje la cookie; esto cierra además el alta y el login entre sitios.
 */
function crossSite(req: Request): boolean {
  const origin = req.headers.get('origin');
  if (!origin) return false;
  try {
    return new URL(origin).host !== (req.headers.get('host') ?? new URL(req.url).host);
  } catch {
    return true;
  }
}

async function readCredentials(req: Request): Promise<{ username: string; password: string } | null> {
  if (!(req.headers.get('content-type') ?? '').toLowerCase().startsWith('application/json')) return null;
  const text = await req.text();
  if (text.length > MAX_BODY_BYTES) return null;
  try {
    const body = JSON.parse(text) as unknown;
    if (typeof body !== 'object' || body === null) return null;
    const { username, password } = body as Record<string, unknown>;
    if (typeof username !== 'string' || typeof password !== 'string') return null;
    return { username, password };
  } catch {
    return null;
  }
}

function toResponse(result: AuthResult, secure: boolean): Response {
  if (!result.ok) return error(result.status, result.code, result.message);
  const headers: Record<string, string> = result.sessionToken
    ? { 'Set-Cookie': sessionCookie(result.sessionToken, secure) }
    : {};
  return json(result.status, result.body, headers);
}

export async function handleCredentials(
  req: Request,
  deps: HttpDeps,
  action: 'register' | 'login',
): Promise<Response> {
  if (crossSite(req)) return error(403, 'FORBIDDEN', 'origen no permitido');
  // ANTES de leer el cuerpo y muy antes de scrypt: el objetivo es no gastar ese
  // trabajo. Una ráfaga de 429 desde un origen es la firma de alguien probando
  // contraseñas, y se registra.
  const key = clientKey(req.headers, deps.trustProxyHeaders);
  if (!deps.limiter.allow(key, deps.nowMs())) {
    deps.log.warn('petición rechazada por límite de tasa', { endpoint: action, origin_key: key });
    return error(429, 'RATE_LIMITED', 'demasiados intentos; espera unos segundos');
  }
  const creds = await readCredentials(req);
  if (!creds) return error(400, 'INVALID_MESSAGE', 'cuerpo inválido');
  try {
    const result =
      action === 'register'
        ? await deps.service.register(creds.username, creds.password)
        : await deps.service.login(creds.username, creds.password);
    return toResponse(result, deps.secureCookies);
  } catch (err) {
    deps.log.error(`${action} fallido`, { err: err instanceof Error ? err.message : String(err) });
    return error(500, 'INTERNAL_ERROR', 'error interno');
  }
}

export async function handleTicket(req: Request, deps: HttpDeps): Promise<Response> {
  if (crossSite(req)) return error(403, 'FORBIDDEN', 'origen no permitido');
  try {
    return toResponse(await deps.service.ticket(readSessionCookie(req)), deps.secureCookies);
  } catch (err) {
    deps.log.error('ticket fallido', { err: err instanceof Error ? err.message : String(err) });
    return error(500, 'INTERNAL_ERROR', 'error interno');
  }
}

export async function handleLogout(req: Request, deps: HttpDeps): Promise<Response> {
  if (crossSite(req)) return error(403, 'FORBIDDEN', 'origen no permitido');
  await deps.service.logout(readSessionCookie(req));
  return new Response(null, { status: 204, headers: { 'Set-Cookie': clearedSessionCookie(deps.secureCookies) } });
}
