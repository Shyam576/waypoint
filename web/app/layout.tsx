import type { Metadata } from 'next';
import './globals.css';
import { SessionProvider } from '@/lib/session';
import NavBar from '@/components/NavBar';

export const metadata: Metadata = {
  title: 'Waypoint',
  description: 'Internal release coordination and evidence tool',
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en">
      <body>
        <SessionProvider>
          <NavBar />
          <main className="mx-auto max-w-6xl px-4 py-6">{children}</main>
        </SessionProvider>
      </body>
    </html>
  );
}
