/**
 * Se ejecuta una vez al arrancar el servidor de Next.js. Aplica las migraciones
 * del almacén de identidad (ADR-018 §2.1), como el Game Server aplica las suyas.
 *
 * La condición tiene que escribirse así, literal y envolviendo el import: Next.js
 * compila este fichero también para el runtime edge, y sólo elimina el código de
 * Node (pg, fs) si la rama es evidentemente muerta.
 */
export async function register(): Promise<void> {
  if (process.env.NEXT_RUNTIME === 'nodejs') {
    const { migrateOnStartup } = await import('./src/server/runtime');
    await migrateOnStartup();
  }
}
