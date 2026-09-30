# Explore invalid provider inputs

`wiretap explore` turns a passing provider test capture into generated rejection
tests. Wiretap owns this optional test runner. Existing SDK capture integrations
remain libraries called by your own test framework.

The source bundle is a read-only build artifact. Hook code and executable
commands stay in the project. Exploration writes a new bundle containing a fresh
baseline, generated attempts, assertions and updated coverage. Reports can be
rebuilt later without the project, hooks or running application.

## Try the complete example

With Go and Python 3.9 or newer installed, from the repository root:

```bash
python3 examples/provider-exploration/demo.py
```

The example captures a happy-path test of an HTTP provider backed by SQLite.
External Python hooks insert a fresh database user and obtain credentials for
each case, assert that rejected requests created no order, and remove the
fixtures. It prints the original and generated bundle paths. Open the generated
bundle's `viewer/index.html`, then **Contract coverage → Invalid-input coverage**.

## Run against your provider

Add an `explore` section to your project `.wiretap.yaml`:

```yaml
version: 1
output_dir: artifacts
sut:
  id: orders
  url: http://127.0.0.1:8080
  schema: schemas/orders.openapi.yaml
explore:
  expected_statuses: [400, 422]
  max_cases: 100
  request_timeout: 15s
  hooks:
    before_case:
      command: [python3, tests/wiretap_hooks.py]
      timeout: 30s
    check_case:
      command: [python3, tests/wiretap_hooks.py]
    after_case:
      command: [python3, tests/wiretap_hooks.py]
  bindings:
    - name: user_id
      in: body
      path: /user_id
```

```bash
wiretap explore --bundle artifacts/<source-run> --test "creates an order" --config .wiretap.yaml
```

Use `--attempt <id>` when a test name has several attempts. With a single authored
attempt, the selector can be omitted. `--target http://host:port`, `--output <parent>`
and `--max-cases <n>` override the project defaults. The target is an HTTP(S)
origin; captured paths and query strings are retained, including any server base
path. The frozen source schema governs validation; changing the project schema
does not change that captured contract.

The SUT and its existing mocks must be available. Hooks can prepare that
environment, but the runner does not infer mock definitions from captured traffic.
Exploration sends provider requests directly and captures their responses;
it does not replay or capture downstream dependency calls. Rejection coverage
is provider-only.

The command prints the new bundle path. Exit status is zero when the fresh
baseline and all executed cases pass, one for test/execution failures, and two
for CLI usage errors. A case budget can leave targets unexecuted even with a zero
exit status; these remain **not covered**. Original coverage is not copied into
the new run: every credited exchange was executed against the current target.

## Lifecycle and external command protocol

| Event | Scope | Purpose |
| --- | --- | --- |
| `before_run` | Once | Prepare shared resources and run context. |
| `before_case` | Baseline and every variation | Reset state, seed users/resources, provide fresh authentication. |
| `before_request` | Each request after binding and mutation | Optionally sign the final request or supply request-specific headers. |
| `check_case` | After execution, before cleanup | Return named business-property checks. |
| `after_case` | Every attempted case, including failed setup | Clean up case fixtures. |
| `after_run` | Run completion, failure or cancellation | Release shared resources. |

All hooks are optional. Each invocation starts a fresh command in the directory
containing `.wiretap.yaml`, inherits the environment, and receives one JSON object
on standard input. Arguments are passed directly to the executable. To use a
shell, name it explicitly: `[bash, tests/setup.sh]` or
`[pwsh, -NoProfile, -File, tests/setup.ps1]`.

Standard output is either empty or one JSON object. Standard error is for logs.
A nonzero exit, timeout or malformed output is an execution failure. Default
timeout is 30 seconds per invocation; output is limited to 1 MiB. The runner
executes cases serially. No Wiretap language SDK is required.

Example input:

```json
{
  "version": 1,
  "event": "before_case",
  "run_id": "run-...",
  "case_id": "negative-...",
  "scenario": "creates an order",
  "mode": "rejection",
  "mutation": {
    "key": "...",
    "schema_id": "orders",
    "operation": "POST /orders",
    "flow_id": "flow-1",
    "instance_location": "/body/quantity",
    "constraint": "required",
    "expected_statuses": [400, 422]
  },
  "bindings": {},
  "state": {},
  "headers": {}
}
```

The baseline has `mode: baseline` and no mutation. Request hooks also receive
`flow_id` and `request` (method, URL, multivalue headers, JSON body and
`body_base64` with the exact final body bytes for request signing). Check and
cleanup hooks receive `exchanges`, `outcome` and any execution `error`. Each
exchange uses the bundle flow representation, including `request.HTTP` and
`response.HTTP`.

Example setup output:

```json
{
  "bindings": {"user_id": "fresh-user"},
  "headers": {"Authorization": "Bearer fresh-token"},
  "state": {"cleanup_user_id": "fresh-user"}
}
```

`before_run` supplies defaults copied into each case. `before_case` updates that
case's bindings, state and default request headers. `before_request` can return
headers for just that request, plus bindings/state for later steps. It cannot
replace the mutated payload. Wiretap revalidates the final request to ensure the
hook did not undo or add violations. `check_case` may update case context for
cleanup. Cleanup hook output is ignored.

Bindings and state are JSON values carried by Wiretap between separate processes.
Live database connections cannot cross that boundary. This context is not stored
in the bundle; request/response values remain subject to capture redaction.
Use hooks or response cookies to supply current authentication: captured
Authorization and Cookie headers are cleared before replay. Those headers and
Set-Cookie are removed from persisted evidence. Source redaction rules are
inherited, and `explore.redactions` can add ordinary Wiretap redaction rules for
other sensitive headers and payload fields. Hook logs and property messages are
project-authored output; avoid putting credentials in them.

`check_case` must return at least one explicit check when configured:

```json
{"checks": [{"name": "no order inserted", "passed": true}]}
```

An empty successful process exit is not an assertion. A failed property fails
the attempt and prevents coverage credit. The hook should choose applicable
checks using `mode`; a successful baseline may create an order, while a rejected
request should not. `check_case` runs after HTTP failures too, with the exchanges
that are available. An earlier hook or binding failure skips checking and goes
to cleanup.

Cleanup runs with its own timeout after failures/cancellation and should be
idempotent, including when setup only partly completed. Cleanup failure stops
exploration to avoid contaminating later cases. Abrupt host termination cannot
guarantee cleanup. Setup/check/signing failures also stop the run; ordinary HTTP
or property failures are recorded and exploration continues within its budget.
Name or tag fixtures with `run_id` and `case_id` when cleanup must recover them
after setup fails before returning its bindings.

## Apply hooks to selected requests

Hook configuration is per lifecycle event, with no YAML path matcher. A
`before_request` command can inspect `request.method` and the path in
`request.url`, then return headers only for matching requests. Return `{}` for
requests needing no changes; the command is still invoked for each request.

Case hooks can branch on `scenario`, `mode` and `mutation.operation`. A baseline
has no mutation, so handle it separately. A configured `check_case` must still
return an applicable assertion; an empty result is not a skipped check.
Bindings and response extraction can be restricted to a captured `flow_id` as
shown below.

## Sequences and fresh values

Wiretap first runs the full happy-path sequence from a fresh fixture. Every
request must be valid and every response must be a schema-valid 2xx. If that
baseline fails, no mutations execute. Every generated case gets fresh setup,
replays earlier steps, then sends the mutated request. Later happy-path steps
are omitted because rejection changes the flow.

Bindings support `body` (JSON pointer), `query`, `header`, `cookie` and `path`
(OpenAPI parameter name). Add `flow_id` to limit a binding to one captured flow.
Use explicit response extraction when a later request needs a newly created ID:

```yaml
explore:
  extract:
    - name: order_id
      flow_id: create-order-flow
      in: body
      path: /id
  bindings:
    - name: order_id
      flow_id: get-order-flow
      in: path
      path: id
```

Extraction also supports one response header (`in: header`, `path: Location`).
Cookie sessions are isolated per case and updated from actual responses. Missing
bindings/extractions fail explicitly. Hooks own non-HTTP setup, cleanup and any
data relationships that are not expressed by these rules.

## Generated cases and coverage

Version `wiretap-negative-1` supports OpenAPI 3.0 JSON bodies and scalar parameters:
missing required body/fields/parameters, wrong types, forbidden nulls, values
outside enums, numeric boundaries, string length/pattern mismatches and array
length limits. It mutates one location at a time and only admits candidates whose
request validation errors identify the intended constraint at that location.
Nested objects and existing array items are included. Authentication headers and
cookie parameters are not mutation targets.

The generator is deterministic. It uses bounded representative values, not every
possible permutation. Composed/recursive schemas, complex parameters, unsupported
serialization and cases without an isolated witness are skipped or rejected;
diagnostics accompany the baseline. Generation is capped at 10,000 targets,
schema depth 20, 32 existing array items per array, and generated length witnesses
below roughly 10,000 elements/characters. OpenAPI 3.1, non-JSON request bodies,
compressed requests, truncated or redacted seed request bodies, concurrent provider
sequences, general property generation and failure shrinking are not supported.

By default an invalid request must produce **400 or 422**, and its response must
match the frozen contract. Configure other validation 4xx statuses explicitly.
401, 403, 429, redirects, server errors and unexpected success never satisfy the
rejection check. Redirects are not followed. Response capture is bounded to 4 MiB;
incomplete exchanges earn no coverage. Receiving an allowed status proves the
configured rejection check; domain-specific reasons and side effects need
`check_case` assertions.

The new bundle contains each attempt's source run/attempt/hash, targeted
constraint, final request-verification result, response checks and properties.
`contract-coverage.json` includes a separate `rejection_coverage` catalogue,
displayed in the HTML Contract coverage tab and Markdown. It counts generated targets
as covered, failed or not covered. Targets never attempted due to budget or setup
failure remain not covered. Expected invalid requests cannot earn positive
request-condition coverage; a passing, valid error response can earn ordinary
response-condition coverage. Expected violations are labelled where retained
evidence permits revalidation. Redaction still makes ordinary condition
measurement conservative; the separately recorded runtime rejection checks
remain explicit evidence.
