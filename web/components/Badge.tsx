const STAGE_COLORS: Record<string, string> = {
  draft: 'bg-slate-100 text-slate-700 ring-1 ring-inset ring-slate-200',
  development: 'bg-blue-50 text-blue-700 ring-1 ring-inset ring-blue-200',
  uat: 'bg-indigo-50 text-indigo-700 ring-1 ring-inset ring-indigo-200',
  ready_for_production: 'bg-amber-50 text-amber-800 ring-1 ring-inset ring-amber-200',
  production: 'bg-emerald-50 text-emerald-700 ring-1 ring-inset ring-emerald-200',
  post_release_support: 'bg-purple-50 text-purple-700 ring-1 ring-inset ring-purple-200',
  closed: 'bg-slate-100 text-slate-500 ring-1 ring-inset ring-slate-200',
};

const SEVERITY_COLORS: Record<string, string> = {
  low: 'bg-slate-100 text-slate-700 ring-1 ring-inset ring-slate-200',
  medium: 'bg-amber-50 text-amber-800 ring-1 ring-inset ring-amber-200',
  high: 'bg-orange-50 text-orange-800 ring-1 ring-inset ring-orange-200',
  critical: 'bg-red-50 text-red-700 ring-1 ring-inset ring-red-200',
};

const DECISION_COLORS: Record<string, string> = {
  approved: 'bg-emerald-50 text-emerald-700 ring-1 ring-inset ring-emerald-200',
  rejected: 'bg-red-50 text-red-700 ring-1 ring-inset ring-red-200',
  pending: 'bg-amber-50 text-amber-800 ring-1 ring-inset ring-amber-200',
  accepted: 'bg-emerald-50 text-emerald-700 ring-1 ring-inset ring-emerald-200',
  success: 'bg-emerald-50 text-emerald-700 ring-1 ring-inset ring-emerald-200',
  failure: 'bg-red-50 text-red-700 ring-1 ring-inset ring-red-200',
  open: 'bg-amber-50 text-amber-800 ring-1 ring-inset ring-amber-200',
  investigating: 'bg-blue-50 text-blue-700 ring-1 ring-inset ring-blue-200',
  resolved: 'bg-emerald-50 text-emerald-700 ring-1 ring-inset ring-emerald-200',
};

function readableLabel(value: string): string {
  return value
    .split('_')
    .map((w) => w.charAt(0).toUpperCase() + w.slice(1))
    .join(' ');
}

export function StageBadge({ stage }: { stage: string }) {
  return <span className={`chip ${STAGE_COLORS[stage] ?? 'bg-slate-100 text-slate-700 ring-1 ring-inset ring-slate-200'}`}>{readableLabel(stage)}</span>;
}

export function SeverityBadge({ severity }: { severity: string }) {
  return (
    <span className={`chip ${SEVERITY_COLORS[severity] ?? 'bg-slate-100 text-slate-700 ring-1 ring-inset ring-slate-200'}`}>
      {readableLabel(severity)}
    </span>
  );
}

export function StatusBadge({ value }: { value: string }) {
  return (
    <span className={`chip ${DECISION_COLORS[value] ?? 'bg-slate-100 text-slate-700 ring-1 ring-inset ring-slate-200'}`}>
      {readableLabel(value)}
    </span>
  );
}
