import type { ApiErrorBody } from './types';

export class ApiError extends Error {
  code: string;
  status: number;
  details?: unknown;

  constructor(status: number, code: string, message: string, details?: unknown) {
    super(message);
    this.status = status;
    this.code = code;
    this.details = details;
  }
}

/**
 * All requests go through the Next.js server's /api/* rewrite to
 * internal/server's chi router, so they are same-origin from the
 * browser's point of view and the backend's HTTP-only session cookie
 * works without any cross-origin cookie configuration.
 */
async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch(path, {
    method,
    headers: body !== undefined ? { 'Content-Type': 'application/json' } : undefined,
    body: body !== undefined ? JSON.stringify(body) : undefined,
    credentials: 'same-origin',
    cache: 'no-store',
  });

  if (res.status === 204) {
    return undefined as T;
  }

  const text = await res.text();
  const json = text ? JSON.parse(text) : undefined;

  if (!res.ok) {
    const errBody = json as ApiErrorBody | undefined;
    throw new ApiError(
      res.status,
      errBody?.error?.code ?? 'unknown_error',
      errBody?.error?.message ?? `Request failed with status ${res.status}`,
      errBody?.error?.details,
    );
  }

  return json as T;
}

export const api = {
  get: <T>(path: string) => request<T>('GET', path),
  post: <T>(path: string, body?: unknown) => request<T>('POST', path, body ?? {}),
  patch: <T>(path: string, body?: unknown) => request<T>('PATCH', path, body ?? {}),
  delete: <T>(path: string) => request<T>('DELETE', path),
};
