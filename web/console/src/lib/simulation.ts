// What a simulator result was asked for, and whether it still answers the
// form (BUG-010). A result belongs to the inputs submitted with it: once the
// form changes, or a later run fails, it is a previous answer and must not
// look like the verdict for what is on screen.

export type Inputs = {
  host: string;
  mode: "user" | "anon";
  email: string;
  verified: boolean;
  groups: string;
  method: "" | "oidc" | "passkey";
  deviceBound: boolean;
  userVerified: "unknown" | "true" | "false";
  deviceFirstSeen: "unknown" | "true" | "false";
  requestMethod: string;
};

// inputKey normalizes what the engine would see: surrounding spaces and
// empty group entries do not change the question, and a caller without a
// session has no email, groups or session evidence.
export function inputKey(i: Inputs): string {
  if (i.mode === "anon") return JSON.stringify([i.host, "anon"]);
  const groups = i.groups.split(",").map((g) => g.trim()).filter(Boolean);
  return JSON.stringify([i.host, "user", i.email.trim(), i.verified, groups, i.method, i.method === "passkey" && i.deviceBound, i.method === "passkey" ? i.userVerified : "unknown", i.method === "passkey" ? i.deviceFirstSeen : "unknown", i.requestMethod]);
}

export type Freshness = "none" | "current" | "previous";

// freshness says how to show the last result: "current" only when it was
// computed for exactly these inputs and no run has failed since.
export function freshness(result: { key: string } | null, current: Inputs, failedSince: boolean): Freshness {
  if (!result) return "none";
  if (failedSince || result.key !== inputKey(current)) return "previous";
  return "current";
}
