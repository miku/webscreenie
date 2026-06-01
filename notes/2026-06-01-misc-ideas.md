# misc ideas

## 2026-06-01 — cookie/consent banners: move from DOM hiding to network filtering

### Observation
`--aggressive` (DOM heuristics) and the Fanboy cosmetic filter list both fail on
heise.de and sueddeutsche.de. These are German publishers running IAB TCF
**consent walls** via Consent Management Platforms (Sourcepoint, Usercentrics,
etc.). Why DOM-level approaches lose here:

- The banner is rendered inside a **cross-origin `<iframe>`** (and often a shadow
  DOM). CSS `display:none` injected into the top document cannot reach inside it,
  and our `hideElements`/`aggressiveScript` only walk the top document
  (`document.body.querySelectorAll('*')`), so they never see the banner nodes.
- It is frequently a full-page **interstitial with scroll-lock**, sometimes a
  hard wall where the article DOM is not even rendered until consent is given.
  Hiding the overlay (even if we could) can leave a blank/partial page.

So the cosmetic list and heuristics are structurally the wrong tool for the CMP
case. The promising direction is to act earlier — at the **network layer** —
and/or to actually **answer** the consent dialog.

### Idea 1 (cheapest win): block CMP/tracker requests at the network layer
Stop the CMP script from ever loading, so the banner never initializes.

- `Network.setBlockedURLs(patterns)` — CDP exists in our cdproto
  (`network.SetBlockedURLs().WithURLPatterns(...)`, network.go:752). Simplest
  possible: pass a list of glob patterns, enable `network.Enable()` first. No
  per-request callback needed.
- For finer control, `fetch.Enable` + listen for `EventRequestPaused`, then
  `fetch.FailRequest(id, network.ErrorReasonBlockedByClient)` to block or
  `fetch.ContinueRequest(id)` to allow. (Both confirmed present in cdproto:
  fetch.go:51/91/182.) This lets us match by host *and* resource type.

Where to get the patterns:
- The **network** rules we currently throw away. `internal/filterlist` parses
  only cosmetic `##` rules; EasyList/EasyPrivacy network rules (`||domain^`,
  `domain^$script`, etc.) are exactly the consent/tracker host blocklist. Add a
  network-rule parser (or a separate minimal list) and translate `||host^` to a
  `*host*` block pattern.
- Dedicated lists worth trying: "I don't care about cookies" (network part),
  EasyList Cookie List network section, and CMP vendor host lists
  (e.g. `*.sourcepoint.*`, `cmp.*`, `*.usercentrics.*`, `consent.*`).

Caveat: on **hard consent walls** blocking the CMP may leave no content at all
(site refuses to render). So this should be a mode, not the default, and we
should detect "page looks empty after block" and fall back.

### Idea 2 (most robust): pre-seed consent cookies / localStorage
Many CMPs render nothing if a valid prior-consent signal is already stored.

- Set the IAB TCF cookie `euconsent-v2` (and CMP-specific cookies) **before**
  navigation via `network.SetCookies` / `storage`. A canned "accept all" or
  "reject all" TCF string per CMP avoids the dialog entirely *and* lets the
  article load as if consented.
- Pair with `localStorage` seeding for CMPs that store state there.
- Maintain a small table keyed by CMP/host. This is the same trick the
  "Consent-O-Matic" and "ddg" approaches use for the stubborn sites.

### Idea 3: actually click "accept/reject" inside the CMP iframe
Port a subset of **Consent-O-Matic** rules (open-source JSON rulesets that map
CMP → the sequence of clicks to dismiss it). This *answers* the dialog so
content loads, rather than hiding it.

- Requires targeting the cross-origin frame's execution context. chromedp can
  run actions against a specific frame; we'd enumerate frames
  (`page.GetFrameTree` / target attach) and run the click script in the CMP
  frame, not the top document. Our current `clickElements`/`hideElements` need
  to be made frame-aware for any of this to work.

### Idea 4: make the existing DOM passes frame-aware
Independent of CMP handling: walk same-origin child frames too (and pierce open
shadow roots) in `hideElements`/`aggressiveScript`. Won't help cross-origin CMP
iframes, but fixes a general blind spot.

### Suggested order of work
1. Idea 1 with `Network.setBlockedURLs` + a small built-in CMP/tracker pattern
   list behind a `--block-requests` flag (quick, low risk, easy to test on
   heise/sueddeutsche). Reuse `internal/filterlist` caching machinery for the
   pattern source.
2. Add empty-page detection → fall back to no-block when a wall blanks the page.
3. Idea 2 (consent cookie seeding) for the hard walls.
4. Idea 3 (Consent-O-Matic-style clicking) only if 1–3 aren't enough.

### Open questions
- Legal/ethical: bypassing consent walls vs. just taking a clean screenshot —
  keep it opt-in and documented.
- Caching the network filter list: dedupe/refresh logic can mirror
  `internal/filterlist`; consider one cache dir with cosmetic + network files.
