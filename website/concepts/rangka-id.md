# Rangka ID and access

Sign-in is built into the kernel; no separate identity server is needed.

- **Password login** with cookie sessions: `POST /auth/login {email, password}`, `POST /auth/logout`, `GET /auth/me`.
- **API tokens** (`rk_…`) for scripts and AI agents: `POST /api/v1/tokens`, then `Authorization: Bearer rk_…`.
- **OpenID Connect provider**, so Omilo and other apps sign in with the same account: discovery at
  `/.well-known/openid-configuration`, `/oauth/authorize` (authorization code + PKCE S256), `/oauth/token`,
  `/oauth/userinfo`, `/oauth/jwks` (RS256 key generated on first run). Clients are listed in `system.rk` under
  `auth.clients`; MCP access needs the `rangka.mcp` scope.
- **Upstream sign-in** (Google Workspace, Microsoft Entra ID, Okta, Keycloak) per organisation domain, configured in
  `system.rk auth.upstream` and `config.rk sso`.

Roles come from `apps/<app>/roles/*.rk`; owners and the roles in `config.ai.approvers` approve change sets.

v0 status: passwords use PBKDF2 (the hash format names its algorithm, so it can move to argon2); passkeys, OIDC
refresh tokens, organisation switching and enforcement of model `permissions:` are not done yet.
