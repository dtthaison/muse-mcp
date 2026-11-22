# muse-browser-shardx

A browser provider for muse-mcp. It gives muse-mcp the Chrome DevTools (CDP) URL of a
ShardX anti-detect browser profile, so muse-mcp can sign in and
run inside that profile (its own fingerprint, proxy and cookies) instead of a
throwaway Chrome.

ShardX can run on your own machine or on a server; in the second case the provider
reaches it through an SSH tunnel.

## Install

```sh
go install github.com/dtthaison/muse-mcp/cmd/muse-browser-shardx@latest
```

or download the release archive for your platform and put `muse-browser-shardx` on your
`PATH` (the same place as `muse-mcp`). It uses only the Go standard library, plus the
system `ssh` when you use a server.

## Setup

### ShardX on this machine

```sh
muse-browser-shardx setup
```

It asks for:

- the Launcher API URL (default `http://127.0.0.1:40325`),
- an SSH host (leave blank for local),
- the API token, or a command that prints it (see below).

Then it saves the config and tests `GET /profiles`; you should see
`OK: found N profile(s)`. Without prompts:

```sh
muse-browser-shardx setup --api-url http://127.0.0.1:40325 \
  --token-command 'security find-generic-password -s shardx -w'
```

### ShardX on a server, over SSH

```sh
muse-browser-shardx setup --ssh root@vps-sing-1 \
  --token-command 'security find-generic-password -s shardx -w'
```

`--ssh` takes a host that plain `ssh <host>` already reaches without a password prompt
(key or agent; the provider runs ssh with `BatchMode=yes`). Put extra options such as a
port or key in `~/.ssh/config`. Keep the `api_url` as the address the Launcher API has
on the server (`http://127.0.0.1:40325`).

For each run the provider opens `ssh -N -L <free port>:127.0.0.1:<api port>` to call the
API, then a second forward to the browser's DevTools port, and prints
`http://127.0.0.1:<local port>`. The API tunnel is closed once the URL is printed; the
DevTools tunnel stays until muse-mcp is done.

### Token storage

The token is read from, in order: `SHARDX_API_TOKEN`, the config `token`, then the output
of `token_command` (run with `sh -c`, output trimmed). The token is sent only as an
`Authorization: Bearer` header; it is never printed, logged, or put on a command line
(which is why `setup` has no `--token` flag).

Better than a token in the file:

- macOS Keychain: `security add-generic-password -s shardx -a $USER -w` once, then
  `token_command` = `security find-generic-password -s shardx -w`.
- A vault CLI, `pass show shardx`, or any command that prints the token.

Config file: `$XDG_CONFIG_HOME/muse-browser-shardx/config.json`
(default `~/.config/muse-browser-shardx/config.json`), mode 0600 in a 0700 directory:

```json
{
  "api_url": "http://127.0.0.1:40325",
  "token": "",
  "token_command": "security find-generic-password -s shardx -w",
  "ssh": "root@vps-sing-1",
  "headless": false
}
```

Environment overrides: `SHARDX_API_URL`, `SHARDX_API_TOKEN`, `SHARDX_TOKEN_COMMAND`,
`SHARDX_SSH`.

## Use with muse-mcp

```sh
muse-browser-shardx list                 # profile names, ids, running or stopped
muse-mcp login --account sg1 --browser shardx:muse-sg-1
```

`shardx:<profile>` runs `muse-browser-shardx -- <profile>` (the `--` keeps a profile named
like a command, `list` or `setup`, from being run as one; by hand you can leave it out). The profile can be given by id,
by exact name, or by name in any letter case; if several profiles match, the provider
stops with the list of matches (use the id then).

Provider contract (what muse-mcp relies on):

- `muse-browser-shardx --describe` prints one line and exits 0.
- `muse-browser-shardx [--] <profile>` prints the DevTools URL as the first line of stdout,
  then keeps running until its stdin closes or it gets SIGTERM/SIGINT; then it removes
  its SSH tunnels and exits 0.
- On any error it prints a message on stderr, prints nothing on stdout and exits non-zero.

If the profile is already running with a DevTools port it is reused as is; otherwise the
provider starts it (visible, or headless with `"headless": true`).

## Notes

- **It never stops or closes a ShardX profile.** Closing the provider only drops the
  tunnels; the browser keeps running until you stop it in ShardX.
- The DevTools port controls the whole browser (every tab, cookie and login). It must stay
  on localhost or behind SSH; never expose it, and never forward it to a public address.
- A profile started by the provider and one started in ShardX by hand look the same to
  muse-mcp. If DevTools was not enabled for an already running profile, close it in
  ShardX and run again.
- Use one real account per person and follow muse's terms of service. This tool is for
  signing in to your own account from a stable browser profile, not for creating bulk or
  throwaway accounts.
