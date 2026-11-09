// Package migrations embeds the checked-in SQL migration files so the API
// binary can apply them on startup without needing the source tree present
// at runtime (e.g. inside a container image).
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
