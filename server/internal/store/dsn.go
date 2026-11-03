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

// commonPragmas are the connection settings both pools share. cache_size is
// per connection (negative means KiB, so 16 MB) and temp_store keeps the
// sorts and GROUP BY tables of the heavy aggregates in memory. mmap stays
// off.
const commonPragmas = "&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)" +
	"&_pragma=cache_size(-16000)&_pragma=temp_store(MEMORY)"

// writerDSN opens the one connection that writes. journal_size_limit
// truncates the WAL back to 64 MB after a checkpoint, and analysis_limit
// bounds the work of PRAGMA optimize.
func writerDSN(path string) string {
	return "file:" + uriPath(path) + "?_pragma=journal_mode(WAL)" + commonPragmas +
		"&_pragma=journal_size_limit(67108864)&_pragma=analysis_limit(" + analysisLimit + ")"
}

// analysisLimit is how many index entries PRAGMA optimize samples per index.
const analysisLimit = "1000"

// readerDSN opens the read pool. query_only makes any write on it fail with
// SQLITE_READONLY, so a write sent to the wrong pool cannot get around the
// single writer. WAL mode belongs to the file and is set by the writer.
func readerDSN(path string) string {
	return "file:" + uriPath(path) + "?_pragma=query_only(1)" + commonPragmas
}
