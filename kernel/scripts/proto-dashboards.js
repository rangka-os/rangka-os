// Prints the prototype engine dashboards for the seed data at a fixed date (see proto-expected.sh).
const fs = require('fs');
const RealDate = Date;
const FIXED = new RealDate('2026-10-30T10:00:00').getTime();
global.Date = class extends RealDate { constructor(...a) { if (a.length === 0) super(FIXED); else super(...a); } static now() { return FIXED; } };
global.window = global;
eval(fs.readFileSync(process.argv[2], 'utf8'));
eval(fs.readFileSync(process.argv[3], 'utf8'));
const E = window.RangkaEngine;
const out = {};
for (const dn of ['crm_overview', 'sales_overview']) {
  const d = E.dashboard(dn);
  out[dn] = d.widgets.map(w => Object.assign({ name: w.name }, E.computeWidget(w, d.default_period || 30)));
}
out.invoices = E.store.sales_invoice.map(r => ({ name: r.name, subtotal: r.subtotal, ppn: r.ppn, grand_total: r.grand_total, status: E.status('sales_invoice', r) }));
out.deals = E.store.deal.map(r => ({ name: r.name, probability: r.probability, weighted_value: r.weighted_value }));
console.log(JSON.stringify(out, null, 1));
