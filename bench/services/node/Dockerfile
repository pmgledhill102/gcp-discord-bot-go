FROM node:26-trixie-slim AS deps
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci --omit=dev

# distroless/nodejs: the node binary and CA certificates, no shell, no npm.
FROM gcr.io/distroless/nodejs26-debian13:nonroot
WORKDIR /app
COPY --from=deps /app/node_modules ./node_modules
COPY index.mjs package.json ./
USER 65532:65532
CMD ["index.mjs"]
