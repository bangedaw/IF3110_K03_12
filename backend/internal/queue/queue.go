package queue

import "context"

type PublishInput struct {
	RequestID  int64
	DatabaseID int64
	ActorID    int64
}

type Publisher interface {
	Publish(ctx context.Context, in PublishInput) (jobID int64, err error)
}
