import type { TracerProvider, Span } from '@opentelemetry/api';

export type Application = { description?: string } &
  ({ name: string; id?: string } | { name?: string; id: string });
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
  protocol?: 'http' | 'grpc';
  schema?: Schema;
  schemas?: Schema[];
  /** HTTP dependency schemas are optional by default. True requires one;
   * supplied schemas are always validated. gRPC still requires a contract. */
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
  sutProtocol?: 'http' | 'grpc';
  dependencies?: Record<string, Dependency> | Dependency[];
  apiId?: string;
  sutTargetKind?: 'mock' | 'service' | 'unknown';
  testSource?: Record<string, unknown>;
  sutSource?: Record<string, unknown>;
  explicitSession?: boolean;
  drainTimeoutMs?: number;
  onSession?: (session: Session) => unknown | Promise<unknown>;
}
export interface CaptureOptions extends TestOptions {
  outputs?: OutputOptions;
  baseUrl?: string;
  /** Control HTTP deadline: 30 seconds by default; default teardown allows its
   * requested drain plus 10 seconds. Set 0 to disable or a positive integer to
   * override every control request, including cleanup. */
  timeoutMs?: number;
  proxy?: true | { listenAddr?: string; targetUrl?: string };
  instrumentation?: boolean;
  tracerProvider?: TracerProvider;
  testDefaults?: TestOptions;
}
/** Framework-neutral options remain compatible with existing callers. */
export interface Config extends CaptureOptions {
  manageRuntime?: boolean;
  autoHooks?: boolean;
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
  traceContext: { traceparent: string; tracestate?: string };
}
export interface CaptureResponse {
  outputs?: OutputResult;
  run_id?: string;
  bundle_path?: string;
  attempt_path?: string;
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
  report(options?: { bundlePath?: string }): Promise<CaptureResponse>;
  finishRun(options?: { outputs?: OutputOptions }): Promise<CaptureResponse>;
}
export interface OutputOptions { coverage?: ('json' | 'yaml' | 'text')[]; postman?: boolean; postman_options?: { omit_incomplete?: boolean; grpc?: Record<string, { url?: string; proto_root?: string; proto_file?: string }> }; }
export interface OutputResult {
  bundle_path: string;
  coverage: Record<string, string>;
  postman: string[];
  postman_files?: string[];
  postman_omissions?: { attempt_id: string; flow_id: string; protocol: string; message: string }[];
  errors: { format: string; message: string }[];
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
}
export function startCapture(config?: Config): Promise<Runtime>;
export function createNodeTestSuite(config?: Config): Suite;
export function bindTest(runtime: Runtime | (() => Runtime), testFn: Function, defaults?: TestOptions): TestFunction;
export function initializeTracing(options?: { instrumentation?: boolean }): void;
