//! Rust port of the gcp-discord-bot-go handler, for the cold-start comparison.
//!
//! Same contract as the Go handler: verify the Ed25519 signature (401 on
//! failure), answer PING with PONG, publish an application command's raw body
//! to Pub/Sub and wait for the server's ack, then return a deferred response.
//!
//! Publishing uses Google's official client, `google-cloud-pubsub`.

use std::{env, sync::Arc};

use axum::{
    Router,
    body::Bytes,
    http::{HeaderMap, StatusCode, header},
    response::{IntoResponse, Response},
};
use ed25519_dalek::{Signature, Verifier, VerifyingKey};
use google_cloud_pubsub::{client::Publisher, model::Message};

struct AppState {
    key: VerifyingKey,
    publisher: Publisher,
}

fn verify(key: &VerifyingKey, headers: &HeaderMap, body: &[u8]) -> bool {
    let (Some(sig), Some(ts)) = (
        headers.get("X-Signature-Ed25519"),
        headers.get("X-Signature-Timestamp"),
    ) else {
        return false;
    };
    let Ok(sig) = hex::decode(sig.as_bytes()) else {
        return false;
    };
    let Ok(sig) = Signature::from_slice(&sig) else {
        return false;
    };
    let mut signed = Vec::with_capacity(ts.len() + body.len());
    signed.extend_from_slice(ts.as_bytes());
    signed.extend_from_slice(body);
    key.verify(&signed, &sig).is_ok()
}

fn json(body: &'static str) -> Response {
    ([(header::CONTENT_TYPE, "application/json")], body).into_response()
}

async fn handle(state: Arc<AppState>, headers: HeaderMap, body: Bytes) -> Response {
    if !verify(&state.key, &headers, &body) {
        return (StatusCode::UNAUTHORIZED, "invalid request signature\n").into_response();
    }

    let Ok(value) = serde_json::from_slice::<serde_json::Value>(&body) else {
        return (StatusCode::BAD_REQUEST, "malformed interaction payload\n").into_response();
    };

    match value.get("type").and_then(|t| t.as_i64()) {
        Some(1) => return json(r#"{"type":1}"#),
        Some(2) => {}
        _ => return (StatusCode::BAD_REQUEST, "unhandled interaction type\n").into_response(),
    }

    if let Err(e) = state.publisher.publish(Message::new().set_data(body)).await {
        eprintln!("Failed to publish message: {e}");
        return (StatusCode::INTERNAL_SERVER_ERROR, "failed to queue interaction\n").into_response();
    }

    json(r#"{"type":5}"#)
}

#[tokio::main]
async fn main() {
    // Startup is the one place a failure may exit: a misconfigured revision
    // should never take traffic.
    let key_hex = env::var("PUBLIC_SIG_KEY").expect("PUBLIC_SIG_KEY is required");
    let key_bytes: [u8; 32] = hex::decode(key_hex)
        .expect("PUBLIC_SIG_KEY must be hex")
        .try_into()
        .expect("PUBLIC_SIG_KEY must be 32 bytes");
    let key = VerifyingKey::from_bytes(&key_bytes).expect("PUBLIC_SIG_KEY is not a valid key");

    let project = env::var("PUBSUB_PROJECT_ID").expect("PUBSUB_PROJECT_ID is required");
    let topic = env::var("PUBSUB_TOPIC_NAME").expect("PUBSUB_TOPIC_NAME is required");
    let publisher = Publisher::builder(format!("projects/{project}/topics/{topic}"))
        .build()
        .await
        .expect("cannot build Pub/Sub publisher");

    let state = Arc::new(AppState { key, publisher });
    let app = Router::new().fallback(move |headers: HeaderMap, body: Bytes| {
        let state = state.clone();
        async move { handle(state, headers, body).await }
    });

    let port = env::var("PORT").unwrap_or_else(|_| "8080".into());
    let listener = tokio::net::TcpListener::bind(format!("0.0.0.0:{port}"))
        .await
        .expect("cannot bind");
    axum::serve(listener, app).await.expect("server error");
}
