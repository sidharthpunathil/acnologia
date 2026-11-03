defmodule Acnologia.Error do
  @moduledoc """
  An error from the Acnologia server, or from talking to it.

  Every function in `Acnologia` returns `{:error, %Acnologia.Error{}}` on
  failure, and the bang variants raise it.

  `reason` is an atom you can match on. For an answer from the server it
  comes from the error code and the HTTP status, the same way the other
  SDKs map them:

    * a known code becomes its own atom: `:unauthenticated`,
      `:service_not_allowed`, `:proxy_not_allowed`, `:lease_not_allowed`,
      `:lease_revoked`, `:no_key_available`, `:all_keys_busy`,
      `:service_unavailable`, `:empty_group`, `:app_limit_reached`,
      `:share_revoked`, `:share_paused`, `:share_expired`,
      `:lease_not_found`, `:report_failed`, `:unknown_service`,
      `:upstream_unreachable`, `:upstream_blocked`, `:loop_detected`,
      `:bad_path`, `:bad_body`, `:body_too_large` and `:method_not_allowed`
    * a 503 with no code is `:no_key_available`
    * any other 401 is `:unauthenticated` and any other 403 is `:forbidden`
    * everything else is `:api`; `status` and `code` still say what happened

  Errors that never reached a server answer use `:transport` (connection
  failed), `:timeout`, `:decode` (the answer was not the JSON expected),
  `:config` (no base URL or token) and `:invalid_argument`.

  `retry_after` is in seconds. It comes from `retry_after_sec` in the body,
  or else from the `Retry-After` header. `limit` and `reset_at` are set for
  `:app_limit_reached`.

  The message never holds the app token, the access password or a leased
  secret.
  """

  @type reason ::
          :unauthenticated
          | :forbidden
          | :service_not_allowed
          | :proxy_not_allowed
          | :lease_not_allowed
          | :lease_revoked
          | :no_key_available
          | :all_keys_busy
          | :service_unavailable
          | :empty_group
          | :app_limit_reached
          | :share_revoked
          | :share_paused
          | :share_expired
          | :lease_not_found
          | :report_failed
          | :unknown_service
          | :upstream_unreachable
          | :upstream_blocked
          | :loop_detected
          | :bad_path
          | :bad_body
          | :body_too_large
          | :method_not_allowed
          | :api
          | :transport
          | :timeout
          | :decode
          | :config
          | :invalid_argument
          | :attempts_exhausted

  @type t :: %__MODULE__{
          reason: reason(),
          status: non_neg_integer() | nil,
          code: String.t(),
          message: String.t(),
          retry_after: number() | nil,
          limit: String.t() | nil,
          reset_at: DateTime.t() | nil
        }

  defexception reason: :api,
               status: nil,
               code: "",
               message: "",
               retry_after: nil,
               limit: nil,
               reset_at: nil

  @codes Map.new(
           ~w(unauthenticated service_not_allowed proxy_not_allowed lease_not_allowed
              lease_revoked no_key_available all_keys_busy service_unavailable empty_group
              app_limit_reached share_revoked share_paused share_expired lease_not_found
              report_failed unknown_service upstream_unreachable upstream_blocked
              loop_detected bad_path bad_body body_too_large method_not_allowed)a,
           &{Atom.to_string(&1), &1}
         )

  @impl true
  def message(%__MODULE__{} = e) do
    label =
      cond do
        e.status && e.code != "" -> "http #{e.status}: #{e.code}"
        e.status -> "http #{e.status}"
        true -> Atom.to_string(e.reason)
      end

    retry =
      if is_number(e.retry_after) and e.retry_after > 0,
        do: " (retry after #{format_seconds(e.retry_after)}s)",
        else: ""

    limit = if e.limit, do: " [limit #{e.limit}]", else: ""
    msg = if e.message in [nil, ""], do: "", else: ": " <> e.message
    "acnologia: " <> label <> limit <> retry <> msg
  end

  @doc "True for `:no_key_available` and `:all_keys_busy`: no key could be handed out right now."
  @spec no_key?(t()) :: boolean()
  def no_key?(%__MODULE__{reason: r}), do: r in [:no_key_available, :all_keys_busy]

  @doc "True when every usable key is at its concurrency limit (`:all_keys_busy`). It clears as soon as another caller reports."
  @spec busy?(t()) :: boolean()
  def busy?(%__MODULE__{reason: r}), do: r == :all_keys_busy

  @doc """
  True when the same call may work later without any change: no key, all
  keys busy, service unavailable, app limit reached, timeout or transport.
  Wait `retry_after` seconds first when it is set.
  """
  @spec retryable?(t()) :: boolean()
  def retryable?(%__MODULE__{reason: r}),
    do:
      r in [
        :no_key_available,
        :all_keys_busy,
        :service_unavailable,
        :app_limit_reached,
        :timeout,
        :transport
      ]

  @doc false
  @spec new(reason(), String.t(), [String.t() | nil]) :: t()
  def new(reason, message, secrets \\ []) do
    %__MODULE__{reason: reason, message: scrub(message, secrets)}
  end

  @doc false
  # Builds the error for a non-2xx answer from Acnologia itself.
  @spec from_response(non_neg_integer(), [{String.t(), String.t()}], binary(), [
          String.t() | nil
        ]) :: t()
  def from_response(status, headers, body, secrets \\ []) do
    data =
      case JSON.decode(body || "") do
        {:ok, map} when is_map(map) -> map
        _ -> %{}
      end

    {code, message} =
      case data["error"] do
        %{} = err -> {string(err["code"]), string(err["message"])}
        err when is_binary(err) -> {"", err}
        _ -> {"", ""}
      end

    message =
      if code == "" and message == "" do
        text = body |> to_string() |> String.trim() |> String.slice(0, 200)
        if text == "", do: "HTTP #{status}", else: text
      else
        message
      end

    retry_after =
      case data["retry_after_sec"] do
        n when is_number(n) and n > 0 -> n
        _ -> header_retry_after(headers)
      end

    %__MODULE__{
      reason: reason_for(status, code),
      status: status,
      code: code,
      message: scrub(message, secrets),
      retry_after: retry_after,
      limit: if(is_binary(data["limit"]) and data["limit"] != "", do: data["limit"]),
      reset_at: parse_time(data["reset_at"])
    }
  end

  @doc false
  @spec reason_for(non_neg_integer(), String.t()) :: reason()
  def reason_for(status, code) do
    case Map.fetch(@codes, code) do
      {:ok, reason} ->
        reason

      :error ->
        cond do
          status == 503 and code == "" -> :no_key_available
          status == 401 -> :unauthenticated
          status == 403 -> :forbidden
          true -> :api
        end
    end
  end

  @doc false
  # Replaces every secret with [redacted], raw and URL encoded.
  @spec scrub(String.t(), [String.t() | nil]) :: String.t()
  def scrub(message, secrets) do
    Enum.reduce(secrets, to_string(message), fn
      s, acc when is_binary(s) and byte_size(s) > 0 ->
        acc
        |> String.replace(s, "[redacted]")
        |> String.replace(URI.encode_www_form(s), "[redacted]")
        |> String.replace(URI.encode(s, &URI.char_unreserved?/1), "[redacted]")

      _, acc ->
        acc
    end)
  end

  @doc false
  @spec parse_time(term()) :: DateTime.t() | nil
  def parse_time(value) when is_binary(value) do
    case DateTime.from_iso8601(value) do
      {:ok, dt, _} -> dt
      _ -> nil
    end
  end

  def parse_time(_), do: nil

  defp header_retry_after(headers) do
    case List.keyfind(headers, "retry-after", 0) do
      {_, value} ->
        case Float.parse(String.trim(value)) do
          {n, ""} when n > 0 -> if n == trunc(n), do: trunc(n), else: n
          _ -> nil
        end

      nil ->
        nil
    end
  end

  defp string(v) when is_binary(v), do: v
  defp string(_), do: ""

  defp format_seconds(n) when is_integer(n), do: Integer.to_string(n)

  defp format_seconds(n) when is_float(n) do
    if n == trunc(n), do: Integer.to_string(trunc(n)), else: Float.to_string(n)
  end
end
