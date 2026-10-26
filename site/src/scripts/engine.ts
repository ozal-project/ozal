// The project page's live demo (SITE-004). It loads engine.wasm, the access
// decision the edge runs compiled to WebAssembly, and shows what it answers for
// a route and a visitor chosen on the page. Everything is rendered with
// textContent: nothing typed or returned is ever parsed as HTML.
import { describe, requestFor, stagesFor, type Report, type Visitor, APPS } from "../lib/engine";

declare const Go: new () => { importObject: WebAssembly.Imports; run(instance: WebAssembly.Instance): Promise<void> };
declare global {
  interface Window {
    switchboardExplain?: (input: string) => string;
    switchboardEngine?: string;
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
    for (let i = 0; i < 40 && !window.switchboardExplain; i++) await new Promise((r) => setTimeout(r, 50));
    if (!window.switchboardExplain) throw new Error("the engine did not start");
  })();
  loading.catch(() => (loading = null)); // a failed load can be retried
  return loading;
}

const $ = <T extends HTMLElement>(root: ParentNode, selector: string) => root.querySelector(selector) as T;
const text = (node: Element, value: string) => (node.textContent = value);

function readVisitor(root: HTMLElement): Visitor {
  const checked = (name: string) => Array.from(root.querySelectorAll<HTMLInputElement>(`input[name="${name}"]:checked`)).map((i) => i.value);
  return {
    who: ($(root, 'input[name="who"]:checked') as HTMLInputElement).value,
    groups: checked("group"),
    verified: $<HTMLInputElement>(root, 'input[name="verified"]').checked,
    method: $<HTMLSelectElement>(root, 'select[name="method"]').value,
    bound: $<HTMLInputElement>(root, 'input[name="bound"]').checked,
  };
}

function render(root: HTMLElement, report: Report | { error: string }) {
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
  text($(root, "[data-reason]"), report.decision.reason);
  for (const stage of stagesFor(report)) {
    const li = document.createElement("li");
    li.className = "flex items-baseline gap-3 py-1.5 text-[0.84rem]";
    const dot = document.createElement("span");
    dot.className = `dot dot-${stage.state === "ok" ? "ok" : stage.state === "no" ? "no" : "off"} translate-y-[-1px]`;
    const name = document.createElement("span");
    name.className = "w-20 shrink-0 font-mono";
    name.textContent = stage.name;
    const note = document.createElement("span");
    note.className = stage.state === "skip" ? "text-muted" : "text-ink-2";
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
  const out = window.switchboardExplain!(JSON.stringify(requestFor(app, visitor)));
  render(root, JSON.parse(out));
}

export function setup(root: HTMLElement) {
  const status = $(root, "[data-status]");
  const panel = $(root, "[data-panel]");
  const fallback = $(root, "[data-fallback]");
  const start = $<HTMLButtonElement>(root, "[data-start]");

  async function boot() {
    start.disabled = true;
    text(status, "Loading the engine (about 2 MB)…");
    try {
      await ensureEngine();
    } catch (e) {
      start.disabled = false;
      text(status, `The engine did not load (${(e as Error).message}). The example above is the same decision, pre-recorded.`);
      return;
    }
    fallback.hidden = true;
    panel.hidden = false;
    start.hidden = true;
    text(status, `Running in your browser · engine ${window.switchboardEngine}`);
    run(root);
  }

  root.addEventListener("input", () => window.switchboardExplain && run(root));
  start.addEventListener("click", boot);
  // Load on arrival unless the visitor asked their browser to save data; then
  // wait for the button.
  const saving = (navigator as Navigator & { connection?: { saveData?: boolean } }).connection?.saveData;
  if (saving) {
    start.hidden = false;
    text(status, "Data saver is on: press the button to load the engine (about 2 MB).");
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
