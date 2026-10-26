# Jest lifecycle conformance

[scenarios.json](scenarios.json) defines the lifecycle and ownership behavior
used by the Jest integration and its internal runtime owner. Jest supplies final
outcomes and fixture hooks; these vectors verify ownership and capture cleanup.

Use the scenarios whose `applies_to` contains the component under test. Invalid
receipt vectors overlay their `set` object onto an otherwise valid receipt.
Platform-specific tests additionally cover missing bundle directories, malformed
JSON, readiness files from another process, and deadlines. The Jest SDK
managed-owner tests consume these vectors directly. See the
[Jest guide](../js/docs/jest.md) for integration and lifecycle details.

Owners start a local executable through a private readiness directory and a
stdin lifeline. Readiness must identify the owned child and an HTTP loopback
control URL with an explicit valid port. URL credentials, query, fragments and
paths other than `/` are invalid. Capture endpoints may also use HTTPS.
Validate the initial run identity before trusting a receipt destination. A
successful finalization must name the same run and archive and say `complete`.
An HTTP export error may retain available output paths, but remains a failure.

Each run owns `<bundle_path>/last-run.json` and a bounded `wiretap.log` in that
same directory. A shared output-root `last-run.json` may remain for compatibility
as an advisory latest snapshot; it can race with other owners and must never
replace the archive-local record. A forced process kill or unsuccessful child
exit fails cleanup even if an export receipt was already returned.

Adapters preserve the runner's original test failure and report secondary
capture cleanup failures. Static skips that never start an attempt need no
capture record. Dynamic skips of an active attempt record `skip`; a timeout or
fixture failure records `fail`. Negative contract-validation evidence alone
does not change a passing framework outcome. Identities remain stable across
retries, with increasing attempt ordinals and a fresh trace per attempt.

The optional capabilities probe describes API `1.0`. The Jest integration
uses the established lifecycle endpoints. All conformance commands prepare
and test local artifacts; none publish packages.
