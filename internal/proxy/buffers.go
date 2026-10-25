package proxy

const proxyBufferSize = 32 * 1024
const maxPooledResponseBuffers = 32

// responseBufferPool supplies ReverseProxy's body-copy buffer. The buffer is
// cleared before reuse so response bytes from one route are not retained for
// a later request in the process heap. The channel bounds retained memory and
// never blocks a request when the pool is empty or full.
type responseBufferPool struct{ buffers chan []byte }

func newResponseBufferPool() *responseBufferPool {
	return &responseBufferPool{buffers: make(chan []byte, maxPooledResponseBuffers)}
}

func (p *responseBufferPool) Get() []byte {
	select {
	case buffer := <-p.buffers:
		return buffer[:proxyBufferSize]
	default:
		return make([]byte, proxyBufferSize)
	}
}

func (p *responseBufferPool) Put(buffer []byte) {
	if cap(buffer) < proxyBufferSize {
		return
	}
	clear(buffer[:cap(buffer)])
	buffer = buffer[:proxyBufferSize]
	select {
	case p.buffers <- buffer:
	default:
	}
}
