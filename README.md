# Discord Bot Go - GCP Serverless

## Overview

This a Go/GCP implementation of a Discord Bot Interaction Endpoint, that copies any requests
made onto a Pub/Sub queue, then returns a success response. It can be deployed in a serverless
fashion into Cloud Functions or Cloud Run.

It is ideally suited for low volume and low (zero) cost scenarios.

### Usage

- Create Pub-Sub Topic
- Deploy Service
- Configure Discord by setting the `INTERACTIONS ENDPOINT URL` value in the Discord App developers portal
  to the value of the Cloud Run endpoint with `/handleDiscordMessage` for example:
  `https://europe-west2-project-id.cloudfunctions.net/discord-bot/handleDiscordMessage`

The Cloud Functions deployment below is the shortest path to a working bot. For a Cloud Run
deployment — and for the service settings that decide whether a cold start lands inside
Discord's 3 second deadline — see [Deploying to Cloud Run](#deploying-to-cloud-run).

```sh
# Public key signature from the Discord Apps Portal
export PUBLIC_SIG_KEY=0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef0

# Name of your GCP project
export PUBSUB_PROJECT_ID=gaming-project

# Name of the Pub Sub Topic to publish messages to
export PUBSUB_TOPIC_NAME=discord-ops

# Create the PubSub Topic
gcloud pubsub topics create ${PUBSUB_TOPIC_NAME}

# Create the Cloud Function
gcloud functions deploy discord-bot \
  --entry-point handleDiscordMessage \
  --runtime go125 \
  --trigger-http \
  --region europe-west2 \
  --gen2 \
  --allow-unauthenticated \
  --set-env-vars PUBLIC_SIG_KEY=${PUBLIC_SIG_KEY},PUBSUB_PROJECT_ID=${PUBSUB_PROJECT_ID},PUBSUB_TOPIC_NAME=${PUBSUB_TOPIC_NAME}
```

### Discord Bots - WebSockets vs. WebHooks

Discord Bots can be implemented one of 2 ways:

- __Web Sockets__.
The Discord Bot has a daemon that runs constantly with a WebSocket connection to the Discord
servers. Any interaction requests are sent over this existing connection. This provides a highly
scalable, efficient and responsive mechanism, and is ideally suited for high volumes of requests.
The connection is a pull mechanism, where the daemon is started and a connection initiated with
the Discord servers.

- __Interaction Endpoint (WebHooks)__.
Discord is provided with an Interaction Endpoint URL. When an interaction occurs a new HTTP request
is made to this endpoint with the request details. There is effectively a WebHook mechanism, with
the downsides being an inside in the overhead per request, and higher latency of requests.

### Serverless and WebHooks

Implementing a WebHook interaction pattern using a serverless compute PaaS offering is ideal for
low cost use-cases. Rather than having to host (and pay for) a daemon running 24x7 on a virtual
machine or within a managed container where you will have to pay per-hour of up-time, with a
serverless compute option you will just have to pay for each interaction.

### Discord and the 3 second problem

When calling Interaction Endpoints, Discord has a time-limit of 3 seconds for the endpoint
to return a response. After the initial response, the Discord Bot can subsequently provide an
updated message to display on the client, but the initial response must be within 3 seconds.

This can cause problems with WebHooks implemented using Serverless PaaS services. All
serverless PaaS services exhibit a problem known as 'cold starts'. This is the where the first
call to a service after a period of time, will take longer to execute - as the execution
environment has to be prepared, and the function initialised. This can easily cause the function
to take longer than the allowed 3 seconds to respond.

This highlights a constraint of most serverless PaaS offering - where once a response has
been sent to the requester, the processing of the function is suspended. This prevents any
use of additional threads to continue the processing and send the follow-up message

For this reason - this bot implementation simply copies the received request to a Pub/Sub
queue, and then returns the acknowledgement.

Picking a fast language gets you most of the way there. The rest is deployment configuration,
and the platform defaults are not on your side — see
[Deploying to Cloud Run](#deploying-to-cloud-run).

### Optimising Language

Cold starts are more/less of a problem depending on the coding language used. Those languages
that are compiled down to native images have the shortest start-up times, where-as those that
are interpreted and/or have large language VMs take the longest. This problem is increased as
the size of the package also tends to be larger, which will take longer to copy across the
network into the execution environment.

Testing of a dummy function showed the following response times, for both cold and warm
executions across a number of different languages:

| Language            | Cold Start | Warm Start |
| ------------------- | ---------- | ---------- |
| DotNet              | 3,137 ms   | 337 ms     |
| NodeJS              | 1,912 ms   | 215 ms     |
| Go                  | 946 ms     | 124 ms     |
| Python              | 2,060 ms   | 284 ms     |
| Java (no framework) | 4,222 ms   | 570 ms     |
| Java (Springboot 2) | 15,000 ms  | 650 ms     |
| Java (GraalVM)      | 3,164 ms   | 332 ms     |

Note: Java can be compiled into a native image using the GraalVM JDK, and compatible frameworks such
as Micronaut, Quarkus or SpringBoot 3 Native.

The figures above are from Cloud Functions. For an equivalent measurement on Cloud Run, taken
against this same Discord webhook workload across 19 language and framework combinations, see
[discord-bot-test-suite](https://github.com/pmgledhill102/discord-bot-test-suite).

### Minimal Execution

To ensure the execution time is kept to a minimum the code just does 3 things:

- __Validate Signature__.
  Each request made by the Discord Servers to the Bot has a signature. It is a security
  requirement that this signature is validated with every request.

- __Add message to Queue__.
  The request body is posted to a Pub/Sub queue for processing.

- __Success response__.
  An `InteractionResponseDeferredChannelMessageWithSource` response type is sent back to the Discord
  servers. This indicates that the request has been received, and let's Discord that the Bot will
  send a further message in the future with the actual message to display to the user.

## Deploying to Cloud Run

Cloud Run is the better home for this bot. It is the same infrastructure that runs Cloud
Functions gen2 underneath, but it exposes the service settings that decide whether a cold
start lands inside Discord's 3 second deadline.

Those settings are the whole point of this section. The Cloud Run defaults are tuned for a
service with steady traffic; a Discord interactions endpoint is the opposite — idle for
hours, then a single request with a hard 3 second budget. Left at their defaults, the bot
works fine while you are testing it and intermittently shows
`The application did not respond` once real traffic goes quiet enough for instances to be
reclaimed.

A ready-to-edit service definition carrying all of them is in
[`deploy/service.yaml`](deploy/service.yaml).

### Prerequisites

The topic, and a dedicated service account that can publish to it. The default compute
service account would work and is over-privileged; this is two commands:

```sh
gcloud pubsub topics create ${PUBSUB_TOPIC_NAME}

gcloud iam service-accounts create discord-bot \
  --display-name "Discord interactions endpoint"

gcloud pubsub topics add-iam-policy-binding ${PUBSUB_TOPIC_NAME} \
  --member="serviceAccount:discord-bot@${PUBSUB_PROJECT_ID}.iam.gserviceaccount.com" \
  --role=roles/pubsub.publisher
```

Add `--service-account discord-bot@${PUBSUB_PROJECT_ID}.iam.gserviceaccount.com` to the
deploy commands below; `deploy/service.yaml` already names it.

### Build and deploy

There is no Dockerfile — both routes below use Google Cloud buildpacks to build a Go binary
straight from source. They differ in one way that matters operationally: the URL path Discord
has to be given.

__As a Cloud Run function.__ The buildpack sets `FUNCTION_TARGET`, and the functions framework
then serves that one handler at the service root:

```sh
gcloud run deploy discord-bot \
  --source . \
  --function handleDiscordMessage \
  --region europe-west2 \
  --allow-unauthenticated \
  --execution-environment gen1 \
  --cpu-boost \
  --cpu-throttling \
  --cpu 1 \
  --memory 512Mi \
  --min-instances 0 \
  --max-instances 2 \
  --timeout 30 \
  --set-env-vars PUBLIC_SIG_KEY=${PUBLIC_SIG_KEY},PUBSUB_PROJECT_ID=${PUBSUB_PROJECT_ID},PUBSUB_TOPIC_NAME=${PUBSUB_TOPIC_NAME}
```

Interactions endpoint URL: `https://<service-url>/`

__As a plain container__, built from `cmd/main.go`:

```sh
gcloud run deploy discord-bot \
  --source . \
  --set-build-env-vars GOOGLE_BUILDABLE=./cmd \
  --region europe-west2 \
  --allow-unauthenticated \
  --execution-environment gen1 \
  --cpu-boost \
  --cpu-throttling \
  --cpu 1 \
  --memory 512Mi \
  --min-instances 0 \
  --max-instances 2 \
  --timeout 30 \
  --set-env-vars PUBLIC_SIG_KEY=${PUBLIC_SIG_KEY},PUBSUB_PROJECT_ID=${PUBSUB_PROJECT_ID},PUBSUB_TOPIC_NAME=${PUBSUB_TOPIC_NAME}
```

The repository root is `package discordbot` — a library, not a `main` — so the Go buildpack
has to be told where the entry point is, which is what `GOOGLE_BUILDABLE` does. With no
`FUNCTION_TARGET` set, the framework serves every registered function at its own path, so the
interactions endpoint URL is `https://<service-url>/handleDiscordMessage`.

Whichever route you take, the URL registered in the Discord developer portal has to match.
A mismatch fails as a 404 during Discord's `PING` validation, not as anything that mentions
paths.

### Applying the tuned settings from YAML

`deploy/service.yaml` carries every setting below with its reasoning inline. Edit the project
ID, region and image URL, then:

```sh
gcloud run services replace deploy/service.yaml --region europe-west2
```

`replace` is declarative and full-fidelity: anything absent from the file is removed from the
service. That is the point — it is the whole configuration in one reviewable place — but it
means a hand-tweak made in the console disappears on the next apply.

Then make the service publicly invocable, which `replace` cannot do because IAM is not part
of the service spec:

```sh
gcloud run services add-iam-policy-binding discord-bot \
  --region europe-west2 \
  --member=allUsers \
  --role=roles/run.invoker
```

Discord cannot present a Google identity, so there is no authenticated option here. The
Ed25519 signature check in the handler is the security boundary, which is why the bot verifies
every request before doing anything else with it.

### The settings that matter

| Setting | Value | Effect on the 3 second budget |
| ------- | ----- | ----------------------------- |
| `run.googleapis.com/startup-cpu-boost` | `'true'` | Extra CPU while the instance starts |
| `run.googleapis.com/execution-environment` | `gen1` | Faster scale-from-zero than gen2 |
| `startupProbe` | *absent* | Default TCP probe passes the moment the port binds |
| `run.googleapis.com/cpu-throttling` | `'true'` | No latency effect; stops the bill going always-on |
| `autoscaling.knative.dev/minScale` | `'0'` | Zero cost, and every cold start you have |
| `timeoutSeconds` | `30` | Discord gave up at 3; longer only keeps doomed instances alive |
| `cpu` / `memory` | `1` / `512Mi` | Past the knee for this workload |

__Startup CPU boost.__ Allocates extra CPU during startup and for 10 seconds afterwards, billed
only while starting. `gcloud run deploy` enables it by default on new services, but a YAML
service definition that omits the annotation is relying on a default that is not yours to
assume — state it explicitly. It is the cheapest latency you will ever buy.

__First generation execution environment.__ gen1 is gVisor; gen2 is a Linux microVM with better
CPU and network performance, full Linux syscall compatibility, and a 512 MiB memory floor.
Google's own guidance is gen1 for services that are sensitive to cold starts and scale from
zero frequently, gen2 for steady traffic that can absorb what the documentation calls
"somewhat slower cold starts". This handler verifies a signature and publishes one Pub/Sub
message; there is nothing in it that a microVM makes faster, and the quicker scale-from-zero
is exactly what the deadline is asking for.

__No explicit startup probe.__ This is the counter-intuitive one, and the one most likely to be
"fixed" by a well-meaning reviewer. Cloud Run's default is a TCP check that succeeds as soon
as the container binds its port. Adding an HTTP startup probe on `/health` does not make
startup safer — it makes it slower. An explicitly configured probe retries at `periodSeconds`
granularity, and the minimum is 1 second, so a container that is not quite listening at the
first attempt waits a full second for the second attempt. That is a third of Discord's entire
budget spent waiting to be asked again.

The default probe is accurate here as well as fast: `init()` reads the configuration and
builds the Pub/Sub client, and only then does `main` start listening. An open port genuinely
means ready. If you manage this in Terraform, note that `startup_probe` is `Optional+Computed`
— removing the block later is a no-op rather than a revert, so it has to stay absent rather
than be deleted.

__CPU throttling / request-based billing.__ `cpu-throttling: 'true'` allocates CPU only while a
request is in flight. Left unset, Cloud Run allocates and bills CPU for the instance's entire
lifetime. That reads like a cost footnote and it is not: combine always-allocated CPU with
anything that keeps instances alive — an uptime check, a warming ping, a monitoring prober —
and "lifetime" means around the clock. On the Discord ops bot these settings came from, that
combination cost roughly £10 in three weeks for one small service that was supposed to be
free.

`minScale: 0` does not save you from this. Nothing scales to zero while a prober is
knocking every 50 seconds.

__Scaling.__ `minScale: 0` is the design: pay nothing while idle. It is also the source of every
cold start you have. `minScale: 1` makes the 3 second problem disappear completely, and takes
the near-zero cost with it. That is a legitimate trade — a bot that matters more than a few
pounds a month should probably make it — but make it deliberately rather than drifting into
it. `maxScale` is a blast-radius cap, not a capacity plan.

### Things that look like fixes and are not

- __Scheduled warming pings.__ Cloud Scheduler hitting the service every few minutes is
  `minScale: 1` with extra moving parts and no guarantee: Cloud Run can still reclaim the
  instance between pings, so you pay for warmth and still cold-start occasionally. If a warm
  instance is worth paying for, buy it directly with `minScale`.
- __Uptime checks used as keep-warm.__ Google probes from several geographic locations at once,
  so a 300 second period is a request roughly every 50 seconds. That is a keep-warm dressed as
  a health check, with the billing consequence described above. Set the period to match the
  question you are actually asking — 15 minutes is ample for "is this reachable at all".
- __An HTTP `/health` startup probe.__ Costs latency, buys nothing here. See above.
- __More CPU or memory.__ Cold start on this workload is dominated by container start and Go
  runtime initialisation, not by the handler's own work. 1 vCPU and 512 MiB with boost enabled
  is already past the point of diminishing returns.

### If you stay on Cloud Functions

Cloud Functions gen2 is Cloud Run underneath, so the same settings apply — but
`gcloud functions deploy` does not expose `--cpu-boost` or `--execution-environment`. Reach
them through the backing Cloud Run service:

```sh
gcloud run services update discord-bot \
  --region europe-west2 \
  --execution-environment gen1 \
  --cpu-boost \
  --cpu-throttling
```

Check that the settings survived the next `gcloud functions deploy`; the two APIs manage the
same underlying resource and only one of them knows about these fields.

## More Information

[Discord Developers - Handling Interactivity](https://discord.com/developers/docs/getting-started#step-3-handling-interactivity)

[Cloud Run - Execution environments](https://cloud.google.com/run/docs/configuring/execution-environments)

[Cloud Run - Health checks](https://cloud.google.com/run/docs/configuring/healthchecks)

[Cloud Run - CPU allocation and billing](https://cloud.google.com/run/docs/configuring/services/cpu)
