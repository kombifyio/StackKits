# Contributing to kombify StackKits

Thanks for helping. This page says how a contribution reaches a release and
what to expect from us.

## How this repository works

`kombifyio/stackKits` is the public release mirror of kombify StackKits. Its source of
truth is a private repository, and each release replaces the tree here with a
curated export. Your pull request is still the way in:

1. We review it here, without running your code on our infrastructure first.
2. If we take it, we import the change into the source with your authorship
   kept. Changes to build configuration or to security-sensitive parts we
   re-implement ourselves and credit to you.
3. When the release that carries it is published, we close your pull request
   with the commit and the version, and the release notes credit you by handle.

If we do not take it, we say why on the pull request.

## Before you start

- **Fixes:** open an issue first, or link one.
- **New features:** start with an idea in [Discussions](https://github.com/kombifyio/stackKits/discussions) so
  we can say whether it fits before you spend time.
- **Small changes** (typos, docs): open a pull request directly.

## Sign off your commits (DCO)

Every commit carries a sign-off line that certifies the
[Developer Certificate of Origin](https://developercertificate.org/), version
1.1: you wrote the change or have the right to submit it under the licence of
this repository ([LICENSING.md](LICENSING.md)). Add the line with
`git commit -s`:

```text
Signed-off-by: Your Name <you@example.com>
```

Use your real name. If you forgot, `git rebase --signoff <base>` adds the line to
every commit of your branch, and you force-push the result to your own branch.
We ask for a missing sign-off on the pull request before we review further.

## Rules

- Follow the [Code of Conduct](CODE_OF_CONDUCT.md).
- Sign off every commit, as above.
- Say which AI tool helped, if any. A person who can explain the change is
  accountable for it.
- No secrets, credentials, personal data or private hostnames in a diff.
- Draft pull requests are welcome for early feedback.

## What to expect

| Step | Target |
| --- | --- |
| First response | One business day |
| Verdict (take, change, or decline with reason) | Three business days |
| Release | The next release after import; see the [changelog](https://docs.kombify.io/changelog/overview) |

These are targets, not contractual promises.

## Local development

StackKits is built around CUE contracts and Go code. Build and validate the part
you changed:

```sh
cue vet -c=false ./foundation/...
cue vet ./basement-kit/... ./cloud-kit/... ./modern-homelab/...
```

When you change generated rollout output, update the CUE or Go source and
regenerate instead of patching generated files directly.

## Reporting

- Bugs: the [bug form](https://github.com/kombifyio/stackKits/issues/new?template=bug_report.yml) in this repository.
- Ideas: [Discussions](https://github.com/kombifyio/stackKits/discussions).
- Questions: see [SUPPORT.md](SUPPORT.md).
- Vulnerabilities: privately, see [SECURITY.md](SECURITY.md).
