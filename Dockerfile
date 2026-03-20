FROM golang:1.25-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -o bale-check .

FROM alpine:3.20

WORKDIR /app

COPY --from=builder /app/bale-check .
COPY config.yaml .

EXPOSE 443

ENTRYPOINT ["./bale-check", "--config", "/app/config.yaml"]
