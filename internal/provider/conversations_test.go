package provider

import "testing"

// Endpoints replaced four copies of this fold. The contract the copies shared:
// the FIRST user turn and the LAST non-blank assistant turn.
func TestEndpointsTakesFirstPromptAndLastResponse(t *testing.T) {
	turns := []TurnInfo{
		{Role: "user", Content: "first question"},
		{Role: "assistant", Content: "first answer"},
		{Role: "user", Content: "second question"},
		{Role: "assistant", Content: "   "},
		{Role: "assistant", Content: "last answer"},
	}
	prompt, response := Endpoints(turns)
	if prompt != "first question" {
		t.Fatalf("prompt: got %q", prompt)
	}
	if response != "last answer" {
		t.Fatalf("response: got %q", response)
	}
}

func TestEndpointsIgnoresBlankAssistantTurns(t *testing.T) {
	_, response := Endpoints([]TurnInfo{{Role: "assistant", Content: "\n\t "}})
	if response != "" {
		t.Fatalf("a blank assistant turn became a response: %q", response)
	}
}

func TestPlanTasksTakesOnlyUncheckedItems(t *testing.T) {
	got := PlanTasks("- [ ] first\n- [x] done\n  * [ ] second\ntext\n")
	if len(got) != 2 || got[0] != "first" || got[1] != "second" {
		t.Fatalf("got %q, want [first second]", got)
	}
}

func TestSkillDescriptionReadsFrontmatter(t *testing.T) {
	got := SkillDescription("---\nname: thing\ndescription: does a thing\n---\n\n# Body\ndescription: not this\n")
	if got != "does a thing" {
		t.Fatalf("got %q", got)
	}
}

// A description after the frontmatter closes is body text, not metadata.
func TestSkillDescriptionStopsAtEndOfFrontmatter(t *testing.T) {
	if got := SkillDescription("---\nname: thing\n---\ndescription: body text\n"); got != "" {
		t.Fatalf("read a description from the body: %q", got)
	}
}

func TestSkillDescriptionWithoutFrontmatter(t *testing.T) {
	if got := SkillDescription("# Just a heading\n"); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}
