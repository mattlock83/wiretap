import type { CaptureOptions } from './index';

export interface WiretapJestOptions {
  /** Executable path or command; defaults to WIRETAP_BINARY or wiretap on PATH. */
  binary?: string;
  /** Project configuration; omitted means discover .wiretap.yaml from Jest's rootDir. */
  config?: string;
  outputDir?: string;
  startupTimeoutMs?: number;
  timeoutMs?: number;
  shutdownTimeoutMs?: number;
  /** Express/Node handler module, loaded inside each Jest test file. Supports <rootDir>. */
  app?: string;
  /** Advanced serializable capture options; dynamic fixtures use captureJest. */
  capture?: Omit<CaptureOptions, 'onSession' | 'explicitSession' | 'tracerProvider'>;
  /** Required for parallel workers; every boundary must propagate test trace context. */
  explicitSession?: boolean;
}

/** Add owned runtime setup and automatic capture to an existing Node/Jest Circus configuration. */
export function withWiretap<T extends object = Record<string, unknown>>(
  config?: T,
  options?: WiretapJestOptions
): T & {
  maxWorkers: number | string;
  testEnvironment: string;
  globalSetup: string;
  globalTeardown: string;
  testEnvironmentOptions: Record<string, unknown>;
};
