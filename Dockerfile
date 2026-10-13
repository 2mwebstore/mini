# syntax=docker/dockerfile:1
# Single-container image for platforms that run one service per app (Railway, Render, Fly…):
# the Go API also serves the HR admin panel at /admin/ and the Telegram Mini App at /app/,
# all on one domain. MySQL, Redis and S3 photo storage are separate services.
# (docker compose uses the per-app Dockerfiles behind nginx instead.)

FROM node:22-alpine AS admin
WORKDIR /src
COPY admin/package.json admin/package-lock.json ./
RUN npm ci --no-audit --no-fund --ignore-scripts
COPY admin/ .
RUN npm run build

FROM node:22-alpine AS miniapp
WORKDIR /src
COPY miniapp/package.json miniapp/package-lock.json ./
RUN npm ci --no-audit --no-fund --ignore-scripts
COPY miniapp/ .
RUN npm run build

FROM golang:1.24-alpine AS api
WORKDIR /src
RUN apk add --no-cache git ca-certificates
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata && adduser -D -u 10001 app
COPY --from=api /out/api /app/api
COPY --from=admin /src/.output/public /app/web/admin
COPY --from=miniapp /src/.output/public /app/web/app
ENV APP_ENV=production WEB_DIR=/app/web
USER app
# Listens on $PORT when the platform sets it (Railway does), else 8080.
EXPOSE 8080
ENTRYPOINT ["/app/api"]
CMD ["serve"]
