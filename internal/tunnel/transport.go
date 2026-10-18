package tunnel

import (
	"errors"
	"io"
	"net/http"
	"sync"
	"time"
)

// ConnectorConnected reports transport attachment only, not application readiness.
func (e *Edge) ConnectorConnected(name string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	live := e.sessions[name]
	return live != nil && !live.session.IsClosed()
}

// TransportFor opens only streams to this named connector. The URL is a routing
// label, never a network destination. No idle stream survives a session rotation.
func (e *Edge) TransportFor(name string) http.RoundTripper {
	return &connectorTransport{edge: e, transport: &http.Transport{
		DialContext: e.dialer(name), DisableKeepAlives: true,
		ResponseHeaderTimeout: 10 * time.Second,
	}}
}

type connectorTransport struct {
	edge      *Edge
	transport *http.Transport
}

func (t *connectorTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := req.Context().Err(); err != nil {
		return nil, err
	}
	select {
	case t.edge.requestSlots <- struct{}{}:
	default:
		return nil, errors.New("tunnel request capacity reached")
	}
	release := func() { <-t.edge.requestSlots }
	response, err := t.transport.RoundTrip(req)
	if err != nil {
		release()
		return nil, err
	}
	body := &slotBody{ReadCloser: response.Body, release: release}
	if stream, ok := response.Body.(io.ReadWriteCloser); ok {
		response.Body = &upgradedSlotBody{slotBody: body, writer: stream}
	} else {
		response.Body = body
	}
	return response, nil
}

type slotBody struct {
	io.ReadCloser
	once    sync.Once
	release func()
	err     error
}

func (body *slotBody) Close() error {
	body.once.Do(func() { body.err = body.ReadCloser.Close(); body.release() })
	return body.err
}

// Preserve the bidirectional body http.Transport returns for HTTP 101.
// ReverseProxy uses this interface to copy upgraded streams in both directions.
type upgradedSlotBody struct {
	*slotBody
	writer io.Writer
}

func (body *upgradedSlotBody) Write(p []byte) (int, error) { return body.writer.Write(p) }
