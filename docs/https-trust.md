# HTTPS Trust Setup

The proxy can run in MITM mode to capture HTTPS traffic. To allow TLS interception in test environments, your system or test runner must trust the Wiretap proxy CA certificate.

Use `wiretap ca > wiretap-ca.pem` to export the public certificate for the current
configuration. The command creates the local CA if needed and never prints its
private key.

## Default CA Location
The CA is stored under the system cache directory:
- macOS: `~/Library/Caches/wiretap/ca/ca-cert.pem`
- Linux: `~/.cache/wiretap/ca/ca-cert.pem`

You can override this location with:
- `WIRETAP_PROXY_CA_DIR=/custom/path`

## macOS (Keychain)
1. Locate the CA certificate (see default paths above).
2. Run:

```sh
sudo security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain \
  ~/Library/Caches/wiretap/ca/ca-cert.pem
```

3. Restart clients that use system trust. Node.js test runners also need the
   [Node.js configuration below](#nodejs-test-runners).

## Linux (Debian/Ubuntu)
1. Copy the CA certificate into the system trust store:

```sh
sudo cp ~/.cache/wiretap/ca/ca-cert.pem /usr/local/share/ca-certificates/wiretap-ca.crt
sudo update-ca-certificates
```

2. Restart clients that use system trust. Node.js test runners also need the
   [Node.js configuration below](#nodejs-test-runners).

## Node.js Test Runners

Node.js uses its bundled CA certificates by default. Export Wiretap's public CA
using the same configuration and `WIRETAP_PROXY_CA_DIR` as the capture runtime,
then set `NODE_EXTRA_CA_CERTS` before launching your test command:

```sh
wiretap ca > wiretap-ca.pem
NODE_EXTRA_CA_CERTS="$PWD/wiretap-ca.pem" npm test
```

In PowerShell:

```powershell
wiretap ca | Set-Content -Encoding ascii wiretap-ca.pem
$env:NODE_EXTRA_CA_CERTS = (Resolve-Path ./wiretap-ca.pem).Path
npm test
```

Use your normal Jest test command in place of
`npm test`. Node reads this setting when the process starts, so setting it inside
a test or setup hook does not update that process's trust. A client that supplies
an explicit `ca` option must include Wiretap's CA in that option instead.
See [Node's CA configuration](https://nodejs.org/api/cli.html#node_extra_ca_certsfile).
Browser clients use their own trust settings.

## CI Environments
- Mount the CA certificate into the container/runner.
- For Node.js tests, set `NODE_EXTRA_CA_CERTS` to the mounted PEM file before
  launching the runner; this also works without modifying system trust.
- For clients that use system trust, append the CA to that store as above.
- If your client cannot trust the CA, run the proxy in `passthrough` mode
  (metadata-only capture).

## Proxy Mode
- `WIRETAP_PROXY_MODE=mitm` enables TLS interception (default).
- `WIRETAP_PROXY_MODE=passthrough` disables decryption and captures metadata only.
