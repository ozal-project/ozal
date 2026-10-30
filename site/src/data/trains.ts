// The four release trains from VISION.md.
export const trains = [
  { v: "v0.1", n: "Edge", when: "Oct – Dec 2026", now: true, line: "The front door, ready for daily use.", items: ["Routing", "Certificates per domain", "Passkeys", "Static sites", "Tunnels", "Telegram alerts"] },
  { v: "v0.2", n: "PKI", when: "Jan – Mar 2027", now: false, line: "Every part proves who it is.", items: ["Private CA", "mTLS between parts", "SSH certificates", "Custom rules"] },
  { v: "v0.3", n: "Access", when: "Apr – Jun 2027", now: false, line: "Beyond web apps.", items: ["SSH and databases", "Telegram approvals", "Session recording", "WAF"] },
  { v: "v0.4", n: "Trust", when: "Jul – Sep 2027", now: false, line: "Decisions anyone can verify.", items: ["Transparency log", "AI-agent gateway", "Explained risk signals"] },
];
