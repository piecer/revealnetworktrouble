FROM golang@sha256:1699c10032ca2582ec89a24a1312d986a3f094aed3d5c1147b19880afe40e052 AS build
ARG VERSION
ARG REVISION
ARG SOURCE_DATE_EPOCH
WORKDIR /src
COPY go.mod ./
COPY backend ./backend
COPY cmd ./cmd
RUN test -n "${SOURCE_DATE_EPOCH}" && \
    CGO_ENABLED=0 SOURCE_DATE_EPOCH="${SOURCE_DATE_EPOCH}" go build -trimpath -buildvcs=false \
      -ldflags="-s -w -buildid= -X main.version=${VERSION} -X main.revision=${REVISION}" \
      -o /checknetwork-api ./cmd/checknetwork-api && \
    test "$(go version -m /checknetwork-api | sed -n '1p')" = '/checknetwork-api: go1.22.12' && \
    touch -d "@${SOURCE_DATE_EPOCH}" /checknetwork-api

FROM alpine@sha256:d9e853e87e55526f6b2917df91a2115c36dd7c696a35be12163d44e6e2a4b6bc AS traceroute
ADD --checksum=sha256:0409f348956dc74b83701cf48294f58ab6424e6f717e0f912687bb89d09026de https://dl-cdn.alpinelinux.org/alpine/v3.20/community/x86_64/traceroute-2.1.5-r0.apk /tmp/traceroute.apk
RUN tar -xzf /tmp/traceroute.apk -C / usr/bin/traceroute && \
    rm -f /tmp/traceroute.apk && \
    test -x /usr/bin/traceroute

FROM alpine@sha256:d9e853e87e55526f6b2917df91a2115c36dd7c696a35be12163d44e6e2a4b6bc
ARG VERSION
ARG REVISION
ARG SOURCE_DATE_EPOCH
LABEL org.opencontainers.image.version=$VERSION \
      org.opencontainers.image.revision=$REVISION
COPY --from=traceroute /usr/bin/traceroute /traceroute
ENV PATH="/:${PATH}"
RUN test -n "$SOURCE_DATE_EPOCH" && test -x /traceroute
COPY --from=build /checknetwork-api /checknetwork-api
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/checknetwork-api"]
