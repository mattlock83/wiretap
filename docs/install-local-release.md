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
dependency. Configure your runner using the [JavaScript SDK guide](../sdk/js/README.md).

## Install the Go SDK

Use Go 1.26+ and retain the source checkout used to prepare the runtime. If a
matching source archive was supplied, verify it with the other checksums and
extract it instead. From your test project, select the source as a local module
replacement:

```sh
go mod edit -require=github.com/mattlock83/wiretap@v0.1.0-local
go mod edit -replace=github.com/mattlock83/wiretap@v0.1.0-local=/absolute/path/to/wiretap-source
go mod tidy
```

Add imports of `github.com/mattlock83/wiretap/sdk/go` before running `go mod tidy`.
The local replacement supplies Wiretap even though the version is not published.
It uses the source directory's current contents, so retain the exact prepared
source and configure the same replacement in CI. Other module dependencies may
still be downloaded. See the [Go SDK guide](../sdk/go/README.md) for the two test hooks.

## Python and pytest

The Python SDK is a separate local package with no runtime download. From this
checkout, install it into the test project's virtual environment:

```sh
python -m pip install ./sdk/python
# Include the optional pytest integration dependency when needed:
python -m pip install "./sdk/python[pytest]"
```

Set `WIRETAP_BINARY` to the extracted local runtime or place it on `PATH`. Use
the [Python guide](../sdk/python/README.md) and
[pytest example](../examples/pytest-suite/README.md) for fixture setup. These
commands install a local project; they do not upload a package to a registry.
