package sourceresolver

import (
	"github.com/pivotal/kpack/pkg/apis/build/v1alpha2"
	"time"
)

type workQueueEnqueuer struct {
	enqueueAfter func(obj interface{}, after time.Duration)
	delay        time.Duration
}

func (e *workQueueEnqueuer) Enqueue(sr *v1alpha2.SourceResolver) error {
	e.enqueueAfter(sr, 1*time.Minute)
	return nil
}
