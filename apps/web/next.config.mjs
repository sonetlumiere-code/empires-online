/**
 * Configuración de Next.js.
 *
 * Nota arquitectónica: aquí NO vive ninguna parte de la simulación. Next.js sirve
 * la interfaz y (más adelante, ADR-010) emitirá el game ticket. La autoridad
 * sobre el mundo es del Game Server en Go, siempre.
 *
 * @type {import('next').NextConfig}
 */
const nextConfig = {
  reactStrictMode: true,

  // El paquete de protocolo se consume como TypeScript del propio monorepo.
  transpilePackages: ['@empires-online/protocol'],

  env: {
    NEXT_PUBLIC_GAME_SERVER_WS_URL:
      process.env.NEXT_PUBLIC_GAME_SERVER_WS_URL ?? 'ws://localhost:8080/ws',
    NEXT_PUBLIC_GAME_SERVER_HTTP_URL:
      process.env.NEXT_PUBLIC_GAME_SERVER_HTTP_URL ?? 'http://localhost:8080',
  },
};

export default nextConfig;
