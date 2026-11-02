/// Dart and Flutter client for Acnologia, a self-hosted API key pool.
///
/// Acnologia can be consumed in two ways:
///
/// * Lease mode: [Acnologia.lease] a key, call the provider yourself, then
///   [Lease.report] the outcome. [Acnologia.withKey] wraps this loop and
///   rotates keys automatically.
/// * Proxy mode: send requests through [Acnologia.proxy]. Acnologia injects a
///   key and rotates on rate limits without ever exposing the secret.
library;

import 'dart:async';
import 'dart:convert';

import 'package:http/http.dart' as http;

import 'src/env_stub.dart' if (dart.library.io) 'src/env_io.dart' as env;

part 'src/client.dart';
part 'src/errors.dart';
part 'src/lease.dart';
part 'src/models.dart';
part 'src/proxy.dart';
part 'src/with_key.dart';
