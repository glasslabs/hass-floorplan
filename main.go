//go:build wasip1

package main

import (
	"bytes"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/glasslabs/client-go"
	"github.com/pawal/go-hass"
)

const (
	stateOn          = "on"
	stateOpen        = "open"
	stateUnavailable = "unavailable"
	stateUnknown     = "unknown"

	classOn          = "on"
	classUnavailable = "unavailable"
)

// Config is the module configuration.
type Config struct {
	URL       string            `json:"url"`
	Token     string            `json:"token"`
	Floorplan string            `json:"floorplan"`
	Mapping   map[string]string `json:"mapping"`
}

// NewConfig creates a default configuration for the module.
func NewConfig() *Config {
	return &Config{}
}

var (
	cfg *Config
	mod *client.Module

	baseSVG []byte

	log *client.Logger
)

func main() {
	log = client.NewLogger()

	var err error
	mod, err = client.NewModule()
	if err != nil {
		log.Error("Could not create module", "error", err.Error())
		return
	}

	cfg = NewConfig()
	if err = mod.ParseConfig(cfg); err != nil {
		log.Error("Could not parse config", "error", err.Error())
		return
	}

	baseSVG, err = mod.Asset(cfg.Floorplan)
	if err != nil {
		log.Error("Could not read floorplan asset", "error", err.Error())
		return
	}

	log.Info("Module ready", "module", mod.Name())

	render()

	for {
		ha := hass.NewAccess(cfg.URL, "")
		ha.SetBearerToken(cfg.Token)

		if err = ha.CheckAPI(); err != nil {
			log.Error("Could not connect to Home Assistant", "error", err.Error())
			time.Sleep(10 * time.Second)
			continue
		}

		if err = syncStates(ha); err != nil {
			log.Error("Could not sync states", "error", err.Error())
			time.Sleep(10 * time.Second)
			continue
		}

		if err = listenStates(ha); err != nil {
			log.Error("State listener error", "error", err.Error())
		}

		time.Sleep(5 * time.Second)
	}
}

func syncStates(ha *hass.Access) error {
	states, err := ha.FilterStates("light", "switch", "cover", "binary_sensor")
	if err != nil {
		return fmt.Errorf("getting states: %w", err)
	}

	for _, state := range states {
		updateState(state.EntityID, state.State)
	}

	render()
	return nil
}

func listenStates(ha *hass.Access) error {
	l, err := ha.ListenEvents()
	if err != nil {
		return fmt.Errorf("calling listen: %w", err)
	}
	defer func() { _ = l.Close() }()

	for {
		event, err := l.NextStateChanged()
		if err != nil {
			return fmt.Errorf("listening for event: %w", err)
		}

		if event.EventType != "state_changed" {
			continue
		}
		prefix := strings.TrimSuffix(strings.SplitAfter(event.Data.EntityID, ".")[0], ".")
		if !slices.Contains([]string{"light", "switch", "cover", "binary_sensor"}, prefix) {
			continue
		}

		if !updateState(event.Data.EntityID, event.Data.NewState.State) {
			continue
		}
		render()
	}
}

func updateState(id, state string) bool {
	if mapping, ok := cfg.Mapping[id]; ok {
		id = mapping
	}

	if !bytes.Contains(baseSVG, []byte(`id="`+id+`"`)) {
		return false
	}

	var stateClass string
	switch state {
	case stateUnavailable, stateUnknown:
		stateClass = classUnavailable
	case stateOn, stateOpen:
		stateClass = classOn
	}

	baseSVG = setElementStateClass(baseSVG, id, stateClass)
	return true
}

func render() {
	mod.Render(client.NewSVG(string(baseSVG)))
}

// setElementStateClass finds the element with the given id in svg, removes any
// existing state classes ("on", "unavailable") from its class attribute, and
// appends stateClass if non-empty. Returns a new slice; svg is not modified.
func setElementStateClass(svg []byte, id, stateClass string) []byte {
	idx := bytes.Index(svg, []byte(`id="`+id+`"`))
	if idx == -1 {
		return svg
	}

	// Locate the opening '<' of the element tag.
	tagStart := bytes.LastIndexByte(svg[:idx], '<')
	if tagStart == -1 {
		return svg
	}

	// Locate the closing '>' of the element tag.
	relEnd := bytes.IndexByte(svg[idx:], '>')
	if relEnd == -1 {
		return svg
	}
	tagEnd := idx + relEnd

	// Find class="..." within the element tag.
	classPrefix := []byte(`class="`)
	relClass := bytes.Index(svg[tagStart:tagEnd], classPrefix)
	if relClass == -1 {
		return svg
	}
	classStart := tagStart + relClass + len(classPrefix)

	relClose := bytes.IndexByte(svg[classStart:tagEnd], '"')
	if relClose == -1 {
		return svg
	}
	classEnd := classStart + relClose

	// Strip existing state classes and append the new one.
	var classes []string
	for c := range strings.FieldsSeq(string(svg[classStart:classEnd])) {
		if c != classOn && c != classUnavailable {
			classes = append(classes, c)
		}
	}
	if stateClass != "" {
		classes = append(classes, stateClass)
	}
	newVal := strings.Join(classes, " ")

	result := make([]byte, 0, len(svg)+(len(newVal)-(classEnd-classStart)))
	result = append(result, svg[:classStart]...)
	result = append(result, newVal...)
	result = append(result, svg[classEnd:]...)
	return result
}
