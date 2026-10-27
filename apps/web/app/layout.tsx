import type { Metadata } from 'next';
import type { ReactNode } from 'react';
import './globals.css';

export const metadata: Metadata = {
  title: 'Empires Online',
  description: 'MMORTS persistente 24/7 con mundo continuo y servidor autoritativo.',
};

/**
 * La página se renderiza en cada petición, nunca prerenderizada en el build: la
 * Content-Security-Policy lleva un nonce distinto por petición (middleware.ts) y
 * Next.js sólo puede ponérselo a sus scripts en línea cuando renderiza. Un HTML
 * prerenderizado los llevaría sin nonce y la propia CSP los bloquearía.
 */
export const dynamic = 'force-dynamic';

export default function RootLayout({ children }: { children: ReactNode }) {
  return (
    <html lang="es">
      <body>{children}</body>
    </html>
  );
}
