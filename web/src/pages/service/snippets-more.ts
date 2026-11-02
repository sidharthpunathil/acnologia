/*
 * Integration snippets for the languages beyond Python, TypeScript and Go.
 * Each one uses that language's Acnologia SDK (sdk/<language> in the repo):
 * proxy mode through its proxy helper, lease mode through its withKey helper,
 * which reports every lease and rotates to the next key on a rate limit. The
 * install line sits in a comment at the top.
 *
 * Every value can come from the server (provider spec, service config), so
 * each one is escaped for the literal syntax of the language it lands in.
 */

export type MoreLang = "rust" | "java" | "csharp" | "cpp" | "swift" | "kotlin" | "php" | "ruby" | "dart";

export interface SnippetInput {
  /** Acnologia origin, for example https://keys.example.com. */
  host: string;
  slug: string;
  /** The example path, without its query. */
  path: string;
  /** The example query parameters, unencoded. */
  params: [string, string][];
  /** "bearer", "header" or "query". */
  authType: string;
}

const code = (c: string) => c.charCodeAt(0);
const hex = (c: string, n: number) => code(c).toString(16).padStart(n, "0");

/** A double quoted literal with the common escapes; `ctl` spells any other control character. */
function dq(v: string, ctl: (c: string) => string, extra: Record<string, string> = {}): string {
  let out = "";
  for (const c of v) {
    if (c in extra) out += extra[c];
    else if (c === "\\") out += "\\\\";
    else if (c === '"') out += '\\"';
    else if (c === "\n") out += "\\n";
    else if (c === "\r") out += "\\r";
    else if (c === "\t") out += "\\t";
    else if (code(c) < 0x20 || code(c) === 0x7f) out += ctl(c);
    else out += c;
  }
  return `"${out}"`;
}

/** A single quoted literal where only the quote and backslash need escaping (PHP, Ruby). */
const sq = (v: string) => `'${v.replace(/[\\']/g, (c) => `\\${c}`)}'`;

const octal = (c: string) => `\\${code(c).toString(8).padStart(3, "0")}`;

export const lits: Record<MoreLang, (v: string) => string> = {
  rust: (v) => dq(v, (c) => `\\u{${hex(c, 1)}}`),
  swift: (v) => dq(v, (c) => `\\u{${hex(c, 1)}}`),
  // Java turns \\uXXXX escapes into characters before parsing, so \\u000a would end the literal: octal is safe.
  java: (v) => dq(v, octal),
  cpp: (v) => dq(v, octal),
  csharp: (v) => dq(v, (c) => `\\u${hex(c, 4)}`),
  kotlin: (v) => dq(v, (c) => `\\u${hex(c, 4)}`, { $: "\\$" }),
  dart: (v) => `'${[...v].map((c) => (c === "\\" || c === "'" || c === "$" ? `\\${c}` : code(c) < 0x20 ? `\\u{${hex(c, 1)}}` : c)).join("")}'`,
  php: sq,
  ruby: sq,
};

export function moreSnippets(i: SnippetInput): Record<"proxy" | "lease", Record<MoreLang, string>> {
  const q = i.authType === "query";
  const L = (lang: MoreLang) => lits[lang];
  const P = i.params;
  /** Query pairs rendered with `pair` and joined, or "" when there are none. */
  const pairs = (pair: (k: string, v: string) => string) => P.map(([k, v]) => pair(k, v)).join(", ");
  const has = P.length > 0;

  const R = L("rust");
  const rustQ = `&[${pairs((k, v) => `(${R(k)}, ${R(v)})`)}]`;
  const rust = {
    proxy: `// cargo add acnologia --git https://github.com/sidharthpunathil/acnologia
// cargo add tokio --features macros,rt-multi-thread
use acnologia::Client;

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    let acn = Client::new(${R(i.host)}, std::env::var("ACNOLOGIA_TOKEN")?)?;

    // Acnologia picks a key, retries on rate limits, and never exposes the secret.
    let res = acn.proxy(${R(i.slug)}).get(${R(i.path)}, ${rustQ}).await?;
    println!("{}", res.text().await?);
    Ok(())
}`,
    lease: `// cargo add acnologia --git https://github.com/sidharthpunathil/acnologia
// cargo add tokio --features macros,rt-multi-thread
// cargo add reqwest@0.12
use acnologia::{check_status, Client};

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    let acn = Client::new(${R(i.host)}, std::env::var("ACNOLOGIA_TOKEN")?)?;
    let http = reqwest::Client::new();

    // with_key reports every lease and rotates to the next key on a rate limit.
    let body = acn
        .with_key(${R(i.slug)}, |lease| {
            let http = http.clone();
            async move {
                let req = http.get(lease.url(${R(i.path)}, ${rustQ}));
                // apply puts the key where this service expects it;
                // check_status turns a 429 into a rate limit.
                let res = check_status(lease.apply(req).send().await?)?;
                Ok(res.text().await?)
            }
        })
        .await?;
    println!("{body}");
    Ok(())
}`,
  };

  const J = L("java");
  const javaQ = has ? `, Map.ofEntries(${pairs((k, v) => `Map.entry(${J(k)}, ${J(v)})`)})` : "";
  const javaInstall = `// Install: cd acnologia/sdk/java && mvn install
// then depend on dev.acnologia:acnologia:0.1.0`;
  const java = {
    proxy: `${javaInstall}
import dev.acnologia.Acnologia;
import java.net.http.HttpResponse;
import java.nio.charset.StandardCharsets;
import java.util.Map;

public class Main {
    public static void main(String[] args) {
        Acnologia acn = Acnologia.create(${J(i.host)}, System.getenv("ACNOLOGIA_TOKEN"));

        // Acnologia picks a key, retries on rate limits, and never exposes the secret.
        HttpResponse<byte[]> res = acn.proxy(${J(i.slug)}).get(${J(i.path)}${javaQ});
        System.out.println(new String(res.body(), StandardCharsets.UTF_8));
    }
}`,
    lease: `${javaInstall}
import dev.acnologia.Acnologia;
import dev.acnologia.RateLimitedException;
import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.util.Map;

public class Main {
    public static void main(String[] args) throws Exception {
        Acnologia acn = Acnologia.create(${J(i.host)}, System.getenv("ACNOLOGIA_TOKEN"));
        HttpClient http = HttpClient.newHttpClient();

        // withKey reports every lease and rotates to the next key on a rate limit.
        String body = acn.withKey(${J(i.slug)}, lease -> {
            URI uri = URI.create(lease.url(${J(i.path)}${javaQ}));
            // apply puts the key where this service expects it
            HttpRequest req = lease.apply(HttpRequest.newBuilder(uri).build());
            HttpResponse<String> res = http.send(req, HttpResponse.BodyHandlers.ofString());
            if (res.statusCode() == 429) throw new RateLimitedException(null, 429, null);
            return res.body();
        });
        System.out.println(body);
    }
}`,
  };

  const C = L("csharp");
  const csQ = has ? `, new Dictionary<string, string> { ${pairs((k, v) => `[${C(k)}] = ${C(v)}`)} }` : "";
  const csInstall = "// dotnet add reference path/to/acnologia/sdk/csharp/src/Acnologia/Acnologia.csproj";
  const csharp = {
    proxy: `${csInstall}
using Acnologia;

using var acn = new AcnologiaClient(${C(i.host)}, Environment.GetEnvironmentVariable("ACNOLOGIA_TOKEN")!);

// Acnologia picks a key, retries on rate limits, and never exposes the secret.
using var res = await acn.Proxy(${C(i.slug)}).GetAsync(${C(i.path)}${csQ});
Console.WriteLine(await res.Content.ReadAsStringAsync());`,
    lease: `${csInstall}
using Acnologia;

using var acn = new AcnologiaClient(${C(i.host)}, Environment.GetEnvironmentVariable("ACNOLOGIA_TOKEN")!);
using var http = new HttpClient();

// WithKeyAsync reports every lease and rotates to the next key on a rate limit.
var body = await acn.WithKeyAsync(${C(i.slug)}, async (lease, ct) =>
{
    using var req = new HttpRequestMessage(HttpMethod.Get, lease.Url(${C(i.path)}${csQ}));
    lease.Apply(req); // puts the key where this service expects it
    using var res = await http.SendAsync(req, ct);
    if ((int)res.StatusCode == 429) throw new RateLimitedException(statusCode: 429);
    return await res.Content.ReadAsStringAsync(ct);
});
Console.WriteLine(body);`,
  };

  const X = L("cpp");
  const cppQ = has ? `, {${pairs((k, v) => `{${X(k)}, ${X(v)}}`)}}` : "";
  const cppInstall = `// CMake: FetchContent_Declare(acnologia GIT_REPOSITORY https://github.com/sidharthpunathil/acnologia.git
//          GIT_TAG main SOURCE_SUBDIR sdk/cpp), then link acnologia::acnologia`;
  const cppClient = `    const char* token = std::getenv("ACNOLOGIA_TOKEN");
    acnologia::Client client(${X(i.host)}, token ? token : "");`;
  const cpp = {
    proxy: `${cppInstall}
#include <acnologia/acnologia.hpp>
#include <cstdlib>
#include <iostream>

int main() {
${cppClient}

    // Acnologia picks a key, retries on rate limits, and never exposes the secret.
    auto res = client.proxy(${X(i.slug)}).get(${X(i.path)}${cppQ});
    std::cout << res.body << std::endl;
}`,
    lease: `${cppInstall}
#include <acnologia/acnologia.hpp>
#include <cstdlib>
#include <iostream>
#include <string>

int main() {
${cppClient}
    auto http = acnologia::make_curl_transport();  // or any HTTP library

    // with_key reports every lease and rotates to the next key on a rate limit.
    std::string body = client.with_key(${X(i.slug)}, [&](acnologia::Lease& lease) {
        acnologia::HttpRequest req;
        req.method = "GET";
${
  q
    ? `        req.url = lease.signed_url(${X(i.path)}${cppQ});  // adds the key as this service expects`
    : `        req.url = lease.url(${X(i.path)}${cppQ});
        auto key = lease.apply();  // the header this service expects
        req.headers.push_back({key.name, key.value});`
}
        auto res = http->send(req);
        if (res.status == 429) throw acnologia::RateLimited({}, 429);
        return res.body;
    });
    std::cout << body << std::endl;
}`,
  };

  const S = L("swift");
  const swiftQ = has ? `, query: [${pairs((k, v) => `URLQueryItem(name: ${S(k)}, value: ${S(v)})`)}]` : "";
  const swiftHead = `// Package.swift: .package(url: "https://github.com/sidharthpunathil/acnologia", branch: "main")
// and .product(name: "Acnologia", package: "acnologia") in your target
import Acnologia
import Foundation

let acn = try Acnologia(baseURL: ${S(i.host)}, token: ProcessInfo.processInfo.environment["ACNOLOGIA_TOKEN"] ?? "")`;
  const swift = {
    proxy: `${swiftHead}

// Acnologia picks a key, retries on rate limits, and never exposes the secret.
let res = try await acn.proxy(${S(i.slug)}).get(${S(i.path)}${swiftQ})
print(res.text)`,
    lease: `${swiftHead}

// withKey reports every lease and rotates to the next key on a rate limit.
let body: String = try await acn.withKey(${S(i.slug)}) { lease in
    var req = URLRequest(url: try lease.url(${S(i.path)}${swiftQ}))
    lease.apply(to: &req) // puts the key where this service expects it
    let (data, res) = try await URLSession.shared.data(for: req)
    if (res as? HTTPURLResponse)?.statusCode == 429 {
        throw AcnologiaError.rateLimited(statusCode: 429)
    }
    return String(decoding: data, as: UTF8.self)
}
print(body)`,
  };

  const K = L("kotlin");
  const kotlinQ = has ? `, mapOf(${pairs((k, v) => `${K(k)} to ${K(v)}`)})` : "";
  const kotlinInstall = `// Install: cd acnologia/sdk/kotlin && gradle publishToMavenLocal
// then implementation("dev.acnologia:acnologia-kotlin:0.1.0") with mavenLocal()`;
  const kotlin = {
    proxy: `${kotlinInstall}
import dev.acnologia.kotlin.Acnologia

suspend fun main() {
    val acn = Acnologia(${K(i.host)}, System.getenv("ACNOLOGIA_TOKEN"))

    // Acnologia picks a key, retries on rate limits, and never exposes the secret.
    acn.proxy(${K(i.slug)}).get(${K(i.path)}${kotlinQ}).use { res -> println(res.body!!.string()) }
}`,
    lease: `${kotlinInstall}
import dev.acnologia.kotlin.Acnologia
import dev.acnologia.kotlin.RateLimitedException
import dev.acnologia.kotlin.await
import okhttp3.OkHttpClient

suspend fun main() {
    val acn = Acnologia(${K(i.host)}, System.getenv("ACNOLOGIA_TOKEN"))
    val http = OkHttpClient()

    // withKey reports every lease and rotates to the next key on a rate limit.
    val body = acn.withKey(${K(i.slug)}) { lease ->
        // lease.request builds a GET with the key where this service expects it
        http.newCall(lease.request(${K(i.path)}${kotlinQ})).await().use { res ->
            if (res.code == 429) throw RateLimitedException(statusCode = 429)
            res.body!!.string()
        }
    }
    println(body)
}`,
  };

  const H = L("php");
  const phpQ = `[${pairs((k, v) => `${H(k)} => ${H(v)}`)}]`;
  const phpHead = `<?php
// Install: add a Composer path repository for acnologia/sdk/php, then composer require acnologia/keys:@dev
require 'vendor/autoload.php';

$acn = new Acnologia\\Acnologia(${H(i.host)}, getenv('ACNOLOGIA_TOKEN'));`;
  const php = {
    proxy: `${phpHead}

// Acnologia picks a key, retries on rate limits, and never exposes the secret.
$res = $acn->proxy(${H(i.slug)})->get(${H(i.path)}${has ? `, ${phpQ}` : ""});
echo $res->body, PHP_EOL;`,
    lease: `${phpHead}

// withKey reports every lease and rotates to the next key on a rate limit.
$body = $acn->withKey(${H(i.slug)}, function (Acnologia\\Lease $lease) {
    // apply puts the key where this service expects it
    ['query' => $query, 'headers' => $headers] = $lease->apply(${phpQ});
    $ch = curl_init($lease->url(${H(i.path)}, $query));
    curl_setopt_array($ch, [
        CURLOPT_RETURNTRANSFER => true,
        CURLOPT_HTTPHEADER => array_map(fn ($k, $v) => "$k: $v", array_keys($headers), $headers),
    ]);
    $body = curl_exec($ch);
    if (curl_getinfo($ch, CURLINFO_RESPONSE_CODE) === 429) {
        throw Acnologia\\Exception\\RateLimited::after(null);
    }
    return $body;
});
echo $body, PHP_EOL;`,
  };

  const B = L("ruby");
  const rubyQ = has ? `{ ${pairs((k, v) => `${B(k)} => ${B(v)}`)} }` : "{}";
  const rubyHead = `# Gemfile: gem "acnologia", git: "https://github.com/sidharthpunathil/acnologia.git", glob: "sdk/ruby/*.gemspec"
# then run with: bundle exec ruby app.rb
require "acnologia"`;
  const rubyClient = `acn = Acnologia::Client.new(${B(i.host)}, ENV.fetch("ACNOLOGIA_TOKEN"))`;
  const ruby = {
    proxy: `${rubyHead}

${rubyClient}

# Acnologia picks a key, retries on rate limits, and never exposes the secret.
res = acn.proxy(${B(i.slug)}).get(${B(i.path)}, ${rubyQ})
puts res.body`,
    lease: `${rubyHead}
require "net/http"

${rubyClient}

# with_key reports every lease and rotates to the next key on a rate limit.
body = acn.with_key(${B(i.slug)}) do |lease|
  # apply puts the key where this service expects it
  query, headers = lease.apply(query: ${rubyQ})
  uri = URI(lease.url(${B(i.path)}, query))
  res = Net::HTTP.start(uri.host, uri.port, use_ssl: uri.scheme == "https") do |http|
    http.get(uri.request_uri, headers)
  end
  raise Acnologia::RateLimited.new(status_code: 429) if res.code == "429"

  res.body
end
puts body`,
  };

  const D = L("dart");
  const dartQ = has ? `, query: {${pairs((k, v) => `${D(k)}: ${D(v)}`)}}` : "";
  const dartInstall = `// pubspec.yaml dependencies:
//   acnologia: {git: {url: https://github.com/sidharthpunathil/acnologia.git, path: sdk/dart}}`;
  const dartClient = `  final acn = Acnologia(baseUrl: ${D(i.host)}, token: Platform.environment['ACNOLOGIA_TOKEN'] ?? '');`;
  const dart = {
    proxy: `${dartInstall}
import 'dart:io';

import 'package:acnologia/acnologia.dart';

Future<void> main() async {
${dartClient}

  // Acnologia picks a key, retries on rate limits, and never exposes the secret.
  final res = await acn.proxy(${D(i.slug)}).get(${D(i.path)}${dartQ});
  print(res.body);
  acn.close();
}`,
    lease: `${dartInstall}
//   http: ^1.1.0
import 'dart:io';

import 'package:acnologia/acnologia.dart';
import 'package:http/http.dart' as http;

Future<void> main() async {
${dartClient}
  final client = http.Client();

  // withKey reports every lease and rotates to the next key on a rate limit.
  final body = await acn.withKey(${D(i.slug)}, (lease) async {
    // lease.request builds a GET with the key where this service expects it
    final req = lease.request('GET', ${D(i.path)}${dartQ});
    final res = await http.Response.fromStream(await client.send(req));
    if (res.statusCode == 429) throw const RateLimitedException(statusCode: 429);
    return res.body;
  });
  print(body);
  client.close();
  acn.close();
}`,
  };

  const all = { rust, java, csharp, cpp, swift, kotlin, php, ruby, dart };
  const pick = (m: "proxy" | "lease") =>
    Object.fromEntries(Object.entries(all).map(([k, v]) => [k, v[m]])) as Record<MoreLang, string>;
  return { proxy: pick("proxy"), lease: pick("lease") };
}
