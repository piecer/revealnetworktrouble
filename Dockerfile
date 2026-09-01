FROM golang:1.22-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY backend ./backend
COPY cmd ./cmd
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /checknetwork-api ./cmd/checknetwork-api

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /checknetwork-api /checknetwork-api
EXPOSE 8080
ENTRYPOINT ["/checknetwork-api"]

