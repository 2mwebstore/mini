# syntax=docker/dockerfile:1
# Single-container image for platforms that run one service per app (Railway, Render, Fly…):
# the Go API also serves the HR admin panel at /admin/ and the Telegram Mini App at /app/,
# all on one domain. MySQL, Redis and S3 photo storage are separate services.
# (docker compose uses the per-app Dockerfiles behind nginx instead.)
# No `apk add` anywhere: builds must not depend on the Alpine package mirror (it failed
# on Railway's builders). Both Alpine images already ship CA certificates; Go fetches
# modules through proxy.golang.org (no git) and the time zones are compiled in.
# Downloads that remain (npm, Go modules) are retried.

FROM node:22-alpine AS admin
WORKDIR /src
COPY admin/package.json admin/package-lock.json ./
RUN npm ci --no-audit --no-fund --ignore-scripts --fetch-retries=5 --fetch-retry-maxtimeout=120000
COPY admin/ .
RUN npm run build

FROM node:22-alpine AS miniapp
WORKDIR /src
COPY miniapp/package.json miniapp/package-lock.json ./
RUN npm ci --no-audit --no-fund --ignore-scripts --fetch-retries=5 --fetch-retry-maxtimeout=120000
COPY miniapp/ .
RUN npm run build

FROM golang:1.24-alpine AS api
WORKDIR /src
COPY backend/go.mod backend/go.sum ./
# retried: one network timeout on the build machine should not fail the deploy
RUN for i in 1 2 3 4; do go mod download && exit 0; echo "go mod download failed, retry $i in $((i * 10))s"; sleep $((i * 10)); done; exit 1
COPY backend/ .
# timetzdata: Asia/Phnom_Penh etc. built in (the runtime image has no tzdata package)
RUN CGO_ENABLED=0 go build -trimpath -tags timetzdata -ldflags="-s -w" -o /out/api ./cmd/api

FROM alpine:3.20
RUN adduser -D -u 10001 app
COPY --from=api /out/api /app/api
COPY --from=admin /src/.output/public /app/web/admin
COPY --from=miniapp /src/.output/public /app/web/app
ENV APP_ENV=production WEB_DIR=/app/web
USER app
# Listens on $PORT when the platform sets it (Railway does), else 8080.
EXPOSE 8080
ENTRYPOINT ["/app/api"]
CMD ["serve"]
