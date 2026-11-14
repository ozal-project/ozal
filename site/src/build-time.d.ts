// The few Node APIs the pages use at build time (Astro renders them in Node)
// to version static files by content. Declared here instead of adding
// @types/node for three signatures.
declare module "node:crypto" {
  export function createHash(algorithm: "sha256"): { update(data: Uint8Array): { digest(encoding: "hex"): string } };
}
declare module "node:fs" {
  export function readFileSync(path: string): Uint8Array;
}
declare const process: { cwd(): string };
