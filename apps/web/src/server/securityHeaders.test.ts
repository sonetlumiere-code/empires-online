/**
 * Cabeceras de seguridad de la web: la CSP por nonce, las cabeceras fijas y el
 * middleware que las pone.
 */
import { NextRequest } from 'next/server';
import { describe, expect, it } from 'vitest';

import { config as middlewareConfig, middleware } from '../../middleware';
import { contentSecurityPolicy, newNonce, staticSecurityHeaders, webSocketOrigin } from './securityHeaders.mjs';

const directive = (csp: string, name: string) =>
  csp
    .split(';')
    .map((d) => d.trim())
    .find((d) => d.startsWith(`${name} `) || d === name);

describe('contentSecurityPolicy', () => {
  const prod = contentSecurityPolicy({ production: true, nonce: 'n0nce', wsUrl: 'wss://game.example.com/ws' });
  const dev = contentSecurityPolicy({ production: false, nonce: 'n0nce', wsUrl: 'ws://localhost:8080/ws' });

  it('los scripts sólo con nonce y strict-dynamic; en producción nunca unsafe-eval ni unsafe-inline', () => {
    expect(directive(prod, 'script-src')).toBe("script-src 'self' 'nonce-n0nce' 'strict-dynamic'");
    expect(prod).not.toContain('unsafe-eval');
    expect(directive(prod, 'script-src')).not.toContain('unsafe-inline');
  });

  it('en desarrollo Next necesita unsafe-eval, y nada más se relaja', () => {
    expect(directive(dev, 'script-src')).toBe("script-src 'self' 'nonce-n0nce' 'strict-dynamic' 'unsafe-eval'");
    expect(dev.replace(" 'unsafe-eval'", '').replace('ws://localhost:8080', 'wss://game.example.com')).toBe(prod);
  });

  it('connect-src admite el origen del WebSocket del Game Server y el propio', () => {
    expect(directive(prod, 'connect-src')).toBe("connect-src 'self' wss://game.example.com");
    expect(directive(dev, 'connect-src')).toBe("connect-src 'self' ws://localhost:8080");
  });

  it('una URL de WebSocket ilegible no abre la política: la conexión queda bloqueada y se nota', () => {
    expect(webSocketOrigin('no es una url')).toBeNull();
    expect(webSocketOrigin('https://game.example.com/ws')).toBeNull();
    expect(webSocketOrigin(undefined)).toBeNull();
    const rota = contentSecurityPolicy({ production: true, nonce: 'n', wsUrl: 'no es una url' });
    expect(directive(rota, 'connect-src')).toBe("connect-src 'self'");
  });

  it('cierra lo que el juego no usa y no se deja enmarcar', () => {
    for (const d of ["default-src 'self'", "object-src 'none'", "base-uri 'self'", "form-action 'self'", "frame-ancestors 'none'"]) {
      expect(prod.split('; ')).toContain(d);
    }
    // PixiJS: texturas desde canvas y blobs, estilos en línea de React, workers desde blobs.
    expect(directive(prod, 'img-src')).toBe("img-src 'self' data: blob:");
    expect(directive(prod, 'style-src')).toBe("style-src 'self' 'unsafe-inline'");
    expect(directive(prod, 'worker-src')).toBe("worker-src 'self' blob:");
    expect(prod).not.toContain('upgrade-insecure-requests');
  });
});

describe('staticSecurityHeaders', () => {
  const byKey = (production: boolean) => new Map(staticSecurityHeaders({ production }).map((h) => [h.key, h.value]));

  it('nosniff, sin enmarcar, referrer acotado y permisos cerrados, siempre', () => {
    for (const production of [true, false]) {
      const h = byKey(production);
      expect(h.get('X-Content-Type-Options')).toBe('nosniff');
      expect(h.get('X-Frame-Options')).toBe('DENY');
      expect(h.get('Referrer-Policy')).toBe('strict-origin-when-cross-origin');
      expect(h.get('Permissions-Policy')).toContain('camera=()');
      expect(h.get('Cross-Origin-Opener-Policy')).toBe('same-origin');
    }
  });

  it('HSTS sólo en producción, un año con subdominios y sin preload', () => {
    expect(byKey(true).get('Strict-Transport-Security')).toBe('max-age=31536000; includeSubDomains');
    expect(byKey(false).has('Strict-Transport-Security')).toBe(false);
  });
});

describe('middleware', () => {
  it('pone la CSP con un nonce distinto en cada respuesta y se lo pasa a Next en la petición', () => {
    const req = () => new NextRequest('http://localhost:3000/');
    const a = middleware(req());
    const b = middleware(req());
    const cspA = a.headers.get('content-security-policy')!;
    const cspB = b.headers.get('content-security-policy')!;
    const nonceOf = (csp: string) => /'nonce-([A-Za-z0-9+/=]+)'/.exec(csp)![1]!;
    expect(nonceOf(cspA)).not.toBe(nonceOf(cspB));
    expect(cspA).toContain("'strict-dynamic'");
    // Next lee el nonce de la cabecera Content-Security-Policy de la PETICIÓN,
    // que el middleware sobrescribe; `x-nonce` queda para quien quiera leerlo.
    expect(a.headers.get('x-middleware-override-headers')).toContain('content-security-policy');
    expect(a.headers.get('x-middleware-request-content-security-policy')).toBe(cspA);
    expect(a.headers.get('x-middleware-request-x-nonce')).toBe(nonceOf(cspA));
  });

  it('el nonce tiene 16 bytes de entropía en base64', () => {
    expect(newNonce()).toMatch(/^[A-Za-z0-9+/]{22}==$/);
    expect(newNonce()).not.toBe(newNonce());
  });

  it('no corre sobre los estáticos de Next ni sobre las precargas del router', () => {
    const [rule] = middlewareConfig.matcher;
    expect(rule!.source).toContain('_next/static');
    expect(rule!.missing.map((m) => m.key)).toEqual(['next-router-prefetch', 'purpose']);
  });
});
