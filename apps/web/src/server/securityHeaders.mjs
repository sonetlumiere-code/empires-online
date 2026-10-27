/**
 * Cabeceras de seguridad de la web: la Content-Security-Policy por petición y las
 * cabeceras fijas (HSTS, nosniff, frame-ancestors, referrer, permissions).
 *
 * JavaScript plano a propósito, sin `server-only` ni nada de Node: lo importan
 * `next.config.mjs` (que Node carga tal cual, sin TypeScript) y `middleware.ts`,
 * que corre en el runtime edge. No lee secretos: todo lo que decide es público.
 *
 * Por qué la CSP lleva un nonce y no `'unsafe-inline'`: Next.js hidrata la
 * página con scripts en línea (`self.__next_f.push(...)`) y los permite de dos
 * formas, abriéndolo todo con `'unsafe-inline'` o marcando cada uno con el nonce
 * que lee de la propia cabecera `Content-Security-Policy` de la petición. Con el
 * nonce y `'strict-dynamic'`, un script inyectado en la página no se ejecuta,
 * que es lo que una CSP tiene que impedir. El precio: la página se renderiza en
 * cada petición (el nonce no puede ir prerenderizado), ver `app/layout.tsx`.
 */

/**
 * Origen (esquema y host) de la URL pública del WebSocket, para `connect-src`.
 * Si la URL no se puede leer, no se añade nada y la conexión queda bloqueada: una
 * configuración rota tiene que verse, no abrir la política.
 *
 * @param {string | undefined} wsUrl NEXT_PUBLIC_GAME_SERVER_WS_URL
 * @returns {string | null}
 */
export function webSocketOrigin(wsUrl) {
  if (!wsUrl) return null;
  try {
    const url = new URL(wsUrl);
    if (url.protocol !== 'ws:' && url.protocol !== 'wss:') return null;
    return `${url.protocol}//${url.host}`;
  } catch {
    return null;
  }
}

/**
 * @param {{ production: boolean, nonce: string, wsUrl: string | undefined }} opts
 *   `production` es NODE_ENV === 'production': en desarrollo Next necesita
 *   `'unsafe-eval'` (React Refresh y los source maps) y nada más.
 * @returns {string} el valor de la cabecera Content-Security-Policy
 */
export function contentSecurityPolicy({ production, nonce, wsUrl }) {
  const ws = webSocketOrigin(wsUrl);
  const directives = [
    `default-src 'self'`,
    // El nonce cubre los scripts que emite Next; 'strict-dynamic' extiende la
    // confianza a los chunks que esos scripts cargan y hace ignorar 'self' en
    // los navegadores que lo entienden (los antiguos se quedan con 'self').
    `script-src 'self' 'nonce-${nonce}' 'strict-dynamic'${production ? '' : " 'unsafe-eval'"}`,
    // React escribe estilos en atributos `style=` y Next inyecta <style> en
    // desarrollo: sin 'unsafe-inline' no se dibuja nada. Un estilo inyectado no
    // ejecuta código; es el único hueco que se deja abierto a sabiendas.
    `style-src 'self' 'unsafe-inline'`,
    // PixiJS sube texturas desde canvas y blobs; data: cubre iconos en línea.
    `img-src 'self' data: blob:`,
    `font-src 'self'`,
    // El WebSocket del Game Server vive en otro origen (otro puerto o dominio).
    // 'self' cubre el HMR de desarrollo, que conecta al mismo host.
    `connect-src 'self'${ws ? ` ${ws}` : ''}`,
    // PixiJS crea workers desde blobs para decodificar imágenes.
    `worker-src 'self' blob:`,
    `object-src 'none'`,
    `base-uri 'self'`,
    `form-action 'self'`,
    `frame-ancestors 'none'`,
    // Sin `upgrade-insecure-requests`: también reescribiría ws:// a wss:// y un
    // build de producción probado en local contra ws://localhost no conectaría.
    // El HTTPS lo garantizan HSTS y el proxy, no la CSP.
  ];
  return directives.join('; ');
}

/**
 * Cabeceras fijas para TODA respuesta, incluidos los estáticos de `_next/`.
 *
 * HSTS sólo en producción: un año, con subdominios, sin `preload` (entrar en la
 * lista de precarga es casi irreversible y se decide aparte). Los navegadores la
 * ignoran sobre HTTP, así que un `next start` local no se queda atrapado.
 *
 * @param {{ production: boolean }} opts
 * @returns {Array<{ key: string, value: string }>}
 */
export function staticSecurityHeaders({ production }) {
  return [
    { key: 'X-Content-Type-Options', value: 'nosniff' },
    // Redundante con frame-ancestors para los navegadores que no leen CSP.
    { key: 'X-Frame-Options', value: 'DENY' },
    { key: 'Referrer-Policy', value: 'strict-origin-when-cross-origin' },
    // El juego no usa nada de esto; cerrarlo evita que un script colado lo pida.
    { key: 'Permissions-Policy', value: 'camera=(), microphone=(), geolocation=(), payment=(), usb=()' },
    { key: 'Cross-Origin-Opener-Policy', value: 'same-origin' },
    ...(production ? [{ key: 'Strict-Transport-Security', value: 'max-age=31536000; includeSubDomains' }] : []),
  ];
}

/**
 * Nonce nuevo por petición: 16 bytes aleatorios en base64. Sólo Web Crypto,
 * que existe tanto en Node como en el runtime edge.
 *
 * @returns {string}
 */
export function newNonce() {
  const bytes = crypto.getRandomValues(new Uint8Array(16));
  let binary = '';
  for (const b of bytes) binary += String.fromCharCode(b);
  return btoa(binary);
}
