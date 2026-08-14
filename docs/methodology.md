# UDI GitHub Metrics Methodology

Methodology version: `2026-08-12.1`

## Purpose

These metrics describe observable development in a best-effort index of public Urbit ecosystem repositories. They do not measure every form of contribution to Urbit and should not be described as an exhaustive census.

## Included Repositories

An included repository is:

- public;
- not a fork;
- not archived or disabled;
- reported by GitHub's languages endpoint as containing Hoon; or
- an approved core exception listed in `config/repositories.json`.

The current approved exceptions are `urbit/urbit` and `urbit/vere`. This ensures the core Hoon OS repository and C runtime remain represented regardless of GitHub language classification.

Discovery combines:

- full public-repository enumeration for configured Urbit-related organizations;
- GitHub repository search for public, non-archived repositories classified as Hoon;
- explicit include and exclude overrides in `config/repositories.json`.

The candidate audit is stored in `data/candidates.json`. Repository search has GitHub-imposed limits; the collector rejects incomplete or over-cap search responses rather than silently publishing partial coverage. GitHub code search for repositories containing Hoon files when Hoon is not a classified repository language is not yet enabled outside the configured organizations.

## Active Repository

An included repository is active when its default branch has at least one commit, or it has at least one merged pull request, during the trailing six calendar months ending at collection time.

## Contributor Identity

A contributor is counted only when GitHub links the activity to a numeric GitHub user ID. Numeric IDs are used to deduplicate the same person across repositories and contribution types.

The collector excludes:

- anonymous or unlinked commit authors;
- accounts whose GitHub user type is `Bot`;
- logins ending in `[bot]` or `-bot`.

The collector never writes public contributor IDs, logins, names, email addresses, or email hashes. Only aggregate counts leave the collection process.

## Active Contributor

An active contributor is a unique identifiable non-bot author of either:

- a default-branch commit during the trailing six months; or
- a pull request merged during the trailing six months;

across **any included ecosystem repository**.

The ecosystem count is a union of contributor identities. It is not a sum of per-repository counts. The `urbit/urbit` and `urbit/vere` figures are separate breakdowns and do not define the boundary of the ecosystem metric.

## All-Time Contributor

An all-time contributor is a unique identifiable non-bot author represented in GitHub's repository contributor aggregation or as the author of a merged pull request across the current included repository set.

GitHub notes that its contributor endpoint may return cached data and links only the first 500 author email addresses in a repository to GitHub users. Unlinked authors are excluded rather than guessed or published under unstable email/name hashes. This makes the all-time count conservative.

## Completeness and Failure Policy

A measured snapshot is published only when:

- every required organization, language, contributor, commit, and pull-request request succeeds;
- every pagination chain completes before the configured safety limit;
- both core repositories are included;
- aggregate and containment validation passes.

Any partial failure leaves the last known-good snapshot untouched. A measured zero is distinct from missing data; the draft uses JSON `null` and renders an em dash.

## Known Limitations

- Organization enumeration does not yet discover independent Hoon repositories across all of GitHub.
- Global repository search finds repositories classified as Hoon, but can miss independent mixed-language repositories where Hoon is not a classified language.
- Contributions through issues, reviews, comments, documentation outside included repositories, chat, operations, design, and community work are not counted.
- Anonymous or unlinked commits are excluded.
- GitHub's contributor aggregation can lag and has identity-linking limits.
- Bot detection is intentionally conservative and may not identify custom automation accounts.
- Repository inclusion changes can change historical totals; the snapshot includes a configuration digest and methodology version for provenance.
