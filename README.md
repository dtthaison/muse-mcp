# muse-mcp

An MCP server and command-line tool that lets Claude Code (or you) use **your
own muse.ai account** to generate images (including the same character in the
same scene with one thing changed) and to chat with your muse agent. Video is
experimental.

muse.ai has no public API, so muse-mcp drives a real Chrome window with a
dedicated profile that you log in to once. Your cookies stay in that profile:
they're never copied, exported or logged.

> muse-mcp is an unofficial, independent project. It is not affiliated with or
> endorsed by Meta or muse.ai. It automates your own account for your own use;
> you are responsible for following muse.ai's terms.

## Requirements

- Go 1.26 or later
- Google Chrome (in `/Applications` on macOS, or `google-chrome`/`chromium` on
  `PATH`), or set `MUSE_CHROME` to its binary
- A muse.ai account

Tested on macOS.

## Install

```sh
go install github.com/dtthaison/muse-mcp/cmd/muse-mcp@latest
muse-mcp install   # adds it to Claude Code (runs `claude mcp add -s user muse ...`)
muse-mcp login     # log in to muse.ai once, in the Chrome window that opens
```

Restart Claude Code and the muse tools are there. `muse-mcp uninstall` removes
it again (your login and saved references stay in `~/.muse-mcp`).

From a clone, use `go build -o muse-mcp ./cmd/muse-mcp && ./muse-mcp install`.

If you skip `muse-mcp login`, the first muse tool call opens the Chrome window
instead and replies "Log in to muse.ai in the window that just opened, then try
again." The window hides itself once muse.ai shows your chats.

## Tools

| Tool | What it does |
|---|---|
| `muse_generate_image` | Makes an image and saves it to `output_path` (`.png`, `.jpg`/`.jpeg` or `.webp`). Takes about a minute. |
| `muse_generate_video` | **Experimental, under testing.** Makes a short video and saves it to `output_path` (`.mp4` or `.webm`). See [Video](#video-experimental). |
| `muse_chat` | Sends a message to your muse agent. Pass `chat_id` to continue a conversation; `save_images_to` saves any images or videos it sends. |
| `muse_decide_approval` | Approves or denies something muse asked permission to do. |
| `muse_save_reference` / `muse_list_references` / `muse_delete_reference` | Saved characters and scenes. |
| `muse_status` | Whether Chrome is running, whether you're logged in, and pending approvals. |

### Keeping a character or scene consistent

Save it once:

> Save fox.png as a character called "fox": a red fox with a white chest and black legs.

Then refer to it by name and say what changes:

> Make an image of fox in the snowy-forest scene; change: the fox is asleep under the pine. Save it to assets/fox-asleep.png.

muse-mcp attaches the saved images every time, labels them in the prompt, and
asks muse to change only what you said.

## Command line

The same binary works without Claude Code:

```sh
muse-mcp image -o fox.png "a red fox in a snowy forest"
muse-mcp image -o fox-asleep.png --character fox --scene snowy-forest --change "the fox is asleep"
muse-mcp video -o steam.mp4 --ref teapot.png "steam rises from the teapot"   # experimental
muse-mcp chat "what can you do?"
muse-mcp chat --chat <chat_id> --save out/ "make it red"
muse-mcp refs save fox --kind character --desc "a red fox with a white chest" fox.png
muse-mcp refs list
muse-mcp status
```

`muse-mcp help` lists every command and flag; `--json` prints the raw result.
When muse asks to act on your accounts, the CLI asks you `Approve? [y/N]` in the
terminal. Without a terminal (in a script) it never approves: the request stays
unanswered in muse and the CLI exits with code 3.

Only one muse-mcp can use your muse login at a time. While a Claude Code session
has the server running, CLI commands that talk to muse say so; use the tools in
Claude Code, or quit that session first. `refs` commands always work.

## Video (experimental)

`muse_generate_video` and `muse-mcp video` are under testing. muse blocks AI
video for some accounts and regions. When it does, it may make a substitute
itself (for example an animated still) and say so in the reply, or answer with
text only, in which case the call fails with muse's reply. Videos are saved
as muse sends them; if that is a `.webm` and you asked for `.mp4`, the file
keeps the `.webm` extension.

## Approvals: you decide every one

muse can act on your real accounts (for example posting to Facebook). When it
asks permission, the tool returns `needs_approval` with what muse wants to do.
Claude then has to call `muse_decide_approval`, and Claude Code asks you first.

**Never add `muse_decide_approval` to an always-allow list.** Claude Code's
permission prompt for it is how you approve each muse action.

An approval nobody answers within 10 minutes is denied. If muse-mcp or Chrome
restarts, pending approvals are dropped and left unanswered in muse (never
approved). Open the chat in muse.ai to decide them there.

## Good to know

- **muse remembers.** muse's agent keeps memory and a workspace across all
  chats, including your own. Each image request starts a new muse chat, but
  that chat is not a blank slate.
- Every request adds a chat to your muse chat list.
- muse-mcp only continues chats it started itself. It never touches yours.
- One request runs at a time.
- Relative `output_path`s resolve against the directory Claude Code started
  the server in.

## Files

Everything lives in `~/.muse-mcp/` (override with `MUSE_MCP_HOME`):
`chrome-profile/` (your muse login), `library/` (saved references),
`chats.json`, `muse-mcp.log`.

## Development

```sh
go test ./...                                   # offline, headless Chrome + a fake muse page
MUSE_TEST_VISIBLE=1 go test ./...               # also the tests that open a visible window
MUSE_LIVE=1 go test ./internal/muse/ -run Live -v -timeout 30m  # real muse.ai: uses quota, adds 2 chats
```

The live test waits up to 10 minutes for you to log in if the profile isn't
logged in yet.

Everything muse-mcp knows about the muse.ai page is in
`internal/musepage/page.go`. When muse.ai changes, start there.
[docs/design.md](docs/design.md) explains the design and what was learned
from the live site.

## License

[MIT](LICENSE)
