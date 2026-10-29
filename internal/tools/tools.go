// Package tools exposes muse-mcp's features as MCP tools.
package tools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/dtthaison/muse-mcp/internal/chats"
	"github.com/dtthaison/muse-mcp/internal/library"
	"github.com/dtthaison/muse-mcp/internal/muse"
	"github.com/dtthaison/muse-mcp/internal/output"
	"github.com/dtthaison/muse-mcp/internal/prompt"
	"github.com/dtthaison/muse-mcp/internal/voices"
)

// Service is the part of muse.Service the tools use.
type Service interface {
	GenerateImage(ctx context.Context, r muse.ImageRequest) (muse.Result, error)
	Chat(ctx context.Context, r muse.ChatRequest) (muse.Result, error)
	Decide(ctx context.Context, chatID, approvalID string, approve bool) (muse.Result, error)
	Status(ctx context.Context) (muse.StatusInfo, error)
	DeleteChat(ctx context.Context, chatID string) error
	ListChats() []chats.Chat
}

type statusIn struct{}

// ImageArgs are the inputs of muse_generate_image (and of the CLI's image and
// video commands).
type ImageArgs struct {
	OutputPath      string   `json:"output_path" jsonschema:"where to save the image: a .png, .jpg/.jpeg or .webp path; relative paths resolve against the project directory"`
	Prompt          string   `json:"prompt,omitempty" jsonschema:"what to make (needed unless change is given)"`
	Change          string   `json:"change,omitempty" jsonschema:"the one thing to change while keeping characters, scene, lighting, framing and style identical"`
	Characters      []string `json:"characters,omitempty" jsonschema:"names of saved character references to attach"`
	Scene           string   `json:"scene,omitempty" jsonschema:"name of a saved scene reference to attach"`
	ReferenceImages []string `json:"reference_images,omitempty" jsonschema:"extra image files to attach as references"`
	ReferenceRoles  []string `json:"reference_roles,omitempty" jsonschema:"what each reference image is for, in the same order, e.g. the product: shape and colour must stay exact / lighting mood only"`
	ReferenceNotes  string   `json:"reference_notes,omitempty" jsonschema:"what the extra reference images are for"`
	EditImage       string   `json:"edit_image,omitempty" jsonschema:"a photo to edit, e.g. the real product; needs change. muse regenerates it, so check the result against the original"`
	Keep            string   `json:"keep,omitempty" jsonschema:"what must stay exactly the same, e.g. the exact shape, colour, stitch pattern, number of items and label wording; restate it on every edit"`
	AspectRatio     string   `json:"aspect_ratio,omitempty" jsonschema:"1:1 product card, 4:5 feed post, 9:16 story or reel cover, 16:9 banner; muse treats it as a request, not a fixed canvas"`
	Count           int      `json:"count,omitempty" jsonschema:"separate versions to make, 1 to 4 (default 1); each is a generation, saved as output_path, then -1, -2 ..."`
}

// videoIn has some of ImageArgs's fields; output_path's formats differ.
type videoIn struct {
	OutputPath      string   `json:"output_path" jsonschema:"where to save the video: a .mp4 or .webm path; relative paths resolve against the project directory"`
	Prompt          string   `json:"prompt,omitempty" jsonschema:"what to make (needed unless change is given)"`
	Change          string   `json:"change,omitempty" jsonschema:"the one thing to change while keeping characters, scene, lighting, framing and style identical"`
	Characters      []string `json:"characters,omitempty" jsonschema:"names of saved character references to attach"`
	Scene           string   `json:"scene,omitempty" jsonschema:"name of a saved scene reference to attach"`
	ReferenceImages []string `json:"reference_images,omitempty" jsonschema:"image files to attach as references, e.g. the real product photo; they guide the clip but are not used as an exact first frame"`
	ReferenceRoles  []string `json:"reference_roles,omitempty" jsonschema:"what each reference image is for, in the same order, e.g. the product: shape, label and colours exact / the look of the opening shot"`
	ReferenceNotes  string   `json:"reference_notes,omitempty" jsonschema:"what the extra reference images are for"`
	Keep            string   `json:"keep,omitempty" jsonschema:"what must stay exactly the same throughout the clip, e.g. the label, shape and colours"`
	AspectRatio     string   `json:"aspect_ratio,omitempty" jsonschema:"e.g. 16:9, 1:1, 9:16 (a request in the prompt, not a setting)"`
}

// VoiceArgs are the inputs of muse_generate_speech (and the CLI's speech command).
type VoiceArgs struct {
	OutputDir string `json:"output_dir,omitempty" jsonschema:"folder to save the clip in (default: the project directory); the file is named <chat id>-<chat title>.mp3 after muse's chat"`
	Text      string `json:"text" jsonschema:"the script: only the words to speak, written as they should sound (numbers, prices and dates in words; no links, emoji, markup, stage directions or speaker labels; Vietnamese with full tone marks)"`
	Voice     string `json:"voice,omitempty" jsonschema:"a voice name from muse's catalog (see muse_list_voices), e.g. Helio or Aria; empty lets muse pick"`
	Language  string `json:"language,omitempty" jsonschema:"language and locale of the script, e.g. Vietnamese (vi-VN), English (en-GB)"`
	Speed     int    `json:"speed,omitempty" jsonschema:"1 to 200, 100 is normal (the default): calm about 85, sad about 80, excited about 120"`

	IgnoreScriptWarnings bool `json:"ignore_script_warnings,omitempty" jsonschema:"send the script even though muse-mcp flagged things a voice would read wrongly"`
}

// ScriptError means the script has things a voice would read wrongly.
type ScriptError struct{ Warnings []string }

func (e *ScriptError) Error() string {
	return e.Explain("set ignore_script_warnings")
}

// Explain says what to fix; override names how to send the script anyway.
func (e *ScriptError) Explain(override string) string {
	return fmt.Sprintf("the script needs fixing before muse is asked (no quota used); it %s.\nFix the script, or %s to send it as is",
		strings.Join(e.Warnings, "; it "), override)
}

type listVoicesIn struct {
	Query  string `json:"query,omitempty" jsonschema:"words that must all appear in the name, accent, tone or description, e.g. deep calm"`
	Gender string `json:"gender,omitempty" jsonschema:"female, male or neutral"`
	Accent string `json:"accent,omitempty" jsonschema:"e.g. British, Vietnamese"`
	Tone   string `json:"tone,omitempty" jsonschema:"e.g. warm, bright, gravelly"`
	Limit  int    `json:"limit,omitempty" jsonschema:"at most this many voices (default 20, max 200)"`
}

type voicesOut struct {
	Voices  []voices.Voice `json:"voices"`
	Matched int            `json:"matched"` // before the limit
}

type chatIn struct {
	Message      string   `json:"message" jsonschema:"what to say to muse"`
	ChatID       string   `json:"chat_id,omitempty" jsonschema:"chat_id from an earlier result, to continue that conversation"`
	Attachments  []string `json:"attachments,omitempty" jsonschema:"files to attach to the message"`
	SaveImagesTo string   `json:"save_images_to,omitempty" jsonschema:"directory to save any images or videos muse sends back"`
}

type decideIn struct {
	ChatID     string `json:"chat_id"`
	ApprovalID string `json:"approval_id"`
	Decision   string `json:"decision" jsonschema:"approve or deny, as the owner chose"`
}

type saveRefIn struct {
	Name        string   `json:"name" jsonschema:"lowercase letters, digits and dashes, e.g. red-fox"`
	Kind        string   `json:"kind" jsonschema:"character or scene"`
	Description string   `json:"description" jsonschema:"fixed description reused word for word in every prompt"`
	ImagePaths  []string `json:"image_paths" jsonschema:"reference image files (copied into the library)"`
	Replace     bool     `json:"replace,omitempty" jsonschema:"overwrite an existing reference with this name"`
}

type nameIn struct {
	Name string `json:"name"`
}

type listIn struct{}

type refsOut struct {
	References []library.Ref `json:"references"`
}

type chatIDIn struct {
	ChatID string `json:"chat_id" jsonschema:"a chat_id from an earlier muse-mcp result or from muse_list_chats"`
}

type deletedOut struct {
	Deleted string `json:"deleted"`
}

type chatsOut struct {
	Chats []chats.Chat `json:"chats"`
}

type okOut struct {
	OK bool `json:"ok"`
}

const (
	descStatus    = `Check whether muse-mcp's Chrome is running, whether you are logged in to muse.ai, and list approvals waiting for the owner's decision.`
	descGenerate  = `Generate an image with muse.ai and save it to output_path (.png, .jpg or .webp). For recurring characters or scenes, pass their saved names in characters/scene so the same reference images are attached every time. To keep everything the same and change one thing, use change (e.g. "the fox is now asleep") instead of re-describing the scene. For a real product, edit its real photo: edit_image plus change (what may change) and keep (what must stay exact). Give each reference image a role in reference_roles. count makes up to 4 separate versions. muse has no size, style, seed or negative-prompt setting: ratio, style and exclusions are words in the prompt. Takes about a minute.`
	descVideo     = `EXPERIMENTAL, not yet tested end to end (muse blocks AI video for the author's account). Generate a short video (about 10 seconds; one clip per request) with muse.ai and save it to output_path (.mp4 or .webm). muse's video tool has no duration, ratio, audio, negative-prompt, seed or style setting: ratio and style are prompt words, and sound can't be controlled. Reference images guide the clip but aren't used as an exact first frame; give each a role in reference_roles, and say what must not change in keep. One shot, one action, one camera move per clip; no on-screen text you need to read. muse blocks AI video for some accounts and regions; it may then make a substitute (for example an animated still), so read the reply. Can take several minutes.`
	descVoice     = `EXPERIMENTAL, under testing. Make a text-to-speech voice clip with muse.ai and save it in output_dir as <chat id>-<chat title>.mp3. muse's voice tool takes only a voice (a catalog name from muse_list_voices), a language and a speed: no emotion, style, pitch or tags. Put the feeling into the script's wording and punctuation and into speed. text is only the words to say; scripts with digits, symbols, links, emoji, markup, tags, phrases in capitals or speaker labels are refused before muse is asked, so no quota is spent; fix them or set ignore_script_warnings. Takes about a minute.`
	descVoices    = `Search muse's text-to-speech voice catalog (names to pass to muse_generate_speech) by words, gender, accent or tone. The catalog is built in; the owner can import a newer one with "muse-mcp voices import FILE".`
	descChat      = `Send a message to the owner's muse.ai agent and return its reply. Pass chat_id from an earlier result to continue that conversation. muse may ask to act on the owner's accounts; that comes back as status "needs_approval".`
	descDecide    = `Approve or deny an action muse asked permission for. This is the owner's decision: only call it after the owner has told you which to choose for this specific approval.`
	descSaveRef   = `Save a named character or scene (images + a fixed description) for consistent images later. Use kind "character" for who (face, markings, outfit) and "scene" for where (layout, lighting, style).`
	descDelChat   = `Delete a muse chat that muse-mcp started (by chat_id), in muse and from muse-mcp's list. It cannot be undone and the chat's images and clips are gone from muse, so delete only when the owner asked, or when the results are saved and nobody needs the chat in muse any more. The owner's own chats are refused.`
	descListChats = `List the chats muse-mcp started (chat_id and when), oldest first: the chats muse_chat can continue and muse_delete_chat can delete.`
	descListRefs  = `List saved characters and scenes.`
	descDelRef    = `Delete a saved character or scene.`
)

// Register adds the muse tools to s.
func Register(s *mcp.Server, svc Service, lib *library.Store, cat *voices.Catalog) {
	mcp.AddTool(s, &mcp.Tool{Name: "muse_status", Description: descStatus},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ statusIn) (*mcp.CallToolResult, muse.StatusInfo, error) {
			st, err := svc.Status(ctx)
			return nil, st, err
		})

	mcp.AddTool(s, &mcp.Tool{Name: "muse_generate_image", Description: descGenerate},
		func(ctx context.Context, _ *mcp.CallToolRequest, in ImageArgs) (*mcp.CallToolResult, muse.Result, error) {
			req, err := BuildImageRequest(lib, in, muse.MediaImage)
			if err != nil {
				return nil, muse.Result{}, err
			}
			res, err := svc.GenerateImage(ctx, req)
			return nil, res, err
		})

	mcp.AddTool(s, &mcp.Tool{Name: "muse_generate_video", Description: descVideo},
		func(ctx context.Context, _ *mcp.CallToolRequest, in videoIn) (*mcp.CallToolResult, muse.Result, error) {
			req, err := BuildImageRequest(lib, ImageArgs{OutputPath: in.OutputPath, Prompt: in.Prompt, Change: in.Change,
				Characters: in.Characters, Scene: in.Scene, ReferenceImages: in.ReferenceImages,
				ReferenceRoles: in.ReferenceRoles, ReferenceNotes: in.ReferenceNotes, Keep: in.Keep,
				AspectRatio: in.AspectRatio}, muse.MediaVideo)
			if err != nil {
				return nil, muse.Result{}, err
			}
			res, err := svc.GenerateImage(ctx, req)
			return nil, res, err
		})

	mcp.AddTool(s, &mcp.Tool{Name: "muse_generate_speech", Description: descVoice},
		func(ctx context.Context, _ *mcp.CallToolRequest, in VoiceArgs) (*mcp.CallToolResult, muse.Result, error) {
			req, err := BuildVoiceRequest(in)
			if err != nil {
				return nil, muse.Result{}, err
			}
			res, err := svc.GenerateImage(ctx, req)
			return nil, res, err
		})

	mcp.AddTool(s, &mcp.Tool{Name: "muse_list_voices", Description: descVoices},
		func(_ context.Context, _ *mcp.CallToolRequest, in listVoicesIn) (*mcp.CallToolResult, voicesOut, error) {
			vs, n, err := cat.Search(voices.Query{Text: in.Query, Gender: in.Gender, Accent: in.Accent, Tone: in.Tone, Limit: in.Limit})
			if vs == nil {
				vs = []voices.Voice{}
			}
			return nil, voicesOut{Voices: vs, Matched: n}, err
		})

	mcp.AddTool(s, &mcp.Tool{Name: "muse_chat", Description: descChat},
		func(ctx context.Context, _ *mcp.CallToolRequest, in chatIn) (*mcp.CallToolResult, muse.Result, error) {
			files, err := CheckFiles(in.Attachments)
			if err != nil {
				return nil, muse.Result{}, err
			}
			res, err := svc.Chat(ctx, muse.ChatRequest{Message: in.Message, ChatID: in.ChatID,
				Attachments: files, SaveImagesTo: in.SaveImagesTo})
			return nil, res, err
		})

	mcp.AddTool(s, &mcp.Tool{Name: "muse_decide_approval", Description: descDecide},
		func(ctx context.Context, _ *mcp.CallToolRequest, in decideIn) (*mcp.CallToolResult, muse.Result, error) {
			if in.Decision != "approve" && in.Decision != "deny" {
				return nil, muse.Result{}, fmt.Errorf(`decision must be "approve" or "deny", not %q`, in.Decision)
			}
			res, err := svc.Decide(ctx, in.ChatID, in.ApprovalID, in.Decision == "approve")
			return nil, res, err
		})

	mcp.AddTool(s, &mcp.Tool{Name: "muse_delete_chat", Description: descDelChat},
		func(ctx context.Context, _ *mcp.CallToolRequest, in chatIDIn) (*mcp.CallToolResult, deletedOut, error) {
			if err := svc.DeleteChat(ctx, in.ChatID); err != nil {
				return nil, deletedOut{}, err
			}
			return nil, deletedOut{Deleted: in.ChatID}, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "muse_list_chats", Description: descListChats},
		func(context.Context, *mcp.CallToolRequest, statusIn) (*mcp.CallToolResult, chatsOut, error) {
			return nil, chatsOut{Chats: svc.ListChats()}, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "muse_save_reference", Description: descSaveRef},
		func(_ context.Context, _ *mcp.CallToolRequest, in saveRefIn) (*mcp.CallToolResult, library.Ref, error) {
			r, err := lib.Save(in.Name, library.Kind(in.Kind), in.Description, in.ImagePaths, in.Replace)
			return nil, r, err
		})

	mcp.AddTool(s, &mcp.Tool{Name: "muse_list_references", Description: descListRefs},
		func(context.Context, *mcp.CallToolRequest, listIn) (*mcp.CallToolResult, refsOut, error) {
			refs, err := lib.List()
			if refs == nil {
				refs = []library.Ref{}
			}
			return nil, refsOut{References: refs}, err
		})

	mcp.AddTool(s, &mcp.Tool{Name: "muse_delete_reference", Description: descDelRef},
		func(_ context.Context, _ *mcp.CallToolRequest, in nameIn) (*mcp.CallToolResult, okOut, error) {
			if err := lib.Delete(in.Name); err != nil {
				return nil, okOut{}, err
			}
			return nil, okOut{OK: true}, nil
		})
}

// BuildImageRequest checks everything that can be checked before Chrome is
// touched, so a mistake costs no muse quota.
func BuildImageRequest(lib *library.Store, in ImageArgs, media muse.Media) (muse.ImageRequest, error) {
	check, video := output.CheckPath, media == muse.MediaVideo
	if video {
		check = output.CheckVideoPath
	}
	if err := check(in.OutputPath); err != nil {
		return muse.ImageRequest{}, err
	}
	pin := prompt.Input{Prompt: in.Prompt, Change: in.Change, References: in.ReferenceImages,
		ReferenceRoles: in.ReferenceRoles, ReferenceNotes: in.ReferenceNotes, EditImage: in.EditImage, Keep: in.Keep,
		AspectRatio: in.AspectRatio, Count: in.Count, Video: video}
	for _, name := range in.Characters {
		r, err := getRef(lib, name, library.Character)
		if err != nil {
			return muse.ImageRequest{}, err
		}
		pin.Characters = append(pin.Characters, r)
	}
	if in.Scene != "" {
		r, err := getRef(lib, in.Scene, library.Scene)
		if err != nil {
			return muse.ImageRequest{}, err
		}
		pin.Scene = &r
	}
	built, err := prompt.Build(pin)
	if err != nil {
		return muse.ImageRequest{}, err
	}
	return muse.ImageRequest{Text: built.Text, Attachments: built.Attachments, OutputPath: in.OutputPath, Media: media}, nil
}

// BuildVoiceRequest checks a voice request before Chrome is touched.
func BuildVoiceRequest(in VoiceArgs) (muse.ImageRequest, error) {
	dir := in.OutputDir
	if dir == "" {
		dir = "."
	}
	if output.CheckAudioPath(dir) == nil {
		return muse.ImageRequest{}, fmt.Errorf("output_dir %q is a file name; give a folder: the clip is named <chat id>-<chat title>.mp3", dir)
	}
	if w := prompt.CheckScript(in.Text, in.Language); len(w) > 0 && !in.IgnoreScriptWarnings {
		return muse.ImageRequest{}, &ScriptError{Warnings: w}
	}
	built, err := prompt.Voice(prompt.VoiceInput{Text: in.Text, Voice: in.Voice, Language: in.Language,
		Speed: in.Speed})
	if err != nil {
		return muse.ImageRequest{}, err
	}
	return muse.ImageRequest{Text: built.Text, OutputPath: dir, Media: muse.MediaVoice}, nil
}

// CheckFiles requires every chat attachment to be an existing regular file
// and returns their absolute paths.
func CheckFiles(paths []string) ([]string, error) {
	var abs []string
	for _, p := range paths {
		a, err := filepath.Abs(p)
		if err != nil {
			return nil, fmt.Errorf("attachment %q: %w", p, err)
		}
		fi, err := os.Stat(a)
		if err != nil {
			return nil, fmt.Errorf("attachment %q: %w", p, err)
		}
		if !fi.Mode().IsRegular() {
			return nil, fmt.Errorf("attachment %q is not a file", p)
		}
		abs = append(abs, a)
	}
	return abs, nil
}

func getRef(lib *library.Store, name string, kind library.Kind) (library.Ref, error) {
	r, err := lib.Get(name)
	if errors.Is(err, library.ErrNotFound) {
		return r, fmt.Errorf("no saved reference named %q; see muse_list_references", name)
	}
	if err != nil {
		return r, err
	}
	if r.Kind != kind {
		return r, fmt.Errorf("%q is a %s reference, not a %s", name, r.Kind, kind)
	}
	return r, nil
}
