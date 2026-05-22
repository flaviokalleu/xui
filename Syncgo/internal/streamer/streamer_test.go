package streamer

import (
	"testing"
)

func TestPlanChunks(t *testing.T) {
	tests := []struct {
		name           string
		from, to       int64
		wantPrefixSkip int64
		wantChunks     []chunkPlan
		wantOutputBytes int64 // sum of useful bytes across all chunks
	}{
		{
			name:            "aligned start, single chunk",
			from:            0,
			to:              4095,
			wantPrefixSkip:  0,
			wantChunks:      []chunkPlan{{0, 4096}},
			wantOutputBytes: 4096,
		},
		{
			name:            "unaligned start, single chunk",
			from:            100,
			to:              10000,
			wantPrefixSkip:  100, // 100 % 4096 = 100
			wantChunks:      []chunkPlan{{0, 12288}}, // ceil((10000-0+1)/4096)*4096 = ceil(10001/4096)*4096 = 3*4096=12288
			wantOutputBytes: 10000 - 100 + 1,
		},
		{
			name:            "aligned start, exactly 1 MiB",
			from:            0,
			to:              maxChunk - 1,
			wantPrefixSkip:  0,
			wantChunks:      []chunkPlan{{0, maxChunk}},
			wantOutputBytes: maxChunk,
		},
		{
			name:           "spans two 1-MiB boundaries",
			from:           0,
			to:             2*maxChunk - 1,
			wantPrefixSkip: 0,
			wantChunks: []chunkPlan{
				{0, maxChunk},
				{maxChunk, maxChunk},
			},
			wantOutputBytes: 2 * maxChunk,
		},
		{
			name:            "unaligned start, spans two chunks",
			from:            100,
			to:              2_000_000,
			wantPrefixSkip:  100,
			wantChunks:      nil, // checked structurally below
			wantOutputBytes: 2_000_000 - 100 + 1,
		},
		{
			name:            "single byte at aligned position",
			from:            4096,
			to:              4096,
			wantPrefixSkip:  0,
			wantChunks:      []chunkPlan{{4096, 4096}},
			wantOutputBytes: 1,
		},
		{
			name:            "single byte at unaligned position",
			from:            5000,
			to:              5000,
			wantPrefixSkip:  5000 - 4096, // = 904
			wantOutputBytes: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan, prefixSkip := planChunks(tt.from, tt.to)

			if prefixSkip != tt.wantPrefixSkip {
				t.Errorf("prefixSkip = %d, want %d", prefixSkip, tt.wantPrefixSkip)
			}

			// Verify chunk alignment invariants.
			for i, cp := range plan {
				if cp.offset%chunkAlign != 0 {
					t.Errorf("chunk[%d] offset %d not aligned to %d", i, cp.offset, chunkAlign)
				}
				if cp.limit%chunkAlign != 0 {
					t.Errorf("chunk[%d] limit %d not aligned to %d", i, cp.limit, chunkAlign)
				}
				if cp.limit > maxChunk {
					t.Errorf("chunk[%d] limit %d exceeds maxChunk %d", i, cp.limit, maxChunk)
				}
				// Must not cross a 1 MiB boundary.
				boundary := (cp.offset/maxChunk + 1) * maxChunk
				if cp.offset+cp.limit > boundary {
					t.Errorf("chunk[%d] (offset=%d limit=%d) crosses 1MiB boundary at %d",
						i, cp.offset, cp.limit, boundary)
				}
			}

			// Verify chunks are contiguous.
			for i := 1; i < len(plan); i++ {
				if plan[i].offset != plan[i-1].offset+plan[i-1].limit {
					t.Errorf("chunk[%d] offset %d not contiguous with chunk[%d] end %d",
						i, plan[i].offset, i-1, plan[i-1].offset+plan[i-1].limit)
				}
			}

			// Verify exact chunk list when specified.
			if tt.wantChunks != nil {
				if len(plan) != len(tt.wantChunks) {
					t.Errorf("len(plan) = %d, want %d: %+v", len(plan), len(tt.wantChunks), plan)
				} else {
					for i := range plan {
						if plan[i] != tt.wantChunks[i] {
							t.Errorf("plan[%d] = %+v, want %+v", i, plan[i], tt.wantChunks[i])
						}
					}
				}
			}

			// Verify total useful bytes matches expected output.
			outputBytes := int64(0)
			skip := prefixSkip
			for _, cp := range plan {
				contribute := cp.limit - skip
				skip = 0
				outputBytes += contribute
			}
			// outputBytes >= wantOutputBytes (last chunk may have padding bytes at the end)
			if outputBytes < tt.wantOutputBytes {
				t.Errorf("plan covers %d bytes, need at least %d", outputBytes, tt.wantOutputBytes)
			}
		})
	}
}

func TestPlanChunksMultiMiB(t *testing.T) {
	// 10 MiB range starting at an unaligned offset.
	from := int64(500)
	to := int64(10*1024*1024 + 500 - 1)
	plan, prefixSkip := planChunks(from, to)

	if prefixSkip != 500 {
		t.Errorf("prefixSkip = %d, want 500", prefixSkip)
	}
	// Should be 11 chunks: 10 full 1MiB chunks + 1 small chunk (500 bytes rounded up).
	if len(plan) < 10 || len(plan) > 12 {
		t.Errorf("expected ~11 chunks, got %d", len(plan))
	}
	for i, cp := range plan {
		if cp.offset%chunkAlign != 0 {
			t.Errorf("chunk[%d] offset not aligned", i)
		}
		if cp.limit > maxChunk {
			t.Errorf("chunk[%d] limit %d exceeds 1 MiB", i, cp.limit)
		}
		boundary := (cp.offset/maxChunk + 1) * maxChunk
		if cp.offset+cp.limit > boundary {
			t.Errorf("chunk[%d] crosses 1MiB boundary", i)
		}
	}
}
