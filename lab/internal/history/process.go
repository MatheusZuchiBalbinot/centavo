package history

import (
	"encoding/json"
	"errors"
	"fmt"
)

const nemesisName = "nemesis"

var errInvalidProcess = errors.New(`process must be a client number or "nemesis"`)

// Process is who wrote a record: a logical client (a number) or the nemesis,
// the chaos bot.
type Process struct {
	client    int
	isNemesis bool
}

// Client is the logical client number n.
func Client(number int) Process {
	return Process{client: number}
}

// Nemesis is the chaos bot.
func Nemesis() Process {
	return Process{isNemesis: true}
}

// IsNemesis reports whether the record comes from the chaos bot.
func (p Process) IsNemesis() bool {
	return p.isNemesis
}

// ClientNumber is the client number; it is meaningless for the nemesis.
func (p Process) ClientNumber() int {
	return p.client
}

func (p Process) String() string {
	if p.isNemesis {
		return nemesisName
	}

	return fmt.Sprint(p.client)
}

// MarshalJSON writes a number for clients and "nemesis" for the bot.
func (p Process) MarshalJSON() ([]byte, error) {
	if p.isNemesis {
		return json.Marshal(nemesisName)
	}

	return json.Marshal(p.client)
}

// UnmarshalJSON reads either form.
func (p *Process) UnmarshalJSON(data []byte) error {
	var client int

	err := json.Unmarshal(data, &client)
	if err == nil {
		*p = Client(client)

		return nil
	}

	var name string

	err = json.Unmarshal(data, &name)
	if err != nil {
		return errInvalidProcess
	}

	if name != nemesisName {
		return errInvalidProcess
	}

	*p = Nemesis()

	return nil
}
