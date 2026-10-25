# Manual browser acceptance

The Playwright browser cache contains Chromium 1248, but the local browser
cannot start in this environment: Chrome exits because `libatk-1.0.so.0` is
missing. No browser or system library was installed. The checks below remain
open for a maintainer with a working Chromium installation. Keep any captures
in a disposable directory such as `/tmp/switchboard-browser-review/`.

## Console: `CTX-001`, `CTX-002`, `CTX-003`, `CTX-005`, `UI-004`

- [ ] Open Activity, Routes and Policy simulator at 1440, 1024, 390 and 320 px.
- [ ] Check page and horizontal navigation for clipping, overlap and unintended
  horizontal page scrolling; keep table scrolling inside its labeled region.
- [ ] Use only the keyboard to reach every navigation item and control; verify
  visible focus, sensible tab order, skip-to-content, dialog focus, Escape,
  cancel and focus return.
- [ ] In Activity, inspect a decision with populated context and one with
  unknown signals. Confirm each value is labeled with its source and time,
  unknown is not presented as a negative, and raw request paths, query strings,
  tokens and credential IDs are absent.
- [ ] In Routes and Policy simulator, inspect an explicitly labeled resource
  and an unlabelled resource. Confirm the latter is shown as unknown, and that
  the explanation identifies matched and unknown context signals.
- [ ] Exercise the configured first-seen step-up journey with a disposable
  identity: a new passkey triggers it once; a later request does not repeat it;
  legacy sessions remain unknown.
- [ ] Open each consequential-action dialog and cancel. Confirm no mutation
  request is sent. For a disposable mutation, submit once and confirm the UI
  reconciles the server response; abort a pending request and confirm the UI
  does not claim that the server rolled it back.
- [ ] Check that the admin token stays in memory for the tab, is not placed in
  the URL, and that stale data and failed requests have distinct labels.

## Project site: `SITE-002`

- [ ] Open the site at 1440, 1024, 390 and 320 px; check navigation, headings,
  calls to action, screenshots and code blocks for clipping or overlap.
- [ ] Navigate the site with keyboard only. Confirm the skip link, section
  navigation, release link and install instructions work with visible focus.
- [ ] Follow each release/install link and compare the page's claims with the
  repository visibility, release channel and shipped assets before changing
  any publication or source-visibility setting.
- [ ] Capture current desktop and mobile screenshots in the disposable
  directory; do not add review captures to the repository.

Record the browser version, viewport sizes, checks that failed, and any
reproduction steps before closing a tracker item. A production or publication
claim requires its own evidence; a local browser review does not establish it.
