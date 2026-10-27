# Jest SDK

Add HTTP API capture to existing Jest tests while keeping your assertions,
mocks and HTTP clients. Wiretap retains each test attempt in a portable archive,
measures OpenAPI coverage, and exports eligible JSON/REST traffic as Arazzo.
The adapter manages the runtime and preserves Jest's outcomes separately from
API contract violations.

Use Node.js 22+ and Jest 29.7 or 30 with Jest Circus and the Node environment.
Install the local SDK tarball alongside Jest and install the Wiretap binary:

```sh
npm install --save-dev /absolute/path/to/wiretap-sdk-0.1.0.tgz
```

Follow the [Jest guide](docs/jest.md) for installation, configuration, Supertest,
existing APIs, fixtures, tracing, retries, concurrency and retained outputs.
It also covers standalone runtime ownership and offline consumer/provider checks.
The [external Express/Jest example](https://github.com/mattlock83/wiretap/tree/main/examples/existing-jest)
demonstrates the integration with an existing suite.

The package includes TypeScript declarations and downloads no executable:

- `@wiretap/sdk` and `@wiretap/sdk/jest` expose `withWiretap` and `captureJest`.
- `@wiretap/sdk/tracing` initializes tracing before application clients load.
- `@wiretap/sdk/jest-environment` supplies the adapter's Jest environment.

Use synthetic data and dummy credentials; captured traffic is retained unchanged.
