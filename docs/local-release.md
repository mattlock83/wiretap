# Local installation and release preparation

Wiretap uses the Go module `github.com/mattlock83/wiretap`. Build tools require
Go 1.26+; release preparation and CI use Go 1.26.10. The JavaScript SDK requires
Node 22+; the CI matrix covers Node 22 and 24 on Linux, macOS and Windows.

All commands below create local files or local images. They do not publish npm
packages, container images, GitHub releases or binary downloads.

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
go run ./scripts/release --version v0.1.0-local
```

This builds Linux, macOS and Windows binaries for amd64 and arm64 under
`dist/bin/`, then creates `.tar.gz` archives for Linux/macOS, `.zip` archives for
Windows and `dist/SHA256SUMS`. Each archive contains only its binary and the MIT
license. Use `--platforms host` for the current platform, or a comma-separated
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

## Pack the SDK and prove a clean-project install

```sh
npm ci --prefix sdk/js
npm pack ./sdk/js --pack-destination ./dist
node scripts/package-smoke.mjs
```

The package has a source-file allowlist and public exports for `@wiretap/sdk`,
`@wiretap/sdk/tracing`, `@wiretap/sdk/jest` and
`@wiretap/sdk/jest-environment`. It includes TypeScript declarations and a copy
of the MIT license. Test fixtures, test dependencies and the lockfile are
excluded from the package.

The cross-platform smoke script builds the local binary and packs the SDK,
installs the tarball into a temporary project outside this checkout, starts an
ordinary HTTP application and runs existing Node tests through Wiretap. It
checks the exact returned archive/output paths, coverage JSON, Postman collection
and recorded outcomes for both passing tests and a deliberately failing test.
The failing test must retain exit status 1 while still writing its outputs.
The temporary project is removed afterward. npm may access its dependency
registry/cache; nothing is published.

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
