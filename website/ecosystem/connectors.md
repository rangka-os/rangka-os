# Connectors

Plug-and-play integrations with outside services, each declared in one `connector.rk`: authentication, triggers
(webhooks, polling), actions, field mapping, retries and idempotency. Deterministic — no LLM in the data path.
Repository: [rangka-os/rangka-connectors](https://github.com/rangka-os/rangka-connectors).
Reference: [connector.rk](/reference/schema/connector).

| Connector | Triggers | Actions | Status |
| --- | --- | --- | --- |
| WhatsApp Business (Meta Cloud API) | `message_received` (signature checked) | `send_template` | Spec; runtime not built |

Planned: QRIS (Xendit, Midtrans), bank statements, e-Faktur / Coretax, Tokopedia, Shopee, TikTok Shop, GoFood /
GrabFood, Accurate / Jurnal import.

Apps use connectors from flows (`on: whatsapp_business.message_received`, `call:` steps). Secrets come from `${ENV}`
values in the site, never from the connector file.
