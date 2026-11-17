package arena

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
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
	History     []Turn    `json:"history"` // see WriteJSON before moving it

	// Result is set once the game is over. Older reports do not have it.
	Result *GameResult `json:"result,omitempty"`
}

// WriteJSON writes the game as JSON, one turn at a time: the reports of long
// games are hundreds of MB, and encoding them at once would double the memory
// they take
func (g *PersistedGame) WriteJSON(w io.Writer) error {
	out := bufio.NewWriterSize(w, 1<<16)

	// Everything but the history, which goes last
	head := *g
	head.History = nil
	data, err := json.Marshal(&head)
	if err != nil {
		return err
	}
	data = data[:len(data)-1] // without the closing brace
	data = append(data, []byte(`,"history":[`)...)
	if _, err := out.Write(data); err != nil {
		return err
	}

	for i := range g.History {
		if i > 0 {
			out.WriteByte(',')
		}
		data, err := json.Marshal(&g.History[i])
		if err != nil {
			return err
		}
		if _, err := out.Write(data); err != nil {
			return err
		}
	}
	out.WriteString("]}\n")
	return out.Flush()
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

	// Result is set once the game is over
	Result *GameResult `json:"result,omitempty"`
}

type StatusResponse struct {
	GitRevision string          `json:"gitRevision"`
	Games       []SessionStatus `json:"games"`
}
