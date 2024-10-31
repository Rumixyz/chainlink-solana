package logpoller_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/utils/tests"
	"github.com/smartcontractkit/chainlink-solana/pkg/solana/logpoller"
)

func TestWorkerGroup(t *testing.T) {
	ctx := tests.Context(t)
	group := logpoller.NewWorkerGroup(5, logger.Nop())

	group.Start(ctx)
	t.Cleanup(func() {
		require.NoError(t, group.Close())
	})

	output := make([]int, 10)

	for idx := range output {
		job := testJob{
			job: func(ctx context.Context) error {
				output[idx] = 1

				return nil
			},
		}

		_ = group.Do(ctx, job)
	}

	tests.AssertEventually(t, func() bool {
		return reflect.DeepEqual([]int{1, 1, 1, 1, 1, 1, 1, 1, 1, 1}, output)
	})
}

func TestWorkerGroup_Retry(t *testing.T) {
	ctx := tests.Context(t)
	group := logpoller.NewWorkerGroup(5, logger.Nop())

	group.Start(ctx)
	t.Cleanup(func() {
		require.NoError(t, group.Close())
	})

	type jobResult struct {
		idx int
	}

	var mu sync.RWMutex

	output := make([]int, 10)
	chResults := make(chan jobResult, 1)

	for idx := range output {
		job := &retryJob{
			name: idx,
			job: func(idx int, res chan jobResult) func(context.Context) error {
				return func(ctx context.Context) error {
					chResults <- jobResult{idx: idx}

					return nil
				}
			}(idx, chResults),
		}

		require.NoError(t, group.Do(ctx, job))
	}

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case result := <-chResults:
				mu.Lock()
				output[result.idx] = 1
				mu.Unlock()
			}
		}
	}()

	tests.AssertEventually(t, func() bool {
		mu.RLock()
		defer mu.RUnlock()

		return reflect.DeepEqual([]int{1, 1, 1, 1, 1, 1, 1, 1, 1, 1}, output)
	})
}

func TestWorkerGroup_Close(t *testing.T) {
	ctx := tests.Context(t)
	group := logpoller.NewWorkerGroup(5, logger.Nop())

	group.Start(ctx)

	output := make([]int, 10)

	for idx := range output {
		_ = group.Do(ctx, testJob{job: func(ctx context.Context) error {
			// make one run longer than the wait time
			if idx == 9 {
				timer := time.NewTimer(time.Second)
				defer timer.Stop()

				select {
				case <-ctx.Done():
					// terminating by the context is expected
					output[idx] = 0
				case <-timer.C:
					// indicate that the process terminated by the timer
					output[idx] = 2
				}

				return nil
			}

			output[idx] = 1

			return nil
		}})
	}

	// wait for the first 9 to finish and close the group
	time.Sleep(100 * time.Millisecond)
	group.Close()
	assert.Equal(t, []int{1, 1, 1, 1, 1, 1, 1, 1, 1, 0}, output)
}

func TestWorkerGroup_DoContext(t *testing.T) {
	t.Run("will not add to queue", func(t *testing.T) {
		ctx := tests.Context(t)
		group := logpoller.NewWorkerGroup(2, logger.Nop())
		job := testJob{job: func(ctx context.Context) error { return nil }}

		require.NoError(t, group.Start(ctx))

		t.Run("if input context cancelled", func(t *testing.T) {
			ctx, cancel := context.WithCancel(ctx)

			// calling cancel before calling Do should result in an error
			cancel()

			require.ErrorIs(t, group.Do(ctx, job), logpoller.ErrContextCancelled)
		})

		t.Run("if queue closed", func(t *testing.T) {
			require.NoError(t, group.Close())

			require.ErrorIs(t, group.Do(ctx, job), logpoller.ErrProcessStopped)
		})
	})
}

type testJob struct {
	job func(context.Context) error
}

func (j testJob) String() string {
	return "testJob"
}

func (j testJob) Run(ctx context.Context) error {
	return j.job(ctx)
}

type retryJob struct {
	count uint8
	name  int
	job   func(context.Context) error
}

func (j *retryJob) String() string {
	return fmt.Sprintf("retryJob: %d", j.name)
}

func (j *retryJob) Run(ctx context.Context) error {
	if j.count < 2 {
		j.count++

		return errors.New("retry")
	}

	return j.job(ctx)
}
