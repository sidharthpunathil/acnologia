package store

import "strings"

// uriPath escapes a file path for a SQLite "file:" URI. SQLite reads ? as the
// start of the query, # as a fragment and %XX as an escape, so a data dir
// such as C:\Users\me\Acnologia #2 would otherwise open the wrong file.
// Backslashes and drive letters pass through: SQLite's Windows VFS takes
// them as they are.
func uriPath(p string) string {
	return strings.NewReplacer("%", "%25", "?", "%3f", "#", "%23").Replace(p)
}
