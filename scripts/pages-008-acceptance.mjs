#!/usr/bin/env node
// An owned loopback lab. Credentials stay in memory and curl's stdin;
// no token is written to config, argv, artifacts or the process log.
import { randomBytes } from "node:crypto";
import { spawn, spawnSync } from "node:child_process";
import { mkdtemp, mkdir, writeFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import net from "node:net";
import { fileURLToPath } from "node:url";
import { setTimeout as delay } from "node:timers/promises";

const github = process.argv.includes("--github");
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const scratch = await mkdtemp(path.join(tmpdir(), "switchboard-pages-008-"));
const secret = randomBytes(32).toString("base64url");
const audience = "https://switchboard-pages-acceptance.invalid";
let child;

function run(command, args, options = {}) {
  const result = spawnSync(command, args, { cwd: root, encoding: "utf8", ...options });
  if (result.error || result.status !== 0) throw new Error(`${path.basename(command)} failed (${result.status ?? "spawn"})`);
  return result.stdout;
}

async function freePort() {
  const server = net.createServer();
  await new Promise((resolve, reject) => { server.once("error", reject); server.listen(0, "127.0.0.1", resolve); });
  const port = server.address().port;
  await new Promise((resolve, reject) => server.close((error) => error ? reject(error) : resolve()));
  return port;
}

async function stop() {
  if (!child || child.exitCode !== null || child.signalCode !== null) return;
  const owned = child;
  const exited = new Promise((resolve) => owned.once("exit", resolve));
  owned.kill("SIGTERM");
  await Promise.race([exited, delay(2500)]);
  if (owned.exitCode === null && owned.signalCode === null) { owned.kill("SIGKILL"); await exited; }
  child = undefined;
}

async function githubToken() {
  const url = process.env.ACTIONS_ID_TOKEN_REQUEST_URL;
  const credential = process.env.ACTIONS_ID_TOKEN_REQUEST_TOKEN;
  if (!url || !credential || !url.startsWith("https://")) throw new Error("GitHub OIDC request environment is unavailable");
  const endpoint = new URL(url); endpoint.searchParams.set("audience", audience);
  const response = await fetch(endpoint, { headers: { Authorization: `bearer ${credential}` }, signal: AbortSignal.timeout(15000) });
  if (!response.ok) throw new Error(`GitHub OIDC request failed (${response.status})`);
  const token = (await response.json()).value;
  if (typeof token !== "string" || !/^[A-Za-z0-9_.-]+$/.test(token)) throw new Error("GitHub returned no usable OIDC token");
  return token;
}

try {
  const binary = process.env.SWITCHBOARD_BIN ? path.resolve(process.env.SWITCHBOARD_BIN) : path.join(scratch, "switchboard");
  if (!process.env.SWITCHBOARD_BIN) run("go", ["build", "-o", binary, "./cmd/switchboard"]);
  const publicPort = await freePort();
  let adminPort = await freePort(); while (adminPort === publicPort) adminPort = await freePort();
  const database = path.join(scratch, "state.db");
  run(binary, ["migrate", "up", "-db", database]);
  const dist = path.join(scratch, "dist"); await mkdir(dist);
  const content = "<!doctype html><title>PAGES-008 acceptance</title><p>verified deployment</p>";
  await writeFile(path.join(dist, "index.html"), content);
  const archive = path.join(scratch, "site.tar.gz");
  run("tar", ["-czf", archive, "-C", dist, "index.html"]);
  const repositoryID = Number(process.env.GITHUB_REPOSITORY_ID);
  const ownerID = Number(process.env.GITHUB_REPOSITORY_OWNER_ID);
  const ref = process.env.GITHUB_REF;
  if (github && (!Number.isSafeInteger(repositoryID) || repositoryID < 1 || !Number.isSafeInteger(ownerID) || ownerID < 1 || !ref?.startsWith("refs/") || !process.env.GITHUB_WORKFLOW_REF)) throw new Error("GitHub repository/owner/ref/workflow context is missing");

  async function start(overrides = {}) {
    await stop();
    const githubTrust = { repository_id: repositoryID, repository_owner_id: ownerID, ref, workflow_ref: process.env.GITHUB_WORKFLOW_REF ?? "", ...overrides.trust };
    const config = {
      listen: `127.0.0.1:${publicPort}`,
      admin: { listen: `127.0.0.1:${adminPort}` },
      pages: { dir: path.join(scratch, "pages"), deploy: { host: "deploy.localhost", audience: overrides.audience ?? audience } },
      projects: [{ name: "acceptance", site: "acceptance", hostnames: ["site.localhost"], deployers: github ? [{ github: githubTrust }] : [{ token: "env:SWITCHBOARD_LAB_DEPLOY_TOKEN" }] }],
      routes: [{ host: "site.localhost", site: "acceptance" }],
    };
    const configPath = path.join(scratch, "config.json"); await writeFile(configPath, JSON.stringify(config), { mode: 0o600 });
    child = spawn(binary, ["-config", configPath, "-db", database], { cwd: root, env: { ...process.env, SWITCHBOARD_LAB_DEPLOY_TOKEN: secret }, stdio: ["ignore", "ignore", "ignore"] });
    for (let attempt = 0; attempt < 100; attempt++) {
      if (child.exitCode !== null) throw new Error("acceptance server exited during startup");
      try { const response = await fetch(`http://127.0.0.1:${adminPort}/_/api/status`, { signal: AbortSignal.timeout(300) }); if (response.ok) return; } catch {}
      await delay(50);
    }
    throw new Error("acceptance server did not become ready");
  }

  function request(method, target, token, expected, code) {
    if (!/^[A-Za-z0-9_.-]+$/.test(token)) throw new Error("invalid in-memory test credential");
    const args = ["--silent", "--show-error", "--noproxy", "*", "--max-time", "15", "--config", "-", "--request", method,
      "--header", "Host: deploy.localhost", "--url", `http://127.0.0.1:${publicPort}${target}`, "--write-out", "\n%{http_code}"];
    if (method === "POST") args.push("--header", "Expect: 100-continue", "--data-binary", `@${archive}`);
    const output = run("curl", args, { input: `header = "Authorization: Bearer ${token}"\n` });
    const split = output.lastIndexOf("\n"); const status = Number(output.slice(split + 1));
    const body = JSON.parse(output.slice(0, split));
    if (status !== expected || (code && body.code !== code)) throw new Error(`HTTP ${status}/${body.code ?? "success"}; expected ${expected}/${code ?? "success"}`);
    return body;
  }

  const upload = "/v1/projects/acceptance/deployments";
  const token = github ? await githubToken() : secret;
  if (github) {
    for (const [label, trust, code] of [
      ["repository", { repository_id: repositoryID + 1 }, "deployer.repository"],
      ["owner", { repository_owner_id: ownerID + 1 }, "deployer.owner"],
      ["ref", { ref: "refs/heads/switchboard-deliberately-untrusted" }, "deployer.ref"],
      ["workflow", { workflow_ref: `${process.env.GITHUB_REPOSITORY}/.github/workflows/switchboard-untrusted.yml@${ref}` }, "deployer.workflow"],
    ]) { await start({ trust }); request("POST", upload, token, 401, code); console.log(`real OIDC rejects other ${label}: OK`); }
    await start({ audience: "https://wrong-audience.invalid" }); request("POST", upload, token, 401, "deployer.token_invalid");
    console.log("real OIDC rejects wrong audience: OK");
  }
  await start();
  request("POST", upload, "deliberately-wrong", 401, "deployer.token_invalid");
  const deployed = request("POST", upload, token, 201);
  if (deployed.project !== "acceptance" || !/^[a-f0-9]{64}$/.test(deployed.id) || !deployed.live) throw new Error("invalid deployment response");
  if (github && (deployed.commit_sha !== process.env.GITHUB_SHA || deployed.run_url !== `https://github.com/${process.env.GITHUB_REPOSITORY}/actions/runs/${process.env.GITHUB_RUN_ID}`)) throw new Error("verified source provenance did not round-trip");
  const live = run("curl", ["--silent", "--show-error", "--noproxy", "*", "--max-time", "15", "--header", "Host: site.localhost", "--url", `http://127.0.0.1:${publicPort}/`, "--write-out", "\n%{http_code}"]);
  const liveSplit = live.lastIndexOf("\n");
  if (Number(live.slice(liveSplit + 1)) !== 200 || live.slice(0, liveSplit) !== content) throw new Error(`public site did not serve the uploaded artifact (HTTP ${live.slice(liveSplit + 1)})`);
  // A status read reuses the upload token: reads spend neither its jti nor the deploy budget.
  const status = request("GET", `${upload}/${deployed.id}`, token, 200);
  if (status.id !== deployed.id || status.project !== "acceptance") throw new Error("project status did not round-trip");
  if (github) {
    request("POST", upload, token, 401, "deployer.replay");
    await start(); request("POST", upload, token, 401, "deployer.replay");
    console.log("real OIDC replay, including process restart: OK");
  }
  console.log(`${github ? "Real GitHub OIDC" : "Project token"} -> curl -> local Switchboard -> live site/status: OK`);
} finally {
  await stop();
  await rm(scratch, { recursive: true, force: true });
}
