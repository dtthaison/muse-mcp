// Package tools exposes muse-mcp's features as MCP tools.
package tools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"pp/muse-mcp/internal/library"
	"pp/muse-mcp/internal/muse"
	"pp/muse-mcp/internal/output"
	"pp/muse-mcp/internal/prompt"
)

// Service is the part of muse.Service the tools use.
type Service interface {
	GenerateImage(ctx context.Context, r muse.ImageRequest) (muse.Result, error)
	Chat(ctx context.Context, r muse.ChatRequest) (muse.Result, error)
	Decide(ctx context.Context, chatID, approvalID string, approve bool) (muse.Result, error)
	Status(ctx context.Context) (muse.StatusInfo, error)
}

type statusIn struct{}

type generateIn struct {
	OutputPath      string   `json:"output_path" jsonschema:"where to save the image: a .png, .jpg/.jpeg or .webp path; relative paths resolve against the project directory"`
	Prompt          string   `json:"prompt,omitempty" jsonschema:"what to make (needed unless change is given)"`
	Change          string   `json:"change,omitempty" jsonschema:"the one thing to change while keeping characters, scene, lighting, framing and style identical"`
	Characters      []string `json:"characters,omitempty" jsonschema:"names of saved character references to attach"`
	Scene           string   `json:"scene,omitempty" jsonschema:"name of a saved scene reference to attach"`
	ReferenceImages []string `json:"reference_images,omitempty" jsonschema:"extra image files to attach as references"`
	ReferenceNotes  string   `json:"reference_notes,omitempty" jsonschema:"what the extra reference images are for"`
	AspectRatio     string   `json:"aspect_ratio,omitempty" jsonschema:"e.g. 16:9, 1:1, 9:16"`
}

type chatIn struct {
	Message      string   `json:"message" jsonschema:"what to say to muse"`
	ChatID       string   `json:"chat_id,omitempty" jsonschema:"chat_id from an earlier result, to continue that conversation"`
	Attachments  []string `json:"attachments,omitempty" jsonschema:"files to attach to the message"`
	SaveImagesTo string   `json:"save_images_to,omitempty" jsonschema:"directory to save any images muse sends back"`
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

type okOut struct {
	OK bool `json:"ok"`
}

const (
	descStatus   = `Check whether muse-mcp's Chrome is running, whether you are logged in to muse.ai, and list approvals waiting for the owner's decision.`
	descGenerate = `Generate an image with muse.ai and save it to output_path (.png, .jpg or .webp). For recurring characters or scenes, pass their saved names in characters/scene so the same reference images are attached every time. To keep everything the same and change one thing, use change (e.g. "the fox is now asleep") instead of re-describing the scene. Takes about a minute.`
	descChat     = `Send a message to the owner's muse.ai agent and return its reply. Pass chat_id from an earlier result to continue that conversation. muse may ask to act on the owner's accounts; that comes back as status "needs_approval".`
	descDecide   = `Approve or deny an action muse asked permission for. This is the owner's decision: only call it after the owner has told you which to choose for this specific approval.`
	descSaveRef  = `Save a named character or scene (images + a fixed description) for consistent images later. Use kind "character" for who (face, markings, outfit) and "scene" for where (layout, lighting, style).`
	descListRefs = `List saved characters and scenes.`
	descDelRef   = `Delete a saved character or scene.`
)

// Register adds the seven muse tools to s.
func Register(s *mcp.Server, svc Service, lib *library.Store) {
	mcp.AddTool(s, &mcp.Tool{Name: "muse_status", Description: descStatus},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ statusIn) (*mcp.CallToolResult, muse.StatusInfo, error) {
			st, err := svc.Status(ctx)
			return nil, st, err
		})

	mcp.AddTool(s, &mcp.Tool{Name: "muse_generate_image", Description: descGenerate},
		func(ctx context.Context, _ *mcp.CallToolRequest, in generateIn) (*mcp.CallToolResult, muse.Result, error) {
			req, err := buildImageRequest(lib, in)
			if err != nil {
				return nil, muse.Result{}, err
			}
			res, err := svc.GenerateImage(ctx, req)
			return nil, res, err
		})

	mcp.AddTool(s, &mcp.Tool{Name: "muse_chat", Description: descChat},
		func(ctx context.Context, _ *mcp.CallToolRequest, in chatIn) (*mcp.CallToolResult, muse.Result, error) {
			files, err := checkFiles(in.Attachments)
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

// buildImageRequest checks everything that can be checked before Chrome is
// touched, so a mistake costs no muse quota.
func buildImageRequest(lib *library.Store, in generateIn) (muse.ImageRequest, error) {
	if err := output.CheckPath(in.OutputPath); err != nil {
		return muse.ImageRequest{}, err
	}
	pin := prompt.Input{Prompt: in.Prompt, Change: in.Change, References: in.ReferenceImages,
		ReferenceNotes: in.ReferenceNotes, AspectRatio: in.AspectRatio}
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
	return muse.ImageRequest{Text: built.Text, Attachments: built.Attachments, OutputPath: in.OutputPath}, nil
}

// checkFiles requires every chat attachment to be an existing regular file
// and returns their absolute paths.
func checkFiles(paths []string) ([]string, error) {
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
