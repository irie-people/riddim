# Working on Riddim

These rules apply to every agent and every contributor.

## Public sources only

- Build from public documentation, the tools' own `--help`, and public open-source code whose license allows it.
- Record every source that shapes the code in `PROVENANCE.md` (date, URL, what it was used for) in the same change. Third-party code also gets its origin and license listed there.
- Never open, search or copy from private codebases. If a local guard blocks something, stop and say what you needed. Don't work around it.

## No personal data

Nothing in the repository should identify a person: no names, usernames, emails, home paths, hostnames or employers in code, tests, fixtures, docs, logs or screenshots. Use `alice`, `/home/alice` and `example.com`.

## Commits

Commit in UTC: `TZ=UTC git commit`. Sign off every commit (`git commit -s`, the DCO).

## Stake

Contributions earn Steak under [riddim-hq/stake](https://github.com/riddim-hq/stake). Agree anything sizeable in an issue first, and say when an agent wrote part of a change.

## Stack

Go, standard library only. The compiler (`cmd/riddim`) checks `.rdm` files and emits Go. The plan, the language and the milestones are in `docs/DESIGN.md`. Test with `go vet ./...` and `go test -race ./...`.
