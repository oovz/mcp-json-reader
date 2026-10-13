# Security policy and operating model

## Deployment boundary

The server runs as a local subprocess. By default, the MCP client can request any regular file your operating-system account can read. To restrict access, use `--root`, `MCP_JSON_ROOT`, or `--config` with a list of absolute workspace directories. An empty workspace list leaves access unrestricted. An invalid explicit configuration stops startup. For restricted deployments, use an unprivileged account with filesystem permissions limited to the intended data. Treat returned document text as untrusted data in the consuming model's instruction hierarchy.

Restricted reads use `os.Root` to confine traversal, including symlink resolution, to configured directory trees. Overlapping workspaces within one tree share the containing workspace's access boundary. `os.Root` does not isolate mount boundaries, hard-link provenance, or every special filesystem behavior. Keep device and virtual filesystems out of restricted workspaces. Unrestricted mode follows ordinary filesystem paths and symlinks. Both modes check descriptor types and reject nonregular files; Unix nonblocking opens prevent a FIFO from stalling before the descriptor check.

Keep source contents immutable while handles are open. The server checks identity, size, and modification time before and after reading. These checks detect common edits and replacement but do not detect every metadata-preserving mutation or create a snapshot. Replace data between handle lifetimes.

Parser, candidate, output, handle, cursor, and concurrency budgets limit work. Deadline checks are cooperative; OS process limits provide an independent boundary against excessive memory or unresponsive storage. Large input-derived diagnostics are truncated at the MCP boundary.

## Reporting

Use the repository's private vulnerability-reporting channel: `Security` →
`Report a vulnerability`. If it is unavailable, open a nonsensitive issue asking
for a private contact channel. Share exploit details privately.

Include the affected commit, configuration, platform, minimized fixture, reproduction steps, and impact. Keep secrets and third-party data out of public reports.
