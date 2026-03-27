# Multi-stage build for TSFlow
# Produces a single binary with embedded SvelteKit frontend

# Frontend build stage
FROM node:25-alpine AS frontend-build

WORKDIR /app/frontend

COPY frontend/package*.json ./
RUN npm ci

COPY frontend/ ./

# SvelteKit builds to ../backend/frontend/dist via adapter config
RUN mkdir -p ../backend/frontend && npm run build

# Backend build stage
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS backend-build

ARG TARGETOS
ARG TARGETARCH

WORKDIR /app/backend

RUN apk add --no-cache git ca-certificates

COPY backend/go.mod backend/go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY backend/ ./
COPY --from=frontend-build /app/backend/frontend/dist ./frontend/dist

# Cross-compile with optimizations
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -ldflags="-w -s" -trimpath -o tsflow .

# Create data directory owned by nonroot user (65532)
RUN mkdir -p /data && chown 65532:65532 /data

# Runtime stage - minimal distroless image
FROM gcr.io/distroless/static:nonroot

WORKDIR /app

COPY --from=backend-build /app/backend/tsflow .

# Data directory for SQLite, pre-created with nonroot ownership
COPY --from=backend-build --chown=65532:65532 /data /app/data
VOLUME /app/data

ENV ENVIRONMENT=production
ENV PORT=8080

EXPOSE 8080

USER 65532:65532

ENTRYPOINT ["/app/tsflow"]
