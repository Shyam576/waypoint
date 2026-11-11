'use client';

import { useEffect, useState } from 'react';
import Link from 'next/link';
import RequireAuth from '@/components/RequireAuth';
import { api, ApiError } from '@/lib/api';
import type { Dashboard, DashboardReleaseRow, Deployment } from '@/lib/types';
import { StageBadge, StatusBadge } from '@/components/Badge';
import { LoadingState, ErrorState, EmptyState } from '@/components/Feedback';
import { useSession } from '@/lib/session';

function ReleaseRow({ row }: { row: DashboardReleaseRow }) {
  return (
    <Link href={`/releases/${row.id}`} className="list-row">
      <div className="min-w-0">
        <div className="truncate font-medium text-slate-900">{row.name}</div>
        <div className="text-xs text-slate-500">{row.project_name}</div>
        {row.reason && <div className="mt-1 text-xs text-amber-700">{row.reason}</div>}
      </div>
      <StageBadge stage={row.stage} />
    </Link>
  );
}

function DeploymentRow({ d }: { d: Deployment }) {
  return (
    <div className="list-row">
      <div className="min-w-0">
        <div className="truncate font-mono text-xs text-slate-700">{d.artifact_digest}</div>
        <div className="text-xs text-slate-500">
          {d.environment} · {new Date(d.occurred_at).toLocaleString()}
        </div>
      </div>
      <StatusBadge value={d.result} />
    </div>
  );
}

function Section({ title, count, children }: { title: string; count: number; children: React.ReactNode }) {
  return (
    <section>
      <div className="mb-2 flex items-center gap-2">
        <h2 className="section-title">{title}</h2>
        <span className="rounded-full bg-slate-100 px-2 py-0.5 text-xs font-medium text-slate-500">{count}</span>
      </div>
      {children}
    </section>
  );
}

function DashboardContent() {
  const { user } = useSession();
  const [data, setData] = useState<Dashboard | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    api
      .get<Dashboard>('/api/v1/dashboard')
      .then(setData)
      .catch((err) => setError(err instanceof ApiError ? err.message : 'Failed to load dashboard'));
  }, []);

  if (error) return <ErrorState message={error} />;
  if (!data) return <LoadingState />;

  return (
    <div className="space-y-8">
      <div>
        <h1 className="page-title">Welcome back{user ? `, ${user.name.split(' ')[0]}` : ''}</h1>
        <p className="page-subtitle">Here&apos;s what needs your attention across your projects.</p>
      </div>

      <Section title="Needs your action" count={data.needs_my_action.length}>
        {data.needs_my_action.length === 0 ? (
          <EmptyState label="Nothing needs your action right now." />
        ) : (
          <div className="card-list">
            {data.needs_my_action.map((r) => (
              <ReleaseRow key={r.id} row={r} />
            ))}
          </div>
        )}
      </Section>

      <Section title="Blocked releases" count={data.blocked.length}>
        {data.blocked.length === 0 ? (
          <EmptyState label="No releases are currently blocked." />
        ) : (
          <div className="card-list">
            {data.blocked.map((r) => (
              <ReleaseRow key={r.id} row={r} />
            ))}
          </div>
        )}
      </Section>

      <Section title="Upcoming production releases" count={data.upcoming_production.length}>
        {data.upcoming_production.length === 0 ? (
          <EmptyState label="No releases are scheduled for production soon." />
        ) : (
          <div className="card-list">
            {data.upcoming_production.map((r) => (
              <ReleaseRow key={r.id} row={r} />
            ))}
          </div>
        )}
      </Section>

      <Section title="Recent deployments" count={data.recent_deployments.length}>
        {data.recent_deployments.length === 0 ? (
          <EmptyState label="No deployments recorded yet." />
        ) : (
          <div className="card-list">
            {data.recent_deployments.map((d) => (
              <DeploymentRow key={d.id} d={d} />
            ))}
          </div>
        )}
      </Section>
    </div>
  );
}

export default function DashboardPage() {
  return (
    <RequireAuth>
      <DashboardContent />
    </RequireAuth>
  );
}
