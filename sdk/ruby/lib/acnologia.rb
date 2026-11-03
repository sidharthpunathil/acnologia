# frozen_string_literal: true

require "json"
require "net/http"
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
                      category: s["category"].to_s, available: s["available"].to_i, logo_url: s["logo_url"]).freeze
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

    def get(path, query = {}, headers: {}, **params)
      request("GET", path, query: merge_query(query, params), headers: headers)
    end

    def head(path, query = {}, headers: {}, **params)
      request("HEAD", path, query: merge_query(query, params), headers: headers)
    end

    def options(path, query = {}, headers: {}, **params)
      request("OPTIONS", path, query: merge_query(query, params), headers: headers)
    end

    # idempotency_key: a String is sent as the Idempotency-Key header, true
    # sends a new random one. The proxy then answers a repeat of the same
    # request with the stored response for 10 minutes instead of calling the
    # provider again (Response#replayed?).
    def post(path, body = nil, headers: {}, query: {}, idempotency_key: nil)
      request("POST", path, query: query, headers: headers, body: body, idempotency_key: idempotency_key)
    end

    def put(path, body = nil, headers: {}, query: {}, idempotency_key: nil)
      request("PUT", path, query: query, headers: headers, body: body, idempotency_key: idempotency_key)
    end

    def patch(path, body = nil, headers: {}, query: {}, idempotency_key: nil)
      request("PATCH", path, query: query, headers: headers, body: body, idempotency_key: idempotency_key)
    end

    def delete(path, query = {}, headers: {}, **params)
      request("DELETE", path, query: merge_query(query, params), headers: headers)
    end

    # body: Hash or Array is sent as JSON, String as is.
    #
    # When the client has max_retries, a 503 from Acnologia (no key, all
    # busy, circuit open) or a 429 app_limit_reached is sent again after its
    # Retry-After, but only for methods in RETRY_SAFE or a request with an
    # Idempotency-Key (the same key on every attempt).
    def request(method, path, query: {}, headers: {}, body: nil, idempotency_key: nil)
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
      h["Authorization"] = "Bearer #{@client.token}"
      retry_ok = RETRY_SAFE.include?(method) || header?(h, "idempotency-key")
      u = url(path, query)
      @client.with_retries(retry_ok) do
        res = @client.send_request(method, u, h, body)
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

    # Services this token may use, as hashes from GET /api/v1/services.
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

    # The usage guide of a service this token may use, as Markdown.
    def guide(service)
      path = "/api/v1/services/#{escape(service)}/guide"
      res = request_raw("GET", path, nil, accept: "text/markdown")
      raise error_for(res) unless res.ok?

      res.body.to_s
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
      data = call("POST", "/api/v1/leases/#{escape(id)}/renew", body)
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
