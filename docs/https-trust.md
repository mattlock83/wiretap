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

3. Restart any test processes to pick up trust settings.

## Linux (Debian/Ubuntu)
1. Copy the CA certificate into the system trust store:

```sh
sudo cp ~/.cache/wiretap/ca/ca-cert.pem /usr/local/share/ca-certificates/wiretap-ca.crt
sudo update-ca-certificates
```

2. Restart any test processes to pick up trust settings.

## CI Environments
- Mount the CA certificate into the container/runner.
- Append it to the system trust store as above.
- If you cannot modify trust settings, run the proxy in `passthrough` mode (metadata-only capture).

## Proxy Mode
- `WIRETAP_PROXY_MODE=mitm` enables TLS interception (default).
- `WIRETAP_PROXY_MODE=passthrough` disables decryption and captures metadata only.
