// A second administrator passkey (ID-017). With only one, losing that device
// leaves the host's recovery as the only way back in, so the console suggests
// another place until there is one. An unknown list never claims either way.
//
// The provider is a hint, never proof. An AAGUID names a provider or a model,
// not a vault or a device: two synced passkeys of one provider may hang on
// one vault, two keys of one model may well be independent, and a passkey of
// unknown provider tells nothing. So the state is "one", "same-provider" (all
// synced in one known provider), "separate" (known providers that differ) or
// "unknown"; none of them says an account is protected.
export type BackupState = "one" | "same-provider" | "separate" | "unknown";
type Hint = { provider?: string; provider_known?: boolean; synced?: boolean };

export function backupState(passkeys: readonly Hint[] | null): BackupState {
  if (!passkeys || passkeys.length === 0) return "unknown";
  if (passkeys.length === 1) return "one";
  if (passkeys.some((p) => !p.provider_known || !p.provider)) return "unknown";
  const providers = new Set(passkeys.map((p) => p.provider));
  if (providers.size > 1) return "separate";
  return passkeys.every((p) => p.synced === true) ? "same-provider" : "unknown";
}

// The console suggests another place for "one" and "same-provider" only.
export function needsBackup(passkeys: readonly Hint[] | null, later: boolean): boolean {
  if (later) return false;
  const state = backupState(passkeys);
  return state === "one" || state === "same-provider";
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
