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
 * el login, y no se guarda en ninguna parte.
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

/** Cierra la sesión web. */
export async function logout(): Promise<void> {
  await fetch('/api/auth/logout', { method: 'POST', credentials: 'same-origin' });
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
