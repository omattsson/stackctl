# Stage 1: Build
FROM golang:1.26.2-alpine@sha256:f85330846cde1e57ca9ec309382da3b8e6ae3ab943d2739500e08c86393a21b1 AS builder

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
FROM alpine:3.23@sha256:25109184c71bdad752c8312a8623239686a9a2071e8825f20acb8f2198c3f659

RUN apk add --no-cache ca-certificates && \
    adduser -D -h /home/stackctl stackctl

COPY --from=builder /build/stackctl /usr/local/bin/stackctl

USER stackctl

ENTRYPOINT ["stackctl"]
