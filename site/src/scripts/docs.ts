// Copy buttons for the guides' code blocks. They appear only when the
// clipboard can be written; without this script every block is still there
// to select by hand. The button copies the block's text as written.
if (navigator.clipboard) {
  for (const pre of document.querySelectorAll<HTMLPreElement>(".prose pre")) {
    const wrap = document.createElement("div");
    wrap.className = "code-block";
    const button = document.createElement("button");
    button.type = "button";
    button.className = "copy";
    button.textContent = "Copy";
    button.setAttribute("aria-label", "Copy this code");
    button.addEventListener("click", async () => {
      try {
        await navigator.clipboard.writeText(pre.textContent ?? "");
        button.textContent = "Copied";
      } catch {
        button.textContent = "Select and copy";
      }
      setTimeout(() => (button.textContent = "Copy"), 2000);
    });
    pre.replaceWith(wrap);
    wrap.append(pre, button);
  }
}
