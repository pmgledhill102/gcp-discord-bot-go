# Language cold-start comparison, November 2026

The measurement behind the language table in the
[README](../README.md#optimising-language). It answers one question: if this
handler were written in another language, would it still answer Discord inside
3 seconds from a cold start?

The code that produced these figures (eleven services, the build and deploy
scripts, the measuring client) and the raw data are kept at commit
[`d291e0c`](https://github.com/pmgledhill102/gcp-discord-bot-go/tree/d291e0c/bench),
under `bench/`. They belong with the wider
[discord-bot-test-suite](https://github.com/pmgledhill102/discord-bot-test-suite),
not in this repository, which is a Go library.

## What was measured

Every implementation did what this repository's handler does with an
application command, in the same order:

1. Verify the Ed25519 signature over timestamp + body, `401` on failure
2. Answer a `PING` with `{"type":1}`
3. Publish the raw body to a real Pub/Sub topic **and wait for the server's
   acknowledgement**
4. Return the deferred response, `{"type":5}`

Step 3 is what matters. On a cold instance the first publish has to fetch a
token from the metadata server, open TLS and set up the client's channel, and
that is the most expensive thing the request does. The handler waits for it
because, with request-based billing, Cloud Run throttles the CPU once the
response has gone.

The archived test suite did not wait. Most of its services published to Pub/Sub
only against an emulator, fire-and-forget after responding, and its Rust
service never published on Cloud Run at all. Its cold starts therefore left out
step 3, which is why its numbers are lower than these and why it was not simply
re-run.

Each port used its language's official Google Pub/Sub client, with that client's
default batching, and the language's usual HTTP stack:

| Service       | Runtime and HTTP stack                                                  |
| ------------- | ----------------------------------------------------------------------- |
| `go`          | This repository's own image: Go 1.27, functions framework, distroless   |
| `rust`        | Rust 1.100, axum, Google's `google-cloud-pubsub`, distroless/cc         |
| `rust-rest`   | Rust 1.100, axum, reqwest + Google's `google-cloud-auth`, REST publish  |
| `node`        | Node.js 26, `node:http`, distroless/nodejs26                            |
| `python`      | Python 3.15, Flask on gunicorn (1 worker, 8 threads)                    |
| `dotnet`      | .NET 10, ASP.NET Core minimal API, chiseled                             |
| `dotnet-aot`  | The same project, Native AOT                                            |
| `java`        | Java 25, JDK HTTP server on virtual threads                             |
| `java-aot`    | The same jar, with a JDK 25 AOT cache from a build-time training run    |
| `java-native` | The same jar, as a GraalVM 25 native image                              |
| `java-spring` | Java 25, Spring Boot 4.2, Spring MVC on Tomcat                          |

All eleven passed the same smoke checks before measuring: bad signature → 401,
ping → pong, malformed body and unhandled type → distinct 400s, and command →
deferred response plus a message on the topic.

Every service was deployed identically, with the settings in
[`deploy/service.yaml`](../deploy/service.yaml): gen1, startup CPU boost,
request-based billing, 1 vCPU, 512 MiB, concurrency 80, scale to zero, no
explicit startup probe, in europe-west2.

## Method

- Five rounds on 2026-11-27, 09:32–11:10 UTC, at least 22 minutes apart, with no
  other traffic to any service in between.
- In each round every service received one cold request, all fired together,
  then five warm requests half a second apart: 5 cold and 25 warm per service.
- Each request was a signed application command shaped like a real one (about
  1 KB), sent on a fresh connection.
- **Latency is server-side**, from Cloud Run's request log, joined to each
  request by its `X-Cloud-Trace-Context`. It runs from Cloud Run receiving the
  request to the response, including any wait for an instance to start, and
  excludes the caller's network path. Discord's own round trip comes on top.
- **A cold request only counts if it was the first request its instance
  served**, according to the log. All 55 passed. None of the 330 requests
  failed.

## Results

Ordered by the slowest cold start, because that is what decides whether a
language is safe: Discord gives up at 3 seconds and does not retry.

| Service     | Cold, worst | Cold, median | Cold, best | Warm, median | Warm, worst | Image  |
| ----------- | ----------: | -----------: | ---------: | -----------: | ----------: | -----: |
| rust-rest   |      528 ms |       392 ms |     248 ms |        22 ms |      427 ms |  14 MB |
| go          |      835 ms |       609 ms |     293 ms |        42 ms |      854 ms |   8 MB |
| rust        |      922 ms |       470 ms |     287 ms |        40 ms |      629 ms |  14 MB |
| dotnet-aot  |    1,943 ms |     1,465 ms |   1,346 ms |        46 ms |      809 ms |  18 MB |
| node        |    1,959 ms |     1,657 ms |   1,480 ms |        49 ms |      741 ms |  69 MB |
| java-native |    2,843 ms |     2,030 ms |   1,337 ms |        38 ms |      699 ms |  50 MB |
| dotnet      |    3,344 ms |     2,968 ms |   2,605 ms |        56 ms |      610 ms |  57 MB |
| java-aot    |    3,979 ms |     3,241 ms |   2,920 ms |        46 ms |      743 ms | 132 MB |
| java        |    5,024 ms |     3,625 ms |   3,294 ms |        48 ms |      424 ms | 120 MB |
| python      |    5,368 ms |     4,933 ms |   4,700 ms |        47 ms |      283 ms |  76 MB |
| java-spring |    7,988 ms |     6,941 ms |   6,668 ms |        42 ms |       89 ms | 139 MB |

Image sizes are compressed, as stored in Artifact Registry.

### Reading it

- **Only Go and Rust have real headroom.** Even their slowest cold start left
  more than two seconds spare. Rust is a little quicker, by a hundred or two
  milliseconds, which is not a reason to switch from Go.
- **Native compilation roughly halves the managed runtimes' cold start, and is
  still not enough.** GraalVM and .NET Native AOT land at 1.5–2 seconds typical.
  GraalVM's slowest came within 0.2 seconds of the deadline before any network
  time, and the .NET JIT build went over it.
- **Python and Spring Boot miss the deadline on their own**, before Discord's
  network leg.
- **Warm, the language barely matters.** Typical warm requests take 20–60 ms,
  almost all of it the Pub/Sub round trip. 34 of the 275 warm requests took
  over 200 ms, up to 854 ms, in every language and always on an instance that
  was already serving. That points at what the services share, the publish or
  CPU throttling between requests, rather than at any runtime. It stays well
  inside the deadline.
- **Rust REST's warm advantage is mostly batching.** The gRPC clients wait up
  to 10 ms for a batch to fill before sending; the REST variant sends at once.

### Limits

- **"Worst" is the slowest of five, not a percentile.** Five samples cannot give
  a tail. Read it as a floor on how bad a cold start can be.
- **Tiers, not ranks.** Neighbouring ranges overlap. The three groups (Go and
  Rust; the native builds and Node; the JVM, .NET JIT, Python and Spring) are
  clear. The order within a group is not.
- **One region, one day, gen1.** gen2 and other regions will move the absolute
  numbers. The archived test suite saw a similar ordering on gen2 in
  europe-west1.

## What it took to build each port

Recorded because each of these cost a failed build or a failed request, and
would again:

- **.NET Native AOT broke Google's auth library.** `Google.Apis.Auth` parses the
  metadata server's token response with Newtonsoft.Json by reflection. Trimmed,
  the response types lose the members it reflects over, and every publish fails
  with "Server response does not contain a JSON object". The fix is
  `<TrimmerRootAssembly>` for `Google.Apis.Auth` and `Google.Apis.Core`.
- **.NET has no Ed25519 in its base library.** The port used NSec, a libsodium
  binding.
- **The JDK 25 AOT cache needs a training run at build time with no
  credentials.** The port drove its own server with signed requests and pointed
  the publisher at a closed local port, so the gRPC classes load and fail fast.
  The cache is valid only for the exact JVM that made it, so the training runs
  in the runtime image.
- **GraalVM native image built first time.** The Google client libraries and
  gRPC ship their own native-image metadata.
- **Spring Boot 4 uses Jackson 3** (`tools.jackson.*`), not `com.fasterxml`.

## Rust's dependencies

The concern going in was that Rust's small standard library would mean a long
tail of third-party crates. The SDK service declared six direct dependencies
and the REST variant eight. All are heavily used, and all but `hex` are actively
maintained:

| Crate                 | Version | Downloads | Last 90 days | Last release | Maintainer          |
| --------------------- | ------- | --------: | -----------: | ------------ | ------------------- |
| `tokio`               | 1.54.1  |    1,253M |         333M | 2026-10-21   | tokio-rs            |
| `axum`                | 0.8.9   |      626M |         180M | 2026-04-14   | tokio-rs            |
| `serde_json`          | 1.0.154 |    1,701M |         470M | 2026-10-11   | serde-rs            |
| `ed25519-dalek`       | 3.0.0   |      293M |          95M | 2026-07-06   | dalek-cryptography  |
| `hex`                 | 0.4.3   |      846M |         229M | 2021-03-03   | KokaKiwi            |
| `google-cloud-pubsub` | 1.8.0   |        9M |           2M | 2026-11-11   | Google (googleapis) |
| `google-cloud-auth`   | 1.19.0  |       52M |          20M | 2026-11-11   | Google (googleapis) |
| `reqwest`             | 0.13.5  |      964M |         288M | 2026-09-08   | seanmonstar (hyper) |
| `base64`              | 0.23.1  |    2,082M |         587M | 2026-08-04   | marshallpierce      |

Figures from crates.io on 2026-11-27. The last three are the REST variant's.

- **`google-cloud-pubsub` is Google's own.** The crate name used to belong to a
  community client, which its author donated to Google; that client lives on as
  `gcloud-pubsub`. The official one is young (1.0.0 shipped on 2026-05-06), but
  it is generated and maintained alongside every other Google Cloud client
  library.
- **`hex` has not released since 2021.** It looks finished rather than
  abandoned: it does one small job and still sees 229M downloads in 90 days. It
  decodes two hex strings, a dozen lines to write by hand if that is a concern.
- **Transitively the tree is larger than it looks.** The SDK variant resolved to
  181 distinct crates and the REST variant to 147; most of the difference is the
  gRPC stack. For comparison, the Go deployable builds against 44 modules, Node
  installed 83 packages, Python 32 distributions and the Java services 56 jars.

## Conclusion

Go stays. It is in the same tier as Rust, more than two seconds inside the
deadline at its worst, and keeps this repository in one language that is
straightforward to maintain. Nothing here argues for a rewrite.
