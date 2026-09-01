FROM golang:1.22-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY backend ./backend
COPY cmd ./cmd
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /checknetwork-api ./cmd/checknetwork-api

FROM alpine:3.20
RUN apk add --no-cache traceroute
COPY --from=build /checknetwork-api /checknetwork-api
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/checknetwork-api"]
