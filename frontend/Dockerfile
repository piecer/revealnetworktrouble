FROM nginx@sha256:65645c7bb6a0661892a8b03b89d0743208a18dd2f3f17a54ef4b76fb8e2f2a10
ARG VERSION
ARG REVISION
ARG SOURCE_DATE_EPOCH
LABEL org.opencontainers.image.version="$VERSION" \
      org.opencontainers.image.revision="$REVISION"
RUN test -n "$VERSION" && \
    printf '%s\n' "$VERSION" | grep -Eq '^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-([0-9A-Za-z-]+\.)*[0-9A-Za-z-]+)?(\+([0-9A-Za-z-]+\.)*[0-9A-Za-z-]+)?$' || \
      { printf '%s\n' 'VERSION must be valid SemVer' >&2; exit 1; }
RUN case "$REVISION" in *[!0-9a-f]*) false ;; esac && \
    test "${#REVISION}" -eq 40 || \
      { printf '%s\n' 'revision must be 40 lowercase hexadecimal characters' >&2; exit 1; }
RUN case "$SOURCE_DATE_EPOCH" in ''|*[!0-9]*) \
      printf '%s\n' 'SOURCE_DATE_EPOCH must be a non-empty decimal integer' >&2; exit 1 ;; \
    esac
RUN rm -rf /usr/share/nginx/html/* /usr/share/nginx/html/.[!.]* /usr/share/nginx/html/..?*
COPY nginx.conf /etc/nginx/nginx.conf
COPY index.html styles.css app.js state.js topology-model.js topology-renderer.js topology-visualizer.js /usr/share/nginx/html/
RUN cd /usr/share/nginx/html && \
    sha256sum app.js index.html state.js styles.css topology-model.js topology-renderer.js topology-visualizer.js | sort -k2 > .checknetwork-assets.sha256 && \
    touch -d "@$SOURCE_DATE_EPOCH" /etc/nginx/nginx.conf . .checknetwork-assets.sha256 \
      app.js index.html state.js styles.css topology-model.js topology-renderer.js topology-visualizer.js
EXPOSE 80
