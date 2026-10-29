# muse-mcp

An MCP server and command-line tool that lets Claude Code (or you) use **your
own muse.ai account** to generate images (including the same character in the
same scene with one thing changed), make voice clips, and chat with your muse
agent. Video and voice are experimental.

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
muse-mcp install   # adds it to Claude Code (runs `claude mcp add -s user muse ...`) and its skills
muse-mcp login     # log in to muse.ai once, in the Chrome window that opens
```

Restart Claude Code and the muse tools are there. `install` also copies the
`muse` skill (`SKILL.md`, `image.md`, `video.md`, `speech.md`), which teaches
Claude how to write image and video prompts and speech scripts for muse, into `~/.claude/skills` (or
`./.claude/skills` with `--scope project` or `local`). It replaces the older
`muse-voice` skill. `muse-mcp uninstall` removes both again (your login and saved
references stay in `~/.muse-mcp`). Run `install` again after upgrading to get
the newest skill.

From a clone, use `go build -o muse-mcp ./cmd/muse-mcp && ./muse-mcp install`.

If you skip `muse-mcp login`, the first muse tool call opens the Chrome window
instead and replies "Log in to muse.ai in the window that just opened, then try
again." The window hides itself once muse.ai shows your chats.

## Tools

| Tool | What it does |
|---|---|
| `muse_generate_image` | Makes an image and saves it to `output_path` (`.png`, `.jpg`/`.jpeg` or `.webp`). Takes about a minute. |
| `muse_generate_video` | **Experimental, under testing.** Makes a short video and saves it to `output_path` (`.mp4` or `.webm`). See [Video](#video-experimental). |
| `muse_generate_speech` | **Experimental, under testing.** Makes a text-to-speech clip and saves it in `output_dir` as `<chat id>-<chat title>.mp3`. See [Speech](#speech-experimental). |
| `muse_list_voices` | Searches muse's voice catalog by words, gender, accent or tone. |
| `muse_chat` | Sends a message to your muse agent. Pass `chat_id` to continue a conversation; `save_images_to` saves any images, videos or audio clips it sends. |
| `muse_decide_approval` | Approves or denies something muse asked permission to do. |
| `muse_list_chats` / `muse_delete_chat` | The chats muse-mcp started, and deleting one of them in muse (cannot be undone). |
| `muse_save_reference` / `muse_list_references` / `muse_delete_reference` | Saved characters and scenes. |
| `muse_status` | Whether Chrome is running, whether you're logged in, and pending approvals. |

### Keeping a character or scene consistent

Save it once:

> Save fox.png as a character called "fox": a red fox with a white chest and black legs.

Then refer to it by name and say what changes:

> Make an image of fox in the snowy-forest scene; change: the fox is asleep under the pine. Save it to assets/fox-asleep.png.

muse-mcp attaches the saved images every time, labels them in the prompt, and
asks muse to change only what you said.

### Images

muse's image tool takes only a prompt and images: no size, style, seed or
negative-prompt setting. The ratio (`aspect_ratio`), style and anything to
leave out are words in the prompt, and the ratio is a request, not a fixed
canvas. muse-mcp adds a few inputs that it turns into prompt wording:

- `edit_image` with `change` and `keep`: edit a real photo, for example a
  product you sell. "Keep" is a request: muse regenerates the photo, so
  compare the result with the real item before you post it.
- `reference_roles`: one role per reference image ("the product: shape and
  colour exact", "lighting mood only").
- `count`: 1 to 4 separate versions in one request (muse's limit).

Text inside an image is unreliable, especially Vietnamese tone marks. For a
shop name, price or date, make the image without text and add the text in a
design tool such as Canva. The `muse` skill's `image.md` has the rest.

## Command line

The same binary works without Claude Code:

```sh
muse-mcp image -o fox.png "a red fox in a snowy forest"
muse-mcp image -o fox-asleep.png --character fox --scene snowy-forest --change "the fox is asleep"
muse-mcp image -o yarn.png --edit yarn.jpg --change "the background: plain cream studio" \
  --keep "the yarn's exact colour, twist and number of balls"
muse-mcp image -o ideas.png --count 3 --ref logo.png --role "the shop logo, colours only" "a cosy yarn shop window"
muse-mcp video -o steam.mp4 --ref teapot.png "steam rises from the teapot"   # experimental
muse-mcp speech -o clips --voice Helio "Hello from muse."                # experimental
muse-mcp voices --gender female --accent british warm
muse-mcp chat "what can you do?"
muse-mcp chat --chat <chat_id> --save out/ "make it red"
muse-mcp chats                       # the chats muse-mcp started
muse-mcp chats delete <chat_id>      # delete one in muse
muse-mcp chats forget <chat_id>      # drop one from the list (muse untouched)
muse-mcp refs save fox --kind character --desc "a red fox with a white chest" fox.png
muse-mcp refs list
muse-mcp status
```

`muse-mcp help` lists every command and flag; `--json` prints the raw result.
When muse asks to act on your accounts, the CLI asks you `Approve? [y/N]` in the
terminal. Without a terminal (in a script) it never approves: the request stays
unanswered in muse and the CLI exits with code 3.

Only one muse-mcp can use your muse login at a time. A Claude Code session
takes it on its first muse call and keeps it until the session ends; until
then, other sessions and CLI commands that talk to muse say so. Use muse in
that session, or quit it first. `refs` and `chats` (the list) always work.

## Video (experimental)

`muse_generate_video` and `muse-mcp video` are under testing. muse makes one
clip of about 10 seconds per request. Its video tool has no length, ratio,
resolution, audio, negative-prompt, seed or style setting (ratio and style
are prompt words), and images guide the clip without being an exact first
frame. Give each reference image a role (`reference_roles`, `--role`) and
say what must not change (`keep`, `--keep`). Add captions, prices and music
in an editor afterwards. The `muse` skill's `video.md` has the prompt rules.

muse blocks AI video for some accounts and regions. When it does, it may make a substitute
itself (for example an animated still) and say so in the reply, or answer with
text only, in which case the call fails with muse's reply. Videos are saved
as muse sends them; if that is a `.webm` and you asked for `.mp4`, the file
keeps the `.webm` extension.

## Speech (experimental)

`muse_generate_speech` and `muse-mcp speech` make a text-to-speech clip of the
exact text you give and save it as `.mp3` in a folder (`output_dir`, `-o`;
the current folder by default). The file is named after muse's chat:
`<chat id>-<chat title>.mp3`, for example
`123e4567-e89b-42d3-a456-426614174000-Generate-Vietnamese-text-to-speech.mp3`,
or `<chat id>-speech.mp3` if muse hasn't named the chat. Pass a voice name (for example
`Helio` or `Aria`) or leave it out and muse picks one.

muse's voice tool takes only a voice, a language and a speed (1 to 200, 100
is normal). It has no emotion, style, dialect or pitch setting and no tags
like `[excited]`: the same voice, script and speed give the same clip, whatever
mood you describe. So a feeling goes into the speed and the way the script is
written:

| Feeling | Speed | Script |
|---|---|---|
| calm | 85 | soft words, commas and full stops for pauses |
| friendly | 100 | warm, welcoming words |
| excited | 120 | short sentences, exclamation marks |
| sad | 80 | slow words, short sentences for long pauses |
| confident | 100 | short strong sentences, no hesitation words |

```sh
muse-mcp speech -o clips --voice "Sparkling Whistle" --language "Vietnamese (vi-VN)" --speed 120 \
  "Quán trà sữa Mây khai trương rồi! Mua một tặng một! Ghé quán ngay nhé!"
```

muse-mcp always sends a speed (100 if you give none); without one, muse
picks its own from the wording.

For speech that should sound like a real person talking, the script needs a
few humanizer words: one every two or three sentences ("well", "haha",
"ừm", "à", "ủa", "na ja"), a "..." for a pause, contractions in English.
Leave them out of formal announcements. The `muse` skill's `speech.md` has the
lists for English, Vietnamese and German.

Write the script as it should sound: numbers, prices and dates in words, no
links, emoji, markdown, tags, phrases in capitals, stage directions or speaker
labels, and Vietnamese with full tone marks. muse-mcp checks for these before
asking muse and refuses a script that has them (no quota used); `ignore_script_warnings` (`--force` on
the command line) sends it anyway.

If you publish a clip, say that the voice is AI-generated.

muse has a catalog of about 1,200 named voices (35 accents). muse-mcp comes
with it built in, as muse listed it on 2026-10-29
([internal/voices/catalog.csv](internal/voices/catalog.csv)), so you can
search it right away:

```sh
muse-mcp voices --accent vietnamese
muse-mcp voices --gender female warm
muse-mcp voices --all        # every voice
muse-mcp voices accents      # how many voices each accent has
```

If muse adds voices, ask it for "your voice catalog as a CSV file with name,
gender, accent, tone and description" and import the file; it replaces the
built-in list (delete `~/.muse-mcp/voices.csv` to go back):

```sh
muse-mcp voices import ~/Downloads/voice-list.csv
```

An accent is how a voice sounds, not a language it speaks well: muse warned
that a Vietnamese-accent voice "may sound a bit imperfect" reading Vietnamese.
Try the hardest 15 to 30 seconds of a long script first.

Then Claude can search it with `muse_list_voices` and pick a voice by name.

muse's page has no download button for clips, so muse-mcp presses the clip's
Play button in its hidden, muted Chrome and saves the audio muse loads.

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
- Every request adds a chat to your muse chat list. muse-mcp never deletes
  one by itself: you (`muse-mcp chats delete`) or Claude (`muse_delete_chat`)
  decide. Deleting can't be undone, and the chat's images and clips go with
  it in muse; files muse-mcp saved stay.
- muse-mcp only continues or deletes chats it started itself. It never touches yours.
- One request runs at a time.
- Relative `output_path`s resolve against the directory Claude Code started
  the server in.

## Files

Everything lives in `~/.muse-mcp/` (override with `MUSE_MCP_HOME`):
`chrome-profile/` (your muse login), `library/` (saved references),
`voices.csv` (a voice catalog you imported, if any), `chats.json` (the chats muse-mcp
started), `muse-mcp.log`.

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

## Contributing

Issues and pull requests are welcome. Run `go test ./...` before sending a
change; it needs Chrome but not a muse.ai account. If muse.ai changed and
something broke, say what the page shows now: the fix usually belongs in
`internal/musepage/page.go`.

## Security

muse-mcp works with your logged-in muse.ai session. Report security problems
privately, as [SECURITY.md](SECURITY.md) describes, not in a public issue.

## License

[MIT](LICENSE)
