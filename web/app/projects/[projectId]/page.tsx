'use client';

import { useEffect, useState, useCallback } from 'react';
import { useParams } from 'next/navigation';
import Link from 'next/link';
import RequireAuth from '@/components/RequireAuth';
import { api, ApiError } from '@/lib/api';
import type { Project, Policy, Release, User } from '@/lib/types';
import { StageBadge } from '@/components/Badge';
import { LoadingState, ErrorState, EmptyState, SuccessBanner } from '@/components/Feedback';
import { useSession } from '@/lib/session';

const EVIDENCE_TYPES = ['change_summary', 'test_results', 'uat_signoff', 'rollback_plan', 'security_review', 'other'];

function PolicyEditor({ projectId, canManage }: { projectId: string; canManage: boolean }) {
  const [policy, setPolicy] = useState<Policy | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);
  const [saving, setSaving] = useState(false);

  const load = useCallback(() => {
    api
      .get<Policy>(`/api/v1/projects/${projectId}/policy`)
      .then(setPolicy)
      .catch((err) => setError(err instanceof ApiError ? err.message : 'Failed to load policy'));
  }, [projectId]);

  useEffect(() => load(), [load]);

  function toggleType(type: string) {
    if (!policy) return;
    const has = policy.required_evidence_types.includes(type);
    setPolicy({
      ...policy,
      required_evidence_types: has ? policy.required_evidence_types.filter((t) => t !== type) : [...policy.required_evidence_types, type],
    });
  }

  async function handleSave() {
    if (!policy) return;
    setSaving(true);
    setError(null);
    setSaved(false);
    try {
      const updated = await api.patch<Policy>(`/api/v1/projects/${projectId}/policy`, {
        required_evidence_types: policy.required_evidence_types,
        required_approvals_count: policy.required_approvals_count,
        block_on_open_rejection: policy.block_on_open_rejection,
      });
      setPolicy(updated);
      setSaved(true);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to save policy');
    } finally {
      setSaving(false);
    }
  }

  if (error) return <ErrorState message={error} />;
  if (!policy) return <LoadingState />;

  return (
    <div className="card space-y-4">
      <div>
        <div className="label">Required evidence types</div>
        <div className="flex flex-wrap gap-2">
          {EVIDENCE_TYPES.map((t) => (
            <label
              key={t}
              className={`flex items-center gap-1.5 rounded-lg border px-2.5 py-1.5 text-xs font-medium transition ${
                policy.required_evidence_types.includes(t)
                  ? 'border-brand-200 bg-brand-50 text-brand-700'
                  : 'border-slate-200 text-slate-600'
              }`}
            >
              <input
                type="checkbox"
                disabled={!canManage}
                checked={policy.required_evidence_types.includes(t)}
                onChange={() => toggleType(t)}
                className="rounded border-slate-300 text-brand-600 focus:ring-brand-500/30"
              />
              {t}
            </label>
          ))}
        </div>
      </div>
      <div>
        <label className="label">Required approvals</label>
        <input
          type="number"
          min={0}
          disabled={!canManage}
          value={policy.required_approvals_count}
          onChange={(e) => setPolicy({ ...policy, required_approvals_count: Number(e.target.value) })}
          className="input w-24"
        />
      </div>
      <label className="flex items-center gap-2 text-sm text-slate-700">
        <input
          type="checkbox"
          disabled={!canManage}
          checked={policy.block_on_open_rejection}
          onChange={(e) => setPolicy({ ...policy, block_on_open_rejection: e.target.checked })}
          className="rounded border-slate-300 text-brand-600 focus:ring-brand-500/30"
        />
        Block the gate if the most recent approval decision is a rejection
      </label>
      {canManage && (
        <button onClick={handleSave} disabled={saving} className="btn-primary">
          {saving ? 'Saving…' : 'Save policy'}
        </button>
      )}
      {saved && <SuccessBanner message="Policy saved." />}
    </div>
  );
}

function MembersPanel({ projectId, canManage }: { projectId: string; canManage: boolean }) {
  const [members, setMembers] = useState<User[] | null>(null);
  const [userId, setUserId] = useState('');
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(() => {
    api
      .get<User[]>(`/api/v1/projects/${projectId}/members`)
      .then(setMembers)
      .catch((err) => setError(err instanceof ApiError ? err.message : 'Failed to load members'));
  }, [projectId]);

  useEffect(() => load(), [load]);

  async function handleAdd(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    try {
      await api.post(`/api/v1/projects/${projectId}/members`, { user_id: userId });
      setUserId('');
      load();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to add member');
    }
  }

  return (
    <div className="card space-y-3">
      {error && <ErrorState message={error} />}
      {!members && <LoadingState />}
      {members && members.length === 0 && <EmptyState label="No members yet." />}
      {members && members.length > 0 && (
        <ul className="divide-y divide-slate-100 text-sm">
          {members.map((m) => (
            <li key={m.id} className="flex justify-between py-2">
              <span className="text-slate-900">{m.name}</span>
              <span className="text-slate-400 capitalize">{m.role.replace(/_/g, ' ')}</span>
            </li>
          ))}
        </ul>
      )}
      {canManage && (
        <form onSubmit={handleAdd} className="flex gap-2 pt-1">
          <input placeholder="User ID to add" value={userId} onChange={(e) => setUserId(e.target.value)} className="input flex-1" />
          <button type="submit" className="btn-primary">
            Add
          </button>
        </form>
      )}
    </div>
  );
}

function ReleasesPanel({ projectId }: { projectId: string }) {
  const [releases, setReleases] = useState<Release[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    api
      .get<Release[]>(`/api/v1/projects/${projectId}/releases`)
      .then(setReleases)
      .catch((err) => setError(err instanceof ApiError ? err.message : 'Failed to load releases'));
  }, [projectId]);

  if (error) return <ErrorState message={error} />;
  if (!releases) return <LoadingState />;
  if (releases.length === 0) return <EmptyState label="No releases yet." />;

  return (
    <div className="card-list">
      {releases.map((r) => (
        <Link key={r.id} href={`/releases/${r.id}`} className="list-row">
          <div>
            <div className="font-medium text-slate-900">{r.name}</div>
            <div className="text-xs capitalize text-slate-500">{r.risk_level} risk</div>
          </div>
          <StageBadge stage={r.stage} />
        </Link>
      ))}
    </div>
  );
}

function ProjectDetailContent() {
  const { projectId } = useParams<{ projectId: string }>();
  const { user } = useSession();
  const [project, setProject] = useState<Project | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    api
      .get<Project>(`/api/v1/projects/${projectId}`)
      .then(setProject)
      .catch((err) => setError(err instanceof ApiError ? err.message : 'Failed to load project'));
  }, [projectId]);

  const canManage = user?.role === 'admin' || user?.role === 'release_coordinator';

  if (error) return <ErrorState message={error} />;
  if (!project) return <LoadingState />;

  return (
    <div className="space-y-8">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="page-title">{project.name}</h1>
          <p className="page-subtitle">{project.owning_team}</p>
        </div>
        <Link href={`/projects/${projectId}/releases/new`} className="btn-primary">
          New release
        </Link>
      </div>

      <section>
        <h2 className="section-title mb-2">Releases</h2>
        <ReleasesPanel projectId={projectId} />
      </section>

      <section>
        <h2 className="section-title mb-2">Release gate policy</h2>
        <PolicyEditor projectId={projectId} canManage={canManage} />
      </section>

      <section>
        <h2 className="section-title mb-2">Members</h2>
        <MembersPanel projectId={projectId} canManage={canManage} />
      </section>
    </div>
  );
}

export default function ProjectDetailPage() {
  return (
    <RequireAuth>
      <ProjectDetailContent />
    </RequireAuth>
  );
}
