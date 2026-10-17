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

func scheduledNetworkFault(fault Fault, r rawFault) (Fault, error) {
	if r.Schedule.Type != SchedulePoisson {
		return Fault{}, fmt.Errorf("schedule type %q is not implemented (known: %s)", r.Schedule.Type, SchedulePoisson)
	}

	mean, err := time.ParseDuration(r.Schedule.MeanInterval)
	if err != nil || mean <= 0 {
		return Fault{}, errors.New("a scheduled network fault needs a positive mean_interval")
	}

	shortest, errMin := time.ParseDuration(r.Duration.Min)
	longest, errMax := time.ParseDuration(r.Duration.Max)

	isValidWindow := errMin == nil && errMax == nil && shortest > 0 && longest >= shortest
	if !isValidWindow {
		return Fault{}, errors.New("a scheduled network fault needs duration {min, max} with 0 < min <= max")
	}

	fault.Schedule = Schedule{Type: r.Schedule.Type, MeanInterval: mean}
	fault.Window = Window{Min: shortest, Max: longest}

	return fault, nil
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
