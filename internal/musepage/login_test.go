package musepage_test

import (
	"context"
	"testing"
	"time"

	"github.com/chromedp/chromedp"

	"github.com/dtthaison/muse-mcp/internal/fakemuse"
	. "github.com/dtthaison/muse-mcp/internal/musepage"
)

// loginPageOf shows body as muse's login page and returns what LoginSnapshot reads.
func loginPageOf(t *testing.T, body string) LoginPage {
	t.Helper()
	ctx := newTab(t)
	srv := fakemuse.New()
	t.Cleanup(srv.Close)
	srv.SetLoginPage(`<p>Log in or create an account</p>` + body)
	if err := chromedp.Do(ctx, chromedp.Navigate(srv.URL)); err != nil {
		t.Fatal(err)
	}
	return loginSnapshot(t, ctx)
}

// loginSnapshot polls LoginSnapshot until the page has loaded.
func loginSnapshot(t *testing.T, ctx context.Context) LoginPage {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		p, err := LoginSnapshot(ctx)
		if err == nil && p.Text != "" {
			return p
		}
		if time.Now().After(deadline) {
			t.Fatalf("the login page did not load: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestLoginSnapshotUnusualScreens(t *testing.T) {
	const email = `<input placeholder="Mobile number or email (required)"><button>Continue</button>`
	for _, tc := range []struct {
		name    string
		body    string
		step    LoginStep
		unusual bool
	}{
		{"plain email step", email, StepEmail, false},
		// With an email box a bare "password" is most likely a link to the other login.
		{"forgot password link", email + `<a href="#">Forgot password?</a>`, StepEmail, false},
		{"password box", email + `<input type="password">`, StepEmail, true},
		{"hidden password box", email + `<input type="password" style="display:none">`, StepEmail, false},
		{"captcha", `<p>Please complete the security check</p>` + email, StepEmail, true},
		{"code step", `<p>Enter your code</p><input inputmode="numeric"><button>Continue</button>`, StepCode, false},
		{"code step with a password box", `<p>Enter your code</p><input inputmode="numeric"><input type="password">`, StepCode, true},
		{"password screen without an email box", `<p>Enter your password</p><input type="text"><button>Continue</button>`, StepOther, true},
		// muse.ai's landing page says "your agent"; that is not "your age".
		{"landing page text", email + `<p>Every Muse user shares a computer with your agent.</p>`, StepEmail, false},
		{"use phone or email button", `<p>Log in or create an account</p><button>Use phone or email</button>`, StepChoose, false},
		{"use phone or email beside the box", email + `<button>Use phone or email</button>`, StepEmail, false},
		{"your age", `<p>Confirm your age</p><button>Next</button>`, StepOther, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := loginPageOf(t, tc.body)
			if p.Step != tc.step || p.Unusual != tc.unusual {
				t.Fatalf("step=%s unusual=%v, want step=%s unusual=%v (text %q)", p.Step, p.Unusual, tc.step, tc.unusual, p.Text)
			}
		})
	}
}
