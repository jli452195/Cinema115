# syntax=docker/dockerfile:1

# 第一阶段：构建 Vue 前端静态页面。
FROM --platform=$BUILDPLATFORM node:22-alpine AS frontend-builder
WORKDIR /src/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ ./
ARG APP_VERSION=dev
ARG APP_REVISION=""
ARG APP_CREATED=""
RUN npm run build

# 第二阶段：编译 Go 后端，支持 amd64 和 arm64 NAS。
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS backend-builder
WORKDIR /src/backend
COPY backend/go.mod backend/go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY backend/ ./
ARG TARGETOS=linux
ARG TARGETARCH
ARG APP_VERSION=dev
ARG APP_REVISION=""
ARG APP_CREATED=""
# 缓存依赖和编译结果，后续修改业务代码时复用；编译并发与垃圾回收使用 Go 默认值。
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath \
      -ldflags="-s -w -X strm115/backend/internal/buildinfo.Version=$APP_VERSION -X strm115/backend/internal/buildinfo.Revision=$APP_REVISION -X strm115/backend/internal/buildinfo.Created=$APP_CREATED" \
      -o /out/ ./cmd/server ./cmd/dbtool

# 第三阶段：Go 同时提供页面和 API，运行时无需 Node.js 或 Nginx。
FROM alpine:3.24 AS runner
ARG APP_VERSION=dev
ARG APP_REVISION=""
ARG APP_CREATED=""
LABEL org.opencontainers.image.title="Cinema115" \
      org.opencontainers.image.source="https://github.com/jli452195/Cinema115" \
      org.opencontainers.image.version=$APP_VERSION \
      org.opencontainers.image.revision=$APP_REVISION \
      org.opencontainers.image.created=$APP_CREATED
RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -g 1000 cinema \
    && adduser -D -u 1000 -G cinema cinema \
    && mkdir -p /app /data /strm \
    && chown cinema:cinema /app /data /strm
WORKDIR /app
COPY --from=backend-builder /out/server /app/server
COPY --from=backend-builder /out/dbtool /app/dbtool
COPY --from=frontend-builder /src/frontend/dist /app/frontend/dist
USER 1000:1000
ENV GIN_MODE=release DATA_DIR=/data STRM_ROOT=/strm FRONTEND_DIST=/app/frontend/dist TZ=Asia/Shanghai
EXPOSE 3002
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD wget -q -O /dev/null "http://127.0.0.1:${HTTP_PORT:-3002}/api/health" || exit 1
ENTRYPOINT ["/app/server"]
