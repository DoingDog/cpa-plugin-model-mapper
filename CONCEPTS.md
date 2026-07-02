# Concepts

Shared domain vocabulary for this project — entities, named processes, and status concepts with project-specific meaning. Seeded with core domain vocabulary, then accretes as ce-compound and ce-compound-refresh process learnings; direct edits are fine. Glossary only, not a spec or catch-all.

## Model Mapping

### Model Mapper
A CLIProxyAPI plugin that changes the model name used for upstream execution while preserving the client-facing model identity when a configured mapping applied.

### Model Mapping Rule
A declarative match-and-replace rule that transforms a requested model name into an upstream model name for a particular request family or fallback scope.

### Client-Requested Model
The model name supplied by the caller and expected to appear in client-facing responses, even when execution is routed to a different upstream model.

### Upstream Model
The model name actually sent through CLIProxyAPI's execution path after mapping rules have been applied.

## Plugin Distribution

### CPA Native Plugin
A compiled extension loaded by CLIProxyAPI that registers capabilities with the host and participates in request routing or execution through host callbacks.

### Plugin Release Package
A distributable plugin archive whose contents are stable enough for automated installation, verification, and plugin-store indexing.
