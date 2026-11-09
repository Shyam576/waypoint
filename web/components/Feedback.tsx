export function LoadingState({ label = 'Loading…' }: { label?: string }) {
  return <div className="rounded border border-slate-200 bg-white p-6 text-sm text-slate-500">{label}</div>;
}

export function EmptyState({ label }: { label: string }) {
  return <div className="rounded border border-dashed border-slate-300 bg-slate-50 p-6 text-sm text-slate-500">{label}</div>;
}

export function ErrorState({ message }: { message: string }) {
  return (
    <div className="rounded border border-red-200 bg-red-50 p-4 text-sm text-red-700" role="alert">
      {message}
    </div>
  );
}

export function SuccessBanner({ message }: { message: string }) {
  return <div className="rounded border border-emerald-200 bg-emerald-50 p-4 text-sm text-emerald-700">{message}</div>;
}
