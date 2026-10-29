# Security

muse-mcp drives a Chrome profile that is logged in to your muse.ai account,
so a bug in it can expose that login or act on your accounts. Please report
anything like that privately.

## Reporting

Use GitHub's private vulnerability reporting: the **Security** tab of this
repository, then **Report a vulnerability**. Please don't open a public issue
for it. Include what you found, how to reproduce it and the muse-mcp version
(`muse-mcp version`).

## What muse-mcp promises

- Your muse.ai cookies stay in the dedicated profile (`~/.muse-mcp/chrome-profile`).
  muse-mcp never copies, exports or logs them, and never logs network request
  or response bodies.
- It never approves anything muse asks permission for on its own. Every
  approval is your decision (`muse_decide_approval`, or `Approve? [y/N]` in a
  terminal); without a terminal the CLI never approves.
- It only sends into, continues or deletes muse chats it started itself
  (`~/.muse-mcp/chats.json`). It never touches your own chats.
- muse-mcp controls Chrome over a pipe, not a network port: its Chrome opens
  no remote-debugging port, so no other program can attach to your logged-in
  session through one.

A way around any of these is a security bug.

## Out of scope

- What muse.ai itself does with your account or content.
- Anything a muse reply says: muse-mcp treats replies as data, and Claude
  Code's permission prompts decide what runs on your machine.
