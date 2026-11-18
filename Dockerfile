FROM golang:1.25-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# The version is baked in so the admin panel can compare against releases.
# Defaults keep a plain `docker build` working without extra arguments.
ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown
# Base64 Ed25519 public key. Empty disables self-update but keeps version checks.
ARG SIGNING_PUBKEY=
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags="-s -w \
      -X 'obblog/internal/version.Version=${VERSION}' \
      -X 'obblog/internal/version.Commit=${COMMIT}' \
      -X 'obblog/internal/version.Date=${BUILD_DATE}' \
      -X 'obblog/internal/updater.PublicKey=${SIGNING_PUBKEY}'" \
    -o /out/obblog .

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends git openssh-client ca-certificates && rm -rf /var/lib/apt/lists/* \
    && groupadd -g 10001 obblog && useradd -u 10001 -g obblog -M obblog \
    && mkdir /data /data/bin && chown obblog:obblog /data /data/bin
COPY --from=build /out/obblog /usr/local/bin/obblog
COPY scripts/entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod 0755 /usr/local/bin/entrypoint.sh
USER obblog
WORKDIR /data
VOLUME /data
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
CMD ["-data", "/data", "-addr", "0.0.0.0:8080"]