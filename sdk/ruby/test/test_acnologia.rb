# frozen_string_literal: true

require "minitest/autorun"
require "json"
require "socket"
require "uri"
require_relative "../lib/acnologia"

class FakeServer
  attr_reader :log, :reports, :lease_bodies, :renew_bodies
  attr_accessor :keys, :busy, :breaker

  def initialize
    @keys = [1, 2, 3]
    @bad = []
    @leases = {}
    @log = []
    @reports = []
    @lease_bodies = []
    @renew_bodies = []
    @n = 0
  end

  def lease_json(exclude, exclusive = false)
    k = @keys.find { |x| !exclude.include?(x) && !@bad.include?(x) }
    return nil unless k

    @n += 1
    id = "l/#{@n}"
    @leases[id] = k
    { "lease_id" => id, "key_id" => k, "service" => "serpapi", "secret" => "s#{k}",
      "auth" => { "type" => "query", "name" => "api_key" }, "base_url" => "https://serpapi.com",
      "expires_at" => "2026-11-01T00:00:00Z", "exclusive" => exclusive }
  end

  def call(method, url, headers, body)
    @log << [method, url, headers, body]
    path = URI(url).path
    json = body ? JSON.parse(body) : nil
    return Acnologia::Response.new(401, {}, "{}") unless headers["Authorization"] == "Bearer acn_x"

    if path == "/api/v1/lease"
      @lease_bodies << json
      if @breaker
        return Acnologia::Response.new(503, { "retry-after" => "9" }, @breaker)
      end
      if @busy
        return Acnologia::Response.new(503, {}, '{"error":{"code":"all_keys_busy","message":"every key is at its limit"},"retry_after_sec":1}')
      end

      l = lease_json(json["exclude_key_ids"] || [], json["exclusive"] == true)
      return Acnologia::Response.new(503, {}, '{"error":{"code":"no_key_available","message":"drained"},"retry_after_sec":7}') unless l

      Acnologia::Response.new(200, {}, JSON.generate(l))
    elsif (m = path.match(%r{\A/api/v1/leases/(.+)/report\z}))
      id = URI.decode_www_form_component(m[1])
      @reports << json.merge("_id" => id)
      key = @leases.fetch(id)
      @bad << key unless json["outcome"] == "ok"
      nxt = json["rotate"] ? lease_json([key]) : nil
      Acnologia::Response.new(200, {}, JSON.generate("ok" => true, "next" => nxt))
    elsif (m = path.match(%r{\A/api/v1/leases/(.+)/renew\z}))
      id = URI.decode_www_form_component(m[1])
      @renew_bodies << json.merge("_id" => id, "_raw" => body)
      unless @leases.key?(id)
        return Acnologia::Response.new(404, {}, '{"error":{"code":"lease_not_found","message":"lease is not open"}}')
      end

      Acnologia::Response.new(200, {}, JSON.generate("ok" => true, "expires_at" => "2026-11-02T00:00:#{json['ttl_sec'] || 0}Z"))
    elsif path == "/api/v1/services"
      Acnologia::Response.new(200, {}, '{"services":[{"slug":"serpapi","available":true}]}')
    else
      Acnologia::Response.new(200, { "x-acnologia-key-id" => "1" }, JSON.generate("url" => url, "body" => body))
    end
  end
end

class AcnologiaTest < Minitest::Test
  def setup
    @srv = FakeServer.new
    @acn = Acnologia::Client.new("http://acnologia.test/", "acn_x", transport: @srv.method(:call))
  end

  def test_services
    assert_equal "serpapi", @acn.services.first["slug"]
  end

  def test_lease_and_report
    l = @acn.lease("serpapi", ttl_sec: 60)
    assert_equal({ "service" => "serpapi", "ttl_sec" => 60 }, @srv.lease_bodies.last)
    assert_equal 1, l.key_id
    refute_includes l.inspect, "s1"
    nxt = l.report("rate_limited", status_code: 429, retry_after_sec: 30, rotate: true)
    assert_equal 2, nxt.key_id
    assert_equal "rate_limited", @srv.reports.last["outcome"]
    assert_equal 30, @srv.reports.last["retry_after_sec"]
    assert_includes @srv.log[-1][1], "/api/v1/leases/l%2F1/report"
  end

  def test_apply
    l = @acn.lease("serpapi")
    q, h = l.apply(query: { "q" => "a" })
    assert_equal({ "q" => "a", "api_key" => "s1" }, q)
    assert_equal({}, h)
    assert_equal "https://serpapi.com/search.json?q=a", l.url("/search.json", "q" => "a")
    b = Acnologia::Lease.new(@acn, "secret" => "z", "auth" => { "type" => "bearer", "name" => "" })
    assert_equal "Bearer z", b.apply[1]["Authorization"]
    hd = Acnologia::Lease.new(@acn, "secret" => "z", "auth" => { "type" => "header", "name" => "xi-api-key" })
    assert_equal "z", hd.apply[1]["xi-api-key"]
  end

  def test_apply_prefix
    mk = ->(auth) { Acnologia::Lease.new(@acn, "secret" => "sk", "auth" => auth) }
    assert_equal "", mk.call("type" => "query", "name" => "api_key").auth["prefix"]
    q, h = mk.call("type" => "query", "name" => "key", "prefix" => "pk_").apply(query: { "q" => "a" })
    assert_equal({ "q" => "a", "key" => "pk_sk" }, q)
    assert_equal({}, h)
    assert_equal({ "Authorization" => "Token sk" },
                 mk.call("type" => "header", "name" => "Authorization", "prefix" => "Token ").apply[1])
    assert_equal({ "xi-api-key" => "sk" }, mk.call("type" => "header", "name" => "xi-api-key").apply[1])
    assert_equal({ "Authorization" => "Key sk" }, mk.call("type" => "bearer", "name" => "", "prefix" => "Key ").apply[1])
    assert_equal({ "X-Token" => "Token sk" },
                 mk.call("type" => "bearer", "name" => "X-Token", "prefix" => "Token ").apply[1])
    assert_equal({ "Authorization" => "Bearer sk" }, mk.call("type" => "bearer", "name" => "", "prefix" => nil).apply[1])
  end

  def test_scrub_message
    secret = "a b+c/d~*"
    msg = "GET /x?key=#{secret}&k2=#{URI.encode_www_form_component(secret)}&k3=a%20b%2Bc%2Fd~%2A"
    assert_equal "GET /x?key=[key]&k2=[key]&k3=[key]", Acnologia.scrub_message(msg, secret)
    assert_equal 500, Acnologia.scrub_message("x" * 600, "sk").length
    assert_equal "plain", Acnologia.scrub_message("plain", "")
    assert_equal "plain", Acnologia.scrub_message("plain", nil)
  end

  def test_with_key_scrubs_report_messages
    assert_raises(RuntimeError) do
      @acn.with_key("serpapi") { |l| raise "GET https://serpapi.com/search?api_key=#{l.secret} failed" }
    end
    assert_equal "error", @srv.reports.last["outcome"]
    assert_equal "RuntimeError: GET https://serpapi.com/search?api_key=[key] failed", @srv.reports.last["message"]
    @acn.with_key("serpapi") do |l|
      raise Acnologia::RateLimited.new("429 for #{l.secret}") if l.key_id == 2

      "ok"
    end
    rl = @srv.reports.find { |r| r["outcome"] == "rate_limited" }
    assert_equal "429 for [key]", rl["message"]
  end

  def test_with_key_rotates
    seen = []
    out = @acn.with_key("serpapi") do |lease|
      seen << lease.key_id
      raise Acnologia::RateLimited.new(retry_after: 5) if lease.key_id == 1
      raise Acnologia::QuotaExhausted if lease.key_id == 2

      "done"
    end
    assert_equal "done", out
    assert_equal [1, 2, 3], seen
    assert_equal [%w[rate_limited true], %w[quota_exhausted true], %w[ok false]],
                 @srv.reports.map { |r| [r["outcome"], r["rotate"].to_s] }
  end

  def test_with_key_max_attempts
    assert_raises(Acnologia::InvalidKey) do
      @acn.with_key("serpapi", max_attempts: 2) { raise Acnologia::InvalidKey }
    end
    assert_equal [true, false], @srv.reports.map { |r| r["rotate"] }
  end

  def test_with_key_other_error
    assert_raises(ZeroDivisionError) { @acn.with_key("serpapi") { 1 / 0 } }
    assert_equal "error", @srv.reports.last["outcome"]
  end

  def test_no_key_available
    @srv.keys = []
    err = assert_raises(Acnologia::NoKeyAvailable) { @acn.lease("serpapi") }
    assert_equal 7, err.retry_after
  end

  def test_lease_sends_wait_sec
    @acn.lease("elevenlabs", ttl_sec: 60, wait_sec: 20).report("ok")
    assert_equal({ "service" => "elevenlabs", "ttl_sec" => 60, "wait_sec" => 20 }, @srv.lease_bodies.last)
    @acn.lease("elevenlabs", wait_sec: 0).report("ok")
    assert_equal({ "service" => "elevenlabs" }, @srv.lease_bodies.last)
    assert_raises(ArgumentError) { @acn.lease("elevenlabs", wait_sec: 61) }
    assert_equal 2, @srv.lease_bodies.size
  end

  def test_with_key_sends_wait_sec
    @srv.keys = [1]
    assert_raises(Acnologia::NoKeyAvailable) do
      @acn.with_key("elevenlabs", wait_sec: 15) { raise Acnologia::RateLimited }
    end
    assert_equal([15, 15], @srv.lease_bodies.map { |b| b["wait_sec"] })
    assert_equal ["rate_limited"], @srv.reports.map { |r| r["outcome"] }
  end

  def test_all_keys_busy
    @srv.busy = true
    err = assert_raises(Acnologia::AllKeysBusy) { @acn.lease("elevenlabs", wait_sec: 5) }
    assert_kind_of Acnologia::NoKeyAvailable, err
    assert err.busy?
    assert_equal "all_keys_busy", err.code
    assert_equal 503, err.status
    assert_equal 1, err.retry_after
    assert_equal "every key is at its limit", err.message
  end

  def test_no_key_available_is_not_busy
    @srv.keys = []
    err = assert_raises(Acnologia::NoKeyAvailable) { @acn.lease("serpapi") }
    refute err.busy?
    refute_kind_of Acnologia::AllKeysBusy, err
  end

  def test_with_key_busy_does_not_yield
    @srv.busy = true
    yielded = false
    assert_raises(Acnologia::AllKeysBusy) { @acn.with_key("elevenlabs", wait_sec: 2) { yielded = true } }
    refute yielded
    assert_empty @srv.reports
  end

  def test_with_key_reports_on_break_and_throw
    @acn.with_key("serpapi") { break }
    assert_equal ["ok"], @srv.reports.map { |r| r["outcome"] }
    catch(:done) { @acn.with_key("serpapi") { throw :done } }
    assert_equal %w[ok ok], @srv.reports.map { |r| r["outcome"] }
    assert_equal 2, @srv.reports.map { |r| r["_id"] }.uniq.size
  end

  def test_lease_wait_read_timeout
    uri = URI("http://acnologia.test/api/v1/lease")
    assert_equal 30, @acn.send(:lease_wait, uri, JSON.generate("service" => "x", "wait_sec" => 30))
    assert_equal 0, @acn.send(:lease_wait, uri, JSON.generate("service" => "x"))
    assert_equal 0, @acn.send(:lease_wait, URI("http://acnologia.test/api/v1/services"), nil)
  end

  def test_bad_token
    acn = Acnologia::Client.new("http://acnologia.test", "acn_bad", transport: @srv.method(:call))
    err = assert_raises(Acnologia::Error) { acn.services }
    assert_equal 401, err.status
  end

  def test_renew
    l = @acn.lease("serpapi")
    refute l.exclusive?
    assert_equal "2026-11-02T00:00:90Z", l.renew(ttl_sec: 89.2)
    assert_equal "2026-11-02T00:00:90Z", l.expires_at
    assert_equal({ "ttl_sec" => 90 }, @srv.renew_bodies.last.slice("ttl_sec"))
    assert_includes @srv.log.last[1], "/api/v1/leases/l%2F1/renew"
    assert_equal "2026-11-02T00:00:0Z", l.renew
    assert_equal "{}", @srv.renew_bodies.last["_raw"]
    assert_equal "2026-11-02T00:00:5Z", @acn.renew(l, ttl_sec: 5)
  end

  def test_renew_not_found
    gone = Acnologia::Lease.new(@acn, "lease_id" => "nope")
    err = assert_raises(Acnologia::Error) { gone.renew(ttl_sec: 60) }
    assert_equal 404, err.status
    assert_equal "lease_not_found", err.code
    assert_nil gone.expires_at
  end

  def test_lease_exclusive
    l = @acn.lease("serpapi", exclusive: true)
    assert_equal({ "service" => "serpapi", "exclusive" => true }, @srv.lease_bodies.last)
    assert l.exclusive
    assert l.exclusive?
    @acn.lease("serpapi", exclusive: false)
    assert_equal({ "service" => "serpapi" }, @srv.lease_bodies.last)
  end

  def test_with_key_forwards_exclusive
    @srv.keys = [1]
    assert_raises(Acnologia::NoKeyAvailable) do
      @acn.with_key("serpapi", exclusive: true, ttl_sec: 30) { raise Acnologia::RateLimited }
    end
    assert_equal [[true, 30], [true, 30]], @srv.lease_bodies.map { |b| [b["exclusive"], b["ttl_sec"]] }
  end

  def test_service_unavailable
    @srv.breaker = '{"error":{"code":"service_unavailable","message":"circuit open"},"retry_after_sec":42}'
    err = assert_raises(Acnologia::ServiceUnavailable) { @acn.lease("serpapi") }
    refute_kind_of Acnologia::NoKeyAvailable, err
    assert_kind_of Acnologia::Error, err
    assert_equal 503, err.status
    assert_equal "service_unavailable", err.code
    assert_equal 42, err.retry_after
    assert_equal "circuit open", err.message
    @srv.breaker = '{"error":{"code":"service_unavailable","message":"circuit open"}}'
    assert_equal 9, assert_raises(Acnologia::ServiceUnavailable) { @acn.lease("serpapi") }.retry_after
    @srv.breaker = ""
    assert_raises(Acnologia::NoKeyAvailable) { @acn.lease("serpapi") }
  end

  def test_proxy
    res = @acn.proxy("serpapi").get("/search.json", "q" => "coffee")
    assert res.ok?
    assert_equal "http://acnologia.test/proxy/serpapi/search.json?q=coffee", res.json["url"]
    assert_equal "1", res["X-Acnologia-Key-Id"]
    res = @acn.proxy("elevenlabs").post("v1/text-to-speech/v", { "text" => "hi" }, headers: { "Accept" => "audio/mpeg" })
    assert_equal '{"text":"hi"}', res.json["body"]
    assert_equal "application/json", @srv.log.last[2]["Content-Type"]
  end
end

# A tiny HTTP/1.1 server on 127.0.0.1 for the tests that go over the network.
# The handler gets a request hash (method, path, query, headers with lower
# cased names, body) and returns [status, headers, body]. One request per
# connection, then the connection closes.
class HTTPFake
  attr_reader :requests, :port
  attr_accessor :handler

  def initialize(&handler)
    @handler = handler
    @requests = []
    @server = TCPServer.new("127.0.0.1", 0)
    @port = @server.addr[1]
    @thread = Thread.new { serve }
  end

  def url
    "http://127.0.0.1:#{@port}"
  end

  def last
    @requests.last
  end

  def close
    @server.close
  rescue IOError
    nil
  ensure
    @thread.kill
  end

  private

  def serve
    loop do
      sock = @server.accept
      Thread.new(sock) { |s| handle(s) }
    end
  rescue IOError, Errno::EBADF
    nil
  end

  def handle(sock)
    line = sock.gets.to_s
    method, target, = line.split(" ")
    headers = {}
    while (h = sock.gets) && h != "\r\n"
      k, v = h.split(":", 2)
      headers[k.strip.downcase] = v.to_s.strip
    end
    len = headers["content-length"].to_i
    body = len.positive? ? sock.read(len) : nil
    uri = URI(target.to_s)
    req = { method: method, path: uri.path, query: uri.query ? URI.decode_www_form(uri.query).to_h : {},
            headers: headers, body: body }
    @requests << req
    status, out_headers, out_body = @handler.call(req)
    out_body = out_body.to_s
    head = +"HTTP/1.1 #{status} X\r\n"
    { "Content-Length" => out_body.bytesize.to_s, "Connection" => "close" }.merge(out_headers || {}).each do |k, v|
      head << "#{k}: #{v}\r\n"
    end
    sock.write(head + "\r\n" + (method == "HEAD" ? "" : out_body))
  rescue IOError, Errno::EPIPE, Errno::ECONNRESET
    nil
  ensure
    sock.close
  end
end

class AcnologiaHTTPTest < Minitest::Test
  TOKEN = "acn_http_secret_token"
  ENV_KEYS = %w[ACNOLOGIA_URL ACNOLOGIA_TOKEN ACNOLOGIA_ACCESS_PASSWORD].freeze

  def setup
    @saved_env = ENV_KEYS.to_h { |k| [k, ENV.fetch(k, nil)] }
    ENV_KEYS.each { |k| ENV.delete(k) }
    @fake = HTTPFake.new { |_req| [200, { "Content-Type" => "application/json" }, "{}"] }
    @slept = []
  end

  def teardown
    @fake.close
    @saved_env.each { |k, v| v.nil? ? ENV.delete(k) : ENV[k] = v }
  end

  def client(**opts)
    Acnologia::Client.new(@fake.url, TOKEN, sleeper: ->(s) { @slept << s }, **opts)
  end

  def answer(status, json, headers = {})
    @fake.handler = ->(_req) { [status, { "Content-Type" => "application/json" }.merge(headers), JSON.generate(json)] }
  end

  def err(code, message = "nope", **extra)
    { "error" => { "code" => code, "message" => message } }.merge(extra.transform_keys(&:to_s))
  end

  ME = {
    "app" => { "id" => 7, "name" => "billing-bot" },
    "limits" => { "daily_requests" => 1000, "monthly_requests" => nil, "daily_budget" => 2.5, "monthly_budget" => nil },
    "usage" => { "today_requests" => 12, "today_cost" => 0.25, "month_requests" => 340, "month_cost" => 4.5,
                 "last_used_at" => "2026-11-04T09:00:00Z", "limit_hit" => nil },
    "limit_hit" => nil,
    "services" => [{ "slug" => "agi-labs", "name" => "AGI Labs", "provider" => "agi-labs", "category" => "ai",
                     "available" => 1, "logo_url" => nil },
                   { "slug" => "maps", "name" => "Maps", "provider" => "maps", "category" => "geo",
                     "available" => 0, "logo_url" => nil }],
    "share" => nil
  }.freeze

  def test_me
    answer(200, ME)
    me = client.me
    assert_equal "GET", @fake.last[:method]
    assert_equal "/api/v1/me", @fake.last[:path]
    assert_equal "Bearer #{TOKEN}", @fake.last[:headers]["authorization"]
    assert_equal 7, me.app.id
    assert_equal "billing-bot", me.app.name
    assert_equal 1000, me.limits.daily_requests
    assert_nil me.limits.monthly_requests
    assert_equal 2.5, me.limits.daily_budget
    assert_equal 12, me.usage.today_requests
    assert_equal 4.5, me.usage.month_cost
    assert_equal "2026-11-04T09:00:00Z", me.usage.last_used_at
    assert_equal %w[agi-labs maps], me.services.map(&:slug)
    assert me.services.first.available?
    refute me.services.last.available?
    refute me.share?
    refute me.limited?
    assert me.services.frozen?
    assert_raises(FrozenError) { me.app.name = "x" }
    refute_includes me.inspect, TOKEN
  end

  def test_me_share_and_limit
    data = ME.merge(
      "services" => [],
      "limit_hit" => { "limit" => "daily_requests", "reset_at" => "2026-11-05T00:00:00Z" },
      "share" => { "id" => "sh_1", "name" => "Team", "owner" => { "name" => "Ana", "id" => "srv_a" },
                   "owner_name" => "Ana", "owner_id" => "srv_a", "recipient" => "bo@example.com", "label" => "",
                   "status" => "paused", "mode" => "proxy", "ends_at" => nil }
    )
    answer(200, data)
    me = client.me
    assert me.share?
    assert_equal "paused", me.share.status
    refute me.share.active?
    assert_equal "Ana", me.share.owner.name
    assert_equal "srv_a", me.share.owner.id
    assert me.limited?
    assert_equal "daily_requests", me.limit_hit.limit
    assert_equal "2026-11-05T00:00:00Z", me.limit_hit.reset_at
    assert_empty me.services
  end

  # [status, body, headers, class, code]
  ERROR_CASES = [
    [401, { "error" => { "code" => "unauthenticated", "message" => "bad token" } }, {}, :Unauthenticated, "unauthenticated"],
    [401, {}, {}, :Unauthenticated, "unauthenticated"],
    [403, { "error" => { "code" => "service_not_allowed" } }, {}, :Forbidden, "service_not_allowed"],
    [403, { "error" => { "code" => "unauthenticated", "message" => "app disabled" } }, {}, :Forbidden, "unauthenticated"],
    [403, { "error" => { "code" => "proxy_not_allowed" } }, {}, :Forbidden, "proxy_not_allowed"],
    [403, { "error" => { "code" => "lease_not_allowed" } }, {}, :LeaseNotAllowed, "lease_not_allowed"],
    [403, { "error" => { "code" => "share_revoked" } }, { "X-Acnologia-Share" => "revoked" }, :ShareRevoked, "share_revoked"],
    [403, { "error" => { "code" => "share_paused" } }, { "X-Acnologia-Share" => "paused" }, :SharePaused, "share_paused"],
    [403, { "error" => { "code" => "share_expired" } }, { "X-Acnologia-Share" => "expired" }, :ShareExpired, "share_expired"],
    [403, {}, { "X-Acnologia-Share" => "revoked" }, :ShareRevoked, "share_revoked"],
    [429, { "error" => { "code" => "app_limit_reached" }, "limit" => "daily_requests",
            "reset_at" => "2026-11-05T00:00:00Z", "retry_after_sec" => 120 },
     { "Retry-After" => "120" }, :AppLimitReached, "app_limit_reached"],
    [502, { "error" => { "code" => "upstream_blocked" } }, {}, :UpstreamBlocked, "upstream_blocked"],
    [502, { "error" => { "code" => "upstream_unreachable" } }, {}, :Error, "upstream_unreachable"],
    [508, { "error" => { "code" => "loop_detected" } }, {}, :LoopDetected, "loop_detected"],
    [503, { "error" => { "code" => "no_key_available" }, "retry_after_sec" => 7 }, {}, :NoKeyAvailable, "no_key_available"],
    [503, { "error" => { "code" => "all_keys_busy" }, "retry_after_sec" => 1 }, {}, :AllKeysBusy, "all_keys_busy"],
    [503, { "error" => { "code" => "service_unavailable" } }, { "Retry-After" => "30" }, :ServiceUnavailable,
     "service_unavailable"],
    [503, {}, {}, :NoKeyAvailable, "no_key_available"],
    [503, { "error" => { "code" => "empty_group" } }, {}, :Error, "empty_group"],
    [404, { "error" => { "code" => "lease_not_found" } }, {}, :Error, "lease_not_found"],
    [404, { "error" => { "code" => "unknown_service" } }, {}, :Error, "unknown_service"],
    [409, { "error" => { "code" => "report_failed" } }, {}, :Error, "report_failed"],
    [413, { "error" => { "code" => "body_too_large" } }, {}, :Error, "body_too_large"]
  ].freeze

  def test_error_mapping
    acn = client
    ERROR_CASES.each do |status, body, headers, klass, code|
      answer(status, body, headers)
      e = assert_raises(Acnologia::Error, "#{status} #{code}") { acn.services }
      assert_instance_of Acnologia.const_get(klass), e, "#{status} #{code}"
      assert_equal status, e.status, "#{status} #{code}"
      assert_equal code, e.code, "#{status} #{code}"
    end
    assert_kind_of Acnologia::Forbidden, Acnologia::ShareRevoked.new
    assert_kind_of Acnologia::ShareUnavailable, Acnologia::SharePaused.new
    assert_kind_of Acnologia::Forbidden, Acnologia::LeaseNotAllowed.new
    assert_equal "HTTP 403", Acnologia.error_for(403, {}, "", by_status: true).message
  end

  def test_share_status
    answer(403, err("share_expired", "this share expired"), "X-Acnologia-Share" => "expired")
    e = assert_raises(Acnologia::ShareUnavailable) { client.me }
    assert_equal "expired", e.share_status
    assert_equal "this share expired", e.message
  end

  def test_app_limit_reached
    answer(429, err("app_limit_reached", "this app reached its monthly budget limit",
                    limit: "monthly_budget", reset_at: "2026-12-01T00:00:00Z", retry_after_sec: 3600),
           "Retry-After" => "3600")
    e = assert_raises(Acnologia::AppLimitReached) { client.lease("agi-labs") }
    assert_equal "monthly_budget", e.limit
    assert_equal "2026-12-01T00:00:00Z", e.reset_at
    assert_equal Time.utc(2026, 12, 1), e.reset_time
    assert_equal 3600, e.retry_after
    assert_equal "monthly_budget", e.body["limit"]
    assert_equal "this app reached its monthly budget limit", e.message
  end

  def test_retry_after_header_fallback
    answer(429, err("app_limit_reached", "limit", limit: "daily_requests"), "Retry-After" => "45")
    assert_equal 45, assert_raises(Acnologia::AppLimitReached) { client.services }.retry_after
    answer(503, err("no_key_available"), "Retry-After" => "8")
    assert_equal 8, assert_raises(Acnologia::NoKeyAvailable) { client.services }.retry_after
    answer(503, err("all_keys_busy"), "Retry-After" => (Time.now + 30).httpdate)
    assert_includes 28..31, assert_raises(Acnologia::AllKeysBusy) { client.services }.retry_after
    answer(503, err("no_key_available"))
    assert_nil assert_raises(Acnologia::NoKeyAvailable) { client.services }.retry_after
    assert_nil Acnologia.parse_retry_after("soon")
    assert_nil Acnologia.parse_retry_after("0")
  end

  def test_retries_honor_retry_after
    n = 0
    @fake.handler = lambda do |_req|
      n += 1
      if n < 3
        [503, { "Retry-After" => "2" }, JSON.generate(err("all_keys_busy"))]
      else
        [200, {}, JSON.generate("lease_id" => "l1", "key_id" => 4, "secret" => "sk", "auth" => { "type" => "bearer" })]
      end
    end
    l = client(max_retries: 3).lease("agi-labs")
    assert_equal 4, l.key_id
    assert_equal [2, 2], @slept
    assert_equal 3, @fake.requests.size
  end

  def test_no_retry_by_default_or_past_max_wait
    answer(503, err("no_key_available"), "Retry-After" => "5")
    assert_raises(Acnologia::NoKeyAvailable) { client.lease("agi-labs") }
    assert_equal 1, @fake.requests.size
    answer(429, err("app_limit_reached", "x", retry_after_sec: 3600), "Retry-After" => "3600")
    assert_raises(Acnologia::AppLimitReached) { client(max_retries: 5, max_retry_wait: 60).services }
    assert_equal 2, @fake.requests.size
    assert_empty @slept
    answer(503, err("service_unavailable"), "Retry-After" => "1")
    assert_raises(Acnologia::ServiceUnavailable) { client(max_retries: 2).me }
    assert_equal 5, @fake.requests.size
    assert_equal [1, 1], @slept
    answer(403, err("lease_not_allowed"))
    assert_raises(Acnologia::LeaseNotAllowed) { client(max_retries: 2).lease("agi-labs") }
    assert_equal 6, @fake.requests.size
  end

  def test_report_and_renew_never_retry
    answer(503, err("no_key_available"), "Retry-After" => "1")
    acn = client(max_retries: 3)
    l = Acnologia::Lease.new(acn, "lease_id" => "l1")
    assert_raises(Acnologia::NoKeyAvailable) { l.report("ok") }
    assert_raises(Acnologia::NoKeyAvailable) { l.renew }
    assert_equal 2, @fake.requests.size
  end

  def test_proxy_idempotency_key
    answer(200, { "ok" => true }, "Idempotent-Replayed" => "true", "X-Acnologia-Key-Id" => "3",
                                  "X-Acnologia-Attempts" => "2")
    px = client.proxy("agi-labs")
    res = px.post("/v1/chat", { "prompt" => "hi" }, idempotency_key: true)
    key = @fake.last[:headers]["idempotency-key"]
    assert_match(/\A\h{8}-\h{4}-\h{4}-\h{4}-\h{12}\z/, key)
    assert res.replayed?
    assert_equal 3, res.key_id
    assert_equal 2, res.attempts
    assert_equal "/proxy/agi-labs/v1/chat", @fake.last[:path]
    assert_equal '{"prompt":"hi"}', @fake.last[:body]
    assert_equal "application/json", @fake.last[:headers]["content-type"]
    assert_equal "Bearer #{TOKEN}", @fake.last[:headers]["authorization"]

    px.post("/v1/chat", { "prompt" => "hi" }, idempotency_key: true)
    refute_equal key, @fake.last[:headers]["idempotency-key"]
    px.post("/v1/chat", "raw", idempotency_key: "order-42")
    assert_equal "order-42", @fake.last[:headers]["idempotency-key"]
    px.post("/v1/chat", "raw", headers: { "Idempotency-Key" => "mine" }, idempotency_key: true)
    assert_equal "mine", @fake.last[:headers]["idempotency-key"]
    px.post("/v1/chat", "raw")
    assert_nil @fake.last[:headers]["idempotency-key"]

    auto = client(idempotency: true).proxy("agi-labs")
    auto.post("/v1/chat", "raw")
    refute_nil @fake.last[:headers]["idempotency-key"]
    auto.patch("/v1/chat/1", { "a" => 1 })
    refute_nil @fake.last[:headers]["idempotency-key"]
    assert_equal "PATCH", @fake.last[:method]
    auto.post("/v1/chat", "raw", idempotency_key: false)
    assert_nil @fake.last[:headers]["idempotency-key"]
    auto.get("/v1/models", "q" => "x")
    assert_nil @fake.last[:headers]["idempotency-key"]
    assert_equal({ "q" => "x" }, @fake.last[:query])
  end

  def test_proxy_retries_only_when_safe
    n = 0
    @fake.handler = lambda do |_req|
      n += 1
      n == 1 ? [503, { "Retry-After" => "1" }, JSON.generate(err("all_keys_busy"))] : [200, {}, "done"]
    end
    px = client(max_retries: 2).proxy("agi-labs")
    res = px.post("/v1/chat", "raw")
    assert_equal 503, res.status
    assert_equal 1, res.retry_after
    assert_instance_of Acnologia::AllKeysBusy, res.error
    assert_raises(Acnologia::AllKeysBusy) { res.raise_for_status! }
    assert_equal 1, @fake.requests.size

    n = 0
    res = px.post("/v1/chat", "raw", idempotency_key: true)
    assert_equal "done", res.body
    keys = @fake.requests.last(2).map { |r| r[:headers]["idempotency-key"] }
    assert_equal 1, keys.uniq.size
    refute_nil keys.first

    n = 0
    assert_equal "done", px.get("/v1/models").body
    assert_equal [1, 1], @slept
  end

  def test_proxy_response_error_ignores_provider_auth_errors
    answer(401, { "error" => { "message" => "provider says bad key" } })
    res = client.proxy("agi-labs").get("/v1/models")
    assert_equal 401, res.status
    assert_instance_of Acnologia::Error, res.error
    answer(502, err("upstream_blocked"))
    assert_instance_of Acnologia::UpstreamBlocked, client.proxy("agi-labs").get("/v1/models").error
    answer(508, err("loop_detected"))
    assert_raises(Acnologia::LoopDetected) { client.proxy("agi-labs").get("/v1/models").raise_for_status! }
    answer(403, err("share_revoked"), "X-Acnologia-Share" => "revoked")
    assert_instance_of Acnologia::ShareRevoked, client.proxy("agi-labs").get("/v1/models").error
    answer(200, {})
    res = client.proxy("agi-labs").get("/v1/models")
    assert_nil res.error
    assert_same res, res.raise_for_status!
  end

  def test_proxy_head_and_options
    answer(200, { "x" => 1 })
    assert_equal 200, client.proxy("agi-labs").head("/v1/models").status
    assert_equal "HEAD", @fake.last[:method]
    client.proxy("agi-labs").options("/v1/models")
    assert_equal "OPTIONS", @fake.last[:method]
  end

  def test_env_defaults
    ENV["ACNOLOGIA_URL"] = "#{@fake.url}/"
    ENV["ACNOLOGIA_TOKEN"] = "acn_from_env"
    answer(200, { "services" => [] })
    acn = Acnologia::Client.new
    assert_equal @fake.url, acn.base_url
    acn.services
    assert_equal "Bearer acn_from_env", @fake.last[:headers]["authorization"]
    assert_equal "acn_from_env", Acnologia::Client.new(nil, nil).token
    assert_equal "acn_explicit", Acnologia::Client.new(nil, "acn_explicit").token
    assert_equal "http://other.test", Acnologia::Client.new("http://other.test").base_url
    ENV.delete("ACNOLOGIA_TOKEN")
    assert_raises(ArgumentError) { Acnologia::Client.new }
    ENV.delete("ACNOLOGIA_URL")
    assert_raises(ArgumentError) { Acnologia::Client.new(nil, "acn_x") }
  end

  def test_access_password
    answer(200, { "services" => [] })
    client.services
    refute @fake.last[:headers].key?("x-acnologia-access")
    refute client.access_password?

    client(access_password: "open sesame").proxy("agi-labs").get("/v1/models")
    assert_equal "open sesame", @fake.last[:headers]["x-acnologia-access"]

    ENV["ACNOLOGIA_ACCESS_PASSWORD"] = "from-env"
    acn = client
    assert acn.access_password?
    acn.me
    assert_equal "from-env", @fake.last[:headers]["x-acnologia-access"]
    client(access_password: false).me
    refute @fake.last[:headers].key?("x-acnologia-access")
    refute_includes acn.inspect, "from-env"
    refute_includes acn.inspect, TOKEN
  end

  def test_extra_headers_and_user_agent
    answer(200, { "services" => [] })
    client(headers: { "X-Trace" => "t1" }).services
    assert_equal "t1", @fake.last[:headers]["x-trace"]
    assert_equal "acnologia-ruby/#{Acnologia::VERSION}", @fake.last[:headers]["user-agent"]
  end

  def test_error_messages_never_carry_secrets
    answer(400, err("bad_request", "token #{TOKEN} and pw hunter2 are wrong"))
    e = assert_raises(Acnologia::Error) { client(access_password: "hunter2").services }
    refute_includes e.message, TOKEN
    refute_includes e.message, "hunter2"
    assert_equal "token [key] and pw [key] are wrong", e.message
    refute_includes client.inspect, TOKEN
  end

  def test_transport_failure_is_scrubbed
    dead = TCPServer.new("127.0.0.1", 0)
    port = dead.addr[1]
    dead.close
    acn = Acnologia::Client.new("http://127.0.0.1:#{port}", TOKEN, open_timeout: 2)
    e = assert_raises(Acnologia::Error) { acn.services }
    assert_equal 0, e.status
    refute_includes e.message, TOKEN
  end

  def test_timeouts
    acn = client(timeout: 9, open_timeout: 3)
    assert_equal 3, acn.open_timeout
    assert_equal 9, acn.read_timeout
    @fake.handler = lambda do |_req|
      sleep 1.5
      [200, {}, "{}"]
    end
    e = assert_raises(Acnologia::TimeoutError) { client(read_timeout: 0.3).services }
    assert_equal 0, e.status
    assert_equal "timeout", e.code
    assert_kind_of Acnologia::Error, e
  end

  def test_lease_block_reports
    leases = 0
    @fake.handler = lambda do |req|
      if req[:path] == "/api/v1/lease"
        leases += 1
        [200, {}, JSON.generate("lease_id" => "l#{leases}", "key_id" => leases, "secret" => "sk_#{leases}",
                                "auth" => { "type" => "bearer" }, "base_url" => "https://agi-labs.example")]
      else
        [200, {}, '{"ok":true,"next":null}']
      end
    end
    acn = client
    assert_equal :done, (acn.lease("agi-labs") { |l| l.key_id == 1 ? :done : :wrong })
    assert_raises(Acnologia::RateLimited) { acn.lease("agi-labs") { raise Acnologia::RateLimited.new(retry_after: 9) } }
    assert_raises(RuntimeError) { acn.lease("agi-labs") { |l| raise "failed with #{l.secret}" } }
    acn.lease("agi-labs") { |l| l.report("banned") }
    reports = @fake.requests.select { |r| r[:path].end_with?("/report") }.map { |r| [r[:path], JSON.parse(r[:body])] }
    assert_equal %w[/api/v1/leases/l1/report /api/v1/leases/l2/report /api/v1/leases/l3/report
                    /api/v1/leases/l4/report], reports.map(&:first)
    assert_equal "ok", reports[0][1]["outcome"]
    assert_equal "rate_limited", reports[1][1]["outcome"]
    assert_equal 9, reports[1][1]["retry_after_sec"]
    assert_equal "error", reports[2][1]["outcome"]
    assert_equal "RuntimeError: failed with [key]", reports[2][1]["message"]
    assert_equal "banned", reports[3][1]["outcome"]
  end

  def test_guide
    @fake.handler = ->(_req) { [200, { "Content-Type" => "text/markdown" }, "# AGI Labs\n"] }
    assert_equal "# AGI Labs\n", client.guide("agi-labs")
    assert_equal "/api/v1/services/agi-labs/guide", @fake.last[:path]
    assert_equal "text/markdown", @fake.last[:headers]["accept"]
    answer(403, err("service_not_allowed"))
    assert_raises(Acnologia::Forbidden) { client.guide("maps") }
  end
end
