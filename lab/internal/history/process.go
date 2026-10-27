package history

import (
	"encoding/json"
	"errors"
	"fmt"
)

const nemesisName = "nemesis"

var errInvalidProcess = errors.New(`process must be a client number or "nemesis"`)

type Process struct {
	client    int
	isNemesis bool
}

func Client(number int) Process {
	return Process{client: number}
}

func Nemesis() Process {
	return Process{isNemesis: true}
}

func (p Process) IsNemesis() bool {
	return p.isNemesis
}

func (p Process) ClientNumber() int {
	return p.client
}

func (p Process) String() string {
	if p.isNemesis {
		return nemesisName
	}

	return fmt.Sprint(p.client)
}

func (p Process) MarshalJSON() ([]byte, error) {
	if p.isNemesis {
		return json.Marshal(nemesisName)
	}

	return json.Marshal(p.client)
}

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
