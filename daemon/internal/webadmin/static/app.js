"use strict";
// 웹 관리 화면. 서버 값은 모두 textContent 로만 넣는다(HTML 로 해석하지 않는다).
const $ = (id) => document.getElementById(id);
const H = { "X-Opereta-Admin": "1", "Content-Type": "application/json" };

async function api(method, path, body) {
  const res = await fetch(path, { method, headers: H, body: body === undefined ? undefined : JSON.stringify(body), credentials: "same-origin" });
  let data = null;
  try { data = await res.json(); } catch (e) { data = null; }
  if (res.status === 401 && path.startsWith("/v1/")) { showLogin(); throw new Error("login required"); }
  if (!res.ok) {
    const msg = data && data.error ? data.error.message : "HTTP " + res.status;
    throw new Error(msg);
  }
  return data;
}

function el(tag, props, ...kids) {
  const n = document.createElement(tag);
  for (const [k, v] of Object.entries(props || {})) {
    if (k === "class") n.className = v; else if (k.startsWith("on")) n.addEventListener(k.slice(2), v); else n.setAttribute(k, v);
  }
  for (const k of kids) n.append(k instanceof Node ? k : document.createTextNode(String(k)));
  return n;
}
function toast(msg) { const t = $("toast"); t.textContent = msg; t.className = "show"; setTimeout(() => (t.className = ""), 3500); }
function fail(e) { toast(e.message || String(e)); }
function mib(n) { return n >= 1024 ? (n / 1024).toFixed(1) + " GiB" : n + " MiB"; }
function fill(id, ...kids) { const n = $(id); n.replaceChildren(...kids); }
function kvTable(rows) {
  const t = el("table");
  for (const [k, v] of rows) t.append(el("tr", {}, el("th", {}, k), el("td", {}, v)));
  return t;
}

function showLogin() { $("login").hidden = false; $("app").hidden = true; $("logout").hidden = true; }
function showApp() { $("login").hidden = true; $("app").hidden = false; $("logout").hidden = false; refresh(); }

$("login-form").addEventListener("submit", async (ev) => {
  ev.preventDefault();
  $("login-error").textContent = "";
  try {
    await api("POST", "/auth/login", { password: $("password").value });
    $("password").value = "";
    showApp();
  } catch (e) { $("login-error").textContent = e.message; }
});
$("logout").addEventListener("click", async () => { await api("POST", "/auth/logout", {}).catch(() => {}); showLogin(); });

async function refresh() {
  await Promise.all([loadServer(), loadConnection(), loadKeys(), loadResources(), loadServices()].map((p) => p.catch(fail)));
}

async function loadServer() {
  const [st, models, cfg, stats] = await Promise.all([api("GET", "/v1/status"), api("GET", "/v1/models"), api("GET", "/v1/config"), api("GET", "/v1/server/stats")]);
  const s = st.server || {};
  const ids = (models.models || []).filter((m) => !m.incomplete).map((m) => m.id);
  const sel = el("select", {}, ...ids.map((id) => el("option", { value: id }, id)));
  if (s.model_id && ids.includes(s.model_id)) sel.value = s.model_id; else if (models.default) sel.value = models.default;
  const running = ["starting", "running", "running_cpu_fallback"].includes(s.state);
  const rows = [["State", s.state + (s.model_id ? " · " + s.model_id : "")]];
  if (stats.running && stats.slots && stats.slots.available) rows.push(["Requests", stats.slots.busy + " of " + stats.slots.total + " slots busy"]);
  if (stats.metrics && stats.metrics.available) rows.push(["Speed", stats.metrics.generated_tokens_per_second.toFixed(1) + " tokens/s generated"]);
  if (s.last_error) rows.push(["Last error", s.last_error.code + ": " + s.last_error.message]);
  const lan = el("input", { type: "checkbox" }); lan.checked = !!cfg.lan_exposed;
  lan.addEventListener("change", () => api("PUT", "/v1/config", { lan_exposed: lan.checked }).then(() => toast("Saved. Restart the server to apply.")).catch((e) => { lan.checked = !lan.checked; fail(e); }));
  fill("server", kvTable(rows),
    el("div", { class: "row" }, sel,
      el("button", { class: "primary", onclick: () => api("POST", "/v1/server/start", { model: sel.value, wait: false }).then(() => { toast("Starting"); setTimeout(refresh, 1500); }).catch(fail) }, "Start"),
      el("button", { class: "danger", onclick: () => api("POST", "/v1/server/stop", {}).then(() => { toast("Stopped"); refresh(); }).catch(fail) }, "Stop")),
    el("label", {}, lan, " Allow other computers to connect"),
    running ? el("p", { class: "muted" }, "The server is running.") : el("p", { class: "muted" }, ids.length ? "The server is stopped." : "No model is installed. Download one with the opereta command or the Control Center."));
}

async function loadConnection() {
  const c = await api("GET", "/v1/connection");
  const rows = [["This computer", c.local_url]];
  for (const a of c.addresses || []) rows.push(["Other computers", a.url]);
  if (c.mdns_url) rows.push(["By name", c.mdns_url]);
  rows.push(["API keys", c.key_count + (c.key_required ? " (required)" : " (not required)")]);
  const kids = [kvTable(rows)];
  for (const w of c.warnings || []) kids.push(el("p", { class: "error" }, w));
  if (c.examples && c.examples.curl) kids.push(el("pre", {}, c.examples.curl));
  fill("connection", ...kids);
}

async function loadKeys() {
  const [list, gw] = await Promise.all([api("GET", "/v1/apikeys"), api("GET", "/v1/gateway").catch(() => null)]);
  const usage = {};
  if (gw && gw.stats) for (const k of gw.stats.keys || []) usage[k.id] = k;
  const t = el("table", {}, el("tr", {}, el("th", {}, "Name"), el("th", {}, "Key"), el("th", {}, "Requests"), el("th", {})));
  for (const k of list.keys || []) {
    const u = usage[k.id] || {};
    t.append(el("tr", {}, el("td", {}, k.label), el("td", {}, k.prefix + "…"), el("td", {}, String(u.requests || 0)),
      el("td", {}, el("button", { class: "danger", onclick: () => confirm("Delete this key?") && api("DELETE", "/v1/apikeys/" + encodeURIComponent(k.id)).then(loadKeys).catch(fail) }, "Delete"))));
  }
  const name = el("input", { placeholder: "Name, such as Orca", maxlength: "64" });
  const shown = el("div");
  const add = el("button", { class: "primary", onclick: () => api("POST", "/v1/apikeys", { label: name.value || "web" }).then((r) => {
    shown.replaceChildren(el("p", {}, "Copy this key now. It is shown only once:"), el("pre", {}, r.key));
    name.value = ""; loadKeys();
  }).catch(fail) }, "Create key");
  fill("keys", t, el("div", { class: "row" }, name, add), shown);
}

async function loadResources() {
  const r = await api("GET", "/v1/resources");
  const s = r.system;
  const rows = [["CPU", s.cpu.used_percent.toFixed(1) + "% of " + s.cpu.cores + " cores"], ["Memory", mib(s.memory.used_mib) + " of " + mib(s.memory.total_mib)]];
  for (const d of s.disks || []) rows.push(["Disk " + d.mount, mib(d.used_mib) + " of " + mib(d.total_mib) + " (" + d.used_percent + "%)"]);
  for (const g of r.gpus || []) rows.push(["GPU " + g.index, g.name + " · " + mib(g.memory_used_mib) + " / " + mib(g.memory_total_mib) + " · " + g.temperature_c + " °C"]);
  for (const t of s.temperatures || []) rows.push(["Temp " + t.name, t.celsius.toFixed(1) + " °C"]);
  fill("resources", kvTable(rows));
}

async function loadServices() {
  const r = await api("GET", "/v1/services");
  const t = el("table", {}, el("tr", {}, el("th", {}, "Service"), el("th", {}, "State"), el("th", {})));
  for (const s of r.services || []) {
    if (!s.installed) continue;
    const act = el("td", {});
    for (const a of ["start", "stop", "restart"]) {
      if ((s.actions || []).includes(a)) act.append(el("button", { onclick: () => api("POST", "/v1/services/" + encodeURIComponent(s.unit) + "/" + a).then(() => { toast(a + " done"); loadServices(); }).catch(fail) }, a), " ");
    }
    t.append(el("tr", {}, el("td", {}, s.title), el("td", {}, s.active + "/" + s.sub), act));
  }
  fill("services", t);
}

api("GET", "/auth/state").then((s) => (s.authenticated ? showApp() : showLogin())).catch(showLogin);
setInterval(() => { if (!$("app").hidden && document.visibilityState === "visible") loadServer().catch(() => {}); }, 5000);
