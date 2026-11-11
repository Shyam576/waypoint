'use client';

import { useState } from 'react';
import { useParams, useRouter } from 'next/navigation';
import RequireAuth from '@/components/RequireAuth';
import { api, ApiError } from '@/lib/api';
import type { Release, RiskLevel } from '@/lib/types';
import { ErrorState } from '@/components/Feedback';

function NewReleaseContent() {
  const { projectId } = useParams<{ projectId: string }>();
  const router = useRouter();

  const [name, setName] = useState('');
  const [riskLevel, setRiskLevel] = useState<RiskLevel>('medium');
  const [targetDate, setTargetDate] = useState('');
  const [summary, setSummary] = useState('');
  const [rollbackPlan, setRollbackPlan] = useState('');
  const [supportPeriodDays, setSupportPeriodDays] = useState(14);
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    setSubmitting(true);
    try {
      const release = await api.post<Release>(`/api/v1/projects/${projectId}/releases`, {
        name,
        risk_level: riskLevel,
        target_date: targetDate || null,
        summary,
        rollback_plan: rollbackPlan,
        support_period_days: supportPeriodDays,
      });
      router.push(`/releases/${release.id}`);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to create release');
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div className="mx-auto max-w-lg">
      <h1 className="page-title mb-4">New release</h1>
      <form onSubmit={handleSubmit} className="card space-y-4">
        {error && <ErrorState message={error} />}
        <div>
          <label className="label">Name / version</label>
          <input required value={name} onChange={(e) => setName(e.target.value)} className="input" />
        </div>
        <div>
          <label className="label">Risk level</label>
          <select value={riskLevel} onChange={(e) => setRiskLevel(e.target.value as RiskLevel)} className="input">
            <option value="low">Low</option>
            <option value="medium">Medium</option>
            <option value="high">High</option>
          </select>
        </div>
        <div>
          <label className="label">Target date</label>
          <input type="date" value={targetDate} onChange={(e) => setTargetDate(e.target.value)} className="input" />
        </div>
        <div>
          <label className="label">Summary</label>
          <textarea value={summary} onChange={(e) => setSummary(e.target.value)} rows={3} className="input" />
        </div>
        <div>
          <label className="label">Rollback plan</label>
          <textarea value={rollbackPlan} onChange={(e) => setRollbackPlan(e.target.value)} rows={3} className="input" />
        </div>
        <div>
          <label className="label">Support period (days)</label>
          <input
            type="number"
            min={0}
            value={supportPeriodDays}
            onChange={(e) => setSupportPeriodDays(Number(e.target.value))}
            className="input w-32"
          />
        </div>
        <button type="submit" disabled={submitting} className="btn-primary">
          {submitting ? 'Creating…' : 'Create release'}
        </button>
      </form>
    </div>
  );
}

export default function NewReleasePage() {
  return (
    <RequireAuth>
      <NewReleaseContent />
    </RequireAuth>
  );
}
