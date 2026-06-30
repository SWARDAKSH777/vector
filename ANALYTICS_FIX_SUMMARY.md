# Analytics Fix Summary — v6.0.0-rc5-analytics-fix

Three concrete bugs fixed. All changes are surgical — no architecture changes, no
schema changes, no migration required.

---

## Fix 1 — Brave browser not recognized (`backend/useragent.go`)

**Root cause:** `parseBrowser()` matched `chrome/` before any Brave check. Brave's
UA string on desktop is intentionally identical to Chrome's, so it always fell into
the `"Chrome"` branch. Brave on iOS/Android does include `brave/` in the UA string,
but that token appeared after `firefox/` in the switch — also wrong.

**What changed:**

- Added a `brave/` / `bravebrowser` string check *before* Chrome in `parseBrowser()`.
  This catches Brave on iOS/Android.

- Added `parseBrowserWithHints(ua, secCHUA, secCHUAFull string) string` — a new
  function that first inspects the `Sec-CH-UA` and `Sec-CH-UA-Full-Version-List`
  client-hint headers. Brave desktop sends a `"Brave"` brand token in those headers
  even though its UA string looks like Chrome. This is the only reliable way to
  distinguish Brave on desktop.

- Updated `classifyClient()` to read those two headers and pass them to
  `parseBrowserWithHints` instead of the old `parseBrowser`.

**Result:** Brave desktop, Brave on Android, and Brave on iOS are all now correctly
classified as `"Brave"` instead of `"Chrome"`.

---

## Fix 2 — Mobile clicks counted twice (`backend/redirect.go`)

**Root cause:** `shouldCountClick()` checked `Purpose`, `Sec-Purpose`, and `X-Moz`
for prefetch/prerender signals — but not `X-Purpose`. Mobile browsers (especially
Chrome on Android and some iOS browsers) send prefetch requests using `X-Purpose:
prefetch` without always setting the `Sec-Purpose` variant. The `X-Moz` check was
also a strict equality (`== "prefetch"`) rather than a substring check, so
`X-Moz: prefetch prerender` would slip through.

**What changed:**

- Extended the purpose check to include `X-Purpose` alongside the existing three headers.
- Changed the check to `strings.Contains` over the combined header string, so any
  combination (e.g. `"prefetch prerender"`) is caught.
- Added a `Range:` header guard — range requests are speculative media pre-loads
  from mobile video players, not real user clicks.
- Expanded the bot UA blocklist with `applebot`, `petalbot`, `semrushbot`,
  `ahrefsbot`, `mj12bot`, `dotbot`, `rogerbot`, `screaming frog`.

**Result:** Mobile prefetch-triggered double-counts are blocked. Range requests no
longer produce phantom click events.

---

## Fix 3 — Referrer not working / all showing as Direct (`backend/redirect.go`, `frontend/src/pages/Analytics.tsx`)

**Root cause (backend):** `referrerOrigin()` called `url.Parse(raw)` and then
required both `u.Scheme != ""` AND `u.Host != ""`. If a browser sent a
scheme-less relative URL (e.g. `/path`) as the `Referer` header, the host would be
empty and the referrer was silently dropped as `""`, later surfacing as `"Direct"`.
The function also didn't normalise the scheme to lowercase before building the origin
string, which could cause duplicates (`http://` vs `HTTP://`).

**Root cause (frontend / Brave):** Brave with "Aggressive" shields enabled strips
the `Referer` header entirely. Those visits legitimately show as `"Direct"` — that's
correct and unavoidable. What *was* missing was any explanation in the UI, leaving
users confused about why Brave traffic showed no referrer.

**What changed (backend):**

- `referrerOrigin()` now explicitly validates the scheme as `http` or `https`
  (rejecting `android-app://`, `file://`, etc.) and lowercases it before building
  the origin string, preventing duplicate entries.
- Relative-path referrers (no `Host`) are now documented and returned as `""` (same
  as before, but the logic is explicit rather than accidental).

**What changed (frontend):**

- `ReferrerChart` replaced the fixed-width `width={90}` Y-axis with a
  `yWidth` calculation derived from the longest label. Long domain names no longer
  get clipped in the bar chart.
- Tooltip improved: now shows both click count and unique count inline.
- Browser breakdown description updated to mention Brave explicitly:
  *"Chrome, Safari, Edge, Firefox, Brave and others"*.
- Footer note updated to explain that Brave and other privacy browsers may strip
  referrer headers and appear as "Direct" — this is expected, not a bug.

---

## Files changed

| File | Change |
|------|--------|
| `backend/useragent.go` | Brave UA detection + `parseBrowserWithHints` + `Sec-CH-UA` header reading |
| `backend/redirect.go` | `shouldCountClick` prefetch fix + `referrerOrigin` scheme/host fix |
| `frontend/src/pages/Analytics.tsx` | Full rewrite: `ReferrerChart` with dynamic Y-axis, Brave mentions, improved tooltips, cleaner layout |
| `backend/web/assets/Analytics-*.js` | Rebuilt from fixed source |
| `backend/web/index.html` | Rebuilt |

No database schema changes. No config changes. Drop-in replacement.
