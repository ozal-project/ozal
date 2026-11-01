// A second administrator passkey (ID-017). With only one, losing that device
// leaves the host's recovery as the only way back in, so the console suggests
// another place until there is one. An unknown list never claims either way.
export function needsBackup(passkeys: readonly unknown[] | null, later: boolean): boolean {
  return !later && passkeys !== null && passkeys.length === 1;
}

// "Not now" lasts for this tab: the next sign-in asks again. Storage that is
// missing or refuses access means the suggestion simply shows.
const laterKey = "ozal.backupPasskeyLater";
type Store = Pick<Storage, "getItem" | "setItem">;

export function readLater(store: Store | undefined): boolean {
  try { return store?.getItem(laterKey) === "1"; } catch { return false; }
}

export function rememberLater(store: Store | undefined): void {
  try { store?.setItem(laterKey, "1"); } catch { /* the suggestion returns next time */ }
}
