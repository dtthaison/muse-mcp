package muse

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/dtthaison/muse-mcp/internal/musepage"
)

// LoginOptions lets Login type the owner's email and code into muse.ai's login
// page. The zero value (no Ask) only opens muse.ai and waits for the owner to
// log in by hand.
type LoginOptions struct {
	Email string                              // asked for with Ask when empty
	Ask   func(prompt string) (string, error) // reads one line from the owner's terminal
	Say   func(msg string)                    // tells the owner something; may be nil
}

const (
	maxCodeTries = 5
	drivePoll    = 400 * time.Millisecond
	// A wrong code that is still on screen from the last try counts as a new
	// answer only after this long without the page clearing it.
	staleWrongGrace = 2 * time.Second
	codeVerifyLimit = 20 * time.Second
)

// loginTab is the tab watchLogin is polling, or nil.
func (s *Service) loginTab() context.Context {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loginCtx
}

func (s *Service) isLoggedIn() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loggedIn
}

// driveLogin types the owner's answers into the login page in tab. It returns
// nil both when the login is done and when it gave up: either way Login goes on
// to wait for the owner and the login detection. Only a cancelled ctx is an
// error. The email and the code are never logged.
func (s *Service) driveLogin(ctx, tab context.Context, o LoginOptions) error {
	say := func(m string) {
		if o.Say != nil {
			o.Say(m)
		}
	}
	op, release := bind(ctx, tab)
	defer release()
	stall := s.o.Timing.LoginStall
	if stall <= 0 {
		stall = 60 * time.Second
	}
	giveUp := func(why string, p musepage.LoginPage) {
		s.o.Log.Info("terminal login stopped; the owner finishes in the window", "why", why)
		msg := fmt.Sprintf("muse-mcp can't continue the login by itself (%s).", why)
		if lines := p.Lines(8); len(lines) > 0 {
			msg += " The page shows:\n  " + strings.Join(lines, "\n  ")
		}
		say(msg + "\nFinish logging in in the browser window; muse-mcp waits for it.")
	}

	email, emailSent, tries := strings.TrimSpace(o.Email), false, 0
	last := time.Now() // the last sign of progress
	var page musepage.LoginPage
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if op.Err() != nil || s.isLoggedIn() {
			return nil // the login was detected, or the window closed; Login reports which
		}
		if time.Since(last) > stall {
			giveUp(fmt.Sprintf("no progress for %s", stall.Round(time.Second)), page)
			return nil
		}
		p, err := musepage.LoginSnapshot(op)
		if err != nil {
			if err := sleep(op, drivePoll); err != nil && ctx.Err() != nil {
				return ctx.Err()
			}
			continue
		}
		page = p
		switch p.Step {
		case musepage.StepLoggedIn:
			return nil
		case musepage.StepMeta:
			if ok, _ := musepage.ClickButton(op, musepage.LabelMeta, 2*time.Second); ok {
				s.o.Log.Info("terminal login: confirmed the Meta account screen")
				last = time.Now()
			}
			sleep(op, time.Second)
		case musepage.StepEmail:
			if emailSent {
				sleep(op, drivePoll)
				continue
			}
			if email == "" {
				a, err := o.Ask("Muse email: ")
				if email = strings.TrimSpace(a); err != nil || email == "" {
					giveUp("no email was given", p)
					return nil
				}
			}
			if err := musepage.TypeEmail(op, email); err != nil {
				giveUp("typing the email failed", p)
				return nil
			}
			if ok, _ := musepage.ClickButton(op, musepage.LabelContinue, 5*time.Second); !ok {
				giveUp(`no "Continue" button on the email screen`, p)
				return nil
			}
			s.o.Log.Info("terminal login: typed the email and pressed Continue")
			emailSent, last = true, time.Now()
			sleep(op, time.Second)
		case musepage.StepCode:
			if tries >= maxCodeTries {
				giveUp("muse did not accept the code", p)
				return nil
			}
			who := "your email"
			if email != "" {
				who = email
			}
			a, err := o.Ask(fmt.Sprintf("PIN code sent to %s (empty = send a new one): ", who))
			if err != nil {
				giveUp("no code was given", p)
				return nil
			}
			last = time.Now()
			code := strings.Join(strings.Fields(a), "")
			if code == "" {
				if ok, _ := musepage.ClickButton(op, musepage.LabelResend, 2*time.Second); ok {
					s.o.Log.Info("terminal login: asked for a new code")
					say("Asked muse to send a new code.")
				} else {
					say("There is no resend button on the page; ask for a new code in the browser window.")
				}
				sleep(op, time.Second)
				continue
			}
			if err := musepage.TypeCode(op, code); err != nil {
				giveUp("typing the code failed", p)
				return nil
			}
			musepage.ClickButton(op, musepage.LabelSubmitCode, time.Second) // some pages submit by themselves
			tries++
			s.o.Log.Info("terminal login: typed a code", "try", tries)
			if s.codeVerdict(op, p.WrongCode) {
				say("That code isn't right.")
			}
			last = time.Now()
		default:
			if p.Unusual {
				giveUp("the page asks for something other than an email and a code", p)
				return nil
			}
			sleep(op, drivePoll)
		}
	}
}

// codeVerdict waits for muse's answer to a code and reports whether it was
// refused. stale says the page already showed a wrong-code message before the
// code was typed.
func (s *Service) codeVerdict(op context.Context, stale bool) bool {
	start := time.Now()
	cleared := !stale
	for time.Since(start) < codeVerifyLimit {
		if sleep(op, drivePoll) != nil {
			return false
		}
		p, err := musepage.LoginSnapshot(op)
		switch {
		case err != nil:
		case p.Step != musepage.StepCode:
			return false // logged in, or moved on to another screen
		case !p.WrongCode:
			cleared = true
		case cleared || time.Since(start) > staleWrongGrace:
			return true
		}
	}
	return false
}
