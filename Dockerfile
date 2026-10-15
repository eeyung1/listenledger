# Multi-stage production container build for ListenLedger
# Reference: DATABASE_MIGRATION.md §7

# Stage 1: Build TypeScript frontend
FROM node:20-alpine AS frontend-builder
WORKDIR /build/frontend
COPY frontend/package*.json ./
RUN npm ci
COPY frontend/ ./
RUN npm run build

# Stage 2: Build backend Go binary with CGO (mattn/go-sqlite3)
FROM golang:1.25-alpine AS backend-builder
RUN apk add --no-cache build-base
WORKDIR /build/backend
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
RUN CGO_ENABLED=1 GOOS=linux go build -ldflags="-s -w" -o /build/bin/listenledger .

# Stage 3: Minimal, secure runtime container
FROM alpine:3.20 AS runner
RUN apk add --no-cache ca-certificates tzdata wget
WORKDIR /app

# Non-root user for security (principle of least privilege)
RUN addgroup -S listenledger && adduser -S listenledger -G listenledger

# Copy binary and frontend assets
COPY --from=backend-builder /build/bin/listenledger /app/bin/listenledger
COPY --from=frontend-builder /build/frontend /app/frontend

# Ensure listenledger user owns runtime working files
RUN chown -R listenledger:listenledger /app

USER listenledger:listenledger

ENV PORT=8080
EXPOSE 8080

HEALTHCHECK --interval=15s --timeout=3s --start-period=5s --retries=3 \
  CMD wget --no-verbose --tries=1 --spider http://127.0.0.1:8080/healthz || exit 1

ENTRYPOINT ["/app/bin/listenledger", "-frontend", "/app/frontend"]

