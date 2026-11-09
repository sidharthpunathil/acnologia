defmodule Acnologia.Response do
  @moduledoc """
  A provider's answer relayed by the proxy, returned by `Acnologia.proxy/5`.

  The status, headers and body are the provider's, as they are, including a
  non 2xx status. Header names are lower case. Acnologia adds its own
  headers, which are also read into fields:

    * `key_id` from `X-Acnologia-Key-Id`: the pool key that served the call
    * `attempts` from `X-Acnologia-Attempts`: how many keys were tried
    * `service` from `X-Acnologia-Service`: the service that answered
      (useful for a service group)
    * `cache` from `X-Acnologia-Cache`: `"hit"`, `"miss"` or `"bypass"`
      when caching applies
    * `replayed` from `Idempotent-Replayed: true`: a stored answer to an
      earlier call with the same `Idempotency-Key`
    * `capability` from `X-Acnologia-Capability`: the capability that ran,
      for a capability call
    * `tried` from `X-Acnologia-Tried`: the routes a capability call tried,
      in order, as `[%{service: slug, status: integer}]`

  `key_id` and `attempts` are nil for a share's token. `acnologia` holds
  every `x-acnologia-*` header by its lower case name.
  """

  defstruct status: 0,
            headers: [],
            body: "",
            key_id: nil,
            attempts: nil,
            service: nil,
            cache: nil,
            replayed: false,
            capability: nil,
            tried: [],
            acnologia: %{}

  @type t :: %__MODULE__{
          status: non_neg_integer(),
          headers: [{String.t(), String.t()}],
          body: binary(),
          key_id: integer() | nil,
          attempts: integer() | nil,
          service: String.t() | nil,
          cache: String.t() | nil,
          replayed: boolean(),
          capability: String.t() | nil,
          tried: [%{service: String.t(), status: integer()}],
          acnologia: %{optional(String.t()) => String.t()}
        }

  @doc false
  @spec new(non_neg_integer(), [{String.t(), String.t()}], binary()) :: t()
  def new(status, headers, body) do
    acn = for {k, v} <- headers, String.starts_with?(k, "x-acnologia-"), into: %{}, do: {k, v}

    %__MODULE__{
      status: status,
      headers: headers,
      body: body,
      key_id: int(acn["x-acnologia-key-id"]),
      attempts: int(acn["x-acnologia-attempts"]),
      service: acn["x-acnologia-service"],
      cache: acn["x-acnologia-cache"],
      replayed: header(headers, "idempotent-replayed") == "true",
      capability: acn["x-acnologia-capability"],
      tried: parse_tried(acn["x-acnologia-tried"]),
      acnologia: acn
    }
  end

  @doc "True for a 2xx status."
  @spec success?(t()) :: boolean()
  def success?(%__MODULE__{status: s}), do: s >= 200 and s < 300

  @doc "The first value of a header, by name in any case, or nil."
  @spec header(t() | [{String.t(), String.t()}], String.t()) :: String.t() | nil
  def header(%__MODULE__{headers: h}, name), do: header(h, name)

  def header(headers, name) when is_list(headers) do
    name = String.downcase(name)

    Enum.find_value(headers, fn {k, v} -> if String.downcase(k) == name, do: v end)
  end

  @doc "Decodes the body as JSON."
  @spec json(t()) :: {:ok, term()} | {:error, Acnologia.Error.t()}
  def json(%__MODULE__{body: body}) do
    case JSON.decode(body) do
      {:ok, value} -> {:ok, value}
      {:error, _} -> {:error, Acnologia.Error.new(:decode, "response body is not JSON")}
    end
  end

  @doc """
  Parses `X-Acnologia-Tried` (`service=status` pairs, comma separated) into
  `[%{service: slug, status: integer}]`. A status that is not a number is 0.
  """
  @spec parse_tried(String.t() | nil) :: [%{service: String.t(), status: integer()}]
  def parse_tried(nil), do: []

  def parse_tried(value) do
    for part <- String.split(value, ","), part = String.trim(part), part != "" do
      case :binary.matches(part, "=") do
        [] ->
          %{service: part, status: 0}

        matches ->
          {pos, _} = List.last(matches)
          service = part |> binary_part(0, pos) |> String.trim()
          status = part |> binary_part(pos + 1, byte_size(part) - pos - 1) |> String.trim()
          %{service: service, status: int(status) || 0}
      end
    end
  end

  defp int(nil), do: nil

  defp int(v) do
    case Integer.parse(v) do
      {n, ""} -> n
      _ -> nil
    end
  end
end
