FROM golang:1.26.4-alpine AS build
WORKDIR /src
RUN apk add --no-cache git ca-certificates
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api

FROM alpine:3.21 AS runtime
RUN apk add --no-cache ca-certificates && \
    adduser -D -u 10001 ruby
COPY --from=build /out/api /usr/local/bin/api
COPY migrations /migrations
USER ruby
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/api"]
