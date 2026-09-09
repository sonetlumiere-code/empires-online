/**
 * @empires-online/protocol
 *
 * Punto de entrada. Hoy sólo existe la v1; cuando exista una v2 ambas coexistirán
 * y este archivo exportará los dos espacios de nombres sin romper a los consumidores
 * de la v1. Ver ../../docs/specs/websocket-protocol.md
 */
export * as v1 from './v1/index.js';
export { PROTOCOL_VERSION } from './v1/common.js';
