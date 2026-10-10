# Getting started

This page takes you from nothing to a first contribution ledger for your repository. It needs one workflow file and no other setup.

## 1. Add the workflow

Create `.github/workflows/oss-chronicle.yml`:

```yaml
name: Contribution ledger
on:
  schedule:
    - cron: '17 4 * * *'   # daily; pick any minute
  workflow_dispatch:       # adds a "Run workflow" button
permissions:
  contents: read
  pull-requests: read
concurrency:
  group: oss-chronicle
  cancel-in-progress: false
jobs:
  ledger:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v7          # only needed for .github/oss-chronicle.yaml
      - uses: ppapapetrou76/oss-chronicle@main
```

The action only reads. It never writes to the repository, and it never comments, labels or opens issues. The default job token is enough to read the repository the workflow runs in.

Without a config file, the defaults apply: the last 90 days, the usual bots and dependency updaters, and components named after your top-level directories. You can add a config later (see [Configuration](configuration.md)).

## 2. Run it

Open the **Actions** tab, pick **Contribution ledger** and click **Run workflow**, or wait for the schedule. Each run collects every pull request updated since the earliest of its [periods](configuration.md#periods) starts, which with the default periods is January 1 of last year. The first run of each month stores last year's pull requests in the Actions cache, and later runs that month fetch only the rest. See [Last year's archive](configuration.md#last-years-archive).

How long a run takes depends on how many pull requests were updated in that time, which grows through the year: by December, last year starts almost two years back. On a busy repository a full collection can take a while. With the [archive](configuration.md#last-years-archive), only the first run of each month collects everything, plus any run after the cache went unused for 7 days. The job token gets 5,000 rate-limit points an hour. To collect less, list fewer periods, or drop `last-year`.

If GitHub rate-limits the token, the collector waits, either until the limit resets or for as long as GitHub asks, and carries on. It gives up when a single request is still limited after ten waits.

## 3. Read the results

### The run summary

Open the finished run. Its summary page shows:

- **Totals** for the window: PRs landed, PRs reviewed (and how many of those reviews left feedback), comments, merges, triage closes and maintenance work.
- **People**: the top 25 contributors by total. Change the number with the `top` input.
- **Size of landed PRs**: how many fell into each size bucket.
- **Components**: per area of the code, the PRs landed, authors and reviewers, the top reviewer's share, response and merge times, and how many PRs wait for a response.
- **Waiting for a first response**: the oldest 10 open PRs that nobody but their author has responded to.
- **Left out**: every kind of activity that was not counted, or was counted only as maintenance, with how much of it there was.
- **How each number is computed**, folded: one sentence per number, and the rules the run counted with, marked where your config changed them.

> [!WARNING]
> On a public repository, anyone can open the Actions tab, so this summary is public from the first run. Read [Before you publish](publishing.md#before-you-publish) before you add the workflow.

### The files

Each run also uploads an artifact called `oss-chronicle`, at the bottom of the run page. It holds:

| File | What it is |
|---|---|
| `site/index.html` | The web page: a period picker, sortable tables, a breakdown per person and per component, a person × component review grid, PR sizes, the left-out activity, and how each number is computed with the rules the run used. Open it in a browser; it loads nothing from other sites. |
| `ledger.json` | Everything the summary shows, for the default period, as JSON. |
| `periods.json` | The same for every period: the last 7, 30, 90, 180 and 365 days, last month, last quarter and last year by default. See [Periods](configuration.md#periods). |
| `summary.md` | The run summary as Markdown. |
| `csv/` | The tables as CSV files for spreadsheets (see below), for the default period, with the other periods in `csv/periods/<id>/`. |
| `prs.jsonl.gz`, `meta.json` | The collected data, to recompute the ledger with a different config without collecting again (see [Run it locally](configuration.md#run-it-locally)). |

GitHub keeps artifacts for 90 days unless your repository or organization sets a shorter retention. To keep the page somewhere permanent, see [Publishing](publishing.md).

The CSV files have a header row and one row per record:

| File | One row per |
|---|---|
| `people.csv` | person, in ranking order, with every count and both weights |
| `components.csv` | component, with every number from `ledger.json` except the lists |
| `person_component.csv` | person and component: PRs landed and reviews, most activity first |
| `waiting.csv` | open PR waiting for a first response, oldest first |
| `left_out.csv` | reason activity was not counted |

Text a spreadsheet would run as a formula (starting with `=`, `+`, `-`, `@`, their full-width forms, a tab or a line break) gets a leading `'`. So a `CODEOWNERS` component such as `@org/team` appears as `'@org/team`; strip the quote to join it with `ledger.json`. An empty median cell means there was nothing to measure. People in `publish.opt_out` are left out of `person_component.csv`, so its review counts can add up to less than `components.csv` shows.

The files are UTF-8 without a byte order mark. In Excel, open them through **Data › From Text/CSV** so names with accents display correctly.

## 4. Adjust

Look at the first summary with someone who knows the project:

- **Components named after people or folders.** Add a [components map](configuration.md#components) to name the areas yourself.
- **A bot in the people table.** Add it to `bots.logins`. A list you set replaces the default list, so copy the default from [Configuration](configuration.md#the-config-file) and append to it.
- **Merges missing, or credited to the wrong person.** Check [how your merge workflow is counted](how-it-counts.md#merge-workflows). If your merge bot uses its own command, add it to `comments.merge_commands`, again keeping the defaults in the list.

Put the config in `.github/oss-chronicle.yaml`. The workflow above already checks out the repository, which is how the action finds the config.

## Troubleshooting

| Symptom | What to do |
|---|---|
| A later step in the same job uses the wrong Go version | The action builds itself with `actions/setup-go`, which leaves its Go on the `PATH`. Run it in its own job. |
| "still rate limited after 10 waits" | Another workflow is using the same token heavily. Run at a quieter time, or pass a token with its own rate limit. |
| The config seems ignored | Check that the job checks out the repository before the action runs, and that the file is at `.github/oss-chronicle.yaml` or wherever the `config` input points. Unknown keys fail the run, so a typo in a key shows up as an error. |
| A component is called something like `@alice @bob` or `@org/approvers` | `use_codeowners` is on and your `CODEOWNERS` names people or approver groups, not areas. Turn it off, or add a [components map](configuration.md#components). |
| The upload fails because the artifact name exists | Two steps in the run use the same `artifact-name`. Give each its own. |
| "GitHub Actions is not permitted to create or approve pull requests" | The [Read the Docs recipe](publishing.md#docs-built-outside-github-actions-read-the-docs-netlify) needs **Settings › Actions › General › Allow GitHub Actions to create and approve pull requests** turned on. The branch was pushed; once the setting is on, run the workflow again to open the pull request. |
| You want the next run to collect everything again, such as after a contributor renamed their account | Delete the `oss-chronicle-…` entry under **Actions → Caches**, or run once with `archive: false`. See [Finding and clearing it](configuration.md#finding-and-clearing-it). |
