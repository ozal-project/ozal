// What can hold a passkey (ID-017). A desktop without a built-in authenticator
// (most Linux machines) jumps straight to "insert your security key"; saying
// beforehand what else works keeps that from being a dead end. The same list
// and rule as the sign-in pages (internal/passkey/assets/passkey.js).
export type Choice = "device" | "manager" | "phone" | "key";
export type Capabilities = Record<string, boolean | undefined> | null;

export const choiceText: Record<Choice, string> = {
  device: "This device: Windows Hello, Touch ID, or the screen lock of a phone or tablet.",
  manager: "A password manager that saves passkeys, built into the browser or added as an extension.",
  phone: "Your phone, if this browser offers a QR code to scan.",
  key: "A security key (USB or NFC).",
};

const order: Choice[] = ["device", "manager", "phone", "key"];

// passkeyChoices orders the choices for this browser: what it confirms goes
// first, what it denies goes last, and every choice stays. Its answer can be
// missing or incomplete, and only the person knows what they have.
export function passkeyChoices(caps: Capabilities): Choice[] {
  const said: Partial<Record<Choice, boolean | undefined>> = {
    device: caps ? caps.userVerifyingPlatformAuthenticator ?? caps.passkeyPlatformAuthenticator : undefined,
    phone: caps ? caps.hybridTransport : undefined,
  };
  const rank = (choice: Choice) => (said[choice] === true ? 0 : said[choice] === false ? 2 : 1);
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
