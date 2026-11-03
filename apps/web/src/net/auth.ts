/**
 * Alta, entrada y obtención del game ticket desde el navegador.
 *
 * Un ticket es EFÍMERO (60 s) y de UN SOLO USO: cada conexión —y cada
 * reconexión— necesita uno nuevo. Guardarlo o reutilizarlo no funciona por
 * diseño, no por descuido. Ver ../../../../docs/decisions/ADR-010-authentication-game-ticket.md
 *
 * Las rutas son de este mismo origen (`/api/auth/*`, ADR-018): el alta y el
 * login dejan una cookie de sesión `HttpOnly` que este código no puede leer, y
 * cada ticket nuevo se pide con ella. La contraseña sólo viaja en el alta y en
 * el login, y no se guarda en ninguna parte. Por eso una recarga no pide entrar
 * otra vez: con la cookie viva basta pedir un ticket (`resumeSession`).
 */

export interface Credentials {
  readonly username: string;
  readonly password: string;
}

export interface TicketResponse {
  readonly playerId: string;
  readonly ticket: string;
  readonly expiresInSeconds: number;
  readonly city?: {
    readonly id: number;
    readonly name: string;
    readonly centerX: number;
    readonly centerY: number;
  };
}

export class AuthError extends Error {
  constructor(
    readonly code: string,
    message: string,
  ) {
    super(message);
    this.name = 'AuthError';
  }
}

async function post(path: string, body?: unknown): Promise<TicketResponse> {
  const response = await fetch(path, {
    method: 'POST',
    credentials: 'same-origin',
    ...(body === undefined
      ? {}
      : { headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) }),
  });

  const payload = (await response.json().catch(() => null)) as
    | (TicketResponse & { code?: string; message?: string })
    | null;

  if (!response.ok || !payload) {
    throw new AuthError(payload?.code ?? 'INTERNAL_ERROR', payload?.message ?? 'error inesperado');
  }
  return payload;
}

/** Da de alta un jugador: crea su ciudad y sus tres aldeanos, y abre la sesión web. */
export function register(creds: Credentials): Promise<TicketResponse> {
  return post('/api/auth/register', creds);
}

/** Verifica credenciales, abre la sesión web y devuelve un ticket. */
export function login(creds: Credentials): Promise<TicketResponse> {
  return post('/api/auth/login', creds);
}

/** Pide un ticket nuevo con la cookie de sesión. */
export async function requestTicket(): Promise<string> {
  const { ticket } = await post('/api/auth/ticket');
  return ticket;
}

/**
 * Retoma la sesión web al cargar la página: si la cookie sigue viva devuelve un
 * ticket para conectar sin pasar por el formulario; si no hay sesión
 * (`UNAUTHORIZED`), `null`. Cualquier otro fallo —red, `429`, `500`— se lanza:
 * no dice que la sesión haya muerto, sólo que hoy no se pudo comprobar.
 */
export async function resumeSession(): Promise<string | null> {
  try {
    return await requestTicket();
  } catch (err) {
    if (err instanceof AuthError && err.code === 'UNAUTHORIZED') return null;
    throw err;
  }
}

/**
 * Cierra la sesión web: el servidor la borra y caduca la cookie. Si no lo
 * confirma, lanza: la cookie podría seguir viva y una recarga volvería a entrar.
 */
export async function logout(): Promise<void> {
  const response = await fetch('/api/auth/logout', { method: 'POST', credentials: 'same-origin' });
  if (response.ok) return;
  const payload = (await response.json().catch(() => null)) as { code?: string; message?: string } | null;
  throw new AuthError(payload?.code ?? 'INTERNAL_ERROR', payload?.message ?? 'error inesperado');
}

/**
 * Construye la función que el cliente WebSocket usa para pedir un ticket fresco
 * en cada (re)conexión. La primera conexión usa el ticket que ya trajo el alta o
 * el login; las siguientes lo piden con la cookie.
 */
export function ticketProvider(first?: string): () => Promise<string> {
  let pending = first;
  return async () => {
    if (pending) {
      const ticket = pending;
      pending = undefined;
      return ticket;
    }
    return requestTicket();
  };
}
