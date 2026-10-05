# Explore invalid inputs inside your tests

Call `wiretap.explore()` after your ordinary happy-path request and assertions,
while its capture session remains open. Wiretap uses that completed traffic and
the frozen OpenAPI or Protobuf contract to generate rejection checks. Awaiting exploration
makes those checks part of the enclosing test in Jest, `node:test`, Go `testing`,
or any other framework using the HTTP API.

Each baseline and generated case is recorded as its own attempt in the **same
run bundle**. There is no previous bundle to prepare or second test runner to
launch. Your framework owns test reporting, including JUnit if configured there.
The bundle retains the detailed generated cases, assertions and coverage.

## Try the complete examples

From the repository root:

```bash
npm ci --prefix sdk/js
./examples/run-basic-rest-e2e.sh
```

This runs an ordinary Node test, captures its happy path and existing inventory
mock, then calls scoped exploration inside the same test. For a provider with
SQLite state and authentication, run:

```bash
python3 examples/provider-exploration/demo.py
```

The Python example uses the HTTP API directly. Local callbacks create a fresh
user for every case, supply credentials and IDs, assert database effects, and
remove fixtures. It requires Go and Python 3.9+, with no Python dependencies.
Open the printed bundle's `docs/viewer.html`, then **Contract coverage →
Invalid-input coverage**. [Microsvc Transactions](../examples/microsvc-transactions/README.md)
shows the same lifecycle in `unittest` against a real service image.
For native gRPC, run `./examples/grpc-exploration/run.sh`; its Go test uses
Protovalidate annotations, fresh fixtures and authentication metadata. It also
supports a local descriptor set with reflection disabled.

## Call from an existing test

```javascript
await wiretap.runTest('creates an order and rejects invalid inputs', async () => {
  const fixture = await createUser();
  try {
    const response = await fetch(`${wiretap.sutUrl}/orders`, {
      method: 'POST',
      headers: { 'content-type': 'application/json', Authorization: fixture.token },
      body: JSON.stringify({ user_id: fixture.id, quantity: 2 })
    });
    assert.equal(response.status, 201);
    await assertOrderCreated(fixture.id);

    await wiretap.explore({
      operations: ['POST /orders'],
      expectedStatuses: [400],
      bindings: [{ name: 'user_id', in: 'body', path: '/user_id' }],
      hooks: {
        beforeCase: async () => {
          const user = await createUser();
          return { bindings: { user_id: user.id }, state: { token: user.token } };
        },
        beforeRequest: {
          operations: ['POST /orders'],
          handler: async event => ({ headers: { Authorization: event.state.token } })
        },
        checkCase: async event => ({ checks: [{
          name: 'expected database effect',
          passed: await orderCount(event.bindings.user_id) ===
            (event.mode === 'baseline' ? 1 : 0)
        }] }),
        afterCase: async event => deleteUserAndOrders(event.bindings.user_id)
      }
    });
  } finally {
    await deleteUserAndOrders(fixture.id);
  }
});
```

Fixture helpers in this example are your ordinary application test helpers.
Callbacks can use live database connections and other variables from the test
process. Only returned JSON bindings, state, headers and checks cross the HTTP
boundary. SDKs contain callback dispatch and lifecycle wrappers; generation,
execution, cancellation and recording live in the Wiretap binary.

An exploration failure rejects the JavaScript promise or returns a Go error.
Await/check it as you would any other test assertion. All checks must finish
before the parent capture is torn down. Await pending happy-path requests before
starting exploration; unfinished traffic cannot be a stable seed.

## Select operations and hook scope

`operations` contains exact OpenAPI **method and spec path** selectors, such as
`POST /orders` or `GET /orders/{id}`, or fully qualified native gRPC procedures
such as `/orders.v1.Orders/CreateOrder`. The SDK's optional `flowId` selects one
captured request when an operation was called several times. Omitting operation
selection includes eligible captured provider operations. Unknown selectors or
operations without a usable captured seed fail explicitly.

A hook can be a callback, a scoped `{operations, handler}` object, or an array of
scoped definitions. For example, use one request signer for `POST /orders` and
another for `GET /orders/{id}`. Request hooks match the current request's spec
operation; case hooks match the case's target operation. A baseline is associated
with its selected operation, so it participates in the same setup and cleanup.

Scope chooses where to generate mutations. Earlier happy-path requests can still
be needed as prerequisites and execute before the selected request. Configure
request hooks for those prerequisites too. Exploration does not invent mock
responses or start dependencies: existing mocks and services remain under your
test harness's control.

## Lifecycle callbacks

| SDK hook | HTTP event | When it runs |
| --- | --- | --- |
| `beforeExplore` | `before_explore` | Once, before the first baseline. Prepare shared context. |
| `beforeCase` | `before_case` | Before each baseline and rejection case, to reset state and create fixtures. |
| `beforeRequest` | `before_request` | After binding and mutation, to authenticate or sign the final request. |
| `checkCase` | `check_case` | After traffic, before cleanup. Check database effects and other business properties. |
| `afterCase` | `after_case` | Clean up each attempted case, including partial setup or execution failures. |
| `afterExplore` | `after_explore` | Release shared resources after completion, failure or cancellation. |

All callbacks are optional. Cases run serially within an invocation. Shared
fixtures still need isolation if the test framework runs several parent tests
concurrently. Callbacks receive `trace_context` for routing requests through the
correct session: invocation hooks use the parent trace, and case hooks use that
generated case's trace. SDKs activate this context around local callbacks;
raw HTTP integrations should propagate it when making requests through Wiretap.

Traffic used by a callback to prepare fixtures, authenticate or inspect effects
is excluded from captured evidence and coverage while that callback is active.
Only the generated provider requests and dependency exchanges they produce
contribute case evidence. Await all callback work before returning so background
fixture traffic cannot escape that lifecycle boundary.

Callbacks receive `event`, `run_id`, `case_id`, `scenario`, `operation`, `mode`
(`baseline` or `rejection`), `bindings`, `state` and `headers`. Rejection cases
include `mutation`, identifying the flow, constraint and target location.
Request callbacks also receive `flow_id` and `request` with method, URL,
multivalue headers, decoded JSON body and `body_base64` for exact signing bytes.
For gRPC, `grpc_request` contains `procedure`, multivalue `metadata`, ProtoJSON
`messages`, exact Protobuf `messages_base64` and `response_gates` describing the
observed receive-before-send order. Returned `headers` become request metadata.
Check and cleanup callbacks receive available `exchanges`, `outcome` and errors.

Callbacks can return JSON `bindings`, `state` and `headers`. Only `checkCase`
may also return `checks`; other lifecycle events must omit that field. A
`checkCase` result can therefore look like:

```json
{
  "bindings": {"user_id": "fresh-user"},
  "state": {"cleanup_user_id": "fresh-user"},
  "headers": {"Authorization": "Bearer fresh-token"},
  "checks": [{"name": "no order inserted", "passed": true}]
}
```

`beforeExplore` supplies defaults copied into cases; `beforeCase` updates that
case's context. `beforeRequest` supplies request-specific headers and can update
context for later requests. It cannot silently replace the intended mutation.
Wiretap revalidates the final request so a signing hook cannot undo the target
violation or introduce unrelated violations. A configured `checkCase` must
return at least one explicit check, or throw to fail. An empty result is not an
assertion. Cleanup callback results do not alter successful checks.

Setup, signing, callback, transport and incomplete-capture failures stop
exploration. Ordinary rejection-status and property failures remain recorded
and allow subsequent independent cases to run. Cleanup failure also stops
subsequent cases because fixture isolation is uncertain, and fails the awaited
exploration call and its enclosing test.
Cleanup callbacks should be idempotent and tolerate partially completed setup.
They have a bounded cleanup opportunity after cancellation; an abruptly killed
test process cannot run local cleanup. JavaScript callbacks receive an abort
`signal` and should honor it for long operations.

## Fresh values and authentication

HTTP bindings target `body` (JSON pointer), `query`, `header`, `cookie` or `path`
(OpenAPI parameter name). Use `flowId` to restrict a binding to one captured flow.
For sequences, `extract` can obtain a value from an earlier response's body or
header and bind it into a later request. The HTTP form uses `flow_id`.

```javascript
await wiretap.explore({
  operations: ['GET /orders/{id}'],
  extract: [{ name: 'order_id', flowId: createFlowId, in: 'body', path: '/id' }],
  bindings: [{ name: 'order_id', flowId: getFlowId, in: 'path', path: 'id' }]
});
```

For gRPC, body bindings use ProtoJSON pointers and optional `messageIndex`
(`message_index` over HTTP). Omitting the index applies a binding to every
request message; response body extraction defaults to message zero. Bind or
extract `metadata`, and extract response `trailers`, by key. Protocol fields
remain native: gRPC does not acquire HTTP query, path or cookie semantics.

Cookie sessions are isolated per HTTP case. Missing bindings and extractions fail
explicitly. Captured Authorization and Cookie headers are cleared before
execution; supply fresh authentication through callbacks or actual responses.
Authorization, Cookie and Set-Cookie values are removed from persisted evidence.
Parent redaction rules apply, and each call can add `redactions`. Runtime context
and hook code are not bundle contents. Avoid credentials in check messages.

## Generated cases and coverage

Version `wiretap-negative-1` supports OpenAPI 3.0 JSON bodies and scalar parameters:
missing required bodies/fields/parameters, wrong types, forbidden nulls, values
outside enums, numeric boundaries, string length/pattern mismatches and array
length limits. It mutates one location at a time, admitting only candidates
whose validation errors identify the intended constraint at that location.
Nested objects and existing array items are included. Authentication headers and
cookie parameters are not mutation targets.

There is **no default case budget**: all supported cases in scope execute. Set
`maxCases` explicitly if useful; unexecuted targets remain not covered. This is a
finite catalogue of representative violations, not every payload permutation.
Safety bounds still apply: at most 10,000 generated targets, schema depth 20,
32 existing items per array, and length witnesses below roughly 10,000
characters/elements. Generation diagnostics expose unsupported or bounded work.
Request timeout defaults to 15 seconds; hook timeout defaults to 30 seconds.

Composed/recursive schemas, complex parameters, unsupported serialization and
cases without an isolated witness are skipped or rejected. OpenAPI 3.1,
non-JSON or compressed request bodies, incomplete/redacted seed bodies,
concurrent provider sequences, general property generation and shrinking remain
unsupported. Other wire protocols fail explicitly. HTTP providers can have gRPC
dependencies, and gRPC providers can have HTTP or gRPC dependencies.

For HTTP, each selected operation has a fresh baseline that replays its happy-path prefix.
That baseline must be schema-valid and succeed before its rejection cases execute. By default rejection requires **400 or 422**, with a response
matching the frozen contract. Configure other validation 4xx statuses explicitly.
401, 403, 429, redirects, server errors and unexpected success never establish
input validation. Redirects are not followed. Incomplete response capture earns
no coverage. Domain-specific reasons and side effects need `checkCase` checks.

## Native gRPC cases

Native unary, server-streaming, client-streaming and bidirectional RPCs use the
same SDK call and local lifecycle. Select a procedure and, if needed, its captured
`flowId`. Set `expectedGrpcStatuses: [3]` (Go: `ExpectedGRPCStatuses`, HTTP:
`expected_grpc_statuses`) for `INVALID_ARGUMENT`, the default. Explicit
`OUT_OF_RANGE` (11) is also supported. Authentication, deadline, transport,
internal errors and arbitrary non-OK codes never prove input validation.

A plain Protobuf contract usually has no application length/range rules. Add
standard `(buf.validate.field)` annotations when those constraints are part of
the API contract; freeze their imported definitions with the rest of the
contract. Reflection and local descriptors behave identically. Wiretap currently
generates isolated witnesses for annotated required fields, string length
bounds, numeric bounds, enum membership/defined values, boolean constants,
repeated collection length/uniqueness and minimum map size. Unsupported rules
and cases with no isolated witness remain visible in generation notes.
It does not invent missing constraints or corrupt the Protobuf wire format.

Protovalidate verifies each baseline and the exact intended rule violation.
Other rules, including custom CEL, are evaluated to prevent an unrelated
violation from passing as the intended case. Wiretap does not synthesize
arbitrary CEL, regex, application semantics or new stream conversations.

A baseline replays earlier provider RPCs and the full selected RPC. A generated
stream case retains the recorded input prefix through the mutated message,
then half-closes. Observed receive-before-send dependencies are retained, so
bidirectional exchanges that need an earlier server response can progress.
A repeated rule has one contract target; evidence identifies the exact message
index for each generated witness. Empty, incomplete, redacted or undecodable
seeds cannot manufacture cases or assurance. Concurrent provider RPC sequences
are unsupported, and all executions have bounded deadlines.

Successful rejection evidence requires complete native observations, verified
Protobuf decoding and an expected application status actually supplied by the
upstream. A trailers-only rejection is valid: no response message is invented.
Use `checkCase` for expected database effects and other business properties.
Passive structural condition coverage remains separate from this generated
rule catalogue; this feature does not claim a full Protovalidate coverage model.

The one bundle records every case's lineage, target, request-verification result,
response checks and properties. `docs/contract-coverage.json` contains the
separate `rejection_coverage` catalogue, also shown in Markdown and HTML.
Generated checks contribute only their qualifying fresh evidence. Expected
invalid inputs cannot earn positive request-condition coverage; valid passing
error responses can earn ordinary response-condition coverage. Failed or
unexecuted targets remain visible. Reports rebuild offline without hook code.

See the [HTTP callback protocol](runtime-api.md#exploration-inside-a-test) to
integrate other languages, and the [implementation plan](code-exploration-plan.md)
for the execution model and its boundaries.
