FROM golang:1.25-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG TARGET
RUN CGO_ENABLED=0 go build -o /app ./cmd/${TARGET}

FROM alpine:latest
RUN apk add --no-cache wget
COPY --from=builder /app /app
ENTRYPOINT ["/app"]
