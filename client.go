package floorplan

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// State is the state of an entity.
type State struct {
	ID    string
	State *bool
}

// Client is a home assistant client.
type Client struct {
	baseURL *url.URL
	token   string
	client  *http.Client
}

// NewClient returns a home assistant client.
func NewClient(baseURL *url.URL, token string) Client {
	return Client{
		baseURL: baseURL,
		token:   token,
		client:  &http.Client{},
	}
}

type eventState struct {
	EntityID string `json:"entity_id"`
	State    string `json:"state"`
}

func stateToBool(state string) *bool {
	switch {
	case state == "on" || state == "open":
		return boolPtr(true)
	case state == "off" || state == "closed":
		return boolPtr(false)
	}

	return nil
}

func boolPtr(b bool) *bool {
	return &b
}

// States returns the states of all entities.
func (c Client) States(ctx context.Context) ([]State, error) {
	u, err := c.baseURL.Parse("api/states")
	if err != nil {
		return nil, fmt.Errorf("parsing state url: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sending request: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("unexpected response code: %d", resp.StatusCode)
	}

	var eventStates []eventState
	err = json.NewDecoder(resp.Body).Decode(&eventStates)
	if err != nil {
		return nil, fmt.Errorf("parsing response data: %w", err)
	}

	states := make([]State, 0, len(eventStates))
	for _, state := range eventStates {
		states = append(states, State{
			ID:    state.EntityID,
			State: stateToBool(state.State),
		})
	}

	return states, nil
}

type eventChanged struct {
	EventType string `json:"event_type"`
	Data      struct {
		NewState eventState `json:"new_state"`
	} `json:"data"`
}

// Events returns a stream of state changes.
func (c Client) Events(ctx context.Context, done chan struct{}) (<-chan State, error) {
	u, err := c.baseURL.Parse("api/stream")
	if err != nil {
		return nil, fmt.Errorf("parsing state url: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.client.Do(req) //nolint: bodyclose
	if err != nil {
		return nil, fmt.Errorf("sending request: %w", err)
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("unexpected response code: %d", resp.StatusCode)
	}

	ch := make(chan State, 10)
	go func() {
		defer func() {
			_ = resp.Body.Close()

			close(ch)
		}()

		buf := bufio.NewReader(resp.Body)
		for {
			line, err := buf.ReadBytes('\n')
			if err != nil {
				return
			}

			if len(line) < 6 || string(line[:6]) != "data: " {
				continue
			}

			if string(line[6:]) == "ping\n" {
				continue
			}

			var event eventChanged
			err = json.Unmarshal(line[6:], &event)
			if err != nil {
				return
			}

			if event.EventType != "state_changed" {
				continue
			}

			ch <- State{
				ID:    event.Data.NewState.EntityID,
				State: stateToBool(event.Data.NewState.State),
			}
		}
	}()

	return ch, nil
}
