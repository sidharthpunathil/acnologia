import 'dart:io' show File, Platform;

/// The process environment on platforms with `dart:io`.
Map<String, String> environment() => Platform.environment;

/// Whether the `User-Agent` header may be set. True outside the browser.
const bool canSetUserAgent = true;

/// Writes [contents] to the file at [path], replacing it.
Future<void> writeTextFile(String path, String contents) => File(path).writeAsString(contents, flush: true);
