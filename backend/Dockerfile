FROM golang:1.25.11 AS builder

ARG GOPROXY=https://goproxy.cn,direct
ENV GOPROXY=${GOPROXY}

WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .
RUN --mount=type=cache,target=/go/pkg/mod CGO_ENABLED=0 GOOS=linux go build -mod=readonly -o /out/actweave-api ./cmd/server
RUN --mount=type=cache,target=/go/pkg/mod CGO_ENABLED=0 GOOS=linux go build -mod=readonly -o /out/actweave-migrate ./cmd/migrate

FROM golang:1.25.11

WORKDIR /app
COPY --from=builder /out/actweave-api /app/actweave-api
COPY --from=builder /out/actweave-migrate /app/actweave-migrate
COPY --from=builder /src/config.yaml /app/config.yaml

EXPOSE 8080

CMD ["/app/actweave-api"]
