package startupmonitorreadiness

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestDOETCDHealthCheck(t *testing.T) {
	scenarios := []struct {
		name        string
		healthy     bool
		reason      string
		msg         string
		rspWriterFn func(w http.ResponseWriter)
	}{
		{
			name:    "scenario 1: happy path, HTTP 200, empty reason and msg",
			healthy: true,
		},

		{
			name: "scenario 2: HTTP 500, unhealthy reason and msg",
			rspWriterFn: func(w http.ResponseWriter) {
				w.WriteHeader(http.StatusInternalServerError)
				w.Write([]byte("etcd is on fire"))
			},
			healthy: false,
			reason:  "EtcdUnhealthy",
			msg:     "etcd is on fire",
		},
		{
			name: "scenario 3: unexpected err from the server",
			rspWriterFn: func(w http.ResponseWriter) {
				panic("bum")
			},
			healthy: false,
			reason:  "EtcdUnhealthyError",
			// we don't check the entire rsp from the server
			msg: "falied while performing the check due to",
		},
		{
			name: "scenario 4: no rsp from the server",
			rspWriterFn: func(w http.ResponseWriter) {
				time.Sleep(4 * time.Second)
			},
			healthy: false,
			reason:  "EtcdUnhealthyError",
			// we don't check the entire rsp from the server
			msg: "context deadline exceeded (Client.Timeout exceeded while awaiting headers)",
		},
	}

	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			// set up the server
			rspWriterFn := func(w http.ResponseWriter) {
				fmt.Fprintf(w, "ok")
			}
			ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				rspWriterFn(w)
			}))
			ts.EnableHTTP2 = true
			ts.StartTLS()
			defer ts.Close()

			// rewrite rsp handler if provided
			if scenario.rspWriterFn != nil {
				rspWriterFn = scenario.rspWriterFn
			}

			// set the client timeout
			client := ts.Client()
			client.Timeout = 2 * time.Second

			// act and validate
			actualHealthy, actualReason, actualMsg := doETCDHealthCheck(context.TODO(), client, ts.URL)
			if scenario.healthy != actualHealthy {
				t.Errorf("unexpected health condition (healthy=%v), expected healthy=%v", actualHealthy, scenario.healthy)
			}
			if scenario.reason != actualReason {
				t.Errorf("unexpected reason %v, expected %v", actualReason, scenario.reason)
			}
			if !strings.Contains(actualMsg, scenario.msg) {
				t.Errorf("unexpected message %v, expected %v", actualMsg, scenario.msg)
			}
		})
	}
}
