# muse-mcp — design

Date: 2026-10-29
Status: draft for review

## 1. Intent

Let Claude Code use the owner's own muse.ai account as a tool: generate images
(including keeping the same character and scene across images while changing
one thing) and chat with the muse agent. Today the only way to do this is by
hand in the muse.ai web app.

The owner looked at [muse2api](https://github.com/www222fff/muse2api), a Python
gateway that exposes muse.ai as an OpenAI-compatible API, and chose **not** to
run it: "rebuild from muse2api knowledge, for safety". The owner does not want
someone else's code handling their muse.ai login. So this is a fresh Go
implementation. muse2api is a reference for how the muse.ai page behaves, and
none of its code is used or run.

## 2. Goals, non-goals, anti-goals

**Goals**
- An MCP server (stdio) that Claude Code launches, with tools for:
  - **Images**: text-to-image, and image generation from reference images (saved
    or ad hoc), saved to a file path the caller chooses.
  - **Chat**: send a message to the muse agent, get the reply, continue the same
    conversation.
  - **Saved references**: named characters and scenes (images plus fixed
    description) that image requests can cite by name.
  - **Approval hand-off**: when muse asks permission to act on the owner's
    accounts, the decision goes to the owner through Claude Code.
- Login happens once, by the owner, in a Chrome window the server controls.
  Cookies are never copied, pasted or exported.

**Non-goals (for now)**
- Video. muse.ai answered "video generation is blocked for this account" in
  testing. Leave it out entirely; don't add a stub tool.
- Multiple muse accounts, account pools, an HTTP/OpenAI API, a dashboard, API
  keys, quota reading.
- Talking to muse.ai's backend without a browser (see §4: not feasible).

**Anti-goals (failure even if it "works")**
- The server ever approves a muse action on its own. Every approval must be an
  explicit owner decision.
- Login cookies leave the dedicated Chrome profile (logged, written to other
  files, sent anywhere). This includes response bodies: the spike's network log
  captured an access token from `/api/auth/check`, so the server must not log
  network bodies.
- It interrupts or edits the owner's existing muse chats. The owner uses muse
  for real work (for example posting to Facebook), and the server only ever
  works in chats it created.

## 3. What the spike established (2026-10-29, Chrome 156, live account)

Throwaway scripts are in `docs/spike/` (Node, raw CDP). They
are proven against the live site and are the best reference for the page logic.

- **muse.ai is a Meta product.** It is a chat-based agent ("hatch") with a cloud
  VM per account, and images come from Meta's Muse Image model (per third-party
  reporting). It has one text box. Images are made by asking in chat.
- **No usable direct API.** The chat goes over
  `wss://hatch.metaaivm.com/v1/noise?vm_id=…&auth_token=…`, and every frame is
  binary and encrypted (Noise protocol). The only plain HTTP endpoints seen are
  housekeeping: `/api/session`, `/api/hatch/vm/wake`, `/api/auth/check`,
  `/api/consent/status`, Meta logging (`/api/falco`), Sentry and Vercel. **So
  the server drives a real Chrome page.**
- **Driving the page works**, using muse2api's selectors unchanged:
  - New chat: navigate to `https://muse.ai/thread/new`. Ready when a `textarea`
    exists, `[data-hatch-shell-hydration-state]` is `hydrated`, and the page
    text doesn't contain `Connecting...`. After sending, the URL becomes
    `https://muse.ai/thread/<uuid>`, which is the chat ID.
  - Type: set the textarea value with the native `HTMLTextAreaElement` value
    setter, then dispatch `input` (React needs this).
  - Send: click the visible button whose `aria-label`/`data-testid` matches
    `/send/i`.
  - Attach: CDP `DOM.setFileInputFiles` on `input[type="file"]` with local
    absolute paths. Verified: the image shows in the user message (as an `img`
    with `alt` = file name) and muse used it. **Several files in one call work
    and keep the given order** (the input has `multiple`). Verified with three
    files: the composer showed them in the order passed, each with a "Remove
    attachment" button, and muse renamed them to `.jpg`.
  - Busy: a `button[aria-label*="Stop" i]` exists while muse is working.
  - Agent text: `div[class*="hatch-agent-bubble-bg"]` (one per agent message).
  - **Result images**: `[data-testid="hatch-chat-attachment-presentation-image"] img`
    with a `blob:` src. They are **not** inside the agent bubble (muse2api's
    code assumes nothing about this, but our first spike did and missed the
    result). Download by running `fetch(blobUrl)` in the page and returning
    base64. Results were `image/webp`, 1920×1280, about 400 KB.
  - Approval card: `[data-testid="hatch-inline-approval-card"]`. muse2api
    clicks a button matching `/^(deny|decline|reject|don.t allow)$/i`. **The
    Approve button's label is unverified**: no approval came up in the spike.
  - Logged out: navigating anywhere lands on `https://muse.ai/` with the text
    "Log in or create an account".
- **Timing:** about 35 to 50 seconds from send to finished image. Chat replies
  stream in over a few seconds.
- **Consistency works:** attaching the first image and saying "this exact same
  fox in the exact same snowy forest … only change: curled up asleep" gave a
  near-identical scene with only the pose changed. Asking for a "last frame with
  head turned right" did the same.
- **muse remembers across chats.** The agent keeps memory and a workspace across
  all chats, and every chat the server opens appears in the owner's muse chat
  list. Some of the consistency may come from memory, not the attachment.
- Only tested **headful** (a visible window). Headless is untested.

### 3a. Video, voice and image tools (checked live later on 2026-10-29)

- **Video:** muse posts a video as a `blob:` `<video>` inside
  `[data-testid="hatch-chat-attachment-presentation-video"]` (1920×1280, 5 s
  in the test). The page has other `<video>` elements (480×480 decoration),
  so only ones inside that wrapper count. AI video was blocked for the test
  account and region; muse made a substitute itself (an animated still).
- **Voice (text to speech):** muse posts a clip as
  `[data-testid="hatch-chat-attachment-presentation-audio"]`, a player card
  with "Play" and "Audio options" buttons (the options menu only has
  "Speed") and no `<audio>` element. Clicking Play loads the clip into one
  page-wide `<audio>` outside the card, with a `blob:` src; it is MP3
  (`audio/mpeg`, mono 24 kHz in the test). muse-mcp clicks Play, pauses the
  player at once, and downloads that blob. The card contains a
  bubble-styled box with the "0:00" clock, so agent text skips anything
  inside an attachment card.
- The voice catalog (1,202 voices: name, gender, accent, tone, description)
  is muse's data. muse-mcp doesn't ship it; the owner asks muse for it as a
  CSV and imports it (`muse-mcp voices import`).
- **What muse's voice tool takes:** a voice, a language code, a speed
  (1 to 200, default 100) and the script, plus a second voice for dialogue
  scripts (muse-mcp doesn't use that). No emotion, style, instruction,
  pitch, volume, SSML or tags (muse's own answer to the owner). A live
  comparison agreed: one voice reading one Vietnamese script with four
  different directions (base, "slow pace", Southern dialect, "lively radio
  host") gave byte-identical MP3s; only the speed muse chose changed a
  clip (15.7 s with no direction, 23 to 24 s with any). So muse-mcp sends
  an explicit speed and no style text, and the muse skill's speech guide turns a
  feeling into speed plus the script's wording and punctuation.
- **What muse's image tool takes** (muse's own answer, same day): an
  ordered list of text and images, and an output format (png, jpg, webp).
  No size, aspect-ratio, resolution, style, seed, quality or
  negative-prompt setting: ratio, style and exclusions are prompt words,
  and the ratio is a request, not an enforced canvas. At most 4 separate
  images per request. It edits an uploaded photo, but the output is
  regenerated, not a pixel edit, so "keep unchanged" is a request. No fixed
  limit on reference images; each one's role should be stated in words.
  In-image text is unreliable (Vietnamese tone marks especially). So
  muse-mcp adds `edit_image` (attached first, "Edit image 1. Change only:
  …"), `keep`, `reference_roles` and `count` (1 to 4) as prompt wording,
  and the muse skill's image guide covers the rest.

## 4. How it works

### 4.1 Shape

A single Go binary, `muse-mcp`, speaking MCP over stdio, added to Claude Code
with `claude mcp add -s user muse -- /path/to/muse-mcp`. It owns:

- A **Chrome process** with a dedicated, persistent profile at
  `~/.muse-mcp/chrome-profile/`, controlled over CDP. The server starts Chrome
  lazily on the first tool call, reuses it for later calls, and shuts it down
  when the server exits.
- A **reference library** at `~/.muse-mcp/library/`.

Language: Go, because the project lives under `~/go/src`. Libraries: the
official MCP Go SDK (`github.com/modelcontextprotocol/go-sdk`) and `chromedp`
(or raw CDP; builder's call), plus `golang.org/x/image/webp` for converting
results.

### 4.2 Login

- Chrome normally runs **hidden**. If any tool finds the page logged out, the
  server relaunches Chrome **visible** on muse.ai and returns a clear error to
  Claude: "Log in to muse.ai in the window that just opened, then try again."
- Once login is detected (polling), the server goes back to hidden mode.
- If muse.ai turns out to block or misbehave in headless Chrome, fall back to a
  visible window positioned off-screen or minimised. This is the builder's call
  after testing. Hidden versus visible is a convenience, not a requirement.
- `muse_status` reports Chrome running, logged in, and the current chat count.

### 4.3 Chats the server creates

- Every image request opens a **fresh chat** and re-attaches its references,
  as the research recommends. A fresh chat is not a blank slate: muse's agent
  keeps memory and a workspace across all chats, including the owner's real
  work. Nothing in the browser can prevent that. The README says so plainly.
- `muse_chat` without a `chat_id` opens a fresh chat. With a `chat_id` (the
  thread UUID it returned earlier) it continues that chat: reuse its tab if
  still open, otherwise navigate to `https://muse.ai/thread/<id>`.
- The server only accepts `chat_id`s it created itself, tracked in
  `~/.muse-mcp/chats.json`. It never sends into the owner's own chats. If
  `chats.json` is missing or unreadable, earlier `chat_id`s are refused ("start
  a new chat"). New chats still work.
- **One request at a time** (a global mutex), held only while a tool call is
  running. muse is a single agent per account, and parallelism is not a goal.
  When a call returns `needs_approval`, the mutex is **released**. Other
  requests (other chats, new images) may run while that approval waits. Only
  that `chat_id` is blocked.

### 4.3a Chrome process

- macOS first (that's the owner's machine). Default binary:
  `/Applications/Google Chrome.app/Contents/MacOS/Google Chrome`; override with
  the `MUSE_CHROME` environment variable. Other OSes aren't required.
- Remote debugging on 127.0.0.1 only, on a free port picked at launch.
- A lock file in `~/.muse-mcp/` keeps a second `muse-mcp` (for example a second
  Claude Code session) from using the same profile. The second instance's tools
  return "muse-mcp is already running in another session".
- On a logged-out page, the relaunch from hidden to visible (§4.2) goes ahead
  even if approvals are pending. Logged out, nothing can be clicked anyway, and
  §4.4 covers losing them.

### 4.4 Approvals

When an approval card appears while the server is waiting on a chat or image
request:

1. The server stops waiting and returns a result with
   `status: "needs_approval"`, the `chat_id`, an `approval_id` (any stable
   identifier for that card), and the card's text (for example "Upload image to
   Page album").
2. The tab stays open on that chat. Further requests on that `chat_id` are
   refused until the approval is decided.
3. Claude calls `muse_decide_approval(chat_id, approval_id, decision:
   "approve"|"deny")`. Claude Code asks the owner for permission on this tool
   call every time, so the owner gets the final yes or no. **The README must
   tell the owner never to add this tool to an always-allow list**; that is how
   the "owner decides each one" promise holds.
4. After clicking, the server keeps waiting on that chat and returns the same
   result shape as the original call (reply text, saved images, or another
   approval).
5. If no decision arrives within 10 minutes, a background timer in the server
   clicks Deny and records that in the chat's state. A later decide call gets
   "already denied (timed out)".
6. Pending approvals live **in memory only**. If the server exits or Chrome is
   relaunched, they are dropped. The card stays unanswered in muse, so nothing
   gets approved. A decide call afterwards gets "no such pending approval (the
   server restarted); open the chat in muse.ai to decide it there", with the
   chat link. The safe failure is "not approved", never "approved".

Image requests can hit approvals too: in muse2api's experience muse sometimes
offers to upload the result as a public link. Same flow.

### 4.5 Reference library

On disk under `~/.muse-mcp/library/<name>/`: `ref.json` (name, kind, description,
created/updated dates) and the image files copied in (`1.png`, `2.png`, …).

- `kind`: `character` or `scene`. A character carries identity (face, markings,
  outfit, proportions); a scene carries layout, props, lighting and style.
- `description`: the fixed "base block" text, reused word for word in every
  prompt.
- Names: lowercase letters, digits and dashes.

### 4.6 Prompt building

`muse_generate_image` builds the muse prompt from its inputs; it doesn't pass the
caller's text through unchanged. The recipe comes from the research (§7):

- Number every attachment in order and say what each one is: "Image 1 is the
  character reference *fox*: <description>. Keep face, fur, markings and
  proportions exactly. Image 2 is the scene reference *snowy-forest*:
  <description>. Keep layout, trees, lighting and style exactly." Ad hoc files
  get "Image N is a reference image" plus an optional caller-supplied note.
- With a `change` input: "Generate a new image. Change ONLY: <change>. Keep
  everything else identical: same character(s), same scene, same lighting, same
  framing and camera angle, same style. Do not add or remove anything else."
- Without references, start with "Generate an image: <prompt>" (proven in the
  spike).
- Optional `aspect_ratio` is added as "aspect ratio W:H".
- Always end with "Attach the final image here in the chat as a single image."

The builder may refine wording, but keep it in one place (a template file or a
single Go file) so it can be tuned from live testing.

### 4.7 Waiting for results (image)

- Before sending, count the image results already on the page (0 in a fresh
  chat).
- Poll about every 1 to 3 seconds: approvals (§4.4), results, busy state.
- **Done** when a new result image has a `blob:` src and the Stop button has
  been gone for a few seconds.
- **Text only, no image** after about 10 seconds idle: send once "Please attach
  the final image here in the chat, as a single image." If there's still no
  image, return an error containing muse's reply text (for example the video
  refusal).
- Timeout: 4 minutes, extended up to 10 minutes while muse is visibly still
  working.
- If the reply has several new images, save all of them (`-1`, `-2`
  suffixes).
- At most **10 attachments per request**, counted after saved references are
  expanded. Above that, the tool returns an error asking for fewer references.

### 4.7a Waiting for results (chat)

- **Done** when the Stop button is gone, and the agent bubble count and last
  bubble's text have stayed unchanged for about 3 seconds. This is muse2api's
  rule: 4 stable polls when not busy, or a forced finish after 12 stable polls
  even if the Stop button lingers. muse can pause between steps, so an early
  finish is possible. The reply is everything the agent wrote after our
  message: all new bubbles, joined in order.
- Approvals, attached images (saved if `save_images_to` is set) and the same
  waiting rules as images apply.
- Timeout: 5 minutes, extended up to 10 minutes while muse is visibly working.
  On timeout, return `status: "timeout"` with the partial reply and the
  `chat_id`. A later `muse_chat` on that chat picks up from there. This isn't
  an error.

### 4.8 Saving images

- `output_path` is required. The extension picks the format: `.webp` is written
  as-is; `.png` and `.jpg`/`.jpeg` are converted. Missing directories are
  created. An existing file is never overwritten: add `-1`, `-2`, and so on, and
  return the real path.
- Relative paths resolve against the server's working directory. Claude Code
  launches MCP servers in the project directory, but the builder should verify
  that and document the result.

## 5. Tools

| Tool | Inputs | Returns |
|---|---|---|
| `muse_status` | none | Chrome running, logged in, open/tracked chats, pending approvals |
| `muse_generate_image` | `output_path` (required); `prompt` or `change` (at least one); `characters` (saved names, optional); `scene` (saved name, optional); `reference_images` (file paths, optional); `reference_notes` (optional); `aspect_ratio` (optional) | `status` (`done`/`needs_approval`), saved paths with size, muse's reply text, `chat_id`, any approval info |
| `muse_chat` | `message` (required); `chat_id` (optional); `attachments` (file paths, optional); `save_images_to` (directory, optional) | `status`, reply text, `chat_id`, saved image paths if muse sent images, approval info |
| `muse_decide_approval` | `chat_id`, `approval_id`, `decision` | same shape as the call that hit the approval |
| `muse_save_reference` | `name`, `kind`, `description`, `image_paths`; with `replace: true`, overwrite an existing entry | the stored entry |
| `muse_list_references` | none | name, kind, description, image count |
| `muse_delete_reference` | `name` | ok |

Tool descriptions are what Claude reads to choose tools. Each one should say
when to use it, in particular: use saved references for recurring
characters/scenes; use `change` to keep everything else the same; and
`muse_decide_approval` is the owner's decision, so never call it without the
owner's instruction.

Errors that Claude can act on (logged out, chat busy awaiting approval, unknown
reference, muse refused) come back as clear tool errors with a next step.

## 6. Code layout and testing

- Keep everything about the muse page (selectors, in-page scripts, text hints
  like "Log in or create an account") in **one file**. Site changes then need
  a one-file fix (muse2api does the same).
- Unit tests: prompt building, library storage, output-path handling, format
  conversion, chat-state bookkeeping.
- Page-logic tests: a local fake muse page (static HTML/JS in `testdata/` that
  mimics the selectors, the Stop button, delayed result images and an approval
  card) driven by real headless Chrome. CI-safe, no network.
- Live test, opt-in only (`MUSE_LIVE=1`): one text-to-image and one
  reference-image generation against the real account. Never runs by default,
  because it costs quota and adds chats to the owner's muse list.
- Logging goes to `~/.muse-mcp/muse-mcp.log` (stdout belongs to MCP). Never log
  cookies, request/response bodies, or tokens.

## 7. Background: consistency research (summary)

From a research pass (vendor guides, practitioner posts; no controlled tests):
re-attach the same master reference every time instead of relying on chat
memory; state the single change plus an explicit keep-list; reuse a fixed
description word for word; give character and scene separate, labeled
reference images; avoid chaining edits more than 2 or 3 times without going
back to the master; consider multi-view character sheets as references.
Reportedly Muse Image accepts several reference images in one prompt. §4.6
encodes this.

## 8. Decided by the owner

- Build a fresh implementation in Go; don't run muse2api (safety).
- Scope: chat and images only; video out for now.
- Approvals: the owner approves each one; the server never auto-approves.
- Support both recurring characters/scenes and one-off images.
- Log in in a separate Chrome profile (Claude-in-Chrome extension wasn't
  available in the session).

## 9. Calls made by Claude (owner to check)

- Chrome hidden by default, visible only for login.
- Fresh muse chat for every image request.
- One request at a time.
- Approval timeout: 10 minutes, then Deny.
- `output_path` required; format by extension; never overwrite.
- Server only touches chats it created.
- Library and profile under `~/.muse-mcp/`.
- No quota tool.
- The mutex is released while an approval waits; only that chat is blocked.
- Pending approvals are in memory only. A restart drops them (left unanswered,
  never approved).
- macOS first, `MUSE_CHROME` override, single-instance lock.
- Chat "done" rule and timeout returning partial text (§4.7a).
- At most 10 attachments per request.
- A lost `chats.json` means old chat IDs are refused.

## 10. Left to the builder

Go module path, chromedp versus raw CDP, exact polling intervals, exact prompt
wording within §4.6, how `approval_id` is derived, internal package layout,
headless versus off-screen fallback (§4.2).

## 11. Risks

- **muse.ai changes its page.** Mitigation: one selectors file, and the fake-page
  tests document the assumptions.
- **The Approve button is unverified.** Find its label on the first live
  approval. Until then, match `/^(approve|allow|confirm|yes|continue)$/i` inside
  the card and fail loudly (leaving the card untouched) if nothing matches.
- **Headless detection** (§4.2).
- **Account restrictions** like the video block may apply to other features
  later. Surface muse's own text in errors.
- **Terms of service:** this automates the owner's own account for personal
  use. Behaving like a person (one request at a time, no scraping) keeps the
  footprint small.
- **Chat list clutter:** every request adds a chat to the owner's muse list.
  Deleting chats is out of scope for now.
