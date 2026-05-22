package streamer

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/gotd/td/tg"
)

const (
	// Telegram requires offset and limit aligned to 4 KiB.
	chunkAlign = 4 * 1024
	// Max chunk size per request (Telegram hard limit).
	maxChunk = 1024 * 1024
	// Concurrent Telegram UploadGetFile requests per stream.
	// Higher values increase per-stream throughput at the cost of more API calls.
	prefetchWorkers = 4
)

type Streamer struct {
	api *tg.Client
}

func New(api *tg.Client) *Streamer {
	return &Streamer{api: api}
}

type chunkPlan struct {
	offset int64
	limit  int64
}

// planChunks returns the sequence of (offset, limit) pairs to request from Telegram
// and the number of prefix bytes to discard from the first chunk.
//
// Telegram requires offsets that are multiples of chunkAlign and limits that do
// not cross 1 MiB boundaries, so we round the starting offset down and cap each
// limit at the next 1 MiB boundary.
func planChunks(from, to int64) (plan []chunkPlan, prefixSkip int64) {
	alignedFrom := from - (from % chunkAlign)
	prefixSkip = from - alignedFrom
	offset := alignedFrom
	curSkip := prefixSkip
	remaining := to - from + 1 // bytes we must write to the client

	for remaining > 0 {
		limit := int64(maxChunk)

		// Never cross a 1 MiB boundary in a single request.
		nextBoundary := (offset/maxChunk + 1) * maxChunk
		if offset+limit > nextBoundary {
			limit = nextBoundary - offset
		}

		// Shrink to what we actually need, rounded up to chunkAlign.
		if limit > remaining+curSkip {
			needed := remaining + curSkip
			if r := needed % chunkAlign; r != 0 {
				needed += chunkAlign - r
			}
			if needed < limit {
				limit = needed
			}
		}

		plan = append(plan, chunkPlan{offset, limit})

		// How many output bytes does this chunk contribute?
		contributed := min(limit-curSkip, remaining)
		remaining -= contributed
		offset += limit // advance by the full Telegram-aligned chunk size
		curSkip = 0    // prefix skip only applies to the first chunk
	}
	return plan, prefixSkip
}

type fetchResult struct {
	data []byte
	err  error
}

func (s *Streamer) fetchChunk(ctx context.Context, location tg.InputFileLocationClass, cp chunkPlan) ([]byte, error) {
	res, err := s.api.UploadGetFile(ctx, &tg.UploadGetFileRequest{
		Precise:      true,
		CDNSupported: false,
		Location:     location,
		Offset:       cp.offset,
		Limit:        int(cp.limit),
	})
	if err != nil {
		return nil, fmt.Errorf("upload.getFile (offset=%d limit=%d): %w", cp.offset, cp.limit, err)
	}
	switch f := res.(type) {
	case *tg.UploadFile:
		return f.Bytes, nil
	case *tg.UploadFileCDNRedirect:
		return nil, errors.New("CDN redirect not supported yet")
	default:
		return nil, fmt.Errorf("unexpected upload.getFile response %T", res)
	}
}

// StreamRange writes bytes [from, to] (inclusive) of the file at location to w.
//
// It fetches up to prefetchWorkers Telegram chunks concurrently and writes them
// in order, so the per-stream throughput is roughly prefetchWorkers × the
// bandwidth of a single sequential request. This keeps latency low for all
// concurrent streams without requiring separate bot tokens per stream.
func (s *Streamer) StreamRange(ctx context.Context, location tg.InputFileLocationClass, from, to int64, w io.Writer) error {
	if from < 0 || to < from {
		return fmt.Errorf("invalid range: from=%d to=%d", from, to)
	}

	plan, prefixSkip := planChunks(from, to)

	// One buffered channel per chunk so results arrive in order.
	resultChans := make([]chan fetchResult, len(plan))
	for i := range resultChans {
		resultChans[i] = make(chan fetchResult, 1)
	}

	// gctx is canceled when StreamRange returns, stopping all in-flight fetches.
	gctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Semaphore bounds the number of concurrent Telegram requests.
	sem := make(chan struct{}, prefetchWorkers)

	// Dispatcher: launches one goroutine per chunk, up to prefetchWorkers at a time.
	go func() {
		for i, cp := range plan {
			select {
			case sem <- struct{}{}:
			case <-gctx.Done():
				return
			}
			go func(i int, cp chunkPlan) {
				defer func() { <-sem }()
				data, err := s.fetchChunk(gctx, location, cp)
				resultChans[i] <- fetchResult{data: data, err: err}
			}(i, cp)
		}
	}()

	// Write chunks to the client in order.
	remaining := to - from + 1
	for i, ch := range resultChans {
		if remaining <= 0 {
			break
		}
		var r fetchResult
		select {
		case r = <-ch:
		case <-ctx.Done():
			return ctx.Err()
		}
		if r.err != nil {
			return r.err
		}
		data := r.data
		if len(data) == 0 {
			return nil // EOF from Telegram
		}
		// Discard aligned prefix bytes from the first chunk only.
		if i == 0 && prefixSkip > 0 {
			if int64(len(data)) <= prefixSkip {
				return fmt.Errorf("first chunk too small: %d bytes, need to skip %d", len(data), prefixSkip)
			}
			data = data[prefixSkip:]
		}
		// Truncate to exactly what remains.
		if int64(len(data)) > remaining {
			data = data[:remaining]
		}
		if _, err := w.Write(data); err != nil {
			return err
		}
		remaining -= int64(len(data))
	}
	return nil
}
