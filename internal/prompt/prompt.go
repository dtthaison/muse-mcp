// Package prompt turns an image request into the text typed into muse.ai and
// the ordered list of files attached with it. All prompt wording lives here so
// it can be tuned from live results.
package prompt

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/dtthaison/muse-mcp/internal/library"
)

// MaxAttachments caps the files sent with one request.
const MaxAttachments = 10

// Nudge is sent once when muse answers with text but no image; NudgeVideo
// when it should have been a video.
const (
	Nudge      = "Please attach the final image here in the chat, as a single image."
	NudgeVideo = "Please attach the final video here in the chat, as a single video."
	NudgeVoice = "Please attach the final audio clip here in the chat, as a single audio file."
)

// Input is what the caller asked for, with saved references already resolved.
type Input struct {
	Prompt         string
	Change         string
	Characters     []library.Ref
	Scene          *library.Ref
	References     []string // ad hoc image files
	ReferenceRoles []string // what each of References is for, in order; may be shorter
	ReferenceNotes string
	EditImage      string // a photo to edit (needs Change); attached first as the canvas
	Keep           string // what must stay exactly the same, restated on every edit
	AspectRatio    string
	Count          int  // separate versions, 1 to MaxCount; 0 means 1
	Video          bool // ask for a video instead of an image
}

// MaxCount is how many separate images muse makes for one request.
const MaxCount = 4

// Built is the text to send and the files to attach, in the order the text
// numbers them.
type Built struct {
	Text        string
	Attachments []string
}

func Build(in Input) (Built, error) {
	if strings.TrimSpace(in.Prompt) == "" && strings.TrimSpace(in.Change) == "" {
		return Built{}, errors.New("give a prompt (what to make) or a change (what to change)")
	}
	if in.EditImage != "" && strings.TrimSpace(in.Change) == "" {
		return Built{}, errors.New("edit_image needs change: say what to change in the photo")
	}
	if in.Count < 0 || in.Count > MaxCount {
		return Built{}, fmt.Errorf("count %d: muse makes 1 to %d images per request", in.Count, MaxCount)
	}
	if in.Count > 1 && in.Video {
		return Built{}, errors.New("count: one video per request")
	}
	if len(in.ReferenceRoles) > len(in.References) {
		return Built{}, fmt.Errorf("%d reference roles for %d reference images", len(in.ReferenceRoles), len(in.References))
	}

	var lines, files []string
	add := func(images []string, line func(span string) string) {
		n := len(files) + 1
		span := fmt.Sprintf("Image %d is", n)
		if len(images) > 1 {
			span = fmt.Sprintf("Images %d–%d are", n, n+len(images)-1)
		}
		lines = append(lines, line(span))
		files = append(files, images...)
	}
	if in.EditImage != "" {
		abs, err := checkedImage(in.EditImage, "photo to edit")
		if err != nil {
			return Built{}, err
		}
		add([]string{abs}, func(span string) string { return span + " the photo to edit." })
	}
	for _, c := range in.Characters {
		add(c.Images, func(span string) string {
			return fmt.Sprintf("%s the character reference %q: %s. Keep face, fur/hair, markings, outfit and proportions exactly.",
				span, c.Name, sentence(c.Description))
		})
	}
	if s := in.Scene; s != nil {
		add(s.Images, func(span string) string {
			return fmt.Sprintf("%s the scene reference %q: %s. Keep layout, objects, lighting and style exactly.",
				span, s.Name, sentence(s.Description))
		})
	}
	for i, p := range in.References {
		abs, err := checkedImage(p, "reference image")
		if err != nil {
			return Built{}, err
		}
		role := "a reference image"
		if i < len(in.ReferenceRoles) && strings.TrimSpace(in.ReferenceRoles[i]) != "" {
			role = sentence(in.ReferenceRoles[i])
		}
		note := ""
		if i == len(in.References)-1 && in.ReferenceNotes != "" {
			note = " Note: " + in.ReferenceNotes
		}
		add([]string{abs}, func(span string) string { return span + " " + role + "." + note })
	}
	if len(files) > MaxAttachments {
		return Built{}, fmt.Errorf("%d reference images; at most %d per request, so use fewer references", len(files), MaxAttachments)
	}

	kind := "image"
	if in.Video {
		kind = "video"
	}
	var instr string
	if in.EditImage != "" {
		instr = fmt.Sprintf("Edit image 1. Change only: %s. Keep everything else in the photo unchanged; do not redesign, recolour or add anything.",
			sentence(in.Change))
		if in.Prompt != "" {
			instr += "\n\n" + in.Prompt
		}
	} else if in.Change != "" {
		instr = fmt.Sprintf("Generate a new "+kind+". Change ONLY: %s. Keep everything else identical: same character(s), same scene, same lighting, same framing and camera angle, same style. Do not add or remove anything else.",
			sentence(in.Change))
		if in.Prompt != "" {
			instr += "\n\n" + in.Prompt
		}
	} else {
		instr = "Generate a video: " + in.Prompt
		if !in.Video {
			instr = "Generate an image: " + in.Prompt
		}
	}
	if k := sentence(in.Keep); k != "" {
		instr += " Keep exactly: " + k + "."
	}
	if in.AspectRatio != "" {
		instr += " Aspect ratio " + in.AspectRatio + "."
	}

	var b strings.Builder
	if len(lines) > 0 {
		b.WriteString(strings.Join(lines, "\n"))
		b.WriteString("\n\n")
	}
	b.WriteString(instr)
	b.WriteString("\n\n")
	if in.Count > 1 {
		fmt.Fprintf(&b, "Make %d separate versions and attach all %d images here in the chat.", in.Count, in.Count)
	} else {
		b.WriteString("Attach the final " + kind + " here in the chat as a single " + kind + ".")
	}
	return Built{Text: b.String(), Attachments: files}, nil
}

func checkedImage(p, what string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", fmt.Errorf("%s %q: %w", what, p, err)
	}
	if err := library.CheckImage(abs); err != nil {
		return "", fmt.Errorf("%s %q: %w", what, p, err)
	}
	return abs, nil
}

// sentence trims whitespace and a trailing full stop so templates can add one.
func sentence(s string) string {
	return strings.TrimRight(strings.TrimSpace(s), ".")
}
