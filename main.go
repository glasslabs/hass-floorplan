//go:build js && wasm

package main

import (
	_ "embed"
	"fmt"
	"strings"
	"time"

	"github.com/glasslabs/client-go"
	"github.com/pawal/go-hass"
)

const htmlWrapper = `<div class="hass-floorplan">%s</div>`

//go:embed assets/style.css
var css []byte

// Config is the module configuration.
type Config struct {
	URL       string            `yaml:"url"`
	Token     string            `yaml:"token"`
	Floorplan string            `yaml:"floorplan"`
	Mapping   map[string]string `yaml:"mapping"`
}

// NewConfig creates a default configuration for the module.
func NewConfig() *Config {
	return &Config{}
}

func main() {
	log := client.NewLogger()
	mod, err := client.NewModule()
	if err != nil {
		log.Error("Could not create module", "error", err.Error())
		return
	}

	cfg := NewConfig()
	if err = mod.ParseConfig(&cfg); err != nil {
		log.Error("Could not parse config", "error", err.Error())
		return
	}

	log.Info("Loading Module", "module", mod.Name())

	m := &Module{
		mod: mod,
		cfg: cfg,
		log: log,
	}

	if err = m.setup(); err != nil {
		log.Error("Could not setup module", "error", err.Error())
		return
	}

	first := true
	for {
		if !first {
			time.Sleep(10 * time.Second)
		}
		first = false

		if err = m.syncStates(); err != nil {
			log.Error("Could not sync states", "error", err.Error())
			continue
		}

		if err = m.listenStates(); err != nil {
			log.Error("Could not listen to states", "error", err.Error())
			continue
		}
	}
}

// Module runs the module.
type Module struct {
	mod *client.Module
	cfg *Config

	ha *hass.Access

	log *client.Logger
}

func (m *Module) setup() error {
	if err := m.mod.LoadCSS(string(css)); err != nil {
		return fmt.Errorf("loading css: %w", err)
	}

	svg, err := m.mod.Asset(m.cfg.Floorplan)
	if err != nil {
		return fmt.Errorf("could not read floorplan image: %w", err)
	}
	m.mod.Element().SetInnerHTML(fmt.Sprintf(htmlWrapper, string(svg)))

	ha := hass.NewAccess(m.cfg.URL, "")
	ha.SetBearerToken(m.cfg.Token)
	if err := ha.CheckAPI(); err != nil {
		return fmt.Errorf("could not connect to home assistant: %w", err)
	}
	m.ha = ha

	return nil
}

func (m *Module) syncStates() error {
	states, err := m.ha.FilterStates("light", "switch", "cover", "binary_sensor")
	if err != nil {
		return fmt.Errorf("getting states: %w", err)
	}

	for _, state := range states {
		m.updateState(state.EntityID, state.State)
	}
	return nil
}

func (m *Module) listenStates() error {
	l, err := m.ha.ListenEvents()
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
		switch strings.TrimSuffix(strings.SplitAfter(event.Data.EntityID, ".")[0], ".") {
		case "light", "switch", "cover", "binary_sensor":
		default:
			continue
		}

		m.updateState(event.Data.EntityID, event.Data.NewState.State)
	}
}

func (m *Module) updateState(id, state string) {
	if mapping, ok := m.cfg.Mapping[id]; ok {
		id = mapping
	}
	id = strings.ReplaceAll(id, ".", "\\.")

	actualState := "unavailable"
	if state != "unavailable" && state != "unknown" {
		actualState = "off"
		if state == "on" || state == "open" {
			actualState = "on"
		}
	}

	elem := m.mod.Element().QuerySelector("#" + id)
	if elem == nil {
		return
	}

	elem.Class().Remove("on")
	elem.Class().Remove("off")
	elem.Class().Remove("unavailable")
	elem.Class().Add(actualState)
}
