package main

import (
	"bytes"
	"image"
	_ "image/png"
	"log"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/text/v2"

	"github.com/hivehelsinki/hive-arena/common"

	_ "embed"
)

var TerrainTiles map[common.Terrain]*ebiten.Image
var EmptyFieldTile *ebiten.Image
var FlowerImage *ebiten.Image

var EntityTiles map[common.EntityType]*ebiten.Image
var EntityOffset = map[common.EntityType]float64{
	common.BEE:  8,
	common.HIVE: 12,
	common.WALL: 8,
}

//go:embed tile-empty.png
var TileEmpty []byte

//go:embed tile-rock.png
var TileRock []byte

//go:embed tile-field.png
var TileField []byte

//go:embed tile-field-empty.png
var TileFieldEmpty []byte

//go:embed bee.png
var SpriteBee []byte

//go:embed hive.png
var SpriteHive []byte

//go:embed wall.png
var SpriteWall []byte

//go:embed flower.png
var SpriteFlower []byte

// Press Start 2P, see pressstart2p-license.txt
//
//go:embed pressstart2p.ttf
var FontData []byte

func loadImage(data []byte) *ebiten.Image {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		log.Fatalf("Could not decode embedded image: %s", err)
	}
	return ebiten.NewImageFromImage(img)
}

var Font *text.GoTextFace
var LineHeight float64

func LoadResources() {
	TerrainTiles = make(map[common.Terrain]*ebiten.Image)

	TerrainTiles[common.EMPTY] = loadImage(TileEmpty)
	TerrainTiles[common.ROCK] = loadImage(TileRock)
	TerrainTiles[common.FIELD] = loadImage(TileField)
	EmptyFieldTile = loadImage(TileFieldEmpty)

	EntityTiles = make(map[common.EntityType]*ebiten.Image)

	EntityTiles[common.BEE] = loadImage(SpriteBee)
	EntityTiles[common.HIVE] = loadImage(SpriteHive)
	EntityTiles[common.WALL] = loadImage(SpriteWall)
	FlowerImage = loadImage(SpriteFlower)

	fontSource, err := text.NewGoTextFaceSource(bytes.NewReader(FontData))
	if err != nil {
		log.Fatalf("Could not load embedded font: %s", err)
	}
	Font = &text.GoTextFace{
		Source: fontSource,
		Size:   16,
	}
	LineHeight = Font.Size + 2
}
