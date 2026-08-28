# Testing

Run all unit and fixture tests:

```bash
go test ./...
```

Run static analysis:

```bash
go vet ./...
```

Build and verify the current static artifact:

```bash
go run ./cmd/udi build
node scripts/verify-dist.mjs
git diff --exit-code -- dist
```

Run the dependency-free Node verifier tests:

```bash
node --test scripts/verify-dist.test.mjs
```

The GitHub collector tests use local `httptest` servers. They cover organization and search pagination, lowercase Hoon classification, primary-language short-circuiting, candidate de-duplication and decisions, explicit core inclusion, merged and unmerged pull requests, ecosystem-wide contributor deduplication, bot exclusion, search incompleteness, and partial failures. They do not require network access or credentials.

Snapshot tests cover schema validation, aggregate containment, canonical definitions, atomic output, public file permissions, and trailing JSON rejection.

The site renderer tests verify draft empty-state rendering, asset copying, and preservation of existing output when validation fails.

The artifact verifier checks the exact public file manifest, copied-asset parity, complete snapshot status, and rendered-page markers. CI rebuilds `dist/` and fails when the committed Vercel artifact differs from the canonical Go build.

Environment-loader tests verify quoted values, optional `export` syntax, shell-variable precedence, and malformed-line errors without reading a real `.env` file.
