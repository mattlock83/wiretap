# Microsvc Transactions with Wiretap

Run the real [Speedscale Microsvc Transactions service](https://github.com/speedscale/microsvc)
from a local Docker image, capture its HTTP boundary with an ordinary Accounts
mock, and explore invalid withdrawal requests inside the same test. No Microsvc
source changes are needed. Python `unittest` owns execution and reporting; its
lifecycle and local exploration callbacks call Wiretap's HTTP API directly.

```bash
./examples/run-microsvc-transactions.sh
```

Requirements: Docker running, Go, Python 3.9+, and these local images:

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

## Exploration and hooks

The successful withdrawal test awaits scoped exploration before closing its
capture session. The SUT, mock and capture proxy remain available throughout.
There is no separate saved-bundle execution command or SUT restart.

`test_transactions.py` passes ordinary local callbacks to the small HTTP example
helper in `examples/explore_http.py`. Fixture helpers remain in `hooks.py`:

- `before_explore`: check that missing credentials return 401 and a fresh JWT
  succeeds. Misconfigured authentication stops before generated checks.
- `before_case`: reset Accounts to 100 and issue a JWT for a fresh test user.
- `before_request`: sign a fresh JWT for the case's user. Its scope includes
  `POST /api/transactions/withdraw` and the prerequisite history requests.
- `check_case`: verify the user's transaction history, balance and Accounts
  call sequence. Rejected requests must create no transaction or account calls;
  the happy baseline must withdraw exactly 25.
- `after_case`: clear the Accounts fixture and call journal.

Transactions accepts signed user claims without looking up a User service row,
so a randomly generated signing key is shared only by this run's containers and
test harness. The hooks generate credentials outside the bundle. Authorization
headers are removed from retained traffic. Fresh user IDs isolate transaction
rows between cases; the entire disposable database is removed after the run.

Exploration checks required fields, types, nullability and the positive amount
boundary supported by the supplied contract. It requires HTTP 400 for invalid
inputs; HTTP 401 is still a failure. The example configures the disposable SUT's
Spring error dispatch as described below so parsing errors retain their status.
Any failing authored test, generated case or property check makes the command
exit unsuccessfully and leaves evidence for inspection.

## Bundles and contract scope

Output defaults to `artifacts/microsvc-transactions/`. The command prints the
one combined bundle path. Open `docs/viewer.html` or
`index.md`; rejection results appear under **Contract coverage → Invalid-input
coverage**. Logs are kept in a sibling `logs-<run>` directory.

- `schemas/transactions.yaml` is an example-maintained contract aligned with
  [Microsvc revision 1433243](https://github.com/speedscale/microsvc/tree/1433243aa37dc27f391f81d7c27e40ce7c757e95).
  It corrects the upstream spec's missing `/api` prefix, includes the positive
  amount rule, and describes the actual response DTO. Deposit, transfer and
  generic creation remain in the catalogue so untested operations stay visible.
- `schemas/accounts.yaml` describes the three operations assumed by the mock;
  it is a consumer subset, not coverage of the complete Accounts API or proof
  of agreement with its real provider.
- HTTP 400 bodies vary between field-error maps, Spring error objects and empty
  responses. The contract checks the rejection status, while hooks check effects;
  it does not claim a common error-body schema.
- Authored attempts include downstream Accounts traffic. Each generated case
  records its provider traffic and explicit hook assertions; dependency evidence
  is credited only where actual traffic was captured.

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

## Authentication and Spring error dispatch

The locally available image `3c8e7bdf02f6` has a separate error-dispatch issue:
even with a valid JWT, Spring routes some input-parsing failures through `/error`
where authentication is lost. Six of the 11 generated cases originally returned
401 instead of 400. Refreshing a token alone cannot fix that application behavior.

The harness sets `SPRING_SECURITY_FILTER_DISPATCHER_TYPES=request,async,forward,include`
on its disposable Transactions container. This uses Spring Boot's
[security filter dispatcher setting](https://docs.spring.io/spring-boot/docs/3.2.12/reference/html/application-properties.html#application-properties.security.spring.security.filter.dispatcher-types)
to exclude internal `ERROR` dispatches while retaining authentication on incoming
requests. It allows Spring's original 400 response to reach Wiretap. Requests
without credentials still return 401, as checked by the hook. This is an explicit
configuration workaround for this demo image, not a Wiretap response rewrite or
a change to your existing Docker stack. For an independently managed SUT, its
owner must fix error-dispatch security separately from supplying valid tokens.

The expected result for this contract is 11 generated rejection cases with
passing side-effect checks. The signing hook is
specific to this demo; for another application, replace `token()` with its test
login/token flow and keep the same hook lifecycle. Never store signing keys or
access tokens in the bundle.

The exact selected image IDs are saved in `logs-<run>/images.json` and the
Transactions image ID is included in authored capture metadata.
