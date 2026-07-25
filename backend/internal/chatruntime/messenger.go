package chatruntime

import (
	"context"
	"errors"

	"actweave/backend/internal/agentrun"
	"actweave/backend/internal/chat"
)

// Messenger decorates chat.Service so a successful SendMessage enqueues async
// run execution without changing the HTTP contract (202 + runId).
// Runtime is agentrun.Runtime (production: factory → chatruntimebridge).
type Messenger struct {
	messages *chat.Service
	runtime  agentrun.Runtime
}

func NewMessenger(messages *chat.Service, runtime agentrun.Runtime) (*Messenger, error) {
	if messages == nil || runtime == nil {
		return nil, errors.New("chat runtime messenger requires message service and runtime")
	}
	return &Messenger{messages: messages, runtime: runtime}, nil
}

func (m *Messenger) SendMessage(
	ctx context.Context,
	input chat.SendMessageInput,
) (chat.SendMessageResult, error) {
	result, err := m.messages.SendMessage(ctx, input)
	if err != nil {
		return chat.SendMessageResult{}, err
	}
	m.runtime.Enqueue(Job{
		WorkspaceID: input.WorkspaceID, SessionID: input.SessionID,
		RunID: input.RunID, UserMessageID: result.Message.ID, ActorID: input.CreatedBy,
	})
	return result, nil
}
