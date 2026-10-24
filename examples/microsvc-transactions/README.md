# Microsvc Transactions with Wiretap

Run the real [Speedscale Microsvc Transactions service](https://github.com/speedscale/microsvc)
from a local Docker image, capture its HTTP boundary with an ordinary Accounts
mock. No Microsvc
source changes are needed. Python `unittest` owns execution and reporting; its
lifecycle calls Wiretap's HTTP API directly. This is an advanced external-runtime
example for Docker and Python. For standard Go or JavaScript suites, start with
the [managed SDK examples](../README.md).

```bash
./examples/run-microsvc-transactions.sh
```

Requirements: Docker running, Go 1.26+, Python 3.9+, and these local images:

- `microsvc_transactions-service:latest`
- `microsvc_fraud-service:latest`
- `postgres:15-alpine`

Override image names with `TRANSACTIONS_IMAGE`, `FRAUD_IMAGE`, and
`POSTGRES_IMAGE`. For example, Compose installations that name images with
hyphens can use:

```bash
TRANSACTIONS_IMAGE=microsvc-transactions-service:latest \
FRAUD_IMAGE=microsvc-fraud-service:latest \
./examples/run-microsvc-transactions.sh --output ./artifacts/transactions
```

If needed, build the application images in your Microsvc checkout first with
`docker compose build transactions-service fraud-service`, and pull
`postgres:15-alpine`. The harness builds the current Wiretap checkout and a small
fixture image using `python:3.12-alpine`; Docker downloads that base if necessary.

## What runs

```text
Python unittest → Wiretap → real Transactions container
                              ├─ HTTP → Wiretap → Accounts mock
                              ├─ PostgreSQL (disposable)
                              └─ gRPC → real Fraud container
```

Each execution gets unique container names, an internal Docker network, and a
database stored in temporary memory. The test client runs inside that network;
no host ports are needed. Existing Microsvc
containers, volumes, images and configuration are left alone. The harness removes
its containers, network and fixture image on exit, retaining bundles and logs.

The Accounts mock is an ordinary HTTP server in `accounts_mock.py`, with a
balance and a call journal. Its administration endpoints prepare and inspect
fixtures directly; Wiretap captures the API calls made by Transactions.

Two authored scenarios run sequentially:

1. **Successful withdrawal:** list an empty transaction history, withdraw 25 from
   an account holding 100, then read the new transaction. Assert a completed
   withdrawal, balance 75, and the ownership/read-balance/update-balance calls.
2. **Insufficient funds:** attempt to withdraw 150 from an account holding 100.
   Assert HTTP 400, no new transaction, balance 100, and no balance update call.

Tests send standard W3C trace context. If the particular image does not propagate
it to Accounts, Wiretap can correlate the sole active session by timing. Such
relationships remain labelled as inferred; this example does not require parallel
test support from the image.

## Fixtures and authentication

`fixtures.py` prepares Accounts state, signs fresh JWTs and checks the captured
service's effects. The suite verifies authentication before starting capture.
Each test uses a fresh user ID; its normal setup and teardown own fixture state.
Authentication uses disposable signing keys and synthetic test credentials.
Captured Authorization headers and payloads are retained unchanged. The harness
discards its database after the run.

Failing tests and incomplete capture make the command fail and leave evidence
for inspection. Contract mismatches are recorded separately from the framework
outcome so deliberate rejection tests can pass.

## Bundles and contract scope

Output defaults to `artifacts/microsvc-transactions/`. The command prints the
capture path. Inspect `bundle.json` and `tests/` for captured exchanges and
`coverage.json` for spec-derived case coverage. Logs are kept in a sibling
`logs-<run>` directory.

- `schemas/transactions.yaml` is an example-maintained contract aligned with
  [Microsvc revision 1433243](https://github.com/speedscale/microsvc/tree/1433243aa37dc27f391f81d7c27e40ce7c757e95).
  It corrects the upstream spec's missing `/api` prefix, includes the positive
  amount rule, and describes the actual response DTO. Deposit, transfer and
  generic creation remain in the catalogue so untested operations stay visible.
- `schemas/accounts.yaml` describes the three operations assumed by the mock;
  it is a consumer subset, not coverage of the complete Accounts API or proof
  of agreement with its real provider.
- HTTP 400 bodies vary between field-error maps, Spring error objects and empty
  responses. The contract checks the rejection status, while tests check effects;
  it does not claim a common error-body schema.
- Each test attempt includes the downstream Accounts traffic actually observed.

Fraud gRPC and PostgreSQL traffic are outside Wiretap's HTTP coverage. The harness
does not run Kafka: its best-effort publication failure is bounded and is not a
tested guarantee. The internal network blocks Internet egress, so asynchronous
Stripe, PayPal and compliance calls can fail in the background; those integrations
are not covered by this example. Withdrawal uses Fraud's local low-risk rules.
The service's random-error and intentional-regression switches are disabled for
repeatability. This is an Accounts-boundary experiment, not the complete banking
environment.

Application images from other revisions can expose contract differences. Inspect
the bundle and logs when that happens; the harness reuses the selected image
without patching its implementation.

## Service configuration

The harness configures Spring security to exclude internal `ERROR` dispatches
while retaining authentication on incoming requests. This avoids losing the
original error status in this demo image. The fixture verifies that requests
without credentials still return 401; Wiretap does not rewrite responses.

The exact selected image IDs are saved in `logs-<run>/images.json` and the
Transactions image ID is included in capture metadata.
