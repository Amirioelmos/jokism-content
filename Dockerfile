FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/jokism ./cmd/server

FROM alpine:3.21
RUN adduser -D -u 10001 app && mkdir /data && chown app:app /data
USER app
WORKDIR /data
COPY --from=build /out/jokism /usr/local/bin/jokism
EXPOSE 8080
ENTRYPOINT ["jokism"]
