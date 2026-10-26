# Install a prepared local release

Use the binary archives and SDK tarball produced by
[local release preparation](local-release.md). The examples use runtime version
`v0.1.0-local` and JavaScript SDK version `0.1.0`. These are local artifacts;
installation does not require a published Wiretap package or a public release.

## Verify and install the runtime

Keep `SHA256SUMS` beside the archives. From that directory, verify the files on
Linux with `sha256sum -c SHA256SUMS`, or on macOS with
`shasum -a 256 -c SHA256SUMS`. All listed files must be present and pass before
installation. A checksum detects file changes; obtain the checksum file and
archives from the same trusted source.

On Windows, verify all entries in PowerShell:

```powershell
Get-Content SHA256SUMS | ForEach-Object {
    $expected, $name = $_ -split '  ', 2
    $actual = (Get-FileHash -Algorithm SHA256 -LiteralPath $name).Hash.ToLowerInvariant()
    if ($actual -ne $expected) { throw "Checksum mismatch: $name" }
}
```

Choose the archive matching the machine's operating system and processor:

| System | Archive suffix |
| --- | --- |
| Linux x86-64 | `linux_amd64.tar.gz` |
| Linux ARM64 | `linux_arm64.tar.gz` |
| macOS Intel | `darwin_amd64.tar.gz` |
| macOS Apple silicon | `darwin_arm64.tar.gz` |
| Windows x86-64 | `windows_amd64.zip` |
| Windows ARM64 | `windows_arm64.zip` |

For example, extract the macOS Apple silicon archive into a new directory:

```sh
mkdir wiretap-v0.1.0-local
tar -xzf wiretap_v0.1.0-local_darwin_arm64.tar.gz -C wiretap-v0.1.0-local
./wiretap-v0.1.0-local/wiretap version --json
```

For Windows x86-64:

```powershell
Expand-Archive wiretap_v0.1.0-local_windows_amd64.zip wiretap-v0.1.0-local
.\wiretap-v0.1.0-local\wiretap.exe version --json
```

Check that the reported version and full commit match the prepared build. A
`dirty: true` value identifies a build containing local source changes. Put the
extracted executable on `PATH`, or set `WIRETAP_BINARY` to its absolute path in
your test environment. The SDK does not download or build the executable.

## Install the JavaScript SDK

Use Node.js 22+ and install the versioned tarball from your existing test project:

```sh
npm install --save-dev /absolute/path/to/release/wiretap-sdk-0.1.0.tgz
```

Commit the resulting package manifest and lockfile to keep the selected SDK
version and package integrity. npm may download the SDK's dependencies. Keep the
tarball at a stable path accessible to developers and CI when using a local file
dependency. Configure Jest using the [Jest SDK guide](../sdk/js/README.md).
