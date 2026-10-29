// The project page's live demo (SITE-004). It loads engine.wasm, the access
// decision the edge runs compiled to WebAssembly, and shows what it answers for
// a route and a visitor chosen on the page. Everything is rendered with
// textContent: nothing typed or returned is ever parsed as HTML.
import { describe, plainDecision, requestFor, stagesFor, type Report, type Visitor, APPS } from "../lib/engine";

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

function readVisitor(root: HTMLElement): Visitor {
  return {
    who: ($(root, 'input[name="who"]:checked') as HTMLInputElement).value,
    verified: $<HTMLInputElement>(root, 'input[name="verified"]').checked,
    method: $<HTMLSelectElement>(root, 'select[name="method"]').value,
    bound: $<HTMLInputElement>(root, 'input[name="bound"]').checked,
  };
}

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
  text($(root, "[data-verdict-title]"), d.title);
  text($(root, "[data-code]"), report.decision.code);
  text($(root, "[data-reason]"), plainDecision(report, app, visitor));
  for (const stage of stagesFor(report)) {
    const li = document.createElement("li");
    li.className = "stage";
    const dot = document.createElement("span");
    dot.className = `lamp ${stage.state === "ok" ? "lamp-ok" : stage.state === "no" ? "lamp-no" : ""}`;
    const name = document.createElement("span");
    name.className = "stage-name";
    name.textContent = stage.name;
    const note = document.createElement("span");
    note.className = stage.state === "skip" ? "quiet" : "soft";
    note.textContent = stage.note;
    li.append(dot, name, note);
    stages.append(li);
  }
  text($(root, "[data-raw]"), JSON.stringify(report, null, 2));
}

function run(root: HTMLElement) {
  const app = ($(root, 'input[name="app"]:checked') as HTMLInputElement).value;
  const visitor = readVisitor(root);
  const bound = $<HTMLInputElement>(root, 'input[name="bound"]');
  bound.disabled = visitor.method !== "passkey" || visitor.who === "none";
  if (bound.disabled) bound.checked = false;
  const config = APPS[app];
  text($(root, "[data-config]"), JSON.stringify({ host: config.host, ...(config.access ? { access: config.access } : {}) }, null, 2));
  const out = window.ozalExplain!(JSON.stringify(requestFor(app, visitor)));
  render(root, JSON.parse(out), app, visitor);
}

export function setup(root: HTMLElement) {
  // There is a status line in the recorded answer and one beside the live
  // decision: say the same thing in both, so it is always where the eye is.
  const statuses = [...root.querySelectorAll<HTMLElement>("[data-status]")];
  const say = (message: string) => statuses.forEach((n) => (n.textContent = message));
  const panel = $(root, "[data-panel]");
  const fallback = $(root, "[data-fallback]");
  const start = $<HTMLButtonElement>(root, "[data-start]");

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
