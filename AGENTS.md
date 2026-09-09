# Project Instructions for AI Agents

A Discord interaction endpoint for GCP: it verifies each request's Ed25519
signature, publishes the interaction to Pub/Sub, and returns a deferred
acknowledgement inside Discord's 3 second deadline.

## Build & Test

```sh
go build ./...
go vet ./...
go test ./...
```

The unit tests need **no environment** — they construct a `Handler` directly and
generate their own keypair. That is deliberate and worth preserving: it is the
property that proves importing this package has no side effects.

One test, `TestPublishesToTopic`, exercises the real Pub/Sub publisher. It skips
itself when `PUBSUB_EMULATOR_HOST` is unset, and **fails** when `CI` is set and
the emulator is not — a test that opts itself out silently is a test that can
stop covering anything. To run it locally you need an emulator:

```sh
PUBSUB_EMULATOR_HOST=127.0.0.1:8085 go test ./...
```

`.github/workflows/test.yml` starts one with `docker run`, not as a `services:`
container — a service container's `options:` are passed to `docker create` as
flags rather than as the command, so the emulator arguments were parsed as an
image name.

## Architecture Overview

Two halves of one package, split on purpose:

- **`discordbot.go`** — the library. `Config`, `New`, `Handler`,
  `ConfigFromEnv`. No package-level state, no side effects, and `Handler` is an
  ordinary `http.Handler`.
- **`function.go`** — the Google Cloud functions framework registration. It
  stays in the **root package** because Cloud Functions is deployed with
  `--entry-point handleDiscordMessage` and the Go buildpack looks for that
  registration in the module's root package. Moving it breaks the documented
  deploy recipe.
- **`cmd/main.go`** — the deployable. It builds the handler *before* serving so
  a misconfigured revision never takes traffic.

The repository root is `package discordbot`, a library rather than a `main`,
which is why the container build targets `./cmd` and the buildpack route needs
`GOOGLE_BUILDABLE=./cmd`.

## Conventions & Patterns

**Nothing on the request path may call `log.Fatalf`, `os.Exit` or `panic`.**
This is the single most important rule in the repo. The endpoint is necessarily
reachable without authentication — Discord cannot present a Google identity, so
the signature check is the only thing in front of it — and exiting on bad input
hands anyone on the internet a way to kill the process. See #49, which was
exactly that bug.

`log.Fatalf` in `init()` and in `cmd/main.go` is correct and should stay:
failing fast at startup means a misconfigured revision never serves.

**An invalid signature must return `401`.** Discord requires it, and runs
automated checks that purposefully send invalid signatures — failing them costs
the registered interactions URL. It is a conformance requirement, not just
robustness.

**Two artefacts, one version.** The repo publishes a container image *and* a Go
module, and the tag covers both, so breaking either is breaking. Below 1.0.0 the
MINOR position is the breaking one; see "Releases and versioning" in the README
before choosing a version.

**Tests assert response bodies, not only status codes**, wherever two branches
can produce the same status. A `400` from the malformed-body branch and a `400`
from the unhandled-type branch are not the same thing, and a test that cannot
tell them apart guards nothing.
