import type { TracerProvider, Span } from '@opentelemetry/api';

export type Application = { description?: string } &
  ({ name: string; id?: string } | { name?: string; id: string });
export interface Schema {
  id?: string;
  type: 'openapi';
  source: { type: 'file'; path: string } | { type: 'url'; url: string } | { type: 'inline'; content: string };
}
export interface Dependency {
  assetName?: string;
  upstreamUrl: string;
  upstreamKind?: 'mock' | 'service' | 'unknown';
  schemas?: Schema[];
  /** HTTP dependency schemas are optional by default. True requires one;
   * supplied schemas are always validated. */
  schemaRequired?: boolean;
}
export interface TestIdentity { id: string; variant?: string; attemptOrdinal?: number; }
export interface TestOptions {
  identity?: TestIdentity;
  application?: Application;
  name?: string;
  tags?: string[];
  meta?: Record<string, unknown>;
  schemas?: Schema[];
  /** The HTTP SUT schema remains required by default. */
  schemaRequired?: boolean;
  sutBaseUrl?: string;
  dependencies?: Record<string, Dependency>;
  apiId?: string;
  sutTargetKind?: 'mock' | 'service' | 'unknown';
  testSource?: Record<string, unknown>;
  sutSource?: Record<string, unknown>;
  explicitSession?: boolean;
  drainTimeoutMs?: number;
  onSession?: (session: Session) => unknown | Promise<unknown>;
}
export interface CaptureOptions extends TestOptions {
  baseUrl?: string;
  /** Control HTTP deadline: 30 seconds by default; default teardown allows its
   * requested drain plus 10 seconds. Set 0 to disable or a positive integer to
   * override every control request, including cleanup. */
  timeoutMs?: number;
  proxy?: true | { listenAddr?: string; targetUrl?: string };
  instrumentation?: boolean;
  tracerProvider?: TracerProvider;
}
export interface Session {
  span: Span;
  sessionId: string;
  name: string;
  sutUrl: string;
  dependencyUrls: Record<string, string>;
  runId: string;
  bundlePath: string;
  traceContext: { traceparent: string; tracestate?: string };
}
export interface CaptureResponse {
  outputs?: OutputResult;
  run_id?: string;
  bundle_path?: string;
  attempt_path?: string;
  [key: string]: unknown;
}
export interface OutputOptions { coverage?: ('json' | 'text' | 'html')[]; arazzo?: boolean; }
export interface OutputResult {
  bundle_path: string;
  coverage: Record<string, string>;
  arazzo?: string;
  arazzo_files?: string[];
  errors: { format: string; message: string }[];
}
