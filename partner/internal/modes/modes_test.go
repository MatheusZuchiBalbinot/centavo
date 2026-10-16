package modes_test

import (
	"errors"
	"testing"
	"time"

	"centavo/partner/internal/modes"
	"centavo/partner/internal/truth"
)

var now = time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)

func lying(t *testing.T, config modes.Config) *modes.Registry {
	t.Helper()

	registry := modes.NewRegistry()

	config.ParticipantID = "centavo-robust"
	if config.TTL == "" {
		config.TTL = "1m"
	}

	err := registry.Set(config, now)
	if err != nil {
		t.Fatal(err)
	}

	return registry
}

func TestHonestByDefault(t *testing.T) {
	decider := modes.NewRegistry().For("centavo-robust", now)

	_, isDown := decider.Outage()
	isHonest := !isDown && !decider.Reject() && !decider.DoubleProcess() && decider.ReturnAfterSettle() == nil && decider.SettleDelay() == modes.HonestSettleDelay
	if !isHonest {
		t.Fatal("a partner without a mode lied")
	}

	planned := decider.Plan(truth.Event{ID: "evt"}, now)
	if len(planned) != 1 || planned[0].IsDropped || !planned[0].At.Equal(now) {
		t.Fatalf("honest plan: %+v", planned)
	}
}

func TestSameSeedReplaysTheSameLies(t *testing.T) {
	config := modes.Config{Seed: "0x5eed", RejectRate: 0.5}

	draw := func() [32]bool {
		decider := lying(t, config).For("centavo-robust", now)

		var answers [32]bool
		for i := range answers {
			answers[i] = decider.Reject()
		}

		return answers
	}

	first, second := draw(), draw()
	if first != second {
		t.Fatal("the same seed gave different lies")
	}
}

func TestModesExpireWithTheirTTL(t *testing.T) {
	registry := lying(t, modes.Config{RejectRate: 1, TTL: "10s"})

	if !registry.For("centavo-robust", now).Reject() {
		t.Fatal("an active mode did not lie")
	}

	if registry.For("centavo-robust", now.Add(11*time.Second)).Reject() {
		t.Fatal("an expired mode still lies")
	}
}

func TestPlanDropsDuplicatesAndDelays(t *testing.T) {
	registry := lying(t, modes.Config{WebhookDrop: 1, WebhookDuplicate: 1, WebhookDuplicateNewID: 1, WebhookDelayMax: "30s"})

	planned := registry.For("centavo-robust", now).Plan(truth.Event{ID: "evt"}, now)

	isLying := len(planned) == 3 && planned[0].IsDropped && planned[1].EventID == "evt" && planned[2].EventID != "evt"
	if !isLying {
		t.Fatalf("plan: %+v", planned)
	}

	for _, delivery := range planned {
		isWithinDelay := !delivery.At.Before(now) && !delivery.At.After(now.Add(30*time.Second))
		if !isWithinDelay {
			t.Fatalf("delivery at %v is outside the delay window", delivery.At)
		}
	}
}

func TestSet_RejectsInvalidModes(t *testing.T) {
	cases := []modes.Config{
		{ParticipantID: "", TTL: "1m"},
		{ParticipantID: "centavo-robust", TTL: "2h"},
		{ParticipantID: "centavo-robust", TTL: "1m", RejectRate: 1.5},
		{ParticipantID: "centavo-robust", TTL: "1m", Seed: "zz"},
		{ParticipantID: "centavo-robust", TTL: "1m", Outage: &modes.OutageConfig{Until: "+1m", Mode: "maybe"}},
	}

	for _, config := range cases {
		err := modes.NewRegistry().Set(config, now)
		if !errors.Is(err, modes.ErrInvalidMode) {
			t.Errorf("%+v: got %v", config, err)
		}
	}
}
