# .rk schemas

Every kind of `.rk` file, generated from `schema/0.4`.

| Kind | Lives in | What it is |
| --- | --- | --- |
| [app.rk](./app) | `apps/<app>/app.rk` | Rangka app manifest |
| [changeset.rk](./changeset) | `sent to /api/v1/changesets (not a file on disk)` | A proposed change to metadata: the only way Rangka AI (and the Console) changes a running installation |
| [config.rk](./config) | `<site>/config.rk` | Layer 4: defaults for an organisation (company) |
| [connector.rk](./connector) | `<connector>/connector.rk (rangka-connectors)` | Layer 3: an integration (rangka-connectors/&lt;name&gt;/connector.rk): how to authenticate, the events it triggers and the actions flows can call. |
| [dashboard.rk](./dashboard) | `apps/<app>/dashboards/<name>.rk` | A dashboard file: apps/&lt;app&gt;/dashboards/&lt;name&gt;.rk |
| [features.rk](./features) | `<site>/features.rk` | Layer 4: plans and the entitlements they grant (apps, capabilities, limits) |
| [flow.rk](./flow) | `apps/<app>/flows/<name>.rk` | Durable flow: steps run in order on an event or a schedule, through the queue. |
| [layout.rk](./layout) | `<site>/layouts/<name>.rk` | Layer 6: the app shell and pages built from UI blocks |
| [model.rk](./model) | `apps/<app>/models/<model>.rk` | Rangka model |
| [overlay.rk](./overlay) | `<site>/custom/<model>.rk` | Company layer (layer 4): fields, rules and view tweaks an organisation adds to an app model, in &lt;site&gt;/custom/&lt;model&gt;.rk |
| [role.rk](./role) | `apps/<app>/roles/<role>.rk` | A role an app defines (apps/&lt;app&gt;/roles/&lt;role&gt;.rk) |
| [system.rk](./system) | `<site>/system.rk` | Layer 2: which backend capabilities are on and which driver serves each |
| [template.rk](./template) | `templates/<name>/template.rk` | A starting point for a new installation: the apps to install, connectors, roles, fixtures and onboarding questions. |
| [theme.rk](./theme) | `<site>/themes/<name>.rk` | Layer 6: colours, type and density for an installation or one organisation |
