# Security policy and operating model

## Deployment boundary

Run the server as an unprivileged local subprocess with a dedicated trusted data root and filesystem read permissions limited to that directory. The MCP client can request any regular file reachable inside that root. Treat returned document text as untrusted data in the consuming model's instruction hierarchy.

`os.Root` confines traversal, including symlink resolution. It does not isolate mount boundaries, hard-link provenance, or every special filesystem behavior. Keep device and virtual filesystems out of the root. Descriptor type checks reject nonregular files; Unix nonblocking opens prevent a FIFO from stalling before the descriptor check.

Keep source contents immutable while handles are open. The server checks identity, size, and modification time before and after reading. These checks detect common edits and replacement but do not detect every metadata-preserving mutation or create a snapshot. Replace data between handle lifetimes.

Parser, candidate, output, handle, cursor, and concurrency budgets limit work. Deadline checks are cooperative; OS process limits provide an independent boundary against excessive memory or unresponsive storage. Large input-derived diagnostics are truncated at the MCP boundary.

## Reporting

Use the repository's private vulnerability-reporting channel: `Security` →
`Report a vulnerability`. If it is unavailable, open a nonsensitive issue asking
for a private contact channel. Share exploit details privately.

Include the affected commit, configuration, platform, minimized fixture, reproduction steps, and impact. Keep secrets and third-party data out of public reports.
