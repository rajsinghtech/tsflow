# Multi-stage build for TSFlow

# Frontend build stage
FROM node:20.17-alpine AS frontend-build

WORKDIR /app/frontend

COPY frontend/package*.json ./
RUN npm install

COPY frontend/ ./
RUN npm run build

# Backend build stage
FROM golang:1.25-alpine AS backend-build

WORKDIR /app

RUN apk add --no-cache git

# Copy root go.mod for the embed package
COPY go.mod ./
COPY embed.go ./

# Copy backend module
COPY backend/ ./backend/

# Copy built frontend for embedding
COPY --from=frontend-build /app/frontend/dist ./frontend/dist

# Build from backend directory
WORKDIR /app/backend
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o ../tsflow-backend ./main.go

# Runtime stage
FROM alpine:3.20

RUN apk --no-cache add ca-certificates

WORKDIR /app

COPY --from=backend-build /app/tsflow-backend ./

# Set default environment to production
ENV ENVIRONMENT=production

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD wget --no-verbose --tries=1 --spider http://localhost:8080/health || exit 1

CMD ["./tsflow-backend"] 