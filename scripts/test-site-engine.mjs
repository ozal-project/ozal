// Runs the real engine.wasm (cmd/policywasm) in Node, as the project page does
// in a browser, and checks it decides like the edge. scripts/build-site-engine.sh
// builds it; scripts/verify.sh runs this after.
import { readFileSync } from "node:fs";
import vm from "node:vm";
import assert from "node:assert/strict";

const dir = new URL("../site/public/", import.meta.url);
const sandbox = { console, TextEncoder, TextDecoder, performance, crypto: globalThis.crypto, setTimeout, clearTimeout, WebAssembly, Uint8Array, Date, Object, Array, Reflect, Error, Promise, Math, JSON };
sandbox.globalThis = sandbox;
vm.createContext(sandbox);
vm.runInContext(readFileSync(new URL("wasm_exec.js", dir), "utf8"), sandbox);

const go = new sandbox.Go();
const { instance } = await WebAssembly.instantiate(readFileSync(new URL("engine.wasm", dir)), go.importObject);
go.run(instance); // returns when main exits; main blocks, so this only starts it
await new Promise((resolve) => setTimeout(resolve, 50));

const explain = (input) => JSON.parse(sandbox.switchboardExplain(JSON.stringify(input)));
const route = { host: "wiki.acme.dev", auth_required: true, access: { required_groups: ["engineering"] } };
const person = (groups) => ({ email: "ana@acme.dev", email_verified: true, groups });

assert.equal(typeof sandbox.switchboardEngine, "string", "the page can say which engine it runs");
assert.equal(explain({ route }).decision.code, "authentication_required");
assert.equal(explain({ route, principal: person(["support"]) }).decision.code, "missing_required_group");
assert.deepEqual(
  (({ allowed, code }) => ({ allowed, code }))(explain({ route, principal: person(["engineering"]) }).decision),
  { allowed: true, code: "allowed" },
);
assert.match(explain({ route: { host: "" } }).error, /host/, "bad input answers with a reason, never throws");
assert.match(JSON.parse(sandbox.switchboardExplain(42)).error, /one JSON string/);

console.log("site engine: 5 checks OK against engine.wasm " + sandbox.switchboardEngine);
process.exit(0);
