// The comparison and setup pages that are not repository guides. They share
// one list so the sitemap, the home page's read-next links and the page
// metadata cannot drift apart. `title` is the browser title; `h1` is the page
// heading, which can be longer and more specific.
/** @type {{ path: string, nav: string, h1: string, title: string, description: string, section: string, updated: string, modified: string }[]} */
export const ARTICLES = [
  {
    path: "/compare/cloudflare-access/",
    nav: "Ozal vs Cloudflare Access",
    h1: "Ozal and Cloudflare Access: where the decision runs",
    title: "Ozal vs Cloudflare Access · Self-hosted gateway",
    description:
      "A hosted zero-trust service and a self-hosted access gateway compared: deployment, identity, policy, audit and the cases where each one fits.",
    section: "Comparison",
    updated: "17 November 2026",
    modified: "2026-11-17",
  },
  {
    path: "/compare/pomerium/",
    nav: "Ozal vs Pomerium",
    h1: "Ozal and Pomerium: two self-hosted access gateways",
    title: "Ozal vs Pomerium · Self-hosted gateway",
    description:
      "Two self-hosted identity-aware proxies compared by scope, identity, policy, deployment and maturity. Ozal is an early alpha.",
    section: "Comparison",
    updated: "17 November 2026",
    modified: "2026-11-17",
  },
  {
    path: "/guides/grafana-sso/",
    nav: "Grafana behind SSO",
    h1: "Put Grafana behind SSO without changing Grafana",
    title: "Grafana behind SSO · Ozal access gateway",
    description:
      "Keep Grafana on loopback, let Ozal authenticate with OIDC or passkeys, and pass the verified email to Grafana's auth proxy.",
    section: "Guide",
    updated: "17 November 2026",
    modified: "2026-11-17",
  },
  {
    path: "/guides/vpn-vs-access-gateway/",
    nav: "VPN or access gateway?",
    h1: "VPN or access gateway? They solve different halves of the problem",
    title: "VPN or access gateway? · Ozal",
    description:
      "A VPN connects a device to a network; an access gateway authenticates a request to an app. When each fits, and where Ozal stops.",
    section: "Guide",
    updated: "17 November 2026",
    modified: "2026-11-17",
  },
  {
    path: "/guides/protect-internal-app/",
    nav: "Protect an internal app",
    h1: "Protect an internal app without exposing the network",
    title: "Protect an internal app · Ozal access gateway",
    description:
      "Keep an internal app on loopback or a private network, put Ozal in front, and decide who gets in with OIDC or passkeys and a rule per route.",
    section: "Guide",
    updated: "17 November 2026",
    modified: "2026-11-17",
  },
  {
    path: "/guides/oidc-or-passkeys/",
    nav: "OIDC or passkeys",
    h1: "OIDC or passkeys: choosing how people sign in",
    title: "OIDC or passkeys · Ozal sign-in",
    description:
      "Native passkeys and an existing OIDC provider compared: what each needs, what Ozal stores, when to choose each, and how step-up works.",
    section: "Guide",
    updated: "17 November 2026",
    modified: "2026-11-17",
  },
  {
    path: "/guides/reverse-proxy-vs-access-gateway/",
    nav: "Reverse proxy or gateway?",
    h1: "Reverse proxy or access gateway? Where the line sits",
    title: "Reverse proxy or access gateway? · Ozal",
    description:
      "A reverse proxy routes and terminates TLS; an access gateway authenticates and authorizes every request. Compare what each does and when a proxy is enough.",
    section: "Guide",
    updated: "17 November 2026",
    modified: "2026-11-17",
  },
];

/**
 * @param {string} path
 * @returns {(typeof ARTICLES)[number]}
 */
export const getArticle = (path) => {
  const found = ARTICLES.find((article) => article.path === path);
  if (!found) throw new Error(`no article for ${path}`);
  return found;
};
