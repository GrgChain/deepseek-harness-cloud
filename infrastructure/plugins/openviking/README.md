The unmodified npm artifact `@openviking/dsh-memory-plugin@0.5.31` is vendored
for offline workspace initialization. No install scripts or dependency resolver
run inside a workspace. Its peers come from the fixed DSH 0.2.1-alpha.2 runtime.

The installer records the package in the workspace profile's `dependencies`
and `dsh.profile.bundles`. DSH loads the upstream `openviking-memory-runtime`
entry through its native loader, owns its lifecycle, and exposes it in the
plugin list for inspection and enabling or disabling. The official package
provides capture, recall, commits, durable retries, MCP tools and its three
bundled skills.

The platform's `cloudSessionMemory` service supplies only a loopback endpoint
and the private Worker forwarding channel; upstream credentials stay on the
server. Forwarding retains the current binding, identity and endpoint checks.
The MCP allowlist exposes `search`, `find`, `read`, `list`, `tree`, `grep`,
`glob` and `remember`; it does not expose deletion, editing, resource import
or skill writes, including when those actions are described by a bundled skill.

Source: https://github.com/volcengine/OpenViking/tree/main/examples/dsh-memory-plugin
Artifact: https://registry.npmjs.org/@openviking/dsh-memory-plugin/-/dsh-memory-plugin-0.5.31.tgz
SHA-256: `111c45ce690b5ab4b3ce1db04a9e847af1fe27ebc310ec97c90dcb76d55de46e`
License: Apache-2.0 (as declared by the upstream package).

The Worker embeds this artifact and the installer verifies its SHA-256 before
extracting it. An update must pin a new artifact and checksum, review the native
endpoint/tool allowlist, and run the session integration tests against the
current DSH runtime. Initialization applies to newly created workspaces;
existing workspace profiles are not automatically rewritten on restart.
