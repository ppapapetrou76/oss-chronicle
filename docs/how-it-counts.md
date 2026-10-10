# How it counts

oss-chronicle counts finished work: one credit per pull request landed, per pull request reviewed, per merge. Activity trackers that count every GitHub event also score merge commits, rebases, authors replying on their own pull requests and the "closed" event GitHub fires on every merge. oss-chronicle leaves those out, and records a reason for every event it drops.

## What counts

| Unit | Credit |
|---|---|
| PRs landed | One per pull request that landed on the default branch, to its author: merged with the button or a merge queue, or closed by the commit that landed it (ghstack, mirror workflows). Replaces every commit, rebase and reply behind it. |
| PRs reviewed | One per reviewer per pull request, on someone else's pull request: a GitHub review, or an approval command such as `/lgtm`, `/approve` or `@bors r+`. Tracks separately whether the review left feedback. |
| Comments | Conversation comments on someone else's pull request, excluding bot commands. |
| Merges | The maintainer who merged. When a bot merged (Prow, bors, merge bots), the person whose merge command it acted on; with no merge command, the last person other than the author who approved before the merge, by approval command or approving review. The "closed" event from the same click is not counted. |
| Triage closes | Closing someone else's pull request without merging it. |
| Maintenance | Reviews and closes on someone else's maintenance pull request, and merges of any: dependency bumps, backports, and any other pull request a bot opened. Shown separately, outside the total. Opening one, its commits and comments on it are not credited. |

The total is PRs landed + PRs reviewed + comments + merges + triage closes, and it sets the default order.

Activity is filtered by its own date, so a review on the day before the window does not count, even when the pull request merged inside it. The same rule gives every [period](configuration.md#periods) its own ledger from one collection.

"Pull requests with activity" counts the pull requests with something dated in the period: opened or landed, or a commit, review, comment, close, force-push or review request. GitHub also marks a pull request updated for changes oss-chronicle does not read, such as labels, so the collected data holds more pull requests than this.

## Size weights

Two weighted columns sit beside the counts. They never replace the counts as the default sort.

- **Authoring weight** sums the size weight of each PR a person landed: XS (up to 10 counted lines) 0.5, S (up to 50) 1, M (up to 250) 2, L (more) 3.
- **Reviewing weight** sums the weight of each PR a person reviewed, doubled (by default) when the review left feedback.

Counted lines leave out lock files, vendored code, common generated files, and anything the repository's own `.gitattributes` marks `linguist-generated`, `linguist-vendored`, `-diff` or `binary` (macros included). Only the root `.gitattributes` on the default branch is read, and its current rules apply to every PR in the window.

## Components

Each file belongs to a component: the first [components map](configuration.md#components) entry whose paths match it, then its owners in `CODEOWNERS`, then its top-level directory. A pull request belongs to the component where most of its counted lines changed, so nothing is counted twice; every other component it changed lists it as touched. Rules in `CODEOWNERS` that match every file are ignored, and so are their owners when naming the others; only `@user` and `@org/team` owners are read. `CODEOWNERS` is read from `.github/`, the repository root or `docs/`, whichever comes first.

For each component, the run summary and `ledger.json` give:

- PRs landed, their authors, and the median time from opening to landing.
- Reviews per reviewer (the person × component grid), the top reviewer's share, and how few people did half the reviews. A component where one person does most of the reviewing has a hidden owner.
- The median time to a first response on non-draft PRs opened in the window, and how many never got one, including PRs their author closed first. A response is a review, comment, merge or close by anyone but the author and bots. A review request is not a response.
- Open, non-draft PRs nobody but the author has responded to, oldest first. These describe the repository as the run found it, so they are the same in every period. The data holds only pull requests updated since the earliest period starts, so an open PR with no activity at all since then is not listed.

People in `publish.opt_out` are left out of component names taken from `CODEOWNERS` and of the reviewer lists, and are never named as top reviewer, but their reviews still count in each component's totals and shares, as they do in the overall totals. PR titles and component names are escaped in the run summary, so they cannot add links, images or @mentions.

## What it leaves out, and why

Every activity that is not counted, apart from a bot's own comments and events, is recorded with a reason, and the run summary lists them:

- pull request branch commits, merge commits and force-pushes, replaced by one credit per landed pull request;
- the "closed" event every merge fires, and review requests;
- authors' reviews, comments and closes on their own pull requests;
- bot commands;
- pull requests that never merged;
- pull requests merged into a branch other than the default (stacked pull requests land later through their parent);
- merges done by a bot with no merge command or approval to credit them to;
- opening a backport or dependency pull request by hand, commits in maintenance pull requests, and comments on them. Reviews, merges and closes on maintenance pull requests are credited as maintenance instead, and the run summary marks those rows. A bot opening a pull request is not recorded, like the rest of a bot's own activity.

## Merge workflows

| Workflow | Example projects | How it is counted |
|---|---|---|
| Merge button | prometheus/prometheus | The merger gets the merge. |
| GitHub merge queue | open-telemetry/opentelemetry-collector | GitHub records the person who queued the pull request as the merger. |
| Prow (`/lgtm`, `/approve`) | kubernetes/kubernetes, kubernetes-sigs/kustomize | `/lgtm` and `/approve` count as reviews; the bot's merge goes to the last `/approve`. When an approver opens the pull request, Prow approves it itself and nobody types `/approve`; as with any bot merge without a merge command, the merge goes to the last `/lgtm` or approving review from someone other than the author. |
| bors (`@bors r+`) | rust-lang/rust | `r+` counts as a review and earns the merge. |
| Merge bot that pushes commits | pytorch/pytorch | The pull request is closed by a commit, not merged; it still counts as landed, and the merge goes to whoever ran the merge command. |
| Release branches | most projects | Pull requests into `release-*`, `release/*`, `stable-*`, `v1.2`-style branches are backports and go to maintenance. |
| Stacked pull requests | any | Reviews count; the pull request lands when its parent does. |

If your merge bot uses a command not in the list, add it to `comments.merge_commands`. A list you set replaces the default one, so copy the defaults from [Configuration](configuration.md#the-config-file) and append to them; dropping `/approve` would break Prow merge credit.

The default branch is the repository's default branch on GitHub, unless `default_branch` is set. When neither is known, as when computing from data collected without `meta.json`, it is the branch most pull requests target.
