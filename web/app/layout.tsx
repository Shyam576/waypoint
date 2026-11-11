import type { Metadata } from 'next';
import { Inter } from 'next/font/google';
import './globals.css';
import { SessionProvider } from '@/lib/session';
import NavBar from '@/components/NavBar';

const inter = Inter({ subsets: ['latin'], variable: '--font-inter', display: 'swap' });

export const metadata: Metadata = {
  title: 'Waypoint',
  description: 'Internal release coordination and evidence tool',
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en" className={inter.variable}>
      <body className="min-h-screen bg-slate-50 font-sans text-slate-900 antialiased">
        <SessionProvider>
          <NavBar />
          <main className="mx-auto max-w-6xl px-4 py-8 sm:px-6 lg:px-8">{children}</main>
        </SessionProvider>
      </body>
    </html>
  );
}
