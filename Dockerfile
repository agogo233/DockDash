FROM node:26-alpine AS client
WORKDIR /app
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml ./
RUN apk add --no-cache python3 make g++ && \
    npm install --global pnpm@11.20.0 && pnpm install --frozen-lockfile
COPY . .
RUN pnpm build

FROM golang:1.24-alpine AS builder
RUN apk add --no-cache gcc musl-dev
WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
COPY drizzle ./drizzle
COPY --from=client /app/dist/client ./internal/assets/web
RUN CGO_ENABLED=1 go build -mod=mod -tags "netgo,osusergo" -ldflags '-s -w -extldflags "-static"' -o /out/dockdash ./cmd/dockdash

FROM gcr.io/distroless/static-debian12
ARG APP_REPO
ARG APP_VERSION=dev
WORKDIR /app
COPY --from=builder /out/dockdash ./dockdash
COPY --from=builder /src/drizzle ./drizzle
USER 0
VOLUME ["/app/data"]
EXPOSE 3001
ENV LOG_LEVEL=info \
    NODE_ENV=production \
    APP_REPO=$APP_REPO \
    APP_VERSION=$APP_VERSION \
    PORT=3001 \
    DOCKER_HOSTS=unix:///var/run/docker.sock \
    NETWORK_CIDRS=192.168.0.0/24 \
    DB_PATH=/app/data/dockdash.db \
    HEALTH_CHECK_INTERVAL=30000 \
    RESOURCE_MONITOR_INTERVAL=5000 \
    UPDATE_CHECK_INTERVAL=3600000
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD ["/app/dockdash", "healthcheck"]
CMD ["/app/dockdash"]
