// Spring Boot port of the gcp-discord-bot-go handler, for the cold-start
// comparison: a stock Spring MVC application on Tomcat, with the same Pub/Sub
// client and Ed25519 code as the framework-free Java service.
package bench;

import com.google.cloud.pubsub.v1.Publisher;
import com.google.protobuf.ByteString;
import com.google.pubsub.v1.PubsubMessage;
import com.google.pubsub.v1.TopicName;
import java.nio.charset.StandardCharsets;
import java.security.KeyFactory;
import java.security.PublicKey;
import java.security.Signature;
import java.security.spec.X509EncodedKeySpec;
import java.util.HexFormat;
import org.springframework.beans.factory.annotation.Value;
import org.springframework.boot.SpringApplication;
import org.springframework.boot.autoconfigure.SpringBootApplication;
import org.springframework.context.annotation.Bean;
import org.springframework.http.MediaType;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.RequestBody;
import org.springframework.web.bind.annotation.RequestHeader;
import org.springframework.web.bind.annotation.PostMapping;
import org.springframework.web.bind.annotation.RestController;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.ObjectMapper;

@SpringBootApplication
@RestController
public class Application {
  private static final byte[] SPKI_PREFIX = HexFormat.of().parseHex("302a300506032b6570032100");

  private final PublicKey key;
  private final Publisher publisher;
  private final ObjectMapper mapper;

  public Application(PublicKey key, Publisher publisher, ObjectMapper mapper) {
    this.key = key;
    this.publisher = publisher;
    this.mapper = mapper;
  }

  public static void main(String[] args) {
    SpringApplication.run(Application.class, args);
  }

  @Bean
  static PublicKey publicKey(@Value("${PUBLIC_SIG_KEY}") String hex) throws Exception {
    byte[] raw = HexFormat.of().parseHex(hex);
    byte[] der = new byte[SPKI_PREFIX.length + raw.length];
    System.arraycopy(SPKI_PREFIX, 0, der, 0, SPKI_PREFIX.length);
    System.arraycopy(raw, 0, der, SPKI_PREFIX.length, raw.length);
    return KeyFactory.getInstance("Ed25519").generatePublic(new X509EncodedKeySpec(der));
  }

  @Bean(destroyMethod = "shutdown")
  static Publisher publisher(
      @Value("${PUBSUB_PROJECT_ID}") String project, @Value("${PUBSUB_TOPIC_NAME}") String topic)
      throws Exception {
    return Publisher.newBuilder(TopicName.of(project, topic)).build();
  }

  private boolean verified(String sig, String ts, byte[] body) {
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

  private static ResponseEntity<String> text(int status, String body) {
    return ResponseEntity.status(status).contentType(MediaType.TEXT_PLAIN).body(body + "\n");
  }

  private static ResponseEntity<String> json(String body) {
    return ResponseEntity.ok().contentType(MediaType.APPLICATION_JSON).body(body);
  }

  // Discord only ever POSTs to an interactions endpoint.
  @PostMapping("/**")
  ResponseEntity<String> handle(
      @RequestHeader(value = "X-Signature-Ed25519", required = false) String sig,
      @RequestHeader(value = "X-Signature-Timestamp", required = false) String ts,
      @RequestBody(required = false) byte[] body) {
    if (body == null) body = new byte[0];
    if (!verified(sig, ts, body)) return text(401, "invalid request signature");

    JsonNode type;
    try {
      JsonNode root = mapper.readTree(body);
      if (root == null || !root.isObject()) return text(400, "malformed interaction payload");
      type = root.get("type");
    } catch (Exception e) {
      return text(400, "malformed interaction payload");
    }

    int t = type != null && type.isInt() ? type.intValue() : -1;
    if (t == 1) return json("{\"type\":1}");
    if (t != 2) return text(400, "unhandled interaction type");

    try {
      publisher.publish(PubsubMessage.newBuilder().setData(ByteString.copyFrom(body)).build()).get();
    } catch (Exception e) {
      System.err.println("Failed to publish message: " + e);
      return text(500, "failed to queue interaction");
    }
    return json("{\"type\":5}");
  }
}
