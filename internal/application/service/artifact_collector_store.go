package service

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// messageRepoArtifactStore adapts interfaces.MessageRepository to the
// SessionArtifactStore contract expected by ArtifactCollector. It is a thin
// projection: KnownArtifacts is documented as best-effort, so we forward
// repo errors verbatim and let the collector decide how to degrade.
type messageRepoArtifactStore struct {
	repo interfaces.MessageRepository
}

// NewMessageRepoArtifactStore wraps a MessageRepository so ArtifactCollector
// can build its de-duplication set from every prior message of the session.
func NewMessageRepoArtifactStore(repo interfaces.MessageRepository) SessionArtifactStore {
	return &messageRepoArtifactStore{repo: repo}
}

// KnownArtifacts satisfies SessionArtifactStore.
func (s *messageRepoArtifactStore) KnownArtifacts(
	ctx context.Context, sessionID string,
) ([]types.MessageArtifact, error) {
	if s == nil || s.repo == nil || sessionID == "" {
		return nil, nil
	}
	items, err := s.repo.GetSessionArtifacts(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	// The collector only diffs metadata; the owning message id and index it
	// carries are irrelevant here, so flatten back to plain artifacts.
	arts := make([]types.MessageArtifact, 0, len(items))
	for _, it := range items {
		arts = append(arts, it.Artifact)
	}
	return arts, nil
}
