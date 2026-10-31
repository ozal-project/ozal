// Where a passkey can live (ID-017). A desktop without a built-in
// authenticator (most Linux machines) jumps straight to "insert your security
// key"; letting the person say first where they keep passkeys, and passing
// that to the browser as a hint, keeps that from being a dead end. The same
// list and rules as the sign-in pages (internal/passkey/assets/passkey.js).
// Hints are a preference the browser may ignore; no product is required.
export type Choice = "device" | "manager" | "phone" | "key";
export type Capabilities = Record<string, boolean | undefined> | null;

export const choices: Record<Choice, { label: string; detail: string; hints: string[] }> = {
  device: { label: "This device", detail: "Windows Hello, Touch ID or the screen lock of this phone or tablet.", hints: ["client-device"] },
  manager: {
    label: "My password manager",
    detail: "Its browser extension takes the prompt: for example Bitwarden (or self-hosted Vaultwarden), 1Password, Proton Pass or KeePassXC, or any other that saves passkeys.",
    hints: [],
  },
  phone: { label: "My phone", detail: "Scan the QR code the browser shows; this computer needs Bluetooth.", hints: ["hybrid"] },
  key: { label: "A security key", detail: "Plugged in by USB or held to NFC.", hints: ["security-key"] },
};

const order: Choice[] = ["device", "manager", "phone", "key"];

const deviceSaid = (caps: Capabilities) => (caps ? caps.userVerifyingPlatformAuthenticator ?? caps.passkeyPlatformAuthenticator : undefined);

// recommendedChoice is the one worth suggesting on this browser, or "" when
// the browser did not say enough to suggest anything.
export function recommendedChoice(caps: Capabilities): Choice | "" {
  const device = deviceSaid(caps);
  return device === true ? "device" : device === false ? "manager" : "";
}

// passkeyChoices orders the choices for this browser: the recommended one
// first, what it confirms next, what it denies last, and every choice stays.
export function passkeyChoices(caps: Capabilities): Choice[] {
  const recommended = recommendedChoice(caps);
  const said: Partial<Record<Choice, boolean | undefined>> = { device: deviceSaid(caps), phone: caps ? caps.hybridTransport : undefined };
  const rank = (choice: Choice) => (choice === recommended ? -1 : said[choice] === true ? 0 : said[choice] === false ? 2 : 1);
  return [...order].sort((a, b) => rank(a) - rank(b) || order.indexOf(a) - order.indexOf(b));
}

type CapabilityAPI = {
  getClientCapabilities?: () => Promise<Record<string, boolean>>;
  isUserVerifyingPlatformAuthenticatorAvailable?: () => Promise<boolean>;
};

// readCapabilities asks the browser briefly. No answer, an error or an old
// browser all mean "unknown", never "unsupported".
export async function readCapabilities(api: CapabilityAPI | undefined, timeout = 1000): Promise<Capabilities> {
  const ask = async (): Promise<Capabilities> => {
    if (typeof api?.getClientCapabilities === "function") return await api.getClientCapabilities();
    if (typeof api?.isUserVerifyingPlatformAuthenticatorAvailable === "function") {
      return { userVerifyingPlatformAuthenticator: await api.isUserVerifyingPlatformAuthenticatorAvailable() };
    }
    return null;
  };
  try {
    return await Promise.race([ask(), new Promise<null>((resolve) => setTimeout(() => resolve(null), timeout))]);
  } catch {
    return null;
  }
}

// withHints adds the chosen place to WebAuthn options; no hint, no member.
export function withHints<T extends Record<string, unknown>>(options: T, hints?: string[]): T {
  return hints && hints.length ? { ...options, hints } : options;
}
