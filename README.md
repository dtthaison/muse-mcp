# muse-mcp

An MCP server that lets Claude Code use **your own muse.ai account** to generate
images (including the same character in the same scene with one thing changed)
and to chat with your muse agent.

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
claude mcp add -s user muse -- "$(go env GOPATH)/bin/muse-mcp"
```

Or from a clone:

```sh
go build -o muse-mcp ./cmd/muse-mcp
claude mcp add -s user muse -- "$PWD/muse-mcp"
```

## First login

The first time a muse tool runs, a Chrome window opens on muse.ai and the tool
replies "Log in to muse.ai in the window that just opened, then try again."
Log in there, and the window hides itself once muse.ai shows your chats. Ask
Claude again.

## Tools

| Tool | What it does |
|---|---|
| `muse_generate_image` | Makes an image and saves it to `output_path` (`.png`, `.jpg`/`.jpeg` or `.webp`). Takes about a minute. |
| `muse_chat` | Sends a message to your muse agent. Pass `chat_id` to continue a conversation. |
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
- Video isn't supported. Where muse blocks AI video for an account, it may
  render a substitute itself; muse-mcp saves only images.
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
