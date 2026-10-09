package office

import (
	"bytes"
	"fmt"
	"io"
)

// RangeReader is an io.ReaderAt over a file that is fetched a byte
// range at a time, for a file that sits behind HTTP rather than on
// disk. It keeps a few recent blocks, so the small reads archive/zip
// makes cost one fetch per block rather than one each; and when reads
// run on from where the last fetch ended, it fetches further ahead each
// time, so a long part costs a handful of requests rather than hundreds.
//
// Nothing is written to disk and the file is never held whole: what it
// keeps is at most cacheBlocks blocks, each copied out of the run it
// came in, so a block kept does not keep the rest of its run with it.
type RangeReader struct {
	size  int64
	fetch func(off, n int64) ([]byte, error)
	// budget is what may still be fetched, in bytes.
	budget int64

	blocks map[int64][]byte
	// order is the blocks by when they were last used, oldest first.
	order []int64
	// next is the block a read carrying on from the last fetch would
	// want, and run is how many blocks that fetch will take.
	next int64
	run  int64

	requests int
	fetched  int64
	err      error
}

// Block sizes. A part of a slide or a style sheet fits in one block; a
// sheet of a million cells is a run of them, fetched at up to two
// megabytes a request.
const (
	blockSize   = 64 << 10
	maxRun      = 32
	cacheBlocks = 48
	// DefaultFetchBudget bounds what one extraction fetches.
	DefaultFetchBudget = 64 << 20
)

// ErrFetchLimit stops a read that has fetched its budget.
var ErrFetchLimit = &Error{Limit: true, Reason: fmt.Sprintf(
	"reading it needs more than %d MB of the file, which is more than this server fetches for one read",
	DefaultFetchBudget>>20)}

// NewRangeReader reads a file of the given size through fetch, which
// returns the n bytes at off. It fetches at most budget bytes in all;
// zero means DefaultFetchBudget.
func NewRangeReader(size int64, fetch func(off, n int64) ([]byte, error), budget int64) *RangeReader {
	if budget <= 0 {
		budget = DefaultFetchBudget
	}
	return &RangeReader{size: size, fetch: fetch, budget: budget, blocks: map[int64][]byte{}, run: 1}
}

// ReadAt reads len(p) bytes at off, fetching what it does not hold.
func (r *RangeReader) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, fmt.Errorf("negative offset %d", off)
	}
	n := 0
	for n < len(p) && off < r.size {
		b, err := r.block(off / blockSize)
		if err != nil {
			return n, err
		}
		c := copy(p[n:], b[off%blockSize:])
		n += c
		off += int64(c)
	}
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

// block returns one block, fetching it, and the run ahead of it when
// reads have been carrying on, when it is not held.
func (r *RangeReader) block(i int64) ([]byte, error) {
	if b, ok := r.blocks[i]; ok {
		r.touch(i)
		return b, nil
	}
	if r.err != nil {
		return nil, r.err
	}
	if i == r.next {
		r.run = min(r.run*2, maxRun)
	} else {
		r.run = 1
	}
	off := i * blockSize
	n := min(r.run*blockSize, r.size-off)
	if n > r.budget {
		r.err = ErrFetchLimit
		return nil, r.err
	}
	data, err := r.fetch(off, n)
	r.requests++
	if err == nil && int64(len(data)) != n {
		err = fmt.Errorf("asked for %d bytes at %d and got %d", n, off, len(data))
	}
	if err != nil {
		r.err = err
		return nil, err
	}
	r.budget -= n
	r.fetched += n
	for j := int64(0); j*blockSize < n; j++ {
		r.blocks[i+j] = bytes.Clone(data[j*blockSize : min((j+1)*blockSize, n)])
		r.touch(i + j)
	}
	r.next = i + (n+blockSize-1)/blockSize
	return r.blocks[i], nil
}

// touch marks a block as just used, and lets the oldest go once more
// than cacheBlocks are held.
func (r *RangeReader) touch(i int64) {
	for k, b := range r.order {
		if b == i {
			r.order = append(r.order[:k], r.order[k+1:]...)
			break
		}
	}
	r.order = append(r.order, i)
	for len(r.order) > cacheBlocks {
		delete(r.blocks, r.order[0])
		r.order = r.order[1:]
	}
}

// Err is the first failure to fetch, which is the reason to give when a
// read fails: archive/zip and encoding/xml report it in their own words,
// or not at all.
func (r *RangeReader) Err() error { return r.err }

// Requests and Fetched say what the reads cost.
func (r *RangeReader) Requests() int { return r.requests }

// Fetched is the bytes fetched in all.
func (r *RangeReader) Fetched() int64 { return r.fetched }
