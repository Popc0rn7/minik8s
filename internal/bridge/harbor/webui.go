package harbor

import (
	"net/http"
	"time"

	"minik8s/internal/node"
	"minik8s/internal/pod"
	"minik8s/internal/service"
)

type webUISnapshot struct {
	Kind       string             `json:"kind"`
	APIVersion string             `json:"apiVersion"`
	Generated  time.Time          `json:"generatedAt"`
	Nodes      []node.Node        `json:"nodes"`
	Pods       []*pod.Pod         `json:"pods"`
	Services   []*service.Service `json:"services"`
}

func (s *Server) handleWebUI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeStatus(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write([]byte(webUIHTML))
}

func (s *Server) handleWebUISnapshot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeStatus(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}
	if _, err := s.refreshNodeLiveness(r.Context()); err != nil {
		writeStatus(w, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}
	if err := s.syncServices(r.Context()); err != nil {
		writeStatus(w, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}
	nodes, err := s.nodes.List()
	if err != nil {
		writeStatus(w, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}
	pods, err := s.pods.List("", nil)
	if err != nil {
		writeStatus(w, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}
	services, err := s.services.List("", nil)
	if err != nil {
		writeStatus(w, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}
	sortNodes(nodes)
	sortPods(pods)
	sortServices(services)
	writeJSON(w, http.StatusOK, webUISnapshot{
		Kind:       "WebUISnapshot",
		APIVersion: "v1",
		Generated:  time.Now().UTC(),
		Nodes:      nodes,
		Pods:       pods,
		Services:   services,
	})
}

const webUIHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Minik8s Harbor</title>
<style>
:root {
  color-scheme: light;
  --ink: #18212f;
  --muted: #637084;
  --line: #d8e0e8;
  --water: #dff5f8;
  --harbor: #f8fafc;
  --ready: #16845b;
  --pending: #b87b00;
  --failed: #b42318;
  --unknown: #64748b;
  --service: #285f9f;
}
* { box-sizing: border-box; }
body {
  margin: 0;
  background: linear-gradient(180deg, #eef8fb 0%, #f8fafc 54%, #fff 100%);
  color: var(--ink);
  font-family: Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
}
button { font: inherit; }
.shell {
  min-height: 100vh;
  display: grid;
  grid-template-rows: auto 1fr;
}
.topbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 18px 24px;
  border-bottom: 1px solid var(--line);
  background: rgba(255,255,255,.78);
  backdrop-filter: blur(12px);
  position: sticky;
  top: 0;
  z-index: 4;
}
.brand { display: flex; align-items: center; gap: 12px; min-width: 220px; }
.mark {
  width: 38px;
  height: 38px;
  border-radius: 8px;
  background: linear-gradient(135deg, #245c86, #2c997c);
  position: relative;
  box-shadow: inset 0 -7px 0 rgba(255,255,255,.22);
}
.mark:before {
  content: "";
  position: absolute;
  left: 9px;
  bottom: 10px;
  width: 20px;
  height: 8px;
  border-radius: 2px 2px 7px 7px;
  background: #fff;
}
h1 { margin: 0; font-size: 18px; line-height: 1.1; letter-spacing: 0; }
.subtitle { margin-top: 3px; color: var(--muted); font-size: 12px; }
.stats { display: flex; flex-wrap: wrap; gap: 8px; justify-content: flex-end; }
.stat {
  min-width: 92px;
  padding: 8px 10px;
  border: 1px solid var(--line);
  border-radius: 8px;
  background: white;
}
.stat strong { display: block; font-size: 18px; line-height: 1; }
.stat span { display: block; margin-top: 4px; color: var(--muted); font-size: 11px; }
.main {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 340px;
  gap: 18px;
  padding: 18px;
}
.panel {
  border: 1px solid var(--line);
  border-radius: 8px;
  background: rgba(255,255,255,.88);
  overflow: hidden;
}
.panel-head {
  display: flex;
  justify-content: space-between;
  gap: 12px;
  padding: 14px 16px;
  border-bottom: 1px solid var(--line);
  background: #fff;
}
.panel-title { margin: 0; font-size: 15px; }
.refresh { color: var(--muted); font-size: 12px; white-space: nowrap; }
.harbor {
  min-height: 620px;
  padding: 16px;
  background:
    linear-gradient(180deg, rgba(255,255,255,.74), rgba(255,255,255,.28)),
    repeating-linear-gradient(135deg, transparent 0, transparent 22px, rgba(36,92,134,.045) 22px, rgba(36,92,134,.045) 24px),
    var(--water);
}
.dock-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(260px, 1fr));
  gap: 14px;
}
.dock {
  min-height: 208px;
  border: 1px solid #b9c8d8;
  border-radius: 8px;
  background: linear-gradient(180deg, #fff 0, #f6fbff 100%);
  overflow: hidden;
}
.dock.unknown { border-color: #cbd5e1; background: #f3f4f6; }
.dock-head {
  display: flex;
  justify-content: space-between;
  gap: 10px;
  padding: 12px;
  border-bottom: 1px solid var(--line);
}
.dock-name { font-weight: 700; overflow-wrap: anywhere; }
.dock-meta { margin-top: 4px; color: var(--muted); font-size: 12px; }
.badge {
  align-self: flex-start;
  border-radius: 999px;
  padding: 4px 8px;
  color: #fff;
  font-size: 12px;
  font-weight: 700;
}
.ready { background: var(--ready); }
.pending { background: var(--pending); }
.failed { background: var(--failed); }
.unknown-badge { background: var(--unknown); }
.waterline {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(112px, 1fr));
  gap: 10px;
  padding: 14px;
}
.pod-ship {
  min-height: 96px;
  padding: 10px;
  border: 1px solid #ccd7e3;
  border-radius: 8px;
  background: #fff;
  position: relative;
  animation: float 4.8s ease-in-out infinite;
}
.pod-ship.pending { animation-duration: 3s; }
.pod-ship.failed { animation: none; border-color: #f1b3ad; }
.pod-ship:before {
  content: "";
  display: block;
  width: 48px;
  height: 18px;
  margin-bottom: 7px;
  border-radius: 3px 3px 12px 12px;
  background: #2c6f9f;
  box-shadow: 16px -8px 0 -4px #f4b84a;
}
.pod-ship.failed:before { background: var(--failed); }
.pod-ship.pending:before { background: var(--pending); }
.pod-ship.succeeded:before { background: var(--unknown); }
.pod-name { font-weight: 700; font-size: 13px; overflow-wrap: anywhere; }
.pod-meta { margin-top: 4px; color: var(--muted); font-size: 11px; overflow-wrap: anywhere; }
.empty {
  padding: 18px;
  color: var(--muted);
  text-align: center;
  border: 1px dashed #c9d6e2;
  border-radius: 8px;
  background: rgba(255,255,255,.7);
}
.service-list { padding: 12px; display: grid; gap: 10px; }
.service-card {
  border: 1px solid #bfd0e4;
  border-radius: 8px;
  padding: 12px;
  background: #fff;
}
.service-name { font-weight: 800; overflow-wrap: anywhere; }
.service-card .ip { color: var(--service); font-size: 12px; margin-top: 4px; }
.route {
  margin-top: 10px;
  padding-top: 10px;
  border-top: 1px dashed var(--line);
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}
.route span {
  border-radius: 999px;
  padding: 4px 7px;
  background: #eef6ff;
  color: #18456f;
  font-size: 11px;
}
.route-empty {
  color: var(--failed);
  font-size: 12px;
  margin-top: 10px;
}
.error {
  display: none;
  margin: 12px 16px 0;
  padding: 10px 12px;
  border: 1px solid #f1b3ad;
  border-radius: 8px;
  color: #8a1f17;
  background: #fff4f2;
}
.error.show { display: block; }
@keyframes float {
  0%, 100% { transform: translateY(0); }
  50% { transform: translateY(-3px); }
}
@media (max-width: 900px) {
  .main { grid-template-columns: 1fr; }
  .topbar { align-items: flex-start; flex-direction: column; }
  .stats { justify-content: flex-start; }
}
</style>
</head>
<body>
<div class="shell">
  <header class="topbar">
    <div class="brand">
      <div class="mark" aria-hidden="true"></div>
      <div>
        <h1>Minik8s Harbor</h1>
        <div class="subtitle">Bridge WebUI for pods, nodes, and service routes</div>
      </div>
    </div>
    <div class="stats" id="stats"></div>
  </header>
  <main class="main">
    <section class="panel">
      <div class="panel-head">
        <h2 class="panel-title">Cluster Harbor</h2>
        <div class="refresh" id="refreshText">Loading snapshot...</div>
      </div>
      <div class="error" id="errorBox"></div>
      <div class="harbor">
        <div class="dock-grid" id="dockGrid"></div>
      </div>
    </section>
    <aside class="panel">
      <div class="panel-head">
        <h2 class="panel-title">Service Routes</h2>
        <div class="refresh" id="serviceCount">0 services</div>
      </div>
      <div class="service-list" id="serviceList"></div>
    </aside>
  </main>
</div>
<script>
const state = { lastSnapshot: null };
const phaseClass = phase => {
  const value = String(phase || "Unknown").toLowerCase();
  if (value === "running" || value === "ready") return "ready";
  if (value === "pending") return "pending";
  if (value === "failed") return "failed";
  return "unknown-badge";
};
const podClass = phase => {
  const value = String(phase || "Unknown").toLowerCase();
  if (value === "running") return "ready";
  if (value === "pending") return "pending";
  if (value === "failed") return "failed";
  if (value === "succeeded") return "succeeded";
  return "unknown-badge";
};
function text(value, fallback = "-") {
  return value === undefined || value === null || value === "" ? fallback : String(value);
}
function escapeHTML(value) {
  return text(value).replace(/[&<>"']/g, ch => ({
    "&": "&amp;",
    "<": "&lt;",
    ">": "&gt;",
    "\"": "&quot;",
    "'": "&#39;"
  }[ch]));
}
function labels(value) {
  if (!value || Object.keys(value).length === 0) return "-";
  return Object.entries(value).map(([k, v]) => text(k) + "=" + text(v)).join(", ");
}
function timestampMillis(value) {
  if (!value) return NaN;
  if (typeof value === "number") return value > 1000000000000 ? value : value * 1000;
  const textValue = String(value);
  if (textValue.startsWith("0001-")) return NaN;
  return new Date(textValue).getTime();
}
function age(value) {
  const diff = Date.now() - timestampMillis(value);
  if (!Number.isFinite(diff) || diff < 0) return "-";
  const mins = Math.floor(diff / 60000);
  if (mins < 60) return mins + "m";
  const hours = Math.floor(mins / 60);
  if (hours < 24) return hours + "h" + (mins % 60) + "m";
  return Math.floor(hours / 24) + "d" + (hours % 24) + "h";
}
function renderStats(snapshot) {
  const nodes = snapshot.nodes || [];
  const pods = snapshot.pods || [];
  const services = snapshot.services || [];
  const readyNodes = nodes.filter(n => n.status && n.status.phase === "Ready").length;
  const runningPods = pods.filter(p => p.status && p.status.phase === "Running").length;
  const failedPods = pods.filter(p => p.status && p.status.phase === "Failed").length;
  document.getElementById("stats").innerHTML = [
    [readyNodes + "/" + nodes.length, "Ready nodes"],
    [runningPods + "/" + pods.length, "Running pods"],
    [String(failedPods), "Failed pods"],
    [String(services.length), "Services"]
  ].map(([num, label]) => '<div class="stat"><strong>' + escapeHTML(num) + '</strong><span>' + escapeHTML(label) + '</span></div>').join("");
}
function renderHarbor(snapshot) {
  const pods = snapshot.pods || [];
  const nodes = snapshot.nodes || [];
  const grouped = new Map();
  for (const node of nodes) grouped.set(node.metadata.name, []);
  const unscheduled = [];
  for (const item of pods) {
    const nodeName = item.spec && item.spec.nodeName;
    if (nodeName && grouped.has(nodeName)) grouped.get(nodeName).push(item);
    else unscheduled.push(item);
  }
  const cards = nodes.map(node => {
    const name = node.metadata.name;
    const status = node.status || {};
    const spec = node.spec || {};
    const dockPods = grouped.get(name) || [];
    return '<article class="dock ' + (status.phase === "Unknown" ? "unknown" : "") + '">' +
      '<div class="dock-head">' +
        '<div>' +
          '<div class="dock-name">' + escapeHTML(name) + '</div>' +
          '<div class="dock-meta">' + escapeHTML(text(spec.role, "Worker")) + ' dock - ' + escapeHTML(text(spec.podCIDR)) + '</div>' +
          '<div class="dock-meta">heartbeat ' + escapeHTML(age(status.lastHeartbeat)) + '</div>' +
        '</div>' +
        '<span class="badge ' + phaseClass(status.phase) + '">' + escapeHTML(text(status.phase, "Unknown")) + '</span>' +
      '</div>' +
      '<div class="waterline">' + (dockPods.length ? dockPods.map(renderPod).join("") : '<div class="empty">No pods moored here</div>') + '</div>' +
    '</article>';
  });
  if (unscheduled.length) {
    cards.push('<article class="dock">' +
      '<div class="dock-head">' +
        '<div>' +
          '<div class="dock-name">Unscheduled Waters</div>' +
          '<div class="dock-meta">Pods without a node assignment</div>' +
        '</div>' +
        '<span class="badge pending">Pending</span>' +
      '</div>' +
      '<div class="waterline">' + unscheduled.map(renderPod).join("") + '</div>' +
    '</article>');
  }
  document.getElementById("dockGrid").innerHTML = cards.length ? cards.join("") : '<div class="empty">No nodes or pods in the harbor yet</div>';
}
function renderPod(item) {
  const meta = item.metadata || {};
  const spec = item.spec || {};
  const status = item.status || {};
  const phase = text(status.phase, "Unknown");
  return '<div class="pod-ship ' + podClass(phase) + '" title="' + escapeHTML(labels(meta.labels)) + '">' +
    '<div class="pod-name">' + escapeHTML(meta.namespace || "default") + '/' + escapeHTML(meta.name) + '</div>' +
    '<div class="pod-meta">' + escapeHTML(phase) + ' - ' + escapeHTML(text(status.podIP)) + '</div>' +
    '<div class="pod-meta">node ' + escapeHTML(text(spec.nodeName)) + '</div>' +
    '<div class="pod-meta">up ' + escapeHTML(age(status.startTime)) + '</div>' +
  '</div>';
}
function renderServices(snapshot) {
  const services = snapshot.services || [];
  document.getElementById("serviceCount").textContent = services.length + " services";
  document.getElementById("serviceList").innerHTML = services.length ? services.map(item => {
    const meta = item.metadata || {};
    const spec = item.spec || {};
    const status = item.status || {};
    const endpoints = status.endpoints || [];
    const ports = (spec.ports || []).map(p => text(p.port) + "->" + text(p.targetPort) + (p.nodePort ? " node:" + text(p.nodePort) : "")).join(", ");
    return '<article class="service-card">' +
      '<div class="service-name">' + escapeHTML(meta.namespace || "default") + '/' + escapeHTML(meta.name) + '</div>' +
      '<div class="ip">' + escapeHTML(text(spec.type, "ClusterIP")) + ' ' + escapeHTML(text(status.clusterIP)) + ' - ' + escapeHTML(text(ports)) + '</div>' +
      '<div class="pod-meta">selector ' + escapeHTML(labels((spec.selector || {}).matchLabels)) + '</div>' +
      (endpoints.length ? '<div class="route">' + endpoints.map(ep => '<span>' + escapeHTML(ep.podName) + ' ' + escapeHTML(ep.ip) + ':' + escapeHTML(ep.targetPort) + '</span>').join("") + '</div>' : '<div class="route-empty">No active endpoints</div>') +
    '</article>';
  }).join("") : '<div class="empty">No services are advertising routes</div>';
}
function render(snapshot) {
  state.lastSnapshot = snapshot;
  renderStats(snapshot);
  renderHarbor(snapshot);
  renderServices(snapshot);
  document.getElementById("refreshText").textContent = "Updated " + new Date(snapshot.generatedAt).toLocaleTimeString();
  document.getElementById("errorBox").classList.remove("show");
}
async function loadSnapshot() {
  try {
    const res = await fetch("/ui/api/snapshot", { cache: "no-store" });
    if (!res.ok) throw new Error("snapshot " + res.status);
    render(await res.json());
  } catch (err) {
    const box = document.getElementById("errorBox");
    box.textContent = "Snapshot refresh failed: " + err.message;
    box.classList.add("show");
  }
}
loadSnapshot();
setInterval(loadSnapshot, 3000);
</script>
</body>
</html>
`
