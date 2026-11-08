# MCP tools

The kernel is an MCP server at `POST /mcp` (JSON-RPC 2.0, Streamable HTTP). Authenticate with an `rk_…` API token or
an OIDC access token with the `rangka.mcp` scope. Claude, Omilo, Rangka AI or any MCP client can connect.

| Tool | Arguments | Returns |
| --- | --- | --- |
| `rangka_meta` | — | The same as `GET /api/v1/meta` |
| `rangka_get_file` | `path` | `{path, kind, layer, text, parsed}` |
| `rangka_query` | `model`, `filter?`, `sort?`, `limit?` | Records |
| `rangka_get_record` | `model`, `name` | One record |
| `rangka_dashboard` | `name`, `period?` | Computed widgets |
| `rangka_system_status` | — | The same as `GET /api/v1/system/status` |
| `rangka_changeset_propose` | `title`, `reason`, `ops` | The change set, `status: proposed`, with risk and layers |
| `rangka_changeset_validate` | `id` | Validation result |
| `rangka_changeset_preview` | `id` | Dry-run result |
| `rangka_changeset_status` | `id` | Current state and history |
| `rangka_catalogue` | — | Every app, module and connector with its state for the site (`installed`, `available`, `not_in_plan`); per app the installed modules it `shares` and the modules it `adds` (contract 0035) |
| `rangka_skills` | — | Rangka skills for AI agents: `{skills: [{name, description, source}]}` |
| `rangka_skill` | `name` | One skill: `{name, description, metadata, source, instructions, files}` |
| `rangka_skill_file` | `name`, `path` | One file of a skill: `{name, path, text}` |

Results come as `content: [{type: "text", text: <json>}]` plus `structuredContent`; errors set `isError: true` with
`{error: {code, message}}`.

There is deliberately **no approve or apply tool**: a person approves in the Console or the app.

The skills are the folders under [`skills/`](https://github.com/rangka-os/rangka-os/tree/main/skills) plus each app's
and module's `skills/`, in the Agent Skills format. Any harness can install them; see the README there.
