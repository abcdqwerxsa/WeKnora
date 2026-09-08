package container

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/application/service"
	"go.uber.org/dig"
)

// TestWorkflowAgentWiringHasNoCycle guards against reintroducing the
// WorkflowService ⇄ AgentService constructor cycle: both constructors needed
// each other and uber/dig rejected the graph at Provide time, panicking on
// startup in production. The cycle is broken with AgentServiceRef (workflow
// holds the ref; NewAgentService fills it). dig detects cycles when the loop
// closes, so providing all three participants is the whole check.
func TestWorkflowAgentWiringHasNoCycle(t *testing.T) {
	c := dig.New()
	for _, p := range []interface{}{
		func() *service.AgentServiceRef { return &service.AgentServiceRef{} },
		service.NewWorkflowService,
		service.NewAgentService,
	} {
		if err := c.Provide(p); err != nil {
			t.Fatalf("container wiring introduced a cycle: %v", err)
		}
	}
}
