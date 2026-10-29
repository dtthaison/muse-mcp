---
name: muse-voice
description: Use when making a voice clip, voiceover, narration, podcast intro or text-to-speech ad with muse (muse_generate_voice, muse_list_voices, or "muse-mcp voice"), in any language, especially Vietnamese.
---

# muse voice clips

muse reads the script exactly as written and reads the direction as how to
say it. Good clips come from a coherent direction and a script that only
changes what a voice would misread.

## The call

1. **Voice.** Search `muse_list_voices` by gender, accent and tone, and pick
   one name. An accent is how a voice sounds, not proof it speaks that
   language well.
2. **Direction** (never spoken):
   - `language` with locale, e.g. `Vietnamese (vi-VN)`.
   - `dialect`: one, e.g. `Southern Vietnamese (Saigon)`.
   - `use_case`: audience, platform, length.
   - `style`: one string of 3 to 5 traits that fit together: persona, one
     emotion, voice quality, pace (`"friendly shop owner, warm, clear, brisk
     pace"`). When the owner asks for opposites ("calm, energetic"),
     choose one reading ("relaxed, warm, quietly upbeat") and tell them which.
   Write the direction in English.
3. **Script** (`text`): the owner's own words, respelled only where a voice
   would misread them:
   - numbers, prices, percentages, dates, versions → words;
   - links → the spoken form, or ask whether to drop them;
   - abbreviations → full words (`TP.HCM` → `Thành phố Hồ Chí Minh`);
   - emoji, markdown, speaker labels, stage directions → removed (the
     feeling goes into `style`).
   Keep their wording, slang and order otherwise. Rephrasing copy, adding
   words ("nha", "about our") or translating loanwords is a writing change:
   offer it, don't do it silently.

## Vietnamese

- Full tone marks everywhere.
- Numbers as said: `199.000đ` → `một trăm chín mươi chín nghìn đồng`
  (Southern speakers often say `ngàn`; match the dialect).
- English loanwords (`shop`, `sale`, `freeship`, `fanpage`, `voucher`) may be
  read as Vietnamese syllables. List the ones in the script and ask the owner
  whether to keep them, respell them as said (`phan pết`), or use Vietnamese.

## Around the call

- Show the owner the exact spoken script with every change marked before or
  with the call.
- A script longer than about 30 seconds (75 words): make the hardest 1 or 2
  sentences first and let the owner listen before the rest.
- Say "asking muse" until the result comes back. Then report the saved path
  and anything muse said about quality ("may sound a bit imperfect").
- If the clip will be published (TikTok, Spotify, YouTube, ads), remind the
  owner to label it as an AI-generated voice.
