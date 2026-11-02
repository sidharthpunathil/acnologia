# frozen_string_literal: true

require "minitest/autorun"
require "json"
require "uri"
require_relative "../lib/acnologia"

class FakeServer
  attr_reader :log, :reports, :lease_bodies
  attr_accessor :keys, :busy

  def initialize
    @keys = [1, 2, 3]
    @bad = []
    @leases = {}
    @log = []
    @reports = []
    @lease_bodies = []
    @n = 0
  end

  def lease_json(exclude)
    k = @keys.find { |x| !exclude.include?(x) && !@bad.include?(x) }
    return nil unless k

    @n += 1
    id = "l/#{@n}"
    @leases[id] = k
    { "lease_id" => id, "key_id" => k, "service" => "serpapi", "secret" => "s#{k}",
      "auth" => { "type" => "query", "name" => "api_key" }, "base_url" => "https://serpapi.com",
      "expires_at" => "2026-11-01T00:00:00Z" }
  end

  def call(method, url, headers, body)
    @log << [method, url, headers, body]
    path = URI(url).path
    json = body ? JSON.parse(body) : nil
    return Acnologia::Response.new(401, {}, "{}") unless headers["Authorization"] == "Bearer acn_x"

    if path == "/api/v1/lease"
      @lease_bodies << json
      if @busy
        return Acnologia::Response.new(503, {}, '{"error":{"code":"all_keys_busy","message":"every key is at its limit"},"retry_after_sec":1}')
      end

      l = lease_json(json["exclude_key_ids"] || [])
      return Acnologia::Response.new(503, {}, '{"error":{"code":"no_key_available","message":"drained"},"retry_after_sec":7}') unless l

      Acnologia::Response.new(200, {}, JSON.generate(l))
    elsif (m = path.match(%r{\A/api/v1/leases/(.+)/report\z}))
      id = URI.decode_www_form_component(m[1])
      @reports << json.merge("_id" => id)
      key = @leases.fetch(id)
      @bad << key unless json["outcome"] == "ok"
      nxt = json["rotate"] ? lease_json([key]) : nil
      Acnologia::Response.new(200, {}, JSON.generate("ok" => true, "next" => nxt))
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
