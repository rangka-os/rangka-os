# Rangka ID and access

Sign-in is built into the kernel; no separate identity server is needed.

- **Password login** with cookie sessions: `POST /auth/login {email, password}`, `POST /auth/logout`, `GET /auth/me`.
- **API tokens** (`rk_…`) for scripts and AI agents: `POST /api/v1/tokens`, then `Authorization: Bearer rk_…`.
- **OpenID Connect provider**, so Omilo and other apps sign in with the same account: discovery at
  `/.well-known/openid-configuration`, `/oauth/authorize` (authorization code + PKCE S256), `/oauth/token`,
  `/oauth/userinfo`, `/oauth/jwks` (RS256 key generated on first run). Clients are listed in `system.rk` under
  `auth.clients`; MCP access needs the `rangka.mcp` scope.
- **Several organisations per person**: `GET /auth/me` lists every membership, and `POST /auth/org {"org": "<id>"}`
  switches a browser session to another one. API and OIDC tokens are fixed to one organisation.
- **Members** are added with `rangka user add <email> --org <id> --role <role>` ([CLI](/reference/cli)).

Roles come from `modules/<module>/roles/*.rk` (the shared ones from `modules/users/roles/`). A role with `scope:` limits
its members to the records of their own outlets. Owners and the roles in `config.ai.approvers` approve change sets, from
a browser session.

v0 status: passwords use bcrypt (PBKDF2 hashes from earlier builds are upgraded at the next sign-in). Model
`permissions:` are enforced for people, API tokens and MCP alike (see [CLI](/reference/cli#permissions)). Not built yet:
passkeys, two-factor sign-in, OIDC refresh tokens, and upstream sign-in (Google Workspace, Microsoft Entra ID, Okta,
Keycloak) per organisation domain; `system.rk auth.upstream`, `auth.two_factor` and `config.rk sso` are in the schema
but the kernel does not act on them yet.
