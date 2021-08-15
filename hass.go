package floorplan

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/glasslabs/looking-glass/module/types"
)

const htmlWrapper = `<div class="hass-floorplan">%s</div>`

// Config is the module configuration.
type Config struct {
	URL       string            `yaml:"url"`
	Token     string            `yaml:"token"`
	Floorplan string            `yaml:"floorplan"`
	CustomCSS string            `yaml:"customCss"`
	Mapping   map[string]string `yaml:"mapping"`
}

// NewConfig creates a default configuration for the module.
func NewConfig() *Config {
	return &Config{}
}

// Module is a clock module.
type Module struct {
	name string
	cfg  *Config
	ui   types.UI
	log  types.Logger

	wg      sync.WaitGroup
	stateCh chan State

	done chan struct{}
}

// New returns a running clock module.
func New(_ context.Context, cfg *Config, info types.Info, ui types.UI) (io.Closer, error) {
	m := &Module{
		name:    info.Name,
		cfg:     cfg,
		ui:      ui,
		log:     info.Log,
		stateCh: make(chan State, 100),
		done:    make(chan struct{}),
	}

	cssPath := filepath.Join(info.Path, "assets/style.css")
	if cfg.CustomCSS != "" {
		cssPath = cfg.CustomCSS
	}
	css, err := os.ReadFile(filepath.Clean(cssPath))
	if err != nil {
		return nil, fmt.Errorf("hass-floorplan: could not read css: %w", err)
	}
	if err = m.ui.LoadCSS(string(css)); err != nil {
		return nil, err
	}

	svg, err := os.ReadFile(filepath.Clean(cfg.Floorplan))
	if err != nil {
		return nil, fmt.Errorf("hass-floorplan: could not read floorplan image: %w", err)
	}
	if err = ui.LoadHTML(fmt.Sprintf(htmlWrapper, string(svg))); err != nil {
		return nil, err
	}

	u, err := url.Parse(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("hass-floorplan: invalid home assistant url: %w", err)
	}
	c := NewClient(u, cfg.Token)

	go m.run()

	m.wg.Add(1)
	go m.listen(c)

	return m, nil
}

func (m *Module) run() {
	for state := range m.stateCh {
		m.updateState(state)
	}
}

func (m *Module) listen(c Client) {
	defer m.wg.Done()

	m.log.Info("fetching state data", "module", "hass-floorpan", "id", m.name)

	m.syncStates(c)

	for {
		// Check for exit before trying to reconnect.
		select {
		case <-m.done:
			return
		default:
		}

		m.log.Info("listening for event data", "module", "hass-floorpan", "id", m.name)

		ch, err := c.Events(context.Background(), m.done)
		if err != nil {
			m.log.Error("error listening to events", "module", "hass-floorpan", "id", m.name, "error", err)

			go m.syncStates(c)

			select {
			case <-m.done:
				return
			case <-time.After(10 * time.Second):
			}

			m.log.Info("reconnecting to event stream", "module", "hass-floorpan", "id", m.name)
			continue
		}

		m.readStates(ch)

		select {
		case <-m.done:
			return
		case <-time.After(10 * time.Second):
		}

		m.log.Info("reconnecting to event stream", "module", "hass-floorpan", "id", m.name)
	}
}

func (m *Module) readStates(ch <-chan State) {
	for {
		select {
		case <-m.done:
			return
		case state, ok := <-ch:
			if !ok {
				return
			}

			m.stateCh <- state
		}
	}
}

func (m *Module) syncStates(c Client) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	states, err := c.States(ctx)
	if err != nil {
		m.log.Error("error fetching to states", "module", "hass-floorpan", "id", m.name, "error", err)
		return
	}

	for _, state := range states {
		m.stateCh <- state
	}
}

func (m *Module) updateState(s State) {
	const docSelector = "document.querySelector('#%s svg').getElementById('%s')"

	id := s.ID
	if mapping, ok := m.cfg.Mapping[id]; ok {
		id = mapping
	}

	// Check if the object actually exists.
	v, err := m.ui.Eval(docSelector, m.name, id)
	if err != nil {
		m.log.Error("could not find object", "module", "hass-floorpan", "id", m.name, "error", err.Error())
	}
	if v == nil {
		return
	}

	state := "unavailable"
	if s.State != nil {
		state = "off"
		if *s.State {
			state = "on"
		}
	}

	_, err = m.ui.Eval(docSelector+".classList.remove('on', 'off', 'unavailable')", m.name, id)
	if err != nil {
		m.log.Error("could not update state", "module", "hass-floorpan", "id", m.name, "error", err.Error())
	}

	_, err = m.ui.Eval(docSelector+".classList.add('%s')", m.name, id, state)
	if err != nil {
		m.log.Error("could not update state", "module", "hass-floorpan", "id", m.name, "error", err.Error())
	}
}

// Close stops and closes the module.
func (m *Module) Close() error {
	close(m.done)
	m.wg.Wait()

	close(m.stateCh)

	return nil
}
