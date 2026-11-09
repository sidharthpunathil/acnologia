# frozen_string_literal: true

require "json"
require "net/http"
require "openssl"
require "securerandom"
require "time"
require "uri"

# Client for Acnologia, a self-hosted API key pool with automatic rotation.
#
#   acn = Acnologia::Client.new("http://localhost:8080", "acn_...")
#   res = acn.proxy("agi-labs").post("/v1/chat", { "prompt" => "hi" })
module Acnologia
  VERSION = "0.2.0"
  OUTCOMES = %w[ok rate_limited busy quota_exhausted invalid banned error].freeze
  # Longest wait_sec the server accepts.
  MAX_WAIT_SEC = 60
  # Cap on a report message built from an exception.
  MAX_REPORT_MESSAGE = 500
  # Header that carries the access password of a locked deploy.
  ACCESS_HEADER = "X-Acnologia-Access"
  # Where forward_proxy_env and save_forward_proxy_ca put the proxy CA by default.
  DEFAULT_CA_PATH = "acnologia-ca.pem"

  # Replace the lease secret (raw and URL encoded) with "[key]" and truncate.
  # Errors from query auth services often carry the full request URL, key
  # included, and a report must not copy it to the server.
  def self.scrub_message(message, secret)
    out = message.to_s
    if secret && !secret.empty?
      [secret, URI.encode_www_form_component(secret), percent_encode(secret)].uniq.each do |s|
        out = out.gsub(s, "[key]")
      end
    end
    out[0, MAX_REPORT_MESSAGE]
  end

  # RFC 3986 percent encoding (like PHP rawurlencode): every byte outside
  # A-Z a-z 0-9 - . _ ~ becomes %XX.
  def self.percent_encode(str)
    str.to_s.b.gsub(/[^A-Za-z0-9\-._~]/n) { |c| format("%%%02X", c.ord) }.force_encoding(Encoding::US_ASCII)
  end
  private_class_method :percent_encode

  # Seconds to wait from a Retry-After header value: a positive number of
  # seconds or an HTTP date. nil when absent, zero or unreadable.
  def self.parse_retry_after(value)
    v = value.to_s.strip
    return nil if v.empty?

    if v.match?(/\A\d+(\.\d+)?\z/)
      n = v.to_f.ceil
      return n.positive? ? n : nil
    end
    secs = (Time.httpdate(v) - Time.now).ceil
    secs.positive? ? secs : nil
  rescue ArgumentError
    nil
  end

  # Non 2xx response from the consumer API, or a transport failure (status 0).
  # retry_after is in seconds, or nil. body is the parsed JSON error body.
  class Error < StandardError
    attr_reader :status, :code, :retry_after, :body

    def initialize(message, status: 0, code: "", retry_after: nil, body: nil)
      super(message)
      @status = status
      @code = code
      @retry_after = retry_after
      @body = body
    end
  end

  # A transport failure because the connection or the answer took too long
  # (open_timeout or read_timeout). status is 0, code "timeout".
  class TimeoutError < Error
    def initialize(message = "request timed out")
      super(message, code: "timeout")
    end
  end

  # HTTP 401: the token is missing, wrong, revoked or expired.
  class Unauthenticated < Error
    def initialize(message = "unauthenticated", code: "unauthenticated", body: nil)
      super(message, status: 401, code: code, body: body)
    end
  end

  # HTTP 403: the app is disabled, or not allowed this service or mode
  # (service_not_allowed, proxy_not_allowed). Subclasses cover
  # lease_not_allowed and the share errors.
  class Forbidden < Error
    def initialize(message = "forbidden", code: "forbidden", body: nil)
      super(message, status: 403, code: code, body: body)
    end
  end

  # HTTP 403 lease_not_allowed: the app is proxy only, or the service is one
  # another Acnologia server shares with yours. Call it through the proxy.
  class LeaseNotAllowed < Forbidden
    def initialize(message = "lease not allowed", body: nil)
      super(message, code: "lease_not_allowed", body: body)
    end
  end

  # HTTP 403 for a share's token whose owner revoked, paused or let the
  # share expire. share_status is "revoked", "paused" or "expired".
  class ShareUnavailable < Forbidden
    attr_reader :share_status

    def initialize(message = "share unavailable", share_status: "", code: nil, body: nil)
      super(message, code: code || "share_#{share_status}", body: body)
      @share_status = share_status
    end
  end

  # HTTP 403 share_revoked: the owner revoked the share. It will not come back.
  class ShareRevoked < ShareUnavailable
    def initialize(message = "share revoked", body: nil)
      super(message, share_status: "revoked", body: body)
    end
  end

  # HTTP 403 share_paused: the owner paused the share. It may resume.
  class SharePaused < ShareUnavailable
    def initialize(message = "share paused", body: nil)
      super(message, share_status: "paused", body: body)
    end
  end

  # HTTP 403 share_expired: the share reached its end date.
  class ShareExpired < ShareUnavailable
    def initialize(message = "share expired", body: nil)
      super(message, share_status: "expired", body: body)
    end
  end

  # HTTP 429 app_limit_reached: the app used up a daily or monthly request
  # or budget limit. limit names it (daily_requests, monthly_requests,
  # daily_budget, monthly_budget), reset_at is when it resets (ISO 8601
  # string) and retry_after is the seconds until then.
  class AppLimitReached < Error
    attr_reader :limit, :reset_at

    def initialize(message = "app limit reached", limit: nil, reset_at: nil, retry_after: nil, body: nil)
      super(message, status: 429, code: "app_limit_reached", retry_after: retry_after, body: body)
      @limit = limit
      @reset_at = reset_at
    end

    # reset_at as a Time, or nil.
    def reset_time
      @reset_at ? Time.iso8601(@reset_at.to_s) : nil
    rescue ArgumentError
      nil
    end
  end

  # HTTP 502 upstream_blocked: the provider's address resolves somewhere the
  # server refuses to call (a cloud metadata address). Retrying will not help.
  class UpstreamBlocked < Error
    def initialize(message = "upstream blocked", body: nil)
      super(message, status: 502, code: "upstream_blocked", body: body)
    end
  end

  # HTTP 508 loop_detected: the request already passed through more than 3
  # Acnologia servers.
  class LoopDetected < Error
    def initialize(message = "loop detected", body: nil)
      super(message, status: 508, code: "loop_detected", body: body)
    end
  end

  # HTTP 503: no key can be leased right now. retry_after is in seconds, or nil.
  #
  # code is "no_key_available" when every key is cooling down, exhausted or
  # excluded, and "all_keys_busy" when every usable key is at its concurrency
  # limit (busy? is true and the AllKeysBusy subclass is raised).
  class NoKeyAvailable < Error
    def initialize(message = "no key available", retry_after: nil, code: "no_key_available", body: nil)
      super(message, status: 503, code: code, retry_after: retry_after, body: body)
    end

    # True when every usable key is at its concurrency limit (all_keys_busy).
    def busy?
      code == "all_keys_busy"
    end
  end

  # HTTP 503 all_keys_busy: usable keys exist but every one is at its
  # concurrency limit. Clears as soon as another caller reports a lease, so
  # retry after retry_after seconds (usually 1) or lease with wait_sec.
  # Subclasses NoKeyAvailable, so existing rescues still catch it.
  class AllKeysBusy < NoKeyAvailable
    def initialize(message = "all keys busy", retry_after: nil, body: nil)
      super(message, retry_after: retry_after, code: "all_keys_busy", body: body)
    end
  end

  # HTTP 503 service_unavailable: the provider keeps failing and its circuit
  # breaker is open, so Acnologia paused traffic to it. retry_after is in
  # seconds, or nil. Not a NoKeyAvailable: waiting for a key will not help.
  class ServiceUnavailable < Error
    def initialize(message = "service unavailable", retry_after: nil, status: 503, body: nil)
      super(message, status: status, code: "service_unavailable", retry_after: retry_after, body: body)
    end
  end

  # Build the typed error for an Acnologia error answer, or nil for a 2xx.
  #
  # status and the error code pick the class. by_status: false maps only
  # the Acnologia error codes, not a bare 401 or 403, which is right for a
  # proxied answer that may be the provider's own. secrets are replaced with
  # "[key]" in the message.
  def self.error_for(status, headers, body, by_status: true, secrets: [])
    return nil if status >= 200 && status < 300

    data = begin
      body.nil? || body.empty? ? nil : JSON.parse(body)
    rescue JSON::ParserError
      nil
    end
    data = nil unless data.is_a?(Hash)
    err = data && data["error"]
    code = ""
    message = "HTTP #{status}"
    if err.is_a?(Hash)
      code = err["code"].to_s
      message = err["message"].to_s unless err["message"].to_s.empty?
    elsif err.is_a?(String) && !err.empty?
      message = err
    end
    secrets.each { |s| message = scrub_message(message, s) if s && !s.empty? }
    hdrs = headers.is_a?(Hash) ? headers : {}
    retry_after = data && data["retry_after_sec"] ? data["retry_after_sec"].to_f.ceil : nil
    retry_after = nil if retry_after && retry_after <= 0
    retry_after ||= parse_retry_after(hdrs["retry-after"])
    share = hdrs["x-acnologia-share"].to_s
    code = "share_#{share}" if code.empty? && status == 403 && %w[revoked paused expired].include?(share)

    case code
    when "service_unavailable" then ServiceUnavailable.new(message, retry_after: retry_after, status: status, body: data)
    when "all_keys_busy" then AllKeysBusy.new(message, retry_after: retry_after, body: data)
    when "no_key_available" then NoKeyAvailable.new(message, retry_after: retry_after, body: data)
    when "app_limit_reached"
      AppLimitReached.new(message, limit: data && data["limit"], reset_at: data && data["reset_at"],
                                   retry_after: retry_after, body: data)
    when "share_revoked" then ShareRevoked.new(message, body: data)
    when "share_paused" then SharePaused.new(message, body: data)
    when "share_expired" then ShareExpired.new(message, body: data)
    when "lease_not_allowed" then LeaseNotAllowed.new(message, body: data)
    when "upstream_blocked" then UpstreamBlocked.new(message, body: data)
    when "loop_detected" then LoopDetected.new(message, body: data)
    else
      if status == 503 && code.empty? && by_status
        NoKeyAvailable.new(message, retry_after: retry_after, body: data)
      elsif status == 401 && by_status
        Unauthenticated.new(message, code: code.empty? ? "unauthenticated" : code, body: data)
      elsif status == 403 && by_status
        Forbidden.new(message, code: code.empty? ? "forbidden" : code, body: data)
      else
        Error.new(message, status: status, code: code, retry_after: retry_after, body: data)
      end
    end
  end

  # Raise a subclass inside with_key to report the key and retry with the next one.
  class KeyProblem < StandardError
    attr_reader :status_code, :retry_after

    def self.outcome
      "error"
    end

    def initialize(message = nil, status_code: nil, retry_after: nil)
      super(message || self.class.outcome)
      @status_code = status_code
      @retry_after = retry_after
    end

    def outcome
      self.class.outcome
    end
  end

  class RateLimited < KeyProblem
    def self.outcome
      "rate_limited"
    end
  end

  class QuotaExhausted < KeyProblem
    def self.outcome
      "quota_exhausted"
    end
  end

  class InvalidKey < KeyProblem
    def self.outcome
      "invalid"
    end
  end

  # One provider a capability or group call tried (X-Acnologia-Tried), with
  # the status it answered.
  Tried = Struct.new(:service, :status, keyword_init: true)

  # Parse an X-Acnologia-Tried value ("a=200, b=429") into Tried entries.
  def self.parse_tried(value)
    value.to_s.split(",").map(&:strip).reject(&:empty?).map do |pair|
      svc, _, code = pair.rpartition("=")
      svc.empty? ? Tried.new(service: code, status: nil).freeze : Tried.new(service: svc, status: code.to_i).freeze
    end
  end

  # Raw HTTP response. Header names are lower cased.
  Response = Struct.new(:status, :headers, :body) do
    def ok?
      status >= 200 && status < 300
    end

    def json
      JSON.parse(body)
    end

    def [](header)
      headers[header.to_s.downcase]
    end

    # Id of the key that served a proxied request (X-Acnologia-Key-Id), or nil.
    def key_id
      v = self["x-acnologia-key-id"]
      v.nil? || v.empty? ? nil : v.to_i
    end

    # How many keys the proxy tried (X-Acnologia-Attempts), or nil.
    def attempts
      v = self["x-acnologia-attempts"]
      v.nil? || v.empty? ? nil : v.to_i
    end

    # True when the proxy replayed a stored answer for a repeated Idempotency-Key.
    def replayed?
      self["idempotent-replayed"].to_s.casecmp("true").zero?
    end
    alias_method :replayed, :replayed?

    # Slug of the service that answered (X-Acnologia-Service), or nil. For a
    # group or capability call, the member that served it.
    def service
      header_value("x-acnologia-service")
    end

    # Cache result (X-Acnologia-Cache: "hit", "miss", "bypass"...), or nil.
    def cache
      header_value("x-acnologia-cache")
    end

    # Slug of the capability that served the call (X-Acnologia-Capability), or nil.
    def capability
      header_value("x-acnologia-capability")
    end

    # The providers a capability or group call tried, in order, as Tried
    # entries (service, status). Empty when the header is absent.
    def tried
      Acnologia.parse_tried(self["x-acnologia-tried"])
    end

    # Seconds from the Retry-After header, or nil.
    def retry_after
      Acnologia.parse_retry_after(self["retry-after"])
    end

    # The typed Acnologia error this answer carries, or nil for a 2xx. Only
    # Acnologia's own error codes map to a subclass, so a provider's 401 or
    # 403 relayed by the proxy stays a plain Acnologia::Error.
    def error
      Acnologia.error_for(status, headers, body, by_status: false)
    end

    # Raise #error unless the answer is 2xx. Returns self.
    def raise_for_status!
      e = error
      raise e if e

      self
    end

    private

    def header_value(name)
      v = self[name]
      v.nil? || v.empty? ? nil : v
    end
  end

  # What GET /api/v1/me says about this token. Every part is frozen.
  #
  # app: AppInfo (id, name). limits: Limits (daily_requests,
  # monthly_requests, daily_budget, monthly_budget, nil when unset).
  # usage: Usage (today_requests, today_cost, month_requests, month_cost,
  # last_used_at, limit_hit). limit_hit: LimitHit (limit, reset_at) or nil.
  # services: Array of MeService. share: Share or nil for an ordinary app.
  # raw: the parsed JSON.
  class Me
    AppInfo = Struct.new(:id, :name, keyword_init: true)
    Limits = Struct.new(:daily_requests, :monthly_requests, :daily_budget, :monthly_budget, keyword_init: true)
    LimitHit = Struct.new(:limit, :reset_at, keyword_init: true)
    Usage = Struct.new(:today_requests, :today_cost, :month_requests, :month_cost, :last_used_at, :limit_hit,
                       keyword_init: true)
    MeService = Struct.new(:slug, :name, :provider, :category, :available, :logo_url, keyword_init: true) do
      # True when a key could serve a request now.
      def available?
        available.to_i.positive?
      end
    end
    Owner = Struct.new(:name, :id, keyword_init: true)
    Share = Struct.new(:id, :name, :owner, :recipient, :label, :status, :mode, :ends_at, keyword_init: true) do
      # True while the share works (status "active").
      def active?
        status == "active"
      end
    end

    attr_reader :app, :limits, :usage, :limit_hit, :services, :share, :raw

    def initialize(data)
      data = {} unless data.is_a?(Hash)
      @raw = data.freeze
      app = hash(data["app"])
      @app = AppInfo.new(id: app["id"], name: app["name"].to_s).freeze
      lim = hash(data["limits"])
      @limits = Limits.new(daily_requests: lim["daily_requests"], monthly_requests: lim["monthly_requests"],
                           daily_budget: lim["daily_budget"], monthly_budget: lim["monthly_budget"]).freeze
      use = hash(data["usage"])
      @usage = Usage.new(today_requests: use["today_requests"].to_i, today_cost: use["today_cost"].to_f,
                         month_requests: use["month_requests"].to_i, month_cost: use["month_cost"].to_f,
                         last_used_at: use["last_used_at"], limit_hit: limit_hit_from(use["limit_hit"])).freeze
      @limit_hit = limit_hit_from(data.key?("limit_hit") ? data["limit_hit"] : use["limit_hit"])
      @services = Array(data["services"]).select { |s| s.is_a?(Hash) }.map do |s|
        MeService.new(slug: s["slug"].to_s, name: s["name"].to_s, provider: s["provider"].to_s,
                      category: s["category"].to_s, available: available_count(s["available"]),
                      logo_url: s["logo_url"]).freeze
      end.freeze
      @share = share_from(data["share"])
    end

    # True when the token belongs to a share (another server's or a person's).
    def share?
      !@share.nil?
    end

    # True when the app reached one of its request or budget limits.
    def limited?
      !@limit_hit.nil?
    end

    def inspect
      "#<Acnologia::Me app=#{@app.name.inspect} services=#{@services.map(&:slug).inspect} " \
        "share=#{@share ? @share.status.inspect : 'nil'} limited=#{limited?}>"
    end
    alias to_s inspect

    private

    def hash(v)
      v.is_a?(Hash) ? v : {}
    end

    # services[].available is an integer here (free keys for a share's
    # token, 1 or 0 for an app). A boolean from an older server counts as 1 or 0.
    def available_count(v)
      return v ? 1 : 0 if v == true || v == false

      v.to_i
    end

    def limit_hit_from(v)
      return nil unless v.is_a?(Hash)

      LimitHit.new(limit: v["limit"].to_s, reset_at: v["reset_at"]).freeze
    end

    def share_from(v)
      return nil unless v.is_a?(Hash)

      o = hash(v["owner"])
      owner = Owner.new(name: (o["name"] || v["owner_name"]).to_s, id: (o["id"] || v["owner_id"]).to_s).freeze
      Share.new(id: v["id"].to_s, name: v["name"].to_s, owner: owner, recipient: v["recipient"].to_s,
                label: v["label"].to_s, status: v["status"].to_s, mode: v["mode"].to_s, ends_at: v["ends_at"]).freeze
    end
  end

  # A service's usage guide from GET /api/v1/services/{slug}/guide.
  # markdown: the guide as Markdown. generated: true when the server built it
  # from the provider spec because nobody wrote one. guide: the structured
  # guide as parsed JSON.
  Guide = Struct.new(:markdown, :generated, :guide, keyword_init: true) do
    def generated?
      generated == true
    end
  end

  # A capability from GET /api/v1/capabilities: one call served by whichever
  # provider can. params is the parsed JSON params description; routes lists
  # Route (service, mapped); url is where to POST it.
  Capability = Struct.new(:slug, :name, :description, :strategy, :params, :routes, :url, keyword_init: true) do
    def self.from(h)
      h = {} unless h.is_a?(Hash)
      routes = Array(h["routes"]).select { |r| r.is_a?(Hash) }.map do |r|
        self::Route.new(service: r["service"].to_s, mapped: r["mapped"] == true).freeze
      end.freeze
      new(slug: h["slug"].to_s, name: h["name"].to_s, description: h["description"].to_s,
          strategy: h["strategy"].to_s, params: h["params"], routes: routes, url: h["url"].to_s).freeze
    end
  end
  # One provider a capability can use. mapped is true when its answer is
  # mapped to the capability's common output.
  Capability::Route = Struct.new(:service, :mapped, keyword_init: true)

  # How to use the forward proxy, from GET /api/v1/proxy. proxy_url_with_token
  # and env carry a "<app token>" placeholder, never the token itself, so
  # printing this is safe. Use Client#forward_proxy_url or
  # Client#forward_proxy_env for values with the real token.
  class ForwardProxy
    Host = Struct.new(:host, :service, :name, :path, keyword_init: true)

    attr_reader :proxy_url, :proxy_url_with_token, :ca_url, :ca_fingerprint, :passthrough, :hosts, :env,
                :service_header, :raw

    def initialize(data)
      data = {} unless data.is_a?(Hash)
      @raw = data.freeze
      @proxy_url = data["proxy_url"].to_s
      @proxy_url_with_token = data["proxy_url_with_token"].to_s
      @ca_url = data["ca_url"].to_s
      @ca_fingerprint = data["ca_fingerprint"].to_s
      @passthrough = data["passthrough"] == true
      @hosts = Array(data["hosts"]).select { |h| h.is_a?(Hash) }.map do |h|
        Host.new(host: h["host"].to_s, service: h["service"].to_s, name: h["name"].to_s, path: h["path"].to_s).freeze
      end.freeze
      env = data["env"].is_a?(Hash) ? data["env"] : {}
      @env = env.each_with_object({}) { |(k, v), o| o[k.to_s] = v.to_s }.freeze
      @service_header = data["service_header"].to_s
    end

    # True when hosts outside the list are tunneled through untouched.
    def passthrough?
      @passthrough
    end

    def inspect
      "#<Acnologia::ForwardProxy proxy_url=#{@proxy_url.inspect} hosts=#{@hosts.size} passthrough=#{@passthrough}>"
    end
    alias to_s inspect
  end

  # A key handed out by Acnologia.
  class Lease
    attr_reader :lease_id, :key_id, :service, :secret, :auth, :base_url, :expires_at, :exclusive
    attr_accessor :reported

    def initialize(client, data)
      @client = client
      @lease_id = data["lease_id"].to_s
      @key_id = data["key_id"].to_i
      @service = data["service"].to_s
      @secret = data["secret"].to_s
      auth = data["auth"].is_a?(Hash) ? data["auth"] : {}
      @auth = { "type" => auth["type"].to_s, "name" => auth["name"].to_s, "prefix" => auth["prefix"].to_s }.freeze
      @base_url = data["base_url"].to_s
      @expires_at = data["expires_at"]
      @exclusive = data["exclusive"] == true
      @reported = false
    end

    # True when the key is reserved for this lease only.
    def exclusive?
      @exclusive
    end

    # Extend this open lease so a long job keeps its key. nil uses the server
    # default (120 seconds); the server caps it at 24 hours. Returns the new
    # expires_at and updates this lease's expires_at. Raises Acnologia::Error
    # with code "lease_not_found" when the lease is no longer open.
    def renew(ttl_sec: nil)
      @expires_at = @client.renew(self, ttl_sec: ttl_sec)
    end

    # Report the outcome. Returns the next Lease when rotate is true and a key is free.
    def report(outcome, status_code: nil, retry_after_sec: nil, message: nil, rotate: false)
      nxt = @client.report_lease(@lease_id, outcome: outcome, status_code: status_code,
                                            retry_after_sec: retry_after_sec, message: message, rotate: rotate)
      @reported = true
      nxt
    end

    # Return [query, headers] copies with the secret placed where auth.type says.
    # A non empty auth "prefix" goes in front of the secret for every type; for
    # bearer it replaces the default "Bearer " prefix.
    def apply(query: {}, headers: {})
      q = query.dup
      h = headers.dup
      name = @auth["name"]
      prefix = @auth["prefix"].to_s
      value = "#{prefix}#{@secret}"
      case @auth["type"]
      when "query" then q[name] = value
      when "header" then h[name.empty? ? "Authorization" : name] = value
      when "bearer" then h[name.empty? ? "Authorization" : name] = prefix.empty? ? "Bearer #{@secret}" : value
      else raise ArgumentError, "unknown auth type #{@auth['type'].inspect}"
      end
      [q, h]
    end

    # Join base_url and path, with an optional query hash.
    def url(path = "", query = {})
      u = "#{@base_url.chomp('/')}/#{path.to_s.sub(%r{\A/+}, '')}"
      query.empty? ? u : "#{u}?#{URI.encode_www_form(query)}"
    end

    def inspect
      "#<Acnologia::Lease lease_id=#{@lease_id.inspect} key_id=#{@key_id} service=#{@service.inspect} secret=***>"
    end
    alias to_s inspect
  end

  # Sends requests through /proxy/{service}/. Non 2xx responses are returned,
  # never raised; call Response#raise_for_status! to raise the typed error.
  class Proxy
    attr_reader :service

    # Methods that are safe to send again after a 503 or 429 without an
    # Idempotency-Key.
    RETRY_SAFE = %w[GET HEAD OPTIONS PUT DELETE].freeze

    def initialize(client, service)
      @client = client
      @service = service
    end

    def url(path = "", query = {})
      u = "#{@client.base_url}/proxy/#{URI.encode_www_form_component(@service)}/#{path.to_s.sub(%r{\A/+}, '')}"
      return u if query.nil? || query.empty?

      u + (u.include?("?") ? "&" : "?") + URI.encode_www_form(query)
    end

    # no_cache: true sends Cache-Control: no-cache, so a service with a cache
    # TTL calls the provider instead of answering from its cache.
    def get(path, query = {}, headers: {}, no_cache: false, **params)
      request("GET", path, query: merge_query(query, params), headers: headers, no_cache: no_cache)
    end

    def head(path, query = {}, headers: {}, no_cache: false, **params)
      request("HEAD", path, query: merge_query(query, params), headers: headers, no_cache: no_cache)
    end

    def options(path, query = {}, headers: {}, no_cache: false, **params)
      request("OPTIONS", path, query: merge_query(query, params), headers: headers, no_cache: no_cache)
    end

    # idempotency_key: a String is sent as the Idempotency-Key header, true
    # sends a new random one. The proxy then answers a repeat of the same
    # request with the stored response for 10 minutes instead of calling the
    # provider again (Response#replayed?).
    def post(path, body = nil, headers: {}, query: {}, idempotency_key: nil, no_cache: false)
      request("POST", path, query: query, headers: headers, body: body, idempotency_key: idempotency_key,
                            no_cache: no_cache)
    end

    def put(path, body = nil, headers: {}, query: {}, idempotency_key: nil, no_cache: false)
      request("PUT", path, query: query, headers: headers, body: body, idempotency_key: idempotency_key,
                           no_cache: no_cache)
    end

    def patch(path, body = nil, headers: {}, query: {}, idempotency_key: nil, no_cache: false)
      request("PATCH", path, query: query, headers: headers, body: body, idempotency_key: idempotency_key,
                             no_cache: no_cache)
    end

    def delete(path, query = {}, headers: {}, no_cache: false, **params)
      request("DELETE", path, query: merge_query(query, params), headers: headers, no_cache: no_cache)
    end

    # body: Hash or Array is sent as JSON, String as is.
    #
    # When the client has max_retries, a 503 from Acnologia (no key, all
    # busy, circuit open) or a 429 app_limit_reached is sent again after its
    # Retry-After, but only for methods in RETRY_SAFE or a request with an
    # Idempotency-Key (the same key on every attempt).
    def request(method, path, query: {}, headers: {}, body: nil, idempotency_key: nil, no_cache: false)
      send_to(method, url(path, query), headers: headers, body: body, idempotency_key: idempotency_key,
                                        no_cache: no_cache)
    end

    # @api private
    # Send to a full proxy URL, with the same body, header and retry rules as #request.
    def send_to(method, target, headers: {}, body: nil, idempotency_key: nil, no_cache: false)
      method = method.to_s.upcase
      h = headers.dup
      if !body.nil? && !body.is_a?(String)
        body = JSON.generate(body)
        h["Content-Type"] = "application/json" unless header?(h, "content-type")
      end
      idempotency_key = true if idempotency_key.nil? && @client.idempotency && %w[POST PATCH].include?(method)
      if idempotency_key && !header?(h, "idempotency-key")
        h["Idempotency-Key"] = idempotency_key == true ? SecureRandom.uuid : idempotency_key.to_s
      end
      h["Cache-Control"] = "no-cache" if no_cache && !header?(h, "cache-control")
      h["Authorization"] = "Bearer #{@client.token}"
      retry_ok = RETRY_SAFE.include?(method) || header?(h, "idempotency-key")
      @client.with_retries(retry_ok) do
        res = @client.send_request(method, target, h, body)
        [res, retryable_error(res)]
      end
    end

    private

    # On Ruby 3 a query hash written without braces, get("/x", "q" => "a"),
    # arrives as keywords; fold it back into the query.
    def merge_query(query, params)
      return query if params.empty?

      (query || {}).merge(params)
    end

    def header?(h, name)
      h.keys.any? { |k| k.to_s.casecmp(name).zero? }
    end

    def retryable_error(res)
      return nil unless res.status == 503 || res.status == 429

      e = res.error
      e if e.is_a?(NoKeyAvailable) || e.is_a?(ServiceUnavailable) || e.is_a?(AppLimitReached)
    end
  end

  class Client
    attr_reader :base_url, :token, :open_timeout, :read_timeout, :max_retries, :max_retry_wait, :idempotency

    # base_url and token fall back to ACNOLOGIA_URL and ACNOLOGIA_TOKEN.
    #
    # timeout: seconds for both open_timeout (connect) and read_timeout
    # unless those are given. A lease with wait_sec gets a read timeout long
    # enough to cover the wait.
    #
    # access_password: sent as X-Acnologia-Access for a deploy behind the
    # access password. The consumer routes pass the lock with the app token
    # alone, so this is rarely needed. nil reads ACNOLOGIA_ACCESS_PASSWORD;
    # false never sends it. Without either, no such header is sent.
    #
    # max_retries: how many times a 503 (no key, all busy, circuit open) or
    # 429 app_limit_reached is retried after sleeping its Retry-After
    # (1 second when the server gives none). Default 0. A wait longer than
    # max_retry_wait seconds is not slept; the error is raised.
    #
    # idempotency: true sends a new Idempotency-Key with every proxied POST
    # and PATCH that has none.
    #
    # headers: extra headers sent with every request.
    #
    # transport: optional callable(method, url, headers, body) -> Response, for tests.
    # sleeper: optional callable(seconds) used between retries, for tests.
    def initialize(base_url = nil, token = nil, timeout: 30, open_timeout: nil, read_timeout: nil,
                   access_password: nil, max_retries: 0, max_retry_wait: 60, idempotency: false,
                   headers: {}, transport: nil, sleeper: nil)
      base_url = ENV["ACNOLOGIA_URL"] if base_url.nil? || base_url.to_s.empty?
      token = ENV["ACNOLOGIA_TOKEN"] if token.nil? || token.to_s.empty?
      raise ArgumentError, "base_url is required (or set ACNOLOGIA_URL)" if base_url.nil? || base_url.empty?
      raise ArgumentError, "token is required (or set ACNOLOGIA_TOKEN)" if token.nil? || token.empty?
      raise ArgumentError, "max_retries must be 0 or more" if max_retries.negative?

      @base_url = base_url.chomp("/")
      @token = token
      @open_timeout = open_timeout || timeout
      @read_timeout = read_timeout || timeout
      @access_password = access_password == false ? nil : (access_password || ENV["ACNOLOGIA_ACCESS_PASSWORD"])
      @access_password = nil if @access_password.to_s.empty?
      @max_retries = max_retries
      @max_retry_wait = max_retry_wait
      @idempotency = idempotency ? true : false
      @extra_headers = headers.dup.freeze
      @transport = transport || method(:net_http)
      @sleeper = sleeper || ->(secs) { sleep(secs) }
    end

    # True when requests carry the X-Acnologia-Access header.
    def access_password?
      !@access_password.nil?
    end

    def inspect
      "#<Acnologia::Client base_url=#{@base_url.inspect} token=***#{access_password? ? ' access=***' : ''}>"
    end
    alias to_s inspect

    # Services this token may use, as hashes from GET /api/v1/services:
    # slug, name, provider, category, available, base_url, auth (type, name,
    # prefix) and guide_url.
    def services
      call("GET", "/api/v1/services", retry_ok: true).fetch("services", [])
    end

    # The token's own access: app, limits, usage, services and, for a
    # share's token, the share. Returns an Acnologia::Me. A share token whose
    # share is paused, expired or revoked still gets an answer here, with
    # that share status and no services.
    def me
      Me.new(call("GET", "/api/v1/me", retry_ok: true))
    end

    # The usage guide of a service this token may use, as Markdown. Same as
    # guide_markdown; guide_doc returns the structured guide too.
    def guide(service)
      path = "/api/v1/services/#{escape(service)}/guide"
      res = request_raw("GET", path, nil, accept: "text/markdown")
      raise error_for(res) unless res.ok?

      res.body.to_s
    end

    # The usage guide of a service as Markdown (GET .../guide with
    # Accept: text/markdown).
    def guide_markdown(service)
      guide(service)
    end

    # The usage guide of a service as an Acnologia::Guide: markdown,
    # generated (true when the server built it from the provider spec) and
    # guide (the structured guide as parsed JSON).
    def guide_doc(service)
      data = call("GET", "/api/v1/services/#{escape(service)}/guide", retry_ok: true)
      Guide.new(markdown: data["markdown"].to_s, generated: data["generated"] == true, guide: data["guide"]).freeze
    end

    # Capabilities this token may call (GET /api/v1/capabilities), as
    # Acnologia::Capability values. A share's token gets none.
    def capabilities
      Array(call("GET", "/api/v1/capabilities", retry_ok: true)["capabilities"]).map { |c| Capability.from(c) }
    end

    # Call a capability: POST /proxy/{slug} with params as the JSON body.
    # service: use only that provider (?_service=). raw: true returns the
    # provider's own body, not the mapped output (?_raw=1). Returns the
    # Acnologia::Response, never raises for a non 2xx (see
    # Response#raise_for_status!, Response#tried and Response#capability).
    def capability(slug, params = nil, service: nil, raw: false, headers: {}, idempotency_key: nil, no_cache: false)
      query = {}
      query["_service"] = service.to_s if service && !service.to_s.empty?
      query["_raw"] = "1" if raw
      target = "#{@base_url}/proxy/#{escape(slug)}"
      target += "?#{URI.encode_www_form(query)}" unless query.empty?
      Proxy.new(self, slug).send_to("POST", target, headers: headers, body: params || {},
                                                    idempotency_key: idempotency_key, no_cache: no_cache)
    end

    # How to use the forward proxy (GET /api/v1/proxy), as an
    # Acnologia::ForwardProxy. Its strings carry a "<app token>" placeholder,
    # never the token.
    def forward_proxy
      ForwardProxy.new(call("GET", "/api/v1/proxy", retry_ok: true))
    end

    # The forward proxy's CA certificate as PEM text
    # (GET /api/v1/proxy/ca.pem). The route is public: no token is sent.
    def forward_proxy_ca
      res = send_request("GET", "#{@base_url}/api/v1/proxy/ca.pem", { "Accept" => "application/x-pem-file" }, nil)
      raise error_for(res) unless res.ok?

      res.body.to_s
    end

    # Download the forward proxy's CA certificate and write it to path.
    # Returns path.
    def save_forward_proxy_ca(path = DEFAULT_CA_PATH)
      File.write(path, forward_proxy_ca)
      path
    end

    # The forward proxy URL with user "app" and this client's token as the
    # password: http://app:acn_...@host:8888. It contains the token: keep it
    # out of logs.
    def forward_proxy_url
      proxy_url_with_token(forward_proxy.proxy_url)
    end

    # Environment for a process that should reach providers through the
    # forward proxy: HTTPS_PROXY and HTTP_PROXY (forward_proxy_url),
    # NODE_USE_ENV_PROXY=1, and NODE_EXTRA_CA_CERTS, REQUESTS_CA_BUNDLE and
    # SSL_CERT_FILE set to ca_path. The proxy values contain the token: keep
    # them out of logs. Save the CA there with save_forward_proxy_ca.
    def forward_proxy_env(ca_path = DEFAULT_CA_PATH)
      url = forward_proxy_url
      {
        "HTTPS_PROXY" => url,
        "HTTP_PROXY" => url,
        "NODE_USE_ENV_PROXY" => "1",
        "NODE_EXTRA_CA_CERTS" => ca_path,
        "REQUESTS_CA_BUNDLE" => ca_path,
        "SSL_CERT_FILE" => ca_path
      }
    end

    # A Net::HTTP for host:port that goes through the forward proxy, with
    # this client's token as the proxy password and the system roots plus
    # the Acnologia CA trusted. Call the provider's own URL with it and
    # Acnologia puts the key on the request. ca_path: a saved CA file;
    # nil downloads it. use_ssl defaults to true unless port is 80. The
    # returned object holds the token.
    #
    #   http = acn.forward_proxy_http("api.openai.com")
    #   res = http.start { |h| h.get("/v1/models") }
    def forward_proxy_http(host, port = 443, use_ssl: port != 80, ca_path: nil)
      proxy = URI(forward_proxy.proxy_url)
      store = OpenSSL::X509::Store.new
      store.set_default_paths
      if ca_path
        store.add_file(ca_path)
      else
        store.add_cert(OpenSSL::X509::Certificate.new(forward_proxy_ca))
      end
      http = Net::HTTP.new(host, port, proxy.host, proxy.port, "app", @token)
      http.use_ssl = use_ssl
      http.cert_store = store
      http.open_timeout = @open_timeout
      http.read_timeout = @read_timeout
      http
    end

    # Borrow a key.
    #
    # wait_sec (0 to 60) lets the server wait that long for a free slot when
    # every usable key is at its concurrency limit. Raises AllKeysBusy (a
    # NoKeyAvailable) if none frees up in time, and NoKeyAvailable when the
    # pool is drained.
    #
    # Always report the lease: on services with a concurrency limit it holds
    # one of the key's slots until it is reported or its TTL expires. With a
    # block, the lease is yielded, reported ("ok", or the outcome of a
    # KeyProblem, or "error") unless the block reported it, and the block's
    # value is returned.
    #
    # exclusive: true keeps the key from every other caller until the lease
    # is reported or expires.
    def lease(service, exclude_key_ids: nil, ttl_sec: nil, wait_sec: nil, exclusive: false)
      body = { "service" => service }
      body["exclude_key_ids"] = exclude_key_ids if exclude_key_ids && !exclude_key_ids.empty?
      body["ttl_sec"] = ttl_sec.to_i unless ttl_sec.nil?
      if wait_sec && wait_sec != 0
        raise ArgumentError, "wait_sec must be between 0 and #{MAX_WAIT_SEC}" if wait_sec.negative? || wait_sec > MAX_WAIT_SEC

        body["wait_sec"] = wait_sec.ceil
      end
      body["exclusive"] = true if exclusive
      l = Lease.new(self, call("POST", "/api/v1/lease", body, retry_ok: true))
      return l unless block_given?

      use_lease(l) { yield l }
    end

    # Lease a key, yield it, report the outcome and return the block's value.
    # Raise RateLimited, QuotaExhausted or InvalidKey in the block to rotate.
    #
    # Every lease is reported, whatever the block does (including raising any
    # exception). On services with a concurrency limit an unreported lease
    # keeps one of the key's slots busy until its TTL expires. ttl_sec,
    # wait_sec and exclusive are passed to every lease call.
    def with_key(service, max_attempts: 5, ttl_sec: nil, wait_sec: nil, exclusive: false)
      raise ArgumentError, "max_attempts must be at least 1" if max_attempts < 1

      tried = []
      lease = self.lease(service, ttl_sec: ttl_sec, wait_sec: wait_sec, exclusive: exclusive)
      attempt = 0
      loop do
        attempt += 1
        tried << lease.key_id
        current = lease
        returned = false
        begin
          result = yield current
          returned = true
        rescue KeyProblem => e
          last = attempt >= max_attempts
          nxt = current.report(e.outcome, status_code: e.status_code, retry_after_sec: e.retry_after,
                                          message: Acnologia.scrub_message(e.message, current.secret), rotate: !last)
          raise if last

          lease = nxt || self.lease(service, exclude_key_ids: tried, ttl_sec: ttl_sec, wait_sec: wait_sec,
                                          exclusive: exclusive)
          next
        rescue Exception => e # rubocop:disable Lint/RescueException
          begin
            current.report("error", message: Acnologia.scrub_message("#{e.class}: #{e.message}", current.secret))
          rescue StandardError
            nil # never mask the caller's exception
          end
          raise
        ensure
          # break, a non local return or throw in the block skips the code
          # below; still release the lease so its concurrency slot frees up.
          if !returned && !current.reported && $!.nil?
            begin
              current.report("ok")
            rescue StandardError
              nil
            end
          end
        end
        lease.report("ok")
        return result
      end
    end

    def proxy(service)
      Proxy.new(self, service)
    end

    # Extend an open lease. nil uses the server default (120 seconds); the
    # server caps it at 24 hours. Returns the new expires_at. Prefer
    # Lease#renew, which also updates the lease's expires_at.
    def renew(lease, ttl_sec: nil)
      id = lease.is_a?(Lease) ? lease.lease_id : lease.to_s
      body = {}
      body["ttl_sec"] = ttl_sec.ceil if ttl_sec && ttl_sec.positive?
      data = call("POST", "/api/v1/leases/#{escape(id)}/renew", body, retry_ok: false)
      exp = data["expires_at"]
      raise Error.new("renew response has no expires_at") if exp.nil? || exp.to_s.empty?

      exp
    end

    # @api private
    def report_lease(lease_id, outcome:, status_code: nil, retry_after_sec: nil, message: nil, rotate: false)
      body = { "outcome" => outcome, "rotate" => rotate ? true : false }
      body["status_code"] = status_code.to_i unless status_code.nil?
      body["retry_after_sec"] = retry_after_sec.ceil unless retry_after_sec.nil?
      body["message"] = message if message && !message.empty?
      data = call("POST", "/api/v1/leases/#{escape(lease_id)}/report", body)
      data["next"].is_a?(Hash) ? Lease.new(self, data["next"]) : nil
    end

    # @api private
    def send_request(method, url, headers, body)
      h = @extra_headers.merge(headers)
      h[ACCESS_HEADER] = @access_password if @access_password
      @transport.call(method, url, h.merge("User-Agent" => "acnologia-ruby/#{VERSION}"), body)
    end

    # @api private
    # Runs the block (which returns [result, retryable_error_or_nil]) and
    # retries while the error is retryable, retry_ok is true, attempts
    # remain and its wait fits max_retry_wait. Returns the last result.
    def with_retries(retry_ok)
      attempt = 0
      loop do
        result, err = yield
        return result if err.nil? || !retry_ok || attempt >= @max_retries

        wait = err.retry_after || 1
        return result if wait > @max_retry_wait

        attempt += 1
        @sleeper.call(wait)
      end
    end

    private

    def proxy_url_with_token(raw)
      u = URI(raw)
      u.userinfo = "app:#{Acnologia.send(:percent_encode, @token)}"
      u.to_s
    rescue URI::Error => e
      raise Error.new("bad proxy_url from the server: #{scrub(e.message)}")
    end

    def escape(segment)
      URI.encode_www_form_component(segment.to_s).gsub("+", "%20")
    end

    def secrets
      [@token, @access_password].compact
    end

    def error_for(res)
      Acnologia.error_for(res.status, res.headers, res.body.to_s, secrets: secrets)
    end

    def request_raw(method, path, json, accept: "application/json")
      headers = { "Authorization" => "Bearer #{@token}", "Accept" => accept }
      body = nil
      unless json.nil?
        headers["Content-Type"] = "application/json"
        body = JSON.generate(json)
      end
      send_request(method, @base_url + path, headers, body)
    end

    def call(method, path, json = nil, retry_ok: false)
      res = with_retries(retry_ok) do
        r = request_raw(method, path, json)
        e = r.ok? ? nil : error_for(r)
        retryable = e.is_a?(NoKeyAvailable) || e.is_a?(ServiceUnavailable) || e.is_a?(AppLimitReached)
        [[r, e], retryable ? e : nil]
      end
      r, e = res
      raise e if e

      data = begin
        r.body.nil? || r.body.empty? ? {} : JSON.parse(r.body)
      rescue JSON::ParserError
        {}
      end
      data.is_a?(Hash) ? data : {}
    end

    # Yield the lease and report it once, like with_key without rotation.
    def use_lease(lease)
      result = yield
      lease.report("ok") unless lease.reported
      result
    rescue KeyProblem => e
      unless lease.reported
        begin
          lease.report(e.outcome, status_code: e.status_code, retry_after_sec: e.retry_after,
                                  message: Acnologia.scrub_message(e.message, lease.secret))
        rescue StandardError
          nil
        end
      end
      raise
    rescue Exception => e # rubocop:disable Lint/RescueException
      unless lease.reported
        begin
          lease.report("error", message: Acnologia.scrub_message("#{e.class}: #{e.message}", lease.secret))
        rescue StandardError
          nil
        end
      end
      raise
    end

    def net_http(method, url, headers, body)
      uri = URI(url)
      http = Net::HTTP.new(uri.host, uri.port)
      http.use_ssl = uri.scheme == "https"
      http.open_timeout = @open_timeout
      wait = lease_wait(uri, body)
      http.read_timeout = wait.positive? ? [@read_timeout, wait + 10].max : @read_timeout
      http.write_timeout = @read_timeout if http.respond_to?(:write_timeout=)
      req = Net::HTTPGenericRequest.new(method, !body.nil?, method != "HEAD", uri.request_uri, headers)
      req.body = body unless body.nil?
      res = http.request(req)
      hdrs = {}
      res.each_header { |k, v| hdrs[k.downcase] = v }
      Response.new(res.code.to_i, hdrs, res.body.to_s)
    rescue Net::OpenTimeout, Net::ReadTimeout, Net::WriteTimeout => e
      raise TimeoutError.new(scrub("request timed out: #{e.message}"))
    rescue StandardError => e
      raise Error.new(scrub("request failed: #{e.message}"))
    end

    def scrub(message)
      secrets.reduce(message.to_s) { |m, s| Acnologia.scrub_message(m, s) }
    end

    # wait_sec of a lease request body, so the read timeout outlasts the server side wait.
    def lease_wait(uri, body)
      return 0 unless body && uri.path.end_with?("/api/v1/lease")

      JSON.parse(body).fetch("wait_sec", 0).to_i
    rescue JSON::ParserError
      0
    end
  end
end
