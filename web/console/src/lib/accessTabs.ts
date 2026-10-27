import { can } from "$lib/adminToken.svelte";
import type { Page } from "$lib/router.svelte";

// People, the groups they form and who runs this console are one page with
// three views; a session sees the views its tools allow.
export function peopleTabs(): { path: Page; label: string }[] {
  return [
    ...(can("people") ? [{ path: "/people" as Page, label: "People" }, { path: "/groups" as Page, label: "Groups" }] : []),
    ...(can("administrators") ? [{ path: "/administrators" as Page, label: "Administrators" }] : []),
  ];
}
