import { test } from "node:test";
import assert from "node:assert/strict";
import { hostLoginCommand } from "./hostCommand.ts";

test("the host login command names the address the browser uses, tunnel port included", () => {
  assert.equal(hostLoginCommand("http://ozal.localhost:19090"), "sudo ozal console login -url http://ozal.localhost:19090");
  assert.equal(hostLoginCommand("http://ozal.localhost:19090/overview#x"), "sudo ozal console login -url http://ozal.localhost:19090");
  assert.equal(hostLoginCommand("http://127.0.0.1:9091", ["people", "sites"]), "sudo ozal console login -url http://127.0.0.1:9091 -tools people,sites");
  assert.equal(hostLoginCommand("http://ozal.localhost:9091", []), "sudo ozal console login -url http://ozal.localhost:9091 -tools TOOL");
});
