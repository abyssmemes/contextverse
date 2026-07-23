# syntax=docker/dockerfile:1
# Template Dockerfile for contextd server.
# Status: in development — not built or published by CI yet.
#
#   docker build -f deploy/docker/Dockerfile -t contextd:local .

FROM golang:1.25-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/contextd ./cmd/contextd

# Alpine keeps a shell so the first-boot entrypoint can run init.
FROM alpine:3.20
RUN apk add --no-cache ca-certificates \
  && adduser -D -u 65532 -h /data contextd
COPY --from=build /out/contextd /usr/local/bin/contextd
COPY deploy/docker/entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh \
  && mkdir -p /data \
  && chown -R contextd:contextd /data
USER contextd
EXPOSE 8743
VOLUME ["/data"]
ENV CONTEXTD_DATA_DIR=/data \
    CONTEXTD_LISTEN_ADDRESS=0.0.0.0 \
    CONTEXTD_LISTEN_PORT=8743
ENTRYPOINT ["/entrypoint.sh"]
