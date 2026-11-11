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
| [`apikit`](https://pkg.go.dev/github.com/mittwald/api-kit) | Error kinds and the exit codes they map to |
| [`cli`](https://pkg.go.dev/github.com/mittwald/api-kit/cli) | The command tree of a tool, with version and shell completion |

## Example

```go
package main

import (
	"context"
	"os"

	"github.com/mittwald/cli-kit/buildinfo"

	"github.com/mittwald/api-kit/cli"
)

func main() {
	os.Exit(cli.Run(context.Background(), cli.Options{
		Name:  "example",
		Short: "Query and change the Example API",
		Build: buildinfo.Get(),
	}, os.Args[1:]))
}
```

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
