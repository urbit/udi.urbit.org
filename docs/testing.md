# Testing

Run all unit and fixture tests:

```bash
go test ./...
```

Run static analysis:

```bash
go vet ./...
```

Build the draft site:

```bash
go run ./cmd/udi build
```

The GitHub collector tests use local `httptest` servers. They cover organization and search pagination, lowercase Hoon classification, primary-language short-circuiting, candidate de-duplication and decisions, explicit core inclusion, merged and unmerged pull requests, ecosystem-wide contributor deduplication, bot exclusion, search incompleteness, and partial failures. They do not require network access or credentials.

Snapshot tests cover schema validation, aggregate containment, canonical definitions, atomic output, public file permissions, and trailing JSON rejection.

The site renderer tests verify draft empty-state rendering, asset copying, and preservation of existing output when validation fails.

Environment-loader tests verify quoted values, optional `export` syntax, shell-variable precedence, and malformed-line errors without reading a real `.env` file.
