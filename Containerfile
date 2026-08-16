FROM docker.io/library/golang:1.25-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
COPY cmd/websideband ./cmd/websideband
COPY internal/store ./internal/store
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/websideband ./cmd/websideband

FROM docker.io/library/alpine:3.22
RUN apk add --no-cache ffmpeg && addgroup -S -g 10001 websideband && adduser -S -D -H -u 10001 -G websideband websideband && mkdir /data && chown websideband:websideband /data
COPY --from=build /out/websideband /usr/local/bin/websideband
USER 10001:10001
VOLUME ["/data"]
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD wget -q --spider http://127.0.0.1:8080/healthz || exit 1
ENTRYPOINT ["/usr/local/bin/websideband"]
