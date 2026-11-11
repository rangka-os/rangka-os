package engine

const memoHeader = "# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/model.json\n"

// memoFiles: a submittable memo with a unique key, a referrer linking it from a field and from a child row, and a
// model whose clock compute reads a link.
var memoFiles = map[string]string{
	"crm/models/memo_doc.rk": memoHeader + `name: memo_doc
label: {id: Memo}
is_submittable: true
naming: MD-{#####}
fields:
  - {name: memo_key, type: data, label: {id: Kunci memo}, unique: true}
`,
	"crm/models/memo_ref.rk": memoHeader + `name: memo_ref
label: {id: Rujukan memo}
naming: MR-{#####}
fields:
  - {name: doc, type: link, to: memo_doc, label: {id: Memo}}
  - {name: lines, type: table, of: memo_ref_line, label: {id: Baris}}
`,
	"crm/models/memo_ref_line.rk": memoHeader + `name: memo_ref_line
is_child: true
fields:
  - {name: doc, type: link, to: memo_doc, label: {id: Memo}}
`,
	"crm/models/memo_clock.rk": memoHeader + `name: memo_clock
label: {id: Memo jam}
naming: MC-{#####}
fields:
  - {name: customer, type: link, to: customer, label: {id: Pelanggan}}
  - {name: city_today, type: data, label: {id: Kota hari ini}, compute: "today() != '' ? customer.city : ''"}
`,
}
