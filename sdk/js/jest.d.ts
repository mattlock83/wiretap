import type { Config, Runtime } from './src';
interface JestTest {
  (name: string, callback: (...args: any[]) => unknown, timeout?: number): unknown;
  each(cases: readonly unknown[]): (name: string, callback: (...args: any[]) => unknown, timeout?: number) => unknown;
  only: JestTest;
  skip: JestTest;
  concurrent: JestTest;
  todo(name: string): unknown;
}
export function createJestSuite(config?: Config, api?: object): {
  test: JestTest;
  readonly runtime: Runtime;
  readonly sutUrl: string;
  dependencyUrl(assetId: string): string;
};
