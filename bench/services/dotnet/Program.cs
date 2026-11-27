// .NET port of the gcp-discord-bot-go handler, for the cold-start comparison.
//
// The same project is published two ways: JIT (Dockerfile) and Native AOT
// (Dockerfile.aot). CreateSlimBuilder is used for both, so AOT is the only
// difference between the two images.
using System.Text;
using System.Text.Json;
using Google.Cloud.PubSub.V1;
using Google.Protobuf;
using NSec.Cryptography;

static string Required(string name) =>
    Environment.GetEnvironmentVariable(name) is { Length: > 0 } v
        ? v
        : throw new InvalidOperationException($"{name} is required");

// Startup is the one place a failure may throw: a misconfigured revision
// should never take traffic.
var key = PublicKey.Import(SignatureAlgorithm.Ed25519, Convert.FromHexString(Required("PUBLIC_SIG_KEY")), KeyBlobFormat.RawPublicKey);
var publisher = await PublisherClient.CreateAsync(new TopicName(Required("PUBSUB_PROJECT_ID"), Required("PUBSUB_TOPIC_NAME")));

var builder = WebApplication.CreateSlimBuilder(args);
builder.WebHost.UseUrls($"http://0.0.0.0:{Environment.GetEnvironmentVariable("PORT") ?? "8080"}");
var app = builder.Build();

bool Verified(HttpRequest req, byte[] body)
{
    var sig = req.Headers["X-Signature-Ed25519"].ToString();
    var ts = req.Headers["X-Signature-Timestamp"].ToString();
    if (sig.Length != 128 || ts.Length == 0) return false;
    byte[] sigBytes;
    try { sigBytes = Convert.FromHexString(sig); }
    catch (FormatException) { return false; }
    var signed = new byte[Encoding.UTF8.GetByteCount(ts) + body.Length];
    var n = Encoding.UTF8.GetBytes(ts, signed);
    body.CopyTo(signed, n);
    return SignatureAlgorithm.Ed25519.Verify(key, signed, sigBytes);
}

IResult Text(int status, string body) => Results.Text(body + "\n", "text/plain", statusCode: status);

app.Map("/{**path}", async (HttpRequest req) =>
{
    using var ms = new MemoryStream();
    await req.Body.CopyToAsync(ms);
    var body = ms.ToArray();

    if (!Verified(req, body)) return Text(401, "invalid request signature");

    int? type;
    try
    {
        using var doc = JsonDocument.Parse(body);
        type = doc.RootElement.ValueKind == JsonValueKind.Object
            && doc.RootElement.TryGetProperty("type", out var t)
            && t.TryGetInt32(out var i) ? i : null;
    }
    catch (JsonException) { return Text(400, "malformed interaction payload"); }

    if (type == 1) return Results.Text("{\"type\":1}", "application/json");
    if (type != 2) return Text(400, "unhandled interaction type");

    try
    {
        await publisher.PublishAsync(ByteString.CopyFrom(body));
    }
    catch (Exception e)
    {
        Console.Error.WriteLine($"Failed to publish message: {e}");
        return Text(500, "failed to queue interaction");
    }
    return Results.Text("{\"type\":5}", "application/json");
});

app.Run();
