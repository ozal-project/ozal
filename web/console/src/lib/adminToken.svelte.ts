// The admin token, shared by the console pages that change state. It lives
// only in this tab's memory: never storage, cookies or the URL. Pasting it
// once covers every page until the tab is closed or reloaded.
export const admin = $state({ token: "" });
