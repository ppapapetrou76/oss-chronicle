# Publishing the results

oss-chronicle can stay inside your Actions runs, get a web page of its own, or become one page of the documentation site you already have. This page covers all three, and what to settle with your contributors first.

## Before you publish

A contribution ledger ranks named people. Settle these points before anyone outside the maintainers sees it:

- **The run summary is already public on a public repository.** Anyone can open the Actions tab, so the first run publishes the people table there, whether or not you set up a page.
- **Tell your contributors first, and say how to opt out.** People listed in `publish.opt_out` are left out of the summary, the page, the CSV files and `ledger.json`, including component names taken from `CODEOWNERS`. Their work still counts in the totals and in each component's shares. Pull requests they opened can still appear, without their name, among those waiting for a response. A simple way to let people opt out is to accept a pull request that adds their login to the list.
- **Opt-out does not touch the collected data.** The artifact's `prs.jsonl.gz` holds every pull request, review and comment as GitHub returned them, logins included. That is already public on GitHub, but if you copy files out of the artifact, copy only `site/`.
- **Component names you choose are published as written.** Opted-out people are removed from component names taken from `CODEOWNERS`, but not from names in your [components map](configuration.md#components).
- **Only GitHub logins appear.** oss-chronicle never asks GitHub for commit emails or real names.
- **A Pages site is public** even when the repository is private, unless your organization uses GitHub Enterprise Cloud's access control for Pages.

## Choose where the results go

| | Where | Setup | Use it when |
|---|---|---|---|
| 1 | The run summary and the artifact | None | You are trying it out, or the maintainers are the only audience. |
| 2 | A page of its own on GitHub Pages, at `https://<owner>.github.io/<repo>/` | Enable Pages once, add a deploy job | The repository does not use GitHub Pages yet. |
| 3 | A page in your existing docs site, such as `/contributors/` | Copy one folder in your docs workflow | You already publish documentation. |

## 1. The run summary only

This is what the workflow in [Getting started](getting-started.md) does. There is nothing more to set up.

## 2. A page of its own on GitHub Pages

> [!WARNING]
> A repository has one Pages site. If yours already publishes documentation to GitHub Pages, this deploy replaces it. Use option 3 instead.

**Enable Pages once.** A repository admin opens **Settings › Pages** and sets **Build and deployment › Source** to **GitHub Actions**. The workflow cannot do this itself: the job token has no permission to turn Pages on. Until someone does, the deploy job fails with "Ensure GitHub Pages has been enabled". On GitHub Free, Pages works only for public repositories.

**Add the deploy job.** Extend the workflow from [Getting started](getting-started.md), keeping its top-level `permissions`:

```yaml
jobs:
  ledger:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v7
      - uses: ppapapetrou76/oss-chronicle@main
        id: ledger
      - uses: actions/upload-pages-artifact@v5
        with:
          path: ${{ steps.ledger.outputs.site }}
  pages:
    needs: ledger
    runs-on: ubuntu-latest
    permissions:
      pages: write
      id-token: write
    environment:
      name: github-pages
      url: ${{ steps.deploy.outputs.page_url }}
    steps:
      - uses: actions/deploy-pages@v5
        id: deploy
```

Only the deploy job gets write permissions; oss-chronicle itself still only reads. The page is refreshed on every scheduled run.

## 3. A page in your existing docs site

The page is one self-contained `index.html` plus copies of `ledger.json` and `periods.json`. It loads nothing from other sites and uses no paths of its own, so it works from any folder of any site. Your docs build only has to copy the folder unchanged.

The pattern is the same for every docs tool: run oss-chronicle in its own job, then in your docs job download its artifact and copy `site/` into your docs source before you build.

### MkDocs

Copy the folder to `docs/contributors/` and add it to the nav. MkDocs copies HTML files in `docs/` unchanged, and a nav entry may point at one:

```yaml
# mkdocs.yml
nav:
  - Home: index.md
  - Contributors: contributors/index.html
```

From a Markdown page, link to it as `[contributors](contributors/index.html)`. Both links work with `mkdocs build --strict`.

A complete workflow that builds an MkDocs site and publishes it to GitHub Pages:

```yaml
name: Docs
on:
  push:
    branches: [main]
  schedule:
    - cron: '17 4 * * *'
  workflow_dispatch:
permissions:
  contents: read
  pull-requests: read
concurrency:
  group: docs
  cancel-in-progress: false
jobs:
  ledger:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v7
      - uses: ppapapetrou76/oss-chronicle@main
  docs:
    needs: ledger
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v7
      - uses: actions/download-artifact@v8
        with:
          name: oss-chronicle
          path: ${{ runner.temp }}/chronicle
      - uses: actions/setup-python@v7
        with:
          python-version: '3.x'
      - run: pip install mkdocs-material
      - run: |
          rm -rf docs/contributors
          cp -R "$RUNNER_TEMP/chronicle/site" docs/contributors
      - run: mkdocs build --strict
      - uses: actions/upload-pages-artifact@v5
        with:
          path: site
  pages:
    needs: docs
    runs-on: ubuntu-latest
    permissions:
      pages: write
      id-token: write
    environment:
      name: github-pages
      url: ${{ steps.deploy.outputs.page_url }}
    steps:
      - uses: actions/deploy-pages@v5
        id: deploy
```

Add `docs/contributors/` to `.gitignore` so nobody commits a stale copy. The ledger job runs on every push to `main` as well as on the schedule; on a large repository it takes several minutes (see [Getting started](getting-started.md#2-run-it)).

### Docusaurus

Put the folder in `static/contributors/`; Docusaurus copies `static/` to the root of the built site unchanged. Link to it with the `pathname://` prefix, which marks the target as a static file outside Docusaurus's own pages:

```js
// docusaurus.config.js, themeConfig.navbar.items
{href: 'pathname:///contributors/', label: 'Contributors', position: 'left', target: '_self'},
```

In Markdown pages, write `[contributors](pathname:///contributors/)`. Docusaurus adds your `baseUrl` to both.

> [!IMPORTANT]
> A plain `to: '/contributors/'` link fails the build with "Docusaurus found broken links", because Docusaurus checks links against its own pages.

In the workflow above, replace the Python steps with your Node setup and build, and copy to `static/contributors` instead of `docs/contributors`.

### Other static site generators

Each generator has a place for files it copies unchanged: `static/` for Hugo, any folder whose name does not start with `_` for Jekyll, and a folder listed in `html_extra_path` for Sphinx. These have not been tested yet; MkDocs and Docusaurus have.

### Docs built outside GitHub Actions (Read the Docs, Netlify)

Read the Docs, Netlify and similar services build from your repository and cannot reach a workflow's artifacts, so the page has to be committed. This workflow runs oss-chronicle once a week and opens a pull request that updates `docs/contributors/`. Merging it publishes the page with your next docs build.

Before the first run:

- **Let workflows open pull requests:** turn on **Settings › Actions › General › Allow GitHub Actions to create and approve pull requests**. Organizations can lock this setting; ask an organization owner, or use a GitHub App token instead of `github.token` (see below; not tested yet).
- **Add the page to your docs.** For MkDocs, add `- Contributors: contributors/index.html` to the nav, as in the [MkDocs](#mkdocs) section. For Sphinx, copy to a folder listed in `html_extra_path` instead of `docs/contributors` (not tested yet). GitHub Pages sites built from a branch with Jekyll need nothing: the page has no front matter, so Jekyll publishes it unchanged (at `contributors/` instead if the site is built from the `docs` folder).
- **Remove `docs/contributors/` from `.gitignore`** if you added it for the Actions recipes. The workflow adds the files with `--force`, but an ignored folder confuses people editing it locally.

```yaml
name: Contributors page
on:
  schedule:
    - cron: '17 4 * * 1'   # weekly, on Mondays
  workflow_dispatch:
permissions:
  contents: read
concurrency:
  group: contributors-page
  cancel-in-progress: false
jobs:
  ledger:
    runs-on: ubuntu-latest
    permissions:
      contents: read
      pull-requests: read
    steps:
      - uses: actions/checkout@v7
        with:
          persist-credentials: false
      - uses: ppapapetrou76/oss-chronicle@main
  propose:
    needs: ledger
    runs-on: ubuntu-latest
    permissions:
      contents: write
      pull-requests: write
    env:
      BRANCH: oss-chronicle/contributors-page
      GH_TOKEN: ${{ github.token }}
    steps:
      - uses: actions/checkout@v7
        with:
          ref: ${{ github.event.repository.default_branch }}   # the default branch, also when started by hand from another one
      - uses: actions/download-artifact@v8
        with:
          name: oss-chronicle
          path: ${{ runner.temp }}/chronicle
      - name: Copy the page
        run: |
          mkdir -p docs/contributors
          cp "$RUNNER_TEMP/chronicle/site/index.html" "$RUNNER_TEMP/chronicle/site/ledger.json" docs/contributors/
          git add --force docs/contributors/index.html docs/contributors/ledger.json
      - name: Open or update the pull request
        run: |
          if git diff --cached --quiet -I 'Generated by <a'; then
            echo "The contributors page has not changed."
            exit 0
          fi
          base=$(git branch --show-current)
          if git fetch --quiet --depth=1 origin "$BRANCH" 2>/dev/null; then
            branch_exists=true
          fi
          if [[ -n "${branch_exists:-}" ]] && git diff --cached --quiet -I 'Generated by <a' FETCH_HEAD -- docs/contributors; then
            echo "The pull request branch already has this page."
          else
            tree=$(jq --null-input --arg base "$(git rev-parse 'HEAD^{tree}')" \
                --rawfile html docs/contributors/index.html --rawfile ledger docs/contributors/ledger.json \
                '{base_tree: $base, tree: [
                  {path: "docs/contributors/index.html", mode: "100644", type: "blob", content: $html},
                  {path: "docs/contributors/ledger.json", mode: "100644", type: "blob", content: $ledger}]}' |
              gh api "repos/$GITHUB_REPOSITORY/git/trees" --input - --jq .sha)
            commit=$(jq --null-input --arg tree "$tree" --arg parent "$(git rev-parse HEAD)" \
                --arg message $'docs: update the contributors page\n\nSigned-off-by: github-actions[bot] <41898282+github-actions[bot]@users.noreply.github.com>' \
                '{message: $message, tree: $tree, parents: [$parent]}' |
              gh api "repos/$GITHUB_REPOSITORY/git/commits" --input - \
                --jq 'if .verification.verified then .sha else error("GitHub did not sign the commit: \(.verification.reason)") end')
            if [[ -n "${branch_exists:-}" ]]; then
              gh api --method PATCH "repos/$GITHUB_REPOSITORY/git/refs/heads/$BRANCH" -f sha="$commit" -F force=true --silent
            else
              gh api "repos/$GITHUB_REPOSITORY/git/refs" -f ref="refs/heads/$BRANCH" -f sha="$commit" --silent
            fi
          fi
          if [[ -z "$(gh pr list --head "$BRANCH" --state open --json number --jq '.[].number')" ]]; then
            gh pr create --base "$base" --head "$BRANCH" --title 'docs: update the contributors page' \
              --body 'Weekly update of the contributors page generated by [oss-chronicle](https://github.com/ppapapetrou76/oss-chronicle). Each run replaces this branch, so commits added to it by hand are lost on the next run.'
          fi
```

How it behaves:

- **Two jobs.** oss-chronicle runs with read-only permissions. Only the second job, which runs no third-party code apart from GitHub's own actions, can write to the repository and open pull requests.
- **One pull request at a time.** Each run rebuilds the `oss-chronicle/contributors-page` branch from the default branch and moves the branch to the new commit, so an open pull request is updated rather than joined by a new one. After you merge it, the next change opens a new one.
- **Re-runs add nothing.** The page's dates move every day, so each weekly run updates it. Running the workflow again the same day adds no commit, but still opens the pull request if there is none, for example after you turn on the setting above.
- **Only `index.html` and `ledger.json` are committed.** The page has every period built in and needs neither file next to it; `ledger.json` is kept because the page names it for the full waiting lists. Every update adds both files to the repository's history again, which is one reason the schedule is weekly.
- **Signed commits.** The commit is created through GitHub's API rather than pushed, so GitHub signs it, which is what branch protection's **Require signed commits** checks. The run stops before moving the branch if GitHub did not sign it.
- **`Signed-off-by`** in the commit message is for projects that require the DCO. Remove that line if yours does not.

> [!WARNING]
> Pull requests opened or updated with `github.token` do not start other workflows, so required checks run by GitHub Actions stay pending and block the merge. Closing and reopening the pull request starts them for its current commit; the next weekly update leaves them pending again. To avoid that, create the pull request with a [GitHub App token](https://github.com/actions/create-github-app-token) instead: generate it in the `propose` job and set `GH_TOKEN` to it. Commits made with an App token are authored by the App's bot account, so change the `Signed-off-by` line to match it. Checks from outside services can still run: CircleCI ran on the workflow's pull request in testing.

The workflow was tested on GitHub with an MkDocs build of its branch, and on a public repository whose GitHub Pages site is built by Jekyll from the default branch, where merging the pull request published the page. That repository requires signed commits, and GitHub reported the workflow's commit as signed and valid. Its pull request was merged with an administrator override, because the repository's other required checks fail or never report, so it is not yet shown that a branch requiring signed commits accepts the merge. A Read the Docs or Netlify build of the merged page was not tested. Read the Docs builds every version from its own branch or tag. Only the version built from the default branch, usually `latest`, gets the weekly updates; a release branch keeps the page as it was when the branch was cut.

## Linking to a period

The page opens on the default period. Add `?period=<id>` to its address to open another, for example `contributors/?period=last-quarter`; choosing a period in the picker does the same, so the address can be shared as it is.

## Rebuilding the page

To rebuild the page from a `ledger.json` without collecting again, from a clone of oss-chronicle:

```bash
go run ./cmd/oss-chronicle page --ledger ledger.json --periods periods.json --out site
```

## GitHub Enterprise Server

The page links people and pull requests to `GITHUB_SERVER_URL`, so the links point at your server. Check that your server's Pages supports `deploy-pages@v5` before using option 2.
