# Local installation and release preparation

Wiretap uses the Go module `github.com/mattlock83/wiretap`. Build tools require
Go 1.26+; release preparation and CI use Go 1.26.10. The JavaScript SDK requires
Node 22+; the CI matrix covers Node 22 and 24 on Linux, macOS and Windows.

All commands below create local files or local images. They do not publish npm
packages, container images, GitHub releases or binary downloads.

Build or install prepared artifacts using this guide. Changes intended for
the next release are listed in the [release notes](../CHANGELOG.md).

## Build and inspect the runtime

From a checkout:

```sh
go build -mod=readonly -trimpath -o ./dist/wiretap ./cmd/wiretap
./dist/wiretap version --json
```

On Windows, name the output `./dist/wiretap.exe`. `wiretap --version` is an alias
of `wiretap version`; `--json` prints the same producer identity recorded with
artifacts. Development builds use `dev`; versioned local builds inject the
version and commit with Go linker flags.

## Prepare versioned binary archives

```sh
go run ./scripts/release --version v0.1.0-local --out dist/v0.1.0-local
```

This builds Linux, macOS and Windows binaries for amd64 and arm64 under
`dist/v0.1.0-local/bin/`, then creates `.tar.gz` archives for Linux/macOS, `.zip`
archives for Windows and `dist/v0.1.0-local/SHA256SUMS`. Each archive contains only
its binary and the MIT license. Use `--platforms host` for the current platform, or a comma-separated
list such as `--platforms linux/amd64,darwin/arm64`. `--out` selects another local
output directory.

The build uses `CGO_ENABLED=0`, `-trimpath`, `-buildvcs=false`, an empty Go build ID,
and explicit version/commit linker values. `git status --porcelain` supplies the
producer's dirty flag, including untracked source files; a local modified checkout
is not labelled as a clean build of its HEAD commit. Archive ordering, permissions,
owners and timestamps are fixed. `SOURCE_DATE_EPOCH` chooses the timestamp;
otherwise it comes from the selected Git commit. ZIP dates must be in
1980–2107. Repeat with the same Go toolchain, source, version, commit, dependency
graph, working-tree dirty state, targets and epoch to reproduce the archive checksums. Existing unrelated
files in the output directory are not included in `SHA256SUMS`.

`--commit` accepts a full or abbreviated Git commit hash and resolves it to the
full hash of the current `HEAD`. Nonexistent hashes and commits other than `HEAD`
are rejected, including when `SOURCE_DATE_EPOCH` is supplied. Check out a different
commit before preparing a build from it.

## Pack the SDK and prove a clean-project install

```sh
npm ci --prefix sdk/js
mkdir -p dist/v0.1.0-local
npm pack ./sdk/js --ignore-scripts --pack-destination ./dist/v0.1.0-local
node scripts/runner-smoke.mjs
```

The package exports the Jest adapter (`@wiretap/sdk` and `@wiretap/sdk/jest`), Jest environment and tracing helpers. It includes TypeScript declarations and the MIT license. Tests, fixture dependencies and lockfiles are excluded from the package.

After the final binary build, append the SDK tarball's checksum to the same
manifest. From `dist/v0.1.0-local`, use `shasum -a 256 wiretap-sdk-0.1.0.tgz >> SHA256SUMS`
on macOS, or `sha256sum wiretap-sdk-0.1.0.tgz >> SHA256SUMS` on Linux. In PowerShell:

```powershell
$name = 'wiretap-sdk-0.1.0.tgz'
$hash = (Get-FileHash -Algorithm SHA256 -LiteralPath $name).Hash.ToLowerInvariant()
Add-Content -Encoding ascii SHA256SUMS "$hash  $name"
```

Use the filename printed by `npm pack` if the SDK version changes. A subsequent
binary build rewrites `SHA256SUMS`, so repeat this step after rebuilding. Retain
the versioned directory, release notes and matching source checkout together.

The Jest runner smoke script builds the binary, packs the SDK and installs it in a clean external project. It verifies captured outcomes, final receipts, cleanup, JSON/text/HTML coverage and Arazzo conformance to the pinned schema. The checks compare exported workflows with raw archive evidence and retained payloads, and preserve deliberately failing Jest outcomes. npm may access its registry/cache; nothing is published.

The [Express/Jest demo](../examples/existing-jest/README.md) checks a larger unchanged external suite. Regenerate lockfiles from package manifests and verify a clean install when dependencies change.

Pass `--binary /absolute/path/to/wiretap` to check a locally built release binary.
On Windows the script invokes npm's JavaScript entry point from the standard
Node installation and requires no Bash.

## Prepare a local container

```sh
docker build --build-arg VERSION=v0.1.0-local --build-arg COMMIT=<git-sha> --build-arg DIRTY=true -t wiretap:local .
docker run --rm wiretap:local version --json
```

Set `DIRTY=false` only for a clean source checkout; the builder cannot inspect Git
because `.git` is excluded from the context. Omitted build arguments leave the
version/commit/source state unspecified (`dev`/`unknown`); omission is not a clean
source attestation.

The image contains the static binary, license and CA trust store. It runs as
numeric nonroot user 65532 with writable `/tmp` and `/artifacts`, and discovers
configuration under `/workspace`. Mount your configuration/contracts and a
writable artifact directory when running capture. Publish container ports to
localhost (for example `-p 127.0.0.1:18000:18000`) when accessing the control API
from the host. Upstreams inside containers need addresses reachable from that
container. The recipe is for local preparation; the CI workflow builds and
checks it without pushing an image.

## Install a prepared release

Keep the binary archives, SDK tarball and `SHA256SUMS` together. The examples
use runtime `v0.1.0-local` and SDK `0.1.0`.

### Verify and install the runtime

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

### Install the JavaScript SDK

Use Node.js 22+ and install the versioned tarball from your existing test project:

```sh
npm install --save-dev /absolute/path/to/release/wiretap-sdk-0.1.0.tgz
```

Commit the resulting package manifest and lockfile to keep the selected SDK
version and package integrity. npm may download the SDK's dependencies. Keep the
tarball at a stable path accessible to developers and CI when using a local file
dependency. Configure Jest using the [Jest SDK guide](../sdk/js/README.md).
