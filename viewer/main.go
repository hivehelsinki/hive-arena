package main

import (
	"fmt"
	"image/color"
	"math"
	"slices"
	"strings"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
	"github.com/hajimehoshi/ebiten/v2/text/v2"

	"github.com/hivehelsinki/hive-arena/arena"
)

const Dx = 32
const Dy = 16

// Size of the tile images, and the highest an entity is drawn above its tile
const TileSize = 32
const MaxEntityOffset = 12

var PlayerColors = []color.Color{
	color.RGBA{255, 100, 100, 255},
	color.RGBA{255, 255, 100, 255},
	color.RGBA{100, 255, 100, 255},
	color.RGBA{100, 255, 255, 255},
	color.RGBA{100, 100, 255, 255},
	color.RGBA{255, 100, 255, 255},
}

func playerColor(player int) color.Color {
	return PlayerColors[player%len(PlayerColors)]
}

// playerName works even when the player names are unknown, e.g. while a live game is loading
func (viewer *Viewer) playerName(player int) string {
	if player < len(viewer.Game.Players) {
		return viewer.Game.Players[player]
	}
	return "?"
}

// Turns per second when playing, by default (ebiten runs 60 ticks per second)
const DefaultSpeed = 6

const ZoomFactor = 1.5
const MinScale = 0.1
const MaxScale = 20

type Viewer struct {
	Game *arena.PersistedGame
	Turn int

	Cx, Cy float64
	Scale  float64

	Live <-chan LiveUpdate

	// Size of the screen, as given by Layout (the window size is not
	// available in the browser)
	Width, Height int

	// The camera fits the whole map, again when the screen is resized, until
	// the user zooms or moves it
	Manual              bool
	fitWidth, fitHeight int

	// Autoplay: Speed turns per second; Progress accumulates the fractions
	// of a turn between ticks
	Playing  bool
	Speed    float64
	Progress float64
}

func (viewer *Viewer) Update() error {

	// A mouse wheel notch zooms by ZoomFactor, trackpads send smaller steps
	_, dy := ebiten.Wheel()
	zoom := max(-1, min(1, dy))
	if inpututil.IsKeyJustPressed(ebiten.KeyW) {
		zoom = 1
	} else if inpututil.IsKeyJustPressed(ebiten.KeyQ) {
		zoom = -1
	}
	viewer.Scale = max(MinScale, min(MaxScale, viewer.Scale*math.Pow(ZoomFactor, zoom)))
	if zoom != 0 {
		viewer.Manual = true
	}

	if inpututil.IsMouseButtonJustPressed(ebiten.MouseButtonLeft) {
		viewer.Manual = true
		x, y := ebiten.CursorPosition()
		m := viewer.CoordsToTransform(arena.Coords{Row: 0, Col: 0})
		m.Invert()
		tx, ty := m.Apply(float64(x), float64(y))
		viewer.Cx = tx / Dx * 2
		viewer.Cy = ty / Dy
	}

	// Toggle Autoplay with Space
	if inpututil.IsKeyJustPressed(ebiten.KeySpace) {
		viewer.Playing = !viewer.Playing
		viewer.Progress = 1 // the next turn right away
	}

	// Run Autoplay
	if viewer.Playing {
		viewer.Progress += viewer.Speed / 60
		for viewer.Playing && viewer.Progress >= 1 {
			viewer.Progress--
			if viewer.Turn < len(viewer.Game.History)-1 {
				viewer.Turn++
			} else {
				viewer.Playing = false
				replayEnded()
			}
		}
	}

	//add modifying how far left/right takes you: shift+arrow moves 10 turns, ctrl+arrow moves 50 turns
	step := 1
	if ebiten.IsKeyPressed(ebiten.KeyShift) || ebiten.IsKeyPressed(ebiten.KeyShiftLeft) || ebiten.IsKeyPressed(ebiten.KeyShiftRight) {
		step = 10
	}
	if ebiten.IsKeyPressed(ebiten.KeyControl) || ebiten.IsKeyPressed(ebiten.KeyControlLeft) || ebiten.IsKeyPressed(ebiten.KeyControlRight) ||
		ebiten.IsKeyPressed(ebiten.KeyAlt) || ebiten.IsKeyPressed(ebiten.KeyAltLeft) || ebiten.IsKeyPressed(ebiten.KeyAltRight) {
		step = 50
	}
	//use modified step length, clamp to maximum len - 1
	if inpututil.IsKeyJustPressed(ebiten.KeyRight) {
		viewer.Turn = min(viewer.Turn+step, len(viewer.Game.History)-1)
	}
	//use modified step length, clamp to minimum 0
	if inpututil.IsKeyJustPressed(ebiten.KeyLeft) {
		viewer.Turn = max(viewer.Turn-step, 0)
	}

	if inpututil.IsKeyJustPressed(ebiten.KeyUp) {
		viewer.Turn = 0
	}

	if inpututil.IsKeyJustPressed(ebiten.KeyDown) {
		viewer.Turn = len(viewer.Game.History) - 1
	}

	viewer.applyLiveUpdates()

	if !viewer.Manual && len(viewer.Game.History) > 0 && viewer.Width > 0 &&
		(viewer.Width != viewer.fitWidth || viewer.Height != viewer.fitHeight) {
		viewer.Fit()
		viewer.fitWidth, viewer.fitHeight = viewer.Width, viewer.Height
	}

	return nil
}

func (viewer *Viewer) applyLiveUpdates() {
	for {
		select {
		case update := <-viewer.Live:
			viewer.Game.History = append(viewer.Game.History, arena.Turn{Orders: nil, State: update.State})
			if viewer.Turn == len(viewer.Game.History)-2 {
				viewer.Turn++
			}

			if update.Info != nil {
				viewer.Game.Players = update.Info.Players
				viewer.Game.Map = update.Info.Map
				viewer.Game.CreatedDate = update.Info.CreatedDate
			}
		default:
			return
		}
	}
}

type CoordHex struct {
	Coords arena.Coords
	Hex    *arena.Hex
}

func (viewer *Viewer) CoordsToTransform(coords arena.Coords) ebiten.GeoM {
	m := ebiten.GeoM{}
	w, h := viewer.Width, viewer.Height

	m.Translate(
		float64(Dx*coords.Col/2-Dx/2)-Dx*viewer.Cx/2,
		float64(Dy*coords.Row-Dy/2)-Dy*viewer.Cy,
	)
	m.Scale(viewer.Scale, viewer.Scale)
	m.Translate(float64(w)/2, float64(h)/2)

	return m
}

func (viewer *Viewer) DrawState(screen *ebiten.Image) {
	state := viewer.Game.History[viewer.Turn].State

	hexes := []CoordHex{}
	for coords, hex := range state.Hexes {
		hexes = append(hexes, CoordHex{coords, hex})
	}
	slices.SortFunc(hexes, func(a, b CoordHex) int {
		return a.Coords.Row - b.Coords.Row
	})

	for _, hex := range hexes {
		opt := ebiten.DrawImageOptions{}
		opt.GeoM = viewer.CoordsToTransform(hex.Coords)

		if hex.Hex.Terrain == arena.FIELD && hex.Hex.Resources == 0 {
			screen.DrawImage(EmptyFieldTile, &opt)
		} else {
			screen.DrawImage(TerrainTiles[hex.Hex.Terrain], &opt)
		}
	}

	for _, hex := range hexes {
		entity := hex.Hex.Entity
		if entity == nil {
			continue
		}

		opt := ebiten.DrawImageOptions{}
		opt.GeoM = viewer.CoordsToTransform(hex.Coords)
		opt.GeoM.Translate(0, -EntityOffset[entity.Type]*viewer.Scale)
		opt.ColorScale.ScaleWithColor(playerColor(entity.Player))
		screen.DrawImage(EntityTiles[entity.Type], &opt)

		if entity.HasFlower {
			opt.ColorScale.Reset()
			screen.DrawImage(FlowerImage, &opt)
		}
	}

	viewer.DrawInfo(screen, state)
}

func (viewer *Viewer) DrawInfo(screen *ebiten.Image, state *arena.GameState) {
	txtOp := &text.DrawOptions{}
	txtOp.GeoM.Translate(LineHeight/2, LineHeight/2)

	text.Draw(screen, fmt.Sprintf("%s (%s) %v",
		viewer.Game.Id,
		viewer.Game.Map,
		viewer.Game.CreatedDate),
		Font, txtOp)

	txtOp.GeoM.Translate(0, LineHeight)
	text.Draw(screen, fmt.Sprintf("Turn: %d", state.Turn), Font, txtOp)

	for i, resources := range state.PlayerResources {
		txtOp.GeoM.Translate(0, LineHeight)
		txtOp.ColorScale.Reset()
		txtOp.ColorScale.ScaleWithColor(playerColor(i))
		text.Draw(screen, fmt.Sprintf("Player %d: %s (%d flowers)", i, viewer.playerName(i), resources), Font, txtOp)
	}

	txtOp.ColorScale.Reset()
	txtOp.GeoM.Translate(0, LineHeight)
	text.Draw(screen, fmt.Sprintf("Game over: %v", state.GameOver), Font, txtOp)

	if state.GameOver {
		txtOp.GeoM.Translate(0, LineHeight)
		var winners []string
		for _, winnerId := range state.Winners {
			winners = append(winners, viewer.playerName(winnerId))
		}
		txt := "Winner: "
		if len(winners) > 1 {
			txt = "Winners: "
		}
		text.Draw(screen, txt+strings.Join(winners, ", "), Font, txtOp)
	}
}

func (viewer *Viewer) Draw(screen *ebiten.Image) {
	if len(viewer.Game.History) > 0 {
		viewer.DrawState(screen)
	} else {
		txtOp := &text.DrawOptions{}
		txtOp.GeoM.Translate(LineHeight/2, LineHeight/2)
		text.Draw(screen, fmt.Sprintf("%s has not started yet", viewer.Game.Id), Font, txtOp)
	}
}

func (viewer *Viewer) Layout(outsideWidth, outsideHeight int) (screenWidth, screenHeight int) {
	viewer.Width, viewer.Height = outsideWidth, outsideHeight
	return outsideWidth, outsideHeight
}

// Fit centers the camera on the map, and zooms to the largest zoom step (a
// power of ZoomFactor) at which the whole map is on the screen
func (viewer *Viewer) Fit() {
	minX, minY := math.Inf(1), math.Inf(1)
	maxX, maxY := math.Inf(-1), math.Inf(-1)
	for coords := range viewer.Game.History[0].State.Hexes {
		// Where CoordsToTransform puts the tile, before the camera
		x := float64(Dx*coords.Col/2 - Dx/2)
		y := float64(Dy*coords.Row - Dy/2)
		minX, maxX = min(minX, x), max(maxX, x+TileSize)
		minY, maxY = min(minY, y-MaxEntityOffset), max(maxY, y+TileSize)
	}
	if minX > maxX {
		return
	}

	viewer.Cx = (minX + maxX) / Dx
	viewer.Cy = (minY + maxY) / 2 / Dy

	fit := min(float64(viewer.Width)/(maxX-minX), float64(viewer.Height)/(maxY-minY))
	// (with a margin for rounding, when the map fits exactly)
	steps := math.Floor(math.Log(fit)/math.Log(ZoomFactor) + 1e-9)
	viewer.Scale = max(MinScale, min(MaxScale, math.Pow(ZoomFactor, steps)))
}

func main() {
	args := readArgs()

	var game *arena.PersistedGame
	var live <-chan LiveUpdate
	if args.URL != "" {
		game = GetURL(args.URL)
	} else if args.File != "" {
		game = GetFile(args.File)
	} else if args.Host != "" && args.GameID != "" && args.Token != "" {
		game, live = StartLiveWatch(args.Host, args.GameID, args.Token)
	} else {
		printUsage()
		return
	}

	if game == nil {
		return
	}

	LoadResources()

	ebiten.SetWindowSize(1024, 768)
	ebiten.SetWindowTitle("Hive Arena Viewer")
	ebiten.SetWindowResizingMode(ebiten.WindowResizingModeEnabled)

	viewer := &Viewer{
		Game:    game,
		Scale:   1.0,
		Live:    live,
		Playing: args.Autoplay,
		Speed:   args.Speed,
	}
	if viewer.Speed <= 0 {
		viewer.Speed = DefaultSpeed
	}
	err := ebiten.RunGame(viewer)

	if err != nil {
		fmt.Println(err)
	}
}
