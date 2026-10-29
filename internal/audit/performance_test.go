package audit

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/ozal-project/ozal/internal/events"
)

func BenchmarkWriterAppendGrouped(b *testing.B) {
	if runtime.GOOS == "windows" {
		b.Skip("durable audit requires owner-only POSIX file modes")
	}
	for _, appenders := range []int{1, 8, 64} {
		b.Run(fmt.Sprintf("appenders_%d", appenders), func(b *testing.B) {
			directory, err := os.MkdirTemp(".", ".audit-append-benchmark-")
			if err != nil {
				b.Fatal(err)
			}
			b.Cleanup(func() { _ = os.RemoveAll(directory) })
			absoluteDirectory, err := filepath.Abs(directory)
			if err != nil {
				b.Fatal(err)
			}
			writer, err := Open(b.Context(), absoluteDirectory, Options{})
			if err != nil {
				b.Fatal(err)
			}
			b.Cleanup(func() {
				if err := writer.Close(); err != nil {
					b.Error(err)
				}
			})

			ctx := b.Context()
			var next atomic.Int64
			start := make(chan struct{})
			errCh := make(chan error, appenders)
			var workers sync.WaitGroup
			workers.Add(appenders)
			for range appenders {
				go func() {
					defer workers.Done()
					<-start
					for {
						if next.Add(1) > int64(b.N) {
							return
						}
						if err := writer.Append(ctx, events.Event{Type: events.AccessAllowed, Host: "app.localhost"}); err != nil {
							errCh <- err
							return
						}
					}
				}()
			}
			b.ReportAllocs()
			b.ResetTimer()
			close(start)
			workers.Wait()
			b.StopTimer()
			close(errCh)
			for err := range errCh {
				b.Fatal(err)
			}
			if err := writer.Close(); err != nil {
				b.Fatal(err)
			}
		})
	}
}
