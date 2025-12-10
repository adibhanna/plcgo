# Runtime stage - GoReleaser provides the pre-built binary
FROM alpine:3.19

# Install runtime dependencies
RUN apk add --no-cache ca-certificates tzdata

# Create non-root user
RUN adduser -D -g '' plcgo

# Set working directory
WORKDIR /app

# Copy pre-built binary from GoReleaser
COPY plcgo /app/plcgo

# Set ownership
RUN chown -R plcgo:plcgo /app

# Switch to non-root user
USER plcgo

# Expose GraphQL port
EXPOSE 4000

# Health check
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:4000/health || exit 1

# Set entrypoint
ENTRYPOINT ["/app/plcgo"]

# Default command
CMD ["--config", "/app/config/config.json"]
