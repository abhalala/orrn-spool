# Scale reader and ORRN machine auth

Status: built 2026-10-24 on branch `fp/scale-reader`. Not yet run against a real
indicator; the FourCubes indicator's make and frame format are still unknown.

## 1. What the scale reader does

The packing-bay scale's indicator sends its weight as ASCII text, several frames
a second, over RS-232 (or a USB-serial adapter). The spool reads that stream,
decides whether the weight is stable, and serves the result to ORRN so the pack
screen can fill the bundle weight instead of the packer typing it.

The reader is **receive only**. It never sends a byte to the indicator: no tare,
no zero, no print request. The serial port is wrapped so the code holding it has
no write method; a `tcp://` source half-closes its sending side on connect.

Code: `internal/scale` (parser, stability detector, service, simulator),
`internal/api/handlers/scale.go` (HTTP), `internal/heartbeat` (presence to ORRN).
Serial access uses `go.bug.st/serial` (the one new dependency; it brings
`github.com/creack/goselect` with it). The device-enumeration subpackage, which
needs cgo on macOS, is not imported.

## 2. Configuration

`config.yaml`, block `scale:`. Environment variables override the file.

| Key | Env | Default | Meaning |
|---|---|---|---|
| `scale.device` | `SPOOL_SCALE_DEVICE` | empty (no scale) | `/dev/ttyUSB0`, `COM3`, or `tcp://host:port` for a serial-to-Ethernet box |
| `scale.baud` | `SPOOL_SCALE_BAUD` | 9600 | baud rate |
| `scale.data_bits` | | 8 | |
| `scale.parity` | | none | `none`, `even`, `odd` |
| `scale.stop_bits` | | 1 | 1 or 2 |
| `scale.frame` | `SPOOL_SCALE_FRAME` | see below | regular expression for one line; needs a named group `kg` (decimal kilograms) or `grams` (integer grams) |
| `scale.simulate` | `SPOOL_SCALE_SIMULATE` | false | serve simulated frames, no device opened |
| `scale.simulate_speed` | | 1 | speed the simulator up (demos, tests) |

Command-line flag: `--scale-sim` is the same as `scale.simulate: true`.

Default frame pattern (`scale.DefaultFramePattern`):

```
^(?:(?P<status>ST|US)\s*,\s*)?(?:(?P<mode>GS|NT|GR|TR)\s*,\s*)?(?P<kg>[+-]?\s*\d+(?:\.\d+)?)\s*kg\s*$
```

It matches the common continuous-output frames `ST,GS,+  12.345kg`,
`US,NT,-   0.020kg` and `+0012.345kg`. Lines may end in CR, LF or CR LF; STX/ETX
bytes around the frame are ignored. Lines that do not match (overload `OL,...`,
banners, noise) are counted as `rejected` and skipped. Kilograms are converted to
grams exactly (no floating point); digits past the third decimal round half up.

The indicator's own `ST`/`US` flag is parsed but not trusted: stability is
decided by the rule below.

### Simulator

```
go run ./cmd/spool --scale-sim
# or: SPOOL_SCALE_SIMULATE=true go run ./cmd/spool
```

One cycle (about 13.7 s): bare platform near zero (0 g, sometimes -5 g), a
bundle of 25 to 120 kg lands with an overshoot and a damped swing, a 4 s hold
with ±4 g noise on a 5 g division, a nudge that creeps 45 g, a 3 s hold, then the
bundle is lifted. Frames are 10 a second in the default format, flagged `US`
while moving. The same seed replays the same stream (`scale.Simulator`).

## 3. Stability rule (ORRN spec, pack Q-PK-3)

Evaluated on every request, from the frames received so far:

1. No frames, or the newest frame is more than **3 s** old: `{"stable": false}`
   with no `grams` (reason `stale` or `no_frames`).
2. Walk back from the newest frame while all frames stay within **20 g** of each
   other (max - min <= 20 g). That run is the current steady stretch.
3. Stable when the run has at least **5 frames** and spans at least **1 s**
   (oldest to newest frame in the run). A move of more than 20 g starts a new run,
   so the 1 s hold restarts.
4. A steady **0 g (or negative)** is never a stable weight: `stable: false`,
   reason `zero`.

The weight reported is the newest frame, which is what the indicator displays.

## 4. HTTP endpoints

All behind the same auth as `/api/*` (spool JWT or ORRN machine token, section 5).
None of them touches the indicator.

`GET /scale/reading`, always 200, `Cache-Control: no-store`:

```json
{"grams": 52415, "stable": true,  "ageMs": 80, "frames": 12, "configured": true}
{"grams": 52360, "stable": false, "ageMs": 60, "frames": 3, "reason": "settling", "configured": true}
{"grams": 0,     "stable": false, "ageMs": 90, "frames": 30, "reason": "zero", "configured": true}
{"stable": false, "ageMs": 3400, "frames": 0, "reason": "stale", "configured": true}
{"stable": false, "frames": 0, "reason": "not_configured", "configured": false}
```

`simulated: true` is added when the simulator is on. `reason` values:
`not_configured`, `no_frames`, `stale`, `settling`, `zero`.

`GET /scale/status`: `{configured, simulated, device, connected, lastFrameAt,
frames, rejected, lastError}`.

`GET /scale/stream`: server-sent events, event `reading`, the same JSON every
250 ms. Used instead of a WebSocket so no second dependency is needed; a browser
`EventSource` or `fetch` stream reads it.

## 5. ORRN machine auth: decision

**Decision: the Go spool accepts ORRN's existing `Bearer v1:<ts>:<hmac>` token in
addition to its own JWT. ORRN's `SpoolClient` is unchanged.**

Why this has less surface than moving ORRN to the JWT:

- ORRN's `SpoolClient` already signs every call this way (printers, templates,
  jobs, health, and now scale). Ten-plus procedures depend on it; none change.
- The JWT is a browser session: password login, 24 h expiry, HttpOnly cookie.
  Using it from ORRN would mean storing each plant's spool admin password in
  ORRN, a login round trip from a stateless Worker, and refresh handling. The
  shared node secret already exists for this purpose (generated at enrollment,
  stored wrapped in `spool_deployment.shared_secret_wrapped`, written to the
  node as `ORRN_SPOOL_SHARED_SECRET` by the install script).
- The spool side is one small file (`internal/api/middleware/orrn_hmac.go`) and
  one branch in `RequireAuth`.

Token format (matches `signSpoolRequest` in ORRN
`packages/server/src/lib/spool-crypto.ts`):

```
Authorization: Bearer v1:<ts>:<sig>
ts       = Unix milliseconds, base 36
sig      = hex( HMAC-SHA256( secret, METHOD + "|" + path + "|" + ts + "|" + bodyHash ) )
path     = request path plus "?query" when there is one
bodyHash = hex( SHA-256(body) ), or "" when the request has no body
```

Spool-side checks: the secret is `orrn.shared_secret` / `ORRN_SPOOL_SHARED_SECRET`
(empty = machine auth off, JWT only); clock window **±5 min**; signature compared
in constant time; a token already accepted is refused until it leaves the window
(replay). Two ORRN requests signed in the same millisecond with the same method,
path and body would collide; ORRN polls far slower than that.

Shared test vectors, identical files in both repos:

- orrn-spool `internal/api/middleware/testdata/orrn_hmac_vectors.json`
- ORRN `packages/server/test/fixtures/spool-hmac-vectors.json`

They were generated by ORRN's signer. Go verifies every vector
(`TestORRNVectorsVerify`) and re-signs the valid ones to the identical token
(`TestORRNVectorsSign`); ORRN does the same in `spool-hmac-vectors.test.ts`. If
either side changes the format, both tests fail.

## 6. Heartbeat (node presence)

The spool did not post a heartbeat before. When `orrn.server_url`,
`orrn.instance_id` and `orrn.shared_secret` are set (install script env
`ORRN_SPOOL_SERVER_URL`, `ORRN_SPOOL_INSTANCE_ID`, `ORRN_SPOOL_SHARED_SECRET`),
it now posts every `orrn.heartbeat_interval` (default 60 s) to ORRN's existing
endpoint `POST /webhooks/spool/activate` (alias `/webhooks/edge/heartbeat`):

```json
{"instance_id": "…", "version": "…",
 "scale": {"configured": true, "simulated": false, "connected": true, "lastFrameAt": "2026-10-24T06:29:59.9Z"}}
```

signed with the same `Bearer v1:` token (path `/webhooks/spool/activate`) and
`X-Spool-Subdomain`. ORRN stores the scale presence only when the signature
verifies, so the Printing screen can show "scale connected, last frame 2 s ago"
honestly. `version` comes from `-ldflags "-X main.Version=…"` (default `dev`).

## 7. Owner-only bring-up (real indicator)

Not possible from code alone:

1. Find the FourCubes packing-bay indicator: make, model, and its output mode
   (continuous vs on-demand). On-demand indicators need a print key or a command;
   this reader will not send commands, so the indicator must be set to
   continuous output in its own menu.
2. Note the port settings from the indicator menu (baud, data bits, parity, stop
   bits) and the cable (DB9 RS-232 to a USB-serial adapter on the spool PC).
3. Capture 30 s of raw output (`cat /dev/ttyUSB0` / PuTTY log) with the platform
   empty, a bundle placed, and the bundle lifted. If the lines do not look like
   `ST,GS,+  12.345kg`, write a `scale.frame` regex with a `kg` or `grams` group
   and check it against the capture (`internal/scale/parser_test.go` shows how).
4. Set `scale.device` (and the rest), restart the spool, open `/scale/status`
   (frames rising, rejected flat) and `/scale/reading` (stable within ~1 s of a
   bundle settling, `zero` when empty).
5. Confirm the indicator's division (5 g, 10 g, 20 g …). With a 20 g division
   the 20 g band allows only one step of flicker; if the reading never settles,
   that is an owner decision on the spec rule, not a code tweak.
