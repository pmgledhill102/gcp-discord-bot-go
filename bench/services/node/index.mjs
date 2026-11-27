// Node.js port of the gcp-discord-bot-go handler, for the cold-start comparison.
//
// node:http and node:crypto (which verifies Ed25519 natively) cover everything
// except Pub/Sub, so the official client is the only dependency.
import { createServer } from 'node:http';
import { createPublicKey, verify } from 'node:crypto';
import { PubSub } from '@google-cloud/pubsub';

function required(name) {
  const v = process.env[name];
  if (!v) {
    console.error(`${name} is required`);
    process.exit(1); // startup only: a misconfigured revision must not serve
  }
  return v;
}

// Wrap the raw 32-byte key in the fixed SPKI prefix for Ed25519.
const key = createPublicKey({
  key: Buffer.concat([Buffer.from('302a300506032b6570032100', 'hex'), Buffer.from(required('PUBLIC_SIG_KEY'), 'hex')]),
  format: 'der',
  type: 'spki',
});
const topic = new PubSub({ projectId: required('PUBSUB_PROJECT_ID') }).topic(required('PUBSUB_TOPIC_NAME'));

function send(res, status, body, type = 'text/plain') {
  res.writeHead(status, { 'Content-Type': type });
  res.end(body);
}

function verified(req, body) {
  const sig = req.headers['x-signature-ed25519'];
  const ts = req.headers['x-signature-timestamp'];
  if (typeof sig !== 'string' || typeof ts !== 'string' || !/^[0-9a-fA-F]{128}$/.test(sig)) return false;
  try {
    return verify(null, Buffer.concat([Buffer.from(ts), body]), key, Buffer.from(sig, 'hex'));
  } catch {
    return false;
  }
}

async function handle(req, res, body) {
  if (!verified(req, body)) return send(res, 401, 'invalid request signature\n');

  let interaction;
  try {
    interaction = JSON.parse(body);
  } catch {
    return send(res, 400, 'malformed interaction payload\n');
  }

  if (interaction?.type === 1) return send(res, 200, '{"type":1}', 'application/json');
  if (interaction?.type !== 2) return send(res, 400, 'unhandled interaction type\n');

  try {
    await topic.publishMessage({ data: body });
  } catch (err) {
    console.error('Failed to publish message:', err);
    return send(res, 500, 'failed to queue interaction\n');
  }
  send(res, 200, '{"type":5}', 'application/json');
}

createServer((req, res) => {
  const chunks = [];
  req.on('data', (c) => chunks.push(c));
  req.on('end', () => {
    handle(req, res, Buffer.concat(chunks)).catch((err) => {
      console.error('Unhandled error:', err);
      if (!res.headersSent) send(res, 500, 'internal error\n');
    });
  });
}).listen(Number(process.env.PORT || 8080));
