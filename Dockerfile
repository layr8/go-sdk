# Build stage
FROM golang:1.24-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Build the target binary (default: echo-agent)
ARG TARGET=echo-agent
RUN CGO_ENABLED=0 go build -o /agent ./cmd/${TARGET}

# Runtime stage
FROM alpine:3.19

RUN apk add --no-cache ca-certificates

WORKDIR /app
COPY --from=builder /agent .

CMD ["./agent"]
