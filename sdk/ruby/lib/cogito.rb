# frozen_string_literal: true

require "json"
require "net/http"
require "uri"

# Client for Cogito, a self-hosted API key pool with automatic rotation.
#
#   cog = Cogito::Client.new("http://localhost:8080", "cog_...")
#   res = cog.proxy("serpapi").get("/search.json", q: "coffee")
module Cogito
  VERSION = "0.1.0"
  OUTCOMES = %w[ok rate_limited quota_exhausted invalid error].freeze

  # Non 2xx response from the consumer API, or a transport failure (status 0).
  class Error < StandardError
    attr_reader :status, :code

    def initialize(message, status: 0, code: "")
      super(message)
      @status = status
      @code = code
    end
  end

  # HTTP 503 no_key_available. retry_after is in seconds, or nil.
  class NoKeyAvailable < Error
    attr_reader :retry_after

    def initialize(message = "no key available", retry_after: nil)
      super(message, status: 503, code: "no_key_available")
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

  # A key handed out by Cogito.
  class Lease
    attr_reader :lease_id, :key_id, :service, :secret, :auth, :base_url, :expires_at
    attr_accessor :reported

    def initialize(client, data)
      @client = client
      @lease_id = data["lease_id"].to_s
      @key_id = data["key_id"].to_i
      @service = data["service"].to_s
      @secret = data["secret"].to_s
      auth = data["auth"].is_a?(Hash) ? data["auth"] : {}
      @auth = { "type" => auth["type"].to_s, "name" => auth["name"].to_s }.freeze
      @base_url = data["base_url"].to_s
      @expires_at = data["expires_at"]
      @reported = false
    end

    # Report the outcome. Returns the next Lease when rotate is true and a key is free.
    def report(outcome, status_code: nil, retry_after_sec: nil, message: nil, rotate: false)
      nxt = @client.report_lease(@lease_id, outcome: outcome, status_code: status_code,
                                            retry_after_sec: retry_after_sec, message: message, rotate: rotate)
      @reported = true
      nxt
    end

    # Return [query, headers] copies with the secret placed where auth.type says.
    def apply(query: {}, headers: {})
      q = query.dup
      h = headers.dup
      name = @auth["name"]
      case @auth["type"]
      when "query" then q[name] = @secret
      when "header" then h[name] = @secret
      when "bearer" then h[name.empty? ? "Authorization" : name] = "Bearer #{@secret}"
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
      "#<Cogito::Lease lease_id=#{@lease_id.inspect} key_id=#{@key_id} service=#{@service.inspect} secret=***>"
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
    def initialize(base_url = ENV["COGITO_URL"], token = ENV["COGITO_TOKEN"], timeout: 30, transport: nil)
      raise ArgumentError, "base_url is required (or set COGITO_URL)" if base_url.nil? || base_url.empty?
      raise ArgumentError, "token is required (or set COGITO_TOKEN)" if token.nil? || token.empty?

      @base_url = base_url.chomp("/")
      @token = token
      @timeout = timeout
      @transport = transport || method(:net_http)
    end

    def services
      call("GET", "/api/v1/services").fetch("services", [])
    end

    def lease(service, exclude_key_ids: nil, ttl_sec: nil)
      body = { "service" => service }
      body["exclude_key_ids"] = exclude_key_ids if exclude_key_ids && !exclude_key_ids.empty?
      body["ttl_sec"] = ttl_sec.to_i unless ttl_sec.nil?
      Lease.new(self, call("POST", "/api/v1/lease", body))
    end

    # Lease a key, yield it, report the outcome and return the block's value.
    # Raise RateLimited, QuotaExhausted or InvalidKey in the block to rotate.
    def with_key(service, max_attempts: 5, ttl_sec: nil)
      raise ArgumentError, "max_attempts must be at least 1" if max_attempts < 1

      tried = []
      lease = self.lease(service, ttl_sec: ttl_sec)
      attempt = 0
      loop do
        attempt += 1
        tried << lease.key_id
        begin
          result = yield lease
        rescue KeyProblem => e
          last = attempt >= max_attempts
          nxt = lease.report(e.outcome, status_code: e.status_code, retry_after_sec: e.retry_after,
                                        message: e.message, rotate: !last)
          raise if last

          lease = nxt || self.lease(service, exclude_key_ids: tried, ttl_sec: ttl_sec)
          next
        rescue Exception => e # rubocop:disable Lint/RescueException
          begin
            lease.report("error", message: "#{e.class}: #{e.message}"[0, 500])
          rescue StandardError
            nil # never mask the caller's exception
          end
          raise
        end
        lease.report("ok")
        return result
      end
    end

    def proxy(service)
      Proxy.new(self, service)
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
      @transport.call(method, url, headers.merge("User-Agent" => "cogito-ruby/#{VERSION}"), body)
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
      if code == "no_key_available" || (res.status == 503 && code.empty?)
        retry_after = data.is_a?(Hash) && data["retry_after_sec"] ? data["retry_after_sec"].to_i : nil
        raise NoKeyAvailable.new(message, retry_after: retry_after)
      end
      raise Error.new(message, status: res.status, code: code)
    end

    def net_http(method, url, headers, body)
      uri = URI(url)
      http = Net::HTTP.new(uri.host, uri.port)
      http.use_ssl = uri.scheme == "https"
      http.open_timeout = @timeout
      http.read_timeout = @timeout
      req = Net::HTTPGenericRequest.new(method, !body.nil?, true, uri.request_uri, headers)
      req.body = body unless body.nil?
      res = http.request(req)
      hdrs = {}
      res.each_header { |k, v| hdrs[k.downcase] = v }
      Response.new(res.code.to_i, hdrs, res.body.to_s)
    rescue StandardError => e
      raise Error.new("request failed: #{e.message}")
    end
  end
end
