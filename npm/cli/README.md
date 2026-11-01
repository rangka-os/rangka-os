# @rangka-os/cli

The [Rangka OS](https://rangka-os.github.io/rangka-os/) kernel as an npm package: one native binary that
validates `.rk` metadata, migrates Postgres, seeds data and serves the REST API, realtime events, MCP and
the Console.

```bash
npm create @rangka-os@latest my-shop   # start a project (recommended)
npx @rangka-os/cli version             # or run the CLI directly
```

Commands: `rangka validate`, `rangka migrate`, `rangka seed`, `rangka serve`, `rangka version`.
Settings come from flags, the environment or a `.env` file in the current directory (`DATABASE_URL`,
`RANGKA_APPS`, `RANGKA_SITE`, `RANGKA_CONNECTORS`, `RANGKA_ORG`, …).

npm installs only the binary for your platform (`@rangka-os/cli-<os>-<arch>`). Rangka OS is a v0 preview:
not for production yet. Licence: Elastic License 2.0 (ELv2).
