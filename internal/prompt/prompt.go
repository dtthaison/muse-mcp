// Package prompt turns an image request into the text typed into muse.ai and
// the ordered list of files attached with it. All prompt wording lives here so
// it can be tuned from live results.
package prompt

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"pp/muse-mcp/internal/library"
)

// MaxAttachments caps the files sent with one request.
const MaxAttachments = 10

// Nudge is sent once when muse answers with text but no image.
const Nudge = "Please attach the final image here in the chat, as a single image."

const closing = "Attach the final image here in the chat as a single image."

// Input is what the caller asked for, with saved references already resolved.
type Input struct {
	Prompt         string
	Change         string
	Characters     []library.Ref
	Scene          *library.Ref
	References     []string // ad hoc image files
	ReferenceNotes string
	AspectRatio    string
}

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
		abs, err := filepath.Abs(p)
		if err != nil {
			return Built{}, fmt.Errorf("reference image %q: %w", p, err)
		}
		if _, err := os.Stat(abs); err != nil {
			return Built{}, fmt.Errorf("reference image %q: %w", p, err)
		}
		note := ""
		if i == len(in.References)-1 && in.ReferenceNotes != "" {
			note = " Note: " + in.ReferenceNotes
		}
		add([]string{abs}, func(span string) string { return span + " a reference image." + note })
	}
	if len(files) > MaxAttachments {
		return Built{}, fmt.Errorf("%d reference images; at most %d per request, so use fewer references", len(files), MaxAttachments)
	}

	var instr string
	if in.Change != "" {
		instr = fmt.Sprintf("Generate a new image. Change ONLY: %s. Keep everything else identical: same character(s), same scene, same lighting, same framing and camera angle, same style. Do not add or remove anything else.",
			sentence(in.Change))
		if in.Prompt != "" {
			instr += "\n\n" + in.Prompt
		}
	} else {
		instr = "Generate an image: " + in.Prompt
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
	b.WriteString(closing)
	return Built{Text: b.String(), Attachments: files}, nil
}

// sentence trims whitespace and a trailing full stop so templates can add one.
func sentence(s string) string {
	return strings.TrimRight(strings.TrimSpace(s), ".")
}
