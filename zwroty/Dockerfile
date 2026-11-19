FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -ldflags "-s -w -X main.version=${VERSION}" -o /zwroty-serwer .

FROM alpine:3.20
RUN apk add --no-cache tzdata ca-certificates
ENV TZ=Europe/Warsaw ZWROTY_DATA=/data
COPY --from=build /zwroty-serwer /usr/local/bin/zwroty-serwer
VOLUME /data
EXPOSE 8081
ENTRYPOINT ["zwroty-serwer"]
