# Urbit Development Institute

Source for `udi.urbit.org`, its public GitHub ecosystem metrics, and published technical reports.

The project is intentionally static. A Go command collects and validates aggregate GitHub data, then renders a dependency-free HTML/CSS site into `dist/`. The deployed site requires no server, database, or client-side JavaScript runtime.

## Status

The private GitHub repository and Urbit-team Vercel project are configured. The `udi.urbit.org` DNS record is not yet configured.

The checked-in `data/latest.json` is the latest validated complete snapshot. A draft snapshot uses JSON `null` values, which the site renders as em dashes rather than inventing numbers.

## Requirements

- Arch Linux / Omarchy
- Go 1.23+
- Node.js 22+ for artifact verification
- Python 3 for the documented local static server
- A GitHub token only when refreshing metrics

## Build the Current Snapshot

```bash
go run ./cmd/udi build
node scripts/verify-dist.mjs
```

Generated output:

```text
dist/
```

`dist/` is committed because it is the exact static artifact published by Vercel. After changing templates, styles, assets, or data, rebuild and commit the corresponding `dist/` changes. CI rebuilds the artifact and rejects stale output.

Preview it locally:

```bash
python -m http.server 4173 --directory dist
```

Then open `http://127.0.0.1:4173`.

## Refresh Metrics and Build

`refresh` requires an authenticated token because repository discovery and language checks exceed GitHub's unauthenticated public rate limit. The command automatically loads `GITHUB_TOKEN` from the repository's local `.env` file. A value already exported by the shell takes precedence.

### 1. Create a least-privilege GitHub token

1. Sign in to GitHub.
2. Open your avatar menu and select **Settings**.
3. Open **Developer settings** at the bottom of the left sidebar.
4. Open **Personal access tokens** → **Fine-grained tokens**.
5. Select **Generate new token**.
6. Use a descriptive name such as `udi-local-metrics`.
7. Set an expiration date. Ninety days is a reasonable local-development default.
8. Select your own account as the resource owner.
9. Limit repository access to **Public repositories (read-only)**. If GitHub instead asks for selected repositories, a classic public-read token may be simpler because this collector reads public repositories across multiple organizations.
10. Under **Repository permissions**, grant only:
    - **Metadata: Read-only** — normally selected automatically;
    - **Contents: Read-only** — needed for commits and language metadata;
    - **Pull requests: Read-only** — needed for merged-PR authors.
11. Do not grant write, administration, organization, workflow, package, or account permissions.
12. Generate the token and copy it immediately. GitHub displays it only once.

GitHub's token UI and organization policies can change. If a fine-grained token cannot read public repositories belonging to multiple organizations, create a **Tokens (classic)** personal access token with a short expiration and **no scopes selected**. An unscoped classic token can authenticate public-data requests without granting private-repository access.

### 2. Create the ignored `.env`

From the repository root:

```bash
cp .env.example .env
chmod 600 .env
```

Open `.env` in your editor and replace the placeholder:

```text
GITHUB_TOKEN=github_pat_your_actual_token_here
```

Do not add spaces around `=`. Quotes are optional. The real `.env` is covered by this repository's `.gitignore`; `.env.example` is safe to commit because it contains no credential.

Confirm that Git will not include it:

```bash
git check-ignore -v .env
```

The output should identify the `.env` rule in `.gitignore`.

### 3. Run the refresh

```bash
go run ./cmd/udi refresh
```

The command will:

1. load `.env` without printing the token;
2. read `config/repositories.json`;
3. collect and validate GitHub repository and contributor aggregates;
4. write `data/latest.json` and a dated file under `data/history/`;
5. rebuild the static site in `dist/`.

On success, the log reports candidate repositories, included repositories, active repositories, active contributors, and all-time contributors. It does not log contributor identities.

## Review and Override Repository Discovery

Repository discovery is separately runnable so you can audit the repository set without waiting for contributor and pull-request history:

```bash
go run ./cmd/udi discover
```

This writes:

```text
data/candidates.json
data/candidates.md
```

`candidates.json` is the structured audit. `candidates.md` is the full human-readable list for review and copying override names. Each public candidate includes its GitHub URL, description, primary language, last push time, discovery sources, Hoon evidence, inclusion state, and exact decision. A full refresh also marks whether each included repository is active in the approved window. No contributor identities are present.

Typical decision values are:

- `included-hoon` — GitHub reports Hoon as the primary language or in the language-byte map;
- `included-explicit` — manually included regardless of language;
- `excluded-no-hoon` — discovered, eligible, but no Hoon evidence was found;
- `excluded-fork`, `excluded-archived`, or `excluded-disabled`;
- `excluded-explicit` — manually excluded.

The source-of-truth override lists are in `config/repositories.json`:

```json
{
  "explicitInclude": [
    "urbit/urbit",
    "urbit/vere",
    "owner/repository-to-force-include"
  ],
  "explicitExclude": [
    "owner/repository-to-force-exclude"
  ]
}
```

Rules:

1. Use exact `owner/repository` names.
2. Explicit exclusion wins if a repository appears in both lists.
3. Include non-Hoon repositories only when they are genuinely part of the Urbit development ecosystem.
4. Exclude mirrors, examples, abandoned repositories, and unrelated Hoon experiments when they would distort the metric.
5. Run `go run ./cmd/udi discover` after editing overrides and review the candidate report diff.
6. Run `go run ./cmd/udi refresh` only after the included set looks correct.

The configured global search query is also in `config/repositories.json`. It currently discovers public, non-archived repositories whose GitHub-classified language is Hoon. Configured organizations are still enumerated so repositories containing some Hoon—but whose primary language is something else—can be found through their language-byte maps.

### 4. Preview and review the result

```bash
python -m http.server 4173 --directory dist
```

Open `http://127.0.0.1:4173`, then review:

- the three ecosystem headline metrics;
- the `urbit/urbit` and `urbit/vere` breakdowns;
- the update date;
- `data/latest.json` and the dated history diff.

Stop the preview server with `Ctrl-C`.

### 5. Rotate or remove the token

Delete the local file when it is no longer needed:

```bash
rm .env
```

Revoke or regenerate the token from GitHub's **Developer settings** if it is exposed, expires, or is no longer in use.

The token needs read-only access to public repository metadata, contents, commits, and pull requests. The command invokes it to:

- enumerate repositories in configured Urbit-related organizations;
- verify Hoon language evidence;
- identify linked commit authors and merged-PR authors;
- deduplicate authors across all included repositories;
- produce aggregate ecosystem and core-repository metrics.

The token is loaded by `internal/config/env.go`, read by `cmd/udi/main.go`, and sent only in the GitHub REST API `Authorization` header. It is never stored in snapshots or site output.

If any required GitHub request, pagination sequence, core repository, or validation check fails, `data/latest.json` remains unchanged and the command exits with an error.

Only one `build`, `discover`, or `refresh` command can run at a time. Concurrent commands fail immediately using the gitignored `.udi-operation.lock` file, preventing overlapping manual, cron, or CI publication.

## Commands

```bash
go run ./cmd/udi build
go run ./cmd/udi discover
go run ./cmd/udi refresh
go test ./...
go vet ./...
node --test scripts/verify-dist.test.mjs
node scripts/verify-dist.mjs
```

Optional flags:

```text
-root PATH        repository root
-output PATH      static output directory; defaults to dist
-github-api URL   refresh-only GitHub API override for testing
```

## Project Structure

```text
cmd/udi/                 build and refresh CLI
config/                  repository discovery configuration
data/latest.json         current public aggregate snapshot
data/history/            dated successful snapshots
internal/github/         GitHub REST collector
internal/metrics/        public data contract and validation
internal/site/           static renderer
site/                    HTML page templates, CSS, and static assets
scripts/                 dependency-free Vercel artifact verification
docs/methodology.md      detailed metric specification and limitations
dist/                    generated, committed Vercel artifact
vercel.json              static deployment configuration
```

The build renders both the overview and the concise public methodology page. The detailed Markdown methodology remains the operator-facing specification for auditing collector behavior.

## Vercel Deployment

Vercel uses the **Other** framework preset, runs the dependency-free Node verifier, and publishes only `dist/`. It does not run the Go collector or renderer and does not need `GITHUB_TOKEN` or any other environment variable.

The Vercel project must use:

- Git repository: `urbit/udi.urbit.org`
- Production branch: `master`
- Root directory: repository root
- Build and output settings: inherited from `vercel.json`

Pull requests receive preview deployments through Vercel's Git integration. A merge to `master` publishes the already-validated artifact to production. Metric updates remain an operator workflow: run `go run ./cmd/udi refresh`, review both `data/` and `dist/`, and commit them together.

Local CLI uploads are protected by `.vercelignore`, which excludes `.env`, lock files, local Vercel state, and interrupted-build directories. Never add `GITHUB_TOKEN` to Vercel; deployment does not use it.

## Font

The site uses the same Space Mono font asset as `urbit.org`. Space Mono is distributed under the SIL Open Font License 1.1, included at `site/assets/fonts/OFL.txt`.
