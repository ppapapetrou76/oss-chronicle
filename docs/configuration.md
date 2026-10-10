# Configuration

oss-chronicle works without any configuration. When the defaults do not fit your project, set action inputs in the workflow, or add a config file.

## Action inputs

| Input | Default | |
|---|---|---|
| `token` | `github.token` | Reads pull requests. |
| `config` | `.github/oss-chronicle.yaml` | Defaults apply when the file does not exist. The job must check out the repository for the action to find it. |
| `from`, `to` | config window (last 90 days) | `YYYY-MM-DD`. `to` also takes `now`, the day of the run. |
| `periods` | config, then all eight [periods](#periods) | Separated by commas or spaces, such as `30d,90d,last-year`. Replaces the config's list; `none` computes the window alone. Date ranges are set in the config file. |
| `top` | `25` | People listed in the run summary. |
| `out-dir` | `oss-chronicle` | Where the files are written. |
| `upload-artifact` | `true` | Upload the files as an artifact. |
| `artifact-name` | `oss-chronicle` | Set it when the action runs more than once in a workflow. |
| `archive` | `true` | Keep [last year's pull requests](#last-years-archive) in the Actions cache. `false` collects everything on every run. |

## Action outputs

| Output | Path of |
|---|---|
| `ledger` | `ledger.json`, the default period's ledger |
| `periods` | `periods.json`, the ledger for every period |
| `summary` | `summary.md` |
| `data` | the collected pull requests, `prs.jsonl.gz` |
| `meta` | `meta.json`: default branch, `.gitattributes` and `CODEOWNERS`, which you need to recompute the ledger |
| `csv` | the directory of CSV files |
| `site` | the directory of the web page, ready for `actions/upload-pages-artifact` |

One output is not a path: `archive` is `used` when last year's pull requests came from the cache, `written` when the run collected and cached them, and empty when no archive applies.

## The config file

Every key is optional, and each one you leave out keeps its default. Unknown keys fail the run, so a typo is reported instead of silently ignored.

> [!IMPORTANT]
> A list you set replaces the default list; it does not add to it. To add a bot, copy the default `bots.logins` or `bots.patterns` below and append to it. `size.exclude_extra` is the one list that adds to the built-in one.

The values below are the defaults, apart from the lines marked as examples:

```yaml
default_branch: ""       # the repository's default branch when empty
window:
  days: 90               # or from: YYYY-MM-DD, with to: YYYY-MM-DD or now; the default period
periods: [7d, 30d, 90d, 180d, 365d, last-month, last-quarter, last-year]   # see Periods below
pull_requests:
  backport:              # count as maintenance
    head_prefixes: [cherry-pick, backport, automated-cherry-pick-of-, mergify/bp/]
    base_branches: ["release-*", "release/*", "release_*", "stable-*", "stable/*", "*-stable", "v[0-9]*"]
  dependencies:          # count as maintenance
    authors: [dependabot, renovate]
    title_prefixes: ["chore(deps", "fix(deps", "build(deps"]
bots:
  patterns: ["[bot]", -bot, -robot, mergebot, dependabot, renovate, github-actions, copilot, codecov, coderabbit, sonarqube, netlify, snyk]  # matched anywhere in the login, ignoring case
  logins: [bors, homu]   # exact logins
comments:
  bot_command_prefixes: ["/", "@dependabot", "@renovate"]   # comments starting with these are not counted
  approval_commands: [/lgtm, /approve, "@bors r+", "@bors r=", "bors r+", "bors r="]   # count as a review
  merge_commands: [/approve, /merge, "@bors r+", "@bors r=", "bors r+", "bors r=", "@bors merge", "bors merge",
    "@mergifyio queue", "@mergifyio merge", "@pytorchbot merge", "@pytorchmergebot merge"]   # credit a bot's merge
size:
  buckets:               # the last bucket has no max
    - {name: XS, max: 10, weight: 0.5}
    - {name: S, max: 50, weight: 1}
    - {name: M, max: 250, weight: 2}
    - {name: L, weight: 3}
  exclude_extra: ["gen/**", openapi_generated.go]   # example; .gitignore-style, added to the built-in list
  # exclude: [...]       # replaces the built-in list: lock files, vendor/ and node_modules/,
  #                      # generated protobuf, zz_generated*, minified files and test snapshots
  use_gitattributes: true          # also skip files .gitattributes marks generated, vendored or binary
  deletions_weight: 1              # counted lines = additions + deletions_weight * deletions
  test_patterns: ["*_test.go", "*_test.py", "test_*.py", "*.test.*", "*.spec.*", "test/**", "tests/**", "**/test/**", "**/tests/**", "**/testdata/**"]
  test_weight: 1                   # multiplier for lines in test files
  review_feedback_multiplier: 2
components:
  use_codeowners: false            # name files no map entry matches after their CODEOWNERS owners
  map:                             # example; empty by default, see Components below
    - {name: UI, paths: [/ui/]}
    - {name: API server, paths: [/server/, /pkg/apiclient/]}
publish:
  opt_out: []            # logins left out of every published table
```

GitHub App accounts are always treated as bots, whatever the patterns say. Pull requests a bot opens count as maintenance, so a login added to `bots` moves that account's pull requests, and the reviews, merges and closes on them, out of the total.

## Periods

Each run computes a ledger for every period in `periods`, from one collection that reaches back to the earliest of them:

| Period | Covers |
|---|---|
| `7d`, `30d`, `90d`, … | The last N days, up to and including the window's last day (today, unless `window.to` is set). Any number of days up to 3,660. |
| `last-month` | The last full calendar month before the one the window ends in. |
| `last-quarter` | The last full calendar quarter: January–March, April–June, July–September or October–December. |
| `last-year` | The last full calendar year. |
| A date range | From one date to another, or from a date until `now`. See [Date ranges](#date-ranges). |

Dates are UTC. The window is the default period: the run summary, `ledger.json` and the top-level CSV files describe it. Every period, the default included, is in `periods.json`, and the web page has a picker to switch between them. The other periods' CSV files are in `csv/periods/<id>/`. When no listed period has the window's dates, the window is added to the list.

`last-year` decides how far back collection goes: up to two years by December. The [archive](#last-years-archive) keeps that to one run a month. Set `periods` to a shorter list to collect less, or to `[]` for the window alone. The `periods` input does the same without a config file: `periods: 30d,90d` or `periods: none`.

### Date ranges

A date range covers fixed dates, such as a release cycle or a fiscal year, and appears in the picker next to the rolling periods:

```yaml
periods:
  - 90d
  - last-quarter
  - id: v3.2                 # used in the page address (?period=v3.2) and the CSV folder
    label: Release 3.2       # shown in the picker; defaults to the dates
    from: 2026-03-01
    to: 2026-06-15
  - id: this-year
    from: 2026-01-01
    to: now                  # until the window's last day: today, unless window.to is set
```

The `id` is up to 64 lowercase letters, digits, dots, dashes and underscores, starting and ending with a letter or digit. `window`, `last-month`, `last-quarter`, `last-year` and any number of days such as `2d` are reserved. `to` defaults to `now`.

A range can be listed before it is over. When the window ends before the range's `to` date, the range ends with the window and its label says "(so far)". A range that starts after the window's last day is left out. With the default window, the window's last day is the day of the run.

> [!WARNING]
> Every run collects back to the earliest period's start, and the archive only covers last year. A range that starts years back makes every run fetch everything since then. For a one-off look at an old release, run the action once with the `from` and `to` inputs instead.

To make a range the default period, set the window to the same dates: `window: {from: 2026-01-01, to: now}` makes `this-year` the period the run summary describes. If two periods have the window's dates, the first one listed is the default.

The counting rules are the same for every period, and so is today's configuration: the components map, `CODEOWNERS`, `.gitattributes` and bot lists of the day of the run apply to all of them. Over a year, a folder that was renamed or a team that was reorganized shows up under its current name. The lists of pull requests waiting for a first response describe the repository as it was when the run collected the data, whatever the period.

## Last year's archive

A pull request last updated during last year can only change by being updated again, which moves its update date into this year. So the action keeps those pull requests in the Actions cache. The first run of each month collects everything and stores them. Later runs that month collect from January 1 of this year, or from the start of an earlier period such as the last 365 days, and add the stored ones. The pull requests are the same as a full collection finds, and so are the ledgers.

- The cache needs no extra permissions. GitHub deletes entries unused for 7 days; a lost entry costs one full run.
- The archive holds data, not results, so config changes and opt-outs apply to it straight away. A new version of the action that collects different fields starts a new archive.
- Each month starts over, which picks up changes that do not update a pull request: a renamed or deleted account, or a commit email someone has since added to their GitHub account. Until then, a renamed account keeps its old login in the archived pull requests, so list both logins when someone opts out.
- Any workflow run in the repository can read the cache, including pull requests from forks of a public repository. It holds the same pull request data as the artifact, comments included.
- It applies when `periods` includes `last-year` and no other period reaches back as far.

### Finding and clearing it

The archive is one cache entry per repository and month, listed under **Actions → Caches** or by `gh cache list`. Its key reads as repository, year, collector fingerprint and month:

```text
oss-chronicle-owner_name-2025-287d91e4-2026-10
```

A new year, a new month, or a version of the action that fetches different fields gives a new key, and so a full collection. The entry holds `prs.jsonl.gz`, in the same format as the artifact's, and `archive.json`, which records the repository, year, fingerprint, month and collection time. A run checks `archive.json` before it uses the data.

Runs on other branches, and pull requests, can read what the default branch saved, but what they save stays with their own branch. GitHub also deletes the least recently used entries once a repository's caches pass its size limit (10 GB by default).

To collect everything on the next run, delete the entry:

```bash
gh cache delete oss-chronicle-owner_name-2025-287d91e4-2026-10 --repo owner/name
```

or run once with `archive: false`, which neither reads nor writes it.

## Components

`components.map` names the areas of your codebase and lists the paths that belong to each, so the components table reads "UI" or "API server" instead of raw folder names.

A changed file gets its component from the first rule that applies:

1. **`components.map`**: the first entry with a path matching the file. Paths are `.gitignore`-style patterns.
2. **`CODEOWNERS`**, when `use_codeowners` is on (it is off by default): the file's owners become the component name.
3. **The top-level directory** the file is in, or `(root files)` for files at the top.

Order matters. In this example `/controller/hydrator/` belongs to Source hydrator, because that entry comes before Application controller, whose `/controller/` also matches:

```yaml
components:
  map:
    - {name: Source hydrator, paths: [/commitserver/, /controller/hydrator/, "*hydrator*"]}
    - {name: Application controller, paths: [/controller/]}
    - {name: UI, paths: [/ui/, /ui-test/]}
    - {name: Docs, paths: [/docs/, /mkdocs.yml]}
    - {name: Shared libraries, paths: ["**"]}
```

A last entry of `"**"` catches every file the others miss. Without it, those files fall back to their folder, or first to `CODEOWNERS` when `use_codeowners` is on.

Without a map, the components are your top-level directories. `use_codeowners: true` names files after their `CODEOWNERS` owners instead; files only a catch-all rule such as `*` matches still go by their folder, so the list can mix owners and folders. Owners are teams or people, not areas of the code, so turn it on only when each team is named after the one area it owns, such as `@org/ui`. Owners named after approver groups or people, and files with several owners, give component names like `@org/approvers-cli @org/approvers-docs`; a components map is the better fix there.

## Opting people out

Logins in `publish.opt_out` are left out of the run summary, the web page, the CSV files and `ledger.json`, including the per-component reviewer lists. When `use_codeowners` is on, they are also removed from component names taken from `CODEOWNERS`: files owned by `@alice @bob` belong to `@bob` once Alice opts out, and files only she owns fall back to their top-level directory. Their activity still counts in the totals. See [Before you publish](publishing.md#before-you-publish) for what opt-out does and does not cover.

## Run it locally

```bash
GITHUB_TOKEN=$(gh auth token) GITHUB_REPOSITORY=owner/name go run ./cmd/oss-chronicle run
```

Run these from a clone of oss-chronicle. `GITHUB_REPOSITORY` names the repository to read, as the Actions runner sets it for the action; `run`, `collect` and `archive-key` need it. `run` collects and counts. The steps also run separately:

- `collect` only fetches the data: `prs.jsonl.gz` and `meta.json`.
- `run` and `collect` take `--archive DIR` to keep [last year's pull requests](#last-years-archive) in a directory between runs, as the action does with the cache.
- `compute` only counts it, which is how you try config changes on data you already have, such as a run's artifact. Add `--periods-out periods.json` to compute every period too.
- `page --ledger ledger.json --out site` only builds the web page. Add `--periods periods.json` for the period picker.

To recompute an artifact's data with your own config:

```bash
go run ./cmd/oss-chronicle compute --data prs.jsonl.gz --meta meta.json \
  --config /path/to/your-repo/.github/oss-chronicle.yaml \
  --from 2026-07-12 --to 2026-10-09 --out ledger.json --csv csv
```

Set `--from` and `--to` to the `from` and `to` in the artifact's `ledger.json`. Without them the window ends today, and the data holds nothing newer than the day it was collected. `--config` is read relative to the current directory, and without `--out` the ledger goes to standard output.

With `--periods-out`, `compute` skips any period that starts before the data does, as recorded in `meta.json`, and says so; a period computed from data that misses part of it would be wrong without looking wrong.
