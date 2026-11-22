package drivetest

import (
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"testing"
)

// Closing any httptest server drops every idle connection of
// http.DefaultTransport, and a parallel test whose request was about to
// reuse one fails with "CloseIdleConnections called". A client on its
// own server's transport keeps its connections through that.
func TestClientKeepsItsConnectionsWhenAnotherServerCloses(t *testing.T) {
	s := New()
	defer s.Close()
	c := Client(t, s)
	if _, err := c.About(t.Context()); err != nil {
		t.Fatalf("first About: %v", err)
	}

	other := httptest.NewServer(http.NotFoundHandler())
	other.Close()

	reused := false
	ctx := httptrace.WithClientTrace(t.Context(), &httptrace.ClientTrace{
		GotConn: func(info httptrace.GotConnInfo) { reused = info.Reused },
	})
	if _, err := c.About(ctx); err != nil {
		t.Fatalf("second About: %v", err)
	}
	if !reused {
		t.Error("the second About dialed anew: another server's Close dropped this client's idle connection")
	}
}
