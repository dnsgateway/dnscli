# Contributing

## Pull Requests

### Creating Pull Requests

Use `.github/PULL_REQUEST_TEMPLATE.md` as the structure for every PR description.

Every PR must include:

- **What?** — a clear description of the changes made.
- **Why?** — the reasoning or motivation, linking the related issue when applicable.
- **Acceptance checklist** — complete the checklist items truthfully:
  - [ ] linked an issue (if applicable)
  - [ ] included tests (if applicable)
  - [ ] ran conformance (`make conformance`)
  - [ ] linted code (`make lint`)
  - [ ] ran unit tests (`make unit`)
  - [ ] disclosed AI usage honestly

### Maintaining Pull Requests

- Address review feedback by force-pushing to the PR branch.
- Update the PR description when the scope of the change evolves so it always reflects the current diff.
- Rebase onto the target branch when requested to resolve conflicts; squash commits into a single commit.
- Ensure CI is green before requesting re-review.

All PRs require a single commit.

Having one commit in a Pull Request is very important for several reasons:
* A single commit per PR keeps the git history clean and readable.
  It helps reviewers and future developers understand the change as one atomic unit of work,
  instead of sifting through many intermediate or redundant commits.
* One commit is easier to cherry-pick into another branch or to track in changelogs.
* Squashing into one meaningful commit ensures the final PR only contains what matters.

### Commit Messages and PR Titles

Use [Conventional Commits 1.0.0](https://www.conventionalcommits.org/en/v1.0.0/)
for commit messages and PR titles:

```text
<type>[optional scope][!]: <description>
```

Keep the entire header lowercase, including the type, scope, and description.
This lowercase requirement is a repository convention. Use `feat` for new
features, `fix` for bug fixes, and an appropriate type such as `docs`, `test`,
or `refactor` for other changes. Use a scope to identify the affected area
when helpful, and keep the PR title aligned with its single commit's header.

Put an optional body and footers after a blank line. Mark breaking changes
with `!` before the colon or an uppercase `BREAKING CHANGE:` footer.
Preserve the DCO `Signed-off-by:` footer; the lowercase rule applies only to the header.

## GitHub Issues

### Creating Issues

- Use the templates in `.github/ISSUE_TEMPLATE/` when creating issues:
  - `BUG_REPORT.md` — for reporting bugs.
  - `FEATURE_REQUEST.md` — for proposing new features or enhancements.
- Every issue must have:
  - A clear, descriptive **title**.
  - A concise **description** of the problem or request.
  - **Steps to reproduce** for bugs.
- Fill in the body, do not remove the "Community Note" section.
- Link related issues or PRs in the description when applicable.

### Maintaining Issues

- Keep issue descriptions up to date as new information is gathered (reproduction steps, environment details, error logs).
- Add follow-up comments with new findings, test results, or workarounds instead of editing over important history.
- Close issues once resolved, and reference the PR or commit that fixed them.
- Reopen issues only if the fix is reverted or proven incomplete.

## Developer Certificate of Origin

All commits require a [DCO](https://developercertificate.org/) sign-off.
This is done by committing with the `--signoff` flag.
