package resource

import "embed"

//go:embed Resource/*
var staticFiles embed.FS

func ReadResourceFile(filename string) []byte {
	data, err := staticFiles.ReadFile("Resource/" + filename)
	if err != nil {
		return nil
	}

	return data
}
