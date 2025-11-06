FROM --platform=$BUILDPLATFORM golang:1.25 AS builder
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY pkg ./pkg

ARG TARGETOS
ARG TARGETARCH
ENV CGO_ENABLED=0

# Build the binary for the requested OS/arch
RUN GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -o /out/cfdns ./cmd

# Minimal runtime
FROM alpine:latest
RUN apk --no-cache add ca-certificates
WORKDIR /app
COPY --from=builder /out/cfdns /app/cfdns
ENTRYPOINT ["/app/cfdns"]