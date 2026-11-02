import 'dart:io' show Platform;

/// The process environment on platforms with `dart:io`.
Map<String, String> environment() => Platform.environment;

/// Whether the `User-Agent` header may be set. True outside the browser.
const bool canSetUserAgent = true;
