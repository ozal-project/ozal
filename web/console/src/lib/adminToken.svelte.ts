// The admin token, shared by the console pages that change state. It lives
// only in this tab's memory: never storage, cookies or the URL. Entering it
// once, in the header, covers every page until the tab is closed or reloaded.
export const admin = $state({ token: "", asked: 0, name: "", expires: "" });

// askForToken is how a page that needs the token says so: the header control
// opens and takes focus, wherever the person was looking on the page.
export function askForToken() {
  admin.asked++;
}
