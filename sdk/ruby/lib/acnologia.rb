# frozen_string_literal: true

require "json"
require "net/http"
require "uri"

# Client for Acnologia, a self-hosted API key pool with automatic rotation.
#
#   acn = Acnologia::Client.new("http://localhost:8080", "acn_...")
#   res = acn.proxy("serpapi").get("/search.json", q: "coffee")
module Acnologia
  VERSION = "0.1.0"
  OUTCOMES = %w[ok rate_limited busy quota_exhausted invalid banned error].freeze
  # Longest wait_sec the server accepts.
  MAX_WAIT_SEC = 60
  # Cap on a report message built from an exception.
  MAX_REPORT_MESSAGE = 500

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

  # Non 2xx response from the consumer API, or a transport failure (status 0).
  class Error < StandardError
    attr_reader :status, :code

    def initialize(message, status: 0, code: "")
      super(message)
      @status = status
      @code = code
    end
  end

  # HTTP 503: no key can be leased right now. retry_after is in seconds, or nil.
  #
  # code is "no_key_available" when every key is cooling down, exhausted or
  # excluded, and "all_keys_busy" when every usable key is at its concurrency
  # limit (busy? is true and the AllKeysBusy subclass is raised).
  class NoKeyAvailable < Error
    attr_reader :retry_after

    def initialize(message = "no key available", retry_after: nil, code: "no_key_available")
      super(message, status: 503, code: code)
      @retry_after = retry_after
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
    def initialize(message = "all keys busy", retry_after: nil)
      super(message, retry_after: retry_after, code: "all_keys_busy")
    end
  end

  # HTTP 503 service_unavailable: the provider keeps failing and its circuit
  # breaker is open, so Acnologia paused traffic to it. retry_after is in
  # seconds, or nil. Not a NoKeyAvailable: waiting for a key will not help.
  class ServiceUnavailable < Error
    attr_reader :retry_after

    def initialize(message = "service unavailable", retry_after: nil, status: 503)
      super(message, status: status, code: "service_unavailable")
      @retry_after = retry_after
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

  # Sends requests through /proxy/{service}/. Non 2xx responses are returned, never raised.
  class Proxy
    attr_reader :service

    def initialize(client, service)
      @client = client
      @service = service
    end

    def url(path = "", query = {})
      u = "#{@client.base_url}/proxy/#{URI.encode_www_form_component(@service)}/#{path.to_s.sub(%r{\A/+}, '')}"
      return u if query.nil? || query.empty?

      u + (u.include?("?") ? "&" : "?") + URI.encode_www_form(query)
    end

    def get(path, query = {}, headers: {})
      request("GET", path, query: query, headers: headers)
    end

    def post(path, body = nil, headers: {})
      request("POST", path, body: body, headers: headers)
    end

    def put(path, body = nil, headers: {})
      request("PUT", path, body: body, headers: headers)
    end

    def delete(path, query = {}, headers: {})
      request("DELETE", path, query: query, headers: headers)
    end

    # body: Hash or Array is sent as JSON, String as is.
    def request(method, path, query: {}, headers: {}, body: nil)
      h = headers.dup
      if !body.nil? && !body.is_a?(String)
        body = JSON.generate(body)
        h["Content-Type"] = "application/json" unless h.keys.any? { |k| k.to_s.casecmp("content-type").zero? }
      end
      h["Authorization"] = "Bearer #{@client.token}"
      @client.send_request(method.to_s.upcase, url(path, query), h, body)
    end
  end

  class Client
    attr_reader :base_url, :token

    # transport: optional callable(method, url, headers, body) -> Response, for tests.
    def initialize(base_url = ENV["ACNOLOGIA_URL"], token = ENV["ACNOLOGIA_TOKEN"], timeout: 30, transport: nil)
      raise ArgumentError, "base_url is required (or set ACNOLOGIA_URL)" if base_url.nil? || base_url.empty?
      raise ArgumentError, "token is required (or set ACNOLOGIA_TOKEN)" if token.nil? || token.empty?

      @base_url = base_url.chomp("/")
      @token = token
      @timeout = timeout
      @transport = transport || method(:net_http)
    end

    def services
      call("GET", "/api/v1/services").fetch("services", [])
    end

    # Borrow a key.
    #
    # wait_sec (0 to 60) lets the server wait that long for a free slot when
    # every usable key is at its concurrency limit. Raises AllKeysBusy (a
    # NoKeyAvailable) if none frees up in time, and NoKeyAvailable when the
    # pool is drained.
    #
    # Always report the lease: on services with a concurrency limit it holds
    # one of the key's slots until it is reported or its TTL expires.
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
      Lease.new(self, call("POST", "/api/v1/lease", body))
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
      data = call("POST", "/api/v1/leases/#{URI.encode_www_form_component(id).gsub('+', '%20')}/renew", body)
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
      data = call("POST", "/api/v1/leases/#{URI.encode_www_form_component(lease_id).gsub('+', '%20')}/report", body)
      data["next"].is_a?(Hash) ? Lease.new(self, data["next"]) : nil
    end

    # @api private
    def send_request(method, url, headers, body)
      @transport.call(method, url, headers.merge("User-Agent" => "acnologia-ruby/#{VERSION}"), body)
    end

    private

    def call(method, path, json = nil)
      headers = { "Authorization" => "Bearer #{@token}", "Accept" => "application/json" }
      body = nil
      unless json.nil?
        headers["Content-Type"] = "application/json"
        body = JSON.generate(json)
      end
      res = send_request(method, @base_url + path, headers, body)
      data = begin
        res.body.nil? || res.body.empty? ? {} : JSON.parse(res.body)
      rescue JSON::ParserError
        {}
      end
      return data.is_a?(Hash) ? data : {} if res.ok?

      err = data.is_a?(Hash) && data["error"].is_a?(Hash) ? data["error"] : {}
      code = err["code"].to_s
      message = (err["message"] || "HTTP #{res.status}").to_s
      retry_after = data.is_a?(Hash) && data["retry_after_sec"] ? data["retry_after_sec"].to_i : nil
      if code == "service_unavailable"
        retry_after ||= header_retry_after(res)
        raise ServiceUnavailable.new(message, retry_after: retry_after, status: res.status)
      end
      raise AllKeysBusy.new(message, retry_after: retry_after) if code == "all_keys_busy"
      raise NoKeyAvailable.new(message, retry_after: retry_after) if code == "no_key_available" || (res.status == 503 && code.empty?)

      raise Error.new(message, status: res.status, code: code)
    end

    # Retry-After in seconds, when the header is a positive number.
    def header_retry_after(res)
      v = res.headers.is_a?(Hash) ? res.headers["retry-after"].to_s.strip : ""
      v.match?(/\A\d+\z/) && v.to_i.positive? ? v.to_i : nil
    end

    def net_http(method, url, headers, body)
      uri = URI(url)
      http = Net::HTTP.new(uri.host, uri.port)
      http.use_ssl = uri.scheme == "https"
      http.open_timeout = @timeout
      http.read_timeout = [@timeout, lease_wait(uri, body) + 10].max
      req = Net::HTTPGenericRequest.new(method, !body.nil?, true, uri.request_uri, headers)
      req.body = body unless body.nil?
      res = http.request(req)
      hdrs = {}
      res.each_header { |k, v| hdrs[k.downcase] = v }
      Response.new(res.code.to_i, hdrs, res.body.to_s)
    rescue StandardError => e
      raise Error.new("request failed: #{e.message}")
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
