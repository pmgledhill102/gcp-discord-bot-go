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
  --runtime go121 \
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

### More Information

[Discord Developers - Handling Interactivity](https://discord.com/developers/docs/getting-started#step-3-handling-interactivity)
