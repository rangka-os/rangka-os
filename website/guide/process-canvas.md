# Process canvas

The process canvas draws one business process, for example "from lead to paid", as stages from left to right. Open a
stage to see its triggers, conditions, rules, approvals, actions and lifecycle steps in place. The canvas is built from
your `.rk` files; it never stores business logic of its own. The full rules are in
[contract 0014](https://github.com/rangka-os/rangka-os/blob/main/docs/contracts/0014-process-canvas.md); this page is the short version.

## Name the stages

An app with a `menu:` already gets a canvas: one stage per model, in menu order, marked "Disimpulkan" (inferred).
To name the stages yourself, add a `process` file to a module, an app or the site:

```text
crm/
  processes/
    lead_to_cash.rk
```

```yaml
# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/process.json
name: lead_to_cash           # same as the file name
label: {id: Dari lead sampai lunas, en: Lead to cash}
stages:
  - {name: lead_in, label: {id: Lead masuk}, model: lead}
  - {name: deal, label: {id: Peluang}, model: deal}
  - {name: invoice, label: {id: Faktur}, model: sales_invoice}
  - {name: paid, label: {id: Lunas}, model: sales_invoice, when: "status == 'paid'", models: [payment]}
```

A process changes no behaviour. `when` narrows a stage to documents in one state, so two stages can share a model.
A site file with the same name replaces the one in the unit. Reference: [process.rk](/reference/schema/process).

## Where you see it

- **Console**: `#/processes` lists the processes; `#/processes/<name>` opens the canvas. Change a text value of a node
  in the side panel and choose "Usulkan perubahan": the edit becomes a change set, reviewed and applied like any other.
- **Web app**: `#/proses/<name>`, read only, shown in the sidebar as "Proses bisnis" when the kernel has processes.
- **API**: `GET /api/v1/processes` and `GET /api/v1/processes/{name}`; see the [REST API](/reference/rest-api).

A person only sees the stages whose model they may read. Only the owner saves the layout (which stages are open).
