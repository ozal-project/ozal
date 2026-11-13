# Product design

How Ozal's visible surfaces are designed: the project page, the console, the
sign-in pages, notifications and CLI output. [Design](design.md) covers the
code; this file covers what people see and touch.

## The method: name a reference, not an adjective

"Modern", "clean" and "professional" do not describe a design. Each person, or
coding agent, reads them differently, and the result drifts toward a generic
template. Ozal briefs design work the other way:

1. **Name the element.** Not "the page", but one concern: layout, type,
   motion, onboarding, filters, feedback.
2. **Name one reference that is excellent at that element,** and say which
   principle to take from it.
3. **Translate the principle into Ozal's constraints:** its tokens, its
   self-hosted assets, its CSP and its tone.
4. **Say what to leave behind.** Every reference also carries things that are
   wrong for a security product.

A reference is a source of principles, never of material. Never copy another
product's logo, illustrations, copy, layouts pixel for pixel or distinctive
trade dress. If a screen could be mistaken for the reference, it went too far.

## Ozal's character

Every borrowed principle passes through these. When a reference conflicts with
them, they win.

- **Calm, because it guards the door.** A trust plane earns confidence by
  being quiet and exact. No urgency, no spectacle, no celebration of access.
- **Every state is true.** The interface never invents state: no fake
  progress, no guessed health, no "secure" badge without evidence. This is the
  visual form of [claims match evidence](../../VISION.md#principles-for-product-decisions).
- **A decision is a first-class object.** Allowed, refused or waiting is shown
  with its stable code and a reason in plain words, for the operator and for
  the refused person.
- **Two quiet colours, one loud colour.** Warm ink is the ground (about 60%
  of a screen) and graphite carries surfaces, borders and quiet text (about
  30%). Camel is the only loud colour (about 10%). It goes on the one detail
  a screen should be read by first: the headline's key words, the next action,
  the choice the visitor just made, the current place and focus. Everything
  else stays quiet so that detail stands out. Section labels, bullets, link
  underlines, borders and glows are decoration and are never camel. Ivory is
  text and the arch of the mark. If two things on a screen compete for the
  first look, one of them is wrong.
- **States sit outside that budget.** Green, red, blue and yellow mean
  allowed, refused, waiting and warning. They appear only where a real state
  exists, always with a word or a code, and the loud colour never stands in
  for one. Tokens live in `site/src/styles.css` and
  `web/console/src/theme.css`.
- **Nothing leaves the server.** Fonts and images are bundled; no third-party
  requests, analytics or embeds. Motion and styles must work inside the
  [console CSP](../engineering.md#console-asset-and-csp-contract).
- **One voice everywhere.** The site, console, sign-in page, alerts and CLI
  speak the same plain, short English.

## Reference map

Each row is one element, one reference, what Ozal takes and what it leaves.
The references are chosen for the element, not for the brand.

| Element | Reference | Take | Leave |
| --- | --- | --- | --- |
| Landing page layout | stripe.com | One claim per section, the real artifact (a config, a decision, a command) beside short prose, depth on demand. | Gradient spectacle, many competing calls to action. |
| Typography | pentagram.com | Type carries the hierarchy: few sizes, confident headlines, a strict grid, generous measure. | Experimental display faces; Ozal uses bundled Geist Sans and Mono only. |
| Minimalism | muji.com | Remove until only the useful remains; materials are honest; empty space is deliberate. | Retail warmth for its own sake. |
| Art direction | aesop.com | One consistent, restrained tone across every touchpoint, from the project page to a refusal page to an alert. | Product photography and lifestyle imagery. |
| Console UI | linear.app | Density without noise, keyboard-first navigation, fast transitions, identical states everywhere. | Decorative gradients and glow. |
| Micro-interactions | framer.com | Every control answers within 150 ms: pressed, pending, done or failed, each visibly different. | Hover effects that exist only to impress. |
| Animation | apple.com | Motion explains a mechanism, such as a request walking through the pipeline or a route switching snapshot. | Scroll-jacking, autoplay video, motion without information. |
| Onboarding | duolingo.com | One step per screen, an early real win (an app published in minutes), visible progress, immediate confirmation. | Streaks, mascots, guilt reminders and any nudge to grant access faster. |
| Search and filters | airbnb.com | Visible filter chips, live result counts, filter state kept across reloads, an obvious "clear". Applies to access history and activity. | Map-first or image-first browsing. |
| Overview screens | notion.com | Calm blocks, generous whitespace, empty states that teach the next action. | User-arranged layouts; and a service-health dashboard is out of scope (host plane, not trust plane). |

Add or replace a reference by editing this table, with the same four columns.
A reference without a "Leave" entry has not been thought through.

## Rules that follow

- **Motion has a budget.** Animate only `transform` and `opacity`, keep
  interface transitions at 150–250 ms, and disable non-essential motion under
  `prefers-reduced-motion: reduce`. A screen must be complete without motion.
- **Refusals are designed screens, not error pages.** They name the
  application, the reason in plain words, the decision code and what the person
  can do next, without revealing policy the person may not see.
- **Empty, loading and failed states are designed with the full state.** A
  list is not finished until its empty state teaches the next action.
- **Confirm in proportion to risk.** Reversible actions act at once with an
  undo or a clear result; revoking access, deleting and upgrading confirm with
  the consequence spelled out.
- **Copy before chrome.** If a label, heading or sentence makes the meaning
  clear, do not add an icon, badge or colour to say it again.
- **Accessible by default.** WCAG 2.1 AA contrast, visible focus in camel,
  touch targets of at least 44 px, and every state readable without colour.

## Briefing a change

Use this shape when asking a person or an agent for design work:

```text
Surface:     console, Access history screen
Element:     search and filters
Reference:   airbnb.com, for visible filter chips, a live result count and an
             obvious "clear"
Adapt to:    Ozal tokens and Geist; filters for person, application and
             decision; refused rows use the reserved red with their code
Leave:       image-first results; any third-party script or font
Constraints: console CSP, prefers-reduced-motion, keyboard reachable,
             mobile layout without horizontal scroll
Done when:   the empty, loading and no-results states exist and a refused
             row shows its reason without opening it
```

## Review checklist

Before a visible change is complete:

- Which reference and principle does each new pattern come from, and is it
  in the map above?
- Is camel the only loud colour on the screen, at about a tenth of it, and
  is it on the detail that should be seen first rather than on decoration?
- Does every state colour mark a real state?
- Is any state shown that the system cannot prove?
- Are the empty, loading, failed and refused states designed?
- Does it work with reduced motion, keyboard only and at 360 px wide?
- Does it make any request outside the server?
