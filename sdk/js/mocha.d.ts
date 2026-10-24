import type { IncomingMessage, ServerResponse } from 'node:http';
import type { CaptureOptions } from './src/index';
import type { ManagedRuntimeOptions } from './src/managed-runtime';

export interface MochaWiretapOptions extends Omit<ManagedRuntimeOptions, 'signal'> {
  /** Module exporting a Node request handler, or the handler itself. Wiretap owns its ephemeral HTTP listener. */
  app?: string | ((request: IncomingMessage, response: ServerResponse) => void);
  /** Advanced capture defaults; normal usage inherits .wiretap.yaml. */
  capture?: Omit<CaptureOptions, 'baseUrl' | 'explicitSession'>;
  /** Require propagated trace context instead of serial fallback attribution. */
  explicitSession?: boolean;
}

/** Add to .mocharc.cjs; existing tests, hooks and reporters are preserved. */
export function withWiretap<T extends object = Record<string, unknown>>(config?: T, options?: MochaWiretapOptions): T & { require: string[] };
/** Mocha root-hook plugin entry point, loaded automatically by withWiretap. */
export function mochaHooks(): Promise<{ beforeAll: Function; beforeEach: Function }>;
/** Mocha global fixture entry point, called after user suite cleanup. */
export function mochaGlobalTeardown(): Promise<void>;
