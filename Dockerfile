FROM node:24-alpine AS web
WORKDIR /src/web
COPY web/package*.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.27.1-alpine AS go
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
COPY VERSION ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X github.com/xiaoliu-heng/homefleet/internal/model.Version=$(cat VERSION)" -o /out/homefleet-hub ./cmd/hub
RUN mkdir -p /out/releases && for target in linux/amd64 linux/arm64 darwin/arm64 windows/amd64; do \
  os=${target%/*}; arch=${target#*/}; suffix=""; if [ "$os" = windows ]; then suffix=".exe"; fi; \
  GOOS=$os GOARCH=$arch CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X github.com/xiaoliu-heng/homefleet/internal/model.Version=$(cat VERSION)" -o "/out/releases/homefleet-agent-$os-$arch$suffix" ./cmd/agent; \
  done
RUN go run ./cmd/release -dir /out/releases -version "$(cat VERSION)"

FROM alpine:3.23
RUN apk add --no-cache ca-certificates tzdata && addgroup -g 10001 fleet && adduser -D -u 10001 -G fleet fleet \
 && mkdir -p /data && chown fleet:fleet /data
WORKDIR /app
COPY --from=go /out/homefleet-hub /app/homefleet-hub
COPY --from=go /out/releases /app/releases
COPY --from=web /src/web/dist /app/web
COPY scripts/bootstrap-* scripts/install-* scripts/uninstall-* /app/releases/
COPY LICENSE THIRD_PARTY_NOTICES.md /app/releases/
RUN cd /app/releases && sha256sum homefleet-agent-* agent-release.json bootstrap-* install-* uninstall-* LICENSE THIRD_PARTY_NOTICES.md > SHA256SUMS
RUN apk add --no-cache libcap && setcap cap_net_raw=ep /app/homefleet-hub
USER fleet
ENV HOMEFLEET_LISTEN=0.0.0.0:8080 HOMEFLEET_DB=/data/homefleet.db HOMEFLEET_WEB=/app/web HOMEFLEET_RELEASES=/app/releases
EXPOSE 8080
ENTRYPOINT ["/app/homefleet-hub"]
