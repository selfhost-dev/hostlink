# syntax=docker/dockerfile:1
# The SQL stream relay (cmd/sqlrelay), deployed next to the selfhost backend. See docs/sql-stream.md.
# docker build -f dockers/sqlrelay.Dockerfile -t sqlrelay .

FROM golang:1.27.3 AS build

WORKDIR /app
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY ./ ./
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /sqlrelay ./cmd/sqlrelay

# CA certificates for SQLRELAY_KEYS_URL over HTTPS, and a non-root user.
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /sqlrelay /sqlrelay
EXPOSE 8090
USER nonroot:nonroot
ENTRYPOINT ["/sqlrelay"]
