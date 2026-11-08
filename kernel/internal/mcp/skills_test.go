package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/engine"
	"github.com/rangka-os/rangka-os/kernel/internal/skills"
)

// The platform skills name only tools this server lists, and pass the rest of rangka skills check.
func TestPlatformSkillsPassCheck(t *testing.T) {
	tools := ToolNames()
	for _, s := range skills.Platform() {
		for _, p := range skills.Check(s, tools) {
			t.Error(p.String())
		}
	}
}

func TestSkillAndCatalogueTools(t *testing.T) {
	h := &Handler{Engine: func() *engine.Engine { return nil }, Catalogue: func() any { return map[string]any{"apps": []any{}} }}
	ctx, c := context.Background(), &Caller{}
	for _, n := range []string{"rangka_catalogue", "rangka_skills", "rangka_skill", "rangka_skill_file"} {
		if !ToolNames()[n] || !assistantTools[n] {
			t.Errorf("%s: listed %v, allowed for the assistant %v", n, ToolNames()[n], assistantTools[n])
		}
	}
	if v, err := h.call(ctx, c, "rangka_catalogue", nil); err != nil || v.(map[string]any)["apps"] == nil {
		t.Errorf("catalogue: %v %v", v, err)
	}
	v, err := h.call(ctx, c, "rangka_skills", nil)
	if err != nil || len(v.(map[string]any)["skills"].([]any)) < 3 {
		t.Fatalf("skills: %v %v", v, err)
	}
	v, err = h.call(ctx, c, "rangka_skill", map[string]any{"name": "rangka-compose"})
	if err != nil || !strings.Contains(v.(map[string]any)["instructions"].(string), "rangka_catalogue") {
		t.Errorf("skill: %v %v", v, err)
	}
	if _, err := h.call(ctx, c, "rangka_skill", map[string]any{"name": "rangka-nope"}); err == nil {
		t.Error("unknown skill should fail")
	}
	if _, err := h.call(ctx, c, "rangka_skill_file", map[string]any{"name": "rangka-compose", "path": "../rangka-concepts/SKILL.md"}); err == nil {
		t.Error("a path outside the skill should fail")
	}
	v, err = h.call(ctx, c, "rangka_skill_file", map[string]any{"name": "rangka-compose", "path": "SKILL.md"})
	if err != nil || !strings.HasPrefix(v.(map[string]any)["text"].(string), "---\nname: rangka-compose") {
		t.Errorf("skill file: %v %v", v, err)
	}
}
