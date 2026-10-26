import type { CaptureOptions } from './src';
import type { RequestListener } from 'node:http';
export { withWiretap } from './src/jest-config';
/** Capture existing Jest callbacks, optionally hosting an Express/Node app. */
export function captureJest(config?: CaptureOptions & { app?: RequestListener }, api?: object): void;
