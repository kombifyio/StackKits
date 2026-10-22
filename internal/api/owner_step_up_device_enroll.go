package api

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"html/template"
	"net/http"
	"net/url"
	"time"

	"github.com/kombifyio/stackkits/internal/identity"
	"github.com/kombifyio/stackkits/internal/localowner"
)

// The device-enroll mode of the Owner step-up page is the State Console
// enrollment ceremony. The State Console only links to
// ownerStepUpDeviceEnrollPath; the Owner authenticates with a passkey at the
// local PocketID through the existing confidential step-up client, and this
// page alone receives the ID token, issues the single-use pairing code and
// shows it as text and QR code. Neither the token nor the code passes through
// the MCP or agent transcript.
//
// Opening the page performs no identity change: it uses only the already
// prepared step-up client (prepared by an API-key caller, as for action
// approvals). Starting a flow needs a same-origin POST bound to this
// browser's cookie; the code is issued only to the browser that completed the
// passkey login, and only after identity.IssueHomePairingWithOwnerStepUp has
// verified the ID token against the signed Home trust.
const (
	ownerStepUpDeviceEnrollPath  = ownerStepUpPath + "/device-enroll"
	ownerStepUpDeviceEnrollTTL   = 5 * time.Minute
	ownerStepUpDeviceEnrollFlows = 16
	ownerStepUpDeviceEnrollCSRF  = "device-enroll"
)

// prepareDeviceEnroll is the API-key route that provisions the step-up client
// (idempotently, as the action-approval create route does) and returns the
// page the State Console links to.
func (h *ownerStepUpHandler) prepareDeviceEnroll(w http.ResponseWriter, r *http.Request) {
	stepUpHeaders(w)
	service, err := localowner.NewService(h.root)
	if err != nil {
		stepUpFailure(w)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if _, err = service.PrepareStepUp(ctx, h.origin); err != nil {
		stepUpFailure(w)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"deviceEnrollmentUrl": h.origin + ownerStepUpDeviceEnrollPath})
}

var ownerStepUpDeviceEnrollReview = template.Must(template.New("device-enroll-review").Parse(`<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Enroll a household device</title><h1>Enroll a household device</h1><p>Continue to your local PocketID and authenticate with your Owner passkey. This page then shows a single-use pairing code and QR code for five minutes.</p><p>Scan or type the code on the device while it is connected to your home network. The device creates its own key; the code is spent on first use.</p><form method="post"><input type="hidden" name="csrf" value="{{.CSRF}}"><button type="submit">Authenticate with PocketID</button></form></html>`))

func (h *ownerStepUpHandler) reviewDeviceEnroll(w http.ResponseWriter, r *http.Request) {
	stepUpHeaders(w)
	issuer, ok := h.preparedStepUpIssuer(r.Context())
	if !ok || !stepUpFormPolicy(w, issuer) {
		stepUpFailure(w)
		return
	}
	browser, err := stepUpRandom()
	if err != nil {
		stepUpFailure(w)
		return
	}
	w.Header().Set("Referrer-Policy", "same-origin")
	http.SetCookie(w, &http.Cookie{Name: "__Host-stackkit-step-up", Value: browser, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, Expires: time.Now().Add(ownerStepUpDeviceEnrollTTL)})
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = ownerStepUpDeviceEnrollReview.Execute(w, struct{ CSRF string }{stepUpCSRF(browser, ownerStepUpDeviceEnrollCSRF)})
}

func (h *ownerStepUpHandler) startDeviceEnroll(w http.ResponseWriter, r *http.Request) {
	stepUpHeaders(w)
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	cookie, err := r.Cookie("__Host-stackkit-step-up")
	if r.Header.Get("Origin") != h.origin || (r.Header.Get("Sec-Fetch-Site") != "" && r.Header.Get("Sec-Fetch-Site") != "same-origin") || r.ParseForm() != nil ||
		err != nil || len(cookie.Value) != 43 || r.PostForm.Get("csrf") != stepUpCSRF(cookie.Value, ownerStepUpDeviceEnrollCSRF) {
		stepUpFailure(w)
		return
	}
	issuer, ok := h.preparedStepUpIssuer(r.Context())
	if !ok || !stepUpFormPolicy(w, issuer) {
		stepUpFailure(w)
		return
	}
	state, err := stepUpRandom()
	if err != nil {
		stepUpFailure(w)
		return
	}
	verifier, err := stepUpRandom()
	if err != nil {
		stepUpFailure(w)
		return
	}
	nonce, err := stepUpRandom()
	if err != nil {
		stepUpFailure(w)
		return
	}
	now := time.Now()
	flow := ownerStepUpFlow{issuer: issuer, verifier: verifier, started: true, browserHash: sha256.Sum256([]byte(cookie.Value)), deviceEnroll: true, nonce: nonce}
	flow.binding.IssuedAt, flow.binding.ExpiresAt = now, now.Add(ownerStepUpDeviceEnrollTTL)
	h.mu.Lock()
	devices := 0
	for id, existing := range h.flows {
		if !now.Before(existing.binding.ExpiresAt) {
			delete(h.flows, id)
		} else if existing.deviceEnroll {
			devices++
		}
	}
	if len(h.flows) >= 128 || devices >= ownerStepUpDeviceEnrollFlows {
		h.mu.Unlock()
		stepUpFailure(w)
		return
	}
	h.flows[state] = flow
	h.mu.Unlock()
	challenge := sha256.Sum256([]byte(verifier))
	query := url.Values{"response_type": {"code"}, "client_id": {localowner.StepUpClientID}, "redirect_uri": {h.origin + localowner.StepUpCallbackPath}, "scope": {"openid"}, "state": {state}, "nonce": {nonce}, "prompt": {"login"}, "code_challenge_method": {"S256"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(challenge[:])}}
	http.Redirect(w, r, issuer+"/authorize?"+query.Encode(), http.StatusSeeOther)
}

// preparedStepUpIssuer reads the prepared step-up trust without creating or
// changing identity resources.
func (h *ownerStepUpHandler) preparedStepUpIssuer(ctx context.Context) (string, bool) {
	service, err := localowner.NewService(h.root)
	if err != nil {
		return "", false
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	trust, err := service.ReadStepUpTrust(ctx, h.origin)
	return trust.Issuer, err == nil && trust.Issuer != ""
}

func (h *ownerStepUpHandler) finishDeviceEnroll(w http.ResponseWriter, ctx context.Context, service *localowner.Service, code string, flow ownerStepUpFlow) {
	idToken, err := service.ExchangeStepUpIDToken(ctx, h.origin, code, flow.verifier)
	if err != nil {
		stepUpFailure(w)
		return
	}
	// The bootstrap is resolved before the code exists, so a node without a
	// LAN address or step-ca root never spends an Owner step-up on a code.
	bootstrap, err := identity.LoadHomeEnrollmentBootstrap(h.root)
	if err != nil {
		http.Error(w, "Device enrollment needs the Home LAN address and step-ca root; apply the Basement core first.", http.StatusServiceUnavailable)
		return
	}
	pairing, err := identity.IssueHomePairingWithOwnerStepUp(h.root, idToken, flow.nonce, time.Now())
	if err != nil {
		stepUpFailure(w)
		return
	}
	scriptNonce, err := stepUpRandom()
	if err != nil {
		stepUpFailure(w)
		return
	}
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'nonce-"+scriptNonce+"'; style-src 'nonce-"+scriptNonce+"'; form-action 'none'; frame-ancestors 'none'; base-uri 'none'")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = ownerStepUpDeviceEnrollCode.Execute(w, struct {
		Code, Payload, Nonce, URL, ServerName, Fingerprint string
		ExpiresAt                                          time.Time
	}{pairing.Code, bootstrap.PairingURI(pairing), scriptNonce, bootstrap.EnrollmentURL, bootstrap.TLSServerName, bootstrap.CARootSHA256, pairing.ExpiresAt})
}

// ownerStepUpDeviceEnrollCode draws the QR code with inline JavaScript (byte
// mode, error correction L, versions 1-15) so no QR library or network request
// is involved.
var ownerStepUpDeviceEnrollCode = template.Must(template.New("device-enroll-code").Parse(`<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Device pairing code</title>
<style nonce="{{.Nonce}}">body{font-family:system-ui,sans-serif;max-width:40rem;margin:2rem auto;padding:0 1rem}#qr{width:18rem;height:18rem;background:#fff}code{word-break:break-all}.code{font-size:1.5rem;letter-spacing:.1em}</style>
<h1>Pairing code</h1><p class="code"><code>{{.Code}}</code></p><svg id="qr" role="img" aria-label="Pairing QR code" data-payload="{{.Payload}}"></svg>
<p>Valid once, until {{.ExpiresAt.Format "15:04:05 MST"}}. Close this page after the device is enrolled; never share the code.</p>
<p>The device submits its own certificate request to <code>{{.URL}}</code> from the home network. It trusts that address only with a TLS certificate for <code>{{.ServerName}}</code> issued by the node's certificate authority with root fingerprint (SHA-256) <code>{{.Fingerprint}}</code>.</p>
<p>Command-line fallback: <code>stackkit device enroll --code CODE --csr device.csr</code></p>
<script nonce="{{.Nonce}}">` + ownerStepUpQRScript + `</script></html>`))

const ownerStepUpQRScript = `(function () {
  var svg = document.getElementById("qr");
  var result = qrMatrix(svg.getAttribute("data-payload"));
  var size = result.modules.length, path = "";
  for (var y = 0; y < size; y++) for (var x = 0; x < size; x++) if (result.modules[y][x]) path += "M" + (x + 4) + " " + (y + 4) + "h1v1h-1z";
  svg.setAttribute("viewBox", "0 0 " + (size + 8) + " " + (size + 8));
  var p = document.createElementNS("http://www.w3.org/2000/svg", "path");
  p.setAttribute("d", path);
  p.setAttribute("fill", "#000");
  svg.appendChild(p);
})();
function qrMatrix(text) {
  var data = Array.from(new TextEncoder().encode(text));
  var EC = [null, [7, [[1, 19]]], [10, [[1, 34]]], [15, [[1, 55]]], [20, [[1, 80]]], [26, [[1, 108]]], [18, [[2, 68]]], [20, [[2, 78]]], [24, [[2, 97]]], [30, [[2, 116]]], [18, [[2, 68], [2, 69]]], [20, [[4, 81]]], [24, [[2, 92], [2, 93]]], [26, [[4, 107]]], [30, [[3, 115], [1, 116]]], [22, [[5, 87], [1, 88]]]];
  var ALIGN = [null, [], [6, 18], [6, 22], [6, 26], [6, 30], [6, 34], [6, 22, 38], [6, 24, 42], [6, 26, 46], [6, 28, 50], [6, 30, 54], [6, 32, 58], [6, 34, 62], [6, 26, 46, 66], [6, 26, 48, 70]];
  var version = 0, capacity = 0;
  for (var v = 1; v <= 15; v++) {
    capacity = EC[v][1].reduce(function (sum, g) { return sum + g[0] * g[1]; }, 0);
    if (4 + (v < 10 ? 8 : 16) + 8 * data.length <= capacity * 8) { version = v; break; }
  }
  if (!version) throw new Error("payload too long");
  var bits = [];
  function put(value, length) { for (var i = length - 1; i >= 0; i--) bits.push((value >>> i) & 1); }
  put(4, 4); put(data.length, version < 10 ? 8 : 16);
  data.forEach(function (b) { put(b, 8); });
  put(0, Math.min(4, capacity * 8 - bits.length));
  put(0, (8 - bits.length % 8) % 8);
  var codewords = [];
  for (var i = 0; i < bits.length; i += 8) { var b = 0; for (var j = 0; j < 8; j++) b = (b << 1) | bits[i + j]; codewords.push(b); }
  for (var pad = 0xEC; codewords.length < capacity; pad ^= 0xEC ^ 0x11) codewords.push(pad);
  function mul(x, y) { var z = 0; for (var i = 7; i >= 0; i--) { z = (z << 1) ^ ((z >>> 7) * 0x11D); z ^= ((y >>> i) & 1) * x; } return z; }
  var degree = EC[version][0], divisor = [];
  for (var i = 0; i < degree - 1; i++) divisor.push(0);
  divisor.push(1);
  for (var i = 0, root = 1; i < degree; i++, root = mul(root, 2)) {
    for (var j = 0; j < degree; j++) { divisor[j] = mul(divisor[j], root); if (j + 1 < degree) divisor[j] ^= divisor[j + 1]; }
  }
  var blocks = [], offset = 0;
  EC[version][1].forEach(function (group) {
    for (var n = 0; n < group[0]; n++) {
      var block = codewords.slice(offset, offset + group[1]); offset += group[1];
      var ecc = divisor.map(function () { return 0; });
      block.forEach(function (b) { var factor = b ^ ecc.shift(); ecc.push(0); divisor.forEach(function (coef, k) { ecc[k] ^= mul(coef, factor); }); });
      blocks.push([block, ecc]);
    }
  });
  var stream = [];
  for (var i = 0; i < 128; i++) blocks.forEach(function (b) { if (i < b[0].length) stream.push(b[0][i]); });
  for (var i = 0; i < degree; i++) blocks.forEach(function (b) { stream.push(b[1][i]); });
  var size = version * 4 + 17, modules = [], fixed = [];
  for (var y = 0; y < size; y++) { modules.push(new Array(size).fill(false)); fixed.push(new Array(size).fill(false)); }
  function set(x, y, dark) { modules[y][x] = dark; fixed[y][x] = true; }
  for (var i = 0; i < size; i++) { set(6, i, i % 2 === 0); set(i, 6, i % 2 === 0); }
  [[3, 3], [size - 4, 3], [3, size - 4]].forEach(function (c) {
    for (var dy = -4; dy <= 4; dy++) for (var dx = -4; dx <= 4; dx++) {
      var d = Math.max(Math.abs(dx), Math.abs(dy)), x = c[0] + dx, y = c[1] + dy;
      if (x >= 0 && x < size && y >= 0 && y < size) set(x, y, d !== 2 && d !== 4);
    }
  });
  var align = ALIGN[version], last = align.length - 1;
  align.forEach(function (ay, i) {
    align.forEach(function (ax, j) {
      if ((i === 0 && j === 0) || (i === 0 && j === last) || (i === last && j === 0)) return;
      for (var dy = -2; dy <= 2; dy++) for (var dx = -2; dx <= 2; dx++) set(ax + dx, ay + dy, Math.max(Math.abs(dx), Math.abs(dy)) !== 1);
    });
  });
  function drawFormat(mask) {
    var value = (1 << 3) | mask, rem = value;
    for (var i = 0; i < 10; i++) rem = (rem << 1) ^ ((rem >>> 9) * 0x537);
    var f = ((value << 10) | rem) ^ 0x5412;
    function bit(i) { return ((f >>> i) & 1) === 1; }
    for (var i = 0; i <= 5; i++) set(8, i, bit(i));
    set(8, 7, bit(6)); set(8, 8, bit(7)); set(7, 8, bit(8));
    for (var i = 9; i < 15; i++) set(14 - i, 8, bit(i));
    for (var i = 0; i < 8; i++) set(size - 1 - i, 8, bit(i));
    for (var i = 8; i < 15; i++) set(8, size - 15 + i, bit(i));
    set(8, size - 8, true);
  }
  drawFormat(0);
  if (version >= 7) {
    var rem = version;
    for (var i = 0; i < 12; i++) rem = (rem << 1) ^ ((rem >>> 11) * 0x1F25);
    var vbits = (version << 12) | rem;
    for (var i = 0; i < 18; i++) { var dark = ((vbits >>> i) & 1) === 1, a = size - 11 + i % 3, b = Math.floor(i / 3); set(a, b, dark); set(b, a, dark); }
  }
  var index = 0;
  for (var right = size - 1; right >= 1; right -= 2) {
    if (right === 6) right = 5;
    for (var vert = 0; vert < size; vert++) for (var j = 0; j < 2; j++) {
      var x = right - j, y = ((right + 1) & 2) === 0 ? size - 1 - vert : vert;
      if (!fixed[y][x] && index < stream.length * 8) { modules[y][x] = ((stream[index >>> 3] >>> (7 - (index & 7))) & 1) === 1; index++; }
    }
  }
  var MASKS = [
    function (x, y) { return (x + y) % 2 === 0; }, function (x, y) { return y % 2 === 0; },
    function (x) { return x % 3 === 0; }, function (x, y) { return (x + y) % 3 === 0; },
    function (x, y) { return (Math.floor(x / 3) + Math.floor(y / 2)) % 2 === 0; }, function (x, y) { return x * y % 2 + x * y % 3 === 0; },
    function (x, y) { return (x * y % 2 + x * y % 3) % 2 === 0; }, function (x, y) { return ((x + y) % 2 + x * y % 3) % 2 === 0; }];
  function applyMask(m) { for (var y = 0; y < size; y++) for (var x = 0; x < size; x++) if (!fixed[y][x] && MASKS[m](x, y)) modules[y][x] = !modules[y][x]; }
  function penalty() {
    var score = 0, dark = 0;
    for (var pass = 0; pass < 2; pass++) for (var a = 0; a < size; a++) {
      var line = [];
      for (var b = 0; b < size; b++) line.push(pass ? modules[b][a] : modules[a][b]);
      for (var b = 0, run = 1; b < size; b++) {
        if (b + 1 < size && line[b + 1] === line[b]) { run++; continue; }
        if (run >= 5) score += run - 2;
        run = 1;
      }
      var s = line.map(function (d) { return d ? "1" : "0"; }).join("");
      for (var at = s.indexOf("1011101"); at !== -1; at = s.indexOf("1011101", at + 1)) {
        if (at >= 4 && s.slice(at - 4, at) === "0000" || s.slice(at + 7, at + 11) === "0000") score += 40;
      }
    }
    for (var y = 0; y < size; y++) for (var x = 0; x < size; x++) {
      if (modules[y][x]) dark++;
      if (x + 1 < size && y + 1 < size && modules[y][x] === modules[y][x + 1] && modules[y][x] === modules[y + 1][x] && modules[y][x] === modules[y + 1][x + 1]) score += 3;
    }
    return score + Math.floor(Math.abs(dark * 20 - size * size * 10) / (size * size)) * 10;
  }
  var best = 0, bestScore = Infinity;
  for (var m = 0; m < 8; m++) { applyMask(m); drawFormat(m); var p = penalty(); if (p < bestScore) { best = m; bestScore = p; } applyMask(m); }
  applyMask(best); drawFormat(best);
  return { version: version, mask: best, modules: modules };
}`

func ownerStepUpOriginOrEmpty(value string) string {
	if value == "" {
		return ""
	}
	origin, err := localowner.NormalizeStepUpOrigin(value)
	if err != nil {
		return ""
	}
	return origin
}
