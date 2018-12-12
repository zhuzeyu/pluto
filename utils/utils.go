package utils

import (
	"encoding/json"
	"os"

	emUtils "github.com/ethereum/go-ethereum/cmd/utils"
	"github.com/ethereum/go-ethereum/core"
)

// readGenesis will read the given JSON format genesis file and return
// the initialized Genesis structure
func ReadGenesis(genesisPath string) *core.Genesis {
	// Make sure we have a valid genesis JSON
	//genesisPath := ctx.Args().First()
	if len(genesisPath) == 0 {
		emUtils.Fatalf("Must supply path to genesis JSON file")
	}
	file, err := os.Open(genesisPath)
	if err != nil {
		emUtils.Fatalf("Failed to read genesis file: %v", err)
	}
	defer file.Close()

	genesis := new(core.Genesis)
	if err := json.NewDecoder(file).Decode(genesis); err != nil {
		emUtils.Fatalf("invalid genesis file: %v", err)
	}
	return genesis
}
