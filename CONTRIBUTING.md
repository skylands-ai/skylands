# Contributing to Skylands

Issues and pull requests are welcome. Read the [code of conduct](CODE_OF_CONDUCT.md) before you take part, and see the [documentation](https://docs.skylands.ai) for how Skylands works.

## Asking Questions

Ask how to do something, share what you've built, or talk through an idea in [Discussions](https://github.com/skylands-ai/skylands/discussions). Open an issue for a bug or a concrete feature request.

## Reporting Security Issues

Don't open a public issue for a vulnerability. Report it privately from the repository's [Security tab](https://github.com/skylands-ai/skylands/security/advisories/new). [SECURITY.md](SECURITY.md) has the details.

## Reporting Other Issues

Search the [existing issues](https://github.com/skylands-ai/skylands/issues) before you open one. A report should include:

- what you did, what you expected, and what happened instead
- the output of `skylands version`
- the output of `skylands preflight`, for anything involving agents or a daemon
- the log lines around the problem, from the terminal running `up`, or from the service's log (see [Run as a Service](https://docs.skylands.ai/operations/service/))

Remove NATS credentials, API keys, and anything from private repositories before you post logs or transcripts. To add to an existing report, post details that help reproduce it rather than a "+1".

## Proposing Changes

Open an issue before you start on a new feature, a larger refactor, or a change to the event types in `api/v1` or the NATS subjects. The subjects and the event types are the system's public API, so changes to them need agreement first. Describe the problem, your approach, and the alternatives you considered.

Small fixes, documentation corrections, and tests don't need an issue first.

## Development Setup

You need Go 1.26 and Node 22.23.4 or newer. `make lint` also needs [golangci-lint](https://golangci-lint.run/docs/welcome/install/).

| Command | What it does |
| --- | --- |
| `make web` | Builds the web client into `web/dist`, which the Go build embeds |
| `make build` | Builds `bin/skylands` with the web client built in |
| `make install` | Installs `skylands` into your Go bin directory |
| `make test` | Runs every Go test, including the whole-world test in `test/e2e` |
| `make lint` | Runs `go vet` and golangci-lint |
| `make docs` | Builds the documentation site into `docsite/dist` |
| `make snapshot` | Builds every release archive into `dist/` with goreleaser, publishing nothing |

Run `make web` before `make build` or `make install`. A binary built without `web/dist` serves a page saying the client is missing.

For work on the web client, `cd web && npm run dev` serves it with live reload, and proxies the WebSocket and the API to a citadel on port 8080. The dev server has an address of its own, and a browser's approver key belongs to one address. To approve from the dev server, open an "Add an approver" link with its host changed to the dev server's.

## Where Things Live

| Path | What's there |
| --- | --- |
| `cmd/skylands` | The binary's entry point |
| `internal/cli` | The commands |
| `internal/citadel` | The citadel: the web server, projections, and relaying decisions |
| `internal/daemon` | The daemon: sessions, agents over ACP, and the local socket |
| `internal/rules` | The rule language and how rules combine |
| `internal/service` | Installing systemd and launchd services |
| `internal/fakeagent` | A scripted ACP agent that tests run instead of a real one |
| `api/v1` | Event types and subjects |
| `web` | The browser client, in TypeScript with three.js |
| `docsite` | [docs.skylands.ai](https://docs.skylands.ai), built with Starlight |
| `docs` | Design notes, such as `DESIGN.md` and `NATS.md` |
| `test/e2e` | Tests that run a whole world in one process |

## Pull Requests

Keep each pull request to one change. Its description should say what changed, and what you ran to check it.

Include tests with your change. Tests use Go's `testing` package. Agent behavior is tested against `internal/fakeagent`, so a change to how the daemon talks to agents usually needs the fake agent to cover it too.

CI runs on every pull request:

- `go vet ./...` and `go test -race ./...`
- golangci-lint, with the repository's `.golangci.yml`
- the web client's type check, build, and tests (`npm run build` and `npm test` in `web/`)
- the documentation build, when the pull request changes `docsite/`

Update the pages in `docsite/` when you change something a user sees or runs. Write them plainly: say what the reader needs to do or know, use contractions, and leave out how things came to be the way they are.

A pull request needs a maintainer's approval and passing checks to merge.

## Commit Messages and Titles

Pull requests are squash-merged, and the title becomes the commit's subject. Write the title as a short sentence in the imperative that says what the change does for someone using Skylands, without a trailing period:

```text
Say how to install with Homebrew
Remove what ended sessions leave behind, from the citadel
Make devworld's log readable at a glance
```

The description becomes the commit's body. Explain the change there, and link related issues with a keyword such as `Fixes #43` where it applies.

## Review

Reply to feedback, push the changes you agreed on, and say briefly what changed and which checks you ran. You can merge `main` into your branch or rebase on it, since the pull request is squashed either way. If you rewrite a branch you've pushed, use `git push --force-with-lease`.

## AI-Assisted Contributions

You can use AI tools to write code, tests, and documentation. Review everything before you submit it. You're responsible for an AI-assisted contribution the same way as for one you wrote by hand, including its correctness, its tests, and your right to contribute it.

## Coding Style

Follow the conventions of the package you're changing, [Effective Go](https://go.dev/doc/effective_go), and [Go Code Review Comments](https://go.dev/wiki/CodeReviewComments). Leave unrelated cleanup out of a focused change.

- Format Go with `gofmt`. Lint fails on code it would change.
- Give exported identifiers doc comments that say what they do and what callers need to know.
- Use comments for intent and constraints the code doesn't show, not to narrate it.
- Keep code with the package that owns its concern, rather than in a general `utils` package.
- In `web/`, the TypeScript has to pass `tsc --noEmit`, which `npm run build` runs.

## Releases

Maintainers release by pushing a version tag, such as `v0.1.0`. The release workflow builds the archives, uploads them to a draft release, runs `scripts/release-smoke.sh` on the Linux binary, and publishes the release only if that passes. It then updates the cask in [skylands-ai/homebrew-tap](https://github.com/skylands-ai/homebrew-tap), and the tag publishes the documentation site. The smoke test runs on any binary:

```sh
scripts/release-smoke.sh dist/skylands_linux_amd64_v1/skylands
```

## License

Skylands is licensed under the [Apache License 2.0](LICENSE). A contribution you submit is licensed under it too, as section 5 of the license describes.
