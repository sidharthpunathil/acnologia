/*
 * Integration snippets for languages without an Acnologia SDK. Each one uses
 * the HTTP client most projects in that language already have.
 *
 * Every value can come from the server (provider spec, service config), so
 * each one is escaped for the literal syntax of the language it lands in.
 */

export type MoreLang = "rust" | "java" | "csharp" | "cpp" | "swift" | "kotlin" | "php" | "ruby" | "dart";

export interface SnippetInput {
  /** Acnologia origin, for example https://keys.example.com. */
  host: string;
  slug: string;
  /** Proxy URL with the example path and query. */
  proxyUrl: string;
  /** Example path plus its encoded query string, appended to the lease base_url. */
  pathQ: string;
  /** "bearer", "header" or "query". */
  authType: string;
  /** Header name, or the URL encoded query parameter name with "=" for query auth. */
  authName: string;
  queryKey: string;
  /** "&" when pathQ already has a query string, otherwise "?". */
  sep: string;
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

const STATUS_NOTE = "429 means rate limited; any other error is reported as error";

export function moreSnippets(i: SnippetInput): Record<"proxy" | "lease", Record<MoreLang, string>> {
  const q = i.authType === "query";
  const bearer = i.authType === "bearer";
  const L = (lang: MoreLang) => lits[lang];
  /** base + pathQ, plus the query parameter for query auth. */
  const url = (lang: MoreLang, base: string, encoded: string, join = " + ") =>
    [base, L(lang)(i.pathQ), ...(q ? [L(lang)(i.sep + i.queryKey), encoded] : [])].join(join);
  /** Header name and value expression for header and bearer auth. */
  const hName = bearer ? "Authorization" : i.authName;
  const hValue = (secret: string, concat: (a: string, b: string) => string, lang: MoreLang) =>
    bearer ? concat(L(lang)("Bearer "), secret) : secret;
  const plus = (a: string, b: string) => `${a} + ${b}`;

  const rust = {
    proxy: `// Cargo.toml: reqwest = { version = "0.12", features = ["blocking"] }
use std::env;

fn main() -> Result<(), Box<dyn std::error::Error>> {
    let token = env::var("COGITO_TOKEN")?;
    let res = reqwest::blocking::Client::new()
        .get(${L("rust")(i.proxyUrl)})
        .bearer_auth(token)
        .send()?;
    println!("{}", res.text()?);
    Ok(())
}`,
    lease: `// Cargo.toml: reqwest = { version = "0.12", features = ["blocking", "json"] }, serde_json = "1"
use serde_json::{json, Value};
use std::env;

fn main() -> Result<(), Box<dyn std::error::Error>> {
    let host = ${L("rust")(i.host)};
    let token = env::var("COGITO_TOKEN")?;
    let client = reqwest::blocking::Client::new();

    // 1. Lease a key
    let lease: Value = client
        .post(format!("{host}/api/v1/lease"))
        .bearer_auth(&token)
        .json(&json!({ "service": ${L("rust")(i.slug)}, "ttl_sec": 300 }))
        .send()?
        .error_for_status()?
        .json()?;
    let secret = lease["secret"].as_str().unwrap_or_default();
    let base_url = lease["base_url"].as_str().unwrap_or_default();

    // 2. Call the provider yourself
    let res = client
        .get(format!("{base_url}{}", ${L("rust")(i.pathQ)}))
        ${q ? `.query(&[(${L("rust")(decodeURIComponent(i.queryKey.slice(0, -1)))}, secret)])` : bearer ? ".bearer_auth(secret)" : `.header(${L("rust")(i.authName)}, secret)`}
        .send()?;
    let status = res.status().as_u16();
    println!("{}", res.text()?);

    // 3. Report the outcome (${STATUS_NOTE})
    let outcome = match status { 429 => "rate_limited", 200..=399 => "ok", _ => "error" };
    let lease_id = lease["lease_id"].as_str().unwrap_or_default();
    client
        .post(format!("{host}/api/v1/leases/{lease_id}/report"))
        .bearer_auth(&token)
        .json(&json!({ "outcome": outcome, "status_code": status }))
        .send()?;
    Ok(())
}`,
  };

  const java = {
    proxy: `import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;

public class Main {
    public static void main(String[] args) throws Exception {
        HttpRequest req = HttpRequest.newBuilder(URI.create(${L("java")(i.proxyUrl)}))
            .header("Authorization", "Bearer " + System.getenv("COGITO_TOKEN"))
            .build();
        HttpResponse<String> res = HttpClient.newHttpClient().send(req, HttpResponse.BodyHandlers.ofString());
        System.out.println(res.body());
    }
}`,
    lease: `// Needs Jackson (com.fasterxml.jackson.core:jackson-databind) for JSON.
import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import java.net.URI;
import java.net.URLEncoder;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.nio.charset.StandardCharsets;
import java.util.Map;

public class Main {
    static final String HOST = ${L("java")(i.host)};
    static final String TOKEN = System.getenv("COGITO_TOKEN");
    static final HttpClient HTTP = HttpClient.newHttpClient();
    static final ObjectMapper JSON = new ObjectMapper();

    static String cogito(String path, Map<String, Object> body) throws Exception {
        HttpRequest req = HttpRequest.newBuilder(URI.create(HOST + path))
            .header("Authorization", "Bearer " + TOKEN)
            .header("Content-Type", "application/json")
            .POST(HttpRequest.BodyPublishers.ofString(JSON.writeValueAsString(body)))
            .build();
        HttpResponse<String> res = HTTP.send(req, HttpResponse.BodyHandlers.ofString());
        if (res.statusCode() >= 400) throw new IllegalStateException(res.body());
        return res.body();
    }

    public static void main(String[] args) throws Exception {
        // 1. Lease a key
        JsonNode lease = JSON.readTree(cogito("/api/v1/lease", Map.of("service", ${L("java")(i.slug)}, "ttl_sec", 300)));
        String secret = lease.get("secret").asText();

        // 2. Call the provider yourself
        HttpRequest call = HttpRequest.newBuilder(URI.create(${url("java", 'lease.get("base_url").asText()', "URLEncoder.encode(secret, StandardCharsets.UTF_8)")}))${q ? "" : `\n            .header(${L("java")(hName)}, ${hValue("secret", plus, "java")})`}
            .build();
        HttpResponse<String> res = HTTP.send(call, HttpResponse.BodyHandlers.ofString());
        System.out.println(res.body());

        // 3. Report the outcome (${STATUS_NOTE})
        int status = res.statusCode();
        String outcome = status == 429 ? "rate_limited" : status < 400 ? "ok" : "error";
        cogito("/api/v1/leases/" + lease.get("lease_id").asText() + "/report", Map.of("outcome", outcome, "status_code", status));
    }
}`,
  };

  const csharp = {
    proxy: `using System.Net.Http.Headers;

var http = new HttpClient();
http.DefaultRequestHeaders.Authorization =
    new AuthenticationHeaderValue("Bearer", Environment.GetEnvironmentVariable("COGITO_TOKEN"));

var res = await http.GetAsync(${L("csharp")(i.proxyUrl)});
Console.WriteLine(await res.Content.ReadAsStringAsync());`,
    lease: `using System.Net.Http.Headers;
using System.Net.Http.Json;
using System.Text.Json;

var cogito = new HttpClient { BaseAddress = new Uri(${L("csharp")(i.host)}) };
cogito.DefaultRequestHeaders.Authorization =
    new AuthenticationHeaderValue("Bearer", Environment.GetEnvironmentVariable("COGITO_TOKEN"));

// 1. Lease a key
var leaseRes = await cogito.PostAsJsonAsync("/api/v1/lease", new { service = ${L("csharp")(i.slug)}, ttl_sec = 300 });
leaseRes.EnsureSuccessStatusCode();
var lease = await leaseRes.Content.ReadFromJsonAsync<JsonElement>();
var secret = lease.GetProperty("secret").GetString()!;

// 2. Call the provider yourself (a separate client, so the Acnologia token never goes upstream)
var call = new HttpRequestMessage(HttpMethod.Get, ${url("csharp", 'lease.GetProperty("base_url").GetString()', "Uri.EscapeDataString(secret)")});${q ? "" : `\ncall.Headers.TryAddWithoutValidation(${L("csharp")(hName)}, ${hValue("secret", plus, "csharp")});`}
var res = await new HttpClient().SendAsync(call);
Console.WriteLine(await res.Content.ReadAsStringAsync());

// 3. Report the outcome (${STATUS_NOTE})
var status = (int)res.StatusCode;
var outcome = status == 429 ? "rate_limited" : status < 400 ? "ok" : "error";
await cogito.PostAsJsonAsync($"/api/v1/leases/{lease.GetProperty("lease_id").GetString()}/report",
    new { outcome, status_code = status });`,
  };

  const cpp = {
    proxy: `// Needs cpr (github.com/libcpr/cpr).
#include <cpr/cpr.h>
#include <cstdlib>
#include <iostream>

int main() {
    const char* token = std::getenv("COGITO_TOKEN");
    auto res = cpr::Get(cpr::Url{${L("cpp")(i.proxyUrl)}},
                        cpr::Bearer{token ? token : ""});
    std::cout << res.text << std::endl;
}`,
    lease: `// Needs cpr (github.com/libcpr/cpr) and nlohmann/json.
#include <cpr/cpr.h>
#include <nlohmann/json.hpp>
#include <cstdlib>
#include <iostream>

int main() {
    const std::string host = ${L("cpp")(i.host)};
    const char* env = std::getenv("COGITO_TOKEN");
    const cpr::Header cogito{{"Authorization", std::string("Bearer ") + (env ? env : "")},
                             {"Content-Type", "application/json"}};

    // 1. Lease a key
    auto leaseRes = cpr::Post(cpr::Url{host + "/api/v1/lease"}, cogito,
                              cpr::Body{nlohmann::json{{"service", ${L("cpp")(i.slug)}}, {"ttl_sec", 300}}.dump()});
    if (leaseRes.status_code != 200) { std::cerr << leaseRes.text << std::endl; return 1; }
    auto lease = nlohmann::json::parse(leaseRes.text);
    const std::string secret = lease["secret"];

    // 2. Call the provider yourself
    auto res = cpr::Get(cpr::Url{${url("cpp", 'lease["base_url"].get<std::string>()', "cpr::util::urlEncode(secret)")}}${q ? "" : `,\n                        cpr::Header{{${L("cpp")(hName)}, ${hValue("secret", (a, b) => `std::string(${a}) + ${b}`, "cpp")}}}`});
    std::cout << res.text << std::endl;

    // 3. Report the outcome (${STATUS_NOTE})
    const long status = res.status_code;
    const std::string outcome = status == 429 ? "rate_limited" : (status >= 200 && status < 400) ? "ok" : "error";
    cpr::Post(cpr::Url{host + "/api/v1/leases/" + lease["lease_id"].get<std::string>() + "/report"}, cogito,
              cpr::Body{nlohmann::json{{"outcome", outcome}, {"status_code", status}}.dump()});
}`,
  };

  const swift = {
    proxy: `import Foundation

var req = URLRequest(url: URL(string: ${L("swift")(i.proxyUrl)})!)
let token = ProcessInfo.processInfo.environment["COGITO_TOKEN"] ?? ""
req.setValue("Bearer \\(token)", forHTTPHeaderField: "Authorization")

let (data, _) = try await URLSession.shared.data(for: req)
print(String(decoding: data, as: UTF8.self))`,
    lease: `import Foundation

struct Lease: Decodable {
    let lease_id: String
    let secret: String
    let base_url: String
}

let host = ${L("swift")(i.host)}
let token = ProcessInfo.processInfo.environment["COGITO_TOKEN"] ?? ""

func cogito(_ path: String, _ body: [String: Any]) throws -> URLRequest {
    var req = URLRequest(url: URL(string: host + path)!)
    req.httpMethod = "POST"
    req.setValue("Bearer \\(token)", forHTTPHeaderField: "Authorization")
    req.setValue("application/json", forHTTPHeaderField: "Content-Type")
    req.httpBody = try JSONSerialization.data(withJSONObject: body)
    return req
}

// 1. Lease a key
let (leaseData, _) = try await URLSession.shared.data(for: cogito("/api/v1/lease", ["service": ${L("swift")(i.slug)}, "ttl_sec": 300]))
let lease = try JSONDecoder().decode(Lease.self, from: leaseData)

// 2. Call the provider yourself
var call = URLRequest(url: URL(string: ${url("swift", "lease.base_url", "(lease.secret.addingPercentEncoding(withAllowedCharacters: .alphanumerics) ?? \"\")")})!)${q ? "" : `\ncall.setValue(${hValue("lease.secret", plus, "swift")}, forHTTPHeaderField: ${L("swift")(hName)})`}
let (data, response) = try await URLSession.shared.data(for: call)
print(String(decoding: data, as: UTF8.self))

// 3. Report the outcome (${STATUS_NOTE})
let status = (response as? HTTPURLResponse)?.statusCode ?? 0
let outcome = status == 429 ? "rate_limited" : (200..<400).contains(status) ? "ok" : "error"
_ = try await URLSession.shared.data(for: cogito("/api/v1/leases/\\(lease.lease_id)/report", ["outcome": outcome, "status_code": status]))`,
  };

  const kotlin = {
    proxy: `// Needs OkHttp (com.squareup.okhttp3:okhttp).
import okhttp3.OkHttpClient
import okhttp3.Request

fun main() {
    val req = Request.Builder()
        .url(${L("kotlin")(i.proxyUrl)})
        .header("Authorization", "Bearer " + System.getenv("COGITO_TOKEN"))
        .build()
    OkHttpClient().newCall(req).execute().use { println(it.body!!.string()) }
}`,
    lease: `// Needs OkHttp (com.squareup.okhttp3:okhttp) and org.json.
import java.net.URLEncoder
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import org.json.JSONObject

val host = ${L("kotlin")(i.host)}
val token: String = System.getenv("COGITO_TOKEN")
val http = OkHttpClient()

fun cogito(path: String, body: Map<String, Any>): JSONObject {
    val req = Request.Builder()
        .url(host + path)
        .header("Authorization", "Bearer " + token)
        .post(JSONObject(body).toString().toRequestBody("application/json".toMediaType()))
        .build()
    return http.newCall(req).execute().use { res ->
        check(res.isSuccessful) { res.body!!.string() }
        JSONObject(res.body!!.string())
    }
}

fun main() {
    // 1. Lease a key
    val lease = cogito("/api/v1/lease", mapOf("service" to ${L("kotlin")(i.slug)}, "ttl_sec" to 300))
    val secret = lease.getString("secret")

    // 2. Call the provider yourself
    val call = Request.Builder()
        .url(${url("kotlin", 'lease.getString("base_url")', 'URLEncoder.encode(secret, "UTF-8")')})${q ? "" : `\n        .header(${L("kotlin")(hName)}, ${hValue("secret", plus, "kotlin")})`}
        .build()
    val status = http.newCall(call).execute().use { res ->
        println(res.body!!.string())
        res.code
    }

    // 3. Report the outcome (${STATUS_NOTE})
    val outcome = when {
        status == 429 -> "rate_limited"
        status < 400 -> "ok"
        else -> "error"
    }
    cogito("/api/v1/leases/" + lease.getString("lease_id") + "/report", mapOf("outcome" to outcome, "status_code" to status))
}`,
  };

  const php = {
    proxy: `<?php
// composer require guzzlehttp/guzzle
require 'vendor/autoload.php';

$res = (new GuzzleHttp\\Client())->get(${L("php")(i.proxyUrl)}, [
    'headers' => ['Authorization' => 'Bearer ' . getenv('COGITO_TOKEN')],
]);
echo $res->getBody(), PHP_EOL;`,
    lease: `<?php
// composer require guzzlehttp/guzzle
require 'vendor/autoload.php';

$cogito = new GuzzleHttp\\Client([
    'base_uri' => ${L("php")(i.host)},
    'headers' => ['Authorization' => 'Bearer ' . getenv('COGITO_TOKEN')],
]);

// 1. Lease a key
$lease = json_decode((string) $cogito->post('/api/v1/lease', [
    'json' => ['service' => ${L("php")(i.slug)}, 'ttl_sec' => 300],
])->getBody(), true);

// 2. Call the provider yourself (a separate client, so the Acnologia token never goes upstream)
$res = (new GuzzleHttp\\Client())->get(${url("php", "$lease['base_url']", "rawurlencode($lease['secret'])", " . ")}, [
    'http_errors' => false,${q ? "" : `\n    'headers' => [${L("php")(hName)} => ${hValue("$lease['secret']", (a, b) => `${a} . ${b}`, "php")}],`}
]);
echo $res->getBody(), PHP_EOL;

// 3. Report the outcome (${STATUS_NOTE})
$status = $res->getStatusCode();
$outcome = $status === 429 ? 'rate_limited' : ($status < 400 ? 'ok' : 'error');
$cogito->post("/api/v1/leases/{$lease['lease_id']}/report", [
    'json' => ['outcome' => $outcome, 'status_code' => $status],
]);`,
  };

  const ruby = {
    proxy: `require "net/http"

uri = URI(${L("ruby")(i.proxyUrl)})
res = Net::HTTP.get_response(uri, { "Authorization" => "Bearer #{ENV.fetch("COGITO_TOKEN")}" })
puts res.body`,
    lease: `require "json"
require "net/http"

HOST = ${L("ruby")(i.host)}
TOKEN = ENV.fetch("COGITO_TOKEN")

def cogito(path, body)
  res = Net::HTTP.post(URI(HOST + path), body.to_json,
                       "Authorization" => "Bearer #{TOKEN}", "Content-Type" => "application/json")
  raise res.body unless res.is_a?(Net::HTTPSuccess)
  JSON.parse(res.body)
end

# 1. Lease a key
lease = cogito("/api/v1/lease", { service: ${L("ruby")(i.slug)}, ttl_sec: 300 })

# 2. Call the provider yourself
uri = URI(${url("ruby", 'lease["base_url"]', 'URI.encode_www_form_component(lease["secret"])')})
res = Net::HTTP.get_response(uri, { ${q ? "" : `${L("ruby")(hName)} => ${hValue('lease["secret"]', plus, "ruby")}`} })
puts res.body

# 3. Report the outcome (${STATUS_NOTE})
status = res.code.to_i
outcome = status == 429 ? "rate_limited" : status < 400 ? "ok" : "error"
cogito("/api/v1/leases/#{lease["lease_id"]}/report", { outcome: outcome, status_code: status })`,
  };

  const dart = {
    proxy: `// dart pub add http
import 'dart:io';
import 'package:http/http.dart' as http;

Future<void> main() async {
  final res = await http.get(
    Uri.parse(${L("dart")(i.proxyUrl)}),
    headers: {'Authorization': 'Bearer \${Platform.environment['COGITO_TOKEN']}'},
  );
  print(res.body);
}`,
    lease: `// dart pub add http
import 'dart:convert';
import 'dart:io';
import 'package:http/http.dart' as http;

final host = ${L("dart")(i.host)};
final headers = {
  'Authorization': 'Bearer \${Platform.environment['COGITO_TOKEN']}',
  'Content-Type': 'application/json',
};

Future<Map<String, dynamic>> cogito(String path, Map<String, Object> body) async {
  final res = await http.post(Uri.parse(host + path), headers: headers, body: jsonEncode(body));
  if (res.statusCode >= 400) throw Exception(res.body);
  return jsonDecode(res.body) as Map<String, dynamic>;
}

Future<void> main() async {
  // 1. Lease a key
  final lease = await cogito('/api/v1/lease', {'service': ${L("dart")(i.slug)}, 'ttl_sec': 300});
  final String secret = lease['secret'];

  // 2. Call the provider yourself
  final res = await http.get(
    Uri.parse(${url("dart", "(lease['base_url'] as String)", "Uri.encodeQueryComponent(secret)")}),${q ? "" : `\n    headers: {${L("dart")(hName)}: ${hValue("secret", plus, "dart")}},`}
  );
  print(res.body);

  // 3. Report the outcome (${STATUS_NOTE})
  final status = res.statusCode;
  final outcome = status == 429 ? 'rate_limited' : status < 400 ? 'ok' : 'error';
  await cogito('/api/v1/leases/\${lease['lease_id']}/report', {'outcome': outcome, 'status_code': status});
}`,
  };

  const all = { rust, java, csharp, cpp, swift, kotlin, php, ruby, dart };
  const pick = (m: "proxy" | "lease") =>
    Object.fromEntries(Object.entries(all).map(([k, v]) => [k, v[m]])) as Record<MoreLang, string>;
  return { proxy: pick("proxy"), lease: pick("lease") };
}
