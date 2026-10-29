---
name: muse-voice
description: Use when making a voice clip, voiceover, narration, podcast intro or text-to-speech ad with muse (muse_generate_voice, muse_list_voices, or "muse-mcp voice"), in any language, especially Vietnamese, or when a clip should sound calm, excited, sad or any other feeling.
---

# muse voice clips

muse's voice tool takes four things: a voice, a language, a speed (1 to 200,
100 is normal) and the script. It has no emotion, style, dialect, pitch or
volume setting, and no tags like `[excited]`. Everything else comes from the
voice you pick and how the script is written.

## The call

1. **`voice`**: the only way to change how the voice sounds (gender, depth,
   accent). Search `muse_list_voices` by gender, accent and tone and pick
   one name. An accent is how a voice sounds, not proof it speaks that
   language well.
2. **`language`**: with locale, e.g. `Vietnamese (vi-VN)`.
3. **`speed`**: from the feeling (table below).
4. **`text`**: the script, written for the feeling.

## Feeling = speed + how the script is written

| Feeling | speed | Script |
|---|---|---|
| calm | 85 | soft words; commas and full stops for pauses |
| friendly | 100 | warm, welcoming words |
| excited | 120 | short sentences, exclamation marks |
| sad | 80 | slow words; short sentences, full stops for long pauses |
| confident | 100 | short strong sentences, no hesitation words |

Splitting sentences and changing punctuation for the feeling is part of the
job; do it. Changing, adding or removing words is a writing change: offer it.

## Script hygiene

The owner's words, respelled only where a voice would misread them:
- numbers, prices, percentages, dates, versions → words;
- links → the spoken form, or ask whether to drop them;
- abbreviations → full words (`TP.HCM` → `Thành phố Hồ Chí Minh`);
- emoji, markdown, tags, speaker labels, stage directions → removed (their
  feeling goes into wording and speed).

## Vietnamese

- Full tone marks everywhere.
- Numbers as said: `199.000đ` → `một trăm chín mươi chín nghìn đồng`
  (Southern speakers say `ngàn`).
- There is no dialect setting; the voice decides the accent.
- English loanwords (`shop`, `sale`, `freeship`, `fanpage`, `voucher`) may be
  read as Vietnamese syllables. List the ones in the script and ask whether
  to keep them, respell them as said (`phan pết`), or use Vietnamese.

## Around the call

- Show the owner the exact spoken script, the speed, and every change.
- A script longer than about 30 seconds (75 words): make the hardest 1 or 2
  sentences first and let the owner listen before the rest.
- Say "asking muse" until the result comes back. Then report the saved path
  and anything muse said about quality ("may sound a bit imperfect").
- If the clip will be published (TikTok, Spotify, YouTube, ads), remind the
  owner to label it as an AI-generated voice.
