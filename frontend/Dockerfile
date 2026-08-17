# Build from the repository root so file:../sdk/typescript can be installed:
#   docker build -f frontend/Dockerfile .
#
# ZKL-64 MEDIUM-06 / D7-A: pin Node builder + Nginx runtime by patch tag and immutable digest.
# Digests sourced from Docker Hub multi-arch manifests (2026-07-27).
# Node aligns with frontend/.node-version / package engines (22.22.3).
FROM node:22.22.3-alpine@sha256:e58326d0d441090181ac150dc2078d3e2cf6a0d42e809aebba3ef5880935ffdd AS builder

WORKDIR /src

# Local package @actweave/agent-client is a file: dependency of the frontend.
COPY sdk/typescript/package.json sdk/typescript/package-lock.json sdk/typescript/tsconfig.json sdk/typescript/tsconfig.build.json /src/sdk/typescript/
COPY sdk/typescript/src /src/sdk/typescript/src
WORKDIR /src/sdk/typescript
RUN npm ci && npm run build

WORKDIR /src/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ ./
RUN npm run build

FROM nginx:1.28.0-alpine@sha256:30f1c0d78e0ad60901648be663a710bdadf19e4c10ac6782c235200619158284

# Snippet path used by nginx.conf include directives.
RUN mkdir -p /etc/nginx/snippets
COPY frontend/nginx-security-headers.conf /etc/nginx/snippets/actweave-security-headers.conf
COPY frontend/nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=builder /src/frontend/dist /usr/share/nginx/html

# nginx -t resolves `backend` at parse time. Rewrite only for the check so
# the image still ships the compose service name, and so the check works
# when /etc/hosts is read-only (common on Docker Desktop).
RUN cp /etc/nginx/conf.d/default.conf /tmp/default.conf \
    && sed -i 's|http://backend:|http://127.0.0.1:|g' /etc/nginx/conf.d/default.conf \
    && nginx -t \
    && mv /tmp/default.conf /etc/nginx/conf.d/default.conf

EXPOSE 80
