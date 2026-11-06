// Public relay for the Fourcubes legacy spool on agni (CT 130), reached over
// a Workers VPC service. It forwards only /health and the signed calls ORRN's
// SpoolClient makes (packages/server/src/lib/spool-client.ts), pinned by
// method. Settings, passwords, webhooks, AI, archives, the job list, printer
// edits, pages, login, setup and /print answer 404 here and never reach the
// plant. The spool still verifies every HMAC itself and limits machine
// tokens to the same routes (internal/api/middleware DefaultMachineRoutes).

const HOSTS = new Set(["fcprimary-spool.orrn.in", "fcprimary.spool.orrn.in"]);

const ID = "[1-9][0-9]{0,9}";

export const ROUTES = [
  ["GET", "/health"],
  ["GET", "/api/printers"],
  ["POST", "/api/printers"],
  ["GET", `/api/printers/${ID}/status`],
  ["POST", `/api/printers/${ID}/test`],
  ["GET", "/api/templates"],
  ["POST", "/api/templates"],
  ["PUT", `/api/templates/${ID}`],
  ["POST", "/api/jobs"],
  ["GET", `/api/jobs/${ID}`],
  ["POST", `/api/jobs/${ID}/cancel`],
  ["POST", `/api/jobs/${ID}/retry`],
  ["GET", "/scale/reading"],
  ["GET", "/filter/status"],
  ["POST", "/filter/blocking"],
  ["GET", "/filter/domains"],
  ["POST", "/filter/domains"],
  ["POST", "/filter/domains/remove"],
].map(([method, pattern]) => ({ method, re: new RegExp(`^${pattern}$`) }));

// The shape of an ORRN machine token, "Bearer v1:<base36 ms>:<hex HMAC>".
// The spool checks the HMAC. This only stops requests that carry anything
// else (an admin JWT, a cookie, nothing) before they cross the tunnel.
const MACHINE_AUTH = /^Bearer v1:[0-9a-z]{1,13}:[0-9a-f]{64}$/;

function allowed(method, pathname) {
  return ROUTES.some((r) => r.method === method && r.re.test(pathname));
}

export default {
  async fetch(request, env) {
    const url = new URL(request.url);
    if (!HOSTS.has(url.hostname)) {
      return new Response("Not found", { status: 404 });
    }
    // ORRN sends no query strings, so none are forwarded.
    if (url.search !== "" || !allowed(request.method, url.pathname)) {
      return new Response("Not found", { status: 404 });
    }
    if (url.pathname !== "/health" && !MACHINE_AUTH.test(request.headers.get("Authorization") ?? "")) {
      return new Response("Unauthorized", { status: 401 });
    }
    url.protocol = "http:";
    const forwarded = new Request(url, request);
    forwarded.headers.delete("Cookie");
    return env.FCPRIMARY_SPOOL.fetch(forwarded);
  },
};
