# Releasing

A release is one tag. `.github/workflows/release.yml` then:

1. runs the kernel tests;
2. builds `rangka` for macOS, Linux and Windows (amd64 and arm64) with GoReleaser, with `web/` and
   `templates/` bundled inside the binary, and attaches the archives and `checksums.txt` to a GitHub Release;
3. pushes the image `ghcr.io/rangka-os/rangka:<version>` (and `:latest` for non-pre-releases);
4. stages and publishes the npm packages from that build:

| Package | What |
| --- | --- |
| `@rangka-os/cli` | The `rangka` command: a small launcher that runs the native binary |
| `@rangka-os/cli-<os>-<arch>` | One per platform, holding the binary; npm installs only the matching one |
| `@rangka-os/create` | `npm create @rangka-os` — writes a project with the apps, connectors and a demo or blank site of this release |

Pre-release versions (`v0.5.0-alpha.1`) go to the npm `next` tag and are marked pre-release on GitHub.

## One-time setup

- npm: the `@rangka-os` organisation, and an **automation token** with publish rights saved as the
  repository secret `NPM_TOKEN`.
- GitHub: Settings › Actions › General › Workflow permissions: read and write (for the Release and ghcr.io).
- After the first image push, make the `rangka` package public under the organisation's Packages.

## Cut a release

```bash
git tag v0.5.0-alpha.1 && git push origin v0.5.0-alpha.1
```

## Try it locally first

```bash
goreleaser release --snapshot --clean              # binaries + images in dist/, nothing published
node npm/scripts/stage.mjs --version 0.5.0-alpha.1 --dist dist
node npm/scripts/publish.mjs --dry-run
```
