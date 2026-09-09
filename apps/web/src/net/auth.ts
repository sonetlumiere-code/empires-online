/**
 * Obtención del game ticket.
 *
 * Un ticket es EFÍMERO (60 s) y de UN SOLO USO: cada conexión —y cada
 * reconexión— necesita uno nuevo. Guardarlo o reutilizarlo no funciona por
 * diseño, no por descuido. Ver ../../../../docs/decisions/ADR-010-authentication-game-ticket.md
 *
 * Provisional: hoy estos endpoints los sirve el Game Server
 * (`internal/httpapi`). La arquitectura objetivo los traslada a una API route de
 * Next.js, que es quien debe custodiar el secreto de firma. Cuando eso ocurra,
 * sólo cambia `BASE_URL`: la interfaz de este módulo no se mueve.
 */

const BASE_URL = process.env.NEXT_PUBLIC_GAME_SERVER_HTTP_URL ?? 'http://localhost:8080';

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

async function post(path: string, body: unknown): Promise<TicketResponse> {
  const response = await fetch(`${BASE_URL}${path}`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });

  const payload = (await response.json().catch(() => null)) as
    | (TicketResponse & { code?: string; message?: string })
    | null;

  if (!response.ok || !payload) {
    throw new AuthError(payload?.code ?? 'INTERNAL_ERROR', payload?.message ?? 'error inesperado');
  }
  return payload;
}

/** Da de alta un jugador: crea su ciudad y sus tres aldeanos. */
export function register(creds: Credentials): Promise<TicketResponse> {
  return post('/api/auth/register', creds);
}

/** Verifica credenciales y devuelve un ticket nuevo. */
export function login(creds: Credentials): Promise<TicketResponse> {
  return post('/api/auth/login', creds);
}

/**
 * Construye la función que el cliente WebSocket usa para pedir un ticket fresco
 * en cada (re)conexión.
 *
 * Guardar las credenciales en memoria es el compromiso del MVP: la alternativa
 * correcta —cookie de sesión en Next.js— llega con ADR-010, y esta función es
 * justo la costura por la que se sustituirá.
 */
export function ticketProvider(creds: Credentials): () => Promise<string> {
  return async () => {
    const { ticket } = await login(creds);
    return ticket;
  };
}
