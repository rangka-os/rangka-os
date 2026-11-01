# The rangka binary (built by goreleaser, web + templates bundled inside) on a distroless base.
#   docker run --rm -p 8080:8080 -v "$PWD:/srv" -e DATABASE_URL=postgres://… ghcr.io/rangka-os/rangka serve
# /srv is the project folder (apps/, site/, connectors/, fixtures/, .env), as written by
# `npm create @rangka-os`.
FROM gcr.io/distroless/static-debian12:nonroot
COPY rangka /usr/local/bin/rangka
WORKDIR /srv
ENV RANGKA_ADDR=:8080
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/rangka"]
CMD ["serve"]
