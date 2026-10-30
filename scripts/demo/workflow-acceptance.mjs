// Invoked by TestWorkflowBrowserAcceptance. Uses the production embedded SPA,
// real admin handlers/generator and simulated GitHub writes, on loopback only.
import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { mkdir, writeFile } from "node:fs/promises";
import { resolve, join } from "node:path";
import assert from "node:assert/strict";
const exec = promisify(execFile);
const session = `ozal-workflow-${process.pid}`;
const target = process.env.OZAL_WORKFLOW_URL;
if (!target) throw new Error("Run with OZAL_WORKFLOW_BROWSER=1 go test ./internal/admin -run TestWorkflowBrowserAcceptance -v");
const artifactDir = process.env.OZAL_WORKFLOW_ARTIFACTS ? resolve(process.env.OZAL_WORKFLOW_ARTIFACTS) : null;
if (artifactDir) await mkdir(artifactDir, { recursive: true });
let step = "sign-in";
const ab = async (...args) => {
  try {
    const { stdout } = await exec("agent-browser", ["--session", session, ...(process.env.CHROME ? ["--executable-path", process.env.CHROME] : []), ...(process.env.OZAL_BROWSER_ARGS ? ["--args", process.env.OZAL_BROWSER_ARGS] : []), "--json", ...args], { maxBuffer: 4 << 20 });
    return JSON.parse(stdout);
  } catch (error) {
    let detail = String(error.stderr ?? "");
    try { detail += JSON.stringify(JSON.parse(error.stdout).error); } catch { /* no JSON response */ }
    detail = detail.replace(/operator=[^\s"<>]+/g, "operator=[redacted]").slice(0, 600);
    throw new Error(`Browser ${args[0]} failed during ${step}: ${detail}`);
  }
};
const evaluate = async (source) => (await ab("eval", source)).data?.result;
const button = (name) => ab("find", "role", "button", "click", "--name", name, "--exact");
async function audit(label) {
  for (const width of [1440, 1024, 390, 320]) {
    await ab("set", "viewport", String(width), "1000");
    const overflow = await evaluate("({page: document.documentElement.scrollWidth, viewport: innerWidth})");
    assert(overflow.page <= width, `overflow at ${width}: ${JSON.stringify(overflow)}`);
    const data = (await ab("a11y")).data;
    const violations = data?.violations ?? data?.results?.violations;
    assert(Array.isArray(violations), "axe returned no violation list");
    assert.equal(violations.length, 0, `axe at ${width}: ${JSON.stringify(violations.map((v) => ({id: v.id, nodes: v.nodes?.map((n) => n.target)})))}`);
    if (artifactDir) await ab("screenshot", join(artifactDir, `${label}-${width}.png`), "--full");
  }
}
try {
  await ab("open", target);
  await ab("wait", "--text", "Workflow acceptance");
  assert.equal(await evaluate("location.hash"), "", "host credential remained in the address");
  await ab("wait", "--text", "Prepare publishing workflow for blog");
  await button("Prepare publishing workflow for blog");
  step = "preview";
  await button("Preview workflow");
  await ab("wait", "--text", "Read generated workflow");
  const content = await evaluate("document.querySelector('pre[aria-label=\"Generated workflow\"]')?.textContent");
  if (artifactDir) await writeFile(join(artifactDir, "generated.yml"), content);
  assert(content.includes("permissions: {}") && content.includes("id-token: write"), "did not receive the real generator's workflow");
  assert(content.includes("https://deploy.example.test") && content.includes("https://blog.example.test"), "workflow is not bound to the project");
  await ab("find", "text", "Read generated workflow", "click");
  await audit("preview");
  step = "cancel confirmation";
  await button("Review pull request");
  await ab("wait", "--text", "Open this pull request?");
  assert.equal(await evaluate("document.activeElement?.textContent?.trim()"), "Cancel");
  await ab("press", "Escape");
  assert.equal(await evaluate("document.querySelectorAll('dialog[open]').length"), 1, "Escape did not close only the confirmation");
  await ab("wait", "--fn", "document.activeElement?.textContent?.trim() === 'Review pull request'");
  // Change an input after preview: the old review must no longer be usable.
  step = "edited preview";
  await ab("find", "label", "Build directory", "fill", "web/site", "--exact");
  assert.equal(await evaluate("[...document.querySelectorAll('button')].some((b) => b.textContent.trim() === 'Review pull request')"), false, "edited input kept the old review");
  await button("Preview workflow");
  await ab("wait", "--text", "Read generated workflow");
  assert((await evaluate("document.querySelector('pre[aria-label=\"Generated workflow\"]')?.textContent")).includes('working-directory: "web/site"'));
  step = "create proposal";
  await button("Review pull request");
  await audit("confirmation");
  await button("Open pull request");
  await ab("wait", "--text", "Pull request created.");
  assert.equal(await evaluate("document.querySelector('a[href=\"https://github.com/octavia/website/pull/19\"]')?.textContent?.trim()"), "Review pull request #19 on GitHub");
  // Do not navigate to the synthetic PR URL. No real GitHub write occurs.
  await audit("created");
  await button("Close");
  step = "sign-out clears workflow data";
  await ab("set", "viewport", "1440", "1000");
  await button("Workflow acceptance");
  await button("Sign out");
  await ab("wait", "--text", "Private console");
  assert.equal(await evaluate("document.body.textContent.includes('octavia/website')"), false, "repository remained visible after sign-out");
  console.log("Workflow browser acceptance passed: preview, edited-input invalidation, Escape, explicit PR, session cleanup; axe 0 at 1440/1024/390/320.");
} finally {
  await ab("close").catch(() => {});
}
