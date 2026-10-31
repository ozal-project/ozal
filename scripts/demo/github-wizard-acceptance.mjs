// Invoked by TestGitHubWizardBrowserAcceptance. Uses the production embedded
// SPA and real admin handlers; GitHub is simulated, on loopback only.
import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { mkdir } from "node:fs/promises";
import { resolve, join } from "node:path";
import assert from "node:assert/strict";
const exec = promisify(execFile);
const session = `ozal-github-wizard-${process.pid}`;
const target = process.env.OZAL_WORKFLOW_URL;
if (!target) throw new Error("Run with OZAL_WORKFLOW_BROWSER=1 go test ./internal/admin -run TestGitHubWizardBrowserAcceptance -v");
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
const text = () => evaluate("document.body.innerText");
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
  await ab("set", "viewport", "1440", "1000");
}
try {
  await ab("open", target);
  step = "back from GitHub";
  await ab("wait", "--text", "What are you putting online?");
  await ab("wait", "--text", "octavia/website");
  assert.equal(await evaluate("location.search"), "", "GitHub's parameters stayed in the address");
  assert.equal(await evaluate("document.querySelector('input[name=\"app-kind\"][value=\"github\"]')?.checked"), true, "wizard did not reopen on GitHub");
  await audit("repositories");
  step = "choose repository";
  await ab("find", "text", "octavia/website", "click");
  await ab("wait", "--text", "Branch that publishes");
  assert.equal(await evaluate("document.getElementById('gh-branch')?.value"), "main", "default branch not proposed");
  await button("Next");
  step = "address";
  await ab("wait", "--text", "Its address");
  await ab("find", "label", "Address", "fill", "www.example.test", "--exact");
  await button("Next");
  step = "audience";
  await ab("wait", "--text", "Who can open it?");
  await ab("find", "text", "Everyone", "click", "--exact");
  await button("Review");
  step = "review";
  await ab("wait", "--text", "Opens a pull request that adds .github/workflows/ozal-pages.yml");
  assert((await text()).includes("built from octavia/website (main)"), "review does not name the repository and branch");
  await ab("find", "text", "Read the workflow it adds", "click");
  const content = await evaluate("document.querySelector('pre[aria-label=\"Generated workflow\"]')?.textContent");
  assert(content.includes("https://www.example.test") && content.includes("id-token: write"), "review did not show the generated workflow");
  await audit("review");
  await evaluate("document.querySelector('dialog[open] input[type=checkbox]').click()");
  await button("Save application");
  await ab("wait", "--text", "Save this application?");
  assert((await text()).includes("a pull request adding its workflow is opened"), "confirmation does not mention the pull request");
  await button("Confirm and save");
  step = "saved";
  await ab("wait", "--text", "pull request #19");
  assert((await text()).includes("Only octavia/website, branch main, can publish it."), "trust not confirmed");
  assert.equal(await evaluate("document.querySelector('a[href=\"https://github.com/octavia/website/pull/19\"]') !== null"), true, "no link to the pull request");
  await audit("saved");
  step = "connect GitHub";
  await ab("close");
  await ab("open", process.env.OZAL_CONNECT_URL);
  await ab("wait", "--text", "Add application");
  await button("Add application");
  await ab("find", "text", "A website built from GitHub", "click");
  await ab("wait", "--text", "Connect GitHub once");
  assert((await text()).includes("Its key stays on this server."), "connection panel does not say where the key is kept");
  await audit("connect");
  console.log("GitHub wizard browser acceptance passed: connect panel, install return, repository choice, workflow review, one save with trust and PR; axe 0 at 1440/1024/390/320.");
} catch (error) {
  // What the operator saw helps more than the timeout; it holds no credential.
  console.error(await text().catch(() => ""));
  throw error;
} finally {
  await ab("close").catch(() => {});
}
