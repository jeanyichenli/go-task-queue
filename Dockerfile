# syntax=docker/dockerfile:1.7

FROM golang:1.25.6-alpine AS builder
WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/go-task-queue ./cmd/go-task-queue

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app

COPY --from=builder /out/go-task-queue /app/go-task-queue
COPY config/handlers.json /app/config/handlers.json

EXPOSE 8080
ENTRYPOINT ["/app/go-task-queue"]
