// Copyright 2019 Matt Layher
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package corerad

import (
	"context"
	"io"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/mdlayher/corerad/internal/config"
)

func TestServerBuildTasks(t *testing.T) {
	t.Parallel()

	// Since each Task potentially encapsulates a lot of internal state, we
	// just verify the stringified version of each Task to ensure that the
	// appropriate Tasks were built based on input Config.
	tests := []struct {
		name string
		cfg  config.Config
		ss   []string
	}{
		{
			name: "empty config",
			ss:   []string{"link state watcher"},
		},
		{
			name: "debug HTTP",
			cfg: config.Config{
				Debug: config.Debug{Address: ":9430"},
			},
			ss: []string{
				`debug HTTP server ":9430"`,
				"link state watcher",
			},
		},
		{
			name: "full",
			cfg: config.Config{
				Interfaces: []config.Interface{
					{Name: "eth0", Monitor: true},
					{Name: "eth1", Advertise: true},
					// Not configured.
					{Name: "eth2"},
				},
				Debug: config.Debug{Address: ":9430"},
			},
			ss: []string{
				`monitor "eth0"`,
				`advertiser "eth1"`,
				`debug HTTP server ":9430"`,
				"link state watcher",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := NewServer(log.New(os.Stderr, "", 0))

			var ss []string
			for _, task := range srv.BuildTasks(tt.cfg) {
				ss = append(ss, task.String())
			}

			if diff := cmp.Diff(tt.ss, ss); diff != "" {
				t.Fatalf("unexpected task strings (-want +got):\n%s", diff)
			}
		})
	}
}

func TestServerServeBasicTasks(t *testing.T) {
	t.Parallel()

	const text = "CoreRAD"
	var (
		// Pick an address that is likely to be unoccupied for the debug HTTP
		// server bind.
		addr = randAddr(t)
		ll   = log.New(os.Stderr, "", 0)
	)

	tests := []struct {
		name  string
		task  Task
		check func(t *testing.T)
	}{
		{
			name: "debug HTTP",
			task: &httpTask{
				addr: addr,
				h: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					_, _ = io.WriteString(w, text)
				}),
			},
			check: func(t *testing.T) {
				res := httpGet(t, addr)
				defer res.Body.Close()

				if diff := cmp.Diff(res.StatusCode, http.StatusOK); diff != "" {
					t.Fatalf("unexpected HTTP status (-want +got):\n%s", diff)
				}

				b, err := ioutil.ReadAll(res.Body)
				if err != nil {
					t.Fatalf("failed to read HTTP body: %v", err)
				}

				if diff := cmp.Diff(text, string(b)); diff != "" {
					t.Fatalf("unexpected HTTP body (-want +got):\n%s", diff)
				}
			},
		},
		{
			name: "watcher not exist",
			task: &watcherTask{
				watch: func(_ context.Context) error {
					return os.ErrNotExist
				},
				ll: ll,
			},
		},
		{
			name: "watcher OK",
			task: &watcherTask{
				watch: func(ctx context.Context) error {
					<-ctx.Done()
					return nil
				},
				ll: ll,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			timer := time.AfterFunc(5*time.Second, func() {
				panic("test took too long")
			})
			defer timer.Stop()

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			// Run the Server until the context is canceled and verify it
			// actually halts.
			var wg sync.WaitGroup
			wg.Add(1)
			defer func() {
				cancel()
				wg.Wait()
			}()

			sigC := make(chan struct{})

			go func() {
				defer wg.Done()
				close(sigC)

				if err := NewServer(ll).Serve(ctx, []Task{tt.task}); err != nil {
					panicf("failed to serve: %v", err)
				}
			}()

			<-sigC
			if tt.check != nil {
				tt.check(t)
			}
		})
	}
}

func httpGet(t *testing.T, addr string) *http.Response {
	t.Helper()

	addr = "http://" + addr
	u, err := url.Parse(addr)
	if err != nil {
		t.Fatalf("failed to parse URL: %v", err)
	}

	c := &http.Client{Timeout: 1 * time.Second}

	for i := 0; i < 5; i++ {
		res, err := c.Get(u.String())
		if err == nil {
			return res
		}

		t.Logf("HTTP GET retry %02d: %v", i, err)
		time.Sleep(250 * time.Millisecond)
	}

	t.Fatal("failed to HTTP GET, ran out of retry attempts")
	return nil
}

func randAddr(t *testing.T) string {
	t.Helper()

	l, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	_ = l.Close()

	return l.Addr().String()
}
