# frozen_string_literal: true

require "minitest/autorun"
require "json"
require "uri"
require_relative "../lib/cogito"

class FakeServer
  attr_reader :log, :reports, :lease_bodies
  attr_accessor :keys

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
    return Cogito::Response.new(401, {}, "{}") unless headers["Authorization"] == "Bearer cog_x"

    if path == "/api/v1/lease"
      @lease_bodies << json
      l = lease_json(json["exclude_key_ids"] || [])
      return Cogito::Response.new(503, {}, '{"error":{"code":"no_key_available","message":"drained"},"retry_after_sec":7}') unless l

      Cogito::Response.new(200, {}, JSON.generate(l))
    elsif (m = path.match(%r{\A/api/v1/leases/(.+)/report\z}))
      id = URI.decode_www_form_component(m[1])
      @reports << json.merge("_id" => id)
      key = @leases.fetch(id)
      @bad << key unless json["outcome"] == "ok"
      nxt = json["rotate"] ? lease_json([key]) : nil
      Cogito::Response.new(200, {}, JSON.generate("ok" => true, "next" => nxt))
    elsif path == "/api/v1/services"
      Cogito::Response.new(200, {}, '{"services":[{"slug":"serpapi","available":true}]}')
    else
      Cogito::Response.new(200, { "x-cogito-key-id" => "1" }, JSON.generate("url" => url, "body" => body))
    end
  end
end

class CogitoTest < Minitest::Test
  def setup
    @srv = FakeServer.new
    @cog = Cogito::Client.new("http://cogito.test/", "cog_x", transport: @srv.method(:call))
  end

  def test_services
    assert_equal "serpapi", @cog.services.first["slug"]
  end

  def test_lease_and_report
    l = @cog.lease("serpapi", ttl_sec: 60)
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
    l = @cog.lease("serpapi")
    q, h = l.apply(query: { "q" => "a" })
    assert_equal({ "q" => "a", "api_key" => "s1" }, q)
    assert_equal({}, h)
    assert_equal "https://serpapi.com/search.json?q=a", l.url("/search.json", "q" => "a")
    b = Cogito::Lease.new(@cog, "secret" => "z", "auth" => { "type" => "bearer", "name" => "" })
    assert_equal "Bearer z", b.apply[1]["Authorization"]
    hd = Cogito::Lease.new(@cog, "secret" => "z", "auth" => { "type" => "header", "name" => "xi-api-key" })
    assert_equal "z", hd.apply[1]["xi-api-key"]
  end

  def test_with_key_rotates
    seen = []
    out = @cog.with_key("serpapi") do |lease|
      seen << lease.key_id
      raise Cogito::RateLimited.new(retry_after: 5) if lease.key_id == 1
      raise Cogito::QuotaExhausted if lease.key_id == 2

      "done"
    end
    assert_equal "done", out
    assert_equal [1, 2, 3], seen
    assert_equal [%w[rate_limited true], %w[quota_exhausted true], %w[ok false]],
                 @srv.reports.map { |r| [r["outcome"], r["rotate"].to_s] }
  end

  def test_with_key_max_attempts
    assert_raises(Cogito::InvalidKey) do
      @cog.with_key("serpapi", max_attempts: 2) { raise Cogito::InvalidKey }
    end
    assert_equal [true, false], @srv.reports.map { |r| r["rotate"] }
  end

  def test_with_key_other_error
    assert_raises(ZeroDivisionError) { @cog.with_key("serpapi") { 1 / 0 } }
    assert_equal "error", @srv.reports.last["outcome"]
  end

  def test_no_key_available
    @srv.keys = []
    err = assert_raises(Cogito::NoKeyAvailable) { @cog.lease("serpapi") }
    assert_equal 7, err.retry_after
  end

  def test_bad_token
    cog = Cogito::Client.new("http://cogito.test", "cog_bad", transport: @srv.method(:call))
    err = assert_raises(Cogito::Error) { cog.services }
    assert_equal 401, err.status
  end

  def test_proxy
    res = @cog.proxy("serpapi").get("/search.json", "q" => "coffee")
    assert res.ok?
    assert_equal "http://cogito.test/proxy/serpapi/search.json?q=coffee", res.json["url"]
    assert_equal "1", res["X-Cogito-Key-Id"]
    res = @cog.proxy("elevenlabs").post("v1/text-to-speech/v", { "text" => "hi" }, headers: { "Accept" => "audio/mpeg" })
    assert_equal '{"text":"hi"}', res.json["body"]
    assert_equal "application/json", @srv.log.last[2]["Content-Type"]
  end
end
