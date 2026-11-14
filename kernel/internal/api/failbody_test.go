package api

import (
	"context"
	"fmt"
	"net/http"
	"testing"
)

// Card t_0214482d A4: a request that ended while it waited (a reload, a lock) is not answered as a server fault.
func TestFailBodyContextErrors(t *testing.T) {
	for _, c := range []struct {
		err  error
		want int
	}{
		{context.DeadlineExceeded, http.StatusServiceUnavailable},
		{fmt.Errorf("step 2: %w", context.DeadlineExceeded), http.StatusServiceUnavailable},
		{context.Canceled, 499},
		{fmt.Errorf("boom"), http.StatusInternalServerError},
	} {
		if got, _ := failBody(c.err); got != c.want {
			t.Errorf("%v: status %d, want %d", c.err, got, c.want)
		}
	}
}
