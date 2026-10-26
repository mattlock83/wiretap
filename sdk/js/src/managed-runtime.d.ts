import type { CaptureResponse } from './index';

export interface ManagedRuntimeOptions {
  /** Executable path or command; defaults to WIRETAP_BINARY or wiretap on PATH. */
  binary?: string;
  /** Project configuration; omitted means discover .wiretap.yaml from cwd. */
  config?: string;
  cwd?: string;
  outputDir?: string;
  startupTimeoutMs?: number;
  timeoutMs?: number;
  shutdownTimeoutMs?: number;
  signal?: AbortSignal;
}
export interface ManagedRuntime {
  readonly baseUrl: string;
  readonly sutUrl: string;
  readonly runtimeInfo: CaptureResponse & { run_id: string; bundle_path: string; sut_url: string };
  /** Finalize once and stop the owned process, including on failure. The canonical
   * receipt and bounded process log are saved inside runtimeInfo.bundle_path. */
  close(): Promise<CaptureResponse & { status: 'complete'; run_id: string; bundle_path: string }>;
}
export function startManagedRuntime(options?: ManagedRuntimeOptions): Promise<ManagedRuntime>;
