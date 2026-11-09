defmodule AcnologiaTest do
  # Not async: some tests set ACNOLOGIA_* environment variables.
  use ExUnit.Case, async: false

  alias Acnologia.{
    Capability,
    Error,
    FakeServer,
    ForwardProxy,
    Guide,
    Lease,
    Me,
    Response,
    Service
  }

  alias Acnologia.FakeServer.Request

  doctest Acnologia.Lease

  @token "acn_test_token_0123456789"
  @secret "sk-agi-secret/with+chars"
  @env ~w(ACNOLOGIA_URL ACNOLOGIA_TOKEN ACNOLOGIA_ACCESS_PASSWORD)

  setup do
    saved = Map.new(@env, &{&1, System.get_env(&1)})
    Enum.each(@env, &System.delete_env/1)

    on_exit(fn ->
      Enum.each(saved, fn
        {k, nil} -> System.delete_env(k)
        {k, v} -> System.put_env(k, v)
      end)
    end)

    :ok
  end

  defp serve(handler) do
    srv = FakeServer.start(handler)
    on_exit(fn -> FakeServer.stop(srv) end)
    srv
  end

  defp client(srv, opts \\ []) do
    Acnologia.new!(Keyword.merge([base_url: srv.url, token: @token], opts))
  end

  defp lease_json(overrides \\ %{}) do
    Map.merge(
      %{
        "lease_id" => "lse_1",
        "key_id" => 7,
        "service" => "agi-labs",
        "secret" => @secret,
        "auth" => %{"type" => "header", "name" => "X-Api-Key"},
        "base_url" => "https://agi-labs.example/",
        "expires_at" => "2026-11-04T10:00:00Z",
        "exclusive" => false
      },
      overrides
    )
  end

  defp error_body(code, message \\ "nope", extra \\ %{}) do
    Map.merge(%{"error" => %{"code" => code, "message" => message}}, extra)
  end

  defp next_request do
    assert_receive {:fake_request, req}, 2000
    req
  end

  # config

  describe "new/1" do
    test "reads ACNOLOGIA_URL and ACNOLOGIA_TOKEN when no options are given" do
      System.put_env("ACNOLOGIA_URL", "http://127.0.0.1:9/")
      System.put_env("ACNOLOGIA_TOKEN", "acn_from_env")
      assert {:ok, c} = Acnologia.new()
      assert c.base_url == "http://127.0.0.1:9"
      assert c.token == "acn_from_env"
      assert c.access_password == nil
    end

    test "options win over the environment" do
      System.put_env("ACNOLOGIA_URL", "http://env.invalid")
      System.put_env("ACNOLOGIA_TOKEN", "acn_env")
      {:ok, c} = Acnologia.new(base_url: "https://keys.example", token: "acn_opt")
      assert {c.base_url, c.token} == {"https://keys.example", "acn_opt"}
    end

    test "reads ACNOLOGIA_ACCESS_PASSWORD" do
      System.put_env("ACNOLOGIA_ACCESS_PASSWORD", "gatepw")
      {:ok, c} = Acnologia.new(base_url: "http://x.example", token: @token)
      assert c.access_password == "gatepw"
    end

    test "missing base URL or token is a config error" do
      assert {:error, %Error{reason: :config, message: msg}} = Acnologia.new(token: @token)
      assert msg =~ "ACNOLOGIA_URL"

      assert {:error, %Error{reason: :config, message: msg}} =
               Acnologia.new(base_url: "http://x.example")

      assert msg =~ "ACNOLOGIA_TOKEN"

      assert {:error, %Error{reason: :config}} =
               Acnologia.new(base_url: "ftp://x.example", token: @token)

      assert_raise Error, ~r/base_url is required/, fn -> Acnologia.new!(token: @token) end
    end

    test "inspect hides the token and the access password" do
      {:ok, c} =
        Acnologia.new(base_url: "http://x.example", token: @token, access_password: "gatepw")

      out = inspect(c)
      refute out =~ @token
      refute out =~ "gatepw"
      assert out =~ "x.example"
    end
  end

  # proxy

  describe "proxy/5" do
    test "sends method, path, query, headers and JSON body with the app token" do
      srv =
        serve(fn _ ->
          {200,
           [
             {"X-Acnologia-Key-Id", "12"},
             {"X-Acnologia-Attempts", "2"},
             {"X-Acnologia-Service", "agi-labs"},
             {"X-Acnologia-Cache", "miss"},
             {"X-Upstream", "yes"}
           ], %{"reply" => "hello"}}
        end)

      c = client(srv)

      assert {:ok, %Response{} = resp} =
               Acnologia.proxy(c, :post, "agi-labs", "/v1/chat",
                 query: [model: "small", n: 2],
                 headers: %{"x-trace" => "abc"},
                 json: %{prompt: "hi"}
               )

      req = next_request()
      assert req.method == "POST"
      assert req.path == "/proxy/agi-labs/v1/chat"
      assert req.query == %{"model" => "small", "n" => "2"}
      assert req.headers["authorization"] == "Bearer " <> @token
      assert req.headers["user-agent"] =~ "acnologia-elixir/"
      assert req.headers["x-trace"] == "abc"
      assert req.headers["content-type"] == "application/json"
      refute Map.has_key?(req.headers, "x-acnologia-access")
      refute Map.has_key?(req.headers, "idempotency-key")
      assert Request.json(req) == %{"prompt" => "hi"}

      assert resp.status == 200
      assert resp.key_id == 12
      assert resp.attempts == 2
      assert resp.service == "agi-labs"
      assert resp.cache == "miss"
      assert resp.replayed == false
      assert resp.acnologia["x-acnologia-key-id"] == "12"
      assert Response.header(resp, "X-Upstream") == "yes"
      assert Response.success?(resp)
      assert {:ok, %{"reply" => "hello"}} = Response.json(resp)
    end

    test "the caller cannot replace the app token header" do
      srv = serve(fn _ -> {200, [{"X-Acnologia-Service", "agi-labs"}], ""} end)

      {:ok, _} =
        Acnologia.proxy(client(srv), "GET", "agi-labs", "v1/models",
          headers: [{"Authorization", "Bearer other"}]
        )

      assert next_request().headers["authorization"] == "Bearer " <> @token
    end

    test "sends a raw body as is" do
      srv = serve(fn _ -> {200, [{"X-Acnologia-Service", "agi-labs"}], "ok"} end)

      {:ok, resp} =
        Acnologia.proxy(client(srv), :put, "agi-labs", "/v1/files/a",
          body: "plain text",
          headers: [{"Content-Type", "text/plain"}]
        )

      req = next_request()
      assert req.body == "plain text"
      assert req.headers["content-type"] == "text/plain"
      assert resp.body == "ok"
    end

    test "a provider error comes back as a response, not an error" do
      srv =
        serve(fn _ ->
          {429, [{"X-Acnologia-Service", "agi-labs"}, {"X-Acnologia-Key-Id", "3"}],
           error_body("rate_limit", "slow down")}
        end)

      assert {:ok, %Response{status: 429, key_id: 3}} =
               Acnologia.proxy(client(srv), :get, "agi-labs", "/v1/chat")
    end

    test "a provider answer to a share token (no key id) is still a response" do
      srv =
        serve(fn _ -> {404, [{"X-Acnologia-Service", "agi-labs"}], error_body("not_found")} end)

      assert {:ok, %Response{status: 404, key_id: nil}} =
               Acnologia.proxy(client(srv), :get, "agi-labs", "/v1/missing")
    end

    test "Acnologia's own refusal is an error" do
      srv =
        serve(fn _ ->
          {503, [{"Retry-After", "7"}],
           error_body("no_key_available", "every key is cooling down", %{"retry_after_sec" => 7})}
        end)

      assert {:error, %Error{reason: :no_key_available, status: 503, retry_after: 7} = e} =
               Acnologia.proxy(client(srv), :get, "agi-labs", "/v1/chat")

      assert Error.no_key?(e)
      refute Error.busy?(e)
      assert Error.retryable?(e)

      assert_raise Error, ~r/no_key_available/, fn ->
        Acnologia.proxy!(client(srv), :get, "agi-labs")
      end
    end

    test "Idempotency-Key is sent and a replay is flagged" do
      srv =
        serve(fn _ ->
          {200, [{"X-Acnologia-Service", "agi-labs"}, {"Idempotent-Replayed", "true"}], "{}"}
        end)

      {:ok, resp} =
        Acnologia.proxy(client(srv), :post, "agi-labs", "/v1/chat",
          json: %{},
          idempotency_key: "order-42"
        )

      assert next_request().headers["idempotency-key"] == "order-42"
      assert resp.replayed
    end

    test "sends X-Acnologia-Access only when an access password is set" do
      srv = serve(fn _ -> {200, [{"X-Acnologia-Service", "agi-labs"}], ""} end)

      {:ok, _} = Acnologia.proxy(client(srv, access_password: "gatepw"), :get, "agi-labs")
      assert next_request().headers["x-acnologia-access"] == "gatepw"

      System.put_env("ACNOLOGIA_ACCESS_PASSWORD", "envpw")
      {:ok, _} = Acnologia.proxy(client(srv), :get, "agi-labs")
      assert next_request().headers["x-acnologia-access"] == "envpw"

      System.delete_env("ACNOLOGIA_ACCESS_PASSWORD")
      {:ok, _} = Acnologia.proxy(client(srv), :get, "agi-labs")
      refute Map.has_key?(next_request().headers, "x-acnologia-access")
    end

    test "reads a chunked body" do
      srv =
        serve(fn _ ->
          {200,
           [
             {"X-Acnologia-Service", "agi-labs"},
             {"Content-Type", "text/plain"},
             {"Transfer-Encoding", "chunked"}
           ], "5\r\nhello\r\n7;ext=1\r\n, world\r\n0\r\n\r\n"}
        end)

      assert {:ok, %Response{body: "hello, world"}} =
               Acnologia.proxy(client(srv), :get, "agi-labs", "/v1/stream")
    end

    test "rejects an unknown method" do
      srv = serve(fn _ -> {200, [], ""} end)

      assert {:error, %Error{reason: :invalid_argument}} =
               Acnologia.proxy(client(srv), :trace, "agi-labs")
    end
  end

  # error mapping

  @mapping [
    {401, "unauthenticated", :unauthenticated},
    {403, "unauthenticated", :unauthenticated},
    {403, "service_not_allowed", :service_not_allowed},
    {403, "proxy_not_allowed", :proxy_not_allowed},
    {403, "lease_not_allowed", :lease_not_allowed},
    {403, "lease_revoked", :lease_revoked},
    {503, "no_key_available", :no_key_available},
    {503, "all_keys_busy", :all_keys_busy},
    {503, "service_unavailable", :service_unavailable},
    {503, "empty_group", :empty_group},
    {429, "app_limit_reached", :app_limit_reached},
    {403, "share_revoked", :share_revoked},
    {403, "share_paused", :share_paused},
    {403, "share_expired", :share_expired},
    {404, "lease_not_found", :lease_not_found},
    {404, "unknown_service", :unknown_service},
    {409, "report_failed", :report_failed},
    {410, "report_failed", :report_failed},
    {502, "upstream_unreachable", :upstream_unreachable},
    {502, "upstream_blocked", :upstream_blocked},
    {508, "loop_detected", :loop_detected},
    {400, "bad_path", :bad_path},
    {400, "bad_body", :bad_body},
    {413, "body_too_large", :body_too_large},
    {405, "method_not_allowed", :method_not_allowed},
    {400, "bad_params", :bad_params},
    {503, "no_route", :no_route},
    {503, "no_route_worked", :no_route_worked},
    {503, "", :no_key_available},
    {401, "", :unauthenticated},
    {403, "", :forbidden},
    {403, "some_new_code", :forbidden},
    {500, "internal", :api},
    {400, "bad_json", :api}
  ]

  describe "error mapping" do
    test "maps every status and code like the other SDKs" do
      for {status, code, reason} <- @mapping do
        srv = serve(fn _ -> {status, [], error_body(code, "msg for #{code}")} end)
        c = client(srv)

        assert {:error, %Error{} = e} = Acnologia.lease(c, "agi-labs"),
               "lease #{status} #{code}"

        assert {e.reason, e.status, e.code} == {reason, status, code}

        # The same answer from the proxy (no X-Acnologia-Service) maps the same way.
        assert {:error, %Error{reason: ^reason, status: ^status}} =
                 Acnologia.proxy(c, :get, "agi-labs", "/v1/chat")

        FakeServer.stop(srv)
      end
    end

    test "a non JSON error body keeps a short message" do
      srv = serve(fn _ -> {502, [{"content-type", "text/plain"}], "bad gateway"} end)

      assert {:error, %Error{reason: :api, status: 502, code: "", message: "bad gateway"}} =
               Acnologia.me(client(srv))
    end

    test "all_keys_busy is a no key error and busy" do
      srv =
        serve(fn _ ->
          {503, [], error_body("all_keys_busy", "busy", %{"retry_after_sec" => 1})}
        end)

      {:error, e} = Acnologia.lease(client(srv), "agi-labs")
      assert Error.no_key?(e) and Error.busy?(e)
      assert e.retry_after == 1
      assert Exception.message(e) =~ "retry after 1s"
    end

    test "app_limit_reached carries limit, reset_at and retry_after" do
      srv =
        serve(fn _ ->
          {429, [{"Retry-After", "4210"}],
           error_body("app_limit_reached", "this app reached its daily requests limit", %{
             "limit" => "daily_requests",
             "reset_at" => "2026-11-05T00:00:00Z",
             "retry_after_sec" => 4210
           })}
        end)

      {:error, e} = Acnologia.proxy(client(srv), :get, "agi-labs", "/v1/chat")
      assert e.reason == :app_limit_reached
      assert e.limit == "daily_requests"
      assert e.reset_at == ~U[2026-11-05 00:00:00Z]
      assert e.retry_after == 4210
      assert Exception.message(e) =~ "daily_requests"
    end

    test "Retry-After header is used when the body has no retry_after_sec" do
      srv = serve(fn _ -> {503, [{"Retry-After", "30"}], error_body("service_unavailable")} end)
      {:error, e} = Acnologia.lease(client(srv), "agi-labs")
      assert {e.reason, e.retry_after} == {:service_unavailable, 30}
    end

    test "retry_after_sec in the body wins over Retry-After" do
      srv =
        serve(fn _ ->
          {503, [{"Retry-After", "30"}],
           error_body("no_key_available", "x", %{"retry_after_sec" => 5})}
        end)

      {:error, e} = Acnologia.lease(client(srv), "agi-labs")
      assert e.retry_after == 5
    end

    test "messages never carry the token or the access password" do
      srv =
        serve(fn req ->
          echoed = req.headers["authorization"] <> " " <> req.headers["x-acnologia-access"]
          {401, [], error_body("unauthenticated", "bad token " <> echoed)}
        end)

      {:error, e} = Acnologia.me(client(srv, access_password: "gate-pw-123"))
      refute Exception.message(e) =~ @token
      refute Exception.message(e) =~ "gate-pw-123"
      assert e.message =~ "[redacted]"
    end

    test "a connection failure is a transport error without the token" do
      srv = serve(fn _ -> {200, [], ""} end)
      FakeServer.stop(srv)
      Process.sleep(20)

      {:ok, c} = Acnologia.new(base_url: "http://127.0.0.1:1", token: @token)
      assert {:error, %Error{reason: :transport} = e} = Acnologia.me(c)
      refute Exception.message(e) =~ @token
    end

    test "a slow server is a timeout" do
      srv =
        serve(fn _ ->
          Process.sleep(500)
          {200, [], "{}"}
        end)

      assert {:error, %Error{reason: :timeout}} = Acnologia.me(client(srv, timeout: 100))
    end
  end

  # leases

  describe "lease/3" do
    test "sends every option and parses the lease" do
      srv = serve(fn _ -> {200, [], lease_json(%{"exclusive" => true})} end)

      assert {:ok, %Lease{} = lease} =
               Acnologia.lease(client(srv), "agi-labs",
                 exclude_key_ids: [1, 2],
                 ttl_sec: 90,
                 wait_sec: 2.5,
                 exclusive: true
               )

      req = next_request()
      assert {req.method, req.path} == {"POST", "/api/v1/lease"}
      assert req.headers["authorization"] == "Bearer " <> @token
      assert req.headers["content-type"] == "application/json"

      assert Request.json(req) == %{
               "service" => "agi-labs",
               "exclude_key_ids" => [1, 2],
               "ttl_sec" => 90,
               "wait_sec" => 3,
               "exclusive" => true
             }

      assert lease.lease_id == "lse_1"
      assert lease.key_id == 7
      assert lease.secret == @secret
      assert lease.base_url == "https://agi-labs.example"
      assert lease.auth == %{type: "header", name: "X-Api-Key", prefix: ""}
      assert lease.expires_at == ~U[2026-11-04 10:00:00Z]
      assert lease.exclusive
      refute inspect(lease) =~ @secret
    end

    test "sends only the service by default" do
      srv = serve(fn _ -> {200, [], lease_json()} end)
      {:ok, _} = Acnologia.lease(client(srv), "agi-labs")
      assert Request.json(next_request()) == %{"service" => "agi-labs"}
    end

    test "wait_sec must be 0 to 60" do
      srv = serve(fn _ -> {200, [], lease_json()} end)

      assert {:error, %Error{reason: :invalid_argument}} =
               Acnologia.lease(client(srv), "agi-labs", wait_sec: 61)

      assert_raise Error, fn -> Acnologia.lease!(client(srv), "agi-labs", wait_sec: -1) end
    end
  end

  describe "renew/3" do
    test "extends the lease and returns the new expiry" do
      srv =
        serve(fn _ -> {200, [], %{"ok" => true, "expires_at" => "2026-11-04T11:00:00Z"}} end)

      lease = Lease.from_json(lease_json(%{"lease_id" => "lse/1"}))
      assert {:ok, renewed} = Acnologia.renew(client(srv), lease, ttl_sec: 600)
      assert renewed.expires_at == ~U[2026-11-04 11:00:00Z]
      req = next_request()
      assert req.path == "/api/v1/leases/lse%2F1/renew"
      assert Request.json(req) == %{"ttl_sec" => 600}

      {:ok, _} = Acnologia.renew(client(srv), lease)
      assert Request.json(next_request()) == %{}
    end

    test "lease_not_found" do
      srv = serve(fn _ -> {404, [], error_body("lease_not_found", "lease is not open")} end)
      lease = Lease.from_json(lease_json())

      assert {:error, %Error{reason: :lease_not_found, status: 404}} =
               Acnologia.renew(client(srv), lease)

      assert_raise Error, ~r/lease_not_found/, fn -> Acnologia.renew!(client(srv), lease) end
    end
  end

  describe "report/4" do
    test "sends the outcome and scrubs the secret from the message" do
      srv = serve(fn _ -> {200, [], %{"ok" => true, "next" => nil}} end)
      lease = Lease.from_json(lease_json())

      long = String.duplicate("x", 600)

      assert {:ok, nil} =
               Acnologia.report(client(srv), lease, :rate_limited,
                 status_code: 429,
                 retry_after_sec: 1.2,
                 message:
                   "GET https://agi-labs.example/v1/chat?key=#{URI.encode_www_form(@secret)} " <>
                     @secret <> " " <> long
               )

      req = next_request()
      assert req.path == "/api/v1/leases/lse_1/report"
      body = Request.json(req)
      assert body["outcome"] == "rate_limited"
      assert body["status_code"] == 429
      assert body["retry_after_sec"] == 2
      assert body["rotate"] == false
      refute body["message"] =~ @secret
      refute body["message"] =~ URI.encode_www_form(@secret)
      assert body["message"] =~ "key=[key] [key]"
      assert String.length(body["message"]) == 500
    end

    test "rotate returns the next lease" do
      srv =
        serve(fn _ ->
          {200, [],
           %{"ok" => true, "next" => lease_json(%{"lease_id" => "lse_2", "key_id" => 8})}}
        end)

      lease = Lease.from_json(lease_json())

      assert {:ok, %Lease{lease_id: "lse_2", key_id: 8}} =
               Acnologia.report(client(srv), lease, :quota_exhausted, rotate: true)

      assert Request.json(next_request())["rotate"] == true
    end

    test "rejects an unknown outcome and maps report_failed" do
      srv = serve(fn _ -> {409, [], error_body("report_failed", "already reported")} end)
      lease = Lease.from_json(lease_json())

      assert {:error, %Error{reason: :invalid_argument}} =
               Acnologia.report(client(srv), lease, :weird)

      assert {:error, %Error{reason: :report_failed, status: 409}} =
               Acnologia.report(client(srv), lease, "ok")

      assert_raise Error, fn -> Acnologia.report!(client(srv), lease, :ok) end
    end
  end

  describe "Lease apply" do
    test "header auth sets the header and leaves the query" do
      lease = Lease.from_json(lease_json())

      assert Lease.apply_headers(lease, %{"accept" => "x"}) == [
               {"accept", "x"},
               {"X-Api-Key", @secret}
             ]

      assert Lease.apply_query(lease, q: "1") == [{"q", "1"}]
      assert Lease.url(lease, "v1/chat", q: "1") == "https://agi-labs.example/v1/chat?q=1"
    end

    test "header auth with a prefix and a replaced header" do
      lease =
        Lease.from_json(
          lease_json(%{
            "auth" => %{"type" => "header", "name" => "X-Api-Key", "prefix" => "Key "}
          })
        )

      assert Lease.apply_headers(lease, [{"x-api-key", "old"}]) == [
               {"X-Api-Key", "Key " <> @secret}
             ]
    end

    test "bearer auth uses Authorization and Bearer by default" do
      lease = Lease.from_json(lease_json(%{"auth" => %{"type" => "bearer", "name" => ""}}))
      assert Lease.apply_headers(lease) == [{"Authorization", "Bearer " <> @secret}]

      lease =
        Lease.from_json(
          lease_json(%{"auth" => %{"type" => "bearer", "name" => "", "prefix" => "Token "}})
        )

      assert Lease.apply_headers(lease) == [{"Authorization", "Token " <> @secret}]
    end

    test "query auth puts the secret in the query and the URL" do
      lease = Lease.from_json(lease_json(%{"auth" => %{"type" => "query", "name" => "api_key"}}))
      assert Lease.apply_headers(lease, [{"a", "b"}]) == [{"a", "b"}]

      assert Lease.apply_query(lease, %{"api_key" => "old", "q" => "x"}) == [
               {"q", "x"},
               {"api_key", @secret}
             ]

      assert Lease.url(lease, "/v1/chat", q: "x") ==
               "https://agi-labs.example/v1/chat?q=x&api_key=" <> URI.encode_www_form(@secret)
    end
  end

  describe "with_key/4" do
    test "rotates on a key problem, reports every lease and returns the value" do
      {:ok, agent} = Agent.start_link(fn -> 0 end)

      srv =
        serve(fn req ->
          case req.path do
            "/api/v1/lease" ->
              {200, [], lease_json()}

            "/api/v1/leases/lse_1/report" ->
              {200, [],
               %{"ok" => true, "next" => lease_json(%{"lease_id" => "lse_2", "key_id" => 8})}}

            "/api/v1/leases/lse_2/report" ->
              {200, [], %{"ok" => true, "next" => nil}}
          end
        end)

      result =
        Acnologia.with_key(client(srv), "agi-labs", fn lease ->
          n = Agent.get_and_update(agent, &{&1, &1 + 1})
          if n == 0, do: {:rotate, :rate_limited, status_code: 429}, else: {:ok, lease.key_id}
        end)

      assert result == {:ok, 8}
      assert next_request().path == "/api/v1/lease"
      r1 = next_request()
      assert Request.json(r1)["outcome"] == "rate_limited"
      assert Request.json(r1)["rotate"] == true
      assert Request.json(next_request())["outcome"] == "ok"
    end

    test "gives up after max_attempts and reports an exception" do
      srv =
        serve(fn req ->
          case req.path do
            "/api/v1/lease" -> {200, [], lease_json()}
            _ -> {200, [], %{"ok" => true, "next" => nil}}
          end
        end)

      c = client(srv)

      assert {:error, %Error{reason: :attempts_exhausted, code: "invalid"}} =
               Acnologia.with_key(c, "agi-labs", fn _ -> {:rotate, :invalid} end, max_attempts: 1)

      _lease = next_request()
      assert Request.json(next_request())["rotate"] == false

      assert_raise RuntimeError, fn ->
        Acnologia.with_key(c, "agi-labs", fn lease -> raise "boom " <> lease.secret end)
      end

      _lease = next_request()
      body = Request.json(next_request())
      assert body["outcome"] == "error"
      assert body["message"] =~ "boom [key]"
    end
  end

  # me

  describe "me/1" do
    test "parses the token's access" do
      srv =
        serve(fn _ ->
          {200, [],
           %{
             "app" => %{"id" => 3, "name" => "Website"},
             "limits" => %{
               "daily_requests" => 500,
               "monthly_requests" => nil,
               "daily_budget" => nil,
               "monthly_budget" => 20
             },
             "usage" => %{
               "today_requests" => 12,
               "today_cost" => 0.5,
               "month_requests" => 40,
               "month_cost" => 2,
               "last_used_at" => "2026-11-04T09:00:00Z",
               "limit_hit" => nil
             },
             "limit_hit" => %{"limit" => "daily_requests", "reset_at" => "2026-11-05T00:00:00Z"},
             "services" => [
               %{
                 "slug" => "agi-labs",
                 "name" => "AGI Labs",
                 "provider" => "custom",
                 "category" => "ai",
                 "available" => 1,
                 "logo_url" => nil
               }
             ],
             "share" => nil
           }}
        end)

      assert {:ok, %Me{} = me} = Acnologia.me(client(srv))
      req = next_request()
      assert {req.method, req.path} == {"GET", "/api/v1/me"}
      assert req.headers["accept"] == "application/json"
      assert me.app == %{id: 3, name: "Website"}
      assert me.limits.daily_requests == 500
      assert me.limits.monthly_budget == 20
      assert me.usage.today_requests == 12
      assert me.usage.last_used_at == ~U[2026-11-04 09:00:00Z]
      assert me.limit_hit == %{limit: "daily_requests", reset_at: ~U[2026-11-05 00:00:00Z]}
      assert [%{slug: "agi-labs", available: 1}] = me.services
      assert me.share == nil
      assert me.raw["app"]["name"] == "Website"
      assert %Me{} = Acnologia.me!(client(srv))
    end

    test "parses a share" do
      srv =
        serve(fn _ ->
          {200, [],
           %{
             "app" => %{"id" => 9, "name" => "Shared"},
             "services" => [],
             "share" => %{
               "id" => "shr_1",
               "status" => "paused",
               "mode" => "proxy",
               "ends_at" => nil
             }
           }}
        end)

      {:ok, me} = Acnologia.me(client(srv))
      assert me.share.status == "paused"
      assert me.share.ends_at == nil
      assert me.limit_hit == nil
    end
  end

  test "scrub_message/2 hides raw and encoded secrets" do
    assert Acnologia.scrub_message("a #{@secret} b #{URI.encode_www_form(@secret)}", @secret) ==
             "a [key] b [key]"

    assert Acnologia.scrub_message(nil, @secret) == nil
  end

  # 0.2.0

  @fp_json %{
    "proxy_url" => "http://acn.example:8888",
    "proxy_url_with_token" => "http://app:%3Capp%20token%3E@acn.example:8888",
    "ca_url" => "http://acn.example/api/v1/proxy/ca.pem",
    "ca_fingerprint" => "SHA256:ab:cd",
    "passthrough" => true,
    "hosts" => [
      %{"host" => "agi-labs.example", "service" => "agi-labs", "name" => "AGI Labs", "path" => ""}
    ],
    "env" => %{"HTTPS_PROXY" => "http://app:%3Capp%20token%3E@acn.example:8888"},
    "service_header" => "X-Acnologia-Service"
  }

  describe "0.2.0 surface" do
    test "version is 0.2.0" do
      assert Mix.Project.config()[:version] == "0.2.0"
      assert Acnologia.HTTP.user_agent() == "acnologia-elixir/0.2.0"
    end

    test "services/1 parses guide_url" do
      srv =
        serve(fn _ ->
          {200, [],
           %{
             "services" => [
               %{
                 "slug" => "agi-labs",
                 "name" => "AGI Labs",
                 "provider" => "custom",
                 "category" => "ai",
                 "available" => true,
                 "base_url" => "https://agi-labs.example",
                 "auth" => %{"type" => "bearer", "name" => "Authorization", "prefix" => ""},
                 "guide_url" => "http://acn.example/api/v1/services/agi-labs/guide"
               }
             ]
           }}
        end)

      assert {:ok, [%Service{} = s]} = Acnologia.services(client(srv))
      req = next_request()
      assert {req.method, req.path} == {"GET", "/api/v1/services"}
      assert req.headers["authorization"] == "Bearer " <> @token
      assert s.slug == "agi-labs"
      assert s.available
      assert s.auth.type == "bearer"
      assert s.guide_url == "http://acn.example/api/v1/services/agi-labs/guide"
    end

    test "guide/2 and guide_markdown/2" do
      srv =
        serve(fn req ->
          if req.headers["accept"] == "text/markdown" do
            {200, [{"content-type", "text/markdown; charset=utf-8"}], "# AGI Labs é"}
          else
            {200, [],
             %{"markdown" => "# AGI Labs", "generated" => true, "guide" => %{"summary" => "chat"}}}
          end
        end)

      c = client(srv)
      assert {:ok, %Guide{} = g} = Acnologia.guide(c, "agi labs")
      req = next_request()
      assert {req.method, req.path} == {"GET", "/api/v1/services/agi%20labs/guide"}
      assert req.headers["accept"] == "application/json"
      assert g.markdown == "# AGI Labs"
      assert g.generated
      assert g.guide["summary"] == "chat"

      assert {:ok, "# AGI Labs é"} = Acnologia.guide_markdown(c, "agi-labs")
      req = next_request()
      assert {req.method, req.path} == {"GET", "/api/v1/services/agi-labs/guide"}
      assert req.headers["accept"] == "text/markdown"
      assert req.headers["authorization"] == "Bearer " <> @token
    end

    test "guide_markdown/2 for a service not allowed" do
      srv = serve(fn _ -> {403, [], error_body("service_not_allowed")} end)

      assert {:error, %Error{reason: :service_not_allowed}} =
               Acnologia.guide_markdown(client(srv), "x")
    end

    test "capabilities/1" do
      srv =
        serve(fn _ ->
          {200, [],
           %{
             "capabilities" => [
               %{
                 "slug" => "places",
                 "name" => "Places",
                 "description" => "find places",
                 "strategy" => "priority",
                 "params" => [%{"name" => "q", "type" => "string", "required" => true}],
                 "routes" => [
                   %{"service" => "serpapi", "mapped" => true},
                   %{"service" => "gmaps", "mapped" => false}
                 ],
                 "url" => "http://acn.example/proxy/places"
               }
             ]
           }}
        end)

      assert {:ok, [%Capability{} = cap]} = Acnologia.capabilities(client(srv))
      assert next_request().path == "/api/v1/capabilities"
      assert cap.slug == "places"
      assert cap.strategy == "priority"
      assert [%{"name" => "q"}] = cap.params

      assert cap.routes == [
               %{service: "serpapi", mapped: true},
               %{service: "gmaps", mapped: false}
             ]

      assert cap.url == "http://acn.example/proxy/places"
    end

    test "capability/4 posts the params and reads the answer headers" do
      srv =
        serve(fn _ ->
          {200,
           [
             {"X-Acnologia-Service", "gmaps"},
             {"X-Acnologia-Capability", "places"},
             {"X-Acnologia-Tried", "serpapi=429, gmaps=200"}
           ], %{"items" => [], "count" => 0}}
        end)

      c = client(srv)

      assert {:ok, %Response{} = resp} =
               Acnologia.capability(c, "places", %{q: "coffee"},
                 service: "gmaps",
                 raw: true,
                 idempotency_key: "k1",
                 no_cache: true
               )

      req = next_request()
      assert {req.method, req.path} == {"POST", "/proxy/places"}
      assert req.query == %{"_service" => "gmaps", "_raw" => "1"}
      assert req.headers["authorization"] == "Bearer " <> @token
      assert req.headers["content-type"] == "application/json"
      assert req.headers["idempotency-key"] == "k1"
      assert req.headers["cache-control"] == "no-cache"
      assert Request.json(req) == %{"q" => "coffee"}
      assert resp.service == "gmaps"
      assert resp.capability == "places"
      assert resp.tried == [%{service: "serpapi", status: 429}, %{service: "gmaps", status: 200}]

      {:ok, _} = Acnologia.capability(c, "places")
      req = next_request()
      assert {req.path, req.raw_query} == {"/proxy/places", ""}
      assert Request.json(req) == %{}
      refute Map.has_key?(req.headers, "cache-control")
    end

    test "capability/4 bad_params is an error" do
      srv = serve(fn _ -> {400, [], error_body("bad_params", "q is required")} end)

      assert {:error, %Error{reason: :bad_params, code: "bad_params", status: 400}} =
               Acnologia.capability(client(srv), "places", %{})
    end

    test "proxy no_cache sends Cache-Control: no-cache" do
      srv =
        serve(fn _ ->
          {200, [{"X-Acnologia-Service", "agi-labs"}, {"X-Acnologia-Cache", "miss"}], ""}
        end)

      {:ok, resp} = Acnologia.proxy(client(srv), :get, "agi-labs", "/v1/models", no_cache: true)
      assert next_request().headers["cache-control"] == "no-cache"
      assert resp.cache == "miss"
      assert resp.capability == nil
      assert resp.tried == []
    end

    test "Response.parse_tried/1" do
      assert Response.parse_tried(nil) == []
      assert Response.parse_tried("") == []

      assert Response.parse_tried("a=200,b=x, c") == [
               %{service: "a", status: 200},
               %{service: "b", status: 0},
               %{service: "c", status: 0}
             ]
    end

    test "forward_proxy/1 parses the setup" do
      srv = serve(fn _ -> {200, [], @fp_json} end)
      assert {:ok, %ForwardProxy{} = fp} = Acnologia.forward_proxy(client(srv))
      req = next_request()
      assert {req.method, req.path} == {"GET", "/api/v1/proxy"}
      assert req.headers["authorization"] == "Bearer " <> @token
      assert fp.proxy_url == "http://acn.example:8888"
      assert fp.ca_fingerprint == "SHA256:ab:cd"
      assert fp.passthrough
      assert [%{host: "agi-labs.example", service: "agi-labs"}] = fp.hosts
      assert fp.service_header == "X-Acnologia-Service"
      refute inspect(fp) =~ @token
    end

    test "forward_proxy_ca/1 sends no token, and save_forward_proxy_ca/2 writes it" do
      pem = "-----BEGIN CERTIFICATE-----\nAAA\n-----END CERTIFICATE-----\n"
      srv = serve(fn _ -> {200, [{"content-type", "application/x-pem-file"}], pem} end)
      c = client(srv, access_password: "gatepw")

      assert {:ok, ^pem} = Acnologia.forward_proxy_ca(c)
      req = next_request()
      assert {req.method, req.path} == {"GET", "/api/v1/proxy/ca.pem"}
      refute Map.has_key?(req.headers, "authorization")
      assert req.headers["x-acnologia-access"] == "gatepw"

      path = Path.join(System.tmp_dir!(), "acn-ca-#{System.unique_integer([:positive])}.pem")
      on_exit(fn -> File.rm(path) end)
      assert {:ok, ^path} = Acnologia.save_forward_proxy_ca(c, path)
      assert File.read!(path) == pem
    end

    test "forward_proxy_url/1 and forward_proxy_env/2 put the token in the URL" do
      srv = serve(fn _ -> {200, [], @fp_json} end)
      c = Acnologia.new!(base_url: srv.url, token: "acn_a/b+c")
      url = "http://app:acn_a%2Fb%2Bc@acn.example:8888"

      assert {:ok, ^url} = Acnologia.forward_proxy_url(c)

      assert Acnologia.forward_proxy_env!(c) == %{
               "HTTPS_PROXY" => url,
               "HTTP_PROXY" => url,
               "NODE_USE_ENV_PROXY" => "1",
               "NODE_EXTRA_CA_CERTS" => "acnologia-ca.pem",
               "REQUESTS_CA_BUNDLE" => "acnologia-ca.pem",
               "SSL_CERT_FILE" => "acnologia-ca.pem"
             }

      assert {:ok, %{"SSL_CERT_FILE" => "/etc/acn.pem"}} =
               Acnologia.forward_proxy_env(c, "/etc/acn.pem")
    end

    test "app_limit_reached?/1" do
      srv =
        serve(fn _ ->
          {429, [],
           error_body("app_limit_reached", "limit", %{
             "limit" => "monthly_budget",
             "reset_at" => "2026-12-01T00:00:00Z",
             "retry_after_sec" => 60
           })}
        end)

      {:error, e} = Acnologia.me(client(srv))
      assert Error.app_limit_reached?(e)
      refute Error.share_unavailable?(e)

      assert {e.limit, e.reset_at, e.retry_after} ==
               {"monthly_budget", ~U[2026-12-01 00:00:00Z], 60}
    end

    test "share errors carry the share status" do
      for st <- ~w(revoked paused expired) do
        srv =
          serve(fn _ -> {403, [{"X-Acnologia-Share", st}], error_body("share_" <> st)} end)

        {:error, e} = Acnologia.proxy(client(srv), :get, "agi-labs", "/v1/chat")
        assert Error.share_unavailable?(e)
        assert e.share == st
        assert e.reason == String.to_atom("share_" <> st)
        FakeServer.stop(srv)
      end

      srv = serve(fn _ -> {403, [{"X-Acnologia-Share", "revoked"}], "forbidden"} end)
      {:error, e} = Acnologia.me(client(srv))
      assert {e.reason, e.share, e.code} == {:share_revoked, "revoked", ""}
    end

    test "me/1 parses available as a count and logo_url" do
      srv =
        serve(fn _ ->
          {200, [],
           %{
             "app" => %{"id" => 1, "name" => "a"},
             "services" => [
               %{"slug" => "s", "available" => 3, "logo_url" => "data:image/png;base64,AA"}
             ]
           }}
        end)

      {:ok, me} = Acnologia.me(client(srv))
      assert [%{available: 3, logo_url: "data:image/png;base64,AA"}] = me.services
    end
  end
end
