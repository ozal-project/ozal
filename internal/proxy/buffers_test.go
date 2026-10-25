package proxy

import "testing"

func TestResponseBufferPoolClearsResponseBytesBeforeReuse(t *testing.T) {
	pool := newResponseBufferPool()
	buffer := make([]byte, proxyBufferSize, proxyBufferSize+16)
	full := buffer[:cap(buffer)]
	for i := range full {
		full[i] = 0xa5
	}
	pool.Put(buffer)
	for i, value := range full {
		if value != 0 {
			t.Fatalf("buffer byte %d = %#x after Put, want cleared", i, value)
		}
	}
	reused := pool.Get()
	if &reused[0] != &buffer[0] {
		t.Fatal("pool did not return the cleared buffer")
	}
}
