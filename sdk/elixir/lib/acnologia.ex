defmodule Acnologia do
  @moduledoc """
  Client for Acnologia, a self-hosted API key server.

  Your code holds an app token (`acn_...`), never a provider key. Call a
  provider through the proxy with `proxy/5`, or borrow a key with `lease/3`
  when your code has to hold it, then `report/4` how it went.

      {:ok, acn} = Acnologia.new(base_url: "https://keys.example.com", token: "acn_...")

      {:ok, resp} = Acnologia.proxy(acn, :post, "agi-labs", "/v1/chat", json: %{prompt: "hi"})
      resp.status

  Every function returns `{:ok, value}` or `{:error, %Acnologia.Error{}}`.
  The bang variants return the value or raise the error.
  """

  alias Acnologia.{
    Capability,
    Client,
    Error,
    ForwardProxy,
    Guide,
    HTTP,
    Lease,
    Me,
    Response,
    Service
  }

  @max_wait_sec 60
  @max_report_message 500
  @outcomes ~w(ok rate_limited busy quota_exhausted invalid banned error)
  @rotating ~w(rate_limited busy quota_exhausted invalid banned)
  @methods ~w(get head post put patch delete options)a

  @type client :: Client.t()
  @type result(t) :: {:ok, t} | {:error, Error.t()}
  @type outcome ::
          :ok | :rate_limited | :busy | :quota_exhausted | :invalid | :banned | :error

  # client

  @doc """
  Builds a client.

  Options:

    * `:base_url` where Acnologia runs. Defaults to `ACNOLOGIA_URL`.
    * `:token` the app token, `acn_...`. Defaults to `ACNOLOGIA_TOKEN`.
    * `:access_password` sent as `X-Acnologia-Access`, for a server behind an
      access password. Defaults to `ACNOLOGIA_ACCESS_PASSWORD`; when neither
      is set the header is not sent.
    * `:timeout` how long to wait for an answer, in milliseconds. Default
      30000. A lease that waits for a free key gets at least its wait plus 10
      seconds.
    * `:connect_timeout` in milliseconds. Default 10000.
    * `:ssl` options for `:ssl` that replace the defaults (certificate
      verification against the system trust store).

  Returns `{:error, %Acnologia.Error{reason: :config}}` when the base URL or
  the token is missing, or the base URL is not http or https.
  """
  @spec new(keyword()) :: result(Client.t())
  def new(opts \\ []) do
    base = opts[:base_url] || env("ACNOLOGIA_URL")
    token = opts[:token] || env("ACNOLOGIA_TOKEN")
    access = opts[:access_password] || env("ACNOLOGIA_ACCESS_PASSWORD")

    cond do
      base in [nil, ""] ->
        {:error, Error.new(:config, "base_url is required (or set ACNOLOGIA_URL)")}

      token in [nil, ""] ->
        {:error, Error.new(:config, "token is required (or set ACNOLOGIA_TOKEN)")}

      not valid_base?(base) ->
        {:error, Error.new(:config, "base_url must be an http or https URL")}

      true ->
        {:ok,
         %Client{
           base_url: String.trim_trailing(base, "/"),
           token: token,
           access_password: access,
           timeout: Keyword.get(opts, :timeout, 30_000),
           connect_timeout: Keyword.get(opts, :connect_timeout, 10_000),
           ssl: opts[:ssl]
         }}
    end
  end

  @doc "Like `new/1`, but returns the client or raises `Acnologia.Error`."
  @spec new!(keyword()) :: Client.t()
  def new!(opts \\ []), do: unwrap(new(opts))

  # proxy

  @doc """
  Sends a request to `/proxy/<service>/<path>`. Acnologia adds a key, moves
  to the next key on a rate limit, and relays the provider's answer. The
  secret never reaches your process.

  `method` is an atom (`:get`, `:post`, `:put`, `:patch`, `:delete`,
  `:head`, `:options`) or a string. `service` is a service or group slug.

  Options:

    * `:query` a map or keyword list for the query string
    * `:headers` a map or list of `{name, value}` pairs for the provider
    * `:json` any term, sent as JSON with `content-type: application/json`
    * `:body` iodata sent as is (set `content-type` in `:headers`). A map or
      list that is not iodata is sent as JSON.
    * `:idempotency_key` sent as `Idempotency-Key`, so a retried call is
      answered once and replayed after
    * `:no_cache` when true, sends `Cache-Control: no-cache` so a cached
      answer is not used
    * `:timeout` in milliseconds, for this call only

  The provider's answer comes back as `{:ok, %Acnologia.Response{}}` even
  when its status is not 2xx. When Acnologia itself refuses the call (no key
  available, app limit reached, service not allowed and so on) you get
  `{:error, %Acnologia.Error{}}` instead.
  """
  @spec proxy(client(), atom() | String.t(), String.t(), String.t(), keyword()) ::
          result(Response.t())
  def proxy(%Client{} = c, method, service, path \\ "", opts \\ []) do
    with {:ok, method} <- method(method),
         {:ok, body, ctype} <- proxy_body(opts) do
      url =
        c.base_url <>
          "/proxy/" <>
          URI.encode(to_string(service), &URI.char_unreserved?/1) <>
          "/" <> String.trim_leading(path, "/") <> query_string(opts[:query])

      send_proxy(c, method, url, body, ctype, opts)
    end
  end

  defp send_proxy(c, method, url, body, ctype, opts) do
    headers =
      HTTP.to_headers(opts[:headers])
      |> then(fn h -> if ctype, do: HTTP.put_headers(h, [{"content-type", ctype}]), else: h end)
      |> then(fn h ->
        case opts[:idempotency_key] do
          nil -> h
          key -> HTTP.put_headers(h, [{"idempotency-key", to_string(key)}])
        end
      end)
      |> then(fn h ->
        if opts[:no_cache] == true,
          do: HTTP.put_headers(h, [{"cache-control", "no-cache"}]),
          else: h
      end)
      |> HTTP.put_headers(HTTP.auth_headers(c))

    http_opts = if opts[:timeout], do: [timeout: opts[:timeout]], else: []

    case HTTP.request(c, method, url, headers, body, http_opts) do
      {:ok, {status, rh, rb}} ->
        if acnologia_refusal?(status, rh, rb) do
          {:error, Error.from_response(status, rh, rb, Client.secrets(c))}
        else
          {:ok, Response.new(status, rh, rb)}
        end

      {:error, _} = err ->
        err
    end
  end

  @doc "Like `proxy/5`, but returns the response or raises `Acnologia.Error`."
  @spec proxy!(client(), atom() | String.t(), String.t(), String.t(), keyword()) :: Response.t()
  def proxy!(c, method, service, path \\ "", opts \\ []),
    do: unwrap(proxy(c, method, service, path, opts))

  # A provider's answer always carries X-Acnologia-Service. An error without
  # it, with Acnologia's error shape, is Acnologia's own refusal.
  defp acnologia_refusal?(status, headers, body) when status < 200 or status >= 300 do
    if List.keymember?(headers, "x-acnologia-service", 0) do
      false
    else
      match?({:ok, %{"error" => %{}}}, JSON.decode(body))
    end
  end

  defp acnologia_refusal?(_, _, _), do: false

  defp proxy_body(opts) do
    cond do
      Keyword.has_key?(opts, :json) ->
        encode_json(opts[:json])

      is_nil(opts[:body]) ->
        {:ok, nil, nil}

      is_binary(opts[:body]) ->
        {:ok, opts[:body], nil}

      is_map(opts[:body]) ->
        encode_json(opts[:body])

      is_list(opts[:body]) ->
        try do
          {:ok, IO.iodata_to_binary(opts[:body]), nil}
        rescue
          ArgumentError -> encode_json(opts[:body])
        end

      true ->
        {:error, Error.new(:invalid_argument, "body must be iodata, a map or a list")}
    end
  end

  defp encode_json(term) do
    {:ok, JSON.encode!(term), "application/json"}
  rescue
    e -> {:error, Error.new(:invalid_argument, "could not encode JSON: " <> Exception.message(e))}
  end

  # leases

  @doc """
  Borrows a key for `service`.

  Options:

    * `:exclude_key_ids` key ids the server must not hand out, for example
      keys you already tried
    * `:ttl_sec` lease lifetime in seconds; the server default applies when
      left out
    * `:wait_sec` 0 to 60 seconds the server may wait for a free slot when
      every usable key is at its concurrency limit
    * `:exclusive` keep the key from every other caller until you report or
      the lease expires

  Report every lease with `report/4`: on a service with a concurrency limit
  an open lease holds one of the key's slots until it is reported or
  expires. Fails with `:no_key_available`, `:all_keys_busy`,
  `:service_unavailable`, `:lease_not_allowed` and the like.
  """
  @spec lease(client(), String.t(), keyword()) :: result(Lease.t())
  def lease(%Client{} = c, service, opts \\ []) do
    wait = opts[:wait_sec]

    if wait != nil and (not is_number(wait) or wait < 0 or wait > @max_wait_sec) do
      {:error, Error.new(:invalid_argument, "wait_sec must be between 0 and #{@max_wait_sec}")}
    else
      body =
        %{"service" => to_string(service)}
        |> put_if("exclude_key_ids", List.wrap(opts[:exclude_key_ids]), &(&1 != []))
        |> put_if("ttl_sec", ceil_int(opts[:ttl_sec]), &(&1 != nil))
        |> put_if("wait_sec", ceil_int(wait), &(&1 not in [nil, 0]))
        |> put_if("exclusive", true, fn _ -> opts[:exclusive] == true end)

      timeout = if wait, do: max(c.timeout, ceil(wait * 1000) + 10_000), else: c.timeout

      with {:ok, data} <- call(c, :post, "/api/v1/lease", body, timeout: timeout) do
        {:ok, Lease.from_json(data)}
      end
    end
  end

  @doc "Like `lease/3`, but returns the lease or raises `Acnologia.Error`."
  @spec lease!(client(), String.t(), keyword()) :: Lease.t()
  def lease!(c, service, opts \\ []), do: unwrap(lease(c, service, opts))

  @doc """
  Extends an open lease so a long job keeps its key. Returns the lease with
  the new `expires_at`.

  Options: `:ttl_sec`, the new lifetime from now. Left out, the server uses
  its default (120 seconds); it caps the value at 24 hours.

  Fails with `:lease_not_found` when the lease is no longer open, and
  `:lease_revoked` when the app may no longer hold the key.
  """
  @spec renew(client(), Lease.t(), keyword()) :: result(Lease.t())
  def renew(%Client{} = c, %Lease{} = lease, opts \\ []) do
    body =
      case ceil_int(opts[:ttl_sec]) do
        n when is_integer(n) and n > 0 -> %{"ttl_sec" => n}
        _ -> %{}
      end

    with {:ok, data} <- call(c, :post, "/api/v1/leases/#{seg(lease.lease_id)}/renew", body) do
      case Error.parse_time(data["expires_at"]) do
        nil -> {:error, Error.new(:decode, "renew response has no expires_at")}
        at -> {:ok, %{lease | expires_at: at}}
      end
    end
  end

  @doc "Like `renew/3`, but returns the lease or raises `Acnologia.Error`."
  @spec renew!(client(), Lease.t(), keyword()) :: Lease.t()
  def renew!(c, lease, opts \\ []), do: unwrap(renew(c, lease, opts))

  @doc """
  Reports how the key did. `outcome` is one of `:ok`, `:rate_limited`,
  `:busy`, `:quota_exhausted`, `:invalid`, `:banned` or `:error` (or the
  same as a string).

  Options:

    * `:status_code` the provider's HTTP status
    * `:retry_after_sec` how long the key should cool down
    * `:message` what went wrong. The lease's secret is replaced with
      `[key]` and the text is cut to 500 characters before it is sent.
    * `:rotate` ask for the next key in the same call

  Returns `{:ok, next_lease}` when `rotate` is true and another key is free,
  and `{:ok, nil}` otherwise. Fails with `:report_failed` when the lease was
  already reported or expired long ago.
  """
  @spec report(client(), Lease.t(), outcome() | String.t(), keyword()) ::
          result(Lease.t() | nil)
  def report(%Client{} = c, %Lease{} = lease, outcome, opts \\ []) do
    outcome = to_string(outcome)

    if outcome not in @outcomes do
      {:error,
       Error.new(:invalid_argument, "outcome must be one of " <> Enum.join(@outcomes, ", "))}
    else
      body =
        %{"outcome" => outcome, "rotate" => opts[:rotate] == true}
        |> put_if("status_code", opts[:status_code], &is_integer/1)
        |> put_if("retry_after_sec", ceil_int(opts[:retry_after_sec]), &(&1 != nil))
        |> put_if("message", scrub_message(opts[:message], lease.secret), &(&1 not in [nil, ""]))

      with {:ok, data} <- call(c, :post, "/api/v1/leases/#{seg(lease.lease_id)}/report", body) do
        case data["next"] do
          %{} = next -> {:ok, Lease.from_json(next)}
          _ -> {:ok, nil}
        end
      end
    end
  end

  @doc "Like `report/4`, but returns the next lease (or nil) or raises `Acnologia.Error`."
  @spec report!(client(), Lease.t(), outcome() | String.t(), keyword()) :: Lease.t() | nil
  def report!(c, lease, outcome, opts \\ []), do: unwrap(report(c, lease, outcome, opts))

  @doc """
  Leases a key, calls `fun` with it and reports every lease it takes, even
  when `fun` raises.

  `fun` returns one of:

    * `{:ok, value}` reports `ok` and returns `{:ok, value}`
    * `{:rotate, outcome}` or `{:rotate, outcome, report_opts}` with
      `:rate_limited`, `:busy`, `:quota_exhausted`, `:invalid` or `:banned`
      reports the key and calls `fun` again with the next one. After
      `:max_attempts` keys it returns
      `{:error, %Acnologia.Error{reason: :attempts_exhausted}}`.
    * `{:error, reason}` reports `error` and returns `{:error, reason}`
    * anything else counts as success and is returned as `{:ok, value}`

  `report_opts` takes `:status_code`, `:retry_after_sec` and `:message`.
  An exception in `fun` is reported as `error` and raised again.

  Options: `:max_attempts` (default 5), plus `:ttl_sec`, `:wait_sec` and
  `:exclusive` for every lease.
  """
  @spec with_key(client(), String.t(), (Lease.t() -> term()), keyword()) :: result(term())
  def with_key(%Client{} = c, service, fun, opts \\ []) when is_function(fun, 1) do
    max = Keyword.get(opts, :max_attempts, 5)
    lease_opts = Keyword.take(opts, [:ttl_sec, :wait_sec, :exclusive])

    if not is_integer(max) or max < 1 do
      {:error, Error.new(:invalid_argument, "max_attempts must be at least 1")}
    else
      with {:ok, lease} <- lease(c, service, lease_opts) do
        with_key_loop(c, service, fun, lease, lease_opts, max, 1, [])
      end
    end
  end

  defp with_key_loop(c, service, fun, lease, lease_opts, max, attempt, tried) do
    tried = [lease.key_id | tried]

    result =
      try do
        fun.(lease)
      rescue
        e ->
          report(c, lease, :error,
            message: Exception.format_banner(:error, e, __STACKTRACE__) |> String.slice(0, 500)
          )

          reraise e, __STACKTRACE__
      catch
        kind, value ->
          report(c, lease, :error, message: Exception.format_banner(kind, value, __STACKTRACE__))
          :erlang.raise(kind, value, __STACKTRACE__)
      end

    case result do
      {:rotate, outcome} ->
        rotate(c, service, fun, lease, lease_opts, max, attempt, tried, outcome, [])

      {:rotate, outcome, ropts} when is_list(ropts) ->
        rotate(c, service, fun, lease, lease_opts, max, attempt, tried, outcome, ropts)

      {:error, _} = err ->
        report(c, lease, :error, message: inspect(elem(err, 1)))
        err

      {:ok, value} ->
        done(c, lease, value)

      value ->
        done(c, lease, value)
    end
  end

  defp done(c, lease, value) do
    with {:ok, _} <- report(c, lease, :ok), do: {:ok, value}
  end

  defp rotate(c, service, fun, lease, lease_opts, max, attempt, tried, outcome, ropts) do
    outcome = to_string(outcome)

    if outcome not in @rotating do
      report(c, lease, :error, message: "with_key: unknown rotate outcome")

      {:error,
       Error.new(
         :invalid_argument,
         "rotate outcome must be one of " <> Enum.join(@rotating, ", ")
       )}
    else
      last = attempt >= max
      ropts = Keyword.take(ropts, [:status_code, :retry_after_sec, :message])

      with {:ok, next} <- report(c, lease, outcome, [rotate: not last] ++ ropts) do
        cond do
          last ->
            {:error,
             %Error{
               reason: :attempts_exhausted,
               code: outcome,
               status: ropts[:status_code],
               message: "gave up after #{max} keys",
               retry_after: ropts[:retry_after_sec]
             }}

          next ->
            with_key_loop(c, service, fun, next, lease_opts, max, attempt + 1, tried)

          true ->
            with {:ok, next} <-
                   lease(
                     c,
                     service,
                     Keyword.put(lease_opts, :exclude_key_ids, Enum.reverse(tried))
                   ) do
              with_key_loop(c, service, fun, next, lease_opts, max, attempt + 1, tried)
            end
        end
      end
    end
  end

  # me

  @doc """
  Returns what the token may do: the app, its limits and use, the services
  it may call and, for a share's token, the share (`GET /api/v1/me`).
  """
  @spec me(client()) :: result(Me.t())
  def me(%Client{} = c) do
    with {:ok, data} <- call(c, :get, "/api/v1/me", nil) do
      {:ok, Me.from_json(data)}
    end
  end

  @doc "Like `me/1`, but returns the struct or raises `Acnologia.Error`."
  @spec me!(client()) :: Me.t()
  def me!(c), do: unwrap(me(c))

  # services and guides

  @doc """
  Lists the services this token may use (`GET /api/v1/services`), each with
  its `guide_url`.
  """
  @spec services(client()) :: result([Service.t()])
  def services(%Client{} = c) do
    with {:ok, data} <- call(c, :get, "/api/v1/services", nil) do
      {:ok, for(s <- List.wrap(data["services"]), is_map(s), do: Service.from_json(s))}
    end
  end

  @doc "Like `services/1`, but returns the list or raises `Acnologia.Error`."
  @spec services!(client()) :: [Service.t()]
  def services!(c), do: unwrap(services(c))

  @doc """
  Returns the usage guide of a service the token may use
  (`GET /api/v1/services/{slug}/guide`): its Markdown, whether it was
  generated, and the structured guide. Fails with `:service_not_allowed`
  for a service the token may not use.
  """
  @spec guide(client(), String.t()) :: result(Guide.t())
  def guide(%Client{} = c, slug) do
    with {:ok, data} <- call(c, :get, "/api/v1/services/#{seg(slug)}/guide", nil) do
      {:ok, Guide.from_json(data)}
    end
  end

  @doc "Like `guide/2`, but returns the guide or raises `Acnologia.Error`."
  @spec guide!(client(), String.t()) :: Guide.t()
  def guide!(c, slug), do: unwrap(guide(c, slug))

  @doc """
  Returns the usage guide of a service as Markdown, ready for an agent's
  prompt (the same route with `Accept: text/markdown`).
  """
  @spec guide_markdown(client(), String.t()) :: result(String.t())
  def guide_markdown(%Client{} = c, slug) do
    text(c, "/api/v1/services/#{seg(slug)}/guide", "text/markdown", HTTP.auth_headers(c))
  end

  @doc "Like `guide_markdown/2`, but returns the Markdown or raises `Acnologia.Error`."
  @spec guide_markdown!(client(), String.t()) :: String.t()
  def guide_markdown!(c, slug), do: unwrap(guide_markdown(c, slug))

  # capabilities

  @doc """
  Lists the capabilities this app may call (`GET /api/v1/capabilities`).
  Empty for a share's token.
  """
  @spec capabilities(client()) :: result([Capability.t()])
  def capabilities(%Client{} = c) do
    with {:ok, data} <- call(c, :get, "/api/v1/capabilities", nil) do
      {:ok, for(x <- List.wrap(data["capabilities"]), is_map(x), do: Capability.from_json(x))}
    end
  end

  @doc "Like `capabilities/1`, but returns the list or raises `Acnologia.Error`."
  @spec capabilities!(client()) :: [Capability.t()]
  def capabilities!(c), do: unwrap(capabilities(c))

  @doc """
  Calls a capability: `POST /proxy/<slug>` with `params` (a map) as the JSON
  body. Acnologia picks a provider, translates the params, rotates keys and
  moves to the next provider when one fails.

  Options:

    * `:service` pins one provider (`?_service=<slug>`)
    * `:raw` when true, returns the provider's own body (`?_raw=1`)
    * `:headers`, `:idempotency_key`, `:no_cache` and `:timeout`, as for
      `proxy/5`

  Returns `{:ok, %Acnologia.Response{}}` with `service`, `capability` and
  `tried` set, or `{:error, %Acnologia.Error{}}` such as `:bad_params`,
  `:no_route` or `:no_route_worked`.
  """
  @spec capability(client(), String.t(), map(), keyword()) :: result(Response.t())
  def capability(%Client{} = c, slug, params \\ %{}, opts \\ []) do
    query =
      []
      |> then(fn q -> if opts[:raw] == true, do: [{"_raw", "1"} | q], else: q end)
      |> then(fn q ->
        if opts[:service] in [nil, ""], do: q, else: [{"_service", to_string(opts[:service])} | q]
      end)

    with {:ok, body, ctype} <- encode_json(params) do
      url = c.base_url <> "/proxy/" <> seg(slug) <> query_string(query)
      send_proxy(c, :post, url, body, ctype, opts)
    end
  end

  @doc "Like `capability/4`, but returns the response or raises `Acnologia.Error`."
  @spec capability!(client(), String.t(), map(), keyword()) :: Response.t()
  def capability!(c, slug, params \\ %{}, opts \\ []),
    do: unwrap(capability(c, slug, params, opts))

  # forward proxy

  @doc """
  Returns how to use the forward proxy (`GET /api/v1/proxy`): its URL, the
  CA to trust, the provider hosts it answers for and the environment to
  set. Nothing in it holds the token.
  """
  @spec forward_proxy(client()) :: result(ForwardProxy.t())
  def forward_proxy(%Client{} = c) do
    with {:ok, data} <- call(c, :get, "/api/v1/proxy", nil) do
      {:ok, ForwardProxy.from_json(data)}
    end
  end

  @doc "Like `forward_proxy/1`, but returns the struct or raises `Acnologia.Error`."
  @spec forward_proxy!(client()) :: ForwardProxy.t()
  def forward_proxy!(c), do: unwrap(forward_proxy(c))

  @doc """
  Downloads the forward proxy's CA certificate as PEM text
  (`GET /api/v1/proxy/ca.pem`). The route is public: the app token is not
  sent.
  """
  @spec forward_proxy_ca(client()) :: result(String.t())
  def forward_proxy_ca(%Client{} = c) do
    text(c, "/api/v1/proxy/ca.pem", "application/x-pem-file", HTTP.public_headers(c))
  end

  @doc "Like `forward_proxy_ca/1`, but returns the PEM or raises `Acnologia.Error`."
  @spec forward_proxy_ca!(client()) :: String.t()
  def forward_proxy_ca!(c), do: unwrap(forward_proxy_ca(c))

  @doc """
  Downloads the forward proxy's CA certificate and writes it to `path`
  (default `acnologia-ca.pem`). Returns `{:ok, path}`.
  """
  @spec save_forward_proxy_ca(client(), Path.t()) :: result(Path.t())
  def save_forward_proxy_ca(%Client{} = c, path \\ "acnologia-ca.pem") do
    with {:ok, pem} <- forward_proxy_ca(c) do
      case File.write(path, pem) do
        :ok ->
          {:ok, path}

        {:error, reason} ->
          {:error,
           Error.new(:invalid_argument, "could not write #{path}: #{:file.format_error(reason)}")}
      end
    end
  end

  @doc "Like `save_forward_proxy_ca/2`, but returns the path or raises `Acnologia.Error`."
  @spec save_forward_proxy_ca!(client(), Path.t()) :: Path.t()
  def save_forward_proxy_ca!(c, path \\ "acnologia-ca.pem"),
    do: unwrap(save_forward_proxy_ca(c, path))

  @doc """
  Returns the forward proxy URL with user `app` and this client's token as
  the password, percent escaped: `http://app:acn_...@host:8888`.

  The result holds the app token. Never log it.
  """
  @spec forward_proxy_url(client()) :: result(String.t())
  def forward_proxy_url(%Client{} = c) do
    with {:ok, fp} <- forward_proxy(c), do: {:ok, proxy_url_with_token(c, fp)}
  end

  @doc "Like `forward_proxy_url/1`, but returns the URL or raises `Acnologia.Error`."
  @spec forward_proxy_url!(client()) :: String.t()
  def forward_proxy_url!(c), do: unwrap(forward_proxy_url(c))

  @doc """
  Returns the environment an app needs to send its own HTTP calls through
  the forward proxy: `HTTPS_PROXY` and `HTTP_PROXY` (the
  `forward_proxy_url/1`), `NODE_USE_ENV_PROXY` set to `1`, and
  `NODE_EXTRA_CA_CERTS`, `REQUESTS_CA_BUNDLE` and `SSL_CERT_FILE` set to
  `ca_path`. Save the CA there with `save_forward_proxy_ca/2`.

  The result holds the app token. Never log it.
  """
  @spec forward_proxy_env(client(), Path.t()) :: result(%{String.t() => String.t()})
  def forward_proxy_env(%Client{} = c, ca_path \\ "acnologia-ca.pem") do
    with {:ok, url} <- forward_proxy_url(c) do
      {:ok,
       %{
         "HTTPS_PROXY" => url,
         "HTTP_PROXY" => url,
         "NODE_USE_ENV_PROXY" => "1",
         "NODE_EXTRA_CA_CERTS" => ca_path,
         "REQUESTS_CA_BUNDLE" => ca_path,
         "SSL_CERT_FILE" => ca_path
       }}
    end
  end

  @doc "Like `forward_proxy_env/2`, but returns the map or raises `Acnologia.Error`."
  @spec forward_proxy_env!(client(), Path.t()) :: %{String.t() => String.t()}
  def forward_proxy_env!(c, ca_path \\ "acnologia-ca.pem"),
    do: unwrap(forward_proxy_env(c, ca_path))

  defp proxy_url_with_token(%Client{token: token}, %ForwardProxy{proxy_url: url}) do
    u = URI.parse(url)
    userinfo = "app:" <> URI.encode(token, &URI.char_unreserved?/1)
    URI.to_string(%{u | userinfo: userinfo})
  end

  # helpers

  @doc """
  Replaces `secret` in `message` (raw and URL encoded) with `[key]` and cuts
  it to 500 characters. `report/4` does this for you; use it for your own
  logs. Errors from query auth services often carry the request URL with
  the key in it.
  """
  @spec scrub_message(String.t() | nil, String.t() | nil) :: String.t() | nil
  def scrub_message(nil, _secret), do: nil

  def scrub_message(message, secret) do
    message = to_string(message)

    message =
      if is_binary(secret) and secret != "" do
        message
        |> String.replace(secret, "[key]")
        |> String.replace(URI.encode_www_form(secret), "[key]")
        |> String.replace(URI.encode(secret, &URI.char_unreserved?/1), "[key]")
      else
        message
      end

    String.slice(message, 0, @max_report_message)
  end

  # A GET whose answer is text, not JSON.
  defp text(c, path, accept, headers) do
    with {:ok, {status, rh, rb}} <-
           HTTP.request(c, :get, c.base_url <> path, [{"accept", accept} | headers], nil) do
      if status < 200 or status >= 300,
        do: {:error, Error.from_response(status, rh, rb, Client.secrets(c))},
        else: {:ok, rb}
    end
  end

  defp call(c, method, path, body, opts \\ []) do
    headers = [{"accept", "application/json"} | HTTP.auth_headers(c)]

    {headers, payload} =
      if body == nil,
        do: {headers, nil},
        else: {[{"content-type", "application/json"} | headers], JSON.encode!(body)}

    with {:ok, {status, rh, rb}} <-
           HTTP.request(c, method, c.base_url <> path, headers, payload, opts) do
      cond do
        status < 200 or status >= 300 ->
          {:error, Error.from_response(status, rh, rb, Client.secrets(c))}

        rb == "" ->
          {:ok, %{}}

        true ->
          case JSON.decode(rb) do
            {:ok, %{} = data} ->
              {:ok, data}

            _ ->
              {:error,
               %Error{reason: :decode, status: status, message: "response is not a JSON object"}}
          end
      end
    end
  end

  defp method(m) when m in @methods, do: {:ok, m}

  defp method(m) when is_binary(m) do
    down = String.downcase(m)

    case Enum.find(@methods, &(Atom.to_string(&1) == down)) do
      nil -> {:error, Error.new(:invalid_argument, "unsupported method #{inspect(m)}")}
      atom -> {:ok, atom}
    end
  end

  defp method(m), do: {:error, Error.new(:invalid_argument, "unsupported method #{inspect(m)}")}

  defp query_string(nil), do: ""

  defp query_string(q) do
    case q |> HTTP.to_headers() |> URI.encode_query() do
      "" -> ""
      qs -> "?" <> qs
    end
  end

  defp seg(id), do: URI.encode(to_string(id), &URI.char_unreserved?/1)

  defp ceil_int(nil), do: nil
  defp ceil_int(n) when is_integer(n), do: n
  defp ceil_int(n) when is_float(n), do: ceil(n)
  defp ceil_int(_), do: nil

  defp put_if(map, key, value, pred) do
    if pred.(value), do: Map.put(map, key, value), else: map
  end

  defp env(name) do
    case System.get_env(name) do
      nil -> nil
      "" -> nil
      v -> v
    end
  end

  defp valid_base?(base) do
    case URI.parse(base) do
      %URI{scheme: s, host: h} when s in ["http", "https"] and is_binary(h) and h != "" -> true
      _ -> false
    end
  end

  defp unwrap({:ok, v}), do: v
  defp unwrap({:error, %Error{} = e}), do: raise(e)
end
