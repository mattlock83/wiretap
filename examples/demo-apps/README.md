# Demo apps

`basic-rest/sut` is the independently managed SUT used by the capture example.
`basic-rest/dependency` stands in for a separately owned dependency server such
as WireMock. The example runner owns both processes and points the SUT's
dependency URL through Wiretap; Wiretap owns neither lifecycle.
