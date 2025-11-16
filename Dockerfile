# Build stage
FROM golang:1.23-alpine AS builder

# Install build dependencies
RUN apk add --no-cache git make gcc musl-dev

# Set working directory
WORKDIR /build

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY src/ ./src/

# Build the application
RUN go build -ldflags="-w -s" -o pgao ./src/main.go

# Runtime stage
FROM alpine:latest

# Install runtime dependencies
RUN apk --no-cache add ca-certificates postgresql-client tzdata

# Create non-root user
RUN addgroup -g 1000 pgao && \
    adduser -D -u 1000 -G pgao pgao

# Set working directory
WORKDIR /app

# Copy binary from builder
COPY --from=builder /build/pgao .

# Copy sample config (can be overridden with volume mount)
COPY --from=builder /build/src/config/ ./config/

# Change ownership
RUN chown -R pgao:pgao /app

# Switch to non-root user
USER pgao

# Expose API port
EXPOSE 8080

# Health check
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:8080/health || exit 1

# Run the application
ENTRYPOINT ["./pgao"]
