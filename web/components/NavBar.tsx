'use client';

import Link from 'next/link';
import { useRouter } from 'next/navigation';
import { useSession } from '@/lib/session';

export default function NavBar() {
  const { user, logout } = useSession();
  const router = useRouter();

  if (!user) return null;

  async function handleLogout() {
    await logout();
    router.push('/login');
  }

  return (
    <nav className="border-b border-slate-200 bg-white">
      <div className="mx-auto flex max-w-6xl items-center justify-between px-4 py-3">
        <div className="flex items-center gap-6">
          <Link href="/dashboard" className="font-semibold text-slate-900">
            Waypoint
          </Link>
          <Link href="/dashboard" className="text-sm text-slate-600 hover:text-slate-900">
            Dashboard
          </Link>
          <Link href="/projects" className="text-sm text-slate-600 hover:text-slate-900">
            Projects
          </Link>
        </div>
        <div className="flex items-center gap-3 text-sm text-slate-600">
          <span>
            {user.name} <span className="text-slate-400">({user.role})</span>
          </span>
          <button onClick={handleLogout} className="rounded border border-slate-300 px-3 py-1 hover:bg-slate-100">
            Sign out
          </button>
        </div>
      </div>
    </nav>
  );
}
