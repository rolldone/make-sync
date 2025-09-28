Agent ignore precedence
=======================

This agent uses a deterministic ignore strategy when performing indexing.

- If a file named `.sync_temp/config.json` exists under the repository root and
  it contains a `devsync.ignores` array, the agent will treat that list as
  authoritative. In that case the agent will NOT scan per-directory
  `.sync_ignore` files on disk and will apply only the uploaded patterns.

- If `devsync.ignores` is not present, the agent will fall back to scanning
  per-directory `.sync_ignore` files and applying cascading semantics.

This design ensures the client can centrally control which files the agent
indexes (useful to avoid pulling agent artifacts or other generated files).
