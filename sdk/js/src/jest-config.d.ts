import type { ManagedRuntimeOptions } from './managed-runtime';
import type { CaptureOptions } from './index';

export interface WiretapJestOptions extends Omit<ManagedRuntimeOptions, 'signal' | 'cwd'> {
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
