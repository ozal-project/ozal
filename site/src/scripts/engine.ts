// The project page's live demo (SITE-004). It loads engine.wasm, the access
// decision the edge runs compiled to WebAssembly, and shows what it answers for
// a route and a visitor chosen on the page. Everything is rendered with
// textContent: nothing typed or returned is ever parsed as HTML.
import { cellWord, describe, plainDecision, requestFor, stagesFor, type Report, type Visitor, APPS, PEOPLE } from "../lib/engine";

declare const Go: new () => { importObject: WebAssembly.Imports; run(instance: WebAssembly.Instance): Promise<void> };
declare global {
  interface Window {
    ozalExplain?: (input: string) => string;
    ozalEngine?: string;
  }
}

let loading: Promise<void> | null = null;

function loadScript(src: string): Promise<void> {
  return new Promise((resolve, reject) => {
    const script = document.createElement("script");
    script.src = src;
    script.onload = () => resolve();
    script.onerror = () => reject(new Error(`could not load ${src}`));
    document.head.appendChild(script);
  });
}

// ensureEngine downloads and starts the engine once.
function ensureEngine(): Promise<void> {
  loading ??= (async () => {
    await loadScript("/wasm_exec.js");
    const go = new Go();
    let instance: WebAssembly.Instance;
    try {
      instance = (await WebAssembly.instantiateStreaming(fetch("/engine.wasm"), go.importObject)).instance;
    } catch {
      // A server that does not send application/wasm: instantiate from bytes.
      const bytes = await (await fetch("/engine.wasm")).arrayBuffer();
      instance = (await WebAssembly.instantiate(bytes, go.importObject)).instance;
    }
    void go.run(instance); // main blocks on purpose: the functions stay callable
    for (let i = 0; i < 40 && !window.ozalExplain; i++) await new Promise((r) => setTimeout(r, 50));
    if (!window.ozalExplain) throw new Error("the engine did not start");
  })();
  loading.catch(() => (loading = null)); // a failed load can be retried
  return loading;
}

const $ = <T extends HTMLElement>(root: ParentNode, selector: string) => root.querySelector(selector) as T;
const text = (node: Element, value: string) => (node.textContent = value);
const personName = (who: string) => (who === "none" ? "No session" : PEOPLE[who] ?? who);

type Settings = Omit<Visitor, "who">;

function readSettings(root: HTMLElement): Settings {
  return {
    verified: $<HTMLInputElement>(root, 'input[name="verified"]').checked,
    method: $<HTMLSelectElement>(root, 'select[name="method"]').value,
    bound: $<HTMLInputElement>(root, 'input[name="bound"]').checked,
  };
}

const ask = (app: string, visitor: Visitor): Report | { error: string } =>
  JSON.parse(window.ozalExplain!(JSON.stringify(requestFor(app, visitor))));

function render(root: HTMLElement, report: Report | { error: string }, app: string, visitor: Visitor) {
  const verdict = $(root, "[data-verdict]");
  const stages = $(root, "[data-stages]");
  stages.replaceChildren();
  if ("error" in report) {
    verdict.dataset.state = "error";
    text($(root, "[data-verdict-title]"), "The engine refused the input");
    text($(root, "[data-code]"), "");
    text($(root, "[data-reason]"), report.error);
    return;
  }
  const d = describe(report);
  verdict.dataset.state = d.state;
  // The cord from the person to the app shows how far the request got.
  const trace = $(root, "[data-trace]");
  trace.dataset.state = d.state;
  text($(root, "[data-trace-who]"), personName(visitor.who));
  text($(root, "[data-trace-app]"), APPS[app].host);
  // Restart the arrival so each new answer reads as a new answer.
  for (const [node, name] of [[trace, "trace-new"], [verdict, "verdict-new"]] as const) {
    node.classList.remove(name);
    void node.offsetWidth;
    node.classList.add(name);
  }
  text($(root, "[data-verdict-title]"), d.title);
  text($(root, "[data-code]"), report.decision.code);
  text($(root, "[data-reason]"), plainDecision(report, app, visitor));
  // A narrow screen hides each app's rule in the matrix, so the answer names it.
  const rule = root.querySelector<HTMLElement>(`[data-cell][data-app="${app}"]`)?.dataset.appRule;
  text($(root, "[data-rule]"), rule ? `${app.charAt(0).toUpperCase()}${app.slice(1)}'s rule: ${rule}` : "");
  for (const stage of stagesFor(report)) {
    const li = document.createElement("li");
    li.className = "stage";
    const dot = document.createElement("span");
    dot.className = `lamp ${stage.state === "ok" ? "lamp-ok" : stage.state === "no" ? "lamp-no" : ""}`;
    li.dataset.state = stage.state;
    const name = document.createElement("span");
    name.className = "stage-name";
    name.textContent = stage.name;
    const note = document.createElement("span");
    note.className = "stage-note";
    note.textContent = stage.note;
    li.append(dot, name, note);
    stages.append(li);
  }
  text($(root, "[data-raw]"), JSON.stringify(report, null, 2));
}

// run asks the engine for every cell of the matrix, then explains the chosen one.
function run(root: HTMLElement) {
  const settings = readSettings(root);
  const bound = $<HTMLInputElement>(root, 'input[name="bound"]');
  bound.disabled = settings.method !== "passkey";
  if (bound.disabled) settings.bound = bound.checked = false;
  const cells = [...root.querySelectorAll<HTMLButtonElement>("[data-cell]")];
  for (const cell of cells) {
    const { who = "", app = "" } = cell.dataset;
    const report = ask(app, { who, ...settings });
    const state = "error" in report ? "error" : describe(report).state;
    const word = "error" in report ? "Error" : cellWord(report);
    // A cell whose answer changed says so, once.
    if (cell.dataset.state && cell.dataset.state !== state) {
      cell.classList.remove("cell-changed");
      void cell.offsetWidth;
      cell.classList.add("cell-changed");
    }
    cell.dataset.state = state;
    text($(cell, ".cell-word"), word);
    cell.setAttribute("aria-label", `${personName(who)}, ${APPS[app].host}: ${word}`);
  }
  const chosen = cells.find((c) => c.getAttribute("aria-pressed") === "true") ?? cells.find((c) => c.dataset.who === "ana" && c.dataset.app === "grafana")!;
  chosen.setAttribute("aria-pressed", "true");
  const { who = "", app = "" } = chosen.dataset;
  const config = APPS[app];
  text($(root, "[data-config]"), JSON.stringify({ host: config.host, ...(config.access ? { access: config.access } : {}) }, null, 2));
  const visitor = { who, ...settings };
  render(root, ask(app, visitor), app, visitor);
}

export function setup(root: HTMLElement) {
  // There is a status line in the recorded answer and one beside the live
  // decision: say the same thing in both, so it is always where the eye is.
  const statuses = [...root.querySelectorAll<HTMLElement>("[data-status]")];
  const say = (message: string) => statuses.forEach((n) => (n.textContent = message));
  const panel = $(root, "[data-panel]");
  const fallback = $(root, "[data-fallback]");
  const start = $<HTMLButtonElement>(root, "[data-start]");
  // Scripts run, so "needs JavaScript" no longer applies; the status line
  // says what is happening instead (loading, failed with a retry, or ready).
  root.querySelector<HTMLElement>("[data-nojs]")?.setAttribute("hidden", "");

  async function boot() {
    start.disabled = true;
    say("Loading the engine (about 2 MB)…");
    try {
      await ensureEngine();
    } catch {
      start.disabled = false;
      start.hidden = false;
      start.textContent = "Retry the engine";
      say("The live engine could not load. You can retry; the recorded example remains available.");
      return;
    }
    fallback.hidden = true;
    panel.hidden = false;
    start.hidden = true;
    say(`Running in your browser · engine ${window.ozalEngine}`);
    run(root);
  }

  root.addEventListener("input", () => window.ozalExplain && run(root));
  root.addEventListener("click", (event) => {
    const cell = (event.target as Element).closest<HTMLElement>("[data-cell]");
    if (!cell || !window.ozalExplain) return;
    root.querySelectorAll("[data-cell]").forEach((c) => c.setAttribute("aria-pressed", String(c === cell)));
    run(root);
  });
  start.addEventListener("click", boot);
  // Load on arrival unless the visitor asked their browser to save data; then
  // wait for the button.
  const saving = (navigator as Navigator & { connection?: { saveData?: boolean } }).connection?.saveData;
  if (saving) {
    start.hidden = false;
    say("Data saver is on: press the button to load the engine (about 2 MB).");
  } else if ("IntersectionObserver" in window) {
    const watcher = new IntersectionObserver((entries) => {
      if (entries.some((e) => e.isIntersecting)) {
        watcher.disconnect();
        void boot();
      }
    }, { rootMargin: "200px" });
    watcher.observe(root);
  } else {
    start.hidden = false;
  }
}
