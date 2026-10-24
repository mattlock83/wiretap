import type { PlaywrightTestArgs, PlaywrightTestOptions, PlaywrightWorkerArgs, PlaywrightWorkerOptions, TestType, PlaywrightTestConfig } from '@playwright/test';
import type { CaptureOptions, Session, TestOptions } from './src/index';
import type { ManagedRuntimeOptions } from './src/managed-runtime';

export interface WiretapPlaywrightOptions extends Omit<ManagedRuntimeOptions, 'signal'> {
  /** Required for multiple workers; the application must propagate W3C traces to dependencies. */
  explicitSession?: boolean;
  capture?: Omit<CaptureOptions, 'baseUrl' | 'explicitSession' | 'tracerProvider' | 'instrumentation' | 'onSession' | 'testDefaults'> & {
    testDefaults?: Omit<TestOptions, 'onSession'>;
  };
}
export interface WiretapFixtures {
  wiretapOptions: Omit<TestOptions, 'onSession' | 'explicitSession' | 'identity'> & { headers?: Record<string, string> };
  wiretap: Pick<Session, 'sessionId' | 'sutUrl' | 'dependencyUrls' | 'traceContext'>;
}
export function withWiretap<T extends PlaywrightTestConfig>(config: T, options?: WiretapPlaywrightOptions): T;
export function capturePlaywright<T extends {}, W extends {}>(test: TestType<T, W>): TestType<T & WiretapFixtures, W>;
export const test: TestType<PlaywrightTestArgs & PlaywrightTestOptions & WiretapFixtures, PlaywrightWorkerArgs & PlaywrightWorkerOptions>;
export { expect } from '@playwright/test';
