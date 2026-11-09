/*
 * Forward proxy snippets: the app sets Acnologia as its HTTPS_PROXY and calls
 * the provider's own URL. Acnologia puts a pool key on every call to the
 * service's host, replacing any key the app sends. HTTPS needs the server's
 * CA trusted, so each snippet loads acnologia-ca.pem.
 *
 * Values can come from the server (the service's base URL, the provider's
 * example), so each one is escaped for the literal it lands in, and comment
 * lines lose any control character that could end the comment.
 */
import { CA_FILE, CA_PATH } from "@/lib/forwardProxy";
import { lits, type MoreLang } from "./snippets-more";

export type ForwardLang = "curl" | "python" | "typescript" | "go" | MoreLang;

export interface ForwardInput {
  /** Acnologia origin, for the CA download. */
  host: string;
  /** The proxy URL with the token in it: http://app:acn_...@host:port. */
  proxy: string;
  /** The provider URL the app calls, query included. */
  url: string;
  /** The same URL without its query, and the query pairs, for clients that build it. */
  base: string;
  params: [string, string][];
}

/** A JSON string literal: valid Python, TypeScript and Go. */
const lit = (v: string) => JSON.stringify(v);
/** Single quoted shell word. */
const shQ = (v: string) => `'${v.replace(/'/g, `'\\''`)}'`;
/** Text safe on one comment line. */
const line = (v: string) => v.replace(/[\u0000-\u001f\u007f\u2028\u2029]/g, " ");

const pyDict = (p: [string, string][]) => `{${p.map(([k, v]) => `${lit(k)}: ${lit(v)}`).join(", ")}}`;

/** For languages whose HTTP client does not read HTTPS_PROXY by itself. */
function manual(lang: string, i: ForwardInput): string {
  return `// ${lang} does not take HTTPS_PROXY on its own. Use Proxy mode, or
// point your HTTP client at the forward proxy yourself:
//   proxy:  ${line(i.proxy)}
//           (the app token is the proxy password)
//   trust:  ${CA_FILE}, from ${line(i.host)}${CA_PATH}
//   call:   ${line(i.url)}
// Acnologia adds the key. Any key the app sends is replaced.`;
}

export function forwardSnippets(i: ForwardInput): Record<ForwardLang, string> {
  const caUrl = `${i.host}${CA_PATH}`;
  const R = lits.rust;
  const P = lits.php;
  const Rb = lits.ruby;
  return {
    curl: `# Download the CA once, then call the provider's own URL through the proxy.
curl -o ${CA_FILE} ${shQ(caUrl)}

curl -x ${shQ(i.proxy)} --cacert ${CA_FILE} \\
  ${shQ(i.url)}

# Or set it for every tool in this shell:
export HTTPS_PROXY=${shQ(i.proxy)}
curl --cacert ${CA_FILE} ${shQ(i.url)}`,
    python: `# Run with:
#   HTTPS_PROXY=${line(shQ(i.proxy))} \\
#   REQUESTS_CA_BUNDLE=${CA_FILE} python app.py
import requests

# Acnologia adds a key from the pool. Any key you send is replaced.
r = requests.get(${lit(i.base)}, params=${pyDict(i.params)})
print(r.json())`,
    typescript: `// Run with Node 24 or later:
//   HTTPS_PROXY=${line(shQ(i.proxy))} \\
//   NODE_EXTRA_CA_CERTS=${CA_FILE} node --use-env-proxy app.js

// Acnologia adds a key from the pool. Any key you send is replaced.
const res = await fetch(${lit(i.url)});
console.log(await res.json());`,
    go: `// Run with HTTPS_PROXY=${line(shQ(i.proxy))}
// Go's default client reads HTTPS_PROXY; the CA is added to the system roots.
package main

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net/http"
	"os"
)

func main() {
	roots, _ := x509.SystemCertPool()
	if roots == nil {
		roots = x509.NewCertPool()
	}
	pem, err := os.ReadFile(${lit(CA_FILE)})
	if err != nil {
		panic(err)
	}
	roots.AppendCertsFromPEM(pem)
	http.DefaultTransport.(*http.Transport).TLSClientConfig = &tls.Config{RootCAs: roots}

	// Acnologia adds a key from the pool. Any key you send is replaced.
	res, err := http.Get(${lit(i.url)})
	if err != nil {
		panic(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	fmt.Println(string(body))
}`,
    rust: `// Run with HTTPS_PROXY=${line(shQ(i.proxy))}
// cargo add reqwest@0.12
// cargo add tokio --features macros,rt-multi-thread
// reqwest reads HTTPS_PROXY; the CA is added to its roots.

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    let ca = reqwest::Certificate::from_pem(&std::fs::read(${R(CA_FILE)})?)?;
    let http = reqwest::Client::builder().add_root_certificate(ca).build()?;

    // Acnologia adds a key from the pool. Any key you send is replaced.
    let res = http.get(${R(i.url)}).send().await?;
    println!("{}", res.text().await?);
    Ok(())
}`,
    php: `<?php
// Acnologia adds a key from the pool. Any key you send is replaced.
$ch = curl_init(${P(i.url)});
curl_setopt_array($ch, [
    CURLOPT_PROXY => ${P(i.proxy)},
    CURLOPT_CAINFO => ${P(CA_FILE)},
    CURLOPT_RETURNTRANSFER => true,
]);
echo curl_exec($ch);`,
    ruby: `# Run with HTTPS_PROXY=${line(shQ(i.proxy))}
# Net::HTTP reads HTTPS_PROXY, user and password included.
require "net/http"

uri = URI(${Rb(i.url)})
http = Net::HTTP.new(uri.host, uri.port)
http.use_ssl = true
http.ca_file = ${Rb(CA_FILE)}

# Acnologia adds a key from the pool. Any key you send is replaced.
puts http.get(uri.request_uri).body`,
    java: manual("Java", i),
    csharp: manual("C#", i),
    cpp: manual("C++", i),
    swift: manual("Swift", i),
    kotlin: manual("Kotlin", i),
    dart: manual("Dart", i),
  };
}
