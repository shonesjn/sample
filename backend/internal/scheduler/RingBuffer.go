package scheduler

import "sync"

// RingBuffer is a thread-safe circular buffer for Frame objects.
// Uses scheduler.Frame defined in scheduler.go.
type RingBuffer struct {
	buffer []*Frame
	head   int
	tail   int
	size   int
	cap    int
	mu     sync.Mutex
}

// NewRingBuffer creates a new ring buffer with the given capacity.
func NewRingBuffer(capacity int) *RingBuffer {
	return &RingBuffer{
		buffer: make([]*Frame, capacity),
		cap:    capacity,
	}
}

// Push adds a frame to the buffer. Returns false if full.
func (rb *RingBuffer) Push(frame *Frame) bool {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	if rb.size >= rb.cap {
		return false // Buffer full
	}

	rb.buffer[rb.tail] = frame
	rb.tail = (rb.tail + 1) % rb.cap
	rb.size++
	return true
}

// Pop removes and returns the oldest frame. Returns nil if empty.
func (rb *RingBuffer) Pop() *Frame {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	if rb.size == 0 {
		return nil
	}

	frame := rb.buffer[rb.head]
	rb.buffer[rb.head] = nil // Allow GC
	rb.head = (rb.head + 1) % rb.cap
	rb.size--
	return frame
}

// Peek returns the oldest frame without removing it. Returns nil if empty.
func (rb *RingBuffer) Peek() *Frame {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	if rb.size == 0 {
		return nil
	}
	return rb.buffer[rb.head]
}

// Len returns the current number of frames in the buffer.
func (rb *RingBuffer) Len() int {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	return rb.size
}

// Cap returns the buffer capacity.
func (rb *RingBuffer) Cap() int {
	return rb.cap
}

// IsFull returns true if the buffer is at capacity.
func (rb *RingBuffer) IsFull() bool {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	return rb.size >= rb.cap
}

// IsEmpty returns true if the buffer has no frames.
func (rb *RingBuffer) IsEmpty() bool {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	return rb.size == 0
}

// Clear removes all frames from the buffer.
func (rb *RingBuffer) Clear() {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	for i := range rb.buffer {
		rb.buffer[i] = nil
	}
	rb.head = 0
	rb.tail = 0
	rb.size = 0
}

// Drain removes all frames and returns them as a slice.
func (rb *RingBuffer) Drain() []*Frame {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	frames := make([]*Frame, 0, rb.size)
	for rb.size > 0 {
		frames = append(frames, rb.buffer[rb.head])
		rb.buffer[rb.head] = nil
		rb.head = (rb.head + 1) % rb.cap
		rb.size--
	}
	rb.tail = 0
	rb.head = 0
	return frames
}
