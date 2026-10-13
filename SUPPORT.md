# Support

The server uses Go 1.27.2 and MCP Go SDK 1.8.0. It supports MCP 2026-07-28
over local stdio, with structured tool results and a 16 MiB inbound frame limit.

| Platform | Architectures | Minimum OS |
| --- | --- | --- |
| Linux | amd64, arm64 | Go 1.27 supported Linux systems |
| macOS | amd64, arm64 | macOS 13 |
| Windows | amd64, arm64 | Go 1.27 supported Windows systems |

Install a standalone release binary, through `go install`, or through npm as
described in [README](README.md). Standalone binaries need no Go, Node.js, or
Python runtime.
The npm launcher requires Node.js 22.14.0 and npm 11.5.1 or later. Release CI
tests the npm command at those versions and the standalone binaries on all six
native targets.

Sources must be immutable regular files. Access is unrestricted by default,
subject to operating-system permissions. `--root`, `MCP_JSON_ROOT`, or a
`--config` file with workspaces restricts source access to those directories.
The [query profile](docs/jsonpath-profile.md) defines the supported syntax;
[performance](docs/performance.md) explains pagination costs and resource limits.

Report defects with the package version, platform, tool arguments, structured
error, and a small nonsensitive fixture. Use the repository issue tracker for
ordinary defects and [SECURITY.md](SECURITY.md) for vulnerability reports.
