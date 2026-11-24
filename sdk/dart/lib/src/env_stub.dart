/// The process environment. Empty on the web, where there is none.
Map<String, String> environment() => const {};

/// Browsers refuse to let scripts set `User-Agent`, so the SDK leaves it alone there.
const bool canSetUserAgent = false;

/// There is no file system on the web.
Future<void> writeTextFile(String path, String contents) =>
    Future.error(UnsupportedError('saving a file needs dart:io, which the web does not have'));

/// There are no system root files on the web.
Future<String?> systemRoots(String target) =>
    Future.error(UnsupportedError('reading the system roots needs dart:io, which the web does not have'));
