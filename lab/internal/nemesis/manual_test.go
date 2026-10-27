package nemesis

import (
	"context"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"centavo/lab/internal/manual"
)

const manualWithin = 3 * time.Second

func TestManual_ACutFromThePanelIsAppliedRecordedAndClosedByTheCure(t *testing.T) {
	bot, historyPath := newTestNemesis(t, t.TempDir())
	fake := &fakeToxiproxy{}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	bot.toxiproxyURL = server.URL
	bot.config.CommandsDir = filepath.Join(t.TempDir(), manual.DirName)

	ctx, cancel := context.WithCancel(t.Context())

	err := bot.openManual(ctx)
	if err != nil {
		t.Fatal(err)
	}

	err = manual.Send(bot.config.CommandsDir, manual.Command{Action: manual.Cut, Target: "api_pg"})
	if err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(manualWithin)
	for len(callsOf(fake)) < 2 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}

	calls := callsOf(fake)
	isCut := len(calls) == 2 && calls[0] == "POST /proxies/api_pg/toxics" && calls[1] == "POST /proxies/api_pg/toxics"
	if !isCut {
		t.Fatalf("toxiproxy calls %v", calls)
	}

	bot.closeManual(ctx)
	cancel()
	bot.wg.Wait()

	content, err := os.ReadFile(historyPath)
	if err != nil {
		t.Fatal(err)
	}

	isRecorded := strings.Contains(string(content), `"proxies":["api_pg"]`) && strings.Contains(string(content), `"via":"manual"`)
	if !isRecorded {
		t.Fatalf("history:\n%s", content)
	}

	err = manual.Send(bot.config.CommandsDir, manual.Command{Action: manual.Cut, Target: "api_pg"})
	if !errors.Is(err, manual.ErrClosed) {
		t.Fatalf("after the cure: %v, want ErrClosed", err)
	}
}

func callsOf(fake *fakeToxiproxy) []string {
	fake.mu.Lock()
	defer fake.mu.Unlock()

	return append([]string(nil), fake.calls...)
}
