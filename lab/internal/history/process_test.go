package history_test

import (
	"encoding/json"
	"strings"
	"testing"

	"centavo/lab/internal/history"
)

func TestProcess_RoundTripsBothForms(t *testing.T) {
	t.Parallel()

	for _, process := range []history.Process{history.Client(7), history.Nemesis()} {
		encoded, err := json.Marshal(process)
		if err != nil {
			t.Fatal(err)
		}

		var decoded history.Process

		err = json.Unmarshal(encoded, &decoded)
		if err != nil || decoded != process {
			t.Fatalf("%s: encoded %s, decoded %v, %v", process, encoded, decoded, err)
		}
	}

	var rejected history.Process
	if json.Unmarshal([]byte(`"lab"`), &rejected) == nil {
		t.Fatal(`accepted process "lab"`)
	}
}

func TestValidate_NemesisRecords(t *testing.T) {
	t.Parallel()

	valid := []string{
		`{"index":1,"round":"r1","process":"nemesis","type":"info","f":"kill","value":{"target":"bank-api"}}`,
		`{"index":2,"round":"r1","process":1,"type":"invoke","f":"deposit","op":"a"}`,
		`{"index":3,"round":"r1","process":"nemesis","type":"info","f":"heal_start"}`,
		`{"index":4,"round":"r1","process":1,"type":"ok","f":"deposit","op":"a"}`,
	}

	report := validate(t, valid)
	if !report.IsWellFormed() || report.Invocations != 1 {
		t.Fatalf("report: %+v", report)
	}

	broken := []string{`{"index":1,"round":"r1","process":"nemesis","type":"invoke","f":"kill","op":"x"}`}

	report = validate(t, broken)
	if report.IsWellFormed() || !strings.Contains(report.Violations[0].Message, "nemesis") {
		t.Fatalf("report: %+v", report)
	}
}
