# muse speech clips (muse_generate_speech)

muse's voice tool takes four things: a voice, a language, a speed (1 to 200,
100 is normal) and the script. It has no emotion, style, dialect, pitch or
volume setting, and no tags like `[excited]` or `[laughs]`. Everything else
comes from the voice you pick and how the script is written.

## The call

1. **`voice`**: the only way to change how the voice sounds (gender, depth,
   accent). Search `muse_list_voices` and pick one name. An accent is how a
   voice sounds, not proof it speaks that language well.
2. **`language`**: with locale: `Vietnamese (vi-VN)`, `English (en-US)`,
   `German (de-DE)`. One language per clip.
3. **`speed`**: from the feeling (table below).
4. **`text`**: the script, written for the feeling.

## Feeling = speed + how the script is written

| Feeling | speed | Script |
|---|---|---|
| calm | 85 | soft words; commas and full stops for pauses |
| friendly | 100 | warm, welcoming words |
| excited | 120 | short sentences, `!` |
| sad | 80 | slow words; `...` and full stops for long pauses |
| confident | 100 | short strong sentences, no fillers |

Punctuation is direction: `,` short pause, `.` pause, `...` hesitation or a
longer pause, `!` energy, `?` a question.

## Sounding human

When the owner wants natural, human or conversational speech (a friendly
social video, someone talking), add humanizer words: one every 2 or 3
sentences, counting any the script already has (a 6-sentence script has 2
or 3 in all), never one per sentence. None in a formal announcement,
tutorial or legal text.

| | English | Vietnamese | German |
|---|---|---|---|
| thinking | well, um, let me think | ừm, à, để xem nào | also, ähm, na ja |
| laughing | haha, hehe | haha, hihi | haha |
| sighing | oh..., sigh... | ôi..., haiz... | ach... |
| surprise | oh, wow, wait, really? | ủa, thật hả, trời ơi | oh, ach so, echt? |
| connectors | I mean, honestly, you know | nè, nha, á, luôn, mà | ja, mal, halt, eigentlich |

A self-correction also sounds human: "hai trăm nghìn... à không, một trăm
chín mươi chín nghìn". In English write contractions: it's, don't, we're.

## What you may change

- Always: respell what a voice would misread (below), split sentences and
  change punctuation for the feeling, and add humanizers when asked for
  natural speech. Show the owner every change.
- Ask before the call: any other change of words, and anything that
  contradicts the request, such as an informal "du"/"bạn" in a script meant
  to be formal ("Sie"/"quý khách"). Keep one register throughout.

## Respelling

- Numbers, prices, percentages, dates, times, versions → words, in the
  language's own order (German `31.10.` → `einunddreißigsten Oktober`).
- Links → the spoken form, or ask whether to drop them.
- Abbreviations → full words (`TP.HCM` → `Thành phố Hồ Chí Minh`).
- Words in capitals → normal case (a voice may spell them out).
- Emoji, markdown, tags, speaker labels, stage directions → removed.

## Per language

- **Vietnamese:** full tone marks. `199.000đ` → `một trăm chín mươi chín
  nghìn đồng` (Southern: `ngàn`). Match vocabulary to the dialect (Southern
  chén, muỗng, dạ; Northern bát, thìa, vâng). Loanwords (`sale`, `freeship`,
  `fanpage`, `voucher`) may be read as Vietnamese syllables: list them and
  ask whether to keep, respell (`phan pết`) or translate.
- **German:** ä ö ü ß, never ae oe ue ss. `de-DE`, not "GE" (Georgia).
  Decimals with a comma (`2,50 €` → `zwei Euro fünfzig`). Times: `21 Uhr` →
  `einundzwanzig Uhr`.
- **Any language:** an English brand name may be mispronounced. Say so, and
  offer a one-line test of the name first.

## Around the call

- Show the owner the exact spoken script, the voice and the speed.
- A script longer than about 30 seconds (75 words): make the hardest 1 or 2
  sentences first and let the owner listen before the rest.
- Say "asking muse" until the result comes back. Then report the saved path
  and anything muse said about quality.
- If the clip will be published (TikTok, Spotify, YouTube, ads), remind the
  owner to label it as an AI-generated voice.
