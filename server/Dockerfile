FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -ldflags "-s -w -X main.version=${VERSION}" -o /przywolania-serwer .

FROM alpine:3.20
RUN apk add --no-cache tzdata ca-certificates su-exec \
 && addgroup -S -g 1001 przyw && adduser -S -D -H -u 1001 -G przyw przyw
ENV TZ=Europe/Warsaw PRZYW_DATA=/data
COPY --from=build /przywolania-serwer /usr/local/bin/przywolania-serwer
VOLUME /data
COPY docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
RUN chmod 755 /usr/local/bin/docker-entrypoint.sh
EXPOSE 8080 8443
ENTRYPOINT ["docker-entrypoint.sh"]
