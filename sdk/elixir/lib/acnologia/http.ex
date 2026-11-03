defmodule Acnologia.HTTP do
  @moduledoc false
  # A thin wrapper over :httpc. Redirects are not followed, so the app token
  # is never replayed to another host. HTTPS verifies the server certificate
  # against the operating system's trust store.

  alias Acnologia.{Client, Error}

  @user_agent "acnologia-elixir/0.1.0"

  @type headers :: [{String.t(), String.t()}]

  @doc false
  def user_agent, do: @user_agent

  @doc false
  # The headers Acnologia needs on every call.
  @spec auth_headers(Client.t()) :: headers()
  def auth_headers(%Client{} = c) do
    base = [{"authorization", "Bearer " <> c.token}, {"user-agent", @user_agent}]

    if c.access_password in [nil, ""],
      do: base,
      else: base ++ [{"x-acnologia-access", c.access_password}]
  end

  @doc false
  @spec request(Client.t(), atom(), String.t(), headers(), iodata() | nil, keyword()) ::
          {:ok, {non_neg_integer(), headers(), binary()}} | {:error, Error.t()}
  def request(%Client{} = c, method, url, headers, body, opts \\ []) do
    timeout = Keyword.get(opts, :timeout, c.timeout)
    {content_type, headers} = pop_header(headers, "content-type")

    content_type =
      if body != nil or method in [:post, :put, :patch],
        do: content_type || "application/octet-stream"

    body = if body == nil and content_type, do: "", else: body
    body = if body == nil, do: nil, else: IO.iodata_to_binary(body)

    result =
      try do
        case adapter() do
          :httpc -> httpc(c, method, url, headers, content_type, body, timeout)
          :raw -> raw(c, method, url, headers, content_type, body, timeout)
        end
      catch
        kind, reason -> {:error, {kind, reason}}
      end

    case result do
      {:ok, {status, resp_headers, resp_body}} ->
        {:ok, {status, resp_headers, resp_body}}

      {:error, :timeout} ->
        {:error, Error.new(:timeout, "request timed out after #{timeout}ms")}

      {:error, reason} ->
        {:error,
         Error.new(
           :transport,
           "could not reach the Acnologia server: " <> inspect(reason),
           Client.secrets(c)
         )}
    end
  end

  # :httpc retries a 503 that carries Retry-After by itself, which would hide
  # Acnologia's no_key_available and service_unavailable answers. The
  # autoretry option that turns this off came with inets 9.6. On older OTP
  # releases the requests go through Acnologia.HTTP.Raw instead.
  @doc false
  @spec adapter() :: :httpc | :raw
  def adapter do
    case Application.get_env(:acnologia, :http_adapter) do
      a when a in [:httpc, :raw] -> a
      _ -> if httpc_autoretry?(), do: :httpc, else: :raw
    end
  end

  defp httpc_autoretry? do
    case :persistent_term.get({__MODULE__, :autoretry}, nil) do
      nil ->
        _ = Application.load(:inets)
        vsn = :inets |> Application.spec(:vsn) |> to_string()
        parts = vsn |> String.split(".") |> Enum.map(&Integer.parse/1)

        ok =
          case parts do
            [{major, _}, {minor, _} | _] -> {major, minor} >= {9, 6}
            [{major, _}] -> major > 9
            _ -> false
          end

        :persistent_term.put({__MODULE__, :autoretry}, ok)
        ok

      ok ->
        ok
    end
  end

  defp httpc(c, method, url, headers, content_type, body, timeout) do
    hdrs = Enum.map(headers, fn {k, v} -> {to_charlist(k), to_charlist(v)} end)
    url_cl = to_charlist(url)

    req =
      if content_type,
        do: {url_cl, hdrs, to_charlist(content_type), body},
        else: {url_cl, hdrs}

    http_opts =
      [
        timeout: timeout,
        connect_timeout: c.connect_timeout,
        autoredirect: false,
        autoretry: 0
      ] ++ ssl_opts(c, url)

    case :httpc.request(method, req, http_opts, body_format: :binary) do
      {:ok, {{_, status, _}, resp_headers, resp_body}} ->
        {:ok, {status, normalize(resp_headers), IO.iodata_to_binary(resp_body)}}

      {:error, _} = err ->
        err
    end
  end

  defp raw(c, method, url, headers, content_type, body, timeout) do
    headers = if content_type, do: headers ++ [{"content-type", content_type}], else: headers
    ssl = if String.starts_with?(url, "https:"), do: c.ssl || default_ssl(), else: []

    Acnologia.HTTP.Raw.request(method, url, headers, body,
      timeout: timeout,
      connect_timeout: c.connect_timeout,
      ssl: ssl
    )
  end

  defp ssl_opts(%Client{ssl: ssl}, url) do
    if String.starts_with?(url, "https:") do
      [ssl: ssl || default_ssl()]
    else
      []
    end
  end

  defp default_ssl do
    [
      verify: :verify_peer,
      cacerts: :public_key.cacerts_get(),
      depth: 4,
      customize_hostname_check: [
        match_fun: :public_key.pkix_verify_hostname_match_fun(:https)
      ]
    ]
  end

  defp normalize(headers) do
    Enum.map(headers, fn {k, v} ->
      {k |> to_string() |> String.downcase(), to_string(v)}
    end)
  end

  @doc false
  @spec pop_header(headers(), String.t()) :: {String.t() | nil, headers()}
  def pop_header(headers, name) do
    {found, rest} = Enum.split_with(headers, fn {k, _} -> String.downcase(k) == name end)

    case found do
      [] -> {nil, rest}
      list -> {list |> List.last() |> elem(1), rest}
    end
  end

  @doc false
  # Turns a map or keyword list of headers into a list of string pairs.
  @spec to_headers(Enumerable.t() | nil) :: headers()
  def to_headers(nil), do: []

  def to_headers(headers) do
    Enum.map(headers, fn {k, v} -> {to_string(k), to_string(v)} end)
  end

  @doc false
  # Sets headers, replacing any with the same name (case insensitive).
  @spec put_headers(headers(), headers()) :: headers()
  def put_headers(headers, new) do
    names = MapSet.new(new, fn {k, _} -> String.downcase(k) end)
    Enum.reject(headers, fn {k, _} -> MapSet.member?(names, String.downcase(k)) end) ++ new
  end
end
