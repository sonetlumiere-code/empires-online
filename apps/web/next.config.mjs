/**
 * Configuración de Next.js.
 *
 * Nota arquitectónica: aquí NO vive ninguna parte de la simulación. Next.js sirve
 * la interfaz, guarda la identidad en su propio almacén y emite el game ticket
 * (ADR-018). La autoridad sobre el mundo es del Game Server en Go, siempre.
 *
 * Sólo las variables NEXT_PUBLIC_ llegan al navegador, y aquí sólo hay una: la
 * URL pública del WebSocket. Los secretos los lee el servidor en tiempo de
 * ejecución (src/server/config.ts), nunca el build.
 *
 * @type {import('next').NextConfig}
 */
const nextConfig = {
  reactStrictMode: true,

  // El paquete de protocolo se consume como TypeScript del propio monorepo.
  transpilePackages: ['@empires-online/protocol'],

  // node-postgres se carga tal cual en el servidor: no se empaqueta. Empaquetarlo
  // arrastra su cliente nativo opcional (pg-native) y el fs de sus opciones TLS.
  serverExternalPackages: ['pg'],

  env: {
    NEXT_PUBLIC_GAME_SERVER_WS_URL:
      process.env.NEXT_PUBLIC_GAME_SERVER_WS_URL ?? 'ws://localhost:8080/ws',
  },
};

export default nextConfig;
