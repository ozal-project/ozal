(() => {
  "use strict";
  const byId = (id) => document.getElementById(id);
  const rows = byId("rows");
  const state = byId("state");
  const labels = {"access.allowed":"Access allowed","access.denied":"Access denied","login.started":"Login started","login.failed":"Login failed","login.succeeded":"Login succeeded","route.created":"Route added","route.deleted":"Route removed","config.reloaded":"Configuration reloaded","tunnel.connected":"Tunnel connected","tunnel.disconnected":"Tunnel disconnected","tunnel.rejected":"Tunnel rejected","origin.denied":"Origin pull rejected"};
  const data = [];
  let paused = false;
  let pausedSnapshot = [];
  let queued = false;
  let reconnect = false;
  const make = (tag, text, cls) => { const el = document.createElement(tag); if (text !== undefined) el.textContent = text; if (cls) el.className = cls; return el; };
  const classification = (e) => e.error || /\.(denied|failed|rejected)$/.test(e.type) ? "deny" : /\.(allowed|succeeded)$/.test(e.type) ? "allow" : "";
  const category = (e) => e.type.startsWith("access.") || e.type.startsWith("origin.") ? "access" : e.type.startsWith("login.") ? "login" : e.type.startsWith("tunnel.") ? "tunnel" : "changes";
  const display = (e) => {
    const tr = make("tr", undefined, classification(e));
    const decision = make("td");
    const label = make("div", undefined, "event-label");
    const mark = make("span", classification(e) === "deny" ? "×" : classification(e) === "allow" ? "✓" : "↳", "event-mark");
    mark.setAttribute("aria-hidden", "true"); label.append(mark, make("span", labels[e.type] || e.type));
    decision.append(label);
    const detail = [e.code, e.reason, e.error].filter(Boolean).join(" · ");
    if (detail) decision.append(make("span", detail, "event-detail"));
    const raw = make("details", undefined, "event-raw");
    raw.append(make("summary", "Event fields"), make("code", JSON.stringify(e, null, 2)));
    decision.append(raw);
    const identity = make("td");
    identity.append(make("span", e.actor || "No actor recorded", "event-actor"), make("span", e.host || "No route recorded", "event-host"));
    if (e.remote) identity.append(make("span", e.remote, "subtext"));
    const when = make("td"); const date = new Date(e.time); const valid = !Number.isNaN(date.getTime());
    const timestamp = make("time", valid ? date.toLocaleTimeString([], {hour:"2-digit",minute:"2-digit",second:"2-digit"}) : "Time unavailable", "event-time");
    if (valid) { timestamp.dateTime = date.toISOString(); timestamp.title = date.toISOString(); }
    when.append(timestamp, make("span", valid ? date.toLocaleDateString() : "", "event-date"));
    tr.append(decision, identity, when); return tr;
  };
  const render = () => {
    const query = byId("search").value.trim().toLowerCase();
    const group = byId("category").value;
    const snapshot = paused ? pausedSnapshot : data;
    const filtered = snapshot.filter((e) => (group === "all" || category(e) === group) && (!query || [e.type,e.actor,e.host,e.code,e.reason,e.error,e.remote].some((v) => String(v || "").toLowerCase().includes(query))));
    rows.replaceChildren(...filtered.map(display));
    byId("total").textContent = String(snapshot.length);
    byId("denied").textContent = String(snapshot.filter((e) => e.type === "access.denied").length);
    byId("event-count").textContent = (paused ? "Paused · " : "") + filtered.length + " shown / " + snapshot.length + " retained";
    byId("empty").hidden = filtered.length > 0;
    byId("empty-title").textContent = snapshot.length ? "No matching events" : "No activity yet";
    byId("empty-detail").textContent = snapshot.length ? "Try another category or search term. Filters only apply to retained events." : "Access decisions appear when an authenticated route is used. Public-route requests do not produce access-decision events.";
  };
  byId("search").addEventListener("input", render);
  byId("category").addEventListener("change", render);
  byId("pause").addEventListener("click", () => {
    paused = !paused;
    if (paused) pausedSnapshot = data.slice();
    byId("pause").textContent = paused ? "Resume view" : "Pause view";
    byId("pause").setAttribute("aria-pressed", String(paused));
    render();
  });
  const loadOverview = async () => {
    try {
      const response = await fetch("/_/overview", {cache:"no-store"});
      if (!response.ok) throw new Error("overview unavailable");
      const overview = await response.json();
      const items = overview.routes.map((route) => {
        const li = make("li"); li.append(make("span", route.host, "host"));
        const mode = make("div", undefined, "route-access"); mode.append(make("span", route.protected ? "Protected" : "Public", route.protected ? "tag protected" : "tag"), make("span", route.kind));
        li.append(mode, make("span", route.policy, "subtext")); return li;
      });
      byId("route-list").replaceChildren(...items);
      if (!items.length) byId("route-list").append(make("li", "No routes configured.", "muted"));
      const protectedCount = overview.routes.filter((r) => r.protected).length;
      byId("protected").textContent = String(protectedCount);
      byId("route-count").textContent = protectedCount + " of " + items.length + " configured routes";
      byId("overview-error").hidden = true;
    } catch {
      byId("overview-error").hidden = false;
      byId("route-list").replaceChildren();
      byId("protected").textContent = "—";
      byId("route-count").textContent = "Route configuration unavailable";
    }
  };
  byId("retry").addEventListener("click", loadOverview);
  loadOverview();
  const refresh = setInterval(loadOverview, 30000);
  const es = new EventSource("/_/events");
  es.onopen = () => { state.textContent = reconnect ? "Live · recent history replayed" : "Live event stream"; state.parentElement.classList.remove("offline"); reconnect = true; };
  es.onerror = () => { state.textContent = "Connection lost · retrying…"; state.parentElement.classList.add("offline"); };
  es.onmessage = (message) => {
    try {
      const event = JSON.parse(message.data);
      if (typeof event.type !== "string" || typeof event.time !== "string") return;
      data.unshift(event); if (data.length > 500) data.pop();
      if (!paused && !queued) {
        queued = true;
        requestAnimationFrame(() => { queued = false; if (!paused) render(); });
      }
    } catch { state.textContent = "An event could not be read"; }
  };
  window.addEventListener("pagehide", () => { es.close(); clearInterval(refresh); });
  window.addEventListener("pageshow", (event) => { if (event.persisted) location.reload(); });
  render();
})();
