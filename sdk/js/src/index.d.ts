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
  protocol?: 'http' | 'grpc';
  schema?: Schema;
  schemas?: Schema[];
  schemaRequired?: boolean;
}
export interface TestOptions {
  name?: string;
  tags?: string[];
  meta?: Record<string, unknown>;
  schemas?: Schema[];
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
  explore(options?: ExploreOptions): Promise<ExplorationResult>;
  readonly sutUrl: string;
  readonly activeSession: Session | null;
  createTestAttempt(name: string, options?: TestOptions): TestAttempt;
  setupTest(options?: TestOptions): Promise<Session>;
  teardownTest(options?: { sessionId?: string; status?: 'pass' | 'fail' | 'skip'; drainTimeoutMs?: number }): Promise<CaptureResponse>;
  runTest<T>(name: string, callback: (session: Session) => T | Promise<T>, options?: TestOptions): Promise<T>;
  dependencyUrl(assetId: string): string;
  report(options?: { bundlePath?: string }): Promise<CaptureResponse>;
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
}
export function startCapture(config?: Config): Promise<Runtime>;
export function createNodeTestSuite(config?: Config): Suite;
export function bindTest(runtime: Runtime | (() => Runtime), testFn: Function, defaults?: TestOptions): TestFunction;
export function initializeTracing(options?: { instrumentation?: boolean }): void;

export type ExploreEvent = 'before_explore' | 'before_case' | 'before_request' | 'check_case' | 'after_case' | 'after_explore';
export interface ExploreHookInput {
  hook_id?: string;
  invocation_id: string;
  parent_attempt_id: string;
  version: number;
  event: ExploreEvent;
  run_id: string;
  case_id?: string;
  scenario: string;
  mode?: 'baseline' | 'rejection';
  operation?: string;
  operations?: string[];
  flow_id?: string;
  mutation?: Record<string, unknown>;
  request?: { method: string; url: string; headers: Record<string, string[]>; body?: unknown; body_base64?: string };
  grpc_request?: { procedure: string; metadata: Record<string, string[]>; messages: unknown[]; messages_base64: string[]; response_gates?: number[] };
  exchanges?: Record<string, unknown>[];
  bindings: Record<string, unknown>;
  state: Record<string, unknown>;
  headers: Record<string, string>;
  outcome?: string;
  error?: string;
  trace_context?: { traceparent: string; tracestate?: string };
  /** Observe cancellation in asynchronous hook work. */
  signal: AbortSignal;
}
export interface ExploreHookOutput {
  bindings?: Record<string, unknown>;
  headers?: Record<string, string>;
  state?: Record<string, unknown>;
  checks?: { name: string; passed: boolean; message?: string }[];
}
export type ExploreCallback = (input: ExploreHookInput) => ExploreHookOutput | void | Promise<ExploreHookOutput | void>;
export type ExploreHook = ExploreCallback | { operations?: string[]; handler: ExploreCallback };
export interface ExploreOptions {
  /** Exact spec operations such as POST /orders/{id} or /orders.v1.Orders/CreateOrder; omitted selects supported provider operations in the seed. */
  operations?: string[];
  flowId?: string;
  /** Zero/omitted runs all supported generated cases. */
  maxCases?: number;
  requestTimeoutMs?: number;
  hookTimeoutMs?: number;
  cleanupTimeoutMs?: number;
  signal?: AbortSignal;
  hooks?: Partial<Record<'beforeExplore' | 'beforeCase' | 'beforeRequest' | 'checkCase' | 'afterCase' | 'afterExplore', ExploreHook | ExploreHook[]>>;
  bindings?: { name: string; in: 'body' | 'query' | 'header' | 'path' | 'cookie' | 'metadata'; path: string; flowId?: string; messageIndex?: number }[];
  extract?: { name: string; in: 'body' | 'header' | 'metadata' | 'trailers'; path: string; flowId: string; messageIndex?: number }[];
  expectedStatuses?: number[];
  expectedGrpcStatuses?: number[];
  redactions?: TestOptions['redactions'];
}
export interface ExplorationResult {
  generated: number;
  executed: number;
  passed: number;
  failed: number;
  baselines: number;
  skipped: number;
  complete: boolean;
  notes?: string[];
  attempt_ids?: string[];
  bundlePath?: string;
}
export class ExplorationError extends Error {
  result?: ExplorationResult;
}
export interface ExplorationReference { session_id: string; exploration_id: string; }
export interface ExplorationHookDescriptor { id: string; event: ExploreEvent; operations?: string[]; }
export interface StartExplorationRequest {
  session_id: string;
  operations?: string[];
  flow_id?: string;
  max_cases?: number;
  request_timeout?: string;
  hook_timeout?: string;
  hooks?: ExplorationHookDescriptor[];
  bindings?: { name: string; in: string; path: string; flow_id?: string; message_index?: number }[];
  extract?: { name: string; in: string; path: string; flow_id: string; message_index?: number }[];
  expected_statuses?: number[];
  expected_grpc_statuses?: number[];
  redactions?: TestOptions['redactions'];
}
export interface ExplorationResponse {
  exploration_id: string;
  state: 'running' | 'complete' | 'failed' | 'cancelled';
  action?: { id: string; hook_id: string; event: ExploreEvent; input: Omit<ExploreHookInput, 'signal'> };
  result?: Omit<ExplorationResult, 'bundlePath'> & { bundle_path?: string };
  error?: string;
}
export class CoreClient {
  constructor(config?: { baseUrl?: string; timeoutMs?: number });
  startRun(payload?: Record<string, unknown>): Promise<CaptureResponse>;
  setupTest(payload?: Record<string, unknown>): Promise<CaptureResponse>;
  teardownTest(payload?: Record<string, unknown>): Promise<CaptureResponse>;
  report(payload?: Record<string, unknown>): Promise<CaptureResponse>;
  finishRun(): Promise<CaptureResponse>;
  startExploration(payload: StartExplorationRequest, options?: { signal?: AbortSignal }): Promise<ExplorationResponse>;
  nextExploration(payload: ExplorationReference, options?: { signal?: AbortSignal }): Promise<ExplorationResponse>;
  respondExploration(payload: ExplorationReference & { action_id: string; output?: ExploreHookOutput; error?: string }, options?: { signal?: AbortSignal }): Promise<unknown>;
  cancelExploration(payload: ExplorationReference, options?: { signal?: AbortSignal }): Promise<unknown>;
}
