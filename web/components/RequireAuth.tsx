'use client';

import { useEffect } from 'react';
import { useRouter } from 'next/navigation';
import { useSession } from '@/lib/session';

/** Wraps a protected page: redirects to /login if there is no session,
 * and shows nothing until the session check resolves. The backend
 * enforces every authorization rule independently; this only improves
 * the signed-out experience. */
export default function RequireAuth({ children }: { children: React.ReactNode }) {
  const { user, loading } = useSession();
  const router = useRouter();

  useEffect(() => {
    if (!loading && !user) {
      router.replace('/login');
    }
  }, [loading, user, router]);

  if (loading) {
    return <div className="p-8 text-sm text-slate-500">Loading…</div>;
  }
  if (!user) {
    return null;
  }
  return <>{children}</>;
}
