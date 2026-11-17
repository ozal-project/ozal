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
