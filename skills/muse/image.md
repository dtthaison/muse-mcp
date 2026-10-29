# muse images (muse_generate_image)

muse's image tool takes a prompt and images, nothing else: no size, style,
seed, quality or negative-prompt setting. The ratio is words in the prompt
(a request, not a fixed canvas). One request makes 1 to 4 separate images.

## The prompt: a short brief to a photographer

One paragraph in English, under about 60 words, most important first
(talk to the owner in their own language):

subject (one hero item: count, colour, material, details) → setting →
one style → lighting and direction → 2 or 3 colours → one mood →
camera angle and placement (and empty space if text goes on later) →
"No text, no watermark, no extra props."

- Sentences that say what relates to what, not keyword lists.
- One style only ("photorealistic product photo" or "watercolour
  illustration", never both). No praise words ("beautiful", "epic").
- Say what should be there ("a clean, empty wooden table") as well as what
  shouldn't.
- For a shop or brand, reuse the same colours and mood phrase in every
  prompt; only the subject changes.

## Ratio (`aspect_ratio`)

| Job | Ratio |
|---|---|
| product card, catalogue | 1:1 |
| Facebook / Instagram feed post | 4:5 |
| Story or Reel cover | 9:16, subject in the middle; top and bottom clear (the app covers them) |
| website banner, Facebook cover | 16:9, one quiet third for a headline |

## Real products: edit the real photo

A text-only prompt invents colour, stitches, labels and counts. When the
image shows something the owner sells, ask for a photo of it and pass it as
`edit_image` with:

- `change`: only what may change (background, light, crop).
- `keep`: the product truth: exact shape, colour, material, pattern, number
  of items, label wording. Restate it on every edit.

`change` and `keep` are enough; add `prompt` only for detail they don't
cover (light, mood, angle). No photo yet? Ask for one before generating. Tell the owner to compare the
result with the real item before posting: muse regenerates the photo, so
"keep" is a request, not a guarantee.

## Text in the image

Image models paint letters; they don't spell. If wording must be exact (shop
name, price, date, Vietnamese with tone marks), generate with
"no text in the image" and leave empty space, then tell the owner to add the
text in Canva. Otherwise: at most 3 to 5 words, in quotes, large, simple
font, plain area, "no other text".

## References and consistency

- Saved characters and scenes: `characters`, `scene`.
- Other images: `reference_images` with one `reference_roles` entry each
  ("the product: shape and colour exact", "lighting mood only"), rather
  than one `reference_notes` for all of them.
- A saved character in a new picture: `characters` plus a full prompt.
- One thing different in an image you already have: `change` (and `keep`),
  not a new prompt.

## Iterating

- When the owner wants options, ask first (each version is a generation),
  then use `count` 2 to 4 in one request. The files are `output_path`, then
  `-1`, `-2` and so on.
- Edit the best one instead of starting again, changing one thing at a time.
- Don't feed muse's output back in again and again: errors build up. After
  two rounds that still drift, use the real photo or a design tool.
