// Run with: node --test cloudflare/
import { test } from "node:test";
import assert from "node:assert/strict";
import worker from "./index.js";

const TOKEN = `Bearer v1:${Date.now().toString(36)}:${"a1".repeat(32)}`;
const ORIGIN = "https://fcprimary-spool.orrn.in";

function fakeSpool() {
  const seen = [];
  return {
    seen,
    env: {
      FCPRIMARY_SPOOL: {
        async fetch(req) {
          seen.push({ url: req.url, method: req.method, headers: req.headers, body: req.body ? await req.text() : null });
          return new Response("forwarded", { status: 200 });
        },
      },
    },
  };
}

async function send(method, path, { auth = TOKEN, cookie, body, host = ORIGIN } = {}) {
  const spool = fakeSpool();
  const headers = new Headers();
  if (auth) headers.set("Authorization", auth);
  if (cookie) headers.set("Cookie", cookie);
  const res = await worker.fetch(new Request(host + path, { method, headers, body }), spool.env);
  return { status: res.status, seen: spool.seen };
}

// Every request ORRN's SpoolClient makes, including the plant filter calls.
const CLOUD_CALLS = [
  ["GET", "/health"],
  ["GET", "/api/printers"],
  ["POST", "/api/printers"],
  ["GET", "/api/printers/1/status"],
  ["POST", "/api/printers/12/test"],
  ["GET", "/api/templates"],
  ["POST", "/api/templates"],
  ["PUT", "/api/templates/2"],
  ["POST", "/api/jobs"],
  ["GET", "/api/jobs/345"],
  ["POST", "/api/jobs/345/cancel"],
  ["POST", "/api/jobs/345/retry"],
  ["GET", "/scale/reading"],
  ["GET", "/filter/status"],
  ["POST", "/filter/blocking"],
  ["GET", "/filter/domains"],
  ["POST", "/filter/domains"],
  ["POST", "/filter/domains/remove"],
];

test("forwards every ORRN machine call over plain HTTP to the VPC service", async () => {
  for (const [method, path] of CLOUD_CALLS) {
    const body = method === "GET" ? undefined : `{"k":"${path}"}`;
    const { status, seen } = await send(method, path, { body });
    assert.equal(status, 200, `${method} ${path}`);
    assert.equal(seen.length, 1, `${method} ${path} forwarded once`);
    assert.equal(seen[0].url, `http://fcprimary-spool.orrn.in${path}`);
    assert.equal(seen[0].method, method);
    assert.equal(seen[0].headers.get("Authorization"), TOKEN);
    if (body) assert.equal(seen[0].body, body);
  }
});

test("health needs no token", async () => {
  const { status, seen } = await send("GET", "/health", { auth: null });
  assert.equal(status, 200);
  assert.equal(seen.length, 1);
});

test("admin, LAN-reaching and page routes never cross the tunnel", async () => {
  const blocked = [
    ["GET", "/api/settings"],
    ["PUT", "/api/settings/password"],
    ["PUT", "/api/settings/archive"],
    ["GET", "/api/webhooks"],
    ["POST", "/api/webhooks"],
    ["POST", "/api/webhooks/1/test"],
    ["GET", "/api/ai/config"],
    ["POST", "/api/ai/api-key"],
    ["DELETE", "/api/ai/api-key"],
    ["GET", "/api/archives"],
    ["POST", "/api/archives/run"],
    ["GET", "/api/jobs"],
    ["GET", "/api/jobs/queue"],
    ["GET", "/api/jobs/stats"],
    ["DELETE", "/api/jobs/1"],
    ["POST", "/api/jobs/1/reprint"],
    ["GET", "/api/queue"],
    ["GET", "/api/stats"],
    ["GET", "/api/printers/1"],
    ["PUT", "/api/printers/1"],
    ["DELETE", "/api/printers/1"],
    ["POST", "/api/printers/1/pause"],
    ["GET", "/api/templates/1"],
    ["DELETE", "/api/templates/1"],
    ["POST", "/api/templates/1/print"],
    ["GET", "/print/bundle/U1"],
    ["POST", "/print/bundle/U1"],
    ["GET", "/login"],
    ["POST", "/login"],
    ["POST", "/api/auth/setup"],
    ["POST", "/api/auth/login"],
    ["GET", "/"],
    ["GET", "/settings"],
    ["GET", "/scale/stream"],
    ["GET", "/scale/status"],
    ["POST", "/filter/disable"],
    ["POST", "/filter/rules"],
    ["GET", "/filter"],
    ["OPTIONS", "/api/jobs"],
    ["HEAD", "/health"],
    ["GET", "/api/jobs/abc"],
    ["GET", "/api/jobs/0"],
    ["GET", "/api/jobs/1%2F..%2F..%2Fsettings"],
    ["GET", "/api/jobs/1/"],
    ["GET", "//api/jobs/1"],
  ];
  for (const [method, path] of blocked) {
    const { status, seen } = await send(method, path);
    assert.equal(status, 404, `${method} ${path}`);
    assert.equal(seen.length, 0, `${method} ${path} must not be forwarded`);
  }
});

test("query strings are refused, so sort_by never reaches the spool", async () => {
  for (const path of ["/api/jobs?sort_by=id", "/api/jobs/1?x=1", "/health?x", "/api/printers?"]) {
    const { status, seen } = await send("GET", path);
    // "/api/printers?" parses to an empty search and is the plain list call.
    if (path === "/api/printers?") {
      assert.equal(status, 200);
      continue;
    }
    assert.equal(status, 404, path);
    assert.equal(seen.length, 0, path);
  }
});

test("dot segments are normalised before matching", async () => {
  const { status, seen } = await send("PUT", "/api/templates/1/../../settings/password");
  assert.equal(status, 404);
  assert.equal(seen.length, 0);
});

test("anything but an ORRN machine token is refused at the edge", async () => {
  const notMachine = [
    null,
    "Bearer eyJhbGciOiJIUzI1NiJ9.eyJhdXRoZW50aWNhdGVkIjp0cnVlfQ.c2ln",
    "Basic YWRtaW46cGFzcw==",
    "Bearer v1:abc:nothex",
    `Bearer v1::${"a1".repeat(32)}`,
    `${TOKEN} extra`,
  ];
  for (const auth of notMachine) {
    const { status, seen } = await send("POST", "/api/jobs", { auth, body: "{}" });
    assert.equal(status, 401, String(auth));
    assert.equal(seen.length, 0);
  }
  const { status, seen } = await send("GET", "/api/printers", { auth: null, cookie: "spool_auth=stolen" });
  assert.equal(status, 401);
  assert.equal(seen.length, 0);
});

test("cookies are stripped from forwarded machine calls", async () => {
  const { status, seen } = await send("GET", "/api/printers", { cookie: "spool_auth=stolen" });
  assert.equal(status, 200);
  assert.equal(seen[0].headers.get("Cookie"), null);
});

test("other hostnames are refused", async () => {
  const { status, seen } = await send("GET", "/health", { host: "https://evil.orrn.in" });
  assert.equal(status, 404);
  assert.equal(seen.length, 0);
});
