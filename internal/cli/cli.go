// Package cli is muse-mcp's command line: with no arguments it runs the MCP
// server; with a command it installs itself, logs in, or uses muse directly.
package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/dtthaison/muse-mcp/internal/library"
	"github.com/dtthaison/muse-mcp/internal/muse"
	"github.com/dtthaison/muse-mcp/internal/output"
	"github.com/dtthaison/muse-mcp/internal/tools"
	"github.com/dtthaison/muse-mcp/internal/voices"
)

// Service is what the commands need from muse.Service.
type Service interface {
	tools.Service
	Login(ctx context.Context) error
}

// Env is everything Run touches outside itself.
type Env struct {
	Stdin       io.Reader
	Stdout      io.Writer
	Stderr      io.Writer
	Interactive bool // stdin is a terminal, so approvals can be asked
	Library     *library.Store
	Voices      *voices.Catalog
	Muse        func() (Service, func(), error)                 // starts Chrome; the func shuts it down
	Serve       func(ctx context.Context) error                 // the MCP server on stdio
	Claude      func(ctx context.Context, args ...string) error // runs the claude CLI
	Exe         string                                          // this binary, for install
	Version     string
}

// Exit codes.
const (
	exitOK        = 0
	exitErr       = 1
	exitUsage     = 2
	exitUndecided = 3 // muse asked for an approval nobody could answer
)

const usage = `muse-mcp: generate images and chat with your muse.ai account.

Usage:
  muse-mcp                       run the MCP server (what Claude Code starts)
  muse-mcp install [--scope S]   add muse-mcp to Claude Code (scope user, project or local; default user)
  muse-mcp uninstall [--scope S] remove it from Claude Code
  muse-mcp login                 open muse.ai and wait until you are logged in
  muse-mcp status                Chrome, login and pending approvals

  muse-mcp image -o OUT.png [flags] PROMPT
  muse-mcp video -o OUT.mp4 [flags] PROMPT     (experimental, under testing)
      --change TEXT        change only this, keep everything else
      --character NAME     saved character to attach (repeatable)
      --scene NAME         saved scene to attach
      --ref FILE           extra reference image (repeatable)
      --ref-notes TEXT     what the extra references are for
      --aspect RATIO       e.g. 16:9

  muse-mcp voice -o OUT.mp3 [--voice NAME] [--style TEXT] TEXT   (experimental)
  muse-mcp voices [--gender G] [--accent A] [--tone T] [--limit N | --all] [WORDS]
  muse-mcp voices accents          how many voices each accent has
  muse-mcp voices import FILE.csv   muse's voice catalog (ask muse for it as CSV)

  muse-mcp chat [--chat ID] [--attach FILE]... [--save DIR] MESSAGE

  muse-mcp refs list
  muse-mcp refs save NAME --kind character|scene --desc TEXT [--replace] IMAGE...
  muse-mcp refs delete NAME

  muse-mcp version

image, video, voice and chat take --json to print the raw result. When muse asks to act
on your accounts, you are asked in the terminal; without a terminal nothing is
approved.
`

// Run runs one command and returns the process exit code.
func Run(ctx context.Context, args []string, env Env) int {
	r := &runner{env: env, in: bufio.NewReader(orEmpty(env.Stdin))}
	if len(args) == 0 {
		if env.Interactive {
			fmt.Fprint(env.Stderr, usage)
			return exitUsage
		}
		return r.fail(env.Serve(ctx))
	}
	switch cmd, rest := args[0], args[1:]; cmd {
	case "serve":
		return r.fail(env.Serve(ctx))
	case "install", "uninstall":
		return r.install(ctx, cmd, rest)
	case "login":
		return r.withMuse(func(svc Service) int {
			fmt.Fprintln(env.Stderr, "Opening muse.ai. If a Chrome window appears, log in there.")
			if err := svc.Login(ctx); err != nil {
				return r.fail(err)
			}
			fmt.Fprintln(env.Stdout, "Logged in to muse.ai.")
			return exitOK
		})
	case "status":
		return r.withMuse(func(svc Service) int { return r.status(ctx, svc) })
	case "image", "video":
		return r.generate(ctx, cmd, rest)
	case "voice":
		return r.voice(ctx, rest)
	case "voices":
		return r.voices(rest)
	case "chat":
		return r.chat(ctx, rest)
	case "refs":
		return r.refs(rest)
	case "version", "--version":
		fmt.Fprintln(env.Stdout, "muse-mcp", env.Version)
		return exitOK
	case "help", "-h", "--help":
		fmt.Fprint(env.Stdout, usage)
		return exitOK
	default:
		fmt.Fprintf(env.Stderr, "muse-mcp: unknown command %q\n\n%s", cmd, usage)
		return exitUsage
	}
}

type runner struct {
	env Env
	in  *bufio.Reader
}

func orEmpty(r io.Reader) io.Reader {
	if r == nil {
		return strings.NewReader("")
	}
	return r
}

func (r *runner) fail(err error) int {
	if err == nil {
		return exitOK
	}
	fmt.Fprintln(r.env.Stderr, "muse-mcp:", err)
	return exitErr
}

func (r *runner) usageErr(format string, a ...any) int {
	fmt.Fprintf(r.env.Stderr, "muse-mcp: "+format+"\n", a...)
	return exitUsage
}

// withMuse starts muse (Chrome) for one command and always shuts it down.
func (r *runner) withMuse(f func(Service) int) int {
	svc, closeMuse, err := r.env.Muse()
	if err != nil {
		return r.fail(err)
	}
	defer closeMuse()
	return f(svc)
}

// newFlags returns a flag set that reports errors to stderr instead of exiting.
func (r *runner) newFlags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(r.env.Stderr)
	fs.Usage = func() { fmt.Fprintf(r.env.Stderr, "see: muse-mcp help\n") }
	return fs
}

// parse parses flags anywhere among the arguments and returns the others.
func parse(fs *flag.FlagSet, args []string) ([]string, error) {
	var rest []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return rest, nil
		}
		rest = append(rest, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

// list is a repeatable string flag.
type list []string

func (l *list) String() string     { return strings.Join(*l, ",") }
func (l *list) Set(v string) error { *l = append(*l, v); return nil }

func (r *runner) install(ctx context.Context, cmd string, args []string) int {
	fs := r.newFlags(cmd)
	scope := fs.String("scope", "user", "user, project or local")
	if _, err := parse(fs, args); err != nil {
		return exitUsage
	}
	claudeArgs := []string{"mcp", "remove", "-s", *scope, "muse"}
	if cmd == "install" {
		claudeArgs = []string{"mcp", "add", "-s", *scope, "muse", "--", r.env.Exe}
	}
	if err := r.env.Claude(ctx, claudeArgs...); err != nil {
		return r.fail(err)
	}
	if cmd == "install" {
		fmt.Fprintln(r.env.Stdout, "Added muse to Claude Code. Restart Claude Code, then run `muse-mcp login` once if you haven't.")
	} else {
		fmt.Fprintln(r.env.Stdout, "Removed muse from Claude Code. Your login and saved references in ~/.muse-mcp are kept.")
	}
	return exitOK
}

func (r *runner) status(ctx context.Context, svc Service) int {
	st, err := svc.Status(ctx)
	if err != nil {
		return r.fail(err)
	}
	yes := func(b bool) string {
		if b {
			return "yes"
		}
		return "no"
	}
	login := yes(st.LoggedIn)
	if !st.ChromeRunning && !st.LoggedIn {
		login = "unknown (muse.ai isn't open yet; `muse-mcp login` checks)"
	}
	out := r.env.Stdout
	fmt.Fprintf(out, "chrome running: %s\nlogged in: %s\ntracked chats: %d\n", yes(st.ChromeRunning), login, st.TrackedChats)
	for _, p := range st.Pending {
		fmt.Fprintf(out, "pending approval %s in chat %s: %s\n", p.ID, p.ChatID, p.Text)
	}
	return exitOK
}

func (r *runner) generate(ctx context.Context, cmd string, args []string) int {
	fs := r.newFlags(cmd)
	var a tools.ImageArgs
	var chars, refs list
	fs.StringVar(&a.OutputPath, "o", "", "output file")
	fs.StringVar(&a.Change, "change", "", "")
	fs.Var(&chars, "character", "")
	fs.StringVar(&a.Scene, "scene", "", "")
	fs.Var(&refs, "ref", "")
	fs.StringVar(&a.ReferenceNotes, "ref-notes", "", "")
	fs.StringVar(&a.AspectRatio, "aspect", "", "")
	asJSON := fs.Bool("json", false, "")
	rest, err := parse(fs, args)
	if err != nil {
		return exitUsage
	}
	if a.OutputPath == "" {
		return r.usageErr("%s needs -o OUTPUT", cmd)
	}
	a.Prompt, a.Characters, a.ReferenceImages = strings.Join(rest, " "), chars, refs
	media := muse.MediaImage
	if cmd == "video" {
		media = muse.MediaVideo
	}
	req, err := tools.BuildImageRequest(r.env.Library, a, media)
	if err != nil {
		return r.fail(err)
	}
	if media == muse.MediaVideo {
		fmt.Fprintln(r.env.Stderr, "Video is experimental: muse may block AI video for your account, or make a substitute.")
	}
	fmt.Fprintf(r.env.Stderr, "Asking muse for the %s (about a minute or more)...\n", cmd)
	return r.withMuse(func(svc Service) int {
		res, err := svc.GenerateImage(ctx, req)
		return r.finish(ctx, svc, res, err, *asJSON)
	})
}

func (r *runner) voice(ctx context.Context, args []string) int {
	fs := r.newFlags("voice")
	var a tools.VoiceArgs
	fs.StringVar(&a.OutputPath, "o", "", "output file")
	fs.StringVar(&a.Voice, "voice", "", "voice name")
	fs.StringVar(&a.Style, "style", "", "how to say it")
	asJSON := fs.Bool("json", false, "")
	rest, err := parse(fs, args)
	if err != nil {
		return exitUsage
	}
	if a.OutputPath == "" {
		return r.usageErr("voice needs -o OUTPUT")
	}
	a.Text = strings.Join(rest, " ")
	req, err := tools.BuildVoiceRequest(a)
	if err != nil {
		return r.fail(err)
	}
	fmt.Fprintln(r.env.Stderr, "Asking muse for the voice clip (experimental, about a minute)...")
	return r.withMuse(func(svc Service) int {
		res, err := svc.GenerateImage(ctx, req)
		return r.finish(ctx, svc, res, err, *asJSON)
	})
}

func (r *runner) voices(args []string) int {
	if len(args) > 0 && args[0] == "import" {
		if len(args) != 2 {
			return r.usageErr("voices import needs FILE")
		}
		n, err := r.env.Voices.Import(args[1])
		if err != nil {
			return r.fail(err)
		}
		fmt.Fprintf(r.env.Stdout, "Imported %d voices.\n", n)
		return exitOK
	}
	if len(args) > 0 && args[0] == "accents" {
		as, err := r.env.Voices.Accents()
		if err != nil {
			return r.fail(err)
		}
		tw := tabwriter.NewWriter(r.env.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "ACCENT\tVOICES\tFEMALE\tMALE\tNEUTRAL")
		for _, a := range as {
			fmt.Fprintf(tw, "%s\t%d\t%d\t%d\t%d\n", a.Accent, a.Total, a.Female, a.Male, a.Neutral)
		}
		tw.Flush()
		return exitOK
	}
	fs := r.newFlags("voices")
	var q voices.Query
	fs.StringVar(&q.Gender, "gender", "", "female, male or neutral")
	fs.StringVar(&q.Accent, "accent", "", "")
	fs.StringVar(&q.Tone, "tone", "", "")
	fs.IntVar(&q.Limit, "limit", 20, "")
	fs.BoolVar(&q.All, "all", false, "every match, no limit")
	rest, err := parse(fs, args)
	if err != nil {
		return exitUsage
	}
	q.Text = strings.Join(rest, " ")
	vs, total, err := r.env.Voices.Search(q)
	if err != nil {
		return r.fail(err)
	}
	for _, v := range vs {
		fmt.Fprintf(r.env.Stdout, "%s (%s, %s, %s): %s\n", v.Name, v.Gender, v.Accent, v.Tone, v.Description)
	}
	if total > len(vs) {
		fmt.Fprintf(r.env.Stdout, "... %d more; narrow the search or raise --limit\n", total-len(vs))
	}
	if total == 0 {
		fmt.Fprintln(r.env.Stdout, "No matching voices.")
	}
	return exitOK
}

func (r *runner) chat(ctx context.Context, args []string) int {
	fs := r.newFlags("chat")
	var attach list
	chatID := fs.String("chat", "", "chat_id to continue")
	fs.Var(&attach, "attach", "")
	save := fs.String("save", "", "directory for images or videos muse sends")
	asJSON := fs.Bool("json", false, "")
	rest, err := parse(fs, args)
	if err != nil {
		return exitUsage
	}
	if len(rest) == 0 {
		return r.usageErr("chat needs a MESSAGE")
	}
	files, err := tools.CheckFiles(attach)
	if err != nil {
		return r.fail(err)
	}
	req := muse.ChatRequest{Message: strings.Join(rest, " "), ChatID: *chatID, Attachments: files, SaveImagesTo: *save}
	return r.withMuse(func(svc Service) int {
		res, err := svc.Chat(ctx, req)
		return r.finish(ctx, svc, res, err, *asJSON)
	})
}

// finish asks the owner about any approvals, then prints the result.
func (r *runner) finish(ctx context.Context, svc Service, res muse.Result, err error, asJSON bool) int {
	for err == nil && res.Status == "needs_approval" && res.Approval != nil {
		a := res.Approval
		fmt.Fprintf(r.env.Stderr, "muse asks to: %s\n", a.Text)
		if !r.env.Interactive {
			fmt.Fprintf(r.env.Stderr, "Not deciding without a terminal; it stays unanswered. Decide it in muse.ai: %s\n", res.ChatURL)
			r.print(res, asJSON)
			return exitUndecided
		}
		fmt.Fprint(r.env.Stderr, "Approve? [y/N] ")
		line, _ := r.in.ReadString('\n')
		answer := strings.ToLower(strings.TrimSpace(line))
		res, err = svc.Decide(ctx, a.ChatID, a.ID, answer == "y" || answer == "yes")
	}
	if err != nil {
		var refused muse.RefusedError
		if errors.As(err, &refused) {
			fmt.Fprintln(r.env.Stderr, "muse-mcp:", err)
			return exitErr
		}
		return r.fail(err)
	}
	r.print(res, asJSON)
	if res.Status == "timeout" {
		fmt.Fprintf(r.env.Stderr, "muse-mcp: timed out waiting for muse; see %s\n", res.ChatURL)
		return exitErr
	}
	return exitOK
}

func (r *runner) print(res muse.Result, asJSON bool) {
	out := r.env.Stdout
	if asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		enc.Encode(res)
		return
	}
	for _, s := range append(append([]output.Saved{}, res.Images...), res.Videos...) {
		fmt.Fprintf(out, "saved %s (%dx%d, %s)\n", s.Path, s.Width, s.Height, size(s.Bytes))
	}
	for _, s := range res.Audio {
		fmt.Fprintf(out, "saved %s (%s)\n", s.Path, size(s.Bytes))
	}
	if res.Reply != "" {
		fmt.Fprintf(out, "muse: %s\n", res.Reply)
	}
	if res.ChatID != "" {
		fmt.Fprintf(out, "chat: %s (continue with --chat %s)\n", res.ChatURL, res.ChatID)
	}
}

func size(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%d KB", n>>10)
	}
	return fmt.Sprintf("%d bytes", n)
}

func (r *runner) refs(args []string) int {
	if len(args) == 0 {
		return r.usageErr("refs needs list, save or delete")
	}
	lib, out := r.env.Library, r.env.Stdout
	switch args[0] {
	case "list":
		refs, err := lib.List()
		if err != nil {
			return r.fail(err)
		}
		if len(refs) == 0 {
			fmt.Fprintln(out, "No saved characters or scenes.")
		}
		for _, ref := range refs {
			fmt.Fprintf(out, "%s (%s): %s [%d image(s)]\n", ref.Name, ref.Kind, ref.Description, len(ref.Images))
		}
		return exitOK
	case "save":
		fs := r.newFlags("refs save")
		kind := fs.String("kind", "", "character or scene")
		desc := fs.String("desc", "", "fixed description")
		replace := fs.Bool("replace", false, "overwrite an existing reference")
		rest, err := parse(fs, args[1:])
		if err != nil {
			return exitUsage
		}
		if len(rest) < 2 {
			return r.usageErr("refs save needs NAME and at least one IMAGE")
		}
		ref, err := lib.Save(rest[0], library.Kind(*kind), *desc, rest[1:], *replace)
		if err != nil {
			return r.fail(err)
		}
		fmt.Fprintf(out, "Saved %s %q with %d image(s).\n", ref.Kind, ref.Name, len(ref.Images))
		return exitOK
	case "delete":
		if len(args) != 2 {
			return r.usageErr("refs delete needs NAME")
		}
		if err := lib.Delete(args[1]); err != nil {
			return r.fail(err)
		}
		fmt.Fprintf(out, "Deleted %q.\n", args[1])
		return exitOK
	default:
		return r.usageErr("unknown refs command %q", args[0])
	}
}
