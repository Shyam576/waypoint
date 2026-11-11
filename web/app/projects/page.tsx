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
    <div className="space-y-5">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="page-title">Projects</h1>
          <p className="page-subtitle">All projects you have access to.</p>
        </div>
        {canCreate && (
          <Link href="/projects/new" className="btn-primary">
            New project
          </Link>
        )}
      </div>

      {error && <ErrorState message={error} />}
      {!error && !projects && <LoadingState />}
      {projects && projects.length === 0 && <EmptyState label="No projects yet. Create one to get started." />}
      {projects && projects.length > 0 && (
        <div className="card-list">
          {projects.map((p) => (
            <Link key={p.id} href={`/projects/${p.id}`} className="list-row">
              <div>
                <div className="font-medium text-slate-900">{p.name}</div>
                <div className="text-xs text-slate-500">{p.owning_team}</div>
              </div>
              <span className="chip bg-slate-100 text-slate-600 ring-1 ring-inset ring-slate-200">{p.status}</span>
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
