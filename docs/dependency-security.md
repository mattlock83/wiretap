# Dependency security review

Reviewed 24 October 2026 against GitHub Dependabot and the upstream advisories.
The repository returned **19 open alerts** at review time. The earlier twentieth
`sprintf-js` alert was already absent; no alert was dismissed to complete this
review. External package publication and remote alert-state changes are separate.

All affected dependency families were upgraded. Contextual limits below explain
reachability; they are not a reason to retain a vulnerable package version.

| Alert | Upstream advisory | Dependency | Resolution |
| --- | --- | --- | --- |
| 27 | [GHSA-74vm-87hj-r66f](https://github.com/getkin/kin-openapi/security/advisories/GHSA-74vm-87hj-r66f) | kin-openapi | Upgrade to 0.149.0; response-header validation fix included. |
| 26 | [GHSA-g8cf-rcjf-c8cf](https://github.com/getkin/kin-openapi/security/advisories/GHSA-g8cf-rcjf-c8cf) | kin-openapi | Upgrade to 0.149.0; circular composition recursion fix included. |
| 25 | [GHSA-rj39-cv6w-8wv7](https://github.com/getkin/kin-openapi/security/advisories/GHSA-rj39-cv6w-8wv7) | kin-openapi | Upgrade to 0.149.0; YAML non-finite-value fix included. |
| 24 | [GHSA-mh7x-f8wq-4jhx](https://github.com/getkin/kin-openapi/security/advisories/GHSA-mh7x-f8wq-4jhx) | kin-openapi | Upgrade to 0.149.0; recursive additionalProperties fix included. |
| 23 | [GHSA-fh4f-47mf-f2gj](https://github.com/getkin/kin-openapi/security/advisories/GHSA-fh4f-47mf-f2gj) | kin-openapi | Upgrade to 0.149.0; legacy-router method panic fix included. |
| 20 | [GHSA-8988-4f7v-96qf](https://github.com/open-telemetry/opentelemetry-js/security/advisories/GHSA-8988-4f7v-96qf) | example OpenTelemetry core | Upgrade the example's OTel family to patched 2.12 / 0.222 releases. SDK already uses core 2.11. |
| 19 | [GHSA-2v4p-qf9q-27wj](https://github.com/grpc/grpc-go/security/advisories/GHSA-2v4p-qf9q-27wj) | grpc-go | Upgrade to patched 1.83.2. Wiretap does not configure xDS servers. |
| 18 | [GHSA-qc2q-p7wx-3px3](https://github.com/grpc/grpc-go/security/advisories/GHSA-qc2q-p7wx-3px3) | grpc-go | Upgrade to 1.83.2. Wiretap does not configure xDS RBAC filters. |
| 17 | [GHSA-vp52-pcj8-j9qc](https://github.com/grpc/grpc-go/security/advisories/GHSA-vp52-pcj8-j9qc) | grpc-go | Upgrade to 1.83.2; HTTP/2 fragmentation fix included. |
| 16 | [GHSA-hrxh-6v49-42gf](https://github.com/grpc/grpc-go/security/advisories/GHSA-hrxh-6v49-42gf) | grpc-go | Upgrade to 1.83.2; xDS/HTTP2 fixes included. |
| 15 | [GO-2026-5028](https://pkg.go.dev/vuln/GO-2026-5028) | x/net | Upgrade to 0.60.0 (HTML parser fixed since 0.55.0). Wiretap imports HTTP/2, not the affected HTML parser. |
| 14 | [GHSA-p77j-4mvh-x3m3](https://github.com/grpc/grpc-go/security/advisories/GHSA-p77j-4mvh-x3m3) | grpc-go | Upgrade to 1.83.2; malformed method-path fix included. |
| 13 | [GHSA-8wmf-6v46-5gfg](https://github.com/open-telemetry/opentelemetry-go/security/advisories/GHSA-8wmf-6v46-5gfg) | OTel Go SDK | Upgrade to 1.45.0. Wiretap does not configure a telemetry exporter. |
| 12 | [GHSA-hfvc-g4fc-pqhx](https://github.com/open-telemetry/opentelemetry-go/security/advisories/GHSA-hfvc-g4fc-pqhx) | OTel Go SDK | Upgrade to 1.45.0; BSD kenv lookup fix included. |
| 11 | [GHSA-9h8m-3fm2-qjrq](https://github.com/open-telemetry/opentelemetry-go/security/advisories/GHSA-9h8m-3fm2-qjrq) | OTel Go SDK | Upgrade to 1.45.0; executable lookup fix included. |
| 10 | [GHSA-mmfr-pmjx-hw9w](https://github.com/getkin/kin-openapi/security/advisories/GHSA-mmfr-pmjx-hw9w) | kin-openapi | Upgrade to 0.149.0; multipart error-conversion fix included. |
| 9 | [GHSA-xhj3-7xw9-vr34](https://github.com/getkin/kin-openapi/security/advisories/GHSA-xhj3-7xw9-vr34) | kin-openapi | Upgrade to 0.149.0; deepObject decoding limits included. |
| 8 | [GHSA-jpcw-4wr7-c3vq](https://github.com/getkin/kin-openapi/security/advisories/GHSA-jpcw-4wr7-c3vq) | kin-openapi | Upgrade to 0.149.0; content-parameter schema fix included. |
| 7 | [GHSA-r277-6w6q-xmqw](https://github.com/getkin/kin-openapi/security/advisories/GHSA-r277-6w6q-xmqw) | kin-openapi | Upgrade to 0.149.0. Wiretap never uses ValidationHandler.Load to enforce authentication; it deliberately checks structural contracts while the tested provider authenticates requests. |

The Go minimum is now 1.26, required by the patched x/net dependency. Security
checks must also use a patched Go toolchain because dependencies alone cannot
fix standard-library vulnerabilities. Re-run `govulncheck ./...`, SDK/example
`npm audit`, Go tests and both JavaScript suites whenever these versions change.

The source scan additionally identified five HTTP/2 issues outside the original
Dependabot alert snapshot. Upgrading x/net to 0.60.0 fixed all five:

- [GO-2026-6603](https://pkg.go.dev/vuln/GO-2026-6603): trailer-header memory exhaustion.
- [GO-2026-6610](https://pkg.go.dev/vuln/GO-2026-6610): malformed framing-related headers.
- [GO-2026-6611](https://pkg.go.dev/vuln/GO-2026-6611): repeated initial-window-change CPU exhaustion.
- [GO-2026-6612](https://pkg.go.dev/vuln/GO-2026-6612): double stream flow-control refund.
- [GO-2026-6617](https://pkg.go.dev/vuln/GO-2026-6617): HPACK encoder race crash.

Verification on 24 October 2026: govulncheck 1.9.0 source/symbol scan with Go
1.26.10 completed with no findings; npm audit reported zero vulnerabilities for
both the JavaScript SDK and native Node gRPC example. These results describe the
committed dependency graphs, not a permanent vulnerability-free guarantee.

On 25 October 2026, the Postman compatibility fixture and smoke harnesses stopped
installing `postman-collection` 5.3.1, whose pinned Faker 5.5.3 and UUID 8.3.2
dependencies remained affected by
[GHSA-qxc2-j82w-r537](https://github.com/faker-js/faker/security/advisories/GHSA-qxc2-j82w-r537)
and [GHSA-w5hq-g745-h8pq](https://github.com/uuidjs/uuid/security/advisories/GHSA-w5hq-g745-h8pq).
They now use exact-pinned Ajv 8.20.0 with the official HTTP v2.1 and gRPC v3
schemas and independent captured-content assertions. A clean fixture install
and `npm audit --audit-level=low` reported zero vulnerabilities. The compatibility
workflow audits this fixture on each relevant change.
