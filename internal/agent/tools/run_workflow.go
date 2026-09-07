package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

// The workflow tools let an agent discover and execute the workspace's
// PUBLISHED workflows. Both are opt-in per agent config (AvailableToolDefinitions
// checkboxes); drafts stay invisible — publishing is the sharing boundary.

// WorkflowRunner is the narrow workflow surface these tools need. The agent
// service injects the real WorkflowService; the interface keeps the tools
// package free of service-layer imports (same pattern as MemoryService).
type WorkflowRunner interface {
	// ListWorkflows returns one page of the caller's tenant workflows.
	ListWorkflows(ctx context.Context, page, pageSize int) ([]*types.Workflow, int64, error)
	// RunWorkflow executes one run (synchronous mode returns the terminal row).
	RunWorkflow(ctx context.Context, id string, req *types.RunWorkflowRequest) (*types.WorkflowRun, error)
}

// ---- list_workflows --------------------------------------------------------

var listWorkflowsTool = BaseTool{
	name: ToolListWorkflows,
	description: `List the workspace's published workflows.

## When to Use

Call this before run_workflow whenever you do not already know the workflow's
exact id or name. Only published workflows are listed; drafts are invisible.

## What It Returns

One line per workflow: id, name, and description.`,
	schema: json.RawMessage(`{
  "type": "object",
  "properties": {},
  "additionalProperties": false
}`),
}

// ListWorkflowsTool lists published workflows for the LLM to pick from.
type ListWorkflowsTool struct {
	BaseTool
	runner WorkflowRunner
}

// NewListWorkflowsTool creates the workflow discovery tool.
func NewListWorkflowsTool(runner WorkflowRunner) *ListWorkflowsTool {
	return &ListWorkflowsTool{BaseTool: listWorkflowsTool, runner: runner}
}

// Execute lists the tenant's published workflows.
func (t *ListWorkflowsTool) Execute(ctx context.Context, _ json.RawMessage) (*types.ToolResult, error) {
	workflows, _, err := t.runner.ListWorkflows(ctx, 1, 50)
	if err != nil {
		return nil, fmt.Errorf("list workflows: %w", err)
	}
	var b strings.Builder
	count := 0
	for _, wf := range workflows {
		if wf == nil || wf.Status != types.WorkflowStatusPublished {
			continue
		}
		count++
		fmt.Fprintf(&b, "- %s | %s", wf.ID, wf.Name)
		if wf.Description != "" {
			fmt.Fprintf(&b, " | %s", wf.Description)
		}
		b.WriteString("\n")
	}
	if count == 0 {
		return &types.ToolResult{
			Success: true,
			Output:  "No published workflows in this workspace.",
		}, nil
	}
	return &types.ToolResult{Success: true, Output: strings.TrimSpace(b.String())}, nil
}

// ---- run_workflow ----------------------------------------------------------

var runWorkflowTool = BaseTool{
	name: ToolRunWorkflow,
	description: `Execute a published workflow and return its answer.

## When to Use

When the user's task matches a published workflow's purpose (a fixed
retrieval/generation/routing pipeline), prefer running it over improvising
the steps yourself — the workflow encodes the exact procedure.

## What It Returns

The workflow's final answer plus the run status. Long pipelines still run to
completion synchronously (bounded by the run cap).`,
	schema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "workflow": {
      "type": "string",
      "description": "Workflow id or exact name (see list_workflows)"
    },
    "query": {
      "type": "string",
      "description": "The input question passed to the workflow"
    },
    "inputs": {
      "type": "object",
      "description": "Optional values for the workflow's declared input form fields, keyed by field name",
      "additionalProperties": true
    }
  },
  "required": ["workflow", "query"]
}`),
}

// RunWorkflowInput defines the input parameters for the tool.
type RunWorkflowInput struct {
	Workflow string         `json:"workflow"`
	Query    string         `json:"query"`
	Inputs   map[string]any `json:"inputs,omitempty"`
}

// RunWorkflowTool executes a published workflow from the agent loop.
type RunWorkflowTool struct {
	BaseTool
	runner WorkflowRunner
}

// NewRunWorkflowTool creates the workflow execution tool.
func NewRunWorkflowTool(runner WorkflowRunner) *RunWorkflowTool {
	return &RunWorkflowTool{BaseTool: runWorkflowTool, runner: runner}
}

// Execute resolves the workflow (id first, then exact published name), runs
// it synchronously, and returns the answer. Tenant identity flows through
// the request context — the service scopes every lookup and run.
func (t *RunWorkflowTool) Execute(ctx context.Context, args json.RawMessage) (*types.ToolResult, error) {
	var input RunWorkflowInput
	if err := json.Unmarshal(args, &input); err != nil {
		return nil, fmt.Errorf("parse arguments: %w", err)
	}
	if strings.TrimSpace(input.Workflow) == "" || strings.TrimSpace(input.Query) == "" {
		return nil, fmt.Errorf("workflow and query are required")
	}
	id, err := t.resolveWorkflowID(ctx, input.Workflow)
	if err != nil {
		return nil, err
	}
	start := time.Now()
	run, err := t.runner.RunWorkflow(ctx, id, &types.RunWorkflowRequest{
		Query:  input.Query,
		Inputs: input.Inputs,
	})
	if err != nil {
		return nil, fmt.Errorf("run workflow: %w", err)
	}
	if run.Status == types.WorkflowRunStatusFailed {
		return &types.ToolResult{
			Success: false,
			Output:  fmt.Sprintf("workflow run failed: %s", run.Error),
			Error:   run.Error,
		}, nil
	}
	answer := ""
	if len(run.Output) > 0 {
		var out struct {
			Answer string `json:"answer"`
		}
		if json.Unmarshal(run.Output, &out) == nil {
			answer = out.Answer
		}
	}
	return &types.ToolResult{
		Success: true,
		Output:  answer,
		Data: map[string]interface{}{
			"run_id":      run.ID,
			"status":      run.Status,
			"duration_ms": time.Since(start).Milliseconds(),
		},
	}, nil
}

// resolveWorkflowID maps a user-supplied id-or-name onto a PUBLISHED
// workflow id. Ids win; names must match exactly among published workflows
// (the tool description points the LLM at list_workflows for the catalog).
func (t *RunWorkflowTool) resolveWorkflowID(ctx context.Context, workflow string) (string, error) {
	workflows, _, err := t.runner.ListWorkflows(ctx, 1, 50)
	if err != nil {
		return "", fmt.Errorf("resolve workflow: %w", err)
	}
	byName := ""
	for _, wf := range workflows {
		if wf == nil || wf.Status != types.WorkflowStatusPublished {
			continue
		}
		if wf.ID == workflow {
			return wf.ID, nil
		}
		if wf.Name == workflow {
			byName = wf.ID
		}
	}
	if byName != "" {
		return byName, nil
	}
	return "", fmt.Errorf("published workflow %q not found — call list_workflows for the catalog", workflow)
}
