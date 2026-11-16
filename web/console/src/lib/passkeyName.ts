// provider names where a passkey lives only when provider_known; otherwise
// it is a kind of passkey ("Synced passkey") and never shown as a place.
export type OwnPasskey = { id: string; label?: string; provider?: string; provider_known?: boolean; synced?: boolean; created_at: string; last_used_at?: string; current?: boolean };

// passkeyName is what a person recognises a passkey by: their own name for
// it, else its known provider, else a plain "Passkey".
export function passkeyName(passkey: OwnPasskey): string {
  return passkey.label || (passkey.provider_known && passkey.provider) || "Passkey";
}

// passkeyPlace is the provider for the second line, when it is known and not
// already the name.
export function passkeyPlace(passkey: OwnPasskey): string {
  return passkey.provider_known && passkey.provider && passkey.provider !== passkeyName(passkey) ? passkey.provider : "";
}
