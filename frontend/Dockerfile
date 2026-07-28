# ZKL-64 MEDIUM-06 / D7-A: pin Node builder + Nginx runtime by patch tag and immutable digest.
# Digests sourced from Docker Hub multi-arch manifests (2026-07-27).
# Node aligns with frontend/.node-version / package engines (22.22.3).
FROM node:22.22.3-alpine@sha256:e58326d0d441090181ac150dc2078d3e2cf6a0d42e809aebba3ef5880935ffdd AS builder

WORKDIR /src

COPY package.json package-lock.json ./
RUN npm ci

COPY . .
RUN npm run build

FROM nginx:1.28.0-alpine@sha256:30f1c0d78e0ad60901648be663a710bdadf19e4c10ac6782c235200619158284

# Snippet path used by nginx.conf include directives.
RUN mkdir -p /etc/nginx/snippets
COPY nginx-security-headers.conf /etc/nginx/snippets/actweave-security-headers.conf
COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=builder /src/dist /usr/share/nginx/html

# Syntax check: map compose service name so upstream resolves at build time.
RUN echo "127.0.0.1 backend" >> /etc/hosts && nginx -t

EXPOSE 80
