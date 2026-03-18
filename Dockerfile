# Build stage
FROM golang:1.24-alpine AS builder

RUN apk add --no-cache git make

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /agentanycastd ./cmd/agentanycastd

# Runtime stage
FROM alpine:3.20

RUN apk add --no-cache ca-certificates
COPY --from=builder /agentanycastd /usr/local/bin/agentanycastd

EXPOSE 50051/tcp

ENTRYPOINT ["/usr/local/bin/agentanycastd"]
