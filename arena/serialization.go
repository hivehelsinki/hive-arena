package arena

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

func (c Coords) String() string {
	return fmt.Sprintf("%d,%d", c.Row, c.Col)
}

func (c *Coords) FromString(s string) error {
	parts := strings.Split(s, ",")
	if len(parts) != 2 {
		return fmt.Errorf("invalid coordinate string format: %s", s)
	}
	row, err := strconv.Atoi(parts[0])
	if err != nil {
		return fmt.Errorf("invalid row value: %w", err)
	}
	col, err := strconv.Atoi(parts[1])
	if err != nil {
		return fmt.Errorf("invalid col value: %w", err)
	}
	c.Row = row
	c.Col = col
	return nil
}

func (c Coords) MarshalText() ([]byte, error) {
	return []byte(c.String()), nil
}

func (c *Coords) UnmarshalText(b []byte) error {
	return c.FromString(string(b))
}

type PersistedGame struct {
	Id          string    `json:"id"`
	Map         string    `json:"map"`
	CreatedDate time.Time `json:"createdDate"`
	Players     []string  `json:"players"`
	History     []Turn    `json:"history"`
}

type Turn struct {
	Orders []*Order   `json:"orders,omitempty"`
	State  *GameState `json:"state"`
}

type SessionStatus struct {
	Id          string    `json:"id"`
	CreatedDate time.Time `json:"createdDate"`
	Map         string    `json:"map"`
	NumPlayers  int       `json:"numPlayers"`
	Players     []string  `json:"players"`
	GameOver    bool      `json:"gameOver"`
}

type StatusResponse struct {
	GitRevision string          `json:"gitRevision"`
	Games       []SessionStatus `json:"games"`
}
