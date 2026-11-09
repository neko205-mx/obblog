FROM golang:1.25-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/obblog .

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends git openssh-client ca-certificates && rm -rf /var/lib/apt/lists/* \
    && groupadd -g 10001 obblog && useradd -u 10001 -g obblog -M obblog \
    && mkdir /data && chown obblog:obblog /data
COPY --from=build /out/obblog /usr/local/bin/obblog
USER obblog
WORKDIR /data
VOLUME /data
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/obblog"]
CMD ["-data", "/data", "-addr", "0.0.0.0:8080"]
