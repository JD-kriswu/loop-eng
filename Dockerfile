FROM golang:1.22-alpine AS builder

WORKDIR /app

# Install dependencies
RUN apk add --no-cache git

# Copy go mod files
COPY go.mod go.sum* ./
RUN go mod download || true

# Copy source
COPY . .

# Build binaries
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /loopany-server ./cmd/server
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /loopanyd ./cmd/loopanyd
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /loopany ./cmd/loopany

# Runtime image
FROM alpine:3.19

RUN apk add --no-cache ca-certificates tzdata

# Copy binaries
COPY --from=builder /loopany-server /usr/local/bin/
COPY --from=builder /loopanyd /usr/local/bin/
COPY --from=builder /loopany /usr/local/bin/

# Create non-root user
RUN adduser -D -g '' appuser

# Default to server
CMD ["loopany-server"]