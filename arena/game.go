package arena

import (
	"fmt"
	"math/rand"
	"slices"
)

// The tunable values of the game. They are part of the game state, so that
// agents can read them.
type Rules struct {
	// Flowers in a field at the start, and at most
	FieldFlowers uint `json:"fieldFlowers"`
	// A field with flowers left regains one every this many turns, up to
	// FieldFlowers. An emptied field stays empty. 0: fields do not regrow.
	RegrowInterval uint `json:"regrowInterval"`

	BeeCost  uint `json:"beeCost"`
	HiveCost uint `json:"hiveCost"`
	WallCost uint `json:"wallCost"`

	// Chances that an attack destroys a wall, and stuns a bee (a stunned bee
	// carrying a flower loses it to its attacker)
	WallAttackChance float64 `json:"wallAttackChance"`
	StunChance       float64 `json:"stunChance"`

	// Players see the hexes this close to their hives and bees
	FieldOfView int `json:"fieldOfView"`
	// The game ends after this many turns (0: no limit)
	MaxTurns uint `json:"maxTurns"`
}

func DefaultRules() Rules {
	return Rules{
		FieldFlowers:     8,
		RegrowInterval:   20,
		BeeCost:          6,
		HiveCost:         12,
		WallCost:         1,
		WallAttackChance: 1.0 / 6.0,
		StunChance:       1.0 / 2.0,
		FieldOfView:      4,
		MaxTurns:         1000,
	}
}

type Entity struct {
	Type      EntityType `json:"type"`
	Player    int        `json:"player"`
	HasFlower bool       `json:"hasFlower,omitzero"`
}

type EntityType string

const (
	WALL EntityType = "WALL"
	HIVE EntityType = "HIVE"
	BEE  EntityType = "BEE"
)

type Hex struct {
	Terrain   Terrain `json:"terrain"`
	Resources uint    `json:"resources,omitzero"`
	Entity    *Entity `json:"entity,omitempty"`
}

type Order struct {
	Type      OrderType   `json:"type"`
	Player    int         `json:"player"`
	Coords    Coords      `json:"coords"`
	Direction Direction   `json:"direction"`
	Status    OrderStatus `json:"status"`
}

type OrderType string

const (
	MOVE       OrderType = "MOVE"
	ATTACK     OrderType = "ATTACK"
	BUILD_WALL OrderType = "BUILD_WALL"
	BUILD_HIVE OrderType = "BUILD_HIVE"
	FORAGE     OrderType = "FORAGE"
	SPAWN      OrderType = "SPAWN"
)

type OrderStatus string

const (
	PENDING              OrderStatus = "PENDING"
	INVALID_ORDER        OrderStatus = "INVALID_ORDER"
	INVALID_UNIT         OrderStatus = "INVALID_UNIT"
	BLOCKED              OrderStatus = "BLOCKED"
	INVALID_TARGET       OrderStatus = "INVALID_TARGET"
	CANNOT_FORAGE        OrderStatus = "CANNOT_FORAGE"
	NOT_ENOUGH_RESOURCES OrderStatus = "NOT_ENOUGH_RESOURCES"
	UNIT_ALREADY_ACTED   OrderStatus = "UNIT_ALREADY_ACTED"
	UNIT_STUNNED         OrderStatus = "UNIT_STUNNED"
	OK                   OrderStatus = "OK"
)

func (o *Order) UnitType() EntityType {
	if o.Type == SPAWN {
		return HIVE
	}
	return BEE
}

// IsValid reports whether the order has a known type, and a valid direction if its type needs one
func (o *Order) IsValid() bool {
	switch o.Type {
	case MOVE, ATTACK, BUILD_WALL, SPAWN:
		_, ok := DirectionToOffset[o.Direction]
		return ok
	case FORAGE, BUILD_HIVE:
		return true
	}
	return false
}

func (o *Order) Target() Coords {
	return o.Coords.Neighbour(o.Direction)
}

type GameState struct {
	NumPlayers      int             `json:"numPlayers"`
	Turn            uint            `json:"turn"`
	Rules           Rules           `json:"rules"`
	Hexes           map[Coords]*Hex `json:"hexes"`
	PlayerResources []uint          `json:"playerResources"`

	Winners  []int `json:"winners,omitempty"`
	GameOver bool  `json:"gameOver"`

	stunned map[*Entity]bool
	rng     *rand.Rand
}

var playerMappings = [][]int{
	{},
	{0, -1, -1, -1, -1, -1},
	{0, -1, -1, 1, -1, -1},
	{0, -1, 1, -1, 2, -1},
	{-1, 0, 1, -1, 2, 3},
	{0, 1, 2, 3, 4, -1},
	{0, 1, 2, 3, 4, 5},
}

const MaxPlayers = 6

func IsValidNumPlayers(n int) bool {
	return n >= 1 && n <= MaxPlayers
}

// NewGameState starts a game with the default rules
func NewGameState(mapData MapData, numPlayers int) *GameState {
	return NewGame(mapData, numPlayers, DefaultRules(), rand.Int63())
}

// NewGame starts a game. The seed decides the random outcomes of the game
// (the order of simultaneous orders, attacks).
func NewGame(mapData MapData, numPlayers int, rules Rules, seed int64) *GameState {

	if !IsValidNumPlayers(numPlayers) {
		return nil
	}

	gs := &GameState{
		NumPlayers: numPlayers,
		Rules:      rules,
		Hexes:      make(map[Coords]*Hex),
		rng:        rand.New(rand.NewSource(seed)),
	}

	for coords, terrain := range mapData.Map {
		gs.Hexes[coords] = &Hex{Terrain: terrain}
	}

	for _, spawn := range mapData.Spawns {
		player := spawn.Player
		if mapData.Players == 0 {
			player = playerMappings[numPlayers][spawn.Player]
		} else if mapData.Players != numPlayers {
			return nil
		}
		if player == -1 {
			continue
		}

		switch spawn.Kind {
		case HIVE:
			gs.Hexes[spawn.Coords].Entity = &Entity{Type: HIVE, Player: player}
		case BEE:
			gs.Hexes[spawn.Coords].Entity = &Entity{Type: BEE, Player: player}
		}
	}

	for _, hex := range gs.Hexes {
		if hex.Terrain == FIELD {
			hex.Resources = rules.FieldFlowers
		}
	}

	gs.PlayerResources = make([]uint, numPlayers)
	gs.checkEndGame()

	return gs
}

func (gs *GameState) EntityAt(coords Coords) *Entity {
	hex, ok := gs.Hexes[coords]
	if !ok {
		return nil
	}
	return hex.Entity
}

func (gs *GameState) ProcessOrders(orders [][]*Order) ([]*Order, error) {
	if gs.GameOver {
		return nil, fmt.Errorf("cannot process orders in a finished game")
	}

	// Drop null orders, fill in player ids and reset statuses

	orders = slices.Clone(orders)
	for player, playerOrders := range orders {
		playerOrders = slices.DeleteFunc(slices.Clone(playerOrders), func(o *Order) bool { return o == nil })
		for _, order := range playerOrders {
			order.Player = player
			order.Status = PENDING
		}
		orders[player] = playerOrders
	}

	// Count how many rounds there will be (the length of the longer player orders list)

	numRounds := 0
	for _, playerOrders := range orders {
		if len(playerOrders) > numRounds {
			numRounds = len(playerOrders)
		}
	}

	// A state decoded from JSON has no generator yet
	if gs.rng == nil {
		gs.rng = rand.New(rand.NewSource(rand.Int63()))
	}

	acted := make(map[*Entity]bool)
	gs.stunned = make(map[*Entity]bool)
	var processed []*Order

	// Process round by round

	for roundNumber := range numRounds {
		roundOrders := []*Order{}

		// Gather orders for this round

		for _, playerOrders := range orders {
			if roundNumber < len(playerOrders) {
				roundOrders = append(roundOrders, playerOrders[roundNumber])
			}
		}

		// Shuffle them

		gs.rng.Shuffle(len(roundOrders), func(i, j int) {
			roundOrders[i], roundOrders[j] = roundOrders[j], roundOrders[i]
		})

		// Apply them

		for _, order := range roundOrders {
			processed = append(processed, order)
			unit := gs.EntityAt(order.Coords)
			if !order.IsValid() {
				order.Status = INVALID_ORDER
			} else if unit == nil || unit.Type != order.UnitType() || unit.Player != order.Player {
				order.Status = INVALID_UNIT
			} else if acted[unit] {
				order.Status = UNIT_ALREADY_ACTED
			} else if gs.stunned[unit] {
				order.Status = UNIT_STUNNED
			} else {
				gs.applyOrder(order, unit)
				acted[unit] = true
			}
		}
	}

	gs.Turn++
	gs.regrow()
	gs.checkEndGame()

	return processed, nil
}

func (gs *GameState) applyOrder(order *Order, unit *Entity) {
	switch order.Type {
	case MOVE:
		gs.applyMoveOrder(order, unit)
	case ATTACK:
		gs.applyAttackOrder(order)
	case BUILD_WALL:
		gs.applyBuildWallOrder(order)
	case BUILD_HIVE:
		gs.applyBuildHiveOrder(order)
	case FORAGE:
		gs.applyForageOrder(order, unit)
	case SPAWN:
		gs.applySpawnOrder(order)
	}
}

func (gs *GameState) TargetIsBlocked(order *Order) bool {

	hex := gs.Hexes[order.Target()]
	if hex == nil || !hex.Terrain.IsWalkable() || hex.Entity != nil {
		order.Status = BLOCKED
		return true
	}
	return false
}

func (gs *GameState) tryToPay(order *Order, cost uint) bool {
	if gs.PlayerResources[order.Player] < cost {
		order.Status = NOT_ENOUGH_RESOURCES
		return false
	}
	gs.PlayerResources[order.Player] -= cost
	return true
}

func (gs *GameState) applyMoveOrder(order *Order, bee *Entity) {
	if gs.TargetIsBlocked(order) {
		return
	}

	gs.Hexes[order.Coords].Entity = nil
	gs.Hexes[order.Target()].Entity = bee

	order.Status = OK
}

func (gs *GameState) applyAttackOrder(order *Order) {
	entity := gs.EntityAt(order.Target())
	if entity == nil {
		order.Status = INVALID_TARGET
		return
	}

	if entity.Type == WALL && gs.rng.Float64() < gs.Rules.WallAttackChance {
		gs.Hexes[order.Target()].Entity = nil
	}

	if entity.Type == BEE && gs.rng.Float64() < gs.Rules.StunChance {
		gs.stunned[entity] = true
		// A stunned bee loses its flower, to its attacker if it can carry it
		if entity.HasFlower {
			entity.HasFlower = false
			if attacker := gs.EntityAt(order.Coords); !attacker.HasFlower {
				attacker.HasFlower = true
			}
		}
	}

	order.Status = OK
}

func (gs *GameState) applyBuildWallOrder(order *Order) {
	if gs.TargetIsBlocked(order) {
		return
	}
	if !gs.tryToPay(order, gs.Rules.WallCost) {
		return
	}

	wall := &Entity{Type: WALL, Player: order.Player}
	gs.Hexes[order.Target()].Entity = wall

	order.Status = OK
}

func (gs *GameState) applyBuildHiveOrder(order *Order) {
	if !gs.tryToPay(order, gs.Rules.HiveCost) {
		return
	}

	hive := &Entity{Type: HIVE, Player: order.Player}
	gs.Hexes[order.Coords].Entity = hive

	order.Status = OK
}

func (gs *GameState) applyForageOrder(order *Order, bee *Entity) {
	if bee.HasFlower {

		for _, n := range order.Coords.Neighbours() {
			entity := gs.EntityAt(n)
			if entity != nil && entity.Type == HIVE && entity.Player == bee.Player {
				bee.HasFlower = false
				gs.PlayerResources[order.Player]++

				order.Status = OK
				return
			}
		}

		order.Status = CANNOT_FORAGE

	} else {
		hex := gs.Hexes[order.Coords]
		if hex.Terrain != FIELD || hex.Resources == 0 {
			order.Status = CANNOT_FORAGE
			return
		}

		hex.Resources--
		bee.HasFlower = true

		order.Status = OK
	}
}

func (gs *GameState) applySpawnOrder(order *Order) {
	if gs.TargetIsBlocked(order) {
		return
	}
	if !gs.tryToPay(order, gs.Rules.BeeCost) {
		return
	}

	bee := &Entity{Type: BEE, Player: order.Player}
	gs.Hexes[order.Target()].Entity = bee

	order.Status = OK
}

// regrow adds a flower to the fields that have flowers left, every
// RegrowInterval turns
func (gs *GameState) regrow() {
	if gs.Rules.RegrowInterval == 0 || gs.Turn%gs.Rules.RegrowInterval != 0 {
		return
	}
	for _, hex := range gs.Hexes {
		if hex.Terrain == FIELD && hex.Resources > 0 && hex.Resources < gs.Rules.FieldFlowers {
			hex.Resources++
		}
	}
}

func (gs *GameState) checkEndGame() {

	// No resources left

	var resourcesLeft uint
	for _, hex := range gs.Hexes {
		resourcesLeft += hex.Resources
		if hex.Entity != nil && hex.Entity.HasFlower {
			resourcesLeft++
		}
	}

	if resourcesLeft == 0 {
		gs.GameOver = true
	}

	// Turn limit

	if gs.Rules.MaxTurns > 0 && gs.Turn >= gs.Rules.MaxTurns {
		gs.GameOver = true
	}

	// Determine winners

	if gs.GameOver {
		maxResources := slices.Max(gs.PlayerResources)
		for player, resources := range gs.PlayerResources {
			if resources == maxResources {
				gs.Winners = append(gs.Winners, player)
			}
		}
	}
}

func (gs *GameState) PlayerView(player int) *GameState {
	view := &GameState{
		NumPlayers: gs.NumPlayers,
		Turn:       gs.Turn,
		Rules:      gs.Rules,
		Hexes:      make(map[Coords]*Hex),
		Winners:    slices.Clone(gs.Winners),
		GameOver:   gs.GameOver,
	}

	var ownEntities []Coords
	for coords, hex := range gs.Hexes {
		if hex.Entity != nil && hex.Entity.Player == player {
			ownEntities = append(ownEntities, coords)
		}
	}

	for coords, hex := range gs.Hexes {
		visible := slices.ContainsFunc(ownEntities, func(entity Coords) bool {
			return entity.Distance(coords) <= gs.Rules.FieldOfView
		})
		if visible {
			view.Hexes[coords] = hex.clone()
		}
	}

	resources := slices.Clone(gs.PlayerResources)
	for i := range resources {
		if i != player {
			resources[i] = 0
		}
	}
	view.PlayerResources = resources

	return view
}

func (hex *Hex) clone() *Hex {
	copy := *hex
	if hex.Entity != nil {
		entity := *hex.Entity
		copy.Entity = &entity
	}
	return &copy
}

func (gs *GameState) Clone() *GameState {
	hexes := make(map[Coords]*Hex)
	for k, v := range gs.Hexes {
		hexes[k] = v.clone()
	}

	return &GameState{
		NumPlayers:      gs.NumPlayers,
		Turn:            gs.Turn,
		Rules:           gs.Rules,
		Hexes:           hexes,
		PlayerResources: slices.Clone(gs.PlayerResources),
		Winners:         slices.Clone(gs.Winners),
		GameOver:        gs.GameOver,
		rng:             gs.rng,
	}
}
