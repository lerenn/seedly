# syntax=docker/dockerfile:1

FROM node:22-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.25-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/web/dist ./web/dist
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /out/seedly ./cmd/seedly

FROM alpine:3.21
ARG VERSION=dev
ARG REVISION=unknown
ARG CREATED=unknown
LABEL org.opencontainers.image.title="Seedly" \
      org.opencontainers.image.description="Open-source seedbox" \
      org.opencontainers.image.source="https://github.com/lerenn/seedly" \
      org.opencontainers.image.url="https://github.com/lerenn/seedly" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${REVISION}" \
      org.opencontainers.image.created="${CREATED}"
RUN apk add --no-cache ca-certificates wget \
  && adduser -D -u 65532 seedly
WORKDIR /
COPY --from=build /out/seedly /seedly
COPY --from=build /src/web/dist /web/dist
RUN mkdir -p /data/db /data/meta /data/downloads \
  && chown -R seedly:seedly /data
ENV SEEDLY_WEB_PATH=/web/dist \
    SEEDLY_DB_PATH=/data/db/seedly.db \
    SEEDLY_META_PATH=/data/meta \
    SEEDLY_DOWNLOADS_PATH=/data/downloads \
    SEEDLY_LISTEN=:8080
EXPOSE 8080
USER seedly
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD wget -qO- http://127.0.0.1:8080/api/health || exit 1
ENTRYPOINT ["/seedly"]
