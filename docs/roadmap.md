# Roadmap

This is the only place for proposed work and deferred release steps. These items
are not shipped features or delivery commitments. See the
[support matrix](support.md) and [documentation index](README.md) for what exists.

Keep changes focused on capturing traffic from existing tests, measuring supplied
contracts, and checking mock accuracy and consumer compatibility using evidence
embedded in executable Postman collections. Prefer small adapters and offline
tools that preserve the original evidence.

## Verification and distribution

- Complete manual Postman desktop acceptance for HTTP and native gRPC collection
  import and execution, including descriptor attachment and streaming behavior.
  Automated schema/content checks do not establish desktop execution behavior.
- Choose the public website URL and add canonical and Open Graph URL metadata
  when hosting is configured.
- Package registry publication, release tags, public binary releases and image
  uploads remain on hold until explicitly requested. This includes npm, PyPI and
  Go module release distribution.

## Deferred integrations

- Java/JUnit SDK, explicitly deferred from the Python integration work.
- Other small runner adapters, such as Vitest or .NET test integrations, only
  where existing lifecycle hooks can supply reliable test ownership and outcomes.
- Connect wire protocol, gRPC-Web and mutual TLS, subject to concrete capture
  requirements and protocol-specific evidence tests.

## Coverage and comparison candidates

- Extend supported contract semantics, including OpenAPI 3.1 and condition
  targets for `const`, `if`/`then`/`else`, format predicates, recursive schemas and
  other unsupported keywords. Define finite targets and explicit measurement
  limits before claiming coverage.
- Explore cross-revision condition mapping. Preserve distinct contract and rule
  identities; a changed schema must not silently inherit coverage.
- Explore explicit expected-negative annotations and evidence that the test
  asserted the rejection. A status code, validation error or test name alone
  cannot establish that assurance.
- Consider selected or pairwise combination targets with an explicit policy and
  denominator. Observed conditions in the same message alone do not establish
  coverage of all combinations.
- Consider a small offline HTML report viewer only if existing text, JSON,
  Markdown and CI reports leave a concrete inspection need.

Test generation, dependency simulation, hosted registries and managed test
execution remain outside the product scope.
