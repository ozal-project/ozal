// Reproducible console acceptance against an isolated real edge. Browser
// authentication is host-issued; credentials never enter stdout or artifacts.
import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { mkdtemp, rm, mkdir, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import assert from "node:assert/strict";
import { startDemoEdge, PORT } from "./edge.mjs";
const exec = promisify(execFile);
const binary = process.env.SWITCHBOARD_BIN;
if (!binary) throw new Error("Set SWITCHBOARD_BIN to the built binary");
const root = await mkdtemp(join(tmpdir(), "sb-product-"));
const session = `switchboard-product-${process.pid}`;
let edge;
let step = "start";
const ab = async (...args) => {
  try { return (await exec("agent-browser", ["--session", session, ...(process.env.SWITCHBOARD_BROWSER_ARGS ? ["--args", process.env.SWITCHBOARD_BROWSER_ARGS] : []), "--json", ...args], { maxBuffer: 4 << 20 })).stdout; }
  catch (error) { let detail = String(error.stderr ?? ""); try { detail += JSON.stringify(JSON.parse(error.stdout).error); } catch {} const safe = detail.replace(/operator=[^\s"<>]+/g, "operator=[redacted]").slice(0, 2000); throw new Error(`Browser ${args[0]} ${args[0] === "click" ? args[1] : ""} failed during ${step}: ${safe}`); }
};
const evaluate = async (source) => JSON.parse(await ab("eval", source)).data?.result;
const api = async (method, path, body) => {
  const res = await fetch(`${edge.consoleURL}${path}`, { method, headers: { Authorization: `Bearer ${edge.token}`, "Content-Type": "application/json" }, body: body ? JSON.stringify(body) : undefined });
  assert(res.ok, `fixture API ${path}: HTTP ${res.status}`);
  return res.json();
};
try {
  edge = await startDemoEdge({ binary, dir: join(root, "edge"), adminSocket: true });
  await api("POST", "/_/api/people/invite", { email: "new-operator-test@example.test", groups: ["ops"] });
  step = "host operator login";
  const { stdout } = await exec(binary, ["console", "login", "-config", join(edge.dir, "config.json"), "-name", "Acceptance operator", "-url", edge.consoleURL]);
  const url = stdout.split("\n").find((line) => line.startsWith("http"));
  assert(url, "host login returned no URL");
  await ab("open", url);
  await ab("wait", "--text", "Acceptance operator");
  assert.equal(await evaluate("location.hash"), "", "one-use credential remained in the URL");
  step = "add existing application";
  await ab("click", 'a[href="/applications"]');
  await ab("find", "role", "button", "click", "--name", "Refresh configuration", "--exact");
  await ab("wait", "--text", "Identity:");
  await ab("find", "role", "button", "click", "--name", "Add existing website", "--exact");
  await ab("fill", "#app-host", `extra.demo.localhost:${PORT}`);
  await ab("fill", "#app-target", "http://127.0.0.1:19101");
  await ab("fill", "#app-groups", "ops");
  await ab("find", "role", "button", "click", "--name", "Review configuration", "--exact");
  await ab("wait", "--text", "I reviewed the destination");
  await ab("check", 'input[type="checkbox"]');
  await ab("find", "role", "button", "click", "--name", "Save reviewed application", "--exact");
  await ab("click", "dialog[open] button:last-child");
  await ab("wait", "--text", "Application saved.");
  const setup = await api("GET", "/_/api/setup");
  const added = setup.routes.find((r) => r.host === `extra.demo.localhost:${PORT}`);
  assert.deepEqual(added?.access?.required_groups, ["ops"]);
  step = "static artifact upload and rollback";
  const before = (await api("GET", "/_/api/sites")).sites.find((s) => s.name === "blog").live;
  const pack = join(root, "artifact"); await mkdir(pack);
  await writeFile(join(pack, "index.html"), "<!doctype html><title>Product acceptance</title><h1>Published from Applications</h1>");
  const archive = join(root, "artifact.tar.gz"); await exec("tar", ["-czf", archive, "-C", pack, "."]);
  await ab("find", "text", "Publish an artifact to blog", "click", "--exact");
  await ab("upload", 'details[open] input[type="file"]', archive);
  await ab("find", "role", "button", "click", "--name", "Review publication", "--exact");
  await ab("click", "dialog[open] button:last-child");
  await ab("wait", "--text", "is live. Check its content");
  let content = (await exec("curl", ["-fsS", "--max-time", "10", "-H", `Host: blog.demo.localhost:${PORT}`, `http://127.0.0.1:${PORT}/`])).stdout;
  assert(content.includes("Published from Applications"));
  await api("POST", "/_/api/sites/blog/rollback", { id: before });
  content = (await exec("curl", ["-fsS", "--max-time", "10", "-H", `Host: blog.demo.localhost:${PORT}`, `http://127.0.0.1:${PORT}/`])).stdout;
  assert(!content.includes("Published from Applications"), "rollback did not restore previous bytes");
  step = "membership consequence and cancellation";
  await ab("click", 'a[href="/people"]');
  await ab("wait", "--text", "new-operator-test@example.test");
  await ab("find", "role", "button", "click", "--name", "Edit groups for new-operator-test@example.test", "--exact");
  await ab("fill", 'input[id^="groups-"]', "staff");
  await ab("find", "role", "button", "click", "--name", "Review change", "--exact");
  await ab("press", "Escape");
  let people = await api("GET", "/_/api/people");
  assert.deepEqual(people.users.find((p) => p.email === "new-operator-test@example.test").groups, ["ops"], "cancel changed membership");
  await ab("find", "role", "button", "click", "--name", "Review change", "--exact");
  await ab("click", "dialog[open] button:last-child");
  await ab("wait", "--text", "Membership saved.");
  people = await api("GET", "/_/api/people");
  assert.deepEqual(people.users.find((p) => p.email === "new-operator-test@example.test").groups, ["staff"]);
  step = "GitHub disconnected state";
  await ab("click", 'a[href="/projects"]');
  await ab("find", "role", "button", "click", "--name", "Read projects & GitHub", "--exact");
  await ab("wait", "--text", "Connect your own GitHub App");
  step = "connector guide";
  await ab("click", 'a[href="/connectors"]');
  await ab("find", "role", "button", "click", "--name", "Read connector registry", "--exact");
  await ab("wait", "--text", "No connectors enrolled.");
  step = "responsive layouts and accessibility";
  await ab("click", 'a[href="/applications"]');
  await ab("find", "role", "button", "click", "--name", "Refresh configuration", "--exact");
  await ab("wait", "--text", `extra.demo.localhost:${PORT}`);
  for (const [width, height] of [[1440, 1000], [1024, 900], [390, 844], [320, 800]]) {
    await ab("set", "viewport", String(width), String(height));
    assert.equal(await evaluate("document.documentElement.scrollWidth <= innerWidth"), true, `overflow at ${width}`);
    const result = JSON.parse(await ab("a11y"));
    const violations = result.data?.violations ?? result.data?.results?.violations;
    assert(Array.isArray(violations), "axe returned no violation list");
    assert.equal(violations.length, 0, `axe violations at ${width}`);
    if (width === 1440 || width === 390) await ab("screenshot", join(root, `applications-${width}.png`));
  }
  console.log("Product console: host login, URL cleanup, app review/save, artifact upload/rollback, membership Escape/save, GitHub and connector states, four viewport widths and axe OK");
  console.log(`Screenshots: ${root}`);
} catch (error) {
  console.error(`Product acceptance failed at ${step}: ${error.message}`);
  if (step !== "host operator login") { console.error(await ab("snapshot", "-i").catch(() => "snapshot unavailable")); }
  process.exitCode = 1;
} finally {
  await exec("agent-browser", ["--session", session, "close"]).catch(() => {});
  if (edge) await edge.stop();
  // Retain only screenshots, never config/state or fixture credentials.
  await rm(join(root, "edge"), { recursive: true, force: true });
  await rm(join(root, "artifact"), { recursive: true, force: true });
  await rm(join(root, "artifact.tar.gz"), { force: true });
}
