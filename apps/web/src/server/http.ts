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
import type { AuthRateLimits, Denied } from './rateLimit';

export const SESSION_COOKIE = 'eo_session';
const MAX_BODY_BYTES = 4096;

export interface HttpDeps {
  readonly service: AuthService;
  readonly limits: AuthRateLimits;
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

/**
 * Una ráfaga de 429 es la firma de alguien probando contraseñas o de un cliente
 * roto, y se registra con el cubo que la cortó. La clave de una sesión ya llega
 * aquí como hash: el token no se escribe en ningún log (INV-PLAYER-007).
 */
function rateLimited(deps: HttpDeps, endpoint: string, denied: Denied): Response {
  deps.log.warn('petición rechazada por límite de tasa', { endpoint, bucket: denied.bucket, key: denied.key });
  return error(429, 'RATE_LIMITED', 'demasiados intentos; espera unos segundos');
}

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

/**
 * Lee el cuerpo con tope, en bytes, que es lo que cuesta recibirlos. Con
 * `Content-Length` se rechaza antes de leer un solo byte; sin ella (transferencia
 * por trozos) se lee hasta el tope y ni uno más: un cuerpo de un gigabyte no se
 * acumula en memoria para descubrir después que no cabía. Null si se pasa.
 */
async function readBoundedBody(req: Request, maxBytes: number): Promise<string | null> {
  const declared = req.headers.get('content-length');
  if (declared !== null && (!/^\d+$/.test(declared) || Number(declared) > maxBytes)) return null;
  if (!req.body) return '';
  const reader = req.body.getReader();
  const chunks: Uint8Array[] = [];
  let received = 0;
  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    received += value.byteLength;
    if (received > maxBytes) {
      // Lo que quede se descarta sin leerlo; a quien lo mandó no se le espera.
      void reader.cancel().catch(() => undefined);
      return null;
    }
    chunks.push(value);
  }
  return Buffer.concat(chunks).toString('utf8');
}

async function readCredentials(req: Request): Promise<{ username: string; password: string } | null> {
  if (!(req.headers.get('content-type') ?? '').toLowerCase().startsWith('application/json')) return null;
  const text = await readBoundedBody(req, MAX_BODY_BYTES);
  if (text === null) return null;
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
  // La IP y el tope global, ANTES de leer el cuerpo y muy antes de scrypt: el
  // objetivo es no gastar ese trabajo.
  const denied = deps.limits.admitCredentials(req.headers, deps.nowMs());
  if (denied) return rateLimited(deps, action, denied);
  const creds = await readCredentials(req);
  if (!creds) return error(400, 'INVALID_MESSAGE', 'cuerpo inválido');
  // El cupo del nombre de usuario necesita el cuerpo, pero sigue yendo antes de
  // scrypt: es lo que separa una contraseña de la fuerza bruta repartida.
  const deniedUser = deps.limits.admitUser(creds.username, deps.nowMs());
  if (deniedUser) return rateLimited(deps, action, deniedUser);
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
  const token = readSessionCookie(req);
  const denied = deps.limits.admitSession(req.headers, token, deps.nowMs());
  if (denied) return rateLimited(deps, 'ticket', denied);
  try {
    return toResponse(await deps.service.ticket(token), deps.secureCookies);
  } catch (err) {
    deps.log.error('ticket fallido', { err: err instanceof Error ? err.message : String(err) });
    return error(500, 'INTERNAL_ERROR', 'error interno');
  }
}

export async function handleLogout(req: Request, deps: HttpDeps): Promise<Response> {
  if (crossSite(req)) return error(403, 'FORBIDDEN', 'origen no permitido');
  const token = readSessionCookie(req);
  const denied = deps.limits.admitSession(req.headers, token, deps.nowMs());
  if (denied) return rateLimited(deps, 'logout', denied);
  await deps.service.logout(token);
  return new Response(null, { status: 204, headers: { 'Set-Cookie': clearedSessionCookie(deps.secureCookies) } });
}
