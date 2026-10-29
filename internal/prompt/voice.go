package prompt

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

// VoiceInput is a text-to-speech request. muse's voice tool takes only a
// voice, a language and a speed: it has no emotion, style or tag settings,
// so a feeling has to be written into the script and the speed.
type VoiceInput struct {
	Text     string // the script, said word for word
	Voice    string // a name from muse's voice catalog, e.g. "Helio"; empty lets muse pick
	Language string // e.g. "Vietnamese (vi-VN)"
	Speed    int    // 1 to 200; 0 means 100, normal
}

// Voice builds the message for a voice clip. Nothing is attached.
func Voice(in VoiceInput) (Built, error) {
	text := strings.TrimSpace(in.Text)
	if text == "" {
		return Built{}, errors.New("give the text to say")
	}
	speed := in.Speed
	if speed == 0 {
		speed = 100 // say it, or muse picks a speed from the wording
	}
	if speed < 1 || speed > 200 {
		return Built{}, fmt.Errorf("speed %d: use 1 to 200 (100 is normal)", in.Speed)
	}
	var b strings.Builder
	b.WriteString("Generate a voice clip (text to speech) and attach it here in the chat as a single audio file.\n\n")
	if v := strings.TrimSpace(in.Voice); v != "" {
		fmt.Fprintf(&b, "Voice: %q from your voice catalog.\n", v)
	} else {
		b.WriteString("Voice: pick one from your voice catalog that suits the script.\n")
	}
	if l := sentence(in.Language); l != "" {
		fmt.Fprintf(&b, "Language: %s\n", l)
	}
	fmt.Fprintf(&b, "Speed: %d (your voice tool's speed setting: 1 to 200, 100 is normal)\n\n", speed)
	fmt.Fprintf(&b, "Script (say exactly this, word for word; add nothing, leave nothing out):\n\"\"\"\n%s\n\"\"\"", text)
	return Built{Text: b.String()}, nil
}

// MaxScript is the longest script sent as one clip; longer ones should be split.
const MaxScript = 4000

var (
	digitRE   = regexp.MustCompile(`\d`)
	symbolRE  = regexp.MustCompile(`[%$€£¥₫]`)
	linkRE    = regexp.MustCompile(`(?i)https?://|www\.|\S+@\S+\.\w`)
	bracketRE = regexp.MustCompile(`[\[\]<>{}]`)
	mdRE      = regexp.MustCompile("\\*\\*|__|`|(?m)^\\s*(#{1,6}\\s|[-*]\\s)")
	speakerRE = regexp.MustCompile(`(?m)^\s*\p{Lu}[\p{L} ]{0,20}:\s`)
)

// CheckScript lists what in a script a voice would read wrongly or aloud,
// per muse's voice-prompt guide. language is the request's language, if any.
func CheckScript(text, language string) []string {
	var w []string
	if digitRE.MatchString(text) {
		w = append(w, "contains digits: write numbers, prices, dates and units out in words in the script's language")
	}
	if symbolRE.MatchString(text) {
		w = append(w, "contains %, $ or another currency symbol: write it out in words")
	}
	if linkRE.MatchString(text) {
		w = append(w, "contains a link or email address: say it in words or leave it out")
	}
	if strings.IndexFunc(text, isEmoji) >= 0 {
		w = append(w, "contains emoji: they are skipped or read out as names")
	}
	if bracketRE.MatchString(text) {
		w = append(w, "contains brackets or tags like [whispers] or <break>: muse has no tags and may read them aloud; express the feeling with wording and speed")
	}
	if mdRE.MatchString(text) {
		w = append(w, "contains markdown: write plain spoken text")
	}
	if speakerRE.MatchString(text) {
		w = append(w, `contains speaker labels like "Lan:": one clip is one voice, and labels are read aloud`)
	}
	if len(text) > MaxScript {
		w = append(w, fmt.Sprintf("is long (%d characters): split it into clips of at most %d", len(text), MaxScript))
	}
	if isVietnamese(language) && looksToneless(text) {
		w = append(w, "is Vietnamese without tone marks: write full diacritics (tiếng Việt, not tieng Viet)")
	}
	return w
}

func isEmoji(r rune) bool {
	return r >= 0x1F000 || (r >= 0x2600 && r <= 0x27BF)
}

func isVietnamese(language string) bool {
	l := strings.ToLower(language)
	return strings.Contains(l, "vietnam") || strings.Contains(l, "tiếng việt") || strings.HasPrefix(l, "vi")
}

// looksToneless reports a script of several words with no letter beyond ASCII:
// real Vietnamese has diacritics in almost every sentence.
func looksToneless(text string) bool {
	if len(strings.Fields(text)) < 4 {
		return false
	}
	for _, r := range text {
		if unicode.IsLetter(r) && r > unicode.MaxASCII {
			return false
		}
	}
	return true
}
