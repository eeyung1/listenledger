FROM node:22-alpine AS frontend-builder
WORKDIR /build/frontend
COPY frontend/package*.json ./
RUN npm ci
COPY frontend/ ./
RUN npm run build

FROM golang:1.25-alpine AS backend-builder
RUN apk add --no-cache build-base
WORKDIR /build/backend
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
RUN CGO_ENABLED=1 GOOS=linux go build -ldflags="-s -w" -o /build/bin/listenledger .

FROM alpine:3.22 AS runner
RUN apk add --no-cache ca-certificates tzdata wget
WORKDIR /app
RUN addgroup -S listenledger && adduser -S listenledger -G listenledger
COPY --from=backend-builder /build/bin/listenledger /app/bin/listenledger
COPY --from=frontend-builder /build/frontend/index.html /app/frontend/index.html
COPY --from=frontend-builder /build/frontend/style.css /app/frontend/style.css
COPY --from=frontend-builder /build/frontend/dist /app/frontend/dist
COPY --from=frontend-builder /build/frontend/src/components /app/frontend/src/components
USER listenledger:listenledger
ENV PORT=8080
EXPOSE 8080
HEALTHCHECK --interval=15s --timeout=3s --start-period=5s --retries=3 \
  CMD wget --no-verbose --tries=1 --spider http://127.0.0.1:8080/readyz || exit 1
ENTRYPOINT ["/app/bin/listenledger"]
CMD ["-frontend", "/app/frontend", "-require-postgres", "-skip-migrations"]
