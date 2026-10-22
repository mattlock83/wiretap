# Application contracts, package identity and spec variants

**Status:** proposed design, 2 October 2026. This supports Phase 1 and registry
increment R1 of the [roadmap](roadmap.md). Field names below are illustrative;
the current configuration accepts version 1 and does not implement this model.

## Decision

Make `.wiretap.yaml` declare the application's identity, the APIs it provides
and the APIs it consumes. Each contract can come from local files, a remote URL
or a registry. Keep these declarations separate from the runtime addresses used
by tests. A dependency's local alias must not change its upstream API identity.

Version specifications independently from application releases and test bundles.
Support a consumer-owned spec variant with its own version and an exact link to
the original. Codegen adjustments must not silently replace the provider's
contract or inherit its verification results.

## Provider-only, consumer-only and mixed projects

`provides` and `consumes` are independently optional: omission and an empty map
both mean no entries were declared on that side. Keep `app` metadata for every
role so a UI's dependency assumptions still have an owner. Missing declarations
alone do not prove the inventory is complete.

| Role | Declarations | Capture requirements |
| --- | --- | --- |
| Provider only | Nonempty `provides`; `consumes` omitted or empty. | Bind the provided API to `sut`. No dependency route or mock is required. |
| Consumer only | Nonempty `consumes`; `provides` omitted or empty. | Dependency routes and test lifecycle calls must work with `sut` omitted. Do not require a dummy inbound API, provider request, provider spec or provider URL. |
| Mixed | Both sides have entries. | Bind the relevant provided API and dependency targets while retaining separate boundary roles. |

A provider-only service gains spec conformance, operation and condition coverage
from its own tests. A consumer-only UI gains evidence of its API assumptions and
dependency coverage. Neither role requires a synthetic API or dependency.

The UI's existing test framework remains the owner of browser execution, setup,
assertions and outcomes. Wiretap must associate outbound API exchanges with
those named tests without an inbound SUT flow. Calls must actually traverse
Wiretap: mocks that intercept inside the browser or test process before network
traffic need a supported adapter or an explicit routing change. Merely declaring
their specs captures no exchanges.

A test framework's trace context does not automatically reach browser requests.
Preserve explicit standard trace propagation or use sequential/isolated workers
where attribution is otherwise ambiguous. This design must not require a special
client or Wiretap-specific application instrumentation.

Consumer-only bundles identify the UI/client as the application and its called
APIs as dependencies, with mock versus real-provider targets explicit. They do
not present backend APIs as APIs provided by the UI. Provider coverage on an
explicitly out-of-scope side is not applicable; unknown inventory remains unknown.
Do not report 100% coverage because that side has no declared operations.
Consumer coverage describes its exercised dependency usage; uncalled provider
operations are not automatically UI test obligations. Scope consumer verification
work to declared or observed usage and expectations, without claiming that mock
observations verify the provider.

These are requirements for the proposed configuration/runtime model, not a claim
that the current binary has a complete consumer-only UI capture workflow.
Separate application/run identity from provider API identity throughout the
config loader, lifecycle API, capture, coverage and reports. Remove assumptions
that every run requires a SUT boundary or should default to a synthetic `sut` API.

## Proposed configuration shape

This is a design example, **not a runnable configuration for today's binary**.
The proposed top-level `version: 2` identifies the config format only.

```yaml
version: 2

app:
  name: Checkout
  id: acme/checkout                 # Optional stable identity
  description: Places orders and collects payment.
  version: "1.8.0"                 # Optional application release label

provides:
  orders:                           # Local declaration key
    api: acme/checkout/orders        # Optional; can derive from app ID + key
    protocol: http
    spec:
      version: "2.1.0"
      source:
        file: specs/orders.openapi.yaml

  admin:
    protocol: http
    spec:
      version: "1.0.0"
      source:
        url: https://contracts.example.org/checkout/admin/1.0.0/openapi.yaml

consumes:
  payments:                         # Consumer's local alias/namespace
    api: acme/payments/public        # Canonical upstream API identity
    protocol: http
    spec:
      version: "3.2.0"
      source:
        url: https://contracts.example.org/payments/3.2.0/openapi.yaml
    variant:
      name: checkout-go
      version: "1"                  # Consumer-owned variant version
      purpose: codegen
      description: Operation names adjusted for our Go client generator.
      source:
        file: specs/payments.codegen.openapi.yaml
      derived_from:
        version: "3.2.0"            # API inherited; exact base pinned in lock

sut:
  contract: provides.orders
  url: ${SUT_BASE_URL}
  kind: service

dependencies:
  billing:
    contract: consumes.payments
    url: ${PAYMENTS_MOCK_URL}
    kind: mock
```

`app.name` is the human-readable application name. Require a short description
for publication; local capture can begin without one. `app.id` is optional and
need not be a UUID. Without it, derive a documented project-local identity from
the name, then qualify it with the publisher namespace on publication. Do not
silently match same-named applications across repositories. Preserve the resolved
identity explicitly when renaming or moving a published application.

Each `provides` entry describes one logical API and its spec root or roots. An
explicit `api` preserves identity when its local key changes; otherwise the
documented default uses the application identity and declaration key. Consumer
keys such as `payments` are local aliases: multiple aliases may refer to the
same upstream API, different pinned versions or different variants. Duplicate
aliases are errors; aliases and display names are not global identifiers.

Registry authority and verified publisher ownership qualify canonical identity.
An identical `acme/payments/public` string on an unrelated registry does not prove
the same owner. Mirrors require an explicit origin mapping or preserved trusted
provenance. A plain local file or HTTPS document cannot establish provider
ownership from its title or URL alone; resolve or declare the mapping, otherwise
leave cross-project identity unresolved.

Runtime references such as `contract: consumes.payments` bind a target to the
declaration without repeating identity and source details. The `billing` runtime
name remains free to suit the test harness. The current runtime has one SUT target;
declaring several provided specs does not create additional listeners or processes.
Unbound declarations such as `admin` remain visible as untested inventory. A
declaration-only publishing workflow can use resolved specs without starting a
SUT or running tests.

## Versions and identities

| Value | Meaning and rule |
| --- | --- |
| Config version | Syntax understood by the configuration loader. Unrelated to product or API releases. |
| Application identity and release | Which application owns the provided APIs, plus an optional friendly software version. Record the actual tested commit/build separately; never substitute a label for build provenance. |
| Logical API identity | Stable boundary owned by a provider. Independent of local aliases, filenames, download locations and test targets. |
| Spec version | Publisher-assigned version of that API description. Optional for local work; allow digest-only publication when no reliable label exists. Use it for discovery, not content equality or compatibility. |
| Source snapshot digest | Byte-exact identity of retained source documents and their import/reference closure. Changed retained bytes produce a different snapshot even if the root version label stays the same. |
| Contract revision | Normalized frozen contract under a named revision profile. Some representation changes, such as supported Protobuf comment/order changes, can retain the same revision. Equality is not universal compatibility proof. |
| Consumer variant identity/version | Consumer-owned derivative, scoped by owner, upstream API and variant name. Its version and digest are independent of upstream releases. |
| Evidence identity | A particular run/snapshot and its build, contracts, attempts and assertions. More test runs do not require new spec versions. |
| Distribution digest | Integrity of a packaged spec or capture artifact. Repacking does not create another independent observation. |

For OpenAPI, the source document's `info.version` can supply a default spec label;
retain it separately if an explicit publication label differs. The `openapi`
field selects the OpenAPI format, not an application version. See the
[OpenAPI version definitions](https://spec.openapis.org/oas/v3.1.1.html#info-object).
Protobuf has no universal application-release field; accept an explicit label or
a content revision without inventing one from a package name.

Prefer semantic versions when a publisher maintains that policy, but support
opaque upstream labels. Resolve exact references first; version-range updates can
be added later. Do not use a suffix such as `+codegen.1` as the sole identity of
a variant: SemVer ignores build metadata for precedence. Keep explicit variant
identity and digest. See [SemVer](https://semver.org/).

A published spec release reference binds its publisher, API and version to fixed
content. Conflicting reuse of that reference must fail or require a new release;
development runs can still record changed workspace content without inventing a
published release. Equal labels do not prove equal content, and unequal digests
do not by themselves prove a breaking change.

## Local, remote and registry sources

Use the same source model for `provides`, `consumes` and variants:

- Local spec files resolve relative to `.wiretap.yaml`.
- HTTPS URLs resolve to frozen content, with each OpenAPI reference resolved
  relative to its containing document.
- Registry references select a spec artifact or an identified frozen spec within
  an evidence bundle; resolve tags to immutable digests.
- Protobuf sources retain explicit files/import roots or descriptor sets and
  selected services. Remote packages must contain or resolve the complete import
  closure; a URL to one `.proto` file is not necessarily a complete contract.

Preserve the explicit HTTP missing-spec exception and its unknown coverage
denominator. Omitting a source must not silently opt out; a broken declared
source is an error. gRPC continues to require a resolved Protobuf contract.

Keep credentials outside config publication, lock data and bundles. Fetch only
the permitted, bounded reference closure; a spec's `servers` entries are not
permission to send test requests there. Runtime targets remain explicit.

Add a machine-maintained `.wiretap.lock.yaml` for resolved external/consumed
references and variant bases. Record requested sources, canonical identities,
resolved labels, exact content/package digests, the reference/import closure and
revision-profile versions. Users supply paths and labels; tools calculate hashes.
Provide an explicit resolve/update workflow, with reviewable lock changes.

Normal capture must not silently refresh remote dependencies. Locked CI fails
on missing inputs or drift, rather than adopting a newer contract. Local provider
specs are captured from current workspace bytes and receive their actual new
revision when changed; local consumed specs and variant changes must also surface
lock drift. Freeze a complete snapshot for each run. Reports and comparisons
over complete bundles require no remote fetch, including after a registry is
unavailable.

Preserve reflection as the default acquisition method for gRPC runtime bindings
without another source. Freeze selected services and descriptors once per run,
and record acquisition provenance. Reflection is not a publisher version or proof
of ownership; reflection from a mock describes that mock. Where a pinned provider
spec and reflected descriptors are both available, compare them explicitly.
Standalone publication from a reflection-only declaration needs a retained
snapshot or a discovery step against an available target. Without either, the
contract remains unresolved; it is not a published resolved spec.

## Consumer variants and code-generation changes

Initially accept a complete modified spec, a description and one `derived_from`
link. The declaration can omit manually written hashes, but resolution must pin
the exact original revision as well as the derivative. In the example, upstream
API identity is inherited and `derived_from.version` selects the original spec
release. Preserve its resolved base in the lock; changing `spec.version` later
must not automatically rebase the variant or rewrite its history. Rebase is an
explicit update after reviewing or regenerating the modified spec.
If the base differs from the selected upstream version and is not already
locked, require an explicit original source/reference under `derived_from` or
leave resolution incomplete. A version label cannot locate arbitrary original
bytes; never guess by rewriting a URL or using the current provider document.

The enclosing `spec` remains the provider contract. A `purpose: codegen` variant
is an additional consumer input; it does not automatically change Wiretap's
enforcement contract. A variant intended to change consumer contract expectations
must declare that intent separately and be evaluated alongside upstream. Record
which spec each validation or comparison used. Keep both source snapshots and
their findings; a pass against a relaxed derivative cannot conceal an upstream
violation.

Classify supported differences as contract-preserving, contract-changing or
unknown, with the comparison profile recorded. Generator package options and
language namespaces can preserve the wire interface, but Protobuf package,
service and method changes can alter RPC identity. OpenAPI operation/component
renames can need explicit mappings; changed required fields, nullable rules or
serialization can change accepted messages. A `codegen` label is a declaration
of purpose, not proof that a change is harmless.

The graph should show both “consumes the Payments API” and “uses Checkout's
variant derived from this Payments revision.” No copying provider coverage or
verification status onto the derivative based solely on lineage. Establish
supported equivalence or run the relevant checks; preserve unknown scope.
Publish derivatives under their consumer owner, without implying provider
endorsement.

Do not add a transformation DSL or run code generators while resolving, viewing
or pulling a spec. Existing builds can produce the modified file and optionally
record their transformation provenance. The initial feature is identity,
versioning and evidence linkage.

## Registry publication

Allow standalone spec artifacts before provider tests exist; classify them as
declared contracts without test evidence. Later capture artifacts reference their
provided and consumed spec revisions and retain complete frozen copies for
offline use. Storage can deduplicate these bytes without making a copied bundle
depend on a live registry.

One application release can link multiple APIs/spec releases and many evidence
bundles. One unchanged spec release can be tested by several application builds.
Selecting a spec version does not select a provider build or inherit all its
verification results. Keep the declared, observed and verified graph edges
separate. Declared but untested APIs remain in the inventory; an explicit scope
declaration is still needed to claim this covers every API the application owns.

## Implementation and acceptance

Implement declarations and identity in Phase 1, source resolution/locking and
package mapping in R1, and variant lineage before offering codegen integrations.
Update user-facing configuration docs only when syntax is implemented. Keep the
strict parser and report unsupported sources explicitly; a clean config revision
can replace the old shape without building an indefinite compatibility layer.

The acceptance example must cover:

1. One application with a name, description, optional stable ID and multiple
   provided specs, including an unbound API that remains visibly untested.
2. Local and remote sources on both provided and consumed sides, plus a registry
   source and an imported schema that changes while its root label does not.
   Distinguish a byte-only change from one that changes the contract revision.
3. A consumer alias distinct from provider identity and runtime target name;
   changing an alias does not change an explicitly resolved canonical API.
4. Independent application, spec, variant and run versions; repeated captures
   do not require a manual bundle-version increment.
5. A codegen variant with a supported harmless change and another with a real
   wire/validation change. Neither gains provider verification from lineage alone.
6. A pinned upstream update that leaves the old variant base intact until an
   explicit rebase; mismatched or unavailable bases remain unresolved.
7. Locked CI, offline reconstruction, authenticated remote sources without
   credential export, and gRPC reflection frozen with accurate provenance.
8. No false identity matches from equal names, URLs or version labels, and no
   registry account needed for local declaration, capture and comparison.
9. A provider-only service captured without dependency routes or mocks, producing
   provider conformance/coverage evidence and supported rejection checks.
10. A consumer-only UI test captured with no `sut` binding or inbound flow,
    producing dependency evidence, correct test attribution and a readable bundle.
    Backend operations must not appear as provided UI APIs. Preserve mock/service
    origin and demonstrate isolated or traced attribution for concurrent tests.
    Use mocks reached through Wiretap, and explain the routing/adapter requirement
    for browser-local interception; do not credit traffic that was never captured.
11. Omitted and empty role maps, incomplete inventory and declarations without
    traffic. No phantom APIs, invented denominators or full-coverage claims for
    an absent side. Later variation tests reuse the UI's existing assertions.
