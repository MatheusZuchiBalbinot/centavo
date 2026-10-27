package nemesis

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"

	"centavo/lab/scenarios"
)

type fakeToxiproxy struct {
	mu    sync.Mutex
	calls []string
}

func TestNetwork_PartitionsBecomeToxiproxyCalls(t *testing.T) {
	cases := []struct {
		toxic      string
		wantApply  []string
		wantRemove []string
	}{
		{toxic: scenarios.ToxicReject, wantApply: []string{"POST /proxies/worker1_pg"}, wantRemove: []string{"POST /proxies/worker1_pg"}},
		{toxic: scenarios.ToxicBlackhole, wantApply: []string{"POST /proxies/worker1_pg/toxics", "POST /proxies/worker1_pg/toxics"}, wantRemove: []string{"DELETE /proxies/worker1_pg/toxics/cut-up", "DELETE /proxies/worker1_pg/toxics/cut-down"}},
		{toxic: scenarios.ToxicOneWay, wantApply: []string{"POST /proxies/worker1_pg/toxics"}, wantRemove: []string{"DELETE /proxies/worker1_pg/toxics/cut-down"}},
		{toxic: "latency", wantApply: []string{"POST /proxies/worker1_pg/toxics"}, wantRemove: []string{"DELETE /proxies/worker1_pg/toxics/cut"}},
	}

	for _, tc := range cases {
		t.Run(tc.toxic, func(t *testing.T) {
			bot, fake := newNetworkNemesis(t)
			fault := scenarios.Fault{ID: "cut", Kind: scenarios.FaultNetwork, Proxies: []string{"worker1_pg"}, Toxic: scenarios.Toxic{Type: tc.toxic, Stream: "downstream", Toxicity: 1}}

			bot.applyNetwork(t.Context(), fault, "")
			applied := slices.Clone(fake.calls)
			fake.calls = nil
			bot.removeNetwork(t.Context(), fault, "")

			if !slices.Equal(applied, tc.wantApply) || !slices.Equal(fake.calls, tc.wantRemove) {
				t.Fatalf("applied %v, removed %v", applied, fake.calls)
			}
		})
	}
}

func TestNetwork_TheCureResetsToxiproxy(t *testing.T) {
	bot, fake := newNetworkNemesis(t)

	bot.resetNetwork(t.Context())

	if !slices.Equal(fake.calls, []string{"POST /reset"}) {
		t.Fatalf("calls %v", fake.calls)
	}
}

func (f *fakeToxiproxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.calls = append(f.calls, r.Method+" "+r.URL.Path)
	f.mu.Unlock()

	w.WriteHeader(http.StatusOK)
}

func newNetworkNemesis(t *testing.T) (*Nemesis, *fakeToxiproxy) {
	t.Helper()

	bot, _ := newTestNemesis(t, t.TempDir())
	fake := &fakeToxiproxy{}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	bot.toxiproxyURL = server.URL

	return bot, fake
}
