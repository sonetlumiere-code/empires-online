// En Vitest no hay condición `react-server`: el paquete `server-only` lanzaría al
// importarse. Los tests importan módulos de servidor a propósito, así que aquí
// se sustituye por un módulo vacío. En el build de Next.js manda el de verdad.
export {};
