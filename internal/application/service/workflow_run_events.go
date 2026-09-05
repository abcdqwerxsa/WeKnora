package service

import (
	"sync"

	"github.com/Tencent/WeKnora/internal/types"
)

// workflowRunBroker fans out per-run progress frames to SSE subscribers.
//
// Why not the global event.EventBus directly: the bus's Off(type) removes
// ALL handlers of a type, so two concurrent SSE clients on the same event
// type would disconnect each other. This broker keys subscriptions by
// run id; the global bus is still notified (observability fan-out) by the
// service alongside broker publication.
//
// Single-instance only: broker state is process-local. A multi-instance
// deployment needs a redis pubsub bridge in front (deliberately out of
// scope — see the SSE endpoint comment).
type workflowRunBroker struct {
	mu   sync.Mutex
	subs map[string][]chan types.WorkflowRunEvent
}

func newWorkflowRunBroker() *workflowRunBroker {
	return &workflowRunBroker{subs: make(map[string][]chan types.WorkflowRunEvent)}
}

// brokerChanSize bounds one subscriber's in-flight frames. Publishers never
// block: a slow SSE client drops intermediate node frames (terminal frames
// are always delivered — see publishTerminal).
const brokerChanSize = 64

// subscribe attaches a feed to runID. The cancel func detaches it and
// closes the channel (idempotent, safe for defer). A channel already
// closed by publishTerminal is simply gone from the map — cancel then
// no-ops instead of double-closing (the SSE endpoint defers cancel after
// receiving the terminal frame, so this path runs on every completed
// stream).
func (b *workflowRunBroker) subscribe(runID string) (<-chan types.WorkflowRunEvent, func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	ch := make(chan types.WorkflowRunEvent, brokerChanSize)
	b.subs[runID] = append(b.subs[runID], ch)
	var once sync.Once
	cancel := func() {
		once.Do(func() {
			b.mu.Lock()
			defer b.mu.Unlock()
			list := b.subs[runID]
			for i, c := range list {
				if c == ch {
					b.subs[runID] = append(list[:i], list[i+1:]...)
					close(ch)
					return
				}
			}
			// Not in the list: publishTerminal already closed this channel.
		})
	}
	return ch, cancel
}

// publish delivers ev to every subscriber of its run. Non-blocking: a full
// subscriber drops the frame (SSE is best-effort progress; the run row is
// the durable record).
func (b *workflowRunBroker) publish(ev types.WorkflowRunEvent) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, ch := range b.subs[ev.RunID] {
		select {
		case ch <- ev:
		default:
		}
	}
}

// publishTerminal delivers the terminal frame with a blocking send (so no
// subscriber misses the run's outcome within brokerChanSize backlog) and
// then closes every subscriber channel of that run. It must be the last
// publish for the run.
func (b *workflowRunBroker) publishTerminal(ev types.WorkflowRunEvent) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, ch := range b.subs[ev.RunID] {
		select {
		case ch <- ev:
		default:
			// Backlog full: close still signals termination; the run row
			// carries the authoritative terminal state.
		}
		close(ch)
	}
	delete(b.subs, ev.RunID)
}
