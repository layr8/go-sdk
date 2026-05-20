FROM golang:1.25-alpine AS builder
WORKDIR /app

# Copy SDK source (replace directive resolves to ../)
COPY go.mod go.sum ./
COPY *.go ./
COPY examples/ ./examples/
COPY tests/ ./tests/

# Copy compat module
COPY compat/ ./compat/

WORKDIR /app/compat
RUN go mod download
RUN CGO_ENABLED=0 go build -o /compat ./cmd/compat/

FROM alpine:3.19
RUN apk add --no-cache ca-certificates
COPY --from=builder /compat /usr/local/bin/compat

LABEL org.opencontainers.image.source=https://github.com/layr8/go-sdk

ENTRYPOINT ["compat"]