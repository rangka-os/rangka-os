# Kursus & pelatihan (`industry: training`)

Non-formal education and training: language courses, bimbel (tutoring), music, coding, driving, cooking and beauty
classes, professional and certification training for companies, online cohorts. Revenue comes from program fees paid
in full, in instalments or monthly (SPP), and from corporate training contracts.

## How it usually runs

| Stage | What happens | Who | Documents the owner names |
| --- | --- | --- | --- |
| Enquiry | Parent, student or HR asks about programs, schedule, price | Admin, CS | Chat WA, brosur, Instagram |
| Trial or placement | Free trial class or placement test (common in language and music) | Pengajar | Hasil tes |
| Registration | Student picks a program and a batch (angkatan) or class slot | Admin | Formulir pendaftaran |
| Payment | Full, instalments, or monthly SPP; sometimes a registration fee and books | Admin, keuangan | Kuitansi, invoice, bukti transfer |
| Classes | Sessions on a schedule; attendance per session; makeup classes | Pengajar | Absen kelas, jurnal mengajar |
| Progress | Reports to parents or HR; certificate at the end | Pengajar, admin | Rapor, sertifikat |
| Collection | Chase late SPP or instalments | Admin | Pengingat WA |
| Reporting | Students per batch, seats left, unpaid fees, teacher hours | Owner | Rekap |

Teachers are often paid per session taught, which links attendance to payroll.

**Size bands:** `staff: to_5` or `to_20` (teachers are often part-time), `customers: to_200` or `to_1000` active
students, `scale: one_location` or `branches_2_5` (franchise-style bimbel). Corporate training firms: few customers,
large invoices with terms.

## Words owners use → Rangka

| Owner says | Rangka concept |
| --- | --- |
| program, kelas, paket kursus | `course_program` (price, sessions, billing plan, `vat_exempt`) |
| angkatan, batch, kelas reguler | `course_batch` (quota, schedule, instructor, online link) |
| pertemuan, sesi | `course_session` |
| siswa, peserta, murid | `student` |
| orang tua yang bayar, perusahaan yang bayar | `bill_to` on the enrollment (a `customer`) |
| daftar, registrasi | `enrollment` (takes a seat) |
| SPP, cicilan, bayar lunas | `billing_plan` monthly / installment / full; each amount is a `course_charge` → `sales_invoice` |
| absen kelas | `course_attendance` (screen `absen_kelas` for instructors) |
| keluar, berhenti | enrollment status Keluar (frees the seat, keeps the history) |
| pengajar, tutor, mentor | `user` with role `instructor` |

## Common pains and the question that uncovers them

| Pain (owner's words) | 0061 code | Question to ask |
| --- | --- | --- |
| "Lupa siapa yang belum bayar SPP bulan ini" | `sales_overdue` | "Tiap awal bulan, kamu tahu siapa yang belum bayar dari mana?" |
| "Bikin kuitansi dan tagihan satu-satu" | `sales_invoices` | "Tagihan dikirim ke siswa, orang tua, atau perusahaan?" |
| "Kelas kepenuhan atau kosong, tidak kelihatan" | none | "Satu kelas maksimal berapa orang, dan sisanya dicatat di mana?" |
| "Absen kelas di kertas, rekap honor pengajar manual" | `staff_pay` | "Honor pengajar dihitung per pertemuan atau gaji bulanan?" |
| "Calon siswa yang tanya tidak di-follow up" | `customer_followup` | "Yang tanya lewat WA tapi belum daftar, ada yang menghubungi lagi?" |
| "Data siswa ada di HP admin" | `customer_records` | "Data siswa sekarang disimpan di mana?" |

## What covers it

- **App:** `kelas_kursus` (modules `courses`, `invoicing`, `contacts`, `catalogue`, `users`): programs, batches with
  quota and schedule, students, enrollment, attendance per session, charges billed full, by instalment or per month.
  Demo site `demo-kursus` adds `faktur_piutang` and `pembukuan`.
- **Add-ons:** `whatsapp_reminder` (overdue invoices), `pipeline` (enquiries to follow up, `crm` app), `bank_statement`,
  `payments` with an online payment connector for SPP links.
- **Teacher pay:** `payroll` exists but requires `workforce`, `projects` and `invoicing` (0061 §1), built for placed
  workers. For per-session teacher honorariums, design a small custom piece reading `course_attendance` unless the
  owner already runs payroll for staff.
- **Usually custom:** trial class and placement test; makeup classes; progress reports and certificates; registration
  fee and book sales as separate lines; sibling or early-bird discounts beyond the enrollment `discount`; refund rules.
- **Integrations:** WhatsApp (connector `whatsapp_business`, or the Cloud-provided WhatsApp number connector listed in `rangka_catalogue`). No connector for LMS platforms,
  Zoom or Google Classroom: the batch stores a class link only.

## KPIs the owner watches

- Active students, new enrollments per month, seats left per batch.
- Unpaid charges and SPP collection rate per month.
- Dropout (enrollments set to Keluar) and re-enrollment into the next level.
- Revenue per program; teacher hours vs. honorarium.

## Money and compliance hotspots

- **Instalments must add up.** The enrollment locks price and plan once the first charge is submitted; one charge per
  period, never billed twice. A changed price goes through a new enrollment or a cancelled and amended charge.
- **Refunds have no document of their own** in `courses`. A refund is an outgoing payment (`payments`) recorded by the
  admin; the policy (how much, until which session) is a custom rule, and the owner approves each refund.
- **PPN on education services:** `course_program` has a `vat_exempt` flag. Whether a given program qualifies: verify
  with a tax consultant; never decide it for the owner.
- **Minors' data:** students may be children. Collect only what the owner needs (name, parent's phone); never ask for
  student data in chat.

## Red flags: not a fit yet

- Formal schools (SD, SMP, SMA, pesantren with curriculum reporting to the ministry, Dapodik): out of scope.
- Owners who mainly need an online learning platform (video lessons, quizzes, graded assignments): Rangka records
  students, schedules and money, not course content. Keep their LMS as `existing`.
- Hourly private tutoring booked ad hoc per teacher calendar: batches fit poorly; say that per-session booking needs a
  part designed for them.
- Large corporate training with per-participant certificates from an accreditation body: certificate issuing is
  custom and the accreditation link is `open`.
