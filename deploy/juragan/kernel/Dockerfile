# Pilot kernel image: the release binary plus headless Chromium, which the kernel runs for
# `GET /api/v1/r/{model}/{name}/print?as=pdf` (contract 0008). The release image is distroless and stays so;
# pilots need a browser next to the binary, so they get this one.
#   docker build --build-arg RANGKA_IMAGE=ghcr.io/rangka-os/rangka:<tag> -t juragan-kernel:<tag> deploy/juragan/kernel
ARG RANGKA_IMAGE
FROM ${RANGKA_IMAGE} AS rangka

FROM debian:12.13-slim
# chromium: PDF output. fonts-noto-core: Latin text (Indonesian needs nothing more). ca-certificates: OIDC/HTTPS calls.
RUN apt-get update \
 && apt-get install -y --no-install-recommends chromium fonts-noto-core ca-certificates \
 && rm -rf /var/lib/apt/lists/*
COPY --from=rangka /usr/local/bin/rangka /usr/local/bin/rangka
# The kernel runs as 65532, same as the release image. Chromium wants a writable home and config/cache dirs:
# /tmp (the kernel also gives every render its own --user-data-dir under /tmp).
ENV HOME=/tmp XDG_CONFIG_HOME=/tmp/.config XDG_CACHE_HOME=/tmp/.cache \
    RANGKA_ADDR=:8080 RANGKA_CHROMIUM=/usr/bin/chromium
USER 65532:65532
WORKDIR /srv
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/rangka"]
CMD ["serve"]
