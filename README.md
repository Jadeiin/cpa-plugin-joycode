# JoyCode Plugin

A CLIProxyAPI plugin that provides JoyCode (JD Coding Assistant) as an upstream provider, routing requests through JoyCode's OpenAI-compatible chat completions endpoint.

## Features

- **Executor**: Sends chat-completions requests to JoyCode's API (`https://joycode-api.jd.com`)
- **Auth Provider**: Parses, verifies, and refreshes JoyCode credentials (ptKey)
- **Model Provider**: Lists available JoyCode models (static fallback + dynamic fetch)
- **Dynamic loginType**: Reads `loginType` from auth metadata instead of hardcoding, supporting multiple JD account types

## Supported Models

| Model | Description |
|-------|-------------|
| JoyAI-Code | JoyCode's default coding model |
| GLM-5.1 | ZhipuAI GLM 5.1 |
| Kimi-K2.6 | Moonshot Kimi K2.6 |
| MiniMax-M2.7 | MiniMax M2.7 |
| Doubao-Seed-2.0-pro | ByteDance Doubao Seed 2.0 Pro |

Additional models are fetched dynamically from JoyCode's model list API when a valid ptKey is available.

## Auth File Format

Create a JSON file in CPA's auth directory:

```json
{
  "type": "joycode",
  "ptKey": "your-pt-key-here",
  "userId": "your-user-id",
  "tenant": "JD",
  "orgFullName": "Your Organization",
  "loginType": "N_PIN_PC"
}
```

Multiple auth files can be placed in the auth directory for multi-account rotation. CPA's scheduler will round-robin across available JoyCode credentials.

## Configuration

```yaml
plugins:
  enabled: true
  configs:
    joycode:
      enabled: true
```

## Building

```bash
make build
```

The Makefile chooses the plugin extension from the target platform:

| GOOS | Output |
|------|--------|
| `linux` / `freebsd` | `joycode.so` |
| `darwin` | `joycode.dylib` |
| `windows` | `joycode.dll` |

Release builds can inject the runtime plugin version:

```bash
make build VERSION=0.1.3
```

## Plugin Store Release Assets

The GitHub Actions workflow builds plugin-store-compatible archives for:

| GOOS | GOARCH | Runner |
|------|--------|--------|
| `linux` | `amd64` | `ubuntu-24.04` |
| `linux` | `arm64` | `ubuntu-24.04-arm` |
| `freebsd` | `amd64` | `go-cross/cgo-actions` on `ubuntu-24.04` |
| `darwin` | `amd64` | `macos-15-intel` |
| `darwin` | `arm64` | `macos-15` |
| `windows` | `amd64` | `windows-2025` |
| `windows` | `arm64` | `go-cross/cgo-actions` on `ubuntu-24.04` |

Tag pushes such as `v0.1.3` publish release assets named:

```text
joycode_0.1.3_linux_amd64.zip
joycode_0.1.3_linux_arm64.zip
joycode_0.1.3_freebsd_amd64.zip
joycode_0.1.3_darwin_amd64.zip
joycode_0.1.3_darwin_arm64.zip
joycode_0.1.3_windows_amd64.zip
joycode_0.1.3_windows_arm64.zip
checksums.txt
```

## License

Apache License 2.0
