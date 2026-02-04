# Implementation Plan (Parallel Codex Instances)

Date: 2026-02-04
Owner: TBD
Status: Draft

## 1) Goal
Enable multiple Codex instances to implement the system in parallel without file conflicts, while preserving a clean Git history and consistent test/commit practices.

## 2) Parallelization Strategy
We will split work into independent vertical slices by component and provide file ownership boundaries, unique build/test commands, and explicit commit conventions.

### 2.1 Workstreams
- **WS1: Core Service Skeleton**
  - Scope: config, lifecycle, HTTP API shell, logging, dependency injection
  - Owned paths: `core/`, `cmd/`, `internal/app/`

- **WS2: Proxy + HTTPS MITM**
  - Scope: explicit proxy, CONNECT, MITM, CA generation
  - Owned paths: `internal/proxy/`, `internal/tls/`

- **WS3: Mock Server**
  - Scope: stub matching, response templating, recording SUT->mock traffic
  - Owned paths: `internal/mock/`

- **WS4: Artifact + Redaction**
  - Scope: artifact model, YAML writer, redaction pipeline
  - Owned paths: `internal/artifact/`, `internal/redact/`, `docs/artifact-openapi.yaml`

- **WS5: JS SDK + Client API**
  - Scope: SDK package, HTTP client, assertion recording
  - Owned paths: `sdk/js/`, `docs/sdk/`

- **WS6: Docs + Examples**
  - Scope: docs, examples, setup instructions
  - Owned paths: `docs/`, `examples/`

### 2.2 Collision Avoidance Rules
- Each workstream owns its paths and does not modify others without coordination.
- Use separate Git branches per workstream.
- Use separate output folders for build/test artifacts to avoid clobbering.

## 3) Local Repo Setup for Parallel Codex

### 3.1 Use Git Worktrees (Recommended)
1. Create a worktree per workstream so each Codex instance has an isolated folder and branch.
2. Example:
   - `git worktree add ../phrond-ws1 codex/ws1-core`
   - `git worktree add ../phrond-ws2 codex/ws2-proxy`
   - `git worktree add ../phrond-ws3 codex/ws3-mock`

Each Codex instance runs in its own worktree directory.

### 3.2 Branch Naming
- Use `codex/` prefix as required.
- Format: `codex/wsX-shortname`
  - Example: `codex/ws2-proxy-mitm`

## 4) Build and Test Conventions

### 4.1 Go Core
- Build: `go build ./...`
- Tests: `go test ./...`

### 4.2 JS SDK
- Install: `npm install`
- Tests: `npm test`
- Build: `npm run build`

### 4.3 Isolation Rules
- Each workstream uses its own `.env` or config under its worktree only.
- Do not run tests that require global ports without reserving a range.

## 5) Commit Conventions

### 5.1 Commit Frequency
- Commit at the end of each meaningful subtask.
- Keep commits small and scoped to the workstream.

### 5.2 Commit Message Format
- `WSX: short description`
  - Example: `WS2: add CONNECT proxy skeleton`

### 5.3 Pre-Commit Checklist
- Run unit tests for your area.
- Run `go test ./...` for core changes, or `npm test` for SDK changes.
- Update docs if behavior changes.

## 6) Integration Cadence

### 6.1 Merge Plan
- WS owners open a PR or request review when their slice is ready.
- The integrator merges in order of dependencies:
  1. WS1 core skeleton
  2. WS2 proxy + MITM
  3. WS3 mock server
  4. WS4 artifact + redaction
  5. WS5 JS SDK
  6. WS6 docs/examples

### 6.2 Conflict Resolution
- If conflicts occur, the workstream owner resolves them within their worktree.
- Avoid cross-editing in the same file unless agreed ahead of time.

## 7) Implementation Task Breakdown

### WS1: Core Service Skeleton
1. Define core packages and config types.
2. Implement lifecycle manager for proxy + mock.
3. Add HTTP API routes for SDK to call.
4. Add structured logging.
5. Add base tests for lifecycle and config validation.

### WS2: Proxy + HTTPS MITM
1. Implement explicit proxy server.
2. Add CONNECT support.
3. Add MITM mode with CA generation and per-host certs.
4. Add passthrough mode with metadata capture only.
5. Add tests for CONNECT and MITM handshake.

### WS3: Mock Server
1. Implement stub registry and matching.
2. Implement response rendering.
3. Record SUT->mock traffic.
4. Add tests for matching and response behaviors.

### WS4: Artifact + Redaction
1. Define artifact model structs.
2. Implement YAML serializer.
3. Implement redaction pipeline.
4. Add tests for redaction and determinism.

### WS5: JS SDK
1. Scaffold SDK package and HTTP client.
2. Implement lifecycle calls.
3. Implement stub and assertion calls.
4. Add tests using a mocked core API.

### WS6: Docs + Examples
1. Add docs for HTTPS trust in macOS and Linux.
2. Add end-to-end example test.
3. Add troubleshooting guide.

## 8) Acceptance Criteria for Each Workstream
- Code compiles and tests pass for its scope.
- No edits outside owned paths.
- At least one test added per new feature.
- Commit history is clean and scoped.

