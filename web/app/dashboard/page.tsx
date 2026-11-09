'use client';

import { useEffect, useState } from 'react';
import Link from 'next/link';
import RequireAuth from '@/components/RequireAuth';
import { api, ApiError } from '@/lib/api';
import type { Dashboard, DashboardReleaseRow, Deployment } from '@/lib/types';
import { StageBadge, StatusBadge } from '@/components/Badge';
import { LoadingState, ErrorState, EmptyState } from '@/components/Feedback';

function ReleaseRow({ row }: { row: DashboardReleaseRow }) {
  return (
    <Link
      href={`/releases/${row.id}`}
      className="flex items-center justify-between rounded border border-slate-200 bg-white px-4 py-3 hover:border-slate-300"
    >
      <div>
        <div className="font-medium text-slate-900">{row.name}</div>
        <div className="text-xs text-slate-500">{row.project_name}</div>
        {row.reason && <div className="mt-1 text-xs text-amber-700">{row.reason}</div>}
      </div>
      <StageBadge stage={row.stage} />
    </Link>
  );
}

function DeploymentRow({ d }: { d: Deployment }) {
  return (
    <div className="flex items-center justify-between rounded border border-slate-200 bg-white px-4 py-3">
      <div>
        <div className="font-mono text-xs text-slate-700">{d.artifact_digest}</div>
        <div className="text-xs text-slate-500">
          {d.environment} · {new Date(d.occurred_at).toLocaleString()}
        </div>
      </div>
      <StatusBadge value={d.result} />
    </div>
  );
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section>
      <h2 className="mb-2 text-sm font-semibold uppercase tracking-wide text-slate-500">{title}</h2>
      <div className="space-y-2">{children}</div>
    </section>
  );
}

function DashboardContent() {
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
      <Section title="Needs your action">
        {data.needs_my_action.length === 0 ? (
          <EmptyState label="Nothing needs your action right now." />
        ) : (
          data.needs_my_action.map((r) => <ReleaseRow key={r.id} row={r} />)
        )}
      </Section>

      <Section title="Blocked releases">
        {data.blocked.length === 0 ? (
          <EmptyState label="No releases are currently blocked." />
        ) : (
          data.blocked.map((r) => <ReleaseRow key={r.id} row={r} />)
        )}
      </Section>

      <Section title="Upcoming production releases">
        {data.upcoming_production.length === 0 ? (
          <EmptyState label="No releases are scheduled for production soon." />
        ) : (
          data.upcoming_production.map((r) => <ReleaseRow key={r.id} row={r} />)
        )}
      </Section>

      <Section title="Recent deployments">
        {data.recent_deployments.length === 0 ? (
          <EmptyState label="No deployments recorded yet." />
        ) : (
          data.recent_deployments.map((d) => <DeploymentRow key={d.id} d={d} />)
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
