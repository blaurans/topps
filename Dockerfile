# syntax=docker/dockerfile:1

# ---- build ------------------------------------------------------------------
# modernc.org/sqlite est du Go pur : CGO reste désactivé et le binaire
# est statique, ce qui évite toute toolchain C dans l'image finale.
FROM golang:1.27-alpine AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go vet ./... \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /topps .

# ---- runtime ----------------------------------------------------------------
# Aucun `apk add` : l'application ne sort pas en HTTPS (donc pas besoin
# de ca-certificates) et journalise en UTC (donc pas besoin de tzdata).
# `adduser` vient de busybox. L'image finale ne dépend donc d'aucun
# miroir de paquets au moment du build.
FROM alpine:3.22
RUN adduser -D -H -u 10001 topps \
 && mkdir -p /data/thumbnails \
 && chown -R topps:topps /data

COPY --from=build /topps /usr/local/bin/topps

USER topps
ENV TOPPS_ADDR=:8080 \
    TOPPS_DB=/data/topps.db \
    TOPPS_THUMBS=/data/thumbnails
EXPOSE 8080
VOLUME ["/data"]

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s \
  CMD wget -qO- http://127.0.0.1:8080/healthz >/dev/null || exit 1

ENTRYPOINT ["topps"]
