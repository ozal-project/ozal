// hostLoginCommand is the break-glass command for this console. It always
// names the address the browser is using: through an SSH tunnel that port is
// not the edge's admin.listen port, and the printed link must open here.
export function hostLoginCommand(origin: string, tools?: string[]): string {
  const url = new URL(origin);
  return `sudo ozal console login -name YOUR_NAME -url ${url.origin}${tools ? ` -tools ${tools.join(",") || "TOOL"}` : ""}`;
}
