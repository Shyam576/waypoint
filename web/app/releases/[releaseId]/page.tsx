'use client';

import { useCallback, useEffect, useState } from 'react';
import { useParams } from 'next/navigation';
import { api, ApiError } from '@/lib/api';
import type { ReleaseDetail, Stage, LinkType, EvidenceType, Decision, Environment, DeploymentResult } from '@/lib/types';
import RequireAuth from '@/components/RequireAuth';
import { StageBadge, StatusBadge, SeverityBadge } from '@/components/Badge';
import { LoadingState, ErrorState, EmptyState, SuccessBanner } from '@/components/Feedback';
import { useSession } from '@/lib/session';

const NEXT_STAGES: Record<Stage, Stage[]> = {
  draft: ['development', 'closed'],
  development: ['uat', 'draft', 'closed'],
  uat: ['ready_for_production', 'development', 'closed'],
  ready_for_production: ['uat', 'closed'],
  production: ['post_release_support', 'closed'],
  post_release_support: ['closed'],
  closed: [],
};

function Section({ title, children, right }: { title: string; children: React.ReactNode; right?: React.ReactNode }) {
  return (
    <section className="space-y-2">
      <div className="flex items-center justify-between">
        <h2 className="text-sm font-semibold uppercase tracking-wide text-slate-500">{title}</h2>
        {right}
      </div>
      {children}
    </section>
  );
}

function ReleaseDetailContent() {
  const { releaseId } = useParams<{ releaseId: string }>();
  const { user } = useSession();
  const [detail, setDetail] = useState<ReleaseDetail | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);

  const load = useCallback(() => {
    api
      .get<ReleaseDetail>(`/api/v1/releases/${releaseId}/full`)
      .then(setDetail)
      .catch((err) => setError(err instanceof ApiError ? err.message : 'Failed to load release'));
  }, [releaseId]);

  useEffect(() => load(), [load]);

  async function run(action: () => Promise<unknown>, successMessage?: string) {
    setError(null);
    setNotice(null);
    try {
      await action();
      if (successMessage) setNotice(successMessage);
      load();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Action failed');
    }
  }

  if (error && !detail) return <ErrorState message={error} />;
  if (!detail) return <LoadingState />;

  const { release, eligibility } = detail;
  const canTransition = user?.role === 'admin' || user?.role === 'developer' || user?.role === 'release_coordinator';
  const canApprove = user?.role === 'admin' || user?.role === 'approver';
  const canReview = user?.role === 'admin' || user?.role === 'developer' || user?.role === 'release_coordinator';
  const canRecordDeployment = user?.role === 'admin' || user?.role === 'release_coordinator';

  return (
    <div className="space-y-8">
      {error && <ErrorState message={error} />}
      {notice && <SuccessBanner message={notice} />}

      <div className="flex items-start justify-between">
        <div>
          <h1 className="text-xl font-semibold text-slate-900">{release.name}</h1>
          <p className="mt-1 text-sm text-slate-600">{release.summary || 'No summary provided.'}</p>
        </div>
        <StageBadge stage={release.stage} />
      </div>

      <Section title="Stage">
        <div className="flex flex-wrap items-center gap-2 rounded border border-slate-200 bg-white p-4">
          <span className="text-sm text-slate-600">Current: </span>
          <StageBadge stage={release.stage} />
          {canTransition &&
            NEXT_STAGES[release.stage].map((next) => (
              <button
                key={next}
                onClick={() => run(() => api.post(`/api/v1/releases/${releaseId}/transition`, { to_stage: next }))}
                className="rounded border border-slate-300 px-2 py-1 text-xs hover:bg-slate-100"
              >
                Move to {next.replace(/_/g, ' ')}
              </button>
            ))}
          {canTransition && release.stage !== 'closed' && (
            <button
              onClick={() => run(() => api.post(`/api/v1/releases/${releaseId}/close`, { reason: 'closed from web app' }), 'Release closed.')}
              className="rounded border border-red-300 px-2 py-1 text-xs text-red-700 hover:bg-red-50"
            >
              Close release
            </button>
          )}
        </div>
      </Section>

      <Section title="Production eligibility">
        <div className={`rounded border p-4 ${eligibility.eligible ? 'border-emerald-200 bg-emerald-50' : 'border-amber-200 bg-amber-50'}`}>
          <div className="mb-2 font-medium">{eligibility.eligible ? 'Eligible for production' : 'Not eligible for production'}</div>
          {eligibility.blockers.length > 0 && (
            <ul className="list-inside list-disc space-y-1 text-sm text-slate-700">
              {eligibility.blockers.map((b, i) => (
                <li key={i}>{b}</li>
              ))}
            </ul>
          )}
        </div>
      </Section>

      <LinksSection releaseId={releaseId} links={detail.links} onChanged={load} />
      <EvidenceSection releaseId={releaseId} evidence={detail.evidence} requiredTypes={eligibility.required_evidence_types} onChanged={load} />
      <ApprovalsSection releaseId={releaseId} approvals={detail.approvals} canApprove={!!canApprove} onChanged={load} />
      <ChangesSection releaseId={releaseId} detail={detail} onChanged={load} />
      <PendingMCPSection pending={detail.pending_mcp_submissions} canReview={!!canReview} onChanged={load} />
      <DeploymentsSection releaseId={releaseId} deployments={detail.deployments} canRecord={!!canRecordDeployment} onChanged={load} />
      <SupportSection releaseId={releaseId} items={detail.support_items} onChanged={load} />
      <ActivitySection activity={detail.activity} />
    </div>
  );
}

function LinksSection({
  releaseId,
  links,
  onChanged,
}: {
  releaseId: string;
  links: ReleaseDetail['links'];
  onChanged: () => void;
}) {
  const [linkType, setLinkType] = useState<LinkType>('pull_request');
  const [url, setUrl] = useState('');
  const [error, setError] = useState<string | null>(null);

  async function handleAdd(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    try {
      await api.post(`/api/v1/releases/${releaseId}/links`, { link_type: linkType, url });
      setUrl('');
      onChanged();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to add link');
    }
  }

  return (
    <Section title="Linked work">
      {error && <ErrorState message={error} />}
      {links.length === 0 ? (
        <EmptyState label="No links yet." />
      ) : (
        <ul className="space-y-1 rounded border border-slate-200 bg-white p-4 text-sm">
          {links.map((l) => (
            <li key={l.id}>
              <span className="mr-2 inline-block rounded bg-slate-100 px-1.5 py-0.5 text-xs">{l.link_type}</span>
              <a href={l.url} target="_blank" rel="noreferrer" className="text-blue-600 hover:underline">
                {l.label || l.url}
              </a>
            </li>
          ))}
        </ul>
      )}
      <form onSubmit={handleAdd} className="flex flex-wrap gap-2">
        <select value={linkType} onChange={(e) => setLinkType(e.target.value as LinkType)} className="rounded border border-slate-300 px-2 py-1 text-sm">
          <option value="pull_request">Pull request</option>
          <option value="commit">Commit</option>
          <option value="work_item">Work item</option>
          <option value="ci_run">CI run</option>
        </select>
        <input
          required
          placeholder="https://…"
          value={url}
          onChange={(e) => setUrl(e.target.value)}
          className="flex-1 rounded border border-slate-300 px-2 py-1 text-sm"
        />
        <button type="submit" className="rounded bg-slate-900 px-3 py-1.5 text-sm font-medium text-white hover:bg-slate-800">
          Add link
        </button>
      </form>
    </Section>
  );
}

function EvidenceSection({
  releaseId,
  evidence,
  requiredTypes,
  onChanged,
}: {
  releaseId: string;
  evidence: ReleaseDetail['evidence'];
  requiredTypes: string[];
  onChanged: () => void;
}) {
  const [type, setType] = useState<EvidenceType>('test_results');
  const [title, setTitle] = useState('');
  const [notes, setNotes] = useState('');
  const [error, setError] = useState<string | null>(null);

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    try {
      await api.post(`/api/v1/releases/${releaseId}/evidence`, { type, title, notes });
      setTitle('');
      setNotes('');
      onChanged();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to submit evidence');
    }
  }

  const presentTypes = new Set(evidence.map((e) => e.type));

  return (
    <Section title="Evidence">
      {error && <ErrorState message={error} />}
      <div className="flex flex-wrap gap-2 text-xs">
        {requiredTypes.map((t) => (
          <span key={t} className={`rounded px-2 py-1 ${presentTypes.has(t as EvidenceType) ? 'bg-emerald-100 text-emerald-700' : 'bg-amber-100 text-amber-800'}`}>
            {presentTypes.has(t as EvidenceType) ? '✓' : '○'} {t}
          </span>
        ))}
      </div>
      {evidence.length === 0 ? (
        <EmptyState label="No evidence submitted yet." />
      ) : (
        <ul className="space-y-2 rounded border border-slate-200 bg-white p-4 text-sm">
          {evidence.map((e) => (
            <li key={e.id} className="border-b border-slate-100 pb-2 last:border-0 last:pb-0">
              <div className="font-medium">
                {e.title} <span className="text-xs text-slate-400">({e.type})</span>
              </div>
              {e.notes && <div className="text-slate-600">{e.notes}</div>}
            </li>
          ))}
        </ul>
      )}
      <form onSubmit={handleSubmit} className="space-y-2 rounded border border-slate-200 bg-white p-4">
        <div className="flex gap-2">
          <select value={type} onChange={(e) => setType(e.target.value as EvidenceType)} className="rounded border border-slate-300 px-2 py-1 text-sm">
            <option value="change_summary">Change summary</option>
            <option value="test_results">Test results</option>
            <option value="uat_signoff">UAT sign-off</option>
            <option value="rollback_plan">Rollback plan</option>
            <option value="security_review">Security review</option>
            <option value="other">Other</option>
          </select>
          <input
            required
            placeholder="Title"
            value={title}
            onChange={(e) => setTitle(e.target.value)}
            className="flex-1 rounded border border-slate-300 px-2 py-1 text-sm"
          />
        </div>
        <textarea
          placeholder="Notes"
          value={notes}
          onChange={(e) => setNotes(e.target.value)}
          rows={2}
          className="w-full rounded border border-slate-300 px-2 py-1 text-sm"
        />
        <button type="submit" className="rounded bg-slate-900 px-3 py-1.5 text-sm font-medium text-white hover:bg-slate-800">
          Submit evidence
        </button>
      </form>
    </Section>
  );
}

function ApprovalsSection({
  releaseId,
  approvals,
  canApprove,
  onChanged,
}: {
  releaseId: string;
  approvals: ReleaseDetail['approvals'];
  canApprove: boolean;
  onChanged: () => void;
}) {
  const [comment, setComment] = useState('');
  const [error, setError] = useState<string | null>(null);

  async function decide(decision: Decision) {
    setError(null);
    try {
      await api.post(`/api/v1/releases/${releaseId}/approvals`, { decision, comment });
      setComment('');
      onChanged();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to record decision');
    }
  }

  return (
    <Section title="Approvals">
      {error && <ErrorState message={error} />}
      {approvals.length === 0 ? (
        <EmptyState label="No approval decisions yet." />
      ) : (
        <ul className="space-y-2 rounded border border-slate-200 bg-white p-4 text-sm">
          {approvals.map((a) => (
            <li key={a.id} className="flex items-center justify-between">
              <div>
                <StatusBadge value={a.decision} /> <span className="ml-2 text-slate-600">{a.comment}</span>
              </div>
              <span className="text-xs text-slate-400">{new Date(a.decided_at).toLocaleString()}</span>
            </li>
          ))}
        </ul>
      )}
      {canApprove && (
        <div className="flex gap-2 rounded border border-slate-200 bg-white p-4">
          <input
            placeholder="Comment"
            value={comment}
            onChange={(e) => setComment(e.target.value)}
            className="flex-1 rounded border border-slate-300 px-2 py-1 text-sm"
          />
          <button onClick={() => decide('approved')} className="rounded bg-emerald-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-emerald-700">
            Approve
          </button>
          <button onClick={() => decide('rejected')} className="rounded bg-red-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-red-700">
            Reject
          </button>
        </div>
      )}
    </Section>
  );
}

function ChangesSection({ releaseId, detail, onChanged }: { releaseId: string; detail: ReleaseDetail; onChanged: () => void }) {
  const [summary, setSummary] = useState('');
  const [decision, setDecision] = useState('');
  const [error, setError] = useState<string | null>(null);

  async function addChangeNote(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    try {
      await api.post(`/api/v1/releases/${releaseId}/change-notes`, { summary, tests_status: 'not_run' });
      setSummary('');
      onChanged();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to add change note');
    }
  }

  async function addDesignDecision(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    try {
      await api.post(`/api/v1/releases/${releaseId}/design-decisions`, { decision });
      setDecision('');
      onChanged();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to add design decision');
    }
  }

  return (
    <Section title="Change notes & design decisions">
      {error && <ErrorState message={error} />}
      <div className="grid gap-4 md:grid-cols-2">
        <div className="space-y-2">
          <div className="text-xs font-medium text-slate-500">Change notes</div>
          {detail.change_notes.length === 0 ? (
            <EmptyState label="None yet." />
          ) : (
            <ul className="space-y-2 rounded border border-slate-200 bg-white p-3 text-sm">
              {detail.change_notes.map((c) => (
                <li key={c.id}>
                  {c.summary} <span className="text-xs text-slate-400">({c.source})</span>
                </li>
              ))}
            </ul>
          )}
          <form onSubmit={addChangeNote} className="flex gap-2">
            <input
              required
              placeholder="What changed?"
              value={summary}
              onChange={(e) => setSummary(e.target.value)}
              className="flex-1 rounded border border-slate-300 px-2 py-1 text-sm"
            />
            <button type="submit" className="rounded bg-slate-900 px-3 py-1.5 text-sm font-medium text-white hover:bg-slate-800">
              Add
            </button>
          </form>
        </div>
        <div className="space-y-2">
          <div className="text-xs font-medium text-slate-500">Design decisions</div>
          {detail.design_decisions.length === 0 ? (
            <EmptyState label="None yet." />
          ) : (
            <ul className="space-y-2 rounded border border-slate-200 bg-white p-3 text-sm">
              {detail.design_decisions.map((d) => (
                <li key={d.id}>
                  {d.decision} <span className="text-xs text-slate-400">({d.source})</span>
                </li>
              ))}
            </ul>
          )}
          <form onSubmit={addDesignDecision} className="flex gap-2">
            <input
              required
              placeholder="What was decided?"
              value={decision}
              onChange={(e) => setDecision(e.target.value)}
              className="flex-1 rounded border border-slate-300 px-2 py-1 text-sm"
            />
            <button type="submit" className="rounded bg-slate-900 px-3 py-1.5 text-sm font-medium text-white hover:bg-slate-800">
              Add
            </button>
          </form>
        </div>
      </div>
    </Section>
  );
}

function PendingMCPSection({
  pending,
  canReview,
  onChanged,
}: {
  pending: ReleaseDetail['pending_mcp_submissions'];
  canReview: boolean;
  onChanged: () => void;
}) {
  const [error, setError] = useState<string | null>(null);

  async function act(id: string, accept: boolean) {
    setError(null);
    try {
      await api.post(`/api/v1/mcp-submissions/${id}/${accept ? 'accept' : 'reject'}`, {});
      onChanged();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to review submission');
    }
  }

  if (pending.length === 0) return null;

  return (
    <Section title="Pending Claude (MCP) submissions">
      {error && <ErrorState message={error} />}
      <ul className="space-y-3">
        {pending.map((s) => (
          <li key={s.id} className="rounded border border-amber-200 bg-amber-50 p-4">
            <div className="mb-1 text-xs font-medium uppercase text-amber-700">{s.type.replace(/_/g, ' ')}</div>
            <pre className="whitespace-pre-wrap text-xs text-slate-700">{JSON.stringify(s.payload, null, 2)}</pre>
            {canReview && (
              <div className="mt-2 flex gap-2">
                <button onClick={() => act(s.id, true)} className="rounded bg-emerald-600 px-3 py-1 text-xs font-medium text-white hover:bg-emerald-700">
                  Accept
                </button>
                <button onClick={() => act(s.id, false)} className="rounded bg-red-600 px-3 py-1 text-xs font-medium text-white hover:bg-red-700">
                  Reject
                </button>
              </div>
            )}
          </li>
        ))}
      </ul>
    </Section>
  );
}

function DeploymentsSection({
  releaseId,
  deployments,
  canRecord,
  onChanged,
}: {
  releaseId: string;
  deployments: ReleaseDetail['deployments'];
  canRecord: boolean;
  onChanged: () => void;
}) {
  const [environment, setEnvironment] = useState<Environment>('staging');
  const [digest, setDigest] = useState('');
  const [runUrl, setRunUrl] = useState('');
  const [result, setResult] = useState<DeploymentResult>('success');
  const [error, setError] = useState<string | null>(null);

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    try {
      await api.post(`/api/v1/releases/${releaseId}/deployments`, {
        environment,
        artifact_digest: digest,
        workflow_run_url: runUrl,
        result,
      });
      setDigest('');
      setRunUrl('');
      onChanged();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to record deployment');
    }
  }

  return (
    <Section title="Deployments">
      {error && <ErrorState message={error} />}
      {deployments.length === 0 ? (
        <EmptyState label="No deployments recorded yet." />
      ) : (
        <ul className="space-y-2 rounded border border-slate-200 bg-white p-4 text-sm">
          {deployments.map((d) => (
            <li key={d.id} className="flex items-center justify-between border-b border-slate-100 pb-2 last:border-0 last:pb-0">
              <div>
                <div className="font-mono text-xs">{d.artifact_digest}</div>
                <div className="text-xs text-slate-500">
                  {d.environment} · {d.recorded_via} · {new Date(d.occurred_at).toLocaleString()}
                </div>
              </div>
              <StatusBadge value={d.result} />
            </li>
          ))}
        </ul>
      )}
      {canRecord && (
        <form onSubmit={handleSubmit} className="space-y-2 rounded border border-slate-200 bg-white p-4">
          <p className="text-xs text-slate-500">
            CI pipelines should record production deployments via the CI token endpoint; this manual form is for
            admins/coordinators backfilling or correcting a record.
          </p>
          <div className="flex gap-2">
            <select value={environment} onChange={(e) => setEnvironment(e.target.value as Environment)} className="rounded border border-slate-300 px-2 py-1 text-sm">
              <option value="development">Development</option>
              <option value="uat">UAT</option>
              <option value="staging">Staging</option>
              <option value="production">Production</option>
            </select>
            <select value={result} onChange={(e) => setResult(e.target.value as DeploymentResult)} className="rounded border border-slate-300 px-2 py-1 text-sm">
              <option value="success">Success</option>
              <option value="failure">Failure</option>
            </select>
          </div>
          <input
            required
            placeholder="Artifact digest (sha256:…)"
            value={digest}
            onChange={(e) => setDigest(e.target.value)}
            className="w-full rounded border border-slate-300 px-2 py-1 text-sm"
          />
          <input
            required
            placeholder="Workflow run URL"
            value={runUrl}
            onChange={(e) => setRunUrl(e.target.value)}
            className="w-full rounded border border-slate-300 px-2 py-1 text-sm"
          />
          <button type="submit" className="rounded bg-slate-900 px-3 py-1.5 text-sm font-medium text-white hover:bg-slate-800">
            Record deployment
          </button>
        </form>
      )}
    </Section>
  );
}

function SupportSection({ releaseId, items, onChanged }: { releaseId: string; items: ReleaseDetail['support_items']; onChanged: () => void }) {
  const [summary, setSummary] = useState('');
  const [severity, setSeverity] = useState('medium');
  const [error, setError] = useState<string | null>(null);

  async function handleAdd(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    try {
      await api.post(`/api/v1/releases/${releaseId}/support-items`, { summary, severity });
      setSummary('');
      onChanged();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to add support item');
    }
  }

  async function updateStatus(id: string, status: string, current: ReleaseDetail['support_items'][number]) {
    setError(null);
    try {
      await api.patch(`/api/v1/support-items/${id}`, {
        severity: current.severity,
        status,
        resolution_note: current.resolution_note,
      });
      onChanged();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to update support item');
    }
  }

  return (
    <Section title="Post-release support">
      {error && <ErrorState message={error} />}
      {items.length === 0 ? (
        <EmptyState label="No support items yet." />
      ) : (
        <ul className="space-y-2 rounded border border-slate-200 bg-white p-4 text-sm">
          {items.map((it) => (
            <li key={it.id} className="flex items-center justify-between border-b border-slate-100 pb-2 last:border-0 last:pb-0">
              <div>
                <div className="font-medium">{it.summary}</div>
                <div className="mt-1 flex gap-2">
                  <SeverityBadge severity={it.severity} />
                  <StatusBadge value={it.status} />
                </div>
              </div>
              {it.status !== 'closed' && (
                <select
                  value={it.status}
                  onChange={(e) => updateStatus(it.id, e.target.value, it)}
                  className="rounded border border-slate-300 px-2 py-1 text-xs"
                >
                  <option value="open">Open</option>
                  <option value="investigating">Investigating</option>
                  <option value="resolved">Resolved</option>
                  <option value="closed">Closed</option>
                </select>
              )}
            </li>
          ))}
        </ul>
      )}
      <form onSubmit={handleAdd} className="flex gap-2">
        <select value={severity} onChange={(e) => setSeverity(e.target.value)} className="rounded border border-slate-300 px-2 py-1 text-sm">
          <option value="low">Low</option>
          <option value="medium">Medium</option>
          <option value="high">High</option>
          <option value="critical">Critical</option>
        </select>
        <input
          required
          placeholder="Issue summary"
          value={summary}
          onChange={(e) => setSummary(e.target.value)}
          className="flex-1 rounded border border-slate-300 px-2 py-1 text-sm"
        />
        <button type="submit" className="rounded bg-slate-900 px-3 py-1.5 text-sm font-medium text-white hover:bg-slate-800">
          Add
        </button>
      </form>
    </Section>
  );
}

function ActivitySection({ activity }: { activity: ReleaseDetail['activity'] }) {
  return (
    <Section title="Activity timeline">
      {activity.length === 0 ? (
        <EmptyState label="No activity yet." />
      ) : (
        <ul className="space-y-2 rounded border border-slate-200 bg-white p-4 text-sm">
          {activity.map((a) => (
            <li key={a.id} className="border-b border-slate-100 pb-2 last:border-0 last:pb-0">
              <div className="flex justify-between">
                <span className="font-medium">{a.action.replace(/_/g, ' ')}</span>
                <span className="text-xs text-slate-400">{new Date(a.created_at).toLocaleString()}</span>
              </div>
              <div className="text-xs text-slate-500">
                {a.actor_label}
                {a.remarks ? ` — ${a.remarks}` : ''}
              </div>
            </li>
          ))}
        </ul>
      )}
    </Section>
  );
}

export default function ReleaseDetailPage() {
  return (
    <RequireAuth>
      <ReleaseDetailContent />
    </RequireAuth>
  );
}
