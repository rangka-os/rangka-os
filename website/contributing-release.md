# Releasing

A release is one tag. `.github/workflows/release.yml` then:

1. runs the kernel tests that need no database (`go test ./...`) and checks that the npm starter stages and its
   templates validate (`npm/scripts/check-staged.sh`);
2. builds `rangka` for macOS, Linux and Windows (amd64 and arm64) with GoReleaser, with `web/` and
   `templates/` bundled inside the binary (`kernel/scripts/bundle-assets.sh`), and attaches the archives and `checksums.txt` to a GitHub Release;
3. pushes the multi-arch image `ghcr.io/rangka-os/rangka:<version>` (and `:latest` for non-pre-releases), built from
   `deploy/Dockerfile`;
4. stages and publishes the npm packages from that build:

| Package | What |
| --- | --- |
| `@rangka-os/cli` | The `rangka` command: a small launcher that runs the native binary |
| `@rangka-os/cli-<os>-<arch>` | One per platform, holding the binary; npm installs only the matching one |
| `@rangka-os/create` | `npm create @rangka-os` — writes a project with the apps, connectors and a demo or blank site of this release |

Pre-release versions (`v0.5.0-alpha.1`) go to the npm `next` tag and are marked pre-release on GitHub.

Released so far: `v0.5.0-alpha.1` to `v0.5.0-alpha.5` (3–4 Nov 2026), all pre-releases. On npm, `next` is
`0.5.0-alpha.5`; `latest` still points at `0.5.0-alpha.2`, published before pre-releases moved to `next`.

## One-time setup

The npm token and workflow permissions are in place (the pre-releases above went out with them).


- npm: the `@rangka-os` organisation, and an **automation token** with publish rights saved as the
  repository secret `NPM_TOKEN`. Packages are published with provenance.
- GitHub: Settings › Actions › General › Workflow permissions: read and write (for the Release and ghcr.io).
- Make the `rangka` container package public under the organisation's Packages, so `docker pull` works without
  signing in to ghcr.io.

## Cut a release

```bash
git tag v0.5.0-alpha.6 && git push origin v0.5.0-alpha.6
```

## Try it locally first

```bash
goreleaser release --snapshot --clean              # binaries + images in dist/, nothing published
node npm/scripts/stage.mjs --version 0.5.0-alpha.6 --dist dist
node npm/scripts/publish.mjs --dry-run
```
