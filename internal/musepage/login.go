package musepage

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/chromedp"
)

// The login screens, as far as muse-mcp drives them: an email box, an optional
// "found a Meta Account" screen, and a box for the code muse emails.
const (
	selEmail = `input[placeholder="Mobile number or email"], input[type="email"], input[name="email"]`
	// The placeholder can carry a suffix such as " (required)", so emailInputJS
	// also accepts a placeholder that starts with this.
	emailPlaceholder = "mobile number or email"
)

// LoginStep says which screen the login page is on.
type LoginStep string

const (
	StepLoggedIn LoginStep = "logged-in"
	StepEmail    LoginStep = "email"
	StepChoose   LoginStep = "choose" // a "Use phone or email" button hides the email box
	StepMeta     LoginStep = "meta"
	StepCode     LoginStep = "code"
	StepOther    LoginStep = "other" // loading, or a screen muse-mcp does not drive
)

// Word boundaries matter: the landing page says "your agent", which contains
// "your age".
const blockers = `\b(?:birthday|birth date|date of birth|how old|your age|captcha|recaptcha|security check|verify you.re human|are you a robot)\b`

var (
	codeScreenRE = regexp.MustCompile(`(?i)enter (your|the) code|confirmation code`)
	metaScreenRE = regexp.MustCompile(`(?i)found a meta account`)
	wrongCodeRE  = regexp.MustCompile(`(?i)incorrect|isn.t right|invalid`)
	// Screens muse-mcp does not type into; the owner finishes these by hand.
	// blockerRE is what stops the login even beside an email box. unexpectedRE
	// adds the bare word "password", which on a screen with an email box is
	// probably just a "Forgot password?" link: there a visible password box
	// (see LoginSnapshot) is what counts.
	blockerRE    = regexp.MustCompile(`(?i)` + blockers)
	unexpectedRE = regexp.MustCompile(`(?i)\bpassword\b|` + blockers)
)

// Button labels, matched on trimmed text, case-insensitively.
const (
	LabelContinue   = `^continue$`
	LabelMeta       = `^continue( as .*)?$`
	LabelSubmitCode = `^(continue|next|confirm|submit|verify)$`
	LabelResend     = `resend|new code`
	LabelUseEmail   = `^use (phone or email|email or phone|email|mobile number or email)$`
)

// LoginPage is one reading of the login page.
type LoginPage struct {
	Step      LoginStep
	WrongCode bool // the page says the code was wrong
	// Unusual says the page wants something muse-mcp does not type: a password
	// box, a birthday, a captcha or a security check, alone or beside the
	// email box. The owner finishes these by hand.
	Unusual bool
	Text    string // the visible text
}

// Lines returns the first n non-empty lines of the visible text, for showing
// the owner what the page asks.
func (p LoginPage) Lines(n int) []string {
	var out []string
	for _, l := range strings.Split(p.Text, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
			if len(out) == n {
				break
			}
		}
	}
	return out
}

// visibleJS is a JS helper: is element e on screen at all?
const visibleJS = `const vis = e => e.getClientRects().length > 0 && getComputedStyle(e).visibility !== 'hidden';`

// emailInputJS finds the visible email box.
var emailInputJS = fmt.Sprintf(`(() => { %s
  return [...document.querySelectorAll('input')].find(i => vis(i) && !i.disabled && (i.matches(%s)
    || (i.getAttribute('placeholder') || '').toLowerCase().startsWith(%s)));
})()`, visibleJS, q(selEmail), q(emailPlaceholder))

// codeInputJS finds the first visible input that is not a button, checkbox or
// the email box.
var codeInputJS = fmt.Sprintf(`(() => { %s
  return [...document.querySelectorAll('input')].find(i => vis(i) && !i.disabled
    && !['hidden', 'checkbox', 'radio', 'submit', 'button', 'file', 'image'].includes(i.type));
})()`, visibleJS)

var loginReadJS = fmt.Sprintf(`(() => { %s
  return {
    text: document.body ? document.body.innerText : '',
    email: !!%s,
    password: [...document.querySelectorAll('input[type=password]')].some(vis),
    buttons: [...document.querySelectorAll('button,[role=button]')].filter(vis).map(b => (b.innerText || b.value || '').trim()),
  };
})()`, visibleJS, emailInputJS)

// LoginSnapshot reads the login page. It can fail transiently while the page
// loads.
func LoginSnapshot(ctx context.Context) (LoginPage, error) {
	st, err := Snapshot(ctx)
	if err != nil {
		return LoginPage{}, err
	}
	if st.Ready && !st.LoggedOut {
		return LoginPage{Step: StepLoggedIn}, nil
	}
	raw, err := chromedp.Run(ctx, chromedp.Evaluate[struct {
		Text     string   `json:"text"`
		Email    bool     `json:"email"`
		Password bool     `json:"password"`
		Buttons  []string `json:"buttons"`
	}](loginReadJS))
	if err != nil {
		return LoginPage{}, err
	}
	p := LoginPage{Step: StepOther, Text: raw.Text, WrongCode: wrongCodeRE.MatchString(raw.Text), Unusual: raw.Password}
	metaButton := regexp.MustCompile(`(?i)^continue as `)
	useEmail := regexp.MustCompile(`(?i)` + LabelUseEmail)
	hasMeta, hasUseEmail := false, false
	for _, b := range raw.Buttons {
		hasMeta = hasMeta || metaButton.MatchString(b)
		hasUseEmail = hasUseEmail || useEmail.MatchString(b)
	}
	switch {
	case codeScreenRE.MatchString(raw.Text):
		p.Step = StepCode
	case hasMeta || metaScreenRE.MatchString(raw.Text):
		p.Step = StepMeta
	case !raw.Email && hasUseEmail:
		p.Step = StepChoose
		p.Unusual = p.Unusual || blockerRE.MatchString(raw.Text)
	case raw.Email:
		// An email box beside a password box, a captcha and the like is not the
		// plain email step: typing the email would not get the owner anywhere.
		p.Step = StepEmail
		p.Unusual = p.Unusual || blockerRE.MatchString(raw.Text)
	default:
		p.Unusual = p.Unusual || unexpectedRE.MatchString(raw.Text)
	}
	return p, nil
}

// TypeEmail types the email into the login page's email box with a real
// insert-text event, so the page's framework sees the input.
func TypeEmail(ctx context.Context, email string) error {
	if err := focus(ctx, emailInputJS); err != nil {
		return fmt.Errorf("muse email box: %w", err)
	}
	_, err := chromedp.Call(ctx, input.InsertText, input.InsertTextParams{Text: email})
	return err
}

// TypeCode types the digits of a code into the code form, one key press each
// so that boxes which jump to the next digit work too.
func TypeCode(ctx context.Context, code string) error {
	if err := focus(ctx, codeInputJS); err != nil {
		return fmt.Errorf("muse code box: %w", err)
	}
	for _, r := range code {
		if err := chromedp.Do(ctx, chromedp.KeyEvent(string(r))); err != nil {
			return err
		}
	}
	return nil
}

// focus focuses the element el (a JS expression) and selects its text, so that
// typing replaces what is there.
func focus(ctx context.Context, el string) error {
	ok, err := chromedp.Run(ctx, chromedp.Evaluate[bool](fmt.Sprintf(`(() => {
  const e = %s;
  if (!e) return false;
  e.scrollIntoView({block: 'center'});
  e.focus();
  if (e.select) e.select();
  return document.activeElement === e;
})()`, el)))
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("not on the page")
	}
	return nil
}

// buttonJS finds the first visible, enabled button or link whose trimmed text
// matches re.
func buttonJS(re string) string {
	return fmt.Sprintf(`(() => { %s
  const re = new RegExp(%s, 'i');
  return [...document.querySelectorAll('button,[role=button],a')].find(b => vis(b) && !b.disabled
    && b.getAttribute('aria-disabled') !== 'true' && re.test((b.innerText || b.value || '').trim()));
})()`, visibleJS, q(re))
}

// ClickButton clicks the button whose text matches re with real mouse events,
// waiting up to wait for one to appear and be enabled. It reports whether it
// clicked.
func ClickButton(ctx context.Context, re string, wait time.Duration) (bool, error) {
	if _, err := waitCentre(ctx, buttonJS(re), wait); err != nil {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		return false, nil
	}
	return true, click(ctx, buttonJS(re))
}
