package history

import "encoding/json"

const (
	TypeInvoke  Type = "invoke"
	TypeAttempt Type = "attempt"
	TypeOK      Type = "ok"
	TypeFail    Type = "fail"
	TypeInfo    Type = "info"
)

const (
	FunctionCreateAccount Function = "create_account"
	FunctionDeposit       Function = "deposit"
	FunctionTransfer      Function = "transfer"
	FunctionReadBalance   Function = "read_balance"
	FunctionReadStatement Function = "read_statement"
	FunctionResolve       Function = "resolve"
	FunctionPixOut        Function = "pix_out"
	FunctionPixIn         Function = "pix_in"
	FunctionReadPixOut    Function = "read_pix_out"
)

const (
	FunctionKill           Function = "kill"
	FunctionArmFailpoint   Function = "arm_failpoint"
	FunctionArmPartner     Function = "arm_partner_modes"
	FunctionFailpointFired Function = "failpoint_fired"
	FunctionHealStart      Function = "heal_start"
	FunctionFreeze         Function = "freeze"
	FunctionFailover       Function = "failover"
	FunctionDatabase       Function = "database"
	FunctionNetwork        Function = "network"
	FunctionSkewClock      Function = "skew_clock"
	FunctionHealComplete   Function = "heal_complete"
	// As Docker reports it, each time it changes.
	FunctionProcessState Function = "process_state"
	FunctionDefense      Function = "defense"
)

type Type string

type Function string

// The writer fills Index, Round, TimeNS and Wall; the caller fills the rest.
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

func (r Record) IsCompletion() bool {
	return r.Type == TypeOK || r.Type == TypeFail || r.Type == TypeInfo
}
