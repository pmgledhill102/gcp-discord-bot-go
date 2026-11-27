"""Python port of the gcp-discord-bot-go handler, for the cold-start comparison.

Flask under gunicorn is the shape Google's Cloud Run samples use. PyNaCl is
what Discord's own documentation verifies signatures with.
"""

import json
import logging
import os

from flask import Flask, Response, request
from google.cloud import pubsub_v1
from nacl.exceptions import BadSignatureError
from nacl.signing import VerifyKey

# Startup is the one place a failure may raise: a misconfigured revision
# should never take traffic.
KEY = VerifyKey(bytes.fromhex(os.environ["PUBLIC_SIG_KEY"]))
PUBLISHER = pubsub_v1.PublisherClient()
TOPIC = PUBLISHER.topic_path(os.environ["PUBSUB_PROJECT_ID"], os.environ["PUBSUB_TOPIC_NAME"])

app = Flask(__name__)


def verified(body: bytes) -> bool:
    sig = request.headers.get("X-Signature-Ed25519", "")
    ts = request.headers.get("X-Signature-Timestamp", "")
    if not sig or not ts:
        return False
    try:
        KEY.verify(ts.encode() + body, bytes.fromhex(sig))
    except (BadSignatureError, ValueError, TypeError):
        return False
    return True


def text(status: int, body: str) -> Response:
    return Response(body + "\n", status=status, mimetype="text/plain")


@app.route("/", defaults={"path": ""}, methods=["GET", "POST", "PUT"])
@app.route("/<path:path>", methods=["GET", "POST", "PUT"])
def handle(path: str) -> Response:
    body = request.get_data()
    if not verified(body):
        return text(401, "invalid request signature")

    try:
        interaction = json.loads(body)
    except ValueError:
        return text(400, "malformed interaction payload")

    kind = interaction.get("type") if isinstance(interaction, dict) else None
    if kind == 1:
        return Response('{"type":1}', mimetype="application/json")
    if kind != 2:
        return text(400, "unhandled interaction type")

    try:
        PUBLISHER.publish(TOPIC, body).result(timeout=30)
    except Exception:  # noqa: BLE001 -- any publish failure is a 500, never a crash
        logging.exception("Failed to publish message")
        return text(500, "failed to queue interaction")

    return Response('{"type":5}', mimetype="application/json")
