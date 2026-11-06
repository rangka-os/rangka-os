# Rules, actions and hooks

## Rules

```yaml
rules:
  - name: lost_needs_reason
    on: save                 # save | submit
    check: stage != 'lost' || lost_reason != ''
    message: {id: Tulis alasan kalah supaya tim bisa belajar}
  - name: open_needs_close_date
    on: save
    level: warn              # error (default) blocks; warn asks to confirm
    check: stage == 'won' || stage == 'lost' || expected_close != ''
    message: {id: Peluang terbuka sebaiknya punya perkiraan closing}
```

Errors return 422 `{errors:[{field, rule, code, message}]}`; warnings come back as `_warnings` on save, and on submit
as 422 with `needs_force: true` until the client resends with `{"force": true}`.

## Actions

Buttons on a record that run a named hook:

```yaml
actions:
  - name: convert
    label: {id: Jadikan pelanggan + peluang}
    hook: crm.convert_lead
    if: converted_deal == ''
    queue: {retries: 3, backoff: 30s}   # optional: run in the background, returns 202
```

## Hooks and automations

Hooks are named Go functions in the kernel (`crm.convert_lead`, `crm.mark_won`, `stock.post_moves`,
`ledger.post_receivable`, `payments.allocate`, …). `on_submit:` and `on_cancel:` attach them to the document lifecycle, and `automations:` run
them when a condition becomes true on save — the CRM turns a won deal into a draft invoice this way:

```yaml
automations:
  - name: won_to_invoice
    when: stage changes to 'won'
    if: app_installed('invoicing')
    do: crm.mark_won
```

A module declares the hooks it uses in `module.rk` (`hooks: [crm.convert_lead, crm.mark_won]`); the validator refuses
unknown ones. A model can also ask for a person's approval before submit, cancel or an action: see
[Approvals](./documents#approvals).
