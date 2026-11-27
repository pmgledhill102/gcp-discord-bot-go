# Builder and runtime share Debian 13, so the glibc the binary links against is
# the one it runs on. aws-lc-rs (rustls' default provider) needs a C toolchain
# and cmake at build time, which the full rust image has.
FROM rust:1.100-trixie AS builder
RUN apt-get update && apt-get install -y --no-install-recommends cmake && rm -rf /var/lib/apt/lists/*
WORKDIR /src
COPY Cargo.toml Cargo.lock ./
COPY src ./src
RUN cargo build --release --locked

# distroless/cc carries glibc, libgcc and CA certificates, and nothing else.
FROM gcr.io/distroless/cc-debian13:nonroot
COPY --from=builder /src/target/release/bench-rust-rest /app
USER 65532:65532
ENTRYPOINT ["/app"]
