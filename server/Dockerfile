FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -ldflags "-s -w -X main.version=${VERSION}" -o /przywolania-serwer .

FROM alpine:3.20
RUN apk add --no-cache tzdata ca-certificates
ENV TZ=Europe/Warsaw PRZYW_DATA=/data
COPY --from=build /przywolania-serwer /usr/local/bin/przywolania-serwer
VOLUME /data
EXPOSE 8080
ENTRYPOINT ["przywolania-serwer"]
