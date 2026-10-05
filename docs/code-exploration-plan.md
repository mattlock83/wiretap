# Exploration inside existing tests

Implemented 2026-10-06. This replaces standalone CLI exploration and
project YAML hook commands with an optional function called by the user's test.

## User contract

The test sets up its normal fixtures/mocks, makes and asserts a happy-path call,
then awaits SDK exploration. The HTTP runtime snapshots completed provider
exchanges in that active test. A previous bundle or completed parent test is not
required. Generated attempts and the authored parent are saved in one run.

Generation supports HTTP/OpenAPI 3.0 and native gRPC with supported declared
Protovalidate (`buf.validate`) rules. Plain protobuf types do not imply business
validation constraints. No guessed business constraints, arbitrary gRPC
error-code denominator or automatic mock generation.

## Execution and hooks

The binary owns planning, mutation, request/response validation, network
execution, capture ownership, cancellation and evidence. Go and JavaScript SDKs
wrap the HTTP API and dispatch local callbacks. Users keep their framework's
assertions, fixtures, database connections, JUnit reporter and CI lifecycle.

Hooks are beforeExplore, beforeCase, beforeRequest, checkCase, afterCase and
afterExplore. Case hooks run for the fresh baseline and every variation.
Callbacks can return bindings, headers, state and named checks. Explicit check
hooks must return checks or throw; an empty callback is not an assertion. Request
hooks see the final mutated request; changing authentication must not repair the
intended violation. Cleanup runs after failures and cancellation with bounded
timeouts. Hook context/code/credentials are not executable bundle content.

Exploration and hooks can select exact `METHOD /spec/path` operations or native
gRPC `/package.Service/Method` procedures. Selection
filters mutation targets, retaining prerequisite sequence calls. Request-hook
selection uses that request's actual resolved operation; case-hook selection
uses the target operation. Multiple scoped callbacks run in declaration order.
Unknown selectors fail before hooks or traffic. Optional flow selection resolves
multiple captured examples explicitly.

All supported generated cases run by default. An explicit maxCases is allowed;
truncation remains visible as incomplete execution. Enumeration has bounded
resource limits which fail explicitly, never silently report exhaustive coverage.
No Cartesian enumeration of every possible payload or future adaptive search.

## HTTP interface

All endpoints use POST and snake_case JSON fields:

- `/v1/explore`: session_id, operations, optional flow_id, budgets, binding and
  extraction rules, expected_statuses (HTTP), expected_grpc_statuses, redactions,
  and hook descriptors
  `{id,event,operations?}`. Returns exploration_id and state.
- `/v1/explore/next`: session_id and exploration_id. Bounded polling returns
  state, an optional `{id,hook_id,event,input}` action, and terminal results.
- `/v1/explore/respond`: matching IDs and action_id plus output or error.
  Identical retries are idempotent; conflicting/late responses cannot resume a
  different action. The server never executes a supplied command.
- `/v1/explore/cancel`: cancels work; clients keep dispatching cleanup actions
  until terminal state. Parent teardown and runtime shutdown also cancel work.

Case callbacks receive fresh W3C context; invocation callbacks receive their
authored parent's context. SDKs do not install a new
test framework or require a callback web server. Hook waits, input/output sizes
and retained job state are bounded; disconnected clients cannot leave a run
permanently blocked. Control requests remain responsive while work runs.

## One bundle and isolated cases

The parent session is paused for exploration admission. Each generated case has
a fresh identity and trace/session ownership, sharing the parent's configured
upstreams and frozen contracts. Serial fallback is transferred only when
unambiguous. Parallel tests require ordinary trace propagation and independent
fixtures as before. Closed traces remain reserved; late traffic cannot enter
another case. Existing session-specific dependency URLs must resolve safely for
the active child or be rejected explicitly.

One component owns each provider exchange: the explorer sends/records it once.
Dependency traffic is captured under the current generated case. Fixture hook
calls and their dependencies are routed under that case but excluded from
test evidence and coverage. Global fixture calls are also excluded from the
parent's authored capture. Runtime drain completes
before evidence is committed. Expected invalid provider requests are checked by
the rejection oracle, not failed again by ordinary positive-input validation.
Dependency requests/responses retain normal contract validation.

Every exploration invocation namespaces its attempt IDs. Semantic target keys
remain stable across invocations. Per-attempt provenance records the parent run,
attempt, selected flows and exploration ID; it never claims a hash of a parent
attempt that has not been committed. Runtime owns bundle finalization. A failed
parent does not erase independently checked child evidence. Reports distinguish
authored, baseline and rejection attempts and aggregate shared target keys using
each generated attempt's own flow reference.

## Native gRPC execution

The binary resolves validation rules from the same frozen descriptor snapshot
used for capture, discovered through reflection or supplied locally. It checks
every seed against the official Protovalidate validator and admits a generated
case only when its request isolates the intended field/rule violation. Unsupported
rules are reported explicitly; a contract without supported witnesses fails
planning before hooks or network execution. This is a finite supported catalogue,
not exhaustive coverage of arbitrary CEL or application business rules.

Unary, server streaming, client streaming and bidirectional streaming use the
existing native transport and capture pipeline, including upstream TLS. Seeds
must be complete bounded conversations. Baselines preserve observed
receive-before-send ordering. Rejections replay the valid prefix through the
mutated message, then half-close; they do not send the later happy-path messages.
Request deadlines and cancellation bound blocked streams.

Hooks receive procedure, metadata, ProtoJSON messages, exact protobuf bytes and
response gates. Bindings can target one message or all messages; response
extraction can select a message, metadata or trailers. Hook-supplied headers
provide fresh authentication metadata. Native rejection credit defaults to
INVALID_ARGUMENT (3), optionally OUT_OF_RANGE (11). Authentication failures,
transport failures and incomplete observations never earn validation coverage.

The bundle layout and evidence version remain 3.1. Generated gRPC attempts add
the required `grpc-exploration.v1` capability and target protocol/message-index
metadata. This prevents readers that only understand HTTP rejection statuses
from silently interpreting native codes. Stable constraint keys are shared across
message witnesses; attempts retain their individual message locations and status
expectations. Generated invalid inputs cannot increase positive input coverage.

## Delivery and checks

1. Extract an in-run executor with immutable seed inputs, scoped hooks and runtime
   capture adapters. Remove default case cap and detect generation exhaustion.
2. Add runtime action handoff, ownership, cancellation, teardown/shutdown handling
   and per-case dependency capture. Reject invalid scope before execution.
3. Add thin Go/JavaScript APIs, local callback dispatch, cancellation and tests.
4. Remove CLI/YAML exploration, migrate examples, update API/user/bundle docs.
5. Verify baseline and rejection outcomes, scoped hooks, repeated invocations,
   fresh auth/fixtures, dependency ownership, late traffic, callback failure,
   timeout/cancellation cleanup, same-bundle coverage and offline rebuilding.
6. Run full Go race tests, SDK suites and the migrated examples. No commit/push
   unless requested.

## Verification

- Full Go regression suite passed with the race detector, including gRPC.
- Runtime integration tests cover one open bundle, repeated invocations,
  invocation and case hooks, fixture traffic exclusion, dependency causality,
  parallel traced parents, cancellation, hook failures, shutdown and cleanup.
- Engine tests cover operation/flow selection, hook scoping, immutable inputs,
  explicit budgets, more than 100 generated cases by default, trace ownership
  and stopping on incomplete capture. SDK tests cover callback dispatch,
  expired actions, cancellation races and framework teardown.
- Node and SQLite examples execute authored tests and rejection cases in one
  bundle. The existing Jest parallel example passes. Microsvc example migration
  is syntax-checked but its Docker services were unavailable for execution.
- Native gRPC runtime tests cover all four RPC shapes, reflection with TLS,
  local descriptors without reflection, fresh authentication, HTTP dependencies,
  blocked-call cancellation/deadlines, cleanup and offline report rebuilding.
  The gRPC demo passes all five generated cases with either contract source.
- Protovalidate generator tests cover supported rule families, exact large
  integer bounds, nested fields, repeated-message witnesses, overlapping CEL,
  plain protobuf without annotations and bounded collection growth after binding.

Limits remain explicit: generated cases require HTTP/OpenAPI 3.0 or supported
declared Protovalidate rules in a complete native gRPC seed; fixtures must be
isolated and callbacks must observe cancellation. A killed
client cannot run local cleanup. If the initial start response is lost after
server registration, the client has no invocation ID to resume polling; the
runtime's callback timeouts bound the wait. Idempotent registration can be added
if remote control-plane recovery becomes a requirement.
