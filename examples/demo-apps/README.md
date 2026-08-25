# Demo apps

`basic-rest/sut` is the independently managed SUT used by the capture example.
`basic-rest/dependency` stands in for a separately owned dependency server such
as WireMock. The example runner owns both processes and points the SUT's
dependency URL through Phrond; Phrond owns neither lifecycle.

`pact-orders` and `pact-inventory` are two separate consumer/provider services
used by the real Pact Broker example. Orders calls inventory through Phrond's
capture proxy. Inventory additionally exposes its own standard Pact
provider-state setup endpoint so the official Pact verifier can exercise both
available and unavailable product scenarios. The example runner, not Phrond,
owns both service processes and the disposable local Broker.
