package nodes

import (
	"context"
	"fmt"
)

// The MCPTool node: calls one PRE-AUTHORIZED MCP tool of a tenant-configured
// service with rendered JSON args. The engine stays network-free — the call
// is injected as MCPFunc; interactive OAuth paths are the adapter's concern.
const ComponentMCPTool = "MCPTool"

func init() {
	RegisterNodeFactory(ComponentMCPTool, newMCPTool)
}

// MCPToolRequest is the rendered input handed to an injected MCPFunc.
type MCPToolRequest struct {
	ServiceID      string
	Tool           string
	ArgsJSON       string // rendered JSON object ("{}" when the node declares no args)
	TimeoutSeconds int
}

// MCPFunc executes one MCP tool call and returns the raw parsed result plus
// a text rendering of it. Injected by the compiler.
type MCPFunc func(ctx context.Context, req MCPToolRequest) (any, string, error)

type mcpToolNode struct {
	serviceID  string
	tool       string
	args       string
	timeoutSec int
	call       MCPFunc
}

func newMCPTool(params map[string]any, deps Deps) (Node, error) {
	serviceID, err := strParam(ComponentMCPTool, "service_id", params, true)
	if err != nil {
		return nil, err
	}
	tool, err := strParam(ComponentMCPTool, "tool", params, true)
	if err != nil {
		return nil, err
	}
	// args is optional: an empty template means no arguments.
	args, _ := params["args"].(string)
	timeoutSec := 30
	if v, ok := params["timeout_seconds"]; ok && v != nil {
		if timeoutSec, err = toInt(ComponentMCPTool, "timeout_seconds", v); err != nil {
			return nil, err
		}
	}
	return &mcpToolNode{
		serviceID: serviceID, tool: tool, args: args,
		timeoutSec: timeoutSec, call: deps.MCPFunc,
	}, nil
}

func (n *mcpToolNode) Invoke(ctx context.Context, inputs map[string]any) (map[string]any, error) {
	if n.call == nil {
		return nil, fmt.Errorf("workflow MCPTool: no MCPFunc injected (no MCP runtime configured)")
	}
	st, err := StateFromInputs(inputs)
	if err != nil {
		return nil, err
	}
	argsJSON := "{}"
	if n.args != "" {
		rendered, rerr := Render(n.args, st)
		if rerr != nil {
			return nil, fmt.Errorf("workflow MCPTool: args: %w", rerr)
		}
		argsJSON = rendered
	}
	result, text, err := n.call(ctx, MCPToolRequest{
		ServiceID: n.serviceID, Tool: n.tool,
		ArgsJSON: argsJSON, TimeoutSeconds: n.timeoutSec,
	})
	if err != nil {
		return nil, fmt.Errorf("workflow MCPTool: %w", err)
	}
	return map[string]any{"result": result, "result_text": text}, nil
}
