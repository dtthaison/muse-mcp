# muse videos (muse_generate_video)

muse makes one clip of about 10 seconds per request. Its video tool takes a
prompt and images, nothing else: no length, ratio, resolution, audio,
negative-prompt, seed or style setting. Ratio and style are prompt words;
sound can't be chosen or muted. Images guide the clip but aren't used as an
exact first frame, and there's no first-and-last-frame mode.

## Before the call

- muse-mcp's video tool hasn't been tested end to end yet: muse blocks AI
  video for its author's account. Treat the first video as a test, and tell
  the owner if the result or the saved file looks wrong.
- muse blocks AI video for some accounts and regions. Say so up front
  unless a video has worked for this owner before. If the reply says it was
  blocked, or muse sent a still or an animated still instead, tell the owner
  and offer: a still image (`muse_generate_image`), or a prompt to paste into
  Kling, Hailuo or CapCut, written with the same rules below.
- Each clip is a muse chat and quota. A video longer than about 10 seconds
  is several clips joined in an editor (CapCut or any other): plan the
  clips, show the owner the list, and ask before making more than one.

## The prompt: one shot, one action, one camera move

2 to 4 sentences, about 40 to 80 words (muse expands short prompts itself),
in this order:

1. shot and camera: shot size, angle, one move with its speed ("medium
   close-up, slow push-in"; "static camera" when nothing should move)
2. subject with visible traits ("a matte-black steel bottle, brushed
   finish"), not claims ("premium", "durable")
3. one action: verb, screen direction, speed, and what stays still ("a hand
   slowly twists the cap; the bottle stays centred")
4. setting, then one light: source, direction, quality ("soft window light
   from the left")
5. style in visible terms: lens, palette, texture ("realistic commercial
   footage, muted warm palette"), not "cinematic" alone
6. what must not change goes in `keep`; exclusions as what should be there
   ("one continuous shot", "an empty hallway", "no on-screen text")

If the prompt says "then", it is two clips. Ratio: `aspect_ratio` plus
framing words ("vertical 9:16 framing, subject in the middle third"). No
ratio given: TikTok, Reels and Shorts are 9:16, YouTube and websites 16:9;
ask if the platform isn't clear. Save clips as `.mp4`, a series in order
(`mochi-1.mp4`, `mochi-2.mp4`).

Camera words muse's models know: static, pan, tilt, dolly in/out, truck,
pedestal, zoom, crane, handheld, orbit/arc; close-up, medium, wide,
low-angle, high-angle, top-down, POV.

## Real products and saved characters

- Pass the real photo in `reference_images` with a role in
  `reference_roles` ("the product: shape, label and colours exact"), and
  `keep` ("the label, shape and colours"). The prompt then describes the
  motion only; don't describe the photo again.
- Saved `characters` / `scene` keep a series consistent. Repeat the same
  light and style sentence word for word in every clip.
- Safe motion: slow turntable ("rotates slowly clockwise"), slow push-in,
  one simple hand verb (open, pour, press, lift) with hands fully visible.
- Breaks easily: fast spins, morphs and before/after transitions, liquids,
  complex hand work, crowds. Make before and after as two clips with the
  same framing and cut between them in the editor.

## Text and sound

- Nothing a viewer must read goes in the clip: captions, prices, logos and
  any Vietnamese text are added in the editor afterwards. For 9:16, keep
  them between about 15% and 75% of the height, away from the right edge.
- muse can't control sound. Add music or a voiceover in the editor; a
  voiceover can come from `muse_generate_speech`.

## After the clip

Tell the owner to watch the whole clip and check the last frame, the label,
hands and faces before posting. To improve a clip, change one thing (camera,
light or action): send the same prompt, references and `keep` again with
that one thing changed, or use `change` with the same characters and scene.
