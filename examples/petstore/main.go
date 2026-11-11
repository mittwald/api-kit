// Command petstore is a complete command-line tool for an invented API. All
// there is to it is the definition next to this file: api-kit generates the
// commands from it.
//
//	petstore get pets --filter species=cat
//	petstore get pets 0a1b2c3d visits -o json
//	petstore rename pets 0a1b2c3d --name Rex
//	petstore adopt pets --name Tom --species cat --owner 4711 --if-none pets ownerId=@owner
package main

import (
	"context"
	_ "embed"
	"os"

	"github.com/mittwald/cli-kit/buildinfo"

	"github.com/mittwald/api-kit/cli"
	"github.com/mittwald/api-kit/definition"
)

//go:embed definition.yaml
var api []byte

func main() {
	os.Exit(cli.Run(context.Background(), options(), os.Args[1:]))
}

// options describe the tool.
func options() cli.Options {
	return cli.Options{
		Name:       "petstore",
		Short:      "Query and change the pet store",
		Build:      buildinfo.Get(),
		Definition: definition.Source{Name: "definition.yaml", Data: api},
		EnvPrefix:  "PETSTORE",
	}
}
