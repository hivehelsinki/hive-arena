package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hivehelsinki/hive-arena/arena"
)

// A small map for tests: 6 seats, a few fields in the middle
const tinyMap = `            H1  B1  R   R   B2  H2
          B1  .   .   F   .   .   B2
        R   .   F   F   F   F   .   R
      R   F   F   F   F   F   F   F   R
    B0  .   F   F   F   R   F   F   .   B3
  H0  .   F   F   R   R   F   F   F   .   H3
    B0  .   F   F   F   R   F   F   .   B3
      R   F   F   F   F   F   F   F   R
        R   .   F   F   F   F   .   R
          B5  .   .   F   .   .   B4
            H5  B5  R   R   B4  H4`

func testMap(t *testing.T) arena.MapData {
	t.Helper()
	mapdata, err := arena.ParseMap(tinyMap)
	if err != nil {
		t.Fatal(err)
	}
	return mapdata
}

func TestMapFiles(t *testing.T) {
	defer func(dir string) { MapDir = dir }(MapDir)

	MapDir = ""
	if maps := loadMaps(); len(maps) != 0 {
		t.Errorf("without a maps directory, no map files: %v", maps)
	}

	MapDir = t.TempDir()
	os.WriteFile(filepath.Join(MapDir, "scenario.txt"), []byte(tinyMap), 0o644)
	if maps := loadMaps(); len(maps) != 1 || len(maps["scenario"].Spawns) == 0 {
		t.Errorf("expected the scenario map: %v", maps)
	}
}
