# Worked examples

Three made-up businesses, one per common request. Each starts with the reuse check, because in all three the
catalogue already holds most of the data. Every file below passes `rangka validate` (schema 0.5) on a site that runs
`sewa_alat`, `kelas_kursus` and `jasa_proyek` from rangka-apps.

The validator does not check that a model you link to or overlay belongs to an *installed* module: confirm it in
`rangka_meta` first, or the change set will point at a table the site does not have.

## 1. Rental with a deposit (site like `demo-sewa`)

> "Aku sewain alat pesta. Tiap sewa ada deposit yang balik kalau barang aman. Kadang penyewa ninggalin kartu identitas
> atau barang sebagai jaminan."

**Reuse check.** `rental` is installed (app `sewa_alat`):

- `rental_item` holds `deposit_per_unit`; `rental_booking.deposit` copies it at booking time (a snapshot with
  `default:`), so changing the item's deposit never rewrites old bookings.
- `rental_return` keeps the late fee and damage from the deposit and computes `deposit_refund`. The deposit returned
  is its own document, not an edit of the booking.

So the deposit needs **no new model and no new field**. Say so: "Deposit sudah tercatat di Pesanan sewa dan
pengembaliannya di Alat kembali, termasuk potongan denda."

**What is missing:** the kind of collateral (*jaminan*). It is a value of the booking, so an overlay field. Store the
kind, not the identity number: the process does not need the number (UU PDP).

`custom/rental_booking.rk`:

```yaml
# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/overlay.json
model: rental_booking
fields:
  - name: x_jaminan
    type: select
    label: {id: Jaminan, en: Collateral}
    default: "'none'"
    options:
      - {value: none, label: {id: Tanpa jaminan}}
      - {value: identitas, label: {id: Kartu identitas}}
      - {value: barang, label: {id: Barang lain}}
  - name: x_jaminan_keterangan
    type: data
    label: {id: Keterangan jaminan}
    depends_on: x_jaminan == 'barang'
rules:
  - name: x_jaminan_barang_ada_keterangan
    on: submit
    check: x_jaminan != 'barang' || x_jaminan_keterangan != ''
    message: {id: Tulis barang apa yang dijadikan jaminan}
views:
  form:
    add:
      - {field: x_jaminan, after: deposit_method}
      - {field: x_jaminan_keterangan}
```

Whether the collateral went back is an event at return time. The booking is submitted by then and cannot change, so
the check goes on `rental_return`, which reads the booking's value through the link:

`custom/rental_return.rk`:

```yaml
# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/overlay.json
model: rental_return
fields:
  - name: x_jaminan
    type: data
    label: {id: Jaminan yang dititip}
    read_only: true
    compute: booking.x_jaminan
  - name: x_jaminan_dikembalikan
    type: check
    label: {id: Jaminan sudah dikembalikan}
    default: 'false'
rules:
  - name: x_jaminan_kembali
    on: submit
    level: warn
    check: x_jaminan == 'none' || x_jaminan_dikembalikan
    message: {id: Jaminan penyewa belum dicentang kembali}
views:
  form:
    add:
      - {field: x_jaminan, after: refund_method}
      - {field: x_jaminan_dikembalikan}
```

- `after:` must name a field already in the app model's form; a second added field without `after` goes to the end
  (`unknown_field` otherwise).
- The rule is `level: warn`: the return can still be submitted, the person confirms.
- Risk: medium (new fields on submittable models, no money). If the owner wanted a collateral *value* in rupiah, that
  `currency` field would make it high.

**If `rental` were only `available`:** compose `sewa_alat` (`rangka-compose`), never a company `x_sewa` model.
**If it were `not_in_plan`:** tell the owner; do not rebuild it.

## 2. Course with exam scores (site like `demo-kursus`)

> "Tiap akhir angkatan ada ujian. Aku mau catat nilai tiap peserta dan berapa yang lulus."

**Reuse check.** `courses` is installed (app `kelas_kursus`): `student`, `course_batch`, `enrollment` (student in a
batch) and `course_session` with its `attendance` child table already exist. Attendance is covered; exam scores are
not.

**Design.** An exam is a document (it has a date, a number, and scores that are announced and must not change
quietly), and the scores are its lines: one aggregate, a header plus a child table. Each line links to `enrollment`
(not a typed name, not a new student list) and shows the student's name through the link.

`models/x_ujian.rk`:

```yaml
# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/model.json
name: x_ujian
label: {id: Ujian, en: Exam}
icon: file
is_submittable: true
naming: UJN-{YYYY}-{####}
title_field: batch
permissions:
  - {role: course_admin, read: true, create: true, write: true, submit: true, cancel: true, amend: true, export: true, print: true}
  - {role: instructor, read: true, create: true, write: true}
  - {role: finance_manager, read: true}
fields:
  - {name: batch, type: link, to: course_batch, label: {id: Angkatan}, required: true, in_list: true}
  - {name: exam_date, type: date, label: {id: Tanggal ujian}, required: true, default: today(), in_list: true}
  - {name: topic, type: data, label: {id: Materi}}
  - {name: pass_score, type: float, label: {id: Nilai lulus}, required: true, default: '70'}
  - {name: scores, type: table, of: x_ujian_nilai, label: {id: Nilai peserta}, min_rows: 1}
  - {name: passed_count, type: int, label: {id: Lulus}, compute: "scores.map(s, s.score >= pass_score ? 1 : 0).sum()", in_list: true}
rules:
  - name: scores_in_range
    on: save
    check: "scores.map(s, s.score < 0 || s.score > 100 ? 1 : 0).sum() == 0"
    message: {id: Nilai harus 0 sampai 100}
  - name: scores_of_batch
    on: save
    check: "scores.map(s, s.batch == batch ? 0 : 1).sum() == 0"
    message: {id: Ada peserta yang bukan dari angkatan ini}
views:
  list:
    columns: [name, batch, exam_date, passed_count, status]
    filters: [batch]
    sort: -exam_date
```

`models/x_ujian_nilai.rk`:

```yaml
# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/model.json
name: x_ujian_nilai
is_child: true
fields:
  - {name: enrollment, type: link, to: enrollment, label: {id: Pendaftaran}, required: true}
  - {name: batch, type: link, to: course_batch, label: {id: Angkatan}, compute: enrollment.batch}
  - {name: student_name, type: data, label: {id: Nama peserta}, compute: enrollment.student.student_name}
  - {name: score, type: float, label: {id: Nilai}, required: true}
  - {name: note, type: data, label: {id: Catatan}}
```

- `passed_count` is computed, never typed. Rules over lines sum a `map`, the same pattern `course_session` uses.
- `is_submittable: true`: a correction after the scores are announced is cancel + amend, and the history stays.
  The kernel rates a new submittable company model **high**: "Perubahan ini perlu disetujui pemilik."
- `permissions` are set at creation, copied from `enrollment`'s roles (`course_admin`, `instructor`,
  `finance_manager`); later changes to them are a person's job.
- The company fields have plain names (`score`, not `x_score`): they become real columns of the company table.

## 3. Outsourcing placements and SLA deductions (site like `demo-manpower`)

> "Aku outsourcing satpam ke beberapa klien. Tiap penempatan ada kontrak PKWT yang ada tanggal habisnya. Klien bisa
> potong tagihan kalau pos kosong."

**Reuse check.** `workforce.worker` is the people placed at clients (one list; never an `x_satpam` model).
`projects.project` is the client contract and `projects.assignment` is the placement of a worker on a project with
`start_date`, `end_date` and rates. Invoices are `invoicing.sales_invoice`.

**Contract facts** are values of the placement: an overlay on `assignment`. The end date already exists, so the
"ends soon" flag is computed from it, not stored by hand.

`custom/assignment.rk`:

```yaml
# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/overlay.json
model: assignment
fields:
  - name: x_contract_no
    type: data
    label: {id: Nomor kontrak, en: Contract number}
  - name: x_contract_type
    type: select
    label: {id: Jenis kontrak, en: Contract type}
    default: "'pkwt'"
    options:
      - {value: pkwt, label: {id: PKWT (waktu tertentu)}}
      - {value: pkwtt, label: {id: PKWTT (tetap)}}
  - name: x_contract_file
    type: attach
    label: {id: Berkas kontrak}
  - name: x_ends_soon
    type: check
    label: {id: Habis dalam 30 hari}
    compute: "x_contract_type == 'pkwt' && end_date != '' && days_between(today(), end_date) <= 30"
rules:
  - name: x_contract_no_for_pkwt
    on: save
    check: x_contract_type != 'pkwt' || x_contract_no != ''
    message: {id: Isi nomor kontrak PKWT}
views:
  form:
    add:
      - {field: x_contract_no, after: end_date}
      - {field: x_contract_type}
      - {field: x_contract_file}
  list:
    add:
      - {field: x_ends_soon}
```

- `x_contract_no` is an overlay field, so `unique` would do nothing: if numbers must never repeat, say so and keep it
  a rule the person checks, or design a company model where the number is a real column.
- `x_ends_soon` reads `today()`, so it is recomputed whenever the record is read and stays current without a save.

**SLA deductions** are not covered by any module. Each deduction is an event with a month, a reason and an amount
against a client contract: a company document. The month is a `date` (first day), not a model per month.

`models/x_potongan_sla.rk`:

```yaml
# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/model.json
name: x_potongan_sla
label: {id: Potongan SLA, en: SLA deduction}
icon: file
is_submittable: true
naming: SLA-{YYYY}-{####}
title_field: project
permissions:
  - {role: project_admin, read: true, create: true, write: true, export: true, print: true}
  - {role: finance_manager, read: true, write: true, submit: true, cancel: true, amend: true, export: true, print: true}
fields:
  - {name: project, type: link, to: project, label: {id: Kontrak klien}, required: true, in_list: true}
  - {name: customer, type: link, to: customer, label: {id: Klien}, read_only: true, compute: project.customer, in_list: true}
  - {name: period, type: date, label: {id: Bulan}, help: {id: Tanggal 1 bulan yang dipotong}, required: true, in_list: true}
  - name: reason
    type: select
    label: {id: Alasan}
    required: true
    options:
      - {value: absent_post, label: {id: Pos kosong}}
      - {value: late_relief, label: {id: Pergantian shift telat}}
      - {value: incident, label: {id: Kejadian di lokasi}}
  - {name: amount, type: currency, label: {id: Jumlah potongan}, required: true, in_list: true}
  - {name: invoice, type: link, to: sales_invoice, label: {id: Faktur yang dipotong}}
  - {name: notes, type: text, label: {id: Catatan}}
rules:
  - name: period_first_day
    on: save
    check: period.endsWith('-01')
    message: {id: Bulan diisi tanggal 1 bulan itu}
  - name: amount_positive
    on: save
    check: amount > 0
    message: {id: Jumlah potongan harus lebih dari 0}
views:
  list:
    columns: [name, project, customer, period, amount, status]
    filters: [project, reason]
    sort: -period
```

- `amount` is `currency`, so the change set is **high** (owner only), and so is the submittable model.
- The deduction records the agreement; it does not change the invoice. Reducing what the client owes stays in
  `invoicing` (a person edits the draft invoice or issues the correction). A company flow that would write the
  invoice is also high and needs the owner; never let an LLM compute the amount.
- `customer` is computed from `project` for lists and filters, so nobody types the client twice.

## What these examples avoid

| Tempting design | Why not |
| --- | --- |
| `x_deposit` model with `status: dikembalikan` edited later | The return is its own document; editing a money record hides history |
| `x_peserta` list for the exam | `student` + `enrollment` already exist |
| Columns `nilai_1` … `nilai_30` on the exam | Child table |
| `x_potongan_okt`, `x_potongan_nov` | One model with a `period` date |
| `jaminan_ktp_no: data` | Personal data the process does not need |
