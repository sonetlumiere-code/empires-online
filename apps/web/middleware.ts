/**
 * Content-Security-Policy con nonce por petición para las páginas.
 *
 * El nonce viaja en la cabecera `Content-Security-Policy` de la PETICIÓN, que es
 * de donde Next.js lo lee para marcar sus scripts en línea y sus chunks, y se
 * repite en la respuesta, que es lo que aplica el navegador. El resto de
 * cabeceras de seguridad no cambia por petición y vive en `next.config.mjs`.
 *
 * Corre en el runtime edge: sólo Web APIs y el módulo plano de cabeceras.
 */
import { NextResponse, type NextRequest } from 'next/server';

import { contentSecurityPolicy, newNonce } from './src/server/securityHeaders.mjs';

export function middleware(request: NextRequest): NextResponse {
  const nonce = newNonce();
  const csp = contentSecurityPolicy({
    production: process.env.NODE_ENV === 'production',
    nonce,
    wsUrl: process.env.NEXT_PUBLIC_GAME_SERVER_WS_URL ?? 'ws://localhost:8080/ws',
  });

  const requestHeaders = new Headers(request.headers);
  requestHeaders.set('x-nonce', nonce);
  requestHeaders.set('Content-Security-Policy', csp);

  const response = NextResponse.next({ request: { headers: requestHeaders } });
  response.headers.set('Content-Security-Policy', csp);
  return response;
}

export const config = {
  matcher: [
    {
      // Páginas y rutas de la API; los estáticos de Next y el favicon no llevan
      // documento que proteger, y las precargas del router no son navegaciones.
      source: '/((?!_next/static|_next/image|favicon.ico).*)',
      missing: [
        { type: 'header', key: 'next-router-prefetch' },
        { type: 'header', key: 'purpose', value: 'prefetch' },
      ],
    },
  ],
};
