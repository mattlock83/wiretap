# Roadmap

Proposed work here is not shipped functionality or a delivery commitment. This experimental branch focuses on Jest, HTTP/HTTPS capture, OpenAPI coverage and Arazzo-backed assumption checks. See [support](support.md) for current behavior.

## Verification and distribution

- Extend independent Arazzo runner/import acceptance beyond schema and archive-content verification. Wiretap itself does not execute workflows.
- Choose a public website URL and add canonical metadata when hosting is configured.
- Registry publication, release tags, public binary releases and image uploads remain on hold until requested.

## Coverage and comparison candidates

- Extend supported contract semantics, including OpenAPI 3.1, format predicates, recursive schemas and conditional keywords. Define finite targets and explicit limits before claiming coverage.
- Explore cross-revision condition mapping while retaining distinct contract/rule identities.
- Explore expected-negative annotations and evidence that the test asserted a rejection. A status code or name alone cannot establish this.
- Consider explicit pairwise combination targets with a declared policy and denominator.
- Improve Jest fixture/concurrency diagnostics where a concrete integration need appears.
- Consider a small offline viewer if text, JSON, Markdown and CI reports leave an inspection gap.

Test generation, dependency simulation, hosted registries and managed test execution remain outside scope. The existing Jest runner still owns test execution and assertions.
