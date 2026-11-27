// Java port of the gcp-discord-bot-go handler, for the cold-start comparison.
//
// No framework: the JDK's own HTTP server and Ed25519 (JDK 15+), the official
// Pub/Sub client, and Gson (already one of that client's dependencies). The same jar is shipped three ways -- plain JVM,
// JVM with a JDK 25 AOT cache, and a GraalVM native image -- so the runtime is
// the only thing that differs between them.
package bench;

import com.google.api.core.ApiFuture;
import com.google.gson.JsonElement;
import com.google.gson.JsonObject;
import com.google.gson.JsonParser;
import com.google.api.gax.core.NoCredentialsProvider;
import com.google.api.gax.grpc.InstantiatingGrpcChannelProvider;
import com.google.api.gax.retrying.RetrySettings;
import com.google.cloud.pubsub.v1.Publisher;
import com.google.protobuf.ByteString;
import com.google.pubsub.v1.PubsubMessage;
import com.google.pubsub.v1.TopicName;
import com.sun.net.httpserver.HttpExchange;
import com.sun.net.httpserver.HttpServer;
import io.grpc.ManagedChannelBuilder;
import java.io.IOException;
import java.net.InetSocketAddress;
import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.nio.charset.StandardCharsets;
import java.security.KeyFactory;
import java.security.KeyPair;
import java.security.KeyPairGenerator;
import java.security.PublicKey;
import java.security.Signature;
import java.security.spec.X509EncodedKeySpec;
import java.time.Duration;
import java.util.HexFormat;
import java.util.concurrent.Executors;

public final class Main {
  // DER SubjectPublicKeyInfo prefix for a raw 32-byte Ed25519 key.
  private static final byte[] SPKI_PREFIX = HexFormat.of().parseHex("302a300506032b6570032100");

  private final PublicKey key;
  private final Publisher publisher;

  private Main(PublicKey key, Publisher publisher) {
    this.key = key;
    this.publisher = publisher;
  }

  static PublicKey publicKey(byte[] raw) throws Exception {
    byte[] der = new byte[SPKI_PREFIX.length + raw.length];
    System.arraycopy(SPKI_PREFIX, 0, der, 0, SPKI_PREFIX.length);
    System.arraycopy(raw, 0, der, SPKI_PREFIX.length, raw.length);
    return KeyFactory.getInstance("Ed25519").generatePublic(new X509EncodedKeySpec(der));
  }

  /** Returns the top-level "type", or null if absent; throws if the body is not a JSON object. */
  static Integer interactionType(String json) {
    JsonObject obj = JsonParser.parseString(json).getAsJsonObject();
    JsonElement type = obj.get("type");
    if (type == null || !type.isJsonPrimitive() || !type.getAsJsonPrimitive().isNumber()) return null;
    return type.getAsInt();
  }

  private boolean verified(HttpExchange ex, byte[] body) {
    String sig = ex.getRequestHeaders().getFirst("X-Signature-Ed25519");
    String ts = ex.getRequestHeaders().getFirst("X-Signature-Timestamp");
    if (sig == null || ts == null || sig.length() != 128) return false;
    try {
      Signature v = Signature.getInstance("Ed25519");
      v.initVerify(key);
      v.update(ts.getBytes(StandardCharsets.UTF_8));
      v.update(body);
      return v.verify(HexFormat.of().parseHex(sig));
    } catch (Exception e) {
      return false;
    }
  }

  private static void send(HttpExchange ex, int status, String body, String type) throws IOException {
    byte[] bytes = body.getBytes(StandardCharsets.UTF_8);
    ex.getResponseHeaders().set("Content-Type", type);
    ex.sendResponseHeaders(status, bytes.length);
    try (var out = ex.getResponseBody()) {
      out.write(bytes);
    }
  }

  private void handle(HttpExchange ex) throws IOException {
    try (ex) {
      byte[] body = ex.getRequestBody().readAllBytes();
      if (!verified(ex, body)) {
        send(ex, 401, "invalid request signature\n", "text/plain");
        return;
      }

      Integer type;
      try {
        type = interactionType(new String(body, StandardCharsets.UTF_8));
      } catch (RuntimeException e) {
        send(ex, 400, "malformed interaction payload\n", "text/plain");
        return;
      }

      if (type != null && type == 1) {
        send(ex, 200, "{\"type\":1}", "application/json");
        return;
      }
      if (type == null || type != 2) {
        send(ex, 400, "unhandled interaction type\n", "text/plain");
        return;
      }

      try {
        ApiFuture<String> id =
            publisher.publish(PubsubMessage.newBuilder().setData(ByteString.copyFrom(body)).build());
        id.get();
      } catch (Exception e) {
        System.err.println("Failed to publish message: " + e);
        send(ex, 500, "failed to queue interaction\n", "text/plain");
        return;
      }
      send(ex, 200, "{\"type\":5}", "application/json");
    } catch (RuntimeException e) {
      // Nothing on the request path may take the process down.
      System.err.println("Unhandled error: " + e);
    }
  }

  private static HttpServer serve(Main app, int port) throws IOException {
    HttpServer server = HttpServer.create(new InetSocketAddress(port), 0);
    server.setExecutor(Executors.newVirtualThreadPerTaskExecutor());
    server.createContext("/", app::handle);
    server.start();
    return server;
  }

  private static String required(String name) {
    String v = System.getenv(name);
    if (v == null || v.isEmpty()) throw new IllegalStateException(name + " is required");
    return v;
  }

  public static void main(String[] args) throws Exception {
    if (args.length == 1 && args[0].equals("--train")) {
      train();
      return;
    }

    // Startup is the one place a failure may throw: a misconfigured revision
    // should never take traffic.
    PublicKey key = publicKey(HexFormat.of().parseHex(required("PUBLIC_SIG_KEY")));
    Publisher publisher =
        Publisher.newBuilder(TopicName.of(required("PUBSUB_PROJECT_ID"), required("PUBSUB_TOPIC_NAME")))
            .build();
    int port = Integer.parseInt(System.getenv().getOrDefault("PORT", "8080"));
    serve(new Main(key, publisher), port);
  }

  /**
   * Build-time training run for the JDK 25 AOT cache. There are no credentials
   * in a Docker build, so the publisher points at a closed local port with no
   * auth and a few attempts: the publish fails, but only after the gRPC,
   * Netty and protobuf classes it needs have been loaded and linked, which is
   * what the cache records. Signed requests drive the real HTTP and Ed25519
   * paths through the same server code.
   */
  private static void train() throws Exception {
    KeyPair kp = KeyPairGenerator.getInstance("Ed25519").generateKeyPair();
    byte[] encoded = kp.getPublic().getEncoded();
    byte[] raw = java.util.Arrays.copyOfRange(encoded, encoded.length - 32, encoded.length);

    Publisher publisher =
        Publisher.newBuilder(TopicName.of("training", "training"))
            .setCredentialsProvider(NoCredentialsProvider.create())
            .setChannelProvider(
                InstantiatingGrpcChannelProvider.newBuilder()
                    .setEndpoint("127.0.0.1:9")
                    .setChannelConfigurator(ManagedChannelBuilder::usePlaintext)
                    .build())
            .setRetrySettings(
                RetrySettings.newBuilder()
                    .setTotalTimeoutDuration(Duration.ofSeconds(10))
                    .setInitialRpcTimeoutDuration(Duration.ofSeconds(1))
                    .setMaxRpcTimeoutDuration(Duration.ofSeconds(1))
                    .setMaxAttempts(2)
                    .build())
            .build();

    HttpServer server = serve(new Main(publicKey(raw), publisher), 18080);
    HttpClient client = HttpClient.newHttpClient();
    String[] bodies = {"{\"type\":1}", "{\"type\":2,\"data\":{\"name\":\"train\"}}"};
    for (int i = 0; i < 3; i++) {
      for (String body : bodies) {
        String ts = Long.toString(System.currentTimeMillis() / 1000);
        Signature s = Signature.getInstance("Ed25519");
        s.initSign(kp.getPrivate());
        s.update((ts + body).getBytes(StandardCharsets.UTF_8));
        // Loopback to this process's own training server, at build time only.
        HttpRequest req =
            // nosemgrep: problem-based-packs.insecure-transport.java-stdlib.httpclient-http-request.httpclient-http-request
            HttpRequest.newBuilder(URI.create("http://127.0.0.1:18080/"))
                .header("X-Signature-Ed25519", HexFormat.of().formatHex(s.sign()))
                .header("X-Signature-Timestamp", ts)
                .POST(HttpRequest.BodyPublishers.ofString(body))
                .build();
        HttpResponse<String> res = client.send(req, HttpResponse.BodyHandlers.ofString());
        System.out.println("training " + body + " -> " + res.statusCode());
      }
    }
    server.stop(0);
    publisher.shutdown();
    System.exit(0);
  }
}
