# Multi-stage build for maxx-next

# Stage 1: Build frontend
FROM node:22-alpine AS frontend-builder

WORKDIR /app/web

# Copy frontend package files
COPY web/package.json web/package-lock.json ./

# Install frontend dependencies
RUN npm ci

# Copy frontend source
COPY web/ ./

# Build frontend
RUN npm run build

# Stage 2: Build backend
FROM golang:1.25-alpine AS backend-builder

# Install build dependencies
RUN apk add --no-cache gcc musl-dev sqlite-dev

WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY cmd/ ./cmd/
COPY internal/ ./internal/

# Build backend binary
RUN CGO_ENABLED=1 GOOS=linux go build -a -installsuffix cgo -ldflags="-s -w" -o maxx cmd/maxx/main.go

# Stage 3: Final runtime image
FROM alpine:latest

# Install runtime dependencies
RUN apk add --no-cache ca-certificates sqlite-libs

WORKDIR /app

# Copy binary from backend builder
COPY --from=backend-builder /app/maxx .

# Copy built frontend from frontend builder
COPY --from=frontend-builder /app/web/dist ./web/dist

# Create directory for data (database, logs, etc.)
RUN mkdir -p /data

# Expose port
EXPOSE 9880

# Run the application
CMD ["./maxx", "-addr", ":9880", "-data", "/data"]
