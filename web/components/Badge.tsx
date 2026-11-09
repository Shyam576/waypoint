const STAGE_COLORS: Record<string, string> = {
  draft: 'bg-slate-100 text-slate-700',
  development: 'bg-blue-100 text-blue-700',
  uat: 'bg-indigo-100 text-indigo-700',
  ready_for_production: 'bg-amber-100 text-amber-800',
  production: 'bg-emerald-100 text-emerald-700',
  post_release_support: 'bg-purple-100 text-purple-700',
  closed: 'bg-slate-200 text-slate-500',
};

const SEVERITY_COLORS: Record<string, string> = {
  low: 'bg-slate-100 text-slate-700',
  medium: 'bg-amber-100 text-amber-800',
  high: 'bg-orange-100 text-orange-800',
  critical: 'bg-red-100 text-red-700',
};

const DECISION_COLORS: Record<string, string> = {
  approved: 'bg-emerald-100 text-emerald-700',
  rejected: 'bg-red-100 text-red-700',
  pending: 'bg-amber-100 text-amber-800',
  accepted: 'bg-emerald-100 text-emerald-700',
  success: 'bg-emerald-100 text-emerald-700',
  failure: 'bg-red-100 text-red-700',
  open: 'bg-amber-100 text-amber-800',
  investigating: 'bg-blue-100 text-blue-700',
  resolved: 'bg-emerald-100 text-emerald-700',
};

function readableLabel(value: string): string {
  return value
    .split('_')
    .map((w) => w.charAt(0).toUpperCase() + w.slice(1))
    .join(' ');
}

export function StageBadge({ stage }: { stage: string }) {
  return (
    <span className={`inline-block rounded-full px-2.5 py-0.5 text-xs font-medium ${STAGE_COLORS[stage] ?? 'bg-slate-100 text-slate-700'}`}>
      {readableLabel(stage)}
    </span>
  );
}

export function SeverityBadge({ severity }: { severity: string }) {
  return (
    <span className={`inline-block rounded-full px-2.5 py-0.5 text-xs font-medium ${SEVERITY_COLORS[severity] ?? 'bg-slate-100 text-slate-700'}`}>
      {readableLabel(severity)}
    </span>
  );
}

export function StatusBadge({ value }: { value: string }) {
  return (
    <span className={`inline-block rounded-full px-2.5 py-0.5 text-xs font-medium ${DECISION_COLORS[value] ?? 'bg-slate-100 text-slate-700'}`}>
      {readableLabel(value)}
    </span>
  );
}
