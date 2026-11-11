'use client';

import { useState } from 'react';
import { useRouter } from 'next/navigation';
import RequireAuth from '@/components/RequireAuth';
import { api, ApiError } from '@/lib/api';
import type { Project } from '@/lib/types';
import { ErrorState } from '@/components/Feedback';

function NewProjectContent() {
  const router = useRouter();
  const [name, setName] = useState('');
  const [owningTeam, setOwningTeam] = useState('');
  const [repoUrl, setRepoUrl] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    setSubmitting(true);
    try {
      const project = await api.post<Project>('/api/v1/projects', {
        name,
        owning_team: owningTeam,
        repo_url: repoUrl || null,
      });
      router.push(`/projects/${project.id}`);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to create project');
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div className="mx-auto max-w-lg">
      <h1 className="page-title mb-4">New project</h1>
      <form onSubmit={handleSubmit} className="card space-y-4">
        {error && <ErrorState message={error} />}
        <div>
          <label className="label">Name</label>
          <input required value={name} onChange={(e) => setName(e.target.value)} className="input" />
        </div>
        <div>
          <label className="label">Owning team</label>
          <input required value={owningTeam} onChange={(e) => setOwningTeam(e.target.value)} className="input" />
        </div>
        <div>
          <label className="label">Repository URL (optional)</label>
          <input value={repoUrl} onChange={(e) => setRepoUrl(e.target.value)} className="input" />
        </div>
        <button type="submit" disabled={submitting} className="btn-primary">
          {submitting ? 'Creating…' : 'Create project'}
        </button>
      </form>
    </div>
  );
}

export default function NewProjectPage() {
  return (
    <RequireAuth>
      <NewProjectContent />
    </RequireAuth>
  );
}
