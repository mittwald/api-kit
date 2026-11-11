# api-kit

[![Go Reference](https://pkg.go.dev/badge/github.com/mittwald/api-kit.svg)](https://pkg.go.dev/github.com/mittwald/api-kit)
[![Test](https://github.com/mittwald/api-kit/actions/workflows/test.yml/badge.svg)](https://github.com/mittwald/api-kit/actions/workflows/test.yml)

api-kit turns a declarative description of a REST API into a command-line
tool. A YAML definition names the resources of an API, how they relate, and
the calls that change them; api-kit supplies the rest: authentication and
token caching, fetching with pagination, joins across resources, multi-step
calls with templates and conditions, and the commands, flags and shell
completion that expose all of it.

api-kit ships no API of its own. A binary embeds its definition and calls
`cli.Run`.

> [!NOTE]
> api-kit is under construction. Packages land one by one and their APIs
> change without notice until the first tagged release.

## Installation

```shell
go get github.com/mittwald/api-kit
```

api-kit supports Go 1.27 and newer on Linux, macOS and Windows. It builds on
[cli-kit](https://github.com/mittwald/cli-kit) and
[cobra](https://github.com/spf13/cobra).

## Packages

| Package | What it does |
|---|---|
| [`apikit`](https://pkg.go.dev/github.com/mittwald/api-kit) | JSON values that keep key order and number spelling, and error kinds with the exit codes they map to |
| [`expr`](https://pkg.go.dev/github.com/mittwald/api-kit/expr) | The small languages of a definition: templates, paths into values, conditions |
| [`definition`](https://pkg.go.dev/github.com/mittwald/api-kit/definition) | Loads a definition, merges the user's overlays into it and reports every mistake with its key |
| [`auth`](https://pkg.go.dev/github.com/mittwald/api-kit/auth) | Token sources, stored sessions, validation, renewal and logins, as the definition describes them |
| [`engine`](https://pkg.go.dev/github.com/mittwald/api-kit/engine) | Requests with caching and retries, listings with pagination, IDs from aliases and searches |
| [`output`](https://pkg.go.dev/github.com/mittwald/api-kit/output) | Results as tables, JSON, YAML, CSV, JSONPath selections and plain lines |
| [`cli`](https://pkg.go.dev/github.com/mittwald/api-kit/cli) | The command tree of a tool, generated from its definition, with shell completion |
| [`apitest`](https://pkg.go.dev/github.com/mittwald/api-kit/apitest) | A scripted fake API for tests, and the definition of an invented pet shop |

## Example

A definition names the environments of an API and describes its resources:

```yaml
environments:
  production:
    apis:
      main: https://api.example.test/v1
resources:
  pets:
    columns: [id, name, species]
    filters: ["species="]
```

A tool embeds it and hands over to `cli.Run`:

```go
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
	os.Exit(cli.Run(context.Background(), cli.Options{
		Name:       "example",
		Short:      "Query and change the Example API",
		Build:      buildinfo.Get(),
		Definition: definition.Source{Name: "definition.yaml", Data: api},
	}, os.Args[1:]))
}
```

```console
$ example get pets --filter species=cat
ID        NAME  SPECIES
0a1b2c3e  Tom   cat
$ example get pets 0a1b2c3e -o json
```

[`examples/petstore`](examples/petstore) is a complete tool of this kind,
with joins, ID aliases, calls that change data and a test that runs it
against a fake API.

## Versioning

api-kit follows [semantic versioning](https://semver.org). While it is
`v0.x`, minor releases may change APIs; such changes are listed first in the
release notes.

## Contributing

Contributions are welcome. See [CONTRIBUTING.md](CONTRIBUTING.md) for how to
build, test and submit changes, and [SECURITY.md](SECURITY.md) for reporting
vulnerabilities.

## License

Copyright (c) 2026 Mittwald CM Service GmbH & Co. KG and contributors

This project is licensed under the MIT License; see the [LICENSE](LICENSE)
file for details.
