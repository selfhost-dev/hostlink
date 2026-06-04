# syntax=docker/dockerfile:1

# Build stage
FROM golang:1.26 AS build-stage

WORKDIR /app

# Download dependencies first for better layer caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build binary
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -o /build .

# Runtime stage
FROM scratch

WORKDIR /

COPY --from=build-stage /build /build

EXPOSE 1323

ENTRYPOINT ["/build"]