# Cold-start comparison

This directory holds the measurement behind the language table in the
[top-level README](../README.md#optimising-language): this repository's handler,
faithful ports of it to other languages, and the client that measured them.

## What was measured

Every implementation does exactly what the Go handler does on an application
command, in the same order:

1. Verify the Ed25519 signature over timestamp + body (`401` on failure)
2. Answer a `PING` with `{"type":1}`
3. Publish the raw body to a real Pub/Sub topic **and wait for the server's
   acknowledgement**
4. Return the deferred response, `{"type":5}`

Step 3 is the point. On a cold instance the first publish has to fetch a token
from the metadata server, open TLS and set up the client's channel, and that is
the most expensive thing the request does. The handler waits for it because,
with request-based billing, Cloud Run throttles the CPU once the response has
gone. The earlier
[discord-bot-test-suite](https://github.com/pmgledhill102/discord-bot-test-suite)
did not wait: most of its services published only to an emulator, and only
after responding.

Each port uses its language's official Google Pub/Sub client with that client's
default batching, and the language's usual HTTP stack:

| Service       | Source                                     | Runtime and HTTP stack                                  |
| ------------- | ------------------------------------------ | ------------------------------------------------------- |
| `go`          | the repository root                        | Go 1.27, functions framework, distroless/static         |
| `rust`        | [`services/rust`](services/rust)           | Rust 1.100, axum, `google-cloud-pubsub`, distroless/cc  |
| `rust-rest`   | [`services/rust-rest`](services/rust-rest) | Rust 1.100, axum, reqwest + `google-cloud-auth`         |
| `node`        | [`services/node`](services/node)           | Node.js 26, `node:http`, distroless/nodejs26            |
| `python`      | [`services/python`](services/python)       | Python 3.15, Flask on gunicorn (1 worker, 8 threads)    |
| `dotnet`      | [`services/dotnet`](services/dotnet)       | .NET 10, ASP.NET Core minimal API, chiseled             |
| `dotnet-aot`  | [`services/dotnet`](services/dotnet)       | the same project, Native AOT                            |
| `java`        | [`services/java`](services/java)           | Java 25, JDK HTTP server on virtual threads             |
| `java-aot`    | [`services/java`](services/java)           | the same jar with a JDK 25 AOT cache from a training run |
| `java-native` | [`services/java`](services/java)           | the same jar as a GraalVM 25 native image               |
| `java-spring` | [`services/java-spring`](services/java-spring) | Java 25, Spring Boot 4.2, Spring MVC on Tomcat      |

Every service was deployed identically, with the settings in
[`deploy/service.yaml`](../deploy/service.yaml): gen1, startup CPU boost,
request-based billing, 1 vCPU, 512 MiB, concurrency 80, scale to zero, no
explicit startup probe, in europe-west2.

## Method

- Five rounds on 2026-11-27, 09:32–11:10 UTC, at least 22 minutes apart, with
  no other traffic to any service in between.
- In each round every service received one cold request, all fired together,
  then five warm requests half a second apart.
- Each request was a signed application command shaped like a real one (about
  1 KB), sent on a fresh connection.
- **Latency is server-side**, taken from Cloud Run's request log, joined to each
  request by its `X-Cloud-Trace-Context`. It runs from Cloud Run receiving the
  request to the response, including any wait for an instance to start, and
  excludes the client's network path. The client's own timings are recorded too,
  but they ran through a sandbox proxy and carry around a second of overhead
  that says nothing about the services.
- **A cold request only counts if it was the first request its instance served**,
  according to the log. All 55 passed; none of the 330 requests failed.

The raw data is in [`results/`](results): `results.jsonl` holds every request
as the client saw it, and `requests.json` is the matching extract of the Cloud
Run request log.

### Results

<!-- Output of: go run ./bench/client report -results bench/results/results.jsonl -logs bench/results/requests.json -->

| Service     | Cold, server (median) | Cold, server (range) | Warm, server (median) | Cold, client (median) | Cold rounds confirmed | Failed requests |
| ----------- | --------------------: | -------------------: | --------------------: | --------------------: | --------------------: | --------------: |
| rust-rest   |                392 ms |           248–528 ms |                 22 ms |              1,529 ms |                5 of 5 |               0 |
| rust        |                470 ms |           287–922 ms |                 40 ms |              1,480 ms |                5 of 5 |               0 |
| go          |                609 ms |           293–835 ms |                 42 ms |              1,595 ms |                5 of 5 |               0 |
| dotnet-aot  |              1,465 ms |       1,346–1,943 ms |                 46 ms |              2,514 ms |                5 of 5 |               0 |
| node        |              1,657 ms |       1,480–1,959 ms |                 49 ms |              2,741 ms |                5 of 5 |               0 |
| java-native |              2,030 ms |       1,337–2,843 ms |                 38 ms |              3,023 ms |                5 of 5 |               0 |
| dotnet      |              2,968 ms |       2,605–3,344 ms |                 56 ms |              3,901 ms |                5 of 5 |               0 |
| java-aot    |              3,241 ms |       2,920–3,979 ms |                 46 ms |              4,332 ms |                5 of 5 |               0 |
| java        |              3,625 ms |       3,294–5,024 ms |                 48 ms |              4,652 ms |                5 of 5 |               0 |
| python      |              4,933 ms |       4,700–5,368 ms |                 47 ms |              6,032 ms |                5 of 5 |               0 |
| java-spring |              6,941 ms |       6,668–7,988 ms |                 42 ms |              8,011 ms |                5 of 5 |               0 |

### Things worth knowing before reading too much into it

- **Five samples per service.** Ranges overlap between neighbours. The ordering
  of the three tiers (Go and Rust; the native and AOT builds and Node; the JVM,
  .NET JIT, Python and Spring) is solid. The order within a tier is not.
- **Rust REST's warm advantage is mostly batching.** The Go and Rust gRPC
  clients wait up to 10 ms for a batch to fill before they send; the REST
  variant sends at once.
- **One region, one day, gen1.** gen2 and other regions will shift the absolute
  numbers; the earlier test suite saw a similar ordering on gen2 in europe-west1.

## Rust's dependencies

The concern going in was that Rust's small standard library would mean a long
tail of third-party crates. The SDK service declares six direct dependencies
and the REST variant eight. All of them are heavily used, and all but `hex` are
actively maintained:

| Crate                 | Version | Downloads | Last 90 days | Last release | Maintainer                  |
| --------------------- | ------- | --------: | -----------: | ------------ | --------------------------- |
| `tokio`               | 1.54.1  |    1,253M |         333M | 2026-10-21   | tokio-rs                    |
| `axum`                | 0.8.9   |      626M |         180M | 2026-04-14   | tokio-rs                    |
| `serde_json`          | 1.0.154 |    1,701M |         470M | 2026-10-11   | serde-rs                    |
| `ed25519-dalek`       | 3.0.0   |      293M |          95M | 2026-07-06   | dalek-cryptography          |
| `hex`                 | 0.4.3   |      846M |         229M | 2021-03-03   | KokaKiwi                    |
| `google-cloud-pubsub` | 1.8.0   |        9M |           2M | 2026-11-11   | Google (googleapis)         |
| `google-cloud-auth`   | 1.19.0  |       52M |          20M | 2026-11-11   | Google (googleapis)         |
| `reqwest`             | 0.13.5  |      964M |         288M | 2026-09-08   | seanmonstar (hyper)         |
| `base64`              | 0.23.1  |    2,082M |         587M | 2026-08-04   | marshallpierce              |

Figures from crates.io on 2026-11-27. The last three are the REST variant's.

- **`google-cloud-pubsub` is Google's own.** The crate name used to belong to a
  community client, which its author donated to Google; that client lives on as
  `gcloud-pubsub`. The official one is young (1.0.0 shipped on 2026-05-06), but it is
  generated and maintained alongside every other Google Cloud client library.
- **`hex` has not released since 2021.** It looks finished rather than abandoned:
  it does one small job and still sees 229M downloads in 90 days. It is also the
  easiest to drop: it decodes two hex strings, a dozen lines to write by hand
  if a dependency with no recent release is a concern.
- **Transitively the tree is larger than it looks.** The SDK variant resolves
  to 181 distinct crates and the REST variant to 147; most of the difference is
  the gRPC stack. For comparison, the Go deployable builds against 44 modules,
  Node installs 83 packages, Python 32 distributions and the Java services 56
  jars.

## Reproducing it

All of this runs against a project of your own; nothing here is tied to the
one used above. From the repository root:

```sh
export PROJECT_ID=my-project REGION=europe-west2

# One-off setup
gcloud pubsub topics create bench-interactions
gcloud iam service-accounts create bench-runtime    # grant it roles/pubsub.publisher on the topic
gcloud iam service-accounts create bench-builder    # see the comment in build.sh for its roles
gcloud artifacts repositories create bench --repository-format docker --location "$REGION"
gcloud storage buckets create "gs://$PROJECT_ID-bench-build" --location "$REGION"

# A throwaway signing key: the services trust its public half
go run ./bench/client keygen -key bench.key > bench.pub

bench/build.sh                                   # every image, in Cloud Build
PUBLIC_SIG_KEY=$(cat bench.pub) bench/deploy.sh  # every service, identical settings
go run ./bench/client smoke -key bench.key https://bench-go-<project-number>.$REGION.run.app/

# Then, at least 20 minutes after the last request, once per round:
PROJECT_NUMBER=<project-number> KEY=bench.key bench/round.sh round-1

# And at the end:
gcloud logging read 'resource.type="cloud_run_revision" AND logName:"run.googleapis.com%2Frequests" AND resource.labels.service_name:"bench-"' \
  --limit 10000 --format=json > logs.json
go run ./bench/client report -results bench/results/results.jsonl -logs logs.json
```
