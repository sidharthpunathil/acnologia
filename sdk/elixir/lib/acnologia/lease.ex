defmodule Acnologia.Lease do
  @moduledoc """
  A key borrowed with `Acnologia.lease/3`.

  `auth` says where the provider expects the key: `type` is `"query"`,
  `"header"` or `"bearer"`, `name` is the parameter or header name, and a
  non empty `prefix` goes in front of the secret (for `"bearer"` it replaces
  the default `"Bearer "`). `apply_headers/2`, `apply_query/2` and `url/3`
  put the secret in the right place for you.

  Report every lease with `Acnologia.report/4` when you are done, and extend
  one a long job still needs with `Acnologia.renew/3`.

  `inspect/1` never shows the secret.
  """

  @derive {Inspect, except: [:secret]}
  defstruct lease_id: "",
            key_id: 0,
            service: "",
            secret: "",
            auth: %{type: "header", name: "", prefix: ""},
            base_url: "",
            expires_at: nil,
            exclusive: false

  @type auth :: %{type: String.t(), name: String.t(), prefix: String.t()}

  @type t :: %__MODULE__{
          lease_id: String.t(),
          key_id: integer(),
          service: String.t(),
          secret: String.t(),
          auth: auth(),
          base_url: String.t(),
          expires_at: DateTime.t() | nil,
          exclusive: boolean()
        }

  @doc false
  @spec from_json(map()) :: t()
  def from_json(data) when is_map(data) do
    auth = if is_map(data["auth"]), do: data["auth"], else: %{}

    %__MODULE__{
      lease_id: str(data["lease_id"]),
      key_id: if(is_integer(data["key_id"]), do: data["key_id"], else: 0),
      service: str(data["service"]),
      secret: str(data["secret"]),
      auth: %{
        type: if(str(auth["type"]) == "", do: "header", else: str(auth["type"])),
        name: str(auth["name"]),
        prefix: str(auth["prefix"])
      },
      base_url: data["base_url"] |> str() |> String.trim_trailing("/"),
      expires_at: Acnologia.Error.parse_time(data["expires_at"]),
      exclusive: data["exclusive"] == true
    }
  end

  @doc """
  Returns `headers` with the secret added when the key goes in a header
  (`"header"` or `"bearer"` auth). For `"query"` auth the headers come back
  unchanged; use `apply_query/2` or `url/3`.

  Takes and returns a list of `{name, value}` string pairs; a map or keyword
  list is accepted too. An existing header with the same name is replaced.

      iex> lease = %Acnologia.Lease{secret: "sk-1", auth: %{type: "header", name: "X-Api-Key", prefix: ""}}
      iex> Acnologia.Lease.apply_headers(lease, [{"accept", "application/json"}])
      [{"accept", "application/json"}, {"X-Api-Key", "sk-1"}]
  """
  @spec apply_headers(t(), Enumerable.t() | nil) :: [{String.t(), String.t()}]
  def apply_headers(%__MODULE__{} = lease, headers \\ []) do
    headers = Acnologia.HTTP.to_headers(headers)

    case lease.auth.type do
      "query" ->
        headers

      "bearer" ->
        name = default(lease.auth.name, "Authorization")
        value = if lease.auth.prefix == "", do: "Bearer " <> lease.secret, else: value(lease)
        Acnologia.HTTP.put_headers(headers, [{name, value}])

      _ ->
        name = default(lease.auth.name, "Authorization")
        Acnologia.HTTP.put_headers(headers, [{name, value(lease)}])
    end
  end

  @doc """
  Returns `query` with the secret added when the key goes in the query
  string (`"query"` auth). For other auth types the query comes back
  unchanged; use `apply_headers/2`.

  Takes and returns a list of `{name, value}` string pairs; a map or keyword
  list is accepted too.

      iex> lease = %Acnologia.Lease{secret: "sk-1", auth: %{type: "query", name: "api_key", prefix: ""}}
      iex> Acnologia.Lease.apply_query(lease, q: "hello")
      [{"q", "hello"}, {"api_key", "sk-1"}]
  """
  @spec apply_query(t(), Enumerable.t() | nil) :: [{String.t(), String.t()}]
  def apply_query(%__MODULE__{} = lease, query \\ []) do
    query = Acnologia.HTTP.to_headers(query)

    case lease.auth.type do
      "query" ->
        name = default(lease.auth.name, "api_key")
        Enum.reject(query, fn {k, _} -> k == name end) ++ [{name, value(lease)}]

      _ ->
        query
    end
  end

  @doc """
  Joins the service's `base_url` with `path` and the query string, adding
  the secret to the query for `"query"` auth.

      iex> lease = %Acnologia.Lease{secret: "sk-1", base_url: "https://agi-labs.example", auth: %{type: "query", name: "key", prefix: ""}}
      iex> Acnologia.Lease.url(lease, "/v1/chat", model: "small")
      "https://agi-labs.example/v1/chat?model=small&key=sk-1"
  """
  @spec url(t(), String.t(), Enumerable.t() | nil) :: String.t()
  def url(%__MODULE__{} = lease, path \\ "", query \\ []) do
    q = apply_query(lease, query)
    p = if path == "" or String.starts_with?(path, "/"), do: path, else: "/" <> path
    qs = URI.encode_query(q)
    lease.base_url <> p <> if(qs == "", do: "", else: "?" <> qs)
  end

  defp value(lease), do: lease.auth.prefix <> lease.secret
  defp default("", d), do: d
  defp default(v, _), do: v
  defp str(v) when is_binary(v), do: v
  defp str(_), do: ""
end
