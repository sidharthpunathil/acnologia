/// The process environment. Empty on the web, where there is none.
Map<String, String> environment() => const {};

/// Browsers refuse to let scripts set `User-Agent`, so the SDK leaves it alone there.
const bool canSetUserAgent = false;
