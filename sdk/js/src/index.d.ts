import type { TracerProvider, Span } from '@opentelemetry/api';

export interface Schema {
  id?: string;
  role?: 'sut' | 'dependency';
  type: 'openapi';
  source: { type: 'file'; path: string } | { type: 'url'; url: string } | { type: 'inline'; content: string };
}
export interface Dependency {
  assetId?: string;
  assetName?: string;
  upstreamUrl: string;
  upstreamKind?: 'mock' | 'service' | 'unknown';
  schema?: Schema;
  schemas?: Schema[];
  schemaRequired?: boolean;
}
export interface GenerateSchemaOptions {
  outputPath?: string;
  bundlePath?: string;
  title?: string;
  version?: string;
  force?: boolean;
}
export interface TestOptions {
  name?: string;
  tags?: string[];
  meta?: Record<string, unknown>;
  schemas?: Schema[];
  schemaRequired?: boolean;
  generateSchema?: boolean | string | GenerateSchemaOptions;
  sutBaseUrl?: string;
  dependencies?: Record<string, Dependency> | Dependency[];
  apiId?: string;
  sutTargetKind?: 'mock' | 'service' | 'unknown';
  testSource?: Record<string, unknown>;
  sutSource?: Record<string, unknown>;
  explicitSession?: boolean;
  drainTimeoutMs?: number;
  redactions?: { field: string; rule: string; replacement?: string }[];
  onSession?: (session: Session) => unknown | Promise<unknown>;
}
export interface Config extends TestOptions {
  baseUrl?: string;
  timeoutMs?: number;
  proxy?: true | { listenAddr?: string; targetUrl?: string };
  instrumentation?: boolean;
  tracerProvider?: TracerProvider;
  manageRuntime?: boolean;
  autoHooks?: boolean;
  testDefaults?: TestOptions;
  testApi?: { test: Function; before: Function; after: Function };
}
export interface Session {
  span: Span;
  sessionId: string;
  name: string;
  sutUrl: string;
  dependencyUrls: Record<string, string>;
  runId: string;
  bundlePath: string;
  indexPath: string;
  viewerPath: string;
  traceContext: { traceparent: string; tracestate?: string };
}
export interface CaptureResponse {
  run_id?: string;
  bundle_path?: string;
  index_path?: string;
  viewer_path?: string;
  attempt_path?: string;
  attempt_page_path?: string;
  [key: string]: unknown;
}
export interface Runtime {
  readonly sutUrl: string;
  readonly activeSession: Session | null;
  createTestAttempt(name: string, options?: TestOptions): TestAttempt;
  setupTest(options?: TestOptions): Promise<Session>;
  teardownTest(options?: { sessionId?: string; status?: 'pass' | 'fail' | 'skip'; drainTimeoutMs?: number }): Promise<CaptureResponse>;
  runTest<T>(name: string, callback: (session: Session) => T | Promise<T>, options?: TestOptions): Promise<T>;
  dependencyUrl(assetId: string): string;
  report(options?: { bundlePath?: string; workflowPath?: string }): Promise<CaptureResponse>;
  generateSchema(options?: GenerateSchemaOptions): Promise<CaptureResponse>;
  finishRun(): Promise<CaptureResponse>;
}
export interface TestAttempt {
  /** Run test or fixture work inside this attempt's trace context. */
  run<T>(callback: (session: Session) => T | Promise<T>): Promise<T>;
  /** Call from the framework's completion hook, including failures/timeouts. */
  finish(outcome?: { status?: 'pass' | 'fail' | 'skip'; error?: unknown }): Promise<CaptureResponse>;
}
export interface TestFunction {
  (name: string, callback: (context: any, session: Session) => unknown): unknown;
  (name: string, options: Record<string, unknown>, callback: (context: any, session: Session) => unknown): unknown;
}
export interface Suite {
  test: TestFunction;
  readonly runtime: Runtime;
  readonly sutUrl: string;
  dependencyUrl(assetId: string): string;
  start(): Promise<Runtime>;
  close(): Promise<unknown>;
  report(options?: Parameters<Runtime['report']>[0]): Promise<CaptureResponse>;
  generateSchema(options?: GenerateSchemaOptions): Promise<CaptureResponse>;
}
export function startCapture(config?: Config): Promise<Runtime>;
export function createNodeTestSuite(config?: Config): Suite;
export function bindTest(runtime: Runtime | (() => Runtime), testFn: Function, defaults?: TestOptions): TestFunction;
export function initializeTracing(options?: { instrumentation?: boolean }): void;
