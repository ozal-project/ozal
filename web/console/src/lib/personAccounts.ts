// What People says about a visitor who is also an administrator: two
// accounts that share only the email. Pure, so the wording is tested
// without a browser.
import { toolSummary } from "./accountMenu.ts";

export type PersonAdmin = { disabled?: boolean; id?: string; name?: string; tools?: string[]; passkeys?: number; last_used?: string };

export const twoAccounts = "One email, two accounts. Each has its own passkeys and sessions; removing one never touches the other. Ozal never merges them by email.";

export function adminBadge(admin: PersonAdmin): string {
  return admin.disabled ? "Administrator · disabled" : "Also an administrator";
}

// adminDetails is what a caller who may see administrators reads about the
// console account; null when the server left the details out.
export function adminDetails(admin: PersonAdmin, locale?: string): { rows: [string, string][]; noBackup: boolean } | null {
  if (!admin.id) return null;
  const tools = toolSummary(admin.tools ?? []);
  const count = admin.passkeys ?? 0;
  const signIn = admin.last_used ? new Date(admin.last_used).toLocaleString(locale, { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" }) : "never";
  return {
    rows: [
      ["Tools", tools.charAt(0).toUpperCase() + tools.slice(1)],
      ["Console passkeys", count === 1 ? "1 passkey" : `${count} passkeys`],
      ["Last sign-in", signIn],
    ],
    noBackup: count === 1 && !admin.disabled,
  };
}

// passkeyName is the owner's name for a visitor passkey, else its provider.
export function passkeyName(key: { provider: string; label?: string }): { name: string; provider: string | null } {
  const label = key.label?.trim();
  return label && label !== key.provider ? { name: label, provider: key.provider } : { name: key.provider, provider: null };
}
