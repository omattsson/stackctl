# Stage 1: Build
FROM golang:1.26.4-alpine AS builder

ARG VERSION=dev
ARG COMMIT=none
ARG DATE=unknown

WORKDIR /build

# Cache dependencies
COPY cli/go.mod cli/go.sum ./cli/
RUN cd cli && go mod download

# Copy source and build
COPY cli/ ./cli/
RUN cd cli && CGO_ENABLED=0 go build \
    -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${DATE}" \
    -o /build/stackctl .

# Stage 2: Runtime
FROM alpine:3.24@sha256:28bd5fe8b56d1bd048e5babf5b10710ebe0bae67db86916198a6eec434943f8b

RUN apk add --no-cache ca-certificates && \
    adduser -D -h /home/stackctl stackctl

COPY --from=builder /build/stackctl /usr/local/bin/stackctl

USER stackctl

ENTRYPOINT ["stackctl"]
