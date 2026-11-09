# Contracts

Changes that cross repositories (schema, validator, kernel contracts, connector format) start as a numbered contract
in `docs/contracts/` ([how contracts change](https://github.com/rangka-os/rangka-os/blob/main/docs/contracts/0000-how-contracts-change.md)).
"Proposed" contracts may already be built in part; each file says what shipped.

| No. | Contract | Status |
| --- | --- | --- |
| [0000](https://github.com/rangka-os/rangka-os/blob/main/docs/contracts/0000-how-contracts-change.md) | How contracts change | proposed |
| [0001](https://github.com/rangka-os/rangka-os/blob/main/docs/contracts/0001-modules-and-apps.md) | Modules and apps | accepted |
| [0002](https://github.com/rangka-os/rangka-os/blob/main/docs/contracts/0002-connector-runtime-and-ports.md) | Connector runtime and ports | proposed |
| [0003](https://github.com/rangka-os/rangka-os/blob/main/docs/contracts/0003-edge-and-devices.md) | Edge agent and devices | proposed |
| [0004](https://github.com/rangka-os/rangka-os/blob/main/docs/contracts/0004-app-packages-and-upgrades.md) | App packages and upgrades | accepted in principle |
| [0005](https://github.com/rangka-os/rangka-os/blob/main/docs/contracts/0005-pilot-readiness.md) | Pilot readiness | accepted |
| [0006](https://github.com/rangka-os/rangka-os/blob/main/docs/contracts/0006-edge-sync.md) | Edge sync (`edge-sync/v1`) | proposed |
| [0007](https://github.com/rangka-os/rangka-os/blob/main/docs/contracts/0007-document-approvals.md) | Document approvals | proposed |
| [0008](https://github.com/rangka-os/rangka-os/blob/main/docs/contracts/0008-print-formats.md) | Print formats | proposed |
| [0009](https://github.com/rangka-os/rangka-os/blob/main/docs/contracts/0009-number-schemes.md) | Number schemes | proposed |
| [0010](https://github.com/rangka-os/rangka-os/blob/main/docs/contracts/0010-multi-org-and-row-scope.md) | Multi-org and row scope | proposed |
| [0011](https://github.com/rangka-os/rangka-os/blob/main/docs/contracts/0011-attachments.md) | Attachments | proposed |
| [0012](https://github.com/rangka-os/rangka-os/blob/main/docs/contracts/0012-imports.md) | Imports | proposed |
| [0013](https://github.com/rangka-os/rangka-os/blob/main/docs/contracts/0013-money-and-currency.md) | Money and currency | proposed |
| [0014](https://github.com/rangka-os/rangka-os/blob/main/docs/contracts/0014-process-canvas.md) | Process canvas | proposed |
| [0015](https://github.com/rangka-os/rangka-os/blob/main/docs/contracts/0015-ubah-mode-and-live-preview.md) | Ubah mode and live preview | proposed |
| [0016](https://github.com/rangka-os/rangka-os/blob/main/docs/contracts/0016-site-company-module.md) | Site company module | proposed |
| [0017](https://github.com/rangka-os/rangka-os/blob/main/docs/contracts/0017-connector-secrets.md) | Connector secrets | proposed |
| [0018](https://github.com/rangka-os/rangka-os/blob/main/docs/contracts/0018-list-aggregates.md) | List aggregates | accepted |
| [0019](https://github.com/rangka-os/rangka-os/blob/main/docs/contracts/0019-frappe-shell-default.md) | Default shell: side-only | accepted |
| [0020](https://github.com/rangka-os/rangka-os/blob/main/docs/contracts/0020-ai-assistant-relay.md) | AI assistant relay | proposed |
| [0021](https://github.com/rangka-os/rangka-os/blob/main/docs/contracts/0021-declarative-stock-moves.md) | Declarative stock moves | implemented |
| [0022](https://github.com/rangka-os/rangka-os/blob/main/docs/contracts/0022-unit-conversion-and-returned-qty.md) | Unit conversion and returned quantities | implemented |
| [0024](https://github.com/rangka-os/rangka-os/blob/main/docs/contracts/0024-list-and-dashboard-export.md) | List and dashboard export | proposed |
| [0025](https://github.com/rangka-os/rangka-os/blob/main/docs/contracts/0025-flow-read-locks.md) | Check-then-write in flows | accepted |
| [0026](https://github.com/rangka-os/rangka-os/blob/main/docs/contracts/0026-append-only-ledgers.md) | Append-only ledgers | accepted |
| [0027](https://github.com/rangka-os/rangka-os/blob/main/docs/contracts/0027-field-visibility-and-dashboard-roles.md) | Field visibility (hidden_for) and dashboard roles | accepted |
| [0028](https://github.com/rangka-os/rangka-os/blob/main/docs/contracts/0028-plan-limits.md) | Plan limits | accepted |
| [0029](https://github.com/rangka-os/rangka-os/blob/main/docs/contracts/0029-xml-file-formats.md) | XML file formats | proposed |
| [0030](https://github.com/rangka-os/rangka-os/blob/main/docs/contracts/0030-work-screens.md) | Work screens (staff tools declared in `.rk`) | accepted |
| [0031](https://github.com/rangka-os/rangka-os/blob/main/docs/contracts/0031-recurring-documents.md) | Recurring documents: month arithmetic and for_each | accepted |
| [0032](https://github.com/rangka-os/rangka-os/blob/main/docs/contracts/0032-free-module-composition.md) | Free module composition | proposed |
| [0033](https://github.com/rangka-os/rangka-os/blob/main/docs/contracts/0033-ai-drafted-company-pieces-and-connectors.md) | AI-drafted company pieces and connectors | proposed |
| [0034](https://github.com/rangka-os/rangka-os/blob/main/docs/contracts/0034-counter-compute-and-summary.md) | Counter: dry compute, summary and availability | proposed |
| [0035](https://github.com/rangka-os/rangka-os/blob/main/docs/contracts/0035-raka-skills.md) | Rangka skills: one Agent Skills library for Raka and any other harness | accepted |
| [0036](https://github.com/rangka-os/rangka-os/blob/main/docs/contracts/0036-catalogue-in-the-platform.md) | Catalogue in the platform: modules and connectors | proposed |
| [0037](https://github.com/rangka-os/rangka-os/blob/main/docs/contracts/0037-nested-navigation.md) | Nested navigation: sub-levels in menus and nav.side | accepted and implemented |
| [0038](https://github.com/rangka-os/rangka-os/blob/main/docs/contracts/0038-responsive-layout.md) | Responsive behaviour in layout.rk | accepted and implemented |
| [0039](https://github.com/rangka-os/rangka-os/blob/main/docs/contracts/0039-counter-stock-per-outlet.md) | Counter: stock per outlet on the catalogue | proposed |

The base contract (v0) follows.

<!--@include: ../../docs/contracts.md{2,}-->
