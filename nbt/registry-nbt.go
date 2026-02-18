package nbt

import _ "embed"

//go:embed nbt.json
var registryJSON []byte

func BuildRegistryNBT() []byte {
	b, err := BuildRegistryNBTFromJSON(registryJSON)
	if err != nil {
		panic(err)
	}
	return b
}
