# Bring Your Own Mock Server

Phrond can capture and correlate dependency traffic without owning the mock
implementation. Use this for WireMock, MockServer, Hoverfly, a bespoke fake, or
any HTTP(S) server.

For a complete mixed built-in/external setup, run
`./examples/run-complex-rest-e2e.sh` and inspect
`examples/complex-rest/test-helpers.js`.

## Configure an external dependency

Start and configure the external mock as usual, then pass its base URL when the
Phrond test session is created:

```js
const runtime = await startServer({
  baseUrl: "http://127.0.0.1:18000",
  dependencies: [
    {
      assetId: "inventory-service",
      assetName: "Inventory",
      mock: {
        mode: "external",
        upstreamUrl: wireMockBaseUrl
      }
    },
    {
      assetId: "pricing-service"
      // No mock block: this dependency uses Phrond's built-in mock.
    }
  ]
});

await runtime.setupTest({ name: "reserves inventory" });

// Configure the SUT with this URL, not wireMockBaseUrl.
const inventoryUrl = runtime.dependencyUrl("inventory-service");
```

The SUT calls `inventoryUrl`. Phrond removes its internal session/dependency
prefix, forwards the original application path and query to `wireMockBaseUrl`,
and records the request and response as `channel: sut_to_mock` with
`asset_id: inventory-service` and `mock_mode: external`.

The optional `preserveHost: true` forwards the SUT's original `Host` header.
The default rewrites `Host` to the external upstream. External redirects are
returned to the SUT and are not followed by Phrond.

## WireMock with Spring Boot

WireMock's official Spring Boot integration starts WireMock and keeps ownership
of stub registration, reset, scenarios, and verification:

```java
@SpringBootTest
@EnableWireMock({
  @ConfigureWireMock(
    name = "inventory",
    baseUrlProperties = "wiremock.inventory.url")
})
class InventoryIntegrationTest {
  @InjectWireMock("inventory")
  WireMockServer inventory;

  @BeforeEach
  void configureInventory() {
    inventory.stubFor(
      get(urlEqualTo("/inventory/SKU-1"))
        .willReturn(okJson("{\"available\":true}")));
  }

  @Test
  void reservesInventory() {
    // Call the SUT. Its inventory base URL is Phrond's returned
    // dependency URL, not wiremock.inventory.url.
  }
}
```

Use the injected `wiremock.inventory.url` value as the dependency's
`mock.upstream_url` when creating the Phrond session. Configure the SUT's
inventory property with the corresponding Phrond `dependency_url` before the
Spring application starts. In suites that build the Spring context first, do
this in a JUnit extension or Spring context initializer.

See the
[official WireMock Spring Boot integration documentation](https://wiremock.org/docs/spring-boot/)
for current dependency and annotation details.

## Artifact semantics

An external exchange retains normal Phrond correlation and fidelity:

- `channel: sut_to_mock`, routed `asset_id`, trace and parent flow IDs,
  fingerprint, timing, request/response bodies, headers, and trailers;
- `mock_mode: external`;
- `transport_error` plus a deterministic recorded `502` when the upstream
  connection or TLS handshake fails;
- no `mock_stub`, because Phrond cannot infer the external provider's match
  rule or response template;
- no `unmatched` flag, because unmatched behavior is provider-specific.

`queueResponse` is intentionally rejected for an external dependency. Continue
using the external mock's API for stub setup and verification. Phrond does not
manage the external server's lifecycle or state. The generic gateway also
cannot populate `consumed_fields`; that would require a future
provider-specific admin API adapter.
