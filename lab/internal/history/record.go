// Package history writes and validates history.jsonl, the record of what
// clients asked and what they got back (docs/invariants-and-checker.md,
// section 4).
package history

import "encoding/json"

// Type is the kind of a history record.
type Type string

const (
	TypeInvoke  Type = "invoke"
	TypeAttempt Type = "attempt"
	TypeOK      Type = "ok"
	TypeFail    Type = "fail"
	TypeInfo    Type = "info"
)

// Function is the operation a client performed, or the injection the nemesis made.
type Function string

const (
	FunctionCreateAccount Function = "create_account"
	FunctionDeposit       Function = "deposit"
	FunctionTransfer      Function = "transfer"
	FunctionReadBalance   Function = "read_balance"
	FunctionReadStatement Function = "read_statement"
)

// What the nemesis records: injections, the cure and fired failpoints.
const (
	FunctionKill           Function = "kill"
	FunctionArmFailpoint   Function = "arm_failpoint"
	FunctionFailpointFired Function = "failpoint_fired"
	FunctionHealStart      Function = "heal_start"
	FunctionHealComplete   Function = "heal_complete"
)

// Record is one line of history.jsonl. The writer fills Index, Round, TimeNS
// and Wall; the caller fills the rest.
type Record struct {
	Index    int64           `json:"index"`
	Round    string          `json:"round"`
	TimeNS   int64           `json:"time_ns"`
	Wall     string          `json:"wall"`
	Process  Process         `json:"process"`
	Type     Type            `json:"type"`
	F        Function        `json:"f,omitempty"`
	Op       string          `json:"op,omitempty"`
	N        int             `json:"n,omitempty"`
	Value    json.RawMessage `json:"value,omitempty"`
	Outcome  json.RawMessage `json:"outcome,omitempty"`
	Error    json.RawMessage `json:"error,omitempty"`
	Attempts int             `json:"attempts,omitempty"`
}

// IsCompletion reports whether the record closes an operation.
func (r Record) IsCompletion() bool {
	return r.Type == TypeOK || r.Type == TypeFail || r.Type == TypeInfo
}
