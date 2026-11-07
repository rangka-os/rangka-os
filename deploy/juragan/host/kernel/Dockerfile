# Tenant kernel image: the rangka binary of one GitHub Release (verified against the release's checksums.txt), on
#   target pdf  (default, production): debian slim + headless Chromium for PDF print (contract 0008), as ../../kernel/Dockerfile
#   target slim (tests, small VMs):    distroless static, no Chromium (PDF answers 501 pdf_unavailable)
# It does not need the GHCR image: on 6 Nov 2026 ghcr.io/rangka-os/rangka answered "unauthorized" to anonymous pulls,
# while the release assets are public.
#   docker build --build-arg RANGKA_TAG=v0.5.0-alpha.8 --target slim -t juragan-tenant-kernel:v0.5.0-alpha.8-slim .
ARG RANGKA_TAG

FROM alpine:3.20 AS fetch
ARG RANGKA_TAG
ARG TARGETARCH
RUN set -eu; \
    : "${RANGKA_TAG:?set RANGKA_TAG to a release tag such as v0.5.0-alpha.8}"; \
    v="${RANGKA_TAG#v}"; f="rangka_${v}_linux_${TARGETARCH}.tar.gz"; \
    base="https://github.com/rangka-os/rangka-os/releases/download/${RANGKA_TAG}"; \
    mkdir -p /fetch /out && cd /fetch; \
    wget -q -O checksums.txt "$base/checksums.txt"; \
    wget -q -O "$f" "$base/$f"; \
    grep "  $f\$" checksums.txt >want.sum; \
    [ "$(wc -l <want.sum)" -eq 1 ] || { echo "no checksum line for $f" >&2; exit 1; }; \
    sha256sum -c want.sum; \
    tar -xzf "$f" rangka; \
    install -m 0755 rangka /out/rangka

FROM gcr.io/distroless/static-debian12:nonroot AS slim
COPY --from=fetch /out/rangka /usr/local/bin/rangka
ENV RANGKA_ADDR=:8080 RANGKA_CHROMIUM=off
WORKDIR /srv
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/rangka"]
CMD ["serve"]

FROM debian:12.13-slim AS pdf
RUN apt-get update \
 && apt-get install -y --no-install-recommends chromium fonts-noto-core ca-certificates \
 && rm -rf /var/lib/apt/lists/*
COPY --from=fetch /out/rangka /usr/local/bin/rangka
ENV HOME=/tmp XDG_CONFIG_HOME=/tmp/.config XDG_CACHE_HOME=/tmp/.cache \
    RANGKA_ADDR=:8080 RANGKA_CHROMIUM=/usr/bin/chromium
USER 65532:65532
WORKDIR /srv
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/rangka"]
CMD ["serve"]
