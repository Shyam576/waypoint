'use client';

import { useEffect, useState } from 'react';
import Link from 'next/link';
import RequireAuth from '@/components/RequireAuth';
import { api, ApiError } from '@/lib/api';
import type { Project } from '@/lib/types';
import { LoadingState, ErrorState, EmptyState } from '@/components/Feedback';
import { useSession } from '@/lib/session';

function ProjectsContent() {
  const { user } = useSession();
  const [projects, setProjects] = useState<Project[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    api
      .get<Project[]>('/api/v1/projects')
      .then(setProjects)
      .catch((err) => setError(err instanceof ApiError ? err.message : 'Failed to load projects'));
  }, []);

  const canCreate = user?.role === 'admin' || user?.role === 'release_coordinator';

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h1 className="text-xl font-semibold text-slate-900">Projects</h1>
        {canCreate && (
          <Link href="/projects/new" className="rounded bg-slate-900 px-3 py-2 text-sm font-medium text-white hover:bg-slate-800">
            New project
          </Link>
        )}
      </div>

      {error && <ErrorState message={error} />}
      {!error && !projects && <LoadingState />}
      {projects && projects.length === 0 && <EmptyState label="No projects yet. Create one to get started." />}
      {projects && projects.length > 0 && (
        <div className="divide-y divide-slate-200 rounded border border-slate-200 bg-white">
          {projects.map((p) => (
            <Link key={p.id} href={`/projects/${p.id}`} className="flex items-center justify-between px-4 py-3 hover:bg-slate-50">
              <div>
                <div className="font-medium text-slate-900">{p.name}</div>
                <div className="text-xs text-slate-500">{p.owning_team}</div>
              </div>
              <span className="text-xs text-slate-400">{p.status}</span>
            </Link>
          ))}
        </div>
      )}
    </div>
  );
}

export default function ProjectsPage() {
  return (
    <RequireAuth>
      <ProjectsContent />
    </RequireAuth>
  );
}
