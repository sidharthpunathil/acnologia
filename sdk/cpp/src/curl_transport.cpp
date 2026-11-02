#include <curl/curl.h>

#include <cctype>
#include <memory>
#include <string>

#include "acnologia/errors.hpp"
#include "acnologia/transport.hpp"

namespace acnologia {
namespace {

// Runs curl_global_init once, the first time a transport is created, and
// curl_global_cleanup at program exit. A function local static is initialized
// thread safely, so this is the only global state in the SDK.
class CurlGlobal {
public:
    CurlGlobal() : code_(curl_global_init(CURL_GLOBAL_DEFAULT)) {}
    ~CurlGlobal() {
        if (code_ == CURLE_OK) curl_global_cleanup();
    }
    CurlGlobal(const CurlGlobal&) = delete;
    CurlGlobal& operator=(const CurlGlobal&) = delete;
    bool ok() const noexcept { return code_ == CURLE_OK; }

private:
    CURLcode code_;
};

const CurlGlobal& curl_global() {
    static CurlGlobal guard;
    return guard;
}

struct EasyDeleter {
    void operator()(CURL* h) const noexcept { curl_easy_cleanup(h); }
};
struct SlistDeleter {
    void operator()(curl_slist* l) const noexcept { curl_slist_free_all(l); }
};
using EasyHandle = std::unique_ptr<CURL, EasyDeleter>;
using Slist = std::unique_ptr<curl_slist, SlistDeleter>;

size_t write_body(char* data, size_t size, size_t n, void* user) {
    static_cast<std::string*>(user)->append(data, size * n);
    return size * n;
}

size_t write_header(char* data, size_t size, size_t n, void* user) {
    auto* headers = static_cast<Headers*>(user);
    std::string line(data, size * n);
    while (!line.empty() && (line.back() == '\r' || line.back() == '\n')) line.pop_back();
    if (line.rfind("HTTP/", 0) == 0) {
        headers->clear();  // a new response (after 100 Continue)
        return size * n;
    }
    auto colon = line.find(':');
    if (colon == std::string::npos) return size * n;
    std::string value = line.substr(colon + 1);
    std::size_t b = 0;
    while (b < value.size() && std::isspace(static_cast<unsigned char>(value[b]))) ++b;
    headers->emplace_back(line.substr(0, colon), value.substr(b));
    return size * n;
}

class CurlTransport final : public Transport {
public:
    CurlTransport() {
        if (!curl_global().ok()) throw TransportError("acnologia: curl_global_init failed");
    }

    HttpResponse send(const HttpRequest& req) override {
        EasyHandle h(curl_easy_init());
        if (!h) throw TransportError("acnologia: curl_easy_init failed");
        CURL* c = h.get();
        char errbuf[CURL_ERROR_SIZE] = {0};
        HttpResponse resp;

        Slist list;
        auto add = [&list](const std::string& line) {
            curl_slist* next = curl_slist_append(list.get(), line.c_str());
            if (!next) throw TransportError("acnologia: out of memory building headers");
            list.release();
            list.reset(next);
        };
        for (const auto& [k, v] : req.headers) add(k + ": " + v);
        add("Expect:");

        curl_easy_setopt(c, CURLOPT_URL, req.url.c_str());
        curl_easy_setopt(c, CURLOPT_HTTPHEADER, list.get());
        curl_easy_setopt(c, CURLOPT_NOSIGNAL, 1L);
        curl_easy_setopt(c, CURLOPT_FOLLOWLOCATION, 0L);
        curl_easy_setopt(c, CURLOPT_ERRORBUFFER, errbuf);
        curl_easy_setopt(c, CURLOPT_WRITEFUNCTION, write_body);
        curl_easy_setopt(c, CURLOPT_WRITEDATA, &resp.body);
        curl_easy_setopt(c, CURLOPT_HEADERFUNCTION, write_header);
        curl_easy_setopt(c, CURLOPT_HEADERDATA, &resp.headers);
        if (req.timeout.count() > 0) {
            curl_easy_setopt(c, CURLOPT_TIMEOUT_MS, static_cast<long>(req.timeout.count()));
        }

        const std::string& m = req.method;
        if (m == "GET" && req.body.empty()) {
            curl_easy_setopt(c, CURLOPT_HTTPGET, 1L);
        } else if (m == "HEAD") {
            curl_easy_setopt(c, CURLOPT_NOBODY, 1L);
        } else {
            curl_easy_setopt(c, CURLOPT_CUSTOMREQUEST, m.c_str());
            if (!req.body.empty() || m == "POST" || m == "PUT" || m == "PATCH") {
                curl_easy_setopt(c, CURLOPT_POSTFIELDSIZE_LARGE, static_cast<curl_off_t>(req.body.size()));
                curl_easy_setopt(c, CURLOPT_POSTFIELDS, req.body.data());
            }
        }

        CURLcode rc = curl_easy_perform(c);
        if (rc != CURLE_OK) {
            std::string msg = errbuf[0] ? errbuf : curl_easy_strerror(rc);
            throw TransportError("acnologia: " + req.method + " request failed: " + msg);
        }
        long status = 0;
        curl_easy_getinfo(c, CURLINFO_RESPONSE_CODE, &status);
        resp.status = static_cast<int>(status);
        return resp;
    }
};

}  // namespace

std::shared_ptr<Transport> make_curl_transport() { return std::make_shared<CurlTransport>(); }

}  // namespace acnologia
