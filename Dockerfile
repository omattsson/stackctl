# Stage 1: Build
FROM golang:1.27.4-alpine AS builder

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
FROM alpine:3.24@sha256:f59abe656b692d901e184be3445e084786602108599d2a51d229c0f5a48c71bf

RUN apk add --no-cache ca-certificates && \
    adduser -D -h /home/stackctl stackctl

COPY --from=builder /build/stackctl /usr/local/bin/stackctl

USER stackctl

ENTRYPOINT ["stackctl"]
