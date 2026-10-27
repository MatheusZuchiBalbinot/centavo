package checker

import (
	"encoding/json"
	"errors"
	"fmt"

	"centavo/lab/internal/history"
)

// ErrMalformedHistory means an operation's arguments or result do not decode
// into what the invariants read: judging the round on the zeros left behind
// would hide or invent violations, so the check stops instead.
var ErrMalformedHistory = errors.New("history value does not decode")

func shapesOf(function history.Function) (args, value any) {
	switch function {
	case history.FunctionCreateAccount:
		return nil, &createAccountValue{}
	case history.FunctionDeposit:
		return &depositArgs{}, &writeValue{}
	case history.FunctionTransfer:
		return &transferArgs{}, &writeValue{}
	case history.FunctionPixOut:
		return &pixOutArgs{}, &writeValue{}
	case history.FunctionPixIn:
		return &pixInArgs{}, nil
	case history.FunctionReadBalance:
		return &readBalanceArgs{}, &readBalanceValue{}
	case history.FunctionReadStatement:
		return nil, &statementValue{}
	case history.FunctionResolve:
		return &resolveArgs{}, &resolveValue{}
	case history.FunctionReadPixOut,
		history.FunctionKill,
		history.FunctionArmFailpoint,
		history.FunctionArmPartner,
		history.FunctionFailpointFired,
		history.FunctionHealStart,
		history.FunctionFreeze,
		history.FunctionFailover,
		history.FunctionDatabase,
		history.FunctionNetwork,
		history.FunctionSkewClock,
		history.FunctionHealComplete,
		history.FunctionProcessState,
		history.FunctionDefense:
		return nil, nil
	default:
		return nil, nil
	}
}

func (ops operations) validate() error {
	for _, o := range ops.ordered {
		arguments, value := shapesOf(o.function)

		err := decodeShape(o.args, arguments)
		if err != nil {
			return fmt.Errorf("%w: %s %s arguments: %w", ErrMalformedHistory, o.op, o.function, err)
		}

		if o.result != history.TypeOK {
			continue
		}

		err = decodeShape(o.value, value)
		if err != nil {
			return fmt.Errorf("%w: %s %s result: %w", ErrMalformedHistory, o.op, o.function, err)
		}
	}

	return nil
}

func decodeShape(raw json.RawMessage, target any) error {
	isAbsent := target == nil || len(raw) == 0
	if isAbsent {
		return nil
	}

	return json.Unmarshal(raw, target)
}

// decodeValidated reads a value validate has already decoded once. An error
// here is a bug in the checker, a type read that validate does not know, not
// a bad history.
func decodeValidated(raw json.RawMessage, target any) {
	err := decodeShape(raw, target)
	if err != nil {
		panic(fmt.Sprintf("checker: decoding a value validate accepted: %v", err))
	}
}
