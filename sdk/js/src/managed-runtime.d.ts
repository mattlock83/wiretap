import type { CaptureResponse, OutputOptions } from './index';

export interface ManagedRuntimeOptions {
  /** Executable path or command; defaults to WIRETAP_BINARY or wiretap on PATH. */
  binary?: string;
  /** Project configuration; omitted means discover .wiretap.yaml from cwd. */
  config?: string;
  cwd?: string;
  outputDir?: string;
  /** Override project export policy when the owned run finalizes. */
  outputs?: OutputOptions;
  startupTimeoutMs?: number;
  timeoutMs?: number;
  shutdownTimeoutMs?: number;
  signal?: AbortSignal;
}
export interface ManagedRuntime {
  readonly baseUrl: string;
  readonly sutUrl: string;
  readonly runtimeInfo: CaptureResponse;
  /** Finalize once, save the runtime receipt and stop the owned process, including on failure. */
  close(): Promise<CaptureResponse>;
}
export function startManagedRuntime(options?: ManagedRuntimeOptions): Promise<ManagedRuntime>;
