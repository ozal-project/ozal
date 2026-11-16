#!/usr/bin/env node
// Copies the country flags of an unpacked @radianui/flags package into the
// login host's assets, named by lower-case ISO 3166-1 alpha-2 code, with the
// package's licence. The output is git-ignored; a build embeds it when present.
// Usage:
//   npm pack @radianui/flags@1.1.0 && tar -xzf radianui-flags-1.1.0.tgz
//   node scripts/flags/import.mjs ./package
import { copyFileSync, mkdirSync, readFileSync, readdirSync, rmSync } from "node:fs";
import { join, resolve, dirname } from "node:path";
import { fileURLToPath } from "node:url";

const pkg = resolve(process.argv[2] || "package");
const out = join(dirname(fileURLToPath(import.meta.url)), "../../internal/admin/loginhost/flags");
const manifest = JSON.parse(readFileSync(join(pkg, "source/manifest.json"), "utf8"));

rmSync(out, { recursive: true, force: true });
mkdirSync(out, { recursive: true });
const seen = new Set();
for (const flag of manifest.flags) {
  for (const code of flag.codes) {
    if (!/^[A-Z]{2}$/.test(code) || seen.has(code)) continue;
    const svg = readFileSync(join(pkg, "source/flags", flag.file), "utf8");
    if (/<script|<foreignObject|\son[a-z]+=|href="(?!#)/i.test(svg)) throw new Error(`${flag.file}: active or external content`);
    seen.add(code);
    copyFileSync(join(pkg, "source/flags", flag.file), join(out, code.toLowerCase() + ".svg"));
  }
}
copyFileSync(join(pkg, "LICENSE"), join(out, "LICENSE"));
console.log(`${seen.size} flags from @radianui/flags ${JSON.parse(readFileSync(join(pkg, "package.json"), "utf8")).version}`);
console.log(readdirSync(out).length - 1, "files written to", out);
