# syntax=docker/dockerfile:1
# ==============================================================================
# Stage 1: Build binary with BuildKit module & compiler cache
# ==============================================================================
FROM golang:1.27.2-alpine AS builder

WORKDIR /app

# Cache Go modules layer separately
COPY go.mod go.sum* ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

# 构建期注入的版本身份（GET /api/version 用它回答"线上跑的是哪一版"）：
# GIT_SHA / APP_VERSION 由编排（docker-compose 的 build args）、deploy.sh 与 release.yml 传入；
# 本机直接 docker build 时是 unknown——不猜，也不留空（空串与"没注入"无法区分）。
# 构建时间在构建现场取，调用方不必再传一个参数。容器里没有 .git（.dockerignore 排除），
# 因此只有编译进二进制才靠得住。
ARG GIT_SHA=unknown
ARG APP_VERSION=unknown

# Copy source code and build using compiler caches
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod tidy && \
    CGO_ENABLED=0 GOOS=linux go build \
      -ldflags="-w -s \
        -X github.com/metafusion/metafusion-app/internal/catalog.buildGitSHA=${GIT_SHA} \
        -X github.com/metafusion/metafusion-app/internal/catalog.buildVersion=${APP_VERSION} \
        -X github.com/metafusion/metafusion-app/internal/catalog.buildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
      -o /app/bin/server ./cmd/server && \
    CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /app/bin/migrate ./cmd/migrate

# ==============================================================================
# Stage 2: Database Migrator Runtime
# ==============================================================================
FROM alpine:3.19 AS migrator

WORKDIR /app
RUN apk add --no-cache ca-certificates tzdata curl

COPY --from=builder /app/bin/migrate /app/migrate

ENTRYPOINT ["/app/migrate"]
CMD ["up"]

# ==============================================================================
# Stage 3: Server Runtime (API Server)
# ==============================================================================
FROM alpine:3.19 AS server

WORKDIR /app
RUN apk add --no-cache ca-certificates tzdata curl

COPY --from=builder /app/bin/server /app/server
COPY --from=builder /app/bin/migrate /app/migrate

EXPOSE 8080
ENTRYPOINT ["/app/server"]
