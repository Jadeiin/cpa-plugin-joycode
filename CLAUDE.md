# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & Test

```bash
go vet ./...              # lint
go test -v -count=1 ./... # all tests
go test -v -run TestABIRegistration -count=1 ./...  # single test
make build                # local build (produces joycode.dylib on macOS)
make build VERSION=0.1.1  # build with injected version string
```

Release: push a `v*` tag to trigger CI (7 platforms). Update `pluginVersion` in `main.go`, `VERSION` in `Makefile`, and version examples in `README.md`/`README_CN.md` before tagging.

## Architecture

CGo shared-library plugin for CLIProxyAPI (CPA). No `net/url` or `net/http` — all HTTP calls go through CPA's host callback mechanism (`callHost`/`callHostJSON` → C function pointer → CPA runtime).

**abi.go** — Cgo declarations, ABI envelope, host callback wrappers, plugin init/call/free/shutdown exports, method dispatch. The `handleRegister` function **hardcodes capability flags** (`Executor`, `AuthProvider`, `ModelProvider`) as `true` because CGo plugins cannot implement host-side Go interfaces — the actual implementation lives in the ABI method dispatch, not in `pluginapi.Plugin` struct fields.

**CGo dual-state requirement**: `cliproxy_plugin_init` must store the host API pointer in **both** Go (`joycodeABIState.host`) and C (`C.joycode_store_host_api(host)`) layers. `callHost` uses `C.joycode_call_host_api` which reads from the C static `joycode_stored_host`; missing the C-layer store causes all host callbacks to return error code 1 (NULL pointer check). `JoycodePluginShutdown` must clear both layers.

**main.go** — `buildPlugin()` returns `pluginapi.Plugin` with metadata and supplementary fields (model scope, input/output formats). The `Capabilities.Executor`/`AuthProvider`/`ModelProvider` interface fields remain nil; they are not used for registration.

**joycode.go** — All business logic. Key functions: `handleExecutorExecute`/`handleExecutorExecuteStream` (chat completions), `handleAuthParse`/`handleAuthLoginStart`/`handleAuthLoginPoll`/`handleAuthRefresh` (auth lifecycle), `handleModelStatic`/`handleModelForAuth` (model list), `injectPayloadFields` (payload enrichment with JoyCode-specific fields), `decompressGzip` (response decompression), `verifyJoyCodeToken` (ptKey validation via userInfo API).

**Registration JSON field names**: `pluginapi.Metadata` has no json tags, so fields serialize as Go defaults (uppercase: `Name`, `Version`, `Author`, `GitHubRepository`). The host expects these exact names. The `TestABIRegistrationSerializesCorrectFieldNames` test guards against mismatches.

**Capabilities JSON**: uses snake_case (`executor`, `auth_provider`, `model_provider`, `executor_model_scope`, etc.) matching `rpcCapabilities` tags on the host side.

## Key constraints

- `defaultLoginType` is `"N_PIN_PC"` — `loginTypeFromAuthMetadata` reads from `AuthMetadata["loginType"]` with this fallback, supporting multiple JD account types.
- `reasoningModels` map (`GLM-5.1`, `Kimi-K2.6`, `MiniMax-M2.7`) preserves existing `thinking` param; non-reasoning models always get `thinking: {type: "disabled"}`.
- Stream handling: `readAndEmitStreamChunks` runs in a goroutine, reading chunks via `host.http.stream_read` and emitting SSE lines via `host.stream.emit`.