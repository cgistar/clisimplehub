FROM --platform=$BUILDPLATFORM golang:1.24 AS builder

WORKDIR /src

# Copy all source files first
COPY . .

# Generate go.sum and download dependencies
RUN go mod tidy && go mod download

ARG TARGETOS
ARG TARGETARCH

RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
    go build -trimpath -ldflags "-s -w" -o /out/cliSimpleHub-server ./cmd/server

FROM alpine:latest

# Install ca-certificates for HTTPS
RUN apk --no-cache add ca-certificates tzdata

# Create non-root user
RUN addgroup -g 1000 appuser && \
    adduser -D -u 1000 -G appuser appuser

# Create data directory with proper permissions
RUN mkdir -p /data && chown -R appuser:appuser /data

WORKDIR /data

COPY --from=builder /out/cliSimpleHub-server /app/cliSimpleHub-server

# Switch to non-root user
USER appuser

EXPOSE 5600

ENTRYPOINT ["/app/cliSimpleHub-server"]
