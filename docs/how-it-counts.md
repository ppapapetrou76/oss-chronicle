# How it counts

oss-chronicle counts finished work: one credit per pull request landed, per pull request reviewed, per merge. Activity trackers that count every GitHub event also score merge commits, rebases, authors replying on their own pull requests and the "closed" event GitHub fires on every merge. oss-chronicle leaves those out, and records a reason for every event it drops.

## What counts

Each number on the web page and in the run summary, and the sentence both show for it:

| Number | How it is computed |
|---|---|
| Total | Landed + reviewed + comments + merges + triage. Maintenance and the weights are shown beside it and are not part of it. |
| Landed | Pull requests the person opened that landed on the default branch in the period: merged, or closed by the commit that landed them. One credit per pull request, replacing the commits, rebases and replies behind it. Maintenance pull requests are not counted. |
| Reviewed | Someone else's pull requests the person reviewed in the period, once per pull request however many reviews they left. A review is a GitHub review or an approval command. Maintenance pull requests are not counted. |
| Reviews with feedback | Reviewed pull requests where one of the person's reviews requested changes, left inline comments or had a written message. |
| Comments | Conversation comments on someone else's pull request in the period; inline review comments belong to the review. Bot and merge commands are not counted, and approval commands count as reviews. Maintenance pull requests are not counted. |
| Merges | Pull requests the person merged into the default branch in the period. When a bot merged, the credit goes to the person whose merge command it acted on, or with no command to the last person other than the author who approved. Maintenance pull requests are not counted. |
| Triage | Closes of someone else's pull request without merging it, in the period; each close counts. A pull request that landed later is not counted. Maintenance pull requests are not counted. |
| Maintenance | Each review and close on someone else's maintenance pull request, and each merge of one, in the period. A review is a GitHub review or an approval command. Maintenance pull requests are backports, dependency updates and pull requests a bot or a deleted account opened. Other comments on them are not counted. Not part of the total. |
| Authoring weight | The size weights of the person's landed pull requests, added up. A pull request's size comes from its counted lines, as the size table shows; one whose files could not be collected adds nothing. |
| Reviewing weight | The size weights of the pull requests the person reviewed, added up, with a weight multiplied when their review left feedback. A pull request whose files could not be collected adds nothing. |

The total sets the default order. A pull request closed by the commit that landed it counts as landed, which covers ghstack and mirror workflows. How each merge workflow is credited, from Prow to merge queues, is under [Merge workflows](#merge-workflows).

The web page and the run summary also list the rules the run counted with: which pull requests are backports or dependency updates, which accounts are bots, the approval, merge and bot commands, what counted lines leave out, the size buckets, the reviewing weight multiplier, and how files map to components. Rules the config sets differently from the defaults are marked "set by this project". Opted-out logins are never listed. Both link here, to the docs for the oss-chronicle version that computed the ledger; a workflow that uses `@main` gets `main`.

Activity is filtered by its own date, so a review on the day before the window does not count, even when the pull request merged inside it. The same rule gives every [period](configuration.md#periods) its own ledger from one collection.

"Pull requests with activity" counts the pull requests with something dated in the period: opened or landed, or a commit, review, comment, close, force-push or review request. GitHub also marks a pull request updated for changes oss-chronicle does not read, such as labels, so the collected data holds more pull requests than this.

## Size weights

Two weighted columns sit beside the counts. They never replace the counts as the default sort.

- **Authoring weight** sums the size weight of each PR a person landed: XS (up to 10 counted lines) 0.5, S (up to 50) 1, M (up to 250) 2, L (more) 3.
- **Reviewing weight** sums the weight of each PR a person reviewed, doubled (by default) when the review left feedback.

Counted lines leave out lock files, vendored code, common generated files, and anything the repository's own `.gitattributes` marks `linguist-generated`, `linguist-vendored`, `-diff` or `binary` (macros included). Only the root `.gitattributes` on the default branch is read, and its current rules apply to every PR in the window.

## Components

Each file belongs to a component: the first [components map](configuration.md#components) entry whose paths match it, then, when `use_codeowners` is on, its owners in `CODEOWNERS`, then its top-level directory. A pull request belongs to the component where most of its counted lines changed, so nothing is counted twice; every other component it changed lists it as touched. Rules in `CODEOWNERS` that match every file are ignored, and so are their owners when naming the others; only `@user` and `@org/team` owners are read. `CODEOWNERS` is read from `.github/`, the repository root or `docs/`, whichever comes first.

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
- opening a backport or dependency pull request by hand, commits in maintenance pull requests, and comments on them other than approval commands. Reviews, approval commands, merges and closes on maintenance pull requests are credited as maintenance instead, and the run summary marks those rows. A bot opening a pull request is not recorded, like the rest of a bot's own activity.

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
