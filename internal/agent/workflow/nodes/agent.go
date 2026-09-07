package nodes

import (
	"context"
	"fmt"
)

// The Agent node: runs ONE ReAct agent turn over tenant context (a minimal
// knowledge-search agent scoped to the node's KBs) and records the final
// answer. The engine stays platform-free — the turn is injected as AgentFunc.
const ComponentAgent = "Agent"

func init() {
	RegisterNodeFactory(ComponentAgent, newAgent)
}

// AgentRequest is the rendered input handed to an injected AgentFunc.
type AgentRequest struct {
	Prompt       string
	SystemPrompt string
	Model        string
	KBIDs        []string
	Temperature  float64
}

// AgentFunc executes one autonomous agent turn and returns its final
// answer. Injected by the compiler.
type AgentFunc func(ctx context.Context, req AgentRequest) (string, error)

type agentNode struct {
	prompt       string
	systemPrompt string
	model        string
	kbIDs        []string
	temperature  float64
	run          AgentFunc
}

func newAgent(params map[string]any, deps Deps) (Node, error) {
	prompt, err := strParam(ComponentAgent, "prompt", params, true)
	if err != nil {
		return nil, err
	}
	systemPrompt, err := strParam(ComponentAgent, "system_prompt", params, false)
	if err != nil {
		return nil, err
	}
	model, _ := params["model"].(string)
	kbIDs, err := strSliceParam(ComponentAgent, "kb_ids", params)
	if err != nil {
		return nil, err
	}
	temperature := 0.0
	if v, ok := params["temperature"]; ok && v != nil {
		if temperature, err = toFloat(ComponentAgent, "temperature", v); err != nil {
			return nil, err
		}
	}
	return &agentNode{
		prompt: prompt, systemPrompt: systemPrompt, model: model,
		kbIDs: kbIDs, temperature: temperature, run: deps.AgentFunc,
	}, nil
}

func (n *agentNode) Invoke(ctx context.Context, inputs map[string]any) (map[string]any, error) {
	if n.run == nil {
		return nil, fmt.Errorf("workflow Agent: no AgentFunc injected (no agent runtime configured)")
	}
	st, err := StateFromInputs(inputs)
	if err != nil {
		return nil, err
	}
	prompt, err := Render(n.prompt, st)
	if err != nil {
		return nil, fmt.Errorf("workflow Agent: %w", err)
	}
	system := ""
	if n.systemPrompt != "" {
		if system, err = Render(n.systemPrompt, st); err != nil {
			return nil, fmt.Errorf("workflow Agent: system_prompt: %w", err)
		}
	}
	answer, err := n.run(ctx, AgentRequest{
		Prompt: prompt, SystemPrompt: system, Model: n.model,
		KBIDs: n.kbIDs, Temperature: n.temperature,
	})
	if err != nil {
		return nil, fmt.Errorf("workflow Agent: %w", err)
	}
	return map[string]any{"answer": answer}, nil
}
