package scenarios

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"
)

// The fields of a toxic that are not its attributes.
const (
	toxicTypeField     = "type"
	toxicStreamField   = "stream"
	toxicToxicityField = "toxicity"
)

func validateNetworkFault(fault Fault, r rawFault) (Fault, error) {
	err := validateTargets(r.Proxies, networkLinks)
	if err != nil {
		return Fault{}, fmt.Errorf("proxies: %w", err)
	}

	toxic, err := toxicOf(r.Toxic)
	if err != nil {
		return Fault{}, err
	}

	fault.Proxies = slices.Clone(r.Proxies)
	fault.Toxic = toxic

	isScheduled := r.Schedule.Type != ""
	if !isScheduled {
		return fault, nil
	}

	return scheduledNetworkFault(fault, r)
}

// validateDatabaseFault reads an action on a poisson schedule; a freeze
// also needs how long each pause lasts.
func validateDatabaseFault(fault Fault, r rawFault) (Fault, error) {
	if !slices.Contains(databaseActions, r.Action) {
		return Fault{}, fmt.Errorf("database action %q is not one of %s", r.Action, strings.Join(databaseActions, ", "))
	}

	schedule, err := poissonOf(r.Schedule)
	if err != nil {
		return Fault{}, err
	}

	fault.Action = r.Action
	fault.Schedule = schedule

	if r.Action != DatabaseFreeze {
		return fault, nil
	}

	fault.Window, err = windowOf(r.Duration)

	return fault, err
}

func scheduledNetworkFault(fault Fault, r rawFault) (Fault, error) {
	schedule, err := poissonOf(r.Schedule)
	if err != nil {
		return Fault{}, err
	}

	window, err := windowOf(r.Duration)
	if err != nil {
		return Fault{}, err
	}

	fault.Schedule = schedule
	fault.Window = window

	return fault, nil
}

func poissonOf(raw rawSchedule) (Schedule, error) {
	if raw.Type != SchedulePoisson {
		return Schedule{}, fmt.Errorf("schedule type %q is not implemented (known: %s)", raw.Type, SchedulePoisson)
	}

	mean, err := time.ParseDuration(raw.MeanInterval)
	if err != nil || mean <= 0 {
		return Schedule{}, errors.New("a scheduled fault needs a positive mean_interval")
	}

	return Schedule{Type: raw.Type, MeanInterval: mean}, nil
}

func windowOf(raw rawWindow) (Window, error) {
	shortest, errMin := time.ParseDuration(raw.Min)
	longest, errMax := time.ParseDuration(raw.Max)

	isValid := errMin == nil && errMax == nil && shortest > 0 && longest >= shortest
	if !isValid {
		return Window{}, errors.New("an episode needs duration {min, max} with 0 < min <= max")
	}

	return Window{Min: shortest, Max: longest}, nil
}

// toxicOf reads {type, stream, toxicity, ...attributes}.
func toxicOf(raw map[string]any) (Toxic, error) {
	toxicType, _ := raw[toxicTypeField].(string)
	if !slices.Contains(toxicTypes, toxicType) {
		return Toxic{}, fmt.Errorf("toxic type %q is not one of %s", toxicType, strings.Join(toxicTypes, ", "))
	}

	toxic := Toxic{Type: toxicType, Stream: "downstream", Toxicity: 1, Attributes: map[string]int64{}}

	stream, hasStream := raw[toxicStreamField].(string)
	if hasStream {
		toxic.Stream = stream
	}

	if !slices.Contains(toxicStreams, toxic.Stream) {
		return Toxic{}, fmt.Errorf("toxic stream %q is not upstream or downstream", toxic.Stream)
	}

	toxicity, hasToxicity := numberOf(raw[toxicToxicityField])
	if hasToxicity {
		toxic.Toxicity = toxicity
	}

	if toxic.Toxicity <= 0 || toxic.Toxicity > 1 {
		return Toxic{}, errors.New("toxicity must be in (0, 1]")
	}

	for _, name := range slices.Sorted(maps.Keys(raw)) {
		isAttribute := name != toxicTypeField && name != toxicStreamField && name != toxicToxicityField
		if !isAttribute {
			continue
		}

		value, isNumber := numberOf(raw[name])
		if !isNumber || value < 0 {
			return Toxic{}, fmt.Errorf("toxic attribute %q must be a number >= 0", name)
		}

		toxic.Attributes[name] = int64(value)
	}

	return toxic, nil
}

func numberOf(value any) (float64, bool) {
	switch number := value.(type) {
	case int:
		return float64(number), true
	case float64:
		return number, true
	default:
		return 0, false
	}
}
