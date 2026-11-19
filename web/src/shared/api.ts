import { ErrorCodeValues, type ErrorCode } from './generated.ts';

type Method = 'GET' | 'HEAD' | 'POST' | 'PUT' | 'PATCH' | 'DELETE';
export interface RequestOptions {
  method?: Method;
  body?: unknown;
  csrf?: string;
  idempotencyKey?: string;
  retries?: 0 | 1;
  signal?: AbortSignal;
  fetcher?: typeof fetch;
}

export class ApiError extends Error {
  readonly status: number;
  readonly code: ErrorCode;
  readonly requestId?: string;

  constructor(status: number, code: ErrorCode, message: string, requestId?: string) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
    this.code = code;
    this.requestId = requestId;
  }
}

export function assertNoProductionMock(production: boolean, value: string | undefined) {
  if (production && value !== undefined && value !== '' && value !== 'false') {
    throw new Error('Mock services are forbidden in production');
  }
}

export async function apiRequest<T>(path: string, options: RequestOptions = {}): Promise<T> {
  if (!path.startsWith('/') || path.startsWith('//') || /[\\#\u0000-\u0020]/u.test(path)) throw new Error('invalid API path');
  const url = new URL(`/api/v1${path}`, 'https://local.invalid');
  if (!url.pathname.startsWith('/api/v1/')) throw new Error('invalid API path');
  const method = options.method ?? 'GET';
  const headers = new Headers({ Accept: 'application/json' });
  if (options.csrf) headers.set('X-CSRF-Token', options.csrf);
  if (options.idempotencyKey) headers.set('Idempotency-Key', options.idempotencyKey);
  if (options.body !== undefined) headers.set('Content-Type', 'application/json');
  const body = options.body === undefined ? undefined : JSON.stringify(options.body);
  const fetcher = options.fetcher ?? fetch;
  const retryable = method === 'GET' || method === 'HEAD' || !!options.idempotencyKey;
  const request: RequestInit = { method, headers, body, signal: options.signal, credentials: 'same-origin', redirect: 'error', cache: 'no-store' };
  let response: Response;
  try {
    response = await fetcher(url.pathname + url.search, request);
  } catch (err) {
    if (!(err instanceof TypeError) || !retryable || options.retries !== 1 || options.signal?.aborted) throw err;
    response = await fetcher(url.pathname + url.search, request);
  }
  if (response.ok && (response.status === 204 || method === 'HEAD')) return undefined as T;
  const content = response.headers.get('Content-Type')?.toLowerCase().split(';')[0]?.trim();
  const received: unknown = content === 'application/json' ? await response.json().catch(() => undefined) : undefined;
  if (response.ok && received !== undefined) return received as T;
  if (received && typeof received === 'object' && 'code' in received && 'message' in received && 'request_id' in received && typeof received.code === 'string' && typeof received.message === 'string' && typeof received.request_id === 'string' && (ErrorCodeValues as readonly string[]).includes(received.code)) {
    throw new ApiError(response.status, received.code as ErrorCode, received.message, received.request_id);
  }
  throw new ApiError(response.status, 'INTERNAL_ERROR', 'Invalid API response', response.headers.get('X-Request-ID') ?? undefined);
}
