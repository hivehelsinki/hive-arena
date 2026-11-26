package main

import (
	"context"
	"fmt"
	"log"
	"time"
)

// PoolState is what is known about the teams after the last pool
// resolution, for the website and the status
type PoolState struct {
	UpdatedAt time.Time   `json:"updatedAt"`
	Teams     []TeamState `json:"teams"`
	Problems  []string    `json:"problems,omitempty"` // of the registry
}

// PoolResolver resolves the pool of a run: the registered teams, their
// current bot (built if new), minus the bots that keep failing
type PoolResolver struct {
	Registry    RegistryConfig
	DataDir     string
	MaxFailures int // 0: no limit
	Store       *Store
	Builder     *Builder
}

func (p *PoolResolver) Resolve(ctx context.Context) ([]Bot, error) {
	teams, problems, err := LoadTeams(ctx, p.Registry, p.DataDir)
	for _, problem := range problems {
		log.Printf("Registry: %s", problem)
	}
	if err != nil {
		// Keep the previous teams, with the problem
		state, _ := p.Store.PoolState()
		state.Problems = append(problems, err.Error())
		p.Store.SavePoolState(state)
		return nil, fmt.Errorf("reading the registry: %w", err)
	}

	_, states := p.Builder.Pool(ctx, teams)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	bots, err := leaveOutFailing(p.Store, p.MaxFailures, states)
	if err != nil {
		return nil, err
	}

	if err := p.Store.SavePoolState(&PoolState{UpdatedAt: time.Now(), Teams: states, Problems: problems}); err != nil {
		return nil, err
	}
	return bots, nil
}

// leaveOutFailing returns the bots of the teams, except those that failed
// their last maxFailures matches (0: no limit), which are marked as left out
func leaveOutFailing(store *Store, maxFailures int, states []TeamState) ([]Bot, error) {
	var bots []Bot
	for i := range states {
		state := &states[i]
		if state.Bot == nil {
			continue
		}
		if maxFailures > 0 {
			failures, err := store.ConsecutiveFailures(state.Bot.Name, state.Bot.Commit)
			if err != nil {
				return nil, err
			}
			if failures >= maxFailures {
				state.LeftOut = fmt.Sprintf("left out of the runs: failed its last %d matches (crashed or did not join) with this version. Push a new tag to play again.", failures)
				log.Printf("Team %s: %s", state.Team.ID, state.LeftOut)
				continue
			}
		}
		bots = append(bots, *state.Bot)
	}
	return bots, nil
}
