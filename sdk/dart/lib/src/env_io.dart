import 'dart:io' show File, FileSystemEntity, FileSystemException, Platform;

/// The process environment on platforms with `dart:io`.
Map<String, String> environment() => Platform.environment;

/// Whether the `User-Agent` header may be set. True outside the browser.
const bool canSetUserAgent = true;

/// Writes [contents] to the file at [path], replacing it.
Future<void> writeTextFile(String path, String contents) => File(path).writeAsString(contents, flush: true);

const _systemRoots = [
  '/etc/ssl/certs/ca-certificates.crt',
  '/etc/pki/tls/certs/ca-bundle.crt',
  '/etc/ssl/ca-bundle.pem',
  '/etc/ssl/cert.pem',
];

/// The first system root bundle found (`$SSL_CERT_FILE` first), skipping the
/// file at [target]. Null on Windows or when there is none.
Future<String?> systemRoots(String target) async {
  if (Platform.isWindows) return null;
  final fromEnv = Platform.environment['SSL_CERT_FILE'] ?? '';
  final paths = [if (fromEnv.isNotEmpty) fromEnv, ..._systemRoots];
  final targetExists = File(target).existsSync();
  for (final p in paths) {
    try {
      if (targetExists && File(p).existsSync() && FileSystemEntity.identicalSync(p, target)) continue;
      final text = await File(p).readAsString();
      if (text.contains('BEGIN CERTIFICATE')) return text;
    } on FileSystemException {
      continue;
    } on FormatException {
      continue;
    }
  }
  return null;
}
