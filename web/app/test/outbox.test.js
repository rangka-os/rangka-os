const test = require("node:test");
const assert = require("node:assert");
const fs = require("fs");
const path = require("path");
const vm = require("vm");

function load() {
  const w = {};
  vm.runInNewContext(fs.readFileSync(path.join(__dirname, "..", "outbox.js"), "utf8"), { window: w, Promise, setTimeout }, { filename: "outbox.js" });
  return w.RangkaApp; // no indexedDB in the vm: the outbox keeps jobs in memory
}
const plain = (x) => JSON.parse(JSON.stringify(x));
const err = (status, code, message) => Object.assign(new Error(message || code), { status, code });

test("newKey: a v4 UUID shape, different each time", () => {
  const A = load();
  const a = A.newKey(), b = A.newKey();
  assert.match(a, /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/);
  assert.notStrictEqual(a, b);
});

test("retryable: no connection, 5xx and idempotency_in_progress only", () => {
  const A = load();
  assert.ok(A.outboxRetryable(err(0, "network")));
  assert.ok(A.outboxRetryable(err(503, "x")));
  assert.ok(A.outboxRetryable(err(409, "idempotency_in_progress")));
  assert.ok(!A.outboxRetryable(err(409, "idempotency_conflict")));
  assert.ok(!A.outboxRetryable(err(422, "validation")));
  assert.ok(!A.outboxRetryable(err(403, "permission_denied")));
  assert.ok(!A.outboxRetryable(null));
});

test("nextState: done, queued again with tries, or failed with the server's words", () => {
  const A = load();
  const j = { id: "j1", state: "queued", tries: 1 };
  assert.deepStrictEqual(plain(A.outboxNext(j, null)), { id: "j1", state: "done", tries: 1, error: null });
  assert.deepStrictEqual(plain(A.outboxNext(j, err(0, "network"))), { id: "j1", state: "queued", tries: 2, error: "Belum ada koneksi" });
  assert.deepStrictEqual(plain(A.outboxNext(j, err(422, "validation", "Jam wajib diisi"))), { id: "j1", state: "failed", tries: 2, error: "Jam wajib diisi" });
});

test("scope: org and email, so another user on the device never sends the job", () => {
  const A = load();
  assert.strictEqual(A.outboxScope({ org: "kopi", email: "sari@x" }), "kopi|sari@x");
  assert.strictEqual(A.outboxScope(null), "|");
});

// a fake API: create and submit record their keys; fail(n) makes the next n calls fail with err
function fakeApi() {
  const calls = [], fails = [];
  return {
    calls, fails,
    create(model, body, o) { calls.push(["create", model, o.key, o.occurredAt || null]); const f = fails.shift(); return f ? Promise.reject(f) : Promise.resolve({ name: "DRAFT-1" }); },
    submit(model, name, o) { calls.push(["submit", name, o.key, o.occurredAt || null]); const f = fails.shift(); return f ? Promise.reject(f) : Promise.resolve({ record: { name: "JAM-1" }, approval: null }); },
  };
}

test("send: a job creates then submits with its own keys; a retryable failure on a queue screen is stored", async () => {
  const A = load();
  const api = fakeApi();
  const job = A.outbox.newJob({ scope: "o|u", screen: "isi", model: "jam", body: { hours: 8 }, submit: true });
  const res = await A.outbox.send(api, job, true);
  assert.strictEqual(res.record.name, "JAM-1");
  assert.deepStrictEqual(api.calls.map((c) => c[0]), ["create", "submit"]);
  assert.strictEqual(api.calls[0][2], job.keys.create);
  assert.strictEqual(api.calls[1][2], job.keys.submit);
  assert.strictEqual(api.calls[0][3], null, "a write sent at once carries no _occurred_at");
  // offline: create fails without a connection, the job waits in the outbox
  const api2 = fakeApi();
  api2.fails.push(err(0, "network"));
  const job2 = A.outbox.newJob({ scope: "o|u", screen: "isi", model: "jam", body: { hours: 7 }, submit: true });
  const q = await A.outbox.send(api2, job2, true);
  assert.ok(q.queued);
  assert.strictEqual((await A.outbox.list("o|u")).length, 1);
  // a screen without offline: queue gets the error instead
  const api3 = fakeApi();
  api3.fails.push(err(0, "network"));
  await assert.rejects(A.outbox.send(api3, A.outbox.newJob({ scope: "o|u", model: "jam", body: {} }), false), (e) => e.status === 0);
  assert.strictEqual((await A.outbox.list("o|u")).length, 1);
});

test("flush: sends queued jobs of the scope in order with the same keys and _occurred_at; refused ones become failed", async () => {
  const A = load();
  const off = fakeApi();
  off.fails.push(err(0, "network"), err(0, "network"), err(0, "network"));
  const j1 = A.outbox.newJob({ scope: "o|u", model: "jam", body: { hours: 1 }, submit: true });
  const j2 = A.outbox.newJob({ scope: "o|u", model: "jam", body: { hours: 2 } });
  const other = A.outbox.newJob({ scope: "o|someone", model: "jam", body: { hours: 3 } });
  j2.at = "2026-11-08T01:00:01.000Z"; j1.at = "2026-11-08T01:00:00.000Z"; other.at = "2026-11-08T00:59:00.000Z";
  await A.outbox.send(off, j1, true); await A.outbox.send(off, j2, true); await A.outbox.send(off, other, true);
  const api = fakeApi();
  api.fails.push(undefined, undefined, err(422, "validation", "Jam wajib diisi"));
  const sent = await A.outbox.flush(api, "o|u");
  assert.strictEqual(sent, 1);
  assert.deepStrictEqual(api.calls.map((c) => [c[0], c[2]]), [["create", j1.keys.create], ["submit", j1.keys.submit], ["create", j2.keys.create]]);
  assert.strictEqual(api.calls[0][3], j1.at, "a queued write says when it was pressed");
  const left = plain(await A.outbox.list());
  assert.deepStrictEqual(left.map((j) => [j.scope, j.state]), [["o|someone", "queued"], ["o|u", "failed"]], "another user's job is untouched");
  assert.strictEqual(left[1].error, "Jam wajib diisi");
  await A.outbox.dismiss(left[1].id);
  assert.strictEqual((await A.outbox.list("o|u")).length, 0);
});

test("flush: a job that created its record but lost the submit only submits on the next try", async () => {
  const A = load();
  const api = fakeApi();
  api.fails.push(undefined, err(503, "busy")); // create ok, submit 503
  const job = A.outbox.newJob({ scope: "o|u", model: "jam", body: {}, submit: true });
  await A.outbox.send(api, job, true);
  const api2 = fakeApi();
  await A.outbox.flush(api2, "o|u");
  assert.deepStrictEqual(api2.calls.map((c) => c[0]), ["submit"]);
  assert.strictEqual(api2.calls[0][1], "DRAFT-1");
});

test("cache: the last good value with its time", async () => {
  const A = load();
  assert.strictEqual(await A.outbox.cache.get("screen:x"), null);
  await A.outbox.cache.put("screen:x", { a: 1 });
  const c = await A.outbox.cache.get("screen:x");
  assert.deepStrictEqual(plain(c.value), { a: 1 });
  assert.ok(c.at);
});
