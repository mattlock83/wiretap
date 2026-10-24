import type { ManagedRuntimeOptions } from './managed-runtime';

export interface WiretapJestOptions extends Omit<ManagedRuntimeOptions, 'signal' | 'cwd'> {
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
