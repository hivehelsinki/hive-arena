package main

import (
	"flag"
	"fmt"
	"image/color"
	"math"
	"slices"
	"strings"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
	"github.com/hajimehoshi/ebiten/v2/text/v2"

	. "hive-arena/common"
)

const Dx = 32
const Dy = 16

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

const AutoplaySpeed = 10 // ebiten runs 60 ticks per second, so 6 turns per second

const ZoomFactor = 1.5
const MinScale = 0.1
const MaxScale = 20

type Viewer struct {
	Game *PersistedGame
	Turn int

	Cx, Cy float64
	Scale  float64

	Live <-chan LiveUpdate

	// Autoplay
	Playing   bool
	PlayTimer int
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

	if inpututil.IsMouseButtonJustPressed(ebiten.MouseButtonLeft) {
		x, y := ebiten.CursorPosition()
		m := viewer.CoordsToTransform(Coords{Row: 0, Col: 0})
		m.Invert()
		tx, ty := m.Apply(float64(x), float64(y))
		viewer.Cx = tx / Dx * 2
		viewer.Cy = ty / Dy
	}

	// Toggle Autoplay with Space
	if inpututil.IsKeyJustPressed(ebiten.KeySpace) {
		viewer.Playing = !viewer.Playing
		viewer.PlayTimer = 0
	}

	// Run Autoplay
	if viewer.Playing {
		viewer.PlayTimer--
		if viewer.PlayTimer <= 0 {
			if viewer.Turn < len(viewer.Game.History)-1 {
				viewer.Turn++
				viewer.PlayTimer = AutoplaySpeed
			} else {
				viewer.Playing = false
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

	return nil
}

func (viewer *Viewer) applyLiveUpdates() {
	for {
		select {
		case update := <-viewer.Live:
			viewer.Game.History = append(viewer.Game.History, Turn{Orders: nil, State: update.State})
			if len(viewer.Game.History) == 1 {
				viewer.CenterCamera()
			}
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
	Coords Coords
	Hex    *Hex
}

func (viewer *Viewer) CoordsToTransform(coords Coords) ebiten.GeoM {
	m := ebiten.GeoM{}
	w, h := ebiten.WindowSize()

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

		if hex.Hex.Terrain == FIELD && hex.Hex.Resources == 0 {
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

func (viewer *Viewer) DrawInfo(screen *ebiten.Image, state *GameState) {
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
	return outsideWidth, outsideHeight
}

func (viewer *Viewer) CenterCamera() {
	cx, cy := CenterTile(viewer.Game.History[0].State)
	viewer.Cx, viewer.Cy = float64(cx), float64(cy)
}

func CenterTile(state *GameState) (int, int) {
	cx, cy := 0, 0
	count := 0
	for coords, _ := range state.Hexes {
		cx += coords.Col
		cy += coords.Row
		count++
	}
	return cx / count, cy / count
}

func main() {
	url := flag.String("url", "", "URL of the history file to view")
	file := flag.String("file", "", "path to the history file to view")
	host := flag.String("host", "", "host for the live game to watch")
	gameId := flag.String("id", "", "ID of the live game to watch")
	token := flag.String("token", "", "access token for the live game to watch")
	flag.Parse()

	var game *PersistedGame
	var live <-chan LiveUpdate
	if *url != "" {
		game = GetURL(*url)
	} else if *file != "" {
		game = GetFile(*file)
	} else if *host != "" && *gameId != "" && *token != "" {
		game, live = StartLiveWatch(*host, *gameId, *token)

	} else {
		flag.PrintDefaults()
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
		Game:  game,
		Scale: 1.0,
		Live:  live,
	}
	if len(game.History) > 0 {
		viewer.CenterCamera()
	}
	err := ebiten.RunGame(viewer)

	if err != nil {
		fmt.Println(err)
	}
}
