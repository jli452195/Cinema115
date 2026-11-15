# syntax=docker/dockerfile:1

# 第一阶段：独立保存前端依赖，供默认分支预热和版本发布复用。
FROM --platform=$BUILDPLATFORM node:22-alpine AS frontend-deps
WORKDIR /src/frontend
COPY frontend/.npmrc frontend/package.json frontend/package-lock.json ./
RUN npm ci

# 第二阶段：构建 Vue 前端静态页面。
FROM frontend-deps AS frontend-builder
COPY frontend/ ./
ARG APP_VERSION=dev
ARG APP_REVISION=""
ARG APP_CREATED=""
RUN npm run build

# 第三阶段：独立保存 Go 模块依赖，不执行应用编译。
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS backend-deps
WORKDIR /src/backend
COPY backend/go.mod backend/go.sum ./
RUN go mod download

# 第四阶段：编译 Go 后端，支持 amd64 和 arm64 NAS。
FROM backend-deps AS backend-builder
COPY backend/ ./
ARG TARGETOS=linux
ARG TARGETARCH
ARG APP_VERSION=dev
ARG APP_REVISION=""
ARG APP_CREATED=""
# 编译缓存供同一构建器复用；依赖直接读取上层的 /go/pkg/mod。
RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath \
      -ldflags="-s -w -X strm115/backend/internal/buildinfo.Version=$APP_VERSION -X strm115/backend/internal/buildinfo.Revision=$APP_REVISION -X strm115/backend/internal/buildinfo.Created=$APP_CREATED" \
      -o /out/ ./cmd/server ./cmd/dbtool

# 第五阶段：Go 同时提供页面和 API，运行时无需 Node.js 或 Nginx。
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
