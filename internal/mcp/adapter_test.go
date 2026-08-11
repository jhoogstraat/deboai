package mcp

import (
	"context"
	"testing"

	"github.com/jhoogstraat/deboai/internal/tools"
)

func TestAdaptBuildsStrictInputSchema(t *testing.T) {
	adapted := Adapt([]tools.Definition{{
		Name:        "ticket",
		Description: "Return a ticket.",
		Arguments: []tools.Argument{
			{Name: "ticket", Description: "Ticket key.", Required: true},
			{Name: "worktree_path", Description: "Worktree path."},
		},
		Handler: func(context.Context, tools.Arguments) (string, error) { return "", nil },
	}})

	if len(adapted) != 1 || adapted[0].Name != "ticket" {
		t.Fatalf("Adapt() = %#v", adapted)
	}
	if adapted[0].InputSchema["additionalProperties"] != false {
		t.Fatal("adapted schema accepts undeclared arguments")
	}
	properties, _ := adapted[0].InputSchema["properties"].(map[string]any)
	if properties["ticket"] == nil || properties["worktree_path"] == nil {
		t.Fatalf("adapted properties = %#v", properties)
	}
	required, _ := adapted[0].InputSchema["required"].([]string)
	if len(required) != 1 || required[0] != "ticket" {
		t.Fatalf("adapted required arguments = %#v", required)
	}
}
