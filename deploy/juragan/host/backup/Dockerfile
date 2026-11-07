# Backup tools for the tenant host: pg_dump/pg_restore 16, rclone, age, jq, GNU find/tar/coreutils and the rangka
# binary, running the same backup.sh / restore.sh / lib.sh as the pilot deploy. Build context: deploy/juragan.
#   docker build -f host/backup/Dockerfile --build-arg KERNEL_IMAGE=juragan-tenant-kernel:<tag>-slim -t juragan-host-backup:<tag> .
ARG KERNEL_IMAGE
FROM ${KERNEL_IMAGE} AS rangka

FROM postgres:16-alpine
RUN apk add --no-cache bash rclone age jq curl findutils coreutils tar
COPY --from=rangka /usr/local/bin/rangka /usr/local/bin/rangka
COPY backup/lib.sh backup/backup.sh backup/restore.sh /usr/local/bin/
RUN chmod 0755 /usr/local/bin/backup.sh /usr/local/bin/restore.sh
ENTRYPOINT ["/usr/local/bin/backup.sh"]
