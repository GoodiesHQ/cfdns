FROM golang:1.23.2 as builder
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY config ./config
RUN CGO_ENABLED=0 GOOS=linux go build -o /app/cfdns ./cmd

FROM scratch
COPY --from=builder /app/cfdns /app/cfdns
WORKDIR /app
ENTRYPOINT ["/app/cfdns"]