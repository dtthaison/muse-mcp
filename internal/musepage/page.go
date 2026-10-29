// Package musepage drives one muse.ai chat tab. Everything muse-mcp knows
// about the muse.ai page (selectors, in-page scripts, text it looks for) lives
// in this file, so a site change is a one-file fix. The facts were verified
// against the live site on 2026-10-29; see docs/design.md §3.
package musepage

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/kb"
)

const (
	selInput       = `textarea`
	selHydration   = `[data-hatch-shell-hydration-state]`
	selStop        = `button[aria-label*="Stop" i]`
	selAgentBubble = `div[class*="hatch-agent-bubble-bg"]`
	selResultImage = `[data-testid="hatch-chat-attachment-presentation-image"] img`
	selResultVideo = `[data-testid="hatch-chat-attachment-presentation-video"] video` // other <video>s on the page are decoration
	selResultAudio = `[data-testid="hatch-chat-attachment-presentation-audio"]`       // a player card; no <audio> until Play
	// The chat's title is the first line of the chat switcher; muse shows
	// "Thread" until it has named the chat.
	selChatTitle     = `[data-testid="hatch-chat-switcher-trigger"]`
	titlePlaceholder = "Thread"
	selAttachment    = `[data-testid^="hatch-chat-attachment-presentation-"]`
	selPlay          = `button[aria-label="Play"]`
	selApprovalCard  = `[data-testid="hatch-inline-approval-card"]`
	selFileInput     = `input[type="file"]`
	selRemoveChip    = `button[aria-label="Remove attachment"]`

	textLoggedOut = "Log in or create an account"
)

var stallTexts = []string{"Still sending", "Connecting..."}

// Button labels inside an approval card, matched on trimmed text. The deny
// labels come from an earlier project; the approve labels are unverified (no approval
// appeared in the spike), so an unmatched card fails loudly instead of guessing.
var (
	denyLabels    = `^(deny|decline|reject|don.t allow)$`
	approveLabels = `^(approve|allow|confirm|yes|continue)$`
)

var (
	ErrLoggedOut = errors.New("muse.ai is showing its login page")
	ErrNotReady  = errors.New("the muse.ai page did not become ready")
	ErrNoButton  = errors.New("no matching button on the muse approval card; the card was left untouched")
)

// Image is a result image, video or audio clip muse posted in the chat. An
// audio clip has no URL until it is played, so its Src is "audio:<n>", the
// clip's position on the page; Download understands it.
type Image struct {
	Src    string `json:"src"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

// Approval is a pending "may I act on your behalf?" card.
type Approval struct {
	Text string `json:"text"`
}

// State is one snapshot of the chat page.
type State struct {
	URL        string    `json:"url"`
	Ready      bool      `json:"ready"`
	LoggedOut  bool      `json:"loggedOut"`
	Busy       bool      `json:"busy"`
	Stalled    bool      `json:"stalled"`
	AgentTexts []string  `json:"agentTexts"`
	Images     []Image   `json:"images"`
	Videos     []Image   `json:"videos"`
	Audios     []Image   `json:"audios"`
	Title      string    `json:"title"` // empty until muse names the chat
	Approval   *Approval `json:"approval"`
}

func NewChatURL(base string) string    { return strings.TrimRight(base, "/") + "/thread/new" }
func ThreadURL(base, id string) string { return strings.TrimRight(base, "/") + "/thread/" + id }

var threadRE = regexp.MustCompile(`^/thread/([0-9a-fA-F-]{36})/?$`)

// ThreadID extracts the chat UUID from a /thread/<uuid> URL.
func ThreadID(raw string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", false
	}
	m := threadRE.FindStringSubmatch(u.Path)
	if m == nil {
		return "", false
	}
	return m[1], true
}

func q(s string) string { b, _ := json.Marshal(s); return string(b) }

var snapshotJS = fmt.Sprintf(`(() => {
  const body = document.body ? document.body.innerText : '';
  const input = document.querySelector(%[1]s);
  const hyd = document.querySelector(%[2]s);
  const card = %[9]s;
  return {
    url: location.href,
    ready: document.readyState === 'complete' && !!input
      && (!hyd || hyd.getAttribute('data-hatch-shell-hydration-state') === 'hydrated')
      && !body.includes('Connecting...'),
    loggedOut: !input && body.includes(%[7]s),
    busy: !!document.querySelector(%[3]s),
    stalled: %[8]s.some(t => body.includes(t)),
    agentTexts: [...document.querySelectorAll(%[4]s)].filter(b => !b.closest(%[11]s)).map(b => (b.innerText || '').trim()),
    images: [...document.querySelectorAll(%[5]s)].map(i => ({
      src: i.currentSrc || i.src || '', width: i.naturalWidth || 0, height: i.naturalHeight || 0 })),
    videos: [...document.querySelectorAll(%[10]s)].map(v => ({
      src: v.currentSrc || v.src || '', width: v.videoWidth || 0, height: v.videoHeight || 0 })),
    audios: [...document.querySelectorAll(%[12]s)].map((a, i) => ({ src: 'audio:' + i, width: 0, height: 0 })),
    approval: card ? { text: (card.innerText || '').trim() } : null,
    title: (() => {
      const t = document.querySelector(%[13]s);
      const line = t ? (t.innerText || '').split('\n')[0].trim() : '';
      return line === %[14]s ? '' : line;
    })(),
  };
})()`, q(selInput), q(selHydration), q(selStop), q(selAgentBubble), q(selResultImage),
	q(selApprovalCard), q(textLoggedOut), mustJSON(stallTexts), pendingCardJS, q(selResultVideo), q(selAttachment), q(selResultAudio),
	q(selChatTitle), q(titlePlaceholder))

func mustJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

// pendingCardJS finds the newest approval card that still has an enabled
// button. A decided card can stay in the chat with its buttons disabled.
var pendingCardJS = fmt.Sprintf(`[...document.querySelectorAll(%s)].reverse().find(c =>
    [...c.querySelectorAll('button,[role=button]')].some(b => !b.disabled && b.getAttribute('aria-disabled') !== 'true'))`,
	q(selApprovalCard))

// Snapshot reads the page state. It can fail transiently while a page loads.
func Snapshot(ctx context.Context) (State, error) {
	return chromedp.Run(ctx, chromedp.Evaluate[State](snapshotJS))
}

// Open navigates to url and waits until the chat is ready to type into.
func Open(ctx context.Context, url string, timeout time.Duration) (State, error) {
	if err := chromedp.Do(ctx, chromedp.Navigate(url)); err != nil {
		return State{}, fmt.Errorf("opening %s: %w", url, err)
	}
	deadline := time.Now().Add(timeout)
	var s State
	for time.Now().Before(deadline) {
		var err error
		if s, err = Snapshot(ctx); err == nil {
			if s.LoggedOut {
				return s, ErrLoggedOut
			}
			if s.Ready {
				return s, nil
			}
		}
		if err := sleep(ctx, 250*time.Millisecond); err != nil {
			return s, err
		}
	}
	return s, ErrNotReady
}

// Attach puts files on the composer in one go (order is kept) and waits until
// muse shows a chip for each.
func Attach(ctx context.Context, files []string) error {
	if len(files) == 0 {
		return nil
	}
	before, err := chromedp.Run(ctx, chromedp.Evaluate[int](fmt.Sprintf(`document.querySelectorAll(%s).length`, q(selRemoveChip))))
	if err != nil {
		return err
	}
	if err := chromedp.Do(ctx, chromedp.SetUploadFiles(chromedp.CSS(selFileInput), files)); err != nil {
		return fmt.Errorf("attaching files: %w", err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		n, err := chromedp.Run(ctx, chromedp.Evaluate[int](fmt.Sprintf(`document.querySelectorAll(%s).length`, q(selRemoveChip))))
		if err == nil && n >= before+len(files) {
			return nil
		}
		if err := sleep(ctx, 200*time.Millisecond); err != nil {
			return err
		}
	}
	return fmt.Errorf("muse did not accept the %d attached file(s)", len(files))
}

// clickSendJS looks for the send button near the chat box (muse's is within
// 5 levels of the textarea), never inside an approval card: a card button
// like "Send email" is an approval, not the composer.
var clickSendJS = fmt.Sprintf(`(() => {
  const isSend = b => b.offsetParent !== null && !b.closest(%s)
    && /send/i.test((b.getAttribute('aria-label') || '') + ' ' + (b.getAttribute('data-testid') || ''));
  let b = null;
  for (let n = document.querySelector(%s), i = 0; n && !b && i < 7; n = n.parentElement, i++) {
    b = [...n.querySelectorAll('button,[role=button]')].find(isSend);
  }
  if (!b) return 'missing';
  if (b.disabled || b.getAttribute('aria-disabled') === 'true') return 'disabled';
  b.click();
  return 'clicked';
})()`, q(selApprovalCard), q(selInput))

var inputEmptyJS = fmt.Sprintf(`!(document.querySelector(%s) || {value: ''}).value`, q(selInput))

// Send types text into the composer and sends it. The send button can stay
// disabled while attachments upload, so it is retried for up to 30s.
func Send(ctx context.Context, text string) error {
	fill := fmt.Sprintf(`(() => {
  const ta = document.querySelector(%s);
  if (!ta) return false;
  ta.focus();
  Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value').set.call(ta, %s);
  ta.dispatchEvent(new Event('input', { bubbles: true }));
  return true;
})()`, q(selInput), q(text))
	ok, err := chromedp.Run(ctx, chromedp.Evaluate[bool](fill))
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("muse chat box not found")
	}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		res, err := chromedp.Run(ctx, chromedp.Evaluate[string](clickSendJS))
		if err != nil {
			return err
		}
		if res == "clicked" {
			return waitEmpty(ctx)
		}
		if res == "missing" {
			break
		}
		if err := sleep(ctx, 200*time.Millisecond); err != nil {
			return err
		}
	}
	if err := chromedp.Do(ctx, chromedp.KeyEvent(kb.Enter)); err != nil {
		return err
	}
	return waitEmpty(ctx)
}

func waitEmpty(ctx context.Context) error {
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		empty, err := chromedp.Run(ctx, chromedp.Evaluate[bool](inputEmptyJS))
		if err == nil && empty {
			return nil
		}
		if err := sleep(ctx, 100*time.Millisecond); err != nil {
			return err
		}
	}
	return errors.New("message was not sent (the chat box still has text)")
}

const toBase64JS = `async src => {
  const u = new Uint8Array(await (await (await fetch(src)).blob()).arrayBuffer());
  let s = '';
  for (let i = 0; i < u.length; i += 0x8000) s += String.fromCharCode.apply(null, u.subarray(i, i + 0x8000));
  return btoa(s);
}`

// audioSrcJS clicks Play on audio clip n and returns the blob URL muse loads
// into its page-wide player, which it pauses at once.
var audioSrcJS = fmt.Sprintf(`async n => {
  const card = document.querySelectorAll(%s)[n];
  const play = card && card.querySelector(%s);
  if (!play) return '';
  const srcOf = a => a.currentSrc || a.src || '';
  const before = new Set([...document.querySelectorAll('audio')].map(srcOf));
  play.click();
  for (let t = 0; t < 100; t++) {
    await new Promise(r => setTimeout(r, 100));
    const a = [...document.querySelectorAll('audio')].find(a => srcOf(a).startsWith('blob:') && (!before.has(srcOf(a)) || !a.paused));
    if (a) { a.pause(); return srcOf(a); }
  }
  return '';
}`, q(selResultAudio), q(selPlay))

// Download fetches a result image, video or audio clip from inside the page.
// Images and videos have blob: URLs; an audio clip ("audio:<n>") is played
// to get one.
func Download(ctx context.Context, src string) ([]byte, error) {
	srcJS := q(src)
	if rest, ok := strings.CutPrefix(src, "audio:"); ok {
		n, err := strconv.Atoi(rest)
		if err != nil || n < 0 {
			return nil, fmt.Errorf("bad audio clip %q", src)
		}
		srcJS = fmt.Sprintf(`await (%s)(%d)`, audioSrcJS, n)
	}
	js := fmt.Sprintf(`(async () => {
  const src = %s;
  if (!src) throw new Error('muse did not load the audio clip');
  return (%s)(src);
})()`, srcJS, toBase64JS)
	b64, err := chromedp.Run(ctx, chromedp.Evaluate[string](js, chromedp.EvalAwaitPromise))
	if err != nil {
		return nil, fmt.Errorf("downloading image: %w", err)
	}
	return base64.StdEncoding.DecodeString(b64)
}

// Decide clicks Approve or Deny on the approval card.
func Decide(ctx context.Context, approve bool) error {
	labels := denyLabels
	if approve {
		labels = approveLabels
	}
	js := fmt.Sprintf(`(() => {
  const card = %s;
  if (!card) return 'nocard';
  const re = new RegExp(%s, 'i');
  const b = [...card.querySelectorAll('button,[role=button]')].find(b => re.test((b.innerText || '').trim()));
  if (!b || b.disabled) return 'nobutton';
  b.click();
  return 'clicked';
})()`, pendingCardJS, q(labels))
	res, err := chromedp.Run(ctx, chromedp.Evaluate[string](js))
	if err != nil {
		return err
	}
	switch res {
	case "clicked":
		return nil
	case "nocard":
		return fmt.Errorf("no approval card on the page: %w", ErrNoButton)
	default:
		return ErrNoButton
	}
}

func sleep(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}
