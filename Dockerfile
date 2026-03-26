# Stage 1: Build
FROM golang:1.26.1-alpine AS builder

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
FROM alpine:3.20

RUN apk add --no-cache ca-certificates && \
    adduser -D -h /home/stackctl stackctl

COPY --from=builder /build/stackctl /usr/local/bin/stackctl

USER stackctl

ENTRYPOINT ["stackctl"]
