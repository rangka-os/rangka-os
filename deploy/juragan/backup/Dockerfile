# Backup tools: pg_dump 16, rclone, age, jq and the rangka binary (for `rangka backup`, contract 0005 §2).
# docker build --build-arg RANGKA_IMAGE=ghcr.io/rangka-os/rangka:<tag> backup
ARG RANGKA_IMAGE
FROM ${RANGKA_IMAGE} AS rangka

FROM postgres:16-bookworm
RUN apt-get update \
 && apt-get install -y --no-install-recommends rclone age jq curl ca-certificates \
 && rm -rf /var/lib/apt/lists/*
COPY --from=rangka /usr/local/bin/rangka /usr/local/bin/rangka
COPY lib.sh backup.sh restore.sh /usr/local/bin/
RUN chmod 0755 /usr/local/bin/backup.sh /usr/local/bin/restore.sh
ENTRYPOINT ["/usr/local/bin/backup.sh"]
