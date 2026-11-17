FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ENV CGO_ENABLED=0
RUN go build -trimpath -ldflags="-s -w" -o /out/hyw-official-reporter .

FROM alpine:3.24
RUN apk add --no-cache ca-certificates su-exec && addgroup -S app && adduser -S -G app app
WORKDIR /app
COPY --from=build /out/hyw-official-reporter /usr/local/bin/hyw-official-reporter
COPY --chmod=755 docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
RUN mkdir -p /app/data && chown -R app:app /app
VOLUME ["/app/data"]
ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
CMD ["hyw-official-reporter"]
